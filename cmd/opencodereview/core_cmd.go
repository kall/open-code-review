// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/alibaba/open-code-review/internal/config/rules"
	"github.com/alibaba/open-code-review/internal/config/template"
	"github.com/alibaba/open-code-review/internal/diff"
	"github.com/alibaba/open-code-review/internal/model"
	"github.com/spf13/cobra"
)

// Input-size guards for the stdin-driven core subcommands. LLM-generated input
// is untrusted, so bound it before parsing (R15).
const (
	maxCoreStdinBytes   = 16 << 20 // 16 MiB total stdin payload
	maxCoreFieldBytes   = 1 << 20  // 1 MiB per individual string field
	maxCoreCommentCount = 2000     // max comments accepted by `ocr core emit`
)

// corePromptPhases are the prompt phases `ocr core prompt` exposes.
// "compression" (MEMORY_COMPRESSION) is deliberately absent: the skill relies on
// Claude Code's own context management instead of porting that phase.
var corePromptPhases = []string{"main", "plan", "filter", "relocation"}

// coreCmd is the `ocr core` command group: LLM-free, deterministic building
// blocks meant to be orchestrated by an external brain (e.g. a Claude Code
// skill). No core subcommand performs an LLM or network call.
var coreCmd = &cobra.Command{
	Use:   "core",
	Short: "LLM-free building blocks for an external review brain",
	Long: `Deterministic, LLM-free building blocks for an external review brain.

No core sub-command makes an LLM or network call, so the group runs without any
provider configuration or API key.

Examples:
  ocr core diff --from main --to feature
  ocr core rule src/main/java/com/example/Foo.java
  ocr core prompt main`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return cmd.Help()
	},
}

type coreDiffOptions struct {
	rulePath  string
	repoDir   string
	from      string
	to        string
	commit    string
	excludes  string
	maxTokens int
}

var coreDiffOpts coreDiffOptions

var coreDiffCmd = &cobra.Command{
	Use:   "diff [flags]",
	Short: "Output reviewable files, diff bodies, hunk maps and exclude reasons as JSON",
	Long: `Output reviewable files, diff bodies, hunk maps and exclude reasons as JSON.

Diff modes: workspace (default), --from/--to, or --commit.`,
	Example: `  ocr core diff
  ocr core diff --from main --to feature
  ocr core diff --commit abc123
  ocr core diff --exclude '**/generated/**,**/testdata/**'`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runCoreDiff(coreDiffOpts)
	},
}

var coreRelocateCmd = &cobra.Command{
	Use:     "relocate",
	Short:   "Map a comment's existing_code to exact line numbers (reads stdin JSON)",
	Long:    "Map a comment's existing_code to exact line numbers. Reads the payload as JSON on stdin.",
	Example: `  echo '{"diff":"...","new_file_content":"...","comment":{"existing_code":"..."}}' | ocr core relocate`,
	Args:    cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runCoreRelocate()
	},
}

var coreEmitCmd = &cobra.Command{
	Use:     "emit",
	Short:   "Wrap a comment array in the ocr review JSON contract (reads stdin JSON)",
	Long:    "Wrap a comment array in the `ocr review` JSON contract. Reads the payload as JSON on stdin.",
	Example: `  echo '{"comments":[]}' | ocr core emit`,
	Args:    cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runCoreEmit()
	},
}

var (
	coreRuleRepoDir  string
	coreRuleRulePath string
)

var coreRuleCmd = &cobra.Command{
	Use:   "rule [flags] <file-path>",
	Short: "Print the review rule that applies to a file path",
	Long:  "Print the review rule that applies to the given file path. The path is matched against glob patterns to select a rule; it is never read from disk.",
	Example: `  ocr core rule src/main/java/com/example/Foo.java
  ocr core rule --rule custom.json src/main/resources/mapper/UserMapper.xml`,
	Args: exactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runCoreRule(coreRuleRepoDir, coreRuleRulePath, args[0])
	},
}

var corePromptCmd = &cobra.Command{
	Use:       "prompt <phase>",
	Short:     "Print an embedded prompt phase (main|plan|filter|relocation) as JSON",
	Long:      "Print an embedded prompt phase as a JSON message array.",
	Example:   "  ocr core prompt main\n  ocr core prompt relocation",
	ValidArgs: corePromptPhases,
	Args:      exactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runCorePrompt(args[0])
	},
}

