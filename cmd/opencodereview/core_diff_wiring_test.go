// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/alibaba/open-code-review/internal/diff"
)

// writeAndStage creates path under dir with the given contents and stages it, so
// the workspace diff provider sees it as a change.
func writeAndStage(t *testing.T, dir, path, contents string) {
	t.Helper()
	full := filepath.Join(dir, path)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(full), err)
	}
	if err := os.WriteFile(full, []byte(contents), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	if out, err := exec.Command("git", "-C", dir, "add", path).CombinedOutput(); err != nil {
		t.Fatalf("git add %s: %v: %s", path, err, out)
	}
}

// captureCoreDiff runs runCoreDiff with stdout redirected and decodes the JSON
// payload it writes.
func captureCoreDiff(t *testing.T, repoDir, rulePath string, excludes []string) *diff.CoreDiffResult {
	t.Helper()

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	orig := os.Stdout
	os.Stdout = w
	runErr := runCoreDiff(repoDir, "", "", "", rulePath, excludes, 0)
	os.Stdout = orig
	if cerr := w.Close(); cerr != nil {
		t.Fatalf("close pipe: %v", cerr)
	}
	if runErr != nil {
		t.Fatalf("runCoreDiff: %v", runErr)
	}

	var res diff.CoreDiffResult
	if err := json.NewDecoder(r).Decode(&res); err != nil {
		t.Fatalf("decode core diff output: %v", err)
	}
	return &res
}

func coreEntry(res *diff.CoreDiffResult, path string) *diff.CoreFileEntry {
	for i := range res.Files {
		if res.Files[i].Path == path {
			return &res.Files[i]
		}
	}
	return nil
}

// TestRunCoreDiff_CLIExcludeApplied covers the wiring `ocr core diff` gained
// when the user file filter was threaded through: a --exclude pattern has to
// reach the filter and change what the command reports as reviewable.
func TestRunCoreDiff_CLIExcludeApplied(t *testing.T) {
	dir := initTestGitRepo(t)
	writeAndStage(t, dir, "src/keep.go", "package src\n\nvar Keep = 1\n")
	writeAndStage(t, dir, "src/generated/skip.go", "package generated\n\nvar Skip = 1\n")

	unfiltered := captureCoreDiff(t, dir, "", nil)
	if e := coreEntry(unfiltered, "src/generated/skip.go"); e == nil || !e.WillReview {
		t.Fatalf("without --exclude the generated file should be reviewable, got %+v", e)
	}

	filtered := captureCoreDiff(t, dir, "", []string{"**/generated/*"})

	keep := coreEntry(filtered, "src/keep.go")
	if keep == nil || !keep.WillReview {
		t.Errorf("src/keep.go should stay reviewable, got %+v", keep)
	}

	skip := coreEntry(filtered, "src/generated/skip.go")
	if skip == nil {
		t.Fatal("excluded file must still be listed")
	}
	if skip.WillReview {
		t.Error("--exclude pattern did not reach the file filter")
	}
	if skip.ExcludeReason != "user_exclude" {
		t.Errorf("exclude_reason = %q, want %q", skip.ExcludeReason, "user_exclude")
	}
}

// TestRunCoreDiff_RuleFileExcludeApplied covers the other half of the same
// wiring: excludes declared in a rule.json layer reach the filter too, which is
// what makes core agree with review for a repo that configures its own rules.
func TestRunCoreDiff_RuleFileExcludeApplied(t *testing.T) {
	dir := initTestGitRepo(t)
	writeAndStage(t, dir, "src/keep.go", "package src\n\nvar Keep = 1\n")
	writeAndStage(t, dir, "src/vendored/dep.go", "package vendored\n\nvar Dep = 1\n")

	rulePath := filepath.Join(t.TempDir(), "rule.json")
	if err := os.WriteFile(rulePath, []byte(`{"exclude":["**/vendored/**"],"rules":[]}`), 0o644); err != nil {
		t.Fatalf("write rule file: %v", err)
	}

	res := captureCoreDiff(t, dir, rulePath, nil)

	if keep := coreEntry(res, "src/keep.go"); keep == nil || !keep.WillReview {
		t.Errorf("src/keep.go should stay reviewable, got %+v", keep)
	}

	skip := coreEntry(res, "src/vendored/dep.go")
	if skip == nil {
		t.Fatal("excluded file must still be listed")
	}
	if skip.WillReview || skip.ExcludeReason != "user_exclude" {
		t.Errorf("rule.json exclude did not reach the filter: %+v", skip)
	}
}
