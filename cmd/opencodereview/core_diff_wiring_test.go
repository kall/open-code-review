// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package main

import (
	"encoding/json"
	"io"
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

// isolateHome points the user home directory at a temp dir so a real
// ~/.opencodereview/rule.json or config.json on the machine running the tests
// cannot change which files the command selects.
func isolateHome(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // os.UserHomeDir on Windows
}

// captureStdoutErr runs fn with os.Stdout redirected and returns what it wrote,
// failing the test if fn returns an error. It exists alongside the package's
// captureStdout helper because `ocr core diff` payloads are unbounded: the pipe
// is drained concurrently here, so output larger than the pipe buffer cannot
// block fn's write forever with no reader.
func captureStdoutErr(t *testing.T, fn func() error) []byte {
	t.Helper()

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}

	done := make(chan []byte, 1)
	go func() {
		out, _ := io.ReadAll(r)
		done <- out
	}()

	orig := os.Stdout
	os.Stdout = w
	runErr := fn()
	os.Stdout = orig

	if cerr := w.Close(); cerr != nil {
		t.Fatalf("close pipe: %v", cerr)
	}
	out := <-done
	if cerr := r.Close(); cerr != nil {
		t.Fatalf("close pipe reader: %v", cerr)
	}
	if runErr != nil {
		t.Fatalf("command returned error: %v", runErr)
	}
	return out
}

// captureCoreDiff runs runCoreDiff with stdout captured and decodes the JSON
// payload it writes.
func captureCoreDiff(t *testing.T, repoDir, rulePath, excludes string) *diff.CoreDiffResult {
	t.Helper()

	out := captureStdoutErr(t, func() error {
		return runCoreDiff(coreDiffOptions{repoDir: repoDir, rulePath: rulePath, excludes: excludes})
	})

	var res diff.CoreDiffResult
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatalf("decode core diff output: %v\noutput was:\n%s", err, out)
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

// TestRunCoreDiff_CLIExcludeApplied pins that a --exclude pattern reaches the
// user file filter and changes what the command reports as reviewable.
func TestRunCoreDiff_CLIExcludeApplied(t *testing.T) {
	isolateHome(t)
	dir := initTestGitRepo(t)
	writeAndStage(t, dir, "src/keep.go", "package src\n\nvar Keep = 1\n")
	writeAndStage(t, dir, "src/generated/skip.go", "package generated\n\nvar Skip = 1\n")
	writeAndStage(t, dir, "src/generated/deep/nested.go", "package deep\n\nvar Deep = 1\n")

	unfiltered := captureCoreDiff(t, dir, "", "")
	if e := coreEntry(unfiltered, "src/generated/skip.go"); e == nil || !e.WillReview {
		t.Fatalf("without --exclude the generated file should be reviewable, got %+v", e)
	}

	// The pattern the docs recommend has to cover nested paths too: `*` does not
	// cross a path separator, so a `**/generated/*` pattern would leave
	// src/generated/deep/nested.go reviewable while the user believes the whole
	// directory is excluded.
	filtered := captureCoreDiff(t, dir, "", "**/generated/**")

	keep := coreEntry(filtered, "src/keep.go")
	if keep == nil || !keep.WillReview {
		t.Errorf("src/keep.go should stay reviewable, got %+v", keep)
	}

	for _, path := range []string{"src/generated/skip.go", "src/generated/deep/nested.go"} {
		skip := coreEntry(filtered, path)
		if skip == nil {
			t.Fatalf("excluded file %s must still be listed", path)
		}
		if skip.WillReview {
			t.Errorf("%s: --exclude pattern did not reach the file filter", path)
		}
		if skip.ExcludeReason != "user_exclude" {
			t.Errorf("%s: exclude_reason = %q, want %q", path, skip.ExcludeReason, "user_exclude")
		}
	}
}

// TestCoreDiffCmd_FlagsBindThroughCobra drives the flags through the real Cobra
// parse path rather than constructing coreDiffOptions directly, so a flag
// registered under the wrong name or bound to the wrong field is caught. The
// direct-call tests above cannot see that class of mistake.
func TestCoreDiffCmd_FlagsBindThroughCobra(t *testing.T) {
	isolateHome(t)
	dir := initTestGitRepo(t)
	writeAndStage(t, dir, "src/keep.go", "package src\n\nvar Keep = 1\n")
	writeAndStage(t, dir, "src/generated/skip.go", "package generated\n\nvar Skip = 1\n")

	// coreDiffOpts is package-level state bound to the flags; Cobra does not
	// reset it between Execute calls, so restore it for later tests.
	saved := coreDiffOpts
	t.Cleanup(func() { coreDiffOpts = saved })

	out := captureStdoutErr(t, func() error {
		return execRoot(t, "core", "diff", "--repo", dir, "--exclude", "**/generated/**")
	})

	var res diff.CoreDiffResult
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatalf("decode core diff output: %v\noutput was:\n%s", err, out)
	}

	if coreDiffOpts.repoDir != dir {
		t.Errorf("--repo bound to %q, want %q", coreDiffOpts.repoDir, dir)
	}
	if coreDiffOpts.excludes != "**/generated/**" {
		t.Errorf("--exclude bound to %q, want %q", coreDiffOpts.excludes, "**/generated/**")
	}

	if keep := coreEntry(&res, "src/keep.go"); keep == nil || !keep.WillReview {
		t.Errorf("src/keep.go should stay reviewable, got %+v", keep)
	}
	skip := coreEntry(&res, "src/generated/skip.go")
	if skip == nil {
		t.Fatal("excluded file must still be listed")
	}
	if skip.WillReview || skip.ExcludeReason != "user_exclude" {
		t.Errorf("--exclude did not reach the filter through the Cobra path: %+v", skip)
	}
}

// TestRunCoreDiff_RuleFileExcludeApplied pins the other half: excludes declared
// in a rule.json layer reach the filter too, which is what makes core agree with
// review for a repo that configures its own rules.
func TestRunCoreDiff_RuleFileExcludeApplied(t *testing.T) {
	isolateHome(t)
	dir := initTestGitRepo(t)
	writeAndStage(t, dir, "src/keep.go", "package src\n\nvar Keep = 1\n")
	writeAndStage(t, dir, "src/vendored/dep.go", "package vendored\n\nvar Dep = 1\n")

	rulePath := filepath.Join(t.TempDir(), "rule.json")
	if err := os.WriteFile(rulePath, []byte(`{"exclude":["**/vendored/**"],"rules":[]}`), 0o644); err != nil {
		t.Fatalf("write rule file: %v", err)
	}

	res := captureCoreDiff(t, dir, rulePath, "")

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
