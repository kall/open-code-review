// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package agent

import (
	"context"
	"fmt"

	allowedext "github.com/alibaba/open-code-review/internal/config/allowlist"
	"github.com/alibaba/open-code-review/internal/model"
)

// ExcludeReason / DiffPreview / DiffPreviewEntry are now type aliases of
// the mode-agnostic preview types in internal/model. Kept for backwards
// compatibility with existing call sites; internal/scan returns the same
// model.Preview shape directly.
type ExcludeReason = model.ExcludeReason
type DiffPreview = model.Preview
type DiffPreviewEntry = model.PreviewEntry

// Re-export the constants so callers can keep writing agent.ExcludeBinary.
const (
	ExcludeNone        = model.ExcludeNone
	ExcludeUserRule    = model.ExcludeUserRule
	ExcludeExtension   = model.ExcludeExtension
	ExcludeDefaultPath = model.ExcludeDefaultPath
	ExcludeDeleted     = model.ExcludeDeleted
	ExcludeBinary      = model.ExcludeBinary
)

// whyExcluded applies the filter algorithm as shouldReview but
// returns the specific reason a file is excluded.
func (a *Agent) whyExcluded(d model.Diff) ExcludeReason {
	if d.IsBinary {
		return ExcludeBinary
	}

	path := d.EffectivePath()
	f := a.args.FileFilter

	if f != nil && f.IsUserExcluded(path) {
		return ExcludeUserRule
	}

	if f != nil && f.HasInclude() && f.IsUserIncluded(path) {
		return ExcludeNone
	}

	ext := model.ExtFromPath(path)
	if ext != "" && !allowedext.IsAllowedExt(ext) {
		return ExcludeExtension
	}

	if allowedext.IsExcludedPath(path) {
		return ExcludeDefaultPath
	}

	return ExcludeNone
}

// Preview loads diffs and applies the filter algorithm, returning structured
// preview data without dispatching any LLM calls.
//
// It builds none of the review runtime — no session, manifest, or runner — so
// previewing cannot open session persistence. Going through New instead would
// auto-create a session and leave an unfinalized JSONL file under the OCR home.
func Preview(ctx context.Context, args Args) (*DiffPreview, error) {
	return (&Agent{args: args}).preview(ctx)
}

func (a *Agent) preview(ctx context.Context) (*DiffPreview, error) {
	if err := a.loadDiffs(ctx); err != nil {
		return nil, fmt.Errorf("load diffs: %w", err)
	}

	result := &DiffPreview{
		TotalInsertions: a.totalInsertions,
		TotalDeletions:  a.totalDeletions,
		TotalFiles:      len(a.diffs),
		// Non-nil so an empty diff marshals as `"files":[]`, not `"files":null`.
		Entries: make([]DiffPreviewEntry, 0, len(a.diffs)),
	}

	for _, d := range a.diffs {
		path := d.EffectivePath()
		entry := DiffPreviewEntry{
			Path:       path,
			Insertions: d.Insertions,
			Deletions:  d.Deletions,
			Status:     d.Status(),
		}

		reason := a.whyExcluded(d)
		if reason == ExcludeNone && d.IsDeleted {
			reason = ExcludeDeleted
		}

		entry.WillReview = reason == ExcludeNone
		entry.ExcludeReason = reason

		if entry.WillReview {
			result.ReviewableCount++
		} else {
			result.ExcludedCount++
		}

		result.Entries = append(result.Entries, entry)
	}

	return result, nil
}
