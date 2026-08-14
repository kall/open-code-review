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
  ANTHROPIC_API_KEY. Performs the PLAN risk pass (large changes), parallel
  per-file main review, deterministic line relocation with an LLM fallback,
  and an opt-in isolated REVIEW_FILTER false-positive pass. Intended for
  local, interactive use. Note: parity with `ocr review` is not yet
  empirically benchmarked (verification gate U6 pending), so treat results as
  best-effort rather than a validated equivalent.
metadata:
  author: alibaba
  homepage: https://github.com/alibaba/open-code-review
  version: "0.2.0"
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

> **Scope.** Intended for **local, interactive** use. Unattended CI use is out
> of scope pending terms-of-service confirmation. The "brain swap keeps parity
> with `ocr review`" premise has not yet been benchmarked on a sample PR set
> (verification gate U6) — report findings as best-effort, not a validated
> equivalent of `ocr review`.

## Phase reference

| Phase | When | `ocr core` building block |
|-------|------|---------------------------|
| Diff | always | `ocr core diff` |
| PLAN | per file, `changed_lines >= 50` | `ocr core prompt plan` |
| Main review | per file, always | `ocr core rule <path>` + `ocr core prompt main` |
| Relocate | per comment | `ocr core relocate` (det.) → `ocr core prompt relocation` (LLM fallback) |
| REVIEW_FILTER | after review, **opt-in only** | `ocr core prompt filter` (isolated, diff-only subagent) |
| Emit | optional | `ocr core emit` |

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

**Repo include/exclude rules.** `ocr core diff` applies a repo's
`.opencodereview/rule.json` include/exclude patterns and the `--exclude` flag the
same way `ocr review` does, so both commands select the same files. Pass
`--rule <path>` for a custom rule file and `--exclude '<pat>,<pat>'` for
throwaway patterns:

```bash
ocr core diff --repo . --exclude '**/generated/*,**/vendor/*'
```

The output has a `files` array. Review only entries with `"will_review": true`.
Each reviewable entry carries `diff` (unified diff body), `new_file_content`,
`hunks` (line maps), and `changed_lines`. Excluded entries carry an
`exclude_reason` (`binary`, `unsupported_ext`, `default_path`, `deleted`,
`user_exclude`, `large_diff`) — skip them silently.

### Step 2: Review the files in parallel

Dispatch **one subagent per `will_review` file** (Task/subagent tool) and run
them concurrently. Each file is independent, so this is a fan-out with no shared
state.

- **Concurrency.** Cap parallelism modestly (≈3–5 at a time) — subscription
  usage, not an API quota, is the limit. For many files, process in batches, and
  lower the batch size when many files cross the PLAN threshold (each adds a
  planning pass and so costs more).
- **Failure isolation (R13).** If a file's subagent fails — tool error,
  timeout, or it hits a usage limit — do **not** abort the whole review.
  Continue with the remaining files. Track the failure as a **skipped-file
  record** in a list kept *separate* from the review comments: `{path, reason}`,
  where `reason` is a short label (`timeout`, `usage limit`, `tool error`).
  Skipped-file records surface in the Step 4 coverage line **only** — they are
  not review comments, are not priority-classified, and are **not** passed to
  `ocr core emit`. A partial review of N−1 files beats no review.

Each file subagent runs this per-file pipeline:

#### 2a. Load the rule and the main prompt

```bash
ocr core rule <path>       # the review checklist that applies to this file
ocr core prompt main       # the main review system+user prompt (JSON: [{role,content},...])
```

Treat the `main` prompt's instructions as your review directive and the `rule`
text as the per-file checklist.

#### 2b. PLAN phase — only when `changed_lines >= 50`

Files with **50 or more changed lines** get a risk-analysis pass *before* the
main review; files with 49 or fewer skip straight to 2c. Use the `changed_lines`
field from Step 1's `ocr core diff` output directly (it already equals
insertions + deletions — do not recompute it).

```bash
ocr core prompt plan       # planning system+user prompt (JSON)
```

Produce the plan's structured output yourself — analyze only added/modified
lines and emit:

```json
{ "change_summary": "...",
  "issues": [ { "severity": "high|medium|low",
                "description": "risk point + potential impact",
                "tool_guidance": [ { "name": "Read|Grep|Glob|git diff",
                                     "reason": "why", "arguments": "..." } ] } ] }
```

The prompt's `{{plan_tools}}` placeholder maps to your native tools: `Read`
(↔ `file_read`), `Grep` (↔ `code_search`), `Glob` (↔ `file_find`), and
`git diff`. Sort `issues` by severity descending. Carry this plan into 2c as
your investigation checklist — it tells you which risks to confirm and which
context to pull — instead of reviewing blind.

#### 2c. Gather context and produce comments

Use `Read`, `Grep`, `Glob`, and `git diff` to understand the change in context —
this replaces `ocr`'s built-in `file_read` / `code_search` / `file_find` tools.
Focus on newly added/modified lines; do not comment on deleted or unchanged
code. For each genuine issue, draft a comment with:

- `path` — the file path
- `content` — the review feedback
- `existing_code` — the exact snippet the comment targets (used for line
  positioning in 2d). Take it from added (`+`) or context (unchanged) lines —
  **never** a deleted (`-`) line, or relocation matches the old-file side and
  mis-positions (see Known limitations).
