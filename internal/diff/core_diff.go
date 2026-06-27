package diff

import (
	"context"
	"strings"

	allowedext "github.com/open-code-review/open-code-review/internal/config/allowlist"
	"github.com/open-code-review/open-code-review/internal/gitcmd"
	"github.com/open-code-review/open-code-review/internal/llm"
	"github.com/open-code-review/open-code-review/internal/model"
)

// Core exclude-reason values, kept string-stable for the JSON contract consumed
// by the `ocr core diff` command. They mirror internal/agent's ExcludeReason
// values; they are duplicated here (not imported) because internal/agent imports
// internal/diff, so the reverse import would be a cycle.
const (
	coreExcludeBinary      = "binary"
	coreExcludeExtension   = "unsupported_ext"
	coreExcludeDefaultPath = "default_path"
	coreExcludeDeleted     = "deleted"
	coreExcludeLargeDiff   = "large_diff"
)

// CoreHunkLine is one line within a hunk, serialized with a string type so the
// JSON contract is self-describing (unlike the internal int enum HunkLineType).
type CoreHunkLine struct {
	Type    string `json:"type"` // "context" | "added" | "deleted"
	Content string `json:"content"`
}

// CoreHunk is a JSON-tagged projection of Hunk for the `ocr core diff` output.
type CoreHunk struct {
	OldStart int            `json:"old_start"`
	OldCount int            `json:"old_count"`
	NewStart int            `json:"new_start"`
	NewCount int            `json:"new_count"`
	Lines    []CoreHunkLine `json:"lines"`
}

// CoreFileEntry is one file's record in the `ocr core diff` output. Diff body,
// new file content, and hunk maps are populated only for reviewable files to
// keep the payload lean.
type CoreFileEntry struct {
	Path           string     `json:"path"`
	OldPath        string     `json:"old_path,omitempty"`
	Status         string     `json:"status"`
	Insertions     int64      `json:"insertions"`
	Deletions      int64      `json:"deletions"`
	ChangedLines   int64      `json:"changed_lines"`
	WillReview     bool       `json:"will_review"`
	ExcludeReason  string     `json:"exclude_reason,omitempty"`
	Diff           string     `json:"diff,omitempty"`
	NewFileContent string     `json:"new_file_content,omitempty"`
	Hunks          []CoreHunk `json:"hunks,omitempty"`
}

// CoreDiffResult is the full `ocr core diff` JSON payload.
type CoreDiffResult struct {
	Files           []CoreFileEntry `json:"files"`
	TotalFiles      int             `json:"total_files"`
	ReviewableCount int             `json:"reviewable_count"`
	ExcludedCount   int             `json:"excluded_count"`
}

// CoreDiffOptions selects the diff mode and filtering behavior. Exactly one of
// the modes is chosen by precedence: Commit, then From+To, then workspace.
type CoreDiffOptions struct {
	RepoDir string
	From    string
	To      string
	Commit  string
	// MaxTokens, when > 0, enables the large-diff pre-filter: any file whose diff
	// body alone exceeds MaxTokens*4/5 tokens is excluded with reason "large_diff".
	// Callers pass the same MAX_TOKENS value `ocr review` uses so both paths review
	// the same file set. When <= 0 the filter is disabled.
	MaxTokens int
	Runner    *gitcmd.Runner
}

// ComputeCoreDiff loads diffs via the same providers `ocr review` uses and
// returns a serializable result with filtering applied. It performs no LLM calls.
func ComputeCoreDiff(ctx context.Context, opts CoreDiffOptions) (*CoreDiffResult, error) {
	runner := opts.Runner
	if runner == nil {
		runner = gitcmd.New(0)
	}

	var provider *Provider
	switch {
	case opts.Commit != "":
		provider = NewCommitProvider(opts.RepoDir, opts.Commit, runner)
	case opts.From != "" && opts.To != "":
		provider = NewProvider(opts.RepoDir, opts.From, opts.To, runner)
	default:
		provider = NewWorkspaceProvider(opts.RepoDir, runner)
	}

	parsed, err := provider.GetDiff(ctx)
	if err != nil {
		return nil, err
	}

	return buildCoreDiffResult(parsed, opts.MaxTokens), nil
}

