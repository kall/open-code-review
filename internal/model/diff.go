// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package model

import "strings"

// Diff represents a single file change in a git diff.
type Diff struct {
	OldPath        string `json:"old_path"`
	NewPath        string `json:"new_path"`
	Diff           string `json:"diff"`
	NewFileContent string `json:"new_file_content"`
	IsBinary       bool   `json:"is_binary"`
	IsDeleted      bool   `json:"is_deleted"`
	IsNew          bool   `json:"is_new"`
	IsRenamed      bool   `json:"is_renamed"`
	Insertions     int64  `json:"insertions"`
	Deletions      int64  `json:"deletions"`
}

// EffectivePath returns the path used to identify the file for filtering and
// display: NewPath normally, but OldPath when the file was deleted (NewPath is
// /dev/null). This is the single source of truth shared by the review agent,
// the scan agent, and the `ocr core diff` command so all paths agree on which
// name a change is keyed under.
func (d Diff) EffectivePath() string {
	if d.NewPath == "/dev/null" {
		return d.OldPath
	}
	return d.NewPath
}

// Status returns a stable, human-readable change kind ("binary", "added",
// "deleted", "renamed", "modified") derived from the diff's flags. Shared so the
// preview, scan, and core-diff outputs report identical statuses.
func (d Diff) Status() string {
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

// ExtFromPath returns the file extension with a leading dot, lowercased, or ""
// when the basename has no extension (dotfiles like ".gitignore" and
// extensionless names like "Makefile" return ""). Shared by every code path
// that applies the allow-listed-extension filter.
func ExtFromPath(path string) string {
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
