// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

const relocateDiffBody = `@@ -1,2 +1,3 @@
 package foo
+var added = 1
-var removed = 2
`

func TestRelocateFromJSON(t *testing.T) {
	t.Run("matches added line", func(t *testing.T) {
		raw := []byte(`{"diff":` + jsonQuote(relocateDiffBody) + `,"comment":{"content":"x","existing_code":"var added = 1"}}`)
		out, err := relocateFromJSON(raw)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !out.Matched || out.StartLine <= 0 {
			t.Errorf("want matched with positive start line, got %+v", out)
		}
	})

	t.Run("empty existing_code does not match", func(t *testing.T) {
		raw := []byte(`{"diff":` + jsonQuote(relocateDiffBody) + `,"comment":{"content":"x","existing_code":""}}`)
		out, err := relocateFromJSON(raw)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if out.Matched || out.StartLine != 0 || out.EndLine != 0 {
			t.Errorf("want unmatched 0/0, got %+v", out)
		}
	})

	t.Run("invalid JSON errors", func(t *testing.T) {
		if _, err := relocateFromJSON([]byte("{not json")); err == nil {
			t.Error("want parse error, got nil")
		}
	})

	t.Run("oversized field rejected", func(t *testing.T) {
		big := strings.Repeat("a", maxCoreFieldBytes+1)
		raw := []byte(`{"comment":{"existing_code":` + jsonQuote(big) + `}}`)
		if _, err := relocateFromJSON(raw); err == nil || !strings.Contains(err.Error(), "max length") {
			t.Errorf("want max-length error, got %v", err)
		}
	})
}

// jsonQuote returns a JSON string literal for s.
func jsonQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func TestParseEmitInput(t *testing.T) {
	t.Run("bare array", func(t *testing.T) {
		c, err := parseEmitInput([]byte(`[{"path":"a.go","content":"hi","start_line":1,"end_line":1}]`))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(c) != 1 || c[0].Path != "a.go" {
			t.Errorf("unexpected comments: %+v", c)
		}
	})

	t.Run("comments object", func(t *testing.T) {
		c, err := parseEmitInput([]byte(`{"comments":[{"path":"b.go","content":"x"}]}`))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(c) != 1 || c[0].Path != "b.go" {
			t.Errorf("unexpected comments: %+v", c)
		}
	})

	t.Run("empty yields non-nil slice", func(t *testing.T) {
		c, err := parseEmitInput([]byte(`{"comments":[]}`))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if c == nil {
			t.Error("comments must be non-nil so the contract emits []")
		}
	})

	t.Run("invalid JSON errors", func(t *testing.T) {
		if _, err := parseEmitInput([]byte(`{bad`)); err == nil {
			t.Error("want error, got nil")
		}
	})

	t.Run("oversized field rejected", func(t *testing.T) {
		big := strings.Repeat("a", maxCoreFieldBytes+1)
		raw := []byte(`[{"path":"a.go","content":` + jsonQuote(big) + `}]`)
		if _, err := parseEmitInput(raw); err == nil || !strings.Contains(err.Error(), "max length") {
			t.Errorf("want max-length error, got %v", err)
		}
	})

	t.Run("too many comments rejected", func(t *testing.T) {
		var sb strings.Builder
		sb.WriteByte('[')
		for i := 0; i < maxCoreCommentCount+1; i++ {
			if i > 0 {
				sb.WriteByte(',')
			}
			sb.WriteString(`{"path":"a.go","content":"x"}`)
		}
		sb.WriteByte(']')
		if _, err := parseEmitInput([]byte(sb.String())); err == nil || !strings.Contains(err.Error(), "too many comments") {
			t.Errorf("want too-many-comments error, got %v", err)
		}
	})
}

// execRoot runs the root command with args and returns the resulting error,
// mirroring how the CLI dispatches in production. Used for the cases that are
// enforced by the Cobra wiring rather than by the run* helpers.
//
// rootCmd is package-level state shared by every test in this file. Cobra does
// not reset a parsed flag between Execute calls, so a case that passes --help
// leaves the flag set on that subcommand and every later invocation of it prints
// help instead of running. Clear it before each execution rather than after, so
// the reset also covers commands an earlier test left dirty.
func execRoot(t *testing.T, args ...string) error {
	t.Helper()
	resetHelpFlags(rootCmd)
	rootCmd.SetArgs(args)
	t.Cleanup(func() { rootCmd.SetArgs(nil) })
	return rootCmd.Execute()
}

