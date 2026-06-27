package main

import (
	"encoding/json"
	"strings"
	"testing"
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

func TestRunCoreUnknownSubcommand(t *testing.T) {
	err := runCore([]string{"bogus"})
	if err == nil || !strings.Contains(err.Error(), "unknown core sub-command") {
		t.Fatalf("want unknown sub-command error, got %v", err)
	}
}

func TestRunCorePromptValidation(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{"missing phase", nil, "usage"},
		{"compression excluded", []string{"compression"}, "out of scope"},
		{"unknown phase", []string{"bogus"}, "unknown prompt phase"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := runCorePrompt(tt.args)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("args %v: want error containing %q, got %v", tt.args, tt.wantErr, err)
			}
		})
	}
}

func TestRunCorePromptValidPhases(t *testing.T) {
	// main/plan/filter/relocation must resolve to a non-empty template phase.
	for _, phase := range []string{"main", "plan", "filter", "relocation"} {
		t.Run(phase, func(t *testing.T) {
			if err := runCorePrompt([]string{phase}); err != nil {
				t.Errorf("phase %q: unexpected error %v", phase, err)
			}
		})
	}
}

func TestRunCoreDiffModeValidation(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{"from without to", []string{"--from", "main"}, "--to is required"},
		{"to without from", []string{"--to", "dev"}, "--from is required"},
		{"both modes", []string{"--from", "a", "--to", "b", "--commit", "c"}, "only one diff mode"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := runCoreDiff(tt.args)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("args %v: want error containing %q, got %v", tt.args, tt.wantErr, err)
			}
		})
	}
}