// buildCoreDiffResult applies the review filters to already-parsed diffs and
// assembles the output. It is pure (no git, no network) so it can be unit-tested
// directly with synthetic model.Diff slices.
func buildCoreDiffResult(parsed []model.Diff, maxTokens int) *CoreDiffResult {
	limit := 0
	if maxTokens > 0 {
		limit = maxTokens * 4 / 5
	}

	res := &CoreDiffResult{TotalFiles: len(parsed)}
	for i := range parsed {
		d := parsed[i]
		path := coreEffectivePath(d)

		entry := CoreFileEntry{
			Path:         path,
			Status:       coreStatus(d),
			Insertions:   d.Insertions,
			Deletions:    d.Deletions,
			ChangedLines: d.Insertions + d.Deletions,
		}
		// Only surface old_path for renames — for deletions the effective path is
		// already OldPath, so emitting it again would duplicate `path`.
		if d.OldPath != "" && d.OldPath != "/dev/null" && d.OldPath != entry.Path {
			entry.OldPath = d.OldPath
		}

		reason := coreWhyExcluded(d)
		if reason == "" && limit > 0 && llm.CountTokens(d.Diff) > limit {
			reason = coreExcludeLargeDiff
		}

		entry.WillReview = reason == ""
		entry.ExcludeReason = reason

		if entry.WillReview {
			entry.Diff = d.Diff
			entry.NewFileContent = d.NewFileContent
			entry.Hunks = toCoreHunks(ParseHunks(d.Diff))
			res.ReviewableCount++
		} else {
			res.ExcludedCount++
		}

		res.Files = append(res.Files, entry)
	}
	return res
}

// coreWhyExcluded mirrors internal/agent's whyExcluded (binary, extension,
// default exclude path, deleted) without the user-configured FileFilter, which
// the core command does not currently accept.
func coreWhyExcluded(d model.Diff) string {
	if d.IsBinary {
		return coreExcludeBinary
	}
	path := coreEffectivePath(d)
	ext := coreExtFromPath(path)
	if ext != "" && !allowedext.IsAllowedExt(ext) {
		return coreExcludeExtension
	}
	if allowedext.IsExcludedPath(path) {
		return coreExcludeDefaultPath
	}
	if d.IsDeleted {
		return coreExcludeDeleted
	}
	return ""
}

func toCoreHunks(hunks []Hunk) []CoreHunk {
	if len(hunks) == 0 {
		return nil
	}
	out := make([]CoreHunk, 0, len(hunks))
	for _, h := range hunks {
		ch := CoreHunk{
			OldStart: h.OldStart,
			OldCount: h.OldCount,
			NewStart: h.NewStart,
			NewCount: h.NewCount,
		}
		for _, l := range h.Lines {
			ch.Lines = append(ch.Lines, CoreHunkLine{
				Type:    hunkLineTypeString(l.Type),
				Content: l.Content,
			})
		}
		out = append(out, ch)
	}
	return out
}

func hunkLineTypeString(t HunkLineType) string {
	switch t {
	case HunkAdded:
		return "added"
	case HunkDeleted:
		return "deleted"
	default:
		return "context"
	}
}

// coreEffectivePath mirrors internal/agent's effectivePath.
func coreEffectivePath(d model.Diff) string {
	if d.NewPath == "/dev/null" {
		return d.OldPath
	}
	return d.NewPath
}

// coreStatus mirrors internal/agent's diffStatus.
func coreStatus(d model.Diff) string {
	switch {
	case d.IsBinary:
		return "binary"
	case d.IsNew:
		return "added"
	case d.IsDeleted:
		return "deleted"
	case d.IsRenamed:
		return "renamed"
	case d.OldPath != d.NewPath && d.OldPath != "" && d.OldPath != "/dev/null":
		return "renamed"
	default:
		return "modified"
	}
}

// coreExtFromPath mirrors internal/agent's extFromPath: extension with leading
// dot, lowercased; empty when none.
func coreExtFromPath(path string) string {
	basename := path
	if idx := strings.LastIndex(path, "/"); idx >= 0 {
		basename = path[idx+1:]
	}
	dot := strings.LastIndex(basename, ".")
	if dot <= 0 {
		return ""
	}
	return strings.ToLower(basename[dot:])
}
