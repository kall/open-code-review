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

			// Preview composes the deleted check on top of whyExcluded; core's
			// coreWhyExcluded folds the same two steps into one call, so compare
			// the composite rather than the raw predicate.
			got := a.whyExcluded(tt.diff)
			if got == model.ExcludeNone && tt.diff.IsDeleted {
				got = model.ExcludeDeleted
			}

			if got != tt.want {
				t.Errorf("whyExcluded(%q) = %q, want %q", tt.diff.EffectivePath(), got, tt.want)
			}
		})
	}
}
