// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package model

import "testing"

func TestDiff_EffectivePath(t *testing.T) {
	tests := []struct {
		name     string
		diff     Diff
		expected string
	}{
		{
			name:     "normal new path",
			diff:     Diff{OldPath: "old.go", NewPath: "new.go"},
			expected: "new.go",
		},
		{
			name:     "new path is dev/null (deleted file)",
			diff:     Diff{OldPath: "deleted.go", NewPath: "/dev/null"},
			expected: "deleted.go",
		},
		{
			name:     "renamed file uses new path",
			diff:     Diff{OldPath: "old_name.go", NewPath: "new_name.go"},
			expected: "new_name.go",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.diff.EffectivePath(); got != tt.expected {
				t.Errorf("EffectivePath() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestDiff_Status(t *testing.T) {
	tests := []struct {
		name     string
		diff     Diff
		expected string
	}{
		{"binary file", Diff{IsBinary: true}, "binary"},
		{"new file", Diff{IsNew: true}, "added"},
		{"deleted file", Diff{IsDeleted: true}, "deleted"},
		{"renamed via flag", Diff{IsRenamed: true}, "renamed"},
		{"renamed via path mismatch", Diff{OldPath: "old.go", NewPath: "new.go"}, "renamed"},
		{"modified file", Diff{OldPath: "main.go", NewPath: "main.go"}, "modified"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.diff.Status(); got != tt.expected {
				t.Errorf("Status() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestExtFromPath(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"main.go", ".go"},
		{"src/app.tsx", ".tsx"},
		{"src/lib/utils.ts", ".ts"},
		{"path/to/FILE.JSON", ".json"},
		{"path/to/FILE.Go", ".go"},
		{"a/b/c.Test.JS", ".js"},
		{"archive.tar.gz", ".gz"},
		{"Makefile", ""},
		{".gitignore", ""},
		{"dir/.hidden", ""},
		{"no-ext", ""},
		{"path/to/", ""},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			if got := ExtFromPath(tt.path); got != tt.want {
				t.Errorf("ExtFromPath(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}