- `suggestion_code` — optional fix

#### 2d. Pin line numbers (deterministic, with LLM fallback)

For each comment, resolve exact line numbers:

```bash
echo '{"diff":"<file diff>","new_file_content":"<file content>","comment":{"content":"<text>","existing_code":"<snippet>"}}' \
  | ocr core relocate
```

Returns `{"start_line","end_line","matched"}`. Pass the file's `diff` and
`new_file_content` from Step 1's output.

**If `matched` is `true`**, use the returned lines. **If `matched` is `false`**,
fall back to the LLM relocation prompt before giving up:

```bash
ocr core prompt relocation   # snippet-extraction prompt; placeholders {diff},{existing_code},{suggestion_content}
```

Fill `{diff}` with the file diff, `{existing_code}` with the snippet that just
failed, and `{suggestion_content}` with the comment text. Produce a single
fenced code block containing the corrected snippet copied **verbatim** from the
diff (strip leading `+`/`-`/space markers). Then re-run `ocr core relocate` with
that corrected snippet as `existing_code`. If it now matches, use those lines;
if it **still** fails to match, report the comment with `start_line: 0,
end_line: 0` (line unknown). Never invent a line number. A line-unknown comment
is still a real comment: keep it, classify it by content like any other, and
include it in `emit` if used — this matches what `ocr review` does when
relocation fails (a downstream poster may place it at the file top).

### Step 3: REVIEW_FILTER — opt-in false-positive pass (isolated)

**Default: skip this step.** Run it only when the user opts in (e.g. asks to
"filter false positives" / "be strict about precision", or passes a filter flag
to the skill invocation).

The filter works *only* because it sees less than the main review did: the main
review used tools to pull real context, so the filter must **not** be able to
rescue or re-derive that context — it can only veto comments the diff itself
disproves (KTD7). Enforce the asymmetry by running it in an **isolated
diff-only subagent**:

1. Dispatch a fresh subagent. Give it **only** the unified diffs and the
   candidate comment list as text — nothing else. The candidate list is the
   review comments from Step 2 (line-unknown `0,0` comments included — the
   filter judges the *claim* against the diff, not the line). Skipped-file
   records are not comments; do not include them.
2. Instruct it explicitly: **do not** open, read, search, or `git`-inspect any
   file; judge **solely** from the diff text provided. (It has no business
   touching the codebase; that is the whole point.)
3. Feed it `ocr core prompt filter` as its directive. Its job is to **falsify,
   not verify**: flag a comment only when the diff contains direct
   counter-evidence that its key claim is wrong. Anything that merely "can't be
   confirmed from the diff" must pass — the main review may have seen context the
   filter cannot.
4. Drop the comments the filter marks as provably incorrect; keep the rest.

Comments grounded in tool-gathered context (not visible in the diff) are
preserved by design.

### Step 4: Report (and optionally emit)

Collect the surviving comments from all file subagents. Classify each by
priority and report to the user:

- **High** — clear bugs, security issues, or precise, well-founded fixes
- **Medium** — context-dependent concerns, style/performance suggestions
- **Low** — likely false positives or nitpicks (discard silently)

Report coverage as `<reviewed> of <will_review total>`, where `<reviewed>`
counts only files whose review **completed** — files dropped by failure
isolation (Step 2) are excluded from `<reviewed>` and listed by path and reason
so the user knows coverage was partial.

```markdown
## Code Review Results

**Files reviewed**: 2 of 3 (1 skipped: `src/api.go` — timeout)
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

- **No API key, but subscription limits still apply.** Large diffs and wide
  parallelism consume subscription usage quickly. Keep concurrency modest and
  prefer small-to-medium diffs.
- **`ocr core` never calls an LLM.** All reasoning is yours. If a step seems to
  need the model, that step belongs to you (or a subagent), not the binary.
- **The filter subagent must stay blind.** If you let it read files, you have
  broken KTD7 — it will start "rescuing" false positives instead of vetoing them.
  Diff text in, verdicts out, nothing else.
- **Working directory matters.** `ocr core diff` operates on the repo at
  `--repo` (default current dir).
- **Comment language follows your output.** Report in the user's language.

## Known limitations

- **`relocate` expects new-file code.** Pass `existing_code` taken from added or
  context (unchanged) lines. A snippet taken from a deleted (`-`) line can match
  the old-file side and return a line number that points at unrelated code in the
  new file.
- **`ocr core prompt` emits raw templates.** The `main`/`plan`/`filter`/
  `relocation` prompts contain unsubstituted placeholders (`{{diff}}`,
  `{{system_rule}}`, `{{plan_guidance}}`, `{{plan_tools}}`, `{{change_files}}`,
  `{{current_file_path}}`, `{{requirement_background}}`, `{diff}`,
  `{existing_code}`, `{suggestion_content}`). Fill them from the `ocr core diff`
  / `ocr core rule` output, or treat them as structural guidance — do not paste
  them verbatim as if already resolved.
- **Parity is unbenchmarked (U6).** This path has not been measured against
  `ocr review` on a sample PR set. It is a faithful reconstruction of the loop,
  not a certified equivalent.

## References

- Full docs: https://github.com/alibaba/open-code-review
- LLM-binary path: the `open-code-review` skill (uses `ocr review` + a model key)
```
