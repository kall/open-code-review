package diff

import (
	"testing"

	"github.com/open-code-review/open-code-review/internal/llm"
	"github.com/open-code-review/open-code-review/internal/model"
)

func init() {
	// Use embedded BPE data so llm.CountTokens does not attempt a network fetch
	// during the large-diff filter test.
	llm.InitEmbeddedLoader()
}

const goDiffBody = `diff --git a/foo.go b/foo.go
--- a/foo.go
+++ b/foo.go
@@ -1,2 +1,3 @@
 package foo
+var added = 1
-var removed = 2
`

func findEntry(res *CoreDiffResult, path string) *CoreFileEntry {
	for i := range res.Files {
		if res.Files[i].Path == path {
			return &res.Files[i]
		}
	}
	return nil
}

func TestBuildCoreDiffResult(t *testing.T) {
	tests := []struct {
		name       string
		diff       model.Diff
		maxTokens  int
		wantReview bool
		wantReason string
	}{
		{
			name:       "reviewable go file",
			diff:       model.Diff{NewPath: "foo.go", OldPath: "foo.go", Diff: goDiffBody, Insertions: 1, Deletions: 1},
			wantReview: true,
			wantReason: "",
		},
		{
			name:       "binary file excluded",
			diff:       model.Diff{NewPath: "image.go", OldPath: "image.go", IsBinary: true},
			wantReview: false,
			wantReason: coreExcludeBinary,
		},
		{
			name:       "unsupported extension excluded",
			diff:       model.Diff{NewPath: "data.xyzzy", OldPath: "data.xyzzy", Diff: "@@ -1 +1 @@\n+x\n"},
			wantReview: false,
			wantReason: coreExcludeExtension,
		},
		{
			name:       "default-exclude path (test file)",
			diff:       model.Diff{NewPath: "pkg/foo_test.go", OldPath: "pkg/foo_test.go", Diff: "@@ -1 +1 @@\n+x\n"},
			wantReview: false,
			wantReason: coreExcludeDefaultPath,
		},
		{
			name:       "deleted file excluded",
			diff:       model.Diff{NewPath: "/dev/null", OldPath: "gone.go", IsDeleted: true, Diff: "@@ -1 +0,0 @@\n-x\n"},
			wantReview: false,
			wantReason: coreExcludeDeleted,
		},
		{
			name:       "large diff excluded when over token limit",
			diff:       model.Diff{NewPath: "big.go", OldPath: "big.go", Diff: goDiffBody, Insertions: 1, Deletions: 1},
			maxTokens:  4, // limit = 4*4/5 = 3 tokens; goDiffBody is well over 3
			wantReview: false,
			wantReason: coreExcludeLargeDiff,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := buildCoreDiffResult([]model.Diff{tt.diff}, tt.maxTokens)
			if res.TotalFiles != 1 {
				t.Fatalf("TotalFiles = %d, want 1", res.TotalFiles)
			}
			path := coreEffectivePath(tt.diff)
			e := findEntry(res, path)
			if e == nil {
				t.Fatalf("entry for %q not found", path)
			}
			if e.WillReview != tt.wantReview {
				t.Errorf("WillReview = %v, want %v (reason=%q)", e.WillReview, tt.wantReview, e.ExcludeReason)
			}
			if e.ExcludeReason != tt.wantReason {
				t.Errorf("ExcludeReason = %q, want %q", e.ExcludeReason, tt.wantReason)
			}
			if tt.wantReview {
				if res.ReviewableCount != 1 || res.ExcludedCount != 0 {
					t.Errorf("counts = reviewable %d / excluded %d, want 1/0", res.ReviewableCount, res.ExcludedCount)
				}
				if e.Diff == "" {
					t.Error("reviewable entry should carry the diff body")
				}
			} else {
				if res.ReviewableCount != 0 || res.ExcludedCount != 1 {
					t.Errorf("counts = reviewable %d / excluded %d, want 0/1", res.ReviewableCount, res.ExcludedCount)
				}
				if e.Diff != "" || e.Hunks != nil {
					t.Error("excluded entry should not carry diff body or hunks")
				}
			}
		})
	}
}

func TestBuildCoreDiffResultOldPath(t *testing.T) {
	res := buildCoreDiffResult([]model.Diff{
		{NewPath: "new.go", OldPath: "old.go", IsRenamed: true, Diff: goDiffBody},
		{NewPath: "/dev/null", OldPath: "gone.go", IsDeleted: true, Diff: "@@ -1 +0,0 @@\n-x\n"},
	}, 0)

	rn := findEntry(res, "new.go")
	if rn == nil || rn.OldPath != "old.go" {
		t.Errorf("rename should carry old_path=old.go, got %+v", rn)
	}
	del := findEntry(res, "gone.go")
	if del == nil {
		t.Fatal("deleted entry not found")
	}
	if del.OldPath != "" {
		t.Errorf("deleted file must not duplicate old_path in old_path field, got %q", del.OldPath)
	}
}

func TestBuildCoreDiffResultHunks(t *testing.T) {
	res := buildCoreDiffResult([]model.Diff{{NewPath: "foo.go", OldPath: "foo.go", Diff: goDiffBody}}, 0)
	e := findEntry(res, "foo.go")
	if e == nil || len(e.Hunks) != 1 {
		t.Fatalf("want 1 hunk, got entry=%v", e)
	}
	h := e.Hunks[0]
	if h.NewStart != 1 {
		t.Errorf("NewStart = %d, want 1", h.NewStart)
	}
	// Real diff bodies end with a newline, so ParseHunks may append a trailing
	// empty context line; assert the meaningful leading lines, not an exact count.
	wantTypes := []string{"context", "added", "deleted"}
	if len(h.Lines) < len(wantTypes) {
		t.Fatalf("hunk lines = %d, want at least %d", len(h.Lines), len(wantTypes))
	}
	for i, want := range wantTypes {
		if h.Lines[i].Type != want {
			t.Errorf("line %d type = %q, want %q", i, h.Lines[i].Type, want)
		}
	}
	// Content must have the leading +/-/space marker stripped.
	if h.Lines[1].Content != "var added = 1" {
		t.Errorf("added line content = %q, want %q", h.Lines[1].Content, "var added = 1")
	}
}
