// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package diff

import (
	"context"
	"strings"
	"testing"

	"github.com/alibaba/open-code-review/internal/config/rules"
	"github.com/alibaba/open-code-review/internal/model"
)

// coreFilterCase is one file-selection scenario. The same table is mirrored in
// internal/agent/whyexcluded_parity_test.go and run through the agent's
// whyExcluded, which is the behavior `ocr core diff` has to match: if the two
// commands ever disagree on which files are reviewable, one of the two tables
// fails. Keep the two in sync when adding a case.
type coreFilterCase struct {
	name   string
	diff   model.Diff
	filter *rules.FileFilter
	want   model.ExcludeReason
}

func coreFilterCases() []coreFilterCase {
	goFile := func(path string) model.Diff {
		return model.Diff{NewPath: path, OldPath: path}
	}

	return []coreFilterCase{
		{
			name: "plain source file is reviewable",
			diff: goFile("src/main.go"),
			want: model.ExcludeNone,
		},
		{
			name: "binary wins over every other rule",
			diff: model.Diff{NewPath: "src/main.go", OldPath: "src/main.go", IsBinary: true},
			filter: &rules.FileFilter{
				Include: []string{"**/*.go"},
			},
			want: model.ExcludeBinary,
		},
		{
			name:   "user exclude beats the default allowlist",
			diff:   goFile("src/generated/api.go"),
			filter: &rules.FileFilter{Exclude: []string{"**/generated/*"}},
			want:   model.ExcludeUserRule,
		},
		{
			name: "user exclude beats a matching user include",
			diff: goFile("src/generated/api.go"),
			filter: &rules.FileFilter{
				Include: []string{"**/*.go"},
				Exclude: []string{"**/generated/*"},
			},
			want: model.ExcludeUserRule,
		},
		{
			name:   "user exclude matches case-insensitively",
			diff:   goFile("src/Generated/API.go"),
			filter: &rules.FileFilter{Exclude: []string{"**/generated/*"}},
			want:   model.ExcludeUserRule,
		},
		{
			name:   "user exclude matches a directory pattern",
			diff:   goFile("vendor/lib/deep/nested.go"),
			filter: &rules.FileFilter{Exclude: []string{"vendor/**"}},
			want:   model.ExcludeUserRule,
		},
		{
			name:   "user exclude expands brace alternatives",
			diff:   goFile("src/main.ts"),
			filter: &rules.FileFilter{Exclude: []string{"**/*.{ts,tsx}"}},
			want:   model.ExcludeUserRule,
		},
		{
			name:   "user include rescues an unsupported extension",
			diff:   goFile("notes/todo.xyz"),
			filter: &rules.FileFilter{Include: []string{"notes/**"}},
			want:   model.ExcludeNone,
		},
		{
			name:   "unmatched user include falls through to the default filters",
			diff:   goFile("notes/todo.xyz"),
			filter: &rules.FileFilter{Include: []string{"src/**"}},
			want:   model.ExcludeExtension,
		},
		{
			name: "unsupported extension is excluded with no filter",
			diff: goFile("notes/todo.xyz"),
			want: model.ExcludeExtension,
		},
		{
			name: "default excluded path is excluded with no filter",
			diff: goFile("src/testdata/fixture.go"),
			want: model.ExcludeDefaultPath,
		},
		{
			name:   "user include rescues a default excluded path",
			diff:   goFile("src/testdata/fixture.go"),
			filter: &rules.FileFilter{Include: []string{"src/testdata/**"}},
			want:   model.ExcludeNone,
		},
		{
			name:   "nested path needs a trailing doublestar, not a single star",
			diff:   goFile("vendor/x/y/pkg.go"),
			filter: &rules.FileFilter{Exclude: []string{"**/vendor/*"}},
			want:   model.ExcludeNone,
		},
		{
			name:   "trailing doublestar excludes the whole directory tree",
			diff:   goFile("vendor/x/y/pkg.go"),
			filter: &rules.FileFilter{Exclude: []string{"**/vendor/**"}},
			want:   model.ExcludeUserRule,
		},
		{
			name:   "mixed-case pattern matches a lowercase path",
			diff:   goFile("src/generated/api.go"),
			filter: &rules.FileFilter{Exclude: []string{"**/GENERATED/**"}},
			want:   model.ExcludeUserRule,
		},
		{
			name:   "rename is judged on the new path",
			diff:   model.Diff{NewPath: "src/keep.go", OldPath: "src/generated/old.go", IsRenamed: true},
			filter: &rules.FileFilter{Exclude: []string{"**/generated/**"}},
			want:   model.ExcludeNone,
		},
		{
			name:   "rename into an excluded directory is excluded",
			diff:   model.Diff{NewPath: "src/generated/new.go", OldPath: "src/keep.go", IsRenamed: true},
			filter: &rules.FileFilter{Exclude: []string{"**/generated/**"}},
			want:   model.ExcludeUserRule,
		},
		{
			name: "deleted file is excluded last",
			diff: model.Diff{NewPath: "/dev/null", OldPath: "src/gone.go", IsDeleted: true},
			want: model.ExcludeDeleted,
		},
		{
			name:   "deleted file still reports the user exclude that fired first",
			diff:   model.Diff{NewPath: "/dev/null", OldPath: "src/generated/gone.go", IsDeleted: true},
			filter: &rules.FileFilter{Exclude: []string{"**/generated/*"}},
			want:   model.ExcludeUserRule,
		},
	}
}

