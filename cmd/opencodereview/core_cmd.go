package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/open-code-review/open-code-review/internal/config/rules"
	"github.com/open-code-review/open-code-review/internal/config/template"
	"github.com/open-code-review/open-code-review/internal/diff"
	"github.com/open-code-review/open-code-review/internal/model"
)

// Input-size guards for the stdin-driven core subcommands. LLM-generated input
// is untrusted, so bound it before parsing (R15).
const (
	maxCoreStdinBytes   = 16 << 20 // 16 MiB total stdin payload
	maxCoreFieldBytes   = 1 << 20  // 1 MiB per individual string field
	maxCoreCommentCount = 2000     // max comments accepted by `ocr core emit`
)

// runCore dispatches the `ocr core` command group: LLM-free, deterministic
// building blocks meant to be orchestrated by an external brain (e.g. a Claude
// Code skill). No core subcommand performs an LLM or network call.
func runCore(args []string) error {
	if len(args) == 0 {
		printCoreUsage()
		return nil
	}
	switch args[0] {
	case "diff":
		return runCoreDiff(args[1:])
	case "relocate":
		return runCoreRelocate(args[1:])
	case "emit":
		return runCoreEmit(args[1:])
	case "rule":
		return runCoreRule(args[1:])
	case "prompt":
		return runCorePrompt(args[1:])
	case "-h", "--help":
		printCoreUsage()
		return nil
	default:
		return fmt.Errorf("unknown core sub-command: %s\nRun 'ocr core -h' for usage", args[0])
	}
}

