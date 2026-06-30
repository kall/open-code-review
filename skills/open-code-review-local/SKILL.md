---
name: open-code-review-local
description: >
  Runs AI code review on Git changes with NO separate LLM API key, using the
  deterministic `ocr core` command group as building blocks and Claude Code
  itself (your subscription) as the review brain. Use when the user asks to
  review code / a PR / staged changes without configuring an Anthropic or
  OpenAI key — i.e. on a Claude Code subscription login. For the full LLM
  binary path (`ocr review`), use the `open-code-review` skill instead.
license: Apache-2.0
compatibility: >
  Requires the `ocr` CLI built with the `core` command group
  (run `ocr core -h` to verify). Requires Claude Code authenticated via a
  Pro/Max subscription (OAuth) — this path does not read or need
  ANTHROPIC_API_KEY. Phase A (this skill) performs a single-pass main review;
  the PLAN, REVIEW_FILTER, and parallel phases are not yet enabled.
metadata:
  author: alibaba
  homepage: https://github.com/alibaba/open-code-review
  version: "0.1.0"
---

# Open Code Review (Local / No-API-Key)

Run [open-code-review](https://github.com/alibaba/open-code-review)-style code
review **without a separate model API key**. The `ocr core` command group does
the deterministic work (diff parsing, line relocation, rule and prompt lookup,
output formatting) and makes **zero LLM/network calls**; Claude Code — running
on your subscription — supplies the reasoning. This is the "split the brain from
the machine" path: `ocr` is the machine, you are the brain.

> **Data notice.** This skill reads your local source and diffs into the Claude
> Code context (your Anthropic subscription) to perform the review. On
> confidential or NDA-covered code, confirm your organization's data-handling
> policy before using it.

> **Scope.** Phase A (this skill) is a single-pass main review intended for
> **local, interactive** use. The PLAN risk phase, the REVIEW_FILTER
> false-positive pass, and parallel per-file review are Phase B and are not
> enabled here. Unattended CI use is out of scope pending terms-of-service
> confirmation.

## Prerequisites check

```bash
# 1. CLI present and built with the core group
ocr core -h >/dev/null 2>&1 || echo "ocr core NOT AVAILABLE — build/update ocr"

# 2. No API key required. This path uses your Claude Code subscription.
#    Do not set ANTHROPIC_API_KEY for this skill; if `ocr review` works for you
#    via a key/gateway, prefer the `open-code-review` skill instead.
```

If `ocr core` is unavailable, the installed `ocr` predates this command group —
build from source or update before continuing.

## Workflow

### Step 1: Get the review targets

Run `ocr core diff` and parse its JSON. Pass mode flags through from the user:

| User says | Command |
|-----------|---------|
| "review my changes" (default) | `ocr core diff --repo .` |
| "review this branch" | `ocr core diff --repo . --from <base> --to <branch>` |
| "review commit abc123" | `ocr core diff --repo . --commit abc123` |

**GitLab MR (`glab`) / GitHub PR (`gh`).** `ocr core diff` reads the local git
repo — it does not call GitLab/GitHub itself. To review an MR/PR, first make its
refs available locally, then map base/head onto `--from`/`--to`:

- **GitLab MR** — `glab mr checkout <id>` (fetches + checks out the MR's source
  branch). Read the target branch with `glab mr view <id>` (the `targetBranch`
  field; usually `main`). Then:
  `ocr core diff --repo . --from <target-branch> --to <mr-source-branch>`
  (or `--to HEAD`, since checkout left you on the MR branch).
- **GitHub PR** — `gh pr checkout <id>`, read the base with
  `gh pr view <id> --json baseRefName`, then the same `--from <base> --to HEAD`.

`--from <base> --to <head>` produces exactly the MR/PR change set
(merge-base(base, head)..head), so non-code, test, and oversized files are
filtered out the same way `ocr review` filters them.

The output has a `files` array. Review only entries with `"will_review": true`.
Each reviewable entry carries `diff` (unified diff body), `new_file_content`,
`hunks` (line maps), and `changed_lines`. Excluded entries carry an
`exclude_reason` (`binary`, `unsupported_ext`, `default_path`, `deleted`,
`large_diff`) — skip them silently.

### Step 2: Review each reviewable file

For each `will_review` file:

1. **Load the rule and prompt:**

   ```bash
   ocr core rule <path>       # the review checklist that applies to this file
   ocr core prompt main       # the main review system+user prompt (JSON: [{role,content},...])
   ```

   Treat the `main` prompt's instructions as your review directive and the
   `rule` text as the per-file checklist.

2. **Gather context with native tools.** Use `Read`, `Grep`, `Glob`, and
   `git diff` to understand the change in context — this replaces `ocr`'s
   built-in `file_read` / `code_search` / `file_find` tools. Focus on newly
   added/modified lines; do not comment on deleted or unchanged code.

3. **Produce comments.** For each genuine issue, draft a comment with:
   - `path` — the file path
   - `content` — the review feedback
   - `existing_code` — the exact snippet the comment targets (used for line
     positioning in the next step)
   - `suggestion_code` — optional fix

### Step 3: Pin line numbers

For each comment, resolve exact line numbers deterministically:

```bash
echo '{"diff":"<file diff>","new_file_content":"<file content>","comment":{"content":"<text>","existing_code":"<snippet>"}}' \
  | ocr core relocate
```

Returns `{"start_line","end_line","matched"}`. Pass the file's `diff` and
`new_file_content` from Step 1's output. If `matched` is `false`, re-read the
file yourself and set `start_line`/`end_line` from the snippet's location; if you
still cannot locate it, report the comment with `start_line: 0, end_line: 0`.

### Step 4: Report (and optionally emit)

Classify each comment by priority and report to the user:

- **High** — clear bugs, security issues, or precise, well-founded fixes
- **Medium** — context-dependent concerns, style/performance suggestions
- **Low** — likely false positives or nitpicks (discard silently)

```markdown
## Code Review Results

**Files reviewed**: N
**Issues found**: X high / Y medium

### High Priority
- **`path/to/file.go:42`** — Brief description
  > Recommendation: how to fix
```

To produce machine-readable output compatible with the existing CI poster, pipe
the final comment array through `emit`:

```bash
echo '[{"path":"...","content":"...","start_line":42,"end_line":42}]' | ocr core emit
```

This wraps the comments in the same `jsonOutput` contract `ocr review` produces,
so downstream consumers work unchanged.

### Step 5: Fix (only if asked)

If the user requested "review and fix", apply High/Medium fixes directly and
verify with the user before committing. If they asked only to review, stop after
reporting.

## Gotchas

- **No API key, but subscription limits still apply.** Large diffs consume
  subscription usage quickly. Prefer small-to-medium diffs.
- **`ocr core` never calls an LLM.** All reasoning is yours. If a step seems to
  need the model, that step belongs to you, not the binary.
- **Working directory matters.** `ocr core diff` operates on the repo at
  `--repo` (default current dir).
- **Comment language follows your output.** Report in the user's language.

## Known limitations (Phase A)

- **User include/exclude rules are not applied by `ocr core diff`.** It honors
  the default extension allowlist, default exclude paths, binary/large-diff
  filters, and `.gitignore` — but NOT a repo's `.opencodereview/rule.json`
  include/exclude (`FileFilter`) patterns. So its reviewable set can differ from
  `ocr review`: a file you excluded via `rule.json` may still appear with
  `will_review: true`. Do not rely on `rule.json` exclude patterns to keep
  sensitive files out of this path; gate them another way until FileFilter is
  wired in (Phase B follow-up).
- **`relocate` expects new-file code.** Pass `existing_code` taken from added or
  context (unchanged) lines. A snippet taken from a deleted (`-`) line can match
  the old-file side and return a line number that points at unrelated code in the
  new file.
- **`ocr core prompt` emits raw templates.** The `main`/`plan` prompts contain
  unsubstituted placeholders (`{{diff}}`, `{{system_rule}}`, `{{plan_guidance}}`,
  `{{change_files}}`, `{{current_file_path}}`, `{{requirement_background}}`).
  Fill them from the `ocr core diff` / `ocr core rule` output, or treat them as
  structural guidance — do not paste them verbatim as if already resolved.

## References

- Full docs: https://github.com/alibaba/open-code-review
- LLM-binary path: the `open-code-review` skill (uses `ocr review` + a model key)