// resetHelpFlags clears the auto-registered help flag on cmd and every command
// beneath it.
func resetHelpFlags(cmd *cobra.Command) {
	if f := cmd.Flags().Lookup("help"); f != nil {
		_ = f.Value.Set("false")
		f.Changed = false
	}
	for _, sub := range cmd.Commands() {
		resetHelpFlags(sub)
	}
}

// TestCoreCmd_UnknownSubcommand pins the parent-command convention: an
// unrecognized `ocr core` subcommand is an error, not silent help with exit 0.
func TestCoreCmd_UnknownSubcommand(t *testing.T) {
	err := execRoot(t, "core", "bogus")
	if err == nil || !strings.Contains(err.Error(), "unknown command") {
		t.Fatalf("want unknown-command error, got %v", err)
	}
}

// TestCoreCmd_NoArgsPrintsHelp pins that the bare parent command prints help
// and exits 0.
func TestCoreCmd_NoArgsPrintsHelp(t *testing.T) {
	if err := execRoot(t, "core"); err != nil {
		t.Fatalf("want nil error for bare parent command, got %v", err)
	}
}

// TestCoreCmd_PositionalArgErrors pins the repo's positional-argument
// convention: a wrong count reports the command's own usage, not Cobra's raw
// "accepts 1 arg(s)" message.
func TestCoreCmd_PositionalArgErrors(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"rule without path", []string{"core", "rule"}},
		{"prompt without phase", []string{"core", "prompt"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := execRoot(t, tt.args...)
			if err == nil {
				t.Fatalf("args %v: want error, got nil", tt.args)
			}
			got := err.Error()
			if !strings.Contains(got, "requires exactly 1 argument(s)") {
				t.Errorf("args %v: want arg-count guidance, got %q", tt.args, got)
			}
			if !strings.Contains(got, "Usage:") {
				t.Errorf("args %v: want usage in error, got %q", tt.args, got)
			}
		})
	}
}

// TestCoreCmd_SubcommandsRegistered pins that every documented core subcommand
// resolves through the root command.
func TestCoreCmd_SubcommandsRegistered(t *testing.T) {
	for _, sub := range []string{"diff", "relocate", "emit", "rule", "prompt"} {
		t.Run(sub, func(t *testing.T) {
			if err := execRoot(t, "core", sub, "--help"); err != nil {
				t.Fatalf("core %s --help: unexpected error %v", sub, err)
			}
		})
	}
}

func TestRunCorePromptValidation(t *testing.T) {
	tests := []struct {
		name    string
		phase   string
		wantErr string
	}{
		{"compression excluded", "compression", "out of scope"},
		{"unknown phase", "bogus", "unknown prompt phase"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := runCorePrompt(tt.phase)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("phase %q: want error containing %q, got %v", tt.phase, tt.wantErr, err)
			}
		})
	}
}

func TestRunCorePromptValidPhases(t *testing.T) {
	// main/plan/filter/relocation must resolve to a non-empty template phase.
	for _, phase := range corePromptPhases {
		t.Run(phase, func(t *testing.T) {
			if err := runCorePrompt(phase); err != nil {
				t.Errorf("phase %q: unexpected error %v", phase, err)
			}
		})
	}
}

// TestRunCorePromptCaseInsensitive pins that the phase argument is normalized,
// so `ocr core prompt MAIN` resolves the same phase as `ocr core prompt main`.
func TestRunCorePromptCaseInsensitive(t *testing.T) {
	if err := runCorePrompt("MAIN"); err != nil {
		t.Errorf("uppercase phase should resolve, got %v", err)
	}
}

func TestRunCoreDiffModeValidation(t *testing.T) {
	tests := []struct {
		name    string
		from    string
		to      string
		commit  string
		wantErr string
	}{
		{"from without to", "main", "", "", "--to is required"},
		{"to without from", "", "dev", "", "--from is required"},
		{"both modes", "a", "b", "c", "only one review mode"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := runCoreDiff(coreDiffOptions{from: tt.from, to: tt.to, commit: tt.commit})
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("want error containing %q, got %v", tt.wantErr, err)
			}
		})
	}
}

// TestRunCoreDiff_RejectsNegativeMaxTokens pins the --max-tokens contract
// against `ocr review`: a negative value is an error, not a silent fallback to
// the configured or template default.
func TestRunCoreDiff_RejectsNegativeMaxTokens(t *testing.T) {
	err := runCoreDiff(coreDiffOptions{maxTokens: -1})
	if err == nil || !strings.Contains(err.Error(), "non-negative") {
		t.Fatalf("want non-negative error, got %v", err)
	}
}