func runCoreDiff(args []string) error {
	a := newOcrFlagSet("ocr core diff")
	var repoDir, from, to, commit string
	var maxTokens int
	a.StringVar(&repoDir, "repo", "", "root directory of the git repository (default: current dir)")
	a.StringVar(&from, "from", "", "source ref to start diff from (e.g., 'main')")
	a.StringVar(&to, "to", "", "target ref to end diff at (e.g., 'feature-branch')")
	a.StringVarP(&commit, "commit", "c", "", "single commit hash or tag to review (vs its parent)")
	a.IntVar(&maxTokens, "max-tokens", 0, "large-diff filter base (0 = use template MAX_TOKENS)")
	if err := a.Parse(args); err != nil {
		return err
	}
	if a.showHelp {
		printCoreUsage()
		return nil
	}

	if (from != "" || to != "") && commit != "" {
		return fmt.Errorf("only one diff mode allowed (--from/--to or --commit)")
	}
	if from != "" && to == "" {
		return fmt.Errorf("--to is required when --from is specified")
	}
	if to != "" && from == "" {
		return fmt.Errorf("--from is required when --to is specified")
	}

	resolvedRepo, err := resolveRepoDir(repoDir)
	if err != nil {
		return err
	}

	if maxTokens <= 0 {
		// Match `ocr review`'s large-diff pre-filter by reusing the template's
		// MAX_TOKENS so both paths review the same file set.
		if tpl, terr := template.LoadDefault(); terr == nil {
			maxTokens = tpl.MaxTokens
		}
	}

	result, err := diff.ComputeCoreDiff(context.Background(), diff.CoreDiffOptions{
		RepoDir:   resolvedRepo,
		From:      from,
		To:        to,
		Commit:    commit,
		MaxTokens: maxTokens,
	})
	if err != nil {
		return fmt.Errorf("compute diff: %w", err)
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(result)
}

// coreRelocateInput is the stdin payload for `ocr core relocate`.
type coreRelocateInput struct {
	Diff           string `json:"diff"`
	NewFileContent string `json:"new_file_content"`
	Comment        struct {
		Content      string `json:"content"`
		ExistingCode string `json:"existing_code"`
	} `json:"comment"`
}

// coreRelocateOutput is the stdout result of `ocr core relocate`.
type coreRelocateOutput struct {
	StartLine int  `json:"start_line"`
	EndLine   int  `json:"end_line"`
	Matched   bool `json:"matched"`
}

func runCoreRelocate(args []string) error {
	a := newOcrFlagSet("ocr core relocate")
	if err := a.Parse(args); err != nil {
		return err
	}
	if a.showHelp {
		printCoreUsage()
		return nil
	}

	raw, err := readCoreStdin()
	if err != nil {
		return err
	}

	out, err := relocateFromJSON(raw)
	if err != nil {
		return err
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

// relocateFromJSON parses and validates a relocate payload and runs the
// deterministic resolver. Split out from stdin handling so it can be unit-tested.
func relocateFromJSON(raw []byte) (coreRelocateOutput, error) {
	var in coreRelocateInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return coreRelocateOutput{}, fmt.Errorf("parse stdin JSON: %w", err)
	}

	for label, field := range map[string]string{
		"diff":                  in.Diff,
		"new_file_content":      in.NewFileContent,
		"comment.existing_code": in.Comment.ExistingCode,
		"comment.content":       in.Comment.Content,
	} {
		if len(field) > maxCoreFieldBytes {
			return coreRelocateOutput{}, fmt.Errorf("field %q exceeds max length (%d bytes)", label, maxCoreFieldBytes)
		}
	}

	cm := model.LlmComment{
		Content:      in.Comment.Content,
		ExistingCode: in.Comment.ExistingCode,
	}
	d := model.Diff{
		Diff:           in.Diff,
		NewFileContent: in.NewFileContent,
	}

	matched := diff.ResolveComment(&cm, &d)
	return coreRelocateOutput{
		StartLine: cm.StartLine,
		EndLine:   cm.EndLine,
		Matched:   matched,
	}, nil
}

// readCoreStdin reads stdin up to maxCoreStdinBytes, rejecting larger payloads.
func readCoreStdin() ([]byte, error) {
	raw, err := io.ReadAll(io.LimitReader(os.Stdin, maxCoreStdinBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read stdin: %w", err)
	}
	if len(raw) > maxCoreStdinBytes {
		return nil, fmt.Errorf("stdin payload exceeds max size (%d bytes)", maxCoreStdinBytes)
	}
	return raw, nil
}

func runCoreEmit(args []string) error {
	a := newOcrFlagSet("ocr core emit")
	if err := a.Parse(args); err != nil {
		return err
	}
	if a.showHelp {
		printCoreUsage()
		return nil
	}

	raw, err := readCoreStdin()
	if err != nil {
		return err
	}

	comments, err := parseEmitInput(raw)
	if err != nil {
		return err
	}

	// Reuse the exact `ocr review` JSON contract so existing CI consumers work
	// unchanged. CI-type-specific escaping of comment text stays the consumer's
	// responsibility, identical to the `ocr review` path.
	return outputJSON(comments)
}

// parseEmitInput accepts either a bare JSON array of comments or an object with
// a "comments" array, validates sizes, and returns the comments. Split out from
// stdin handling so it can be unit-tested.
func parseEmitInput(raw []byte) ([]model.LlmComment, error) {
	trimmed := bytes.TrimSpace(raw)
	var comments []model.LlmComment
	if len(trimmed) > 0 && trimmed[0] == '[' {
		if err := json.Unmarshal(raw, &comments); err != nil {
			return nil, fmt.Errorf("parse comments array: %w", err)
		}
	} else {
		var wrapper struct {
			Comments []model.LlmComment `json:"comments"`
		}
		if err := json.Unmarshal(raw, &wrapper); err != nil {
			return nil, fmt.Errorf("parse stdin JSON: %w", err)
		}
		comments = wrapper.Comments
	}

	if len(comments) > maxCoreCommentCount {
		return nil, fmt.Errorf("too many comments (%d exceeds max %d)", len(comments), maxCoreCommentCount)
	}
	for i := range comments {
		c := comments[i]
		for label, field := range map[string]string{
			"content":         c.Content,
			"suggestion_code": c.SuggestionCode,
			"existing_code":   c.ExistingCode,
		} {
			if len(field) > maxCoreFieldBytes {
				return nil, fmt.Errorf("comment %d field %q exceeds max length (%d bytes)", i, label, maxCoreFieldBytes)
			}
		}
	}
	// Never emit a nil slice — the contract requires `comments` to be present.
	if comments == nil {
		comments = []model.LlmComment{}
	}
	return comments, nil
}

func runCoreRule(args []string) error {
	a := newOcrFlagSet("ocr core rule")
	var repoDir, rulePath string
	a.StringVar(&repoDir, "repo", "", "root directory of the git repository (default: current dir)")
	a.StringVar(&rulePath, "rule", "", "path to JSON file with custom review rules")
	if err := a.Parse(args); err != nil {
		return err
	}
	if a.showHelp {
		printCoreUsage()
		return nil
	}

	rest := a.fs.Args()
	if len(rest) == 0 {
		return fmt.Errorf("usage: ocr core rule [flags] <file-path>")
	}
	filePath := rest[0]

	resolvedRepo, err := resolveRepoDir(repoDir)
	if err != nil {
		return err
	}

	resolver, _, err := rules.NewResolver(resolvedRepo, rulePath)
	if err != nil {
		return fmt.Errorf("load rules: %w", err)
	}

	// The <file-path> is matched against glob patterns to select a rule; it is
	// never read from disk. Output the resolved rule text only.
	fmt.Println(resolver.Resolve(strings.ToLower(filePath)))
	return nil
}

// corePromptPhases maps the public phase name to the template conversation.
// "compression" (MEMORY_COMPRESSION) is intentionally excluded: the skill relies
// on Claude Code's own context management instead of porting that phase.
func runCorePrompt(args []string) error {
	a := newOcrFlagSet("ocr core prompt")
	if err := a.Parse(args); err != nil {
		return err
	}
	if a.showHelp {
		printCoreUsage()
		return nil
	}

	rest := a.fs.Args()
	if len(rest) == 0 {
		return fmt.Errorf("usage: ocr core prompt <main|plan|filter|relocation>")
	}
	phase := strings.ToLower(rest[0])

	tpl, err := template.LoadDefault()
	if err != nil {
		return fmt.Errorf("load prompt templates: %w", err)
	}

	var conv *template.LlmConversation
	switch phase {
	case "main":
		conv = &tpl.MainTask
	case "plan":
		conv = tpl.PlanTask
	case "filter":
		conv = tpl.ReviewFilterTask
	case "relocation":
		conv = tpl.ReLocationTask
	case "compression":
		return fmt.Errorf("phase %q is not exposed (MEMORY_COMPRESSION is out of scope for ocr core)", phase)
	default:
		return fmt.Errorf("unknown prompt phase %q: expected one of main, plan, filter, relocation", phase)
	}
	if conv == nil || len(conv.Messages) == 0 {
		return fmt.Errorf("prompt phase %q is not available in the template", phase)
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(conv.Messages)
}

func printCoreUsage() {
	fmt.Println(`Deterministic, LLM-free building blocks for an external review brain.

Usage:
  ocr core <sub-command> [flags]

Sub-commands:
  diff       Output reviewable files, diff bodies, hunk maps and exclude reasons as JSON
  relocate   Map a comment's existing_code to exact line numbers (reads stdin JSON)
  emit       Wrap a comment array in the ocr review JSON contract (reads stdin JSON)
  rule       Print the review rule that applies to a file path
  prompt     Print an embedded prompt phase (main|plan|filter|relocation) as JSON

Notes:
  - No core sub-command makes an LLM or network call.
  - ocr core diff modes: workspace (default), --from/--to, or --commit.

Examples:
  ocr core diff --from main --to feature
  ocr core rule src/main/java/com/example/Foo.java
  ocr core prompt main`)
}