// TestCoreWhyExcluded_FilterParity pins `ocr core diff` file selection against
// the shared table. Its counterpart in internal/agent runs the same table
// through the review path.
func TestCoreWhyExcluded_FilterParity(t *testing.T) {
	for _, tt := range coreFilterCases() {
		t.Run(tt.name, func(t *testing.T) {
			got := coreWhyExcluded(tt.diff, tt.filter)
			if got != tt.want {
				t.Errorf("coreWhyExcluded(%q) = %q, want %q", tt.diff.EffectivePath(), got, tt.want)
			}
		})
	}
}

// TestCoreDiff_IndexHeaderStripped pins the `ocr core diff` JSON contract
// against the shared parser's index-header removal. Stripping happens in
// ParseHunks' upstream parser, which core reuses, so the change reaches core's
// `diff` field too — this is a deliberate convergence with `ocr review`, not a
// core-only regression. The test fails if the parser ever starts re-emitting
// object IDs, which would silently change output every consumer of this command
// parses.
func TestCoreDiff_IndexHeaderStripped(t *testing.T) {
	const withIndexHeader = `diff --git a/foo.go b/foo.go
index 0123456789abcdef..fedcba9876543210 100644
--- a/foo.go
+++ b/foo.go
@@ -1,2 +1,3 @@
 package foo
+var added = 1
-var removed = 2
`

	parsed, err := ParseDiffText(context.Background(), withIndexHeader, t.TempDir(), "", nil)
	if err != nil {
		t.Fatalf("parse diff: %v", err)
	}
	if len(parsed) != 1 {
		t.Fatalf("want 1 parsed diff, got %d", len(parsed))
	}

	res := buildCoreDiffResult(parsed, 0, nil)
	e := findEntry(res, "foo.go")
	if e == nil {
		t.Fatal("foo.go missing from result")
	}
	if !e.WillReview {
		t.Fatalf("foo.go should be reviewable, got exclude_reason=%q", e.ExcludeReason)
	}

	if strings.Contains(e.Diff, "index 0123456789abcdef") {
		t.Errorf("core diff body must not carry the index header, got:\n%s", e.Diff)
	}
	// The stripping must be surgical: hunk content still has to survive.
	if !strings.Contains(e.Diff, "var added = 1") {
		t.Errorf("core diff body lost hunk content, got:\n%s", e.Diff)
	}
}

// TestBuildCoreDiffResult_FileFilterApplied checks the filter actually reaches
// the assembled result, not just the predicate: a wired-up filter has to move
// files out of the reviewable count and suppress their diff bodies.
func TestBuildCoreDiffResult_FileFilterApplied(t *testing.T) {
	diffs := []model.Diff{
		{NewPath: "src/keep.go", OldPath: "src/keep.go", Diff: goDiffBody},
		{NewPath: "src/generated/skip.go", OldPath: "src/generated/skip.go", Diff: goDiffBody},
	}

	unfiltered := buildCoreDiffResult(diffs, 0, nil)
	if unfiltered.ReviewableCount != 2 {
		t.Fatalf("without a filter both files should be reviewable, got %d", unfiltered.ReviewableCount)
	}

	filtered := buildCoreDiffResult(diffs, 0, &rules.FileFilter{Exclude: []string{"**/generated/*"}})
	if filtered.ReviewableCount != 1 || filtered.ExcludedCount != 1 {
		t.Fatalf("reviewable/excluded = %d/%d, want 1/1", filtered.ReviewableCount, filtered.ExcludedCount)
	}

	skipped := findEntry(filtered, "src/generated/skip.go")
	if skipped == nil {
		t.Fatal("excluded file must still appear in the output")
	}
	if skipped.WillReview {
		t.Error("excluded file must not be marked reviewable")
	}
	if skipped.ExcludeReason != model.ExcludeUserRule {
		t.Errorf("exclude_reason = %q, want %q", skipped.ExcludeReason, model.ExcludeUserRule)
	}
	if skipped.Diff != "" || len(skipped.Hunks) != 0 {
		t.Error("excluded file must not carry a diff body or hunks")
	}
}
