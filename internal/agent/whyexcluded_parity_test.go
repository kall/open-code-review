// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package agent

import (
	"testing"

	"github.com/alibaba/open-code-review/internal/config/rules"
	"github.com/alibaba/open-code-review/internal/model"
)

// This table mirrors coreFilterCases in internal/diff/core_filter_parity_test.go
// case for case. `ocr core diff` promises the same file selection as
// `ocr review`, and the two filters are separate functions, so each side pins
// the same expectations: a divergence fails one of the two tests instead of
// silently shipping two different reviewable-file sets. Keep both in sync when
// adding a case.
func TestWhyExcluded_CoreDiffParity(t *testing.T) {
	goFile := func(path string) model.Diff {
		return model.Diff{NewPath: path, OldPath: path}
	}

	tests := []struct {
		name   string
		diff   model.Diff
		filter *rules.FileFilter
		want   model.ExcludeReason
	}{
		{
			name: "plain source file is reviewable",
			diff: goFile("src/main.go"),
			want: model.ExcludeNone,
		},
		{
			name:   "binary wins over every other rule",
			diff:   model.Diff{NewPath: "src/main.go", OldPath: "src/main.go", IsBinary: true},
			filter: &rules.FileFilter{Include: []string{"**/*.go"}},
			want:   model.ExcludeBinary,
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
			diff:   goFile("apps/x/y/lib.go"),
			filter: &rules.FileFilter{Exclude: []string{"**/apps/*"}},
			want:   model.ExcludeNone,
		},
		{
			name:   "trailing doublestar excludes the whole directory tree",
			diff:   goFile("apps/x/y/lib.go"),
			filter: &rules.FileFilter{Exclude: []string{"**/apps/**"}},
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
			name: "secret path is excluded with no filter",
			diff: goFile(".env"),
			want: model.ExcludeSecret,
		},
		{
			name:   "user include cannot admit a secret path",
			diff:   goFile("config/.env"),
			filter: &rules.FileFilter{Include: []string{"**/.env"}},
			want:   model.ExcludeSecret,
		},
		{
			name:   "user exclude does not reclassify a secret path",
			diff:   goFile(".env"),
			filter: &rules.FileFilter{Exclude: []string{"**/.env"}},
			want:   model.ExcludeSecret,
		},
		{
			name:   "rename out of a secret path stays excluded",
			diff:   model.Diff{NewPath: ".env.example", OldPath: ".env", IsRenamed: true},
			filter: &rules.FileFilter{Include: []string{"**/.env.example"}},
			want:   model.ExcludeSecret,
		},
		{
			name:   "env template with an explicit include stays reviewable",
			diff:   goFile(".env.example"),
			filter: &rules.FileFilter{Include: []string{"**/.env.example"}},
			want:   model.ExcludeNone,
		},
		{
			name: "deleted secret file reports the secret reason",
			diff: model.Diff{NewPath: "/dev/null", OldPath: ".env", IsDeleted: true},
			want: model.ExcludeSecret,
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

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := &Agent{args: Args{FileFilter: tt.filter}}

			// selectFiles composes the deleted check on top of whyExcluded, as
			// core's coreWhyExcluded does, so compare the composite rather than
			// the raw predicate. The zero Template leaves the size gate off.
			got := a.selectFiles([]model.Diff{tt.diff})[0].Reason

			if got != tt.want {
				t.Errorf("selectFiles(%q) = %q, want %q", tt.diff.EffectivePath(), got, tt.want)
			}
		})
	}
}