func init() {
	addRepoFlag(coreDiffCmd, &coreDiffOpts.repoDir)
	addDiffFlags(coreDiffCmd, &coreDiffOpts.from, &coreDiffOpts.to, &coreDiffOpts.commit)
	addRuleFlag(coreDiffCmd, &coreDiffOpts.rulePath)
	addExcludeFlag(coreDiffCmd, &coreDiffOpts.excludes)
	coreDiffCmd.Flags().IntVar(&coreDiffOpts.maxTokens, "max-tokens", 0, "large-diff filter base (0 = configured max_tokens or template default)")

	addRepoFlag(coreRuleCmd, &coreRuleRepoDir)
	addRuleFlag(coreRuleCmd, &coreRuleRulePath)

	coreCmd.AddCommand(coreDiffCmd)
	coreCmd.AddCommand(coreRelocateCmd)
	coreCmd.AddCommand(coreEmitCmd)
	coreCmd.AddCommand(coreRuleCmd)
	coreCmd.AddCommand(corePromptCmd)
}

func runCoreDiff(opts coreDiffOptions) error {
	if err := validateDiffMode(opts.from, opts.to, opts.commit); err != nil {
		return err
	}

	resolvedRepo, err := resolveRepoDir(opts.repoDir)
	if err != nil {
		return err
	}

	// Load the user filter the same way `ocr review` does — rule.json layers
	// first, then CLI --exclude patterns merged on top — so both commands select
	// the same files. NewResolver touches no provider config, so this keeps the
	// no-API-key guarantee that the whole core group rests on.
	_, fileFilter, err := rules.NewResolver(resolvedRepo, opts.rulePath)
	if err != nil {
		return fmt.Errorf("load rules: %w", err)
	}
	fileFilter = mergeCLIExcludes(fileFilter, splitPaths(opts.excludes))

	// Resolve the large-diff pre-filter budget the same way `ocr review` does --
	// CLI override, then the saved app config, then the template default -- so
	// both commands drop the same oversized files. Reading the config here is
	// just a JSON parse (no provider resolution), which keeps the no-API-key
	// guarantee; a missing config yields a nil cfg and the template default.
	maxTokens := opts.maxTokens
	if maxTokens <= 0 {
		tplDefault := 0
		if tpl, terr := template.LoadDefault(); terr == nil {
			tplDefault = tpl.MaxTokens
		}
		var appCfg *Config
		if cfgPath, cerr := defaultConfigPath(); cerr == nil {
			appCfg, err = LoadAppConfig(cfgPath)
			if err != nil {
				return fmt.Errorf("load app config: %w", err)
			}
		}
		maxTokens, err = resolveMaxTokens(tplDefault, appCfg, opts.maxTokens)
		if err != nil {
			return err
		}
	}

	result, err := diff.ComputeCoreDiff(context.Background(), diff.CoreDiffOptions{
		RepoDir:    resolvedRepo,
		From:       opts.from,
		To:         opts.to,
		Commit:     opts.commit,
		MaxTokens:  maxTokens,
		FileFilter: fileFilter,
	})
	if err != nil {
		return fmt.Errorf("compute diff: %w", err)
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(result)
}

// coreRelocateInput is the stdin payload for `ocr core relocate`.
type coreRelocateInput struct {
	Diff           string `json:"diff"`
	NewFileContent string `json:"new_file_content"`
	Comment        struct {
		Content      string `json:"content"`
		ExistingCode string `json:"existing_code"`
	} `json:"comment"`
}

// coreRelocateOutput is the stdout result of `ocr core relocate`.
type coreRelocateOutput struct {
	StartLine int  `json:"start_line"`
	EndLine   int  `json:"end_line"`
	Matched   bool `json:"matched"`
}

func runCoreRelocate() error {
	raw, err := readCoreStdin()
	if err != nil {
		return err
	}

	out, err := relocateFromJSON(raw)
	if err != nil {
		return err
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

// relocateFromJSON parses and validates a relocate payload and runs the
// deterministic resolver. Split out from stdin handling so it can be unit-tested.
func relocateFromJSON(raw []byte) (coreRelocateOutput, error) {
	var in coreRelocateInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return coreRelocateOutput{}, fmt.Errorf("parse stdin JSON: %w", err)
	}

	for label, field := range map[string]string{
		"diff":                  in.Diff,
		"new_file_content":      in.NewFileContent,
		"comment.existing_code": in.Comment.ExistingCode,
		"comment.content":       in.Comment.Content,
	} {
		if len(field) > maxCoreFieldBytes {
			return coreRelocateOutput{}, fmt.Errorf("field %q exceeds max length (%d bytes)", label, maxCoreFieldBytes)
		}
	}

	cm := model.LlmComment{
		Content:      in.Comment.Content,
		ExistingCode: in.Comment.ExistingCode,
	}
	d := model.Diff{
		Diff:           in.Diff,
		NewFileContent: in.NewFileContent,
	}

	matched := diff.ResolveComment(&cm, &d)
	return coreRelocateOutput{
		StartLine: cm.StartLine,
		EndLine:   cm.EndLine,
		Matched:   matched,
	}, nil
}

// readCoreStdin reads stdin up to maxCoreStdinBytes, rejecting larger payloads.
func readCoreStdin() ([]byte, error) {
	raw, err := io.ReadAll(io.LimitReader(os.Stdin, maxCoreStdinBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read stdin: %w", err)
	}
	if len(raw) > maxCoreStdinBytes {
		return nil, fmt.Errorf("stdin payload exceeds max size (%d bytes)", maxCoreStdinBytes)
	}
	return raw, nil
}

func runCoreEmit() error {
	raw, err := readCoreStdin()
	if err != nil {
		return err
	}

	comments, err := parseEmitInput(raw)
	if err != nil {
		return err
	}

	// Reuse the exact `ocr review` JSON contract so existing CI consumers work
	// unchanged. CI-type-specific escaping of comment text stays the consumer's
	// responsibility, identical to the `ocr review` path.
	return outputJSON(comments)
}

// parseEmitInput accepts either a bare JSON array of comments or an object with
// a "comments" array, validates sizes, and returns the comments. Split out from
// stdin handling so it can be unit-tested.
func parseEmitInput(raw []byte) ([]model.LlmComment, error) {
	trimmed := bytes.TrimSpace(raw)
	var comments []model.LlmComment
	if len(trimmed) > 0 && trimmed[0] == '[' {
		if err := json.Unmarshal(raw, &comments); err != nil {
			return nil, fmt.Errorf("parse comments array: %w", err)
		}
	} else {
		var wrapper struct {
			Comments []model.LlmComment `json:"comments"`
		}
		if err := json.Unmarshal(raw, &wrapper); err != nil {
			return nil, fmt.Errorf("parse stdin JSON: %w", err)
		}
		comments = wrapper.Comments
	}

	if len(comments) > maxCoreCommentCount {
		return nil, fmt.Errorf("too many comments (%d exceeds max %d)", len(comments), maxCoreCommentCount)
	}
	for i := range comments {
		c := comments[i]
		for label, field := range map[string]string{
			"content":         c.Content,
			"suggestion_code": c.SuggestionCode,
			"existing_code":   c.ExistingCode,
		} {
			if len(field) > maxCoreFieldBytes {
				return nil, fmt.Errorf("comment %d field %q exceeds max length (%d bytes)", i, label, maxCoreFieldBytes)
			}
		}
	}
	// Never emit a nil slice — the contract requires `comments` to be present.
	if comments == nil {
		comments = []model.LlmComment{}
	}
	return comments, nil
}

func runCoreRule(repoDir, rulePath, filePath string) error {
	resolvedRepo, err := resolveRepoDir(repoDir)
	if err != nil {
		return err
	}

	resolver, _, err := rules.NewResolver(resolvedRepo, rulePath)
	if err != nil {
		return fmt.Errorf("load rules: %w", err)
	}

	// The <file-path> is matched against glob patterns to select a rule; it is
	// never read from disk. Output the resolved rule text only.
	fmt.Println(resolver.Resolve(strings.ToLower(filePath)))
	return nil
}

func runCorePrompt(phase string) error {
	phase = strings.ToLower(phase)

	tpl, err := template.LoadDefault()
	if err != nil {
		return fmt.Errorf("load prompt templates: %w", err)
	}

	var conv *template.LlmConversation
	switch phase {
	case "main":
		conv = &tpl.MainTask
	case "plan":
		conv = tpl.PlanTask
	case "filter":
		conv = tpl.ReviewFilterTask
	case "relocation":
		conv = tpl.ReLocationTask
	case "compression":
		return fmt.Errorf("phase %q is not exposed (MEMORY_COMPRESSION is out of scope for ocr core)", phase)
	default:
		return fmt.Errorf("unknown prompt phase %q: expected one of %s", phase, strings.Join(corePromptPhases, ", "))
	}
	if conv == nil || len(conv.Messages) == 0 {
		return fmt.Errorf("prompt phase %q is not available in the template", phase)
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(conv.Messages)
}
