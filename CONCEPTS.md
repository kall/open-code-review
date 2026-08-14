# Concepts

Shared domain vocabulary for this project — entities, named processes, and status concepts with project-specific meaning. Seeded with core domain vocabulary, then accretes as ce-compound and ce-compound-refresh process learnings; direct edits are fine. Glossary only, not a spec or catch-all.

## Review modes

### Review
A code review driven by a diff: the tool computes a change set between two points in history (a ref range, a single commit, or the working tree) and reviews only what changed. This is the default mode and the one most of the product's vocabulary is built around.

### Scan
A code review of whole files, with no diff involved. Scan exists for the case where "what changed" is not the right question — a first pass on an unfamiliar tree, or a sweep for a class of problem across files that may not have been touched. Because there is no change set, the diff-range concepts do not apply to a Scan.

### Core command group
The deterministic, LLM-free layer of the product, meant to be driven by an external reasoning agent rather than a person. It exposes the machinery a review needs — change-set computation, rule lookup, prompt retrieval, Relocation, output formatting — while making no model or network call itself, so it runs with no provider credentials configured. The split it embodies: this layer is the machine, the calling agent is the brain.

## File selection

### Change set
The set of files a Review considers, before filtering. Derived from the comparison the user asked for; a merge commit is compared against its first parent so a merge does not present every file from the merged branch as changed.

### File Filter
The user-configured include and exclude path patterns for a repository, drawn from the project's rule layers and extended by any exclude patterns passed per-run on the command line. Distinct from the built-in extension allowlist and default excluded paths, which apply whether or not a File Filter exists.

Rule layers do not combine: they are consulted in precedence order and the first layer defining either set wins outright, so naming a custom layer discards the layers beneath it rather than adding to them. Per-run command-line excludes are then appended to whichever layer won.

Patterns are globs matched against the whole path, not gitignore syntax: a single wildcard does not cross a path separator, so excluding a directory tree requires the recursive form. An explicit include short-circuits the built-in filters — a file the user included is reviewed even if its extension or location would normally have dropped it.

### Exclude Reason
The recorded reason a file from the Change set was dropped from review. The shared vocabulary is fixed: a user pattern matched it, its extension is outside the supported set, its path matched a built-in excluded location, it was deleted, or it is binary.

Being oversized is deliberately not one of them. The Core command group exposes an additional core-only reason for a diff too large to review, so an excluded oversized file still appears in its output; Review and Scan instead drop oversized items earlier in their pipeline, logging them but assigning no Exclude Reason at all. So the set of files a run reports on is not the same as the set it considered — a distinction that matters whenever the absence of a file is being read as a signal.

An Exclude Reason is the difference between "the review examined this and found nothing" and "the review never saw this." Any workflow that treats a review's silence as evidence has to enumerate the Exclude Reasons first — coverage is a property of the filter, not of the reviewer's judgment.

## Review output

### Relocation
Mapping a review comment onto exact line numbers in the post-change file, by matching the snippet the comment quotes. Relocation is attempted deterministically first, against the change hunks and then the whole post-change file; when that fails, a model-driven pass may correct the snippet and retry.

A comment whose Relocation never succeeds is still a real comment — it is reported with its line position unknown rather than discarded or given an invented line. Snippets must be taken from added or unchanged lines: a snippet copied from a removed line can match the pre-change side and land on unrelated code.

### Review parity
The property that two paths which both select files for review — different commands, or the same command driven through different front ends — choose the same files and reach the same verdicts on them. Parity is not automatic: each path implements its own copy of the selection algorithm, so parity holds only as long as those copies agree, and it is asserted by tests rather than guaranteed by construction.
