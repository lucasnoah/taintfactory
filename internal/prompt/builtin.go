package prompt

// builtinTemplates maps template filename to content.
var builtinTemplates = map[string]string{
	"implement.md":    implementTemplate,
	"review.md":       reviewTemplate,
	"qa.md":           qaTemplate,
	"fix-checks.md":   fixChecksTemplate,
	"merge.md":        mergeTemplate,
	"agent-merge.md":  agentMergeTemplate,
}

const implementTemplate = `# Implement: {{issue_title}}

> **Do not invoke any skills or slash commands** (e.g. /superpowers, /commit, or any /command). Use only built-in tools.

## Issue #{{issue_number}}
{{issue_body}}

{{#if feature_intent}}
## Feature Intent
{{feature_intent}}
{{/if}}

{{#if acceptance_criteria}}
## Acceptance Criteria
{{acceptance_criteria}}
{{/if}}

## Repository Context
Working in: {{worktree_path}}
Branch: {{branch}}
Stage: {{stage_id}} (attempt {{attempt}})

## Goal
{{goal}}

## Instructions
1. Read the relevant code to understand the current state
2. Implement the feature described above
3. Write or update tests for your changes
4. Run tests to verify they pass
5. When complete, ensure all changes are committed
{{#if check_failures}}

## Previous Check Failures
The following checks failed and need to be addressed:
{{check_failures}}
{{/if}}
{{#if prior_stage_summary}}

## Prior Stage Context
{{prior_stage_summary}}
{{/if}}
`

const reviewTemplate = `# Code Review: {{issue_title}}

> **Do not invoke any skills or slash commands** (e.g. /superpowers, /commit, or any /command). Use only built-in tools.

## Issue #{{issue_number}}
{{issue_body}}

{{#if feature_intent}}
## Feature Intent
{{feature_intent}}
{{/if}}

{{#if acceptance_criteria}}
## Acceptance Criteria
{{acceptance_criteria}}
{{/if}}

## Repository Context
Working in: {{worktree_path}}
Branch: {{branch}}
Stage: {{stage_id}} (attempt {{attempt}})

{{#if git_diff_summary}}
## Changes Summary
{{git_diff_summary}}
{{/if}}

{{#if files_changed}}
### Files Changed
{{files_changed}}
{{/if}}

{{#if git_commits}}
### Commits
{{git_commits}}
{{/if}}

## Review Instructions

Your job is adversarial review. Assume the implementation is wrong until proven otherwise. Do not give the author the benefit of the doubt — if something looks suspicious, dig in.

1. Use git to explore the changes: ` + "`git log`" + `, ` + "`git show <commit>`" + `, ` + "`git diff main...HEAD`" + `. Read every changed file in full — do not skim.
2. **Do not trust the tests.** Tests written by the implementer are the most likely place for blind spots. Ask: what cases are not tested? What inputs would break this? Write tests for those cases and run them.
3. **Do not trust the happy path.** Actively look for what happens when things go wrong: nil inputs, empty slices, zero values, network failures, DB errors, concurrent access, clock edge cases (midnight, DST, leap day). If error paths are unhandled or silently swallowed, that is a bug.
4. **Do not trust that the acceptance criteria are met.** Read each criterion and find the exact code path that satisfies it. If you cannot point to it, it may not exist.
5. Look for: off-by-one errors, incorrect SQL (wrong joins, missing WHERE clauses, unintended full scans), timezone/date math bugs, integer overflow, incorrect type conversions, missing transaction boundaries, data races, and resource leaks.
6. Check that no existing behavior was silently broken. Read the files that were modified — not just the diff — to understand what was there before and whether the change is safe.
7. **Fix every issue you find.** Do not just report problems — actually edit the code to resolve them. Commit your fixes.
8. If the tests are inadequate (wrong mocks, missing coverage, happy-path only), rewrite or extend them. The test suite should make you confident, not just green.
9. Run the full test suite after your fixes. If anything fails that was passing before, fix it.
`

const qaTemplate = `# QA Testing: {{issue_title}}

> **Do not invoke any skills or slash commands** (e.g. /superpowers, /commit, or any /command). Use only built-in tools.

## Issue #{{issue_number}}
{{issue_body}}

{{#if feature_intent}}
## Feature Intent
{{feature_intent}}
{{/if}}

{{#if acceptance_criteria}}
## Acceptance Criteria
{{acceptance_criteria}}
{{/if}}

## Repository Context
Working in: {{worktree_path}}
Branch: {{branch}}
Stage: {{stage_id}} (attempt {{attempt}})

{{#if git_diff_summary}}
## Changes Made
{{git_diff_summary}}
{{/if}}

{{#if files_changed}}
### Files Changed
{{files_changed}}
{{/if}}

{{#if git_commits}}
### Commits
{{git_commits}}
{{/if}}

## QA Instructions
1. Use git to explore the changes: ` + "`git log`" + `, ` + "`git show <commit>`" + `, ` + "`git diff main...HEAD`" + `, and read the changed files directly
2. Review the acceptance criteria and feature intent carefully
3. **Exercise each acceptance criterion end-to-end by actually running the code.** Do not verify criteria by reading the implementation and reasoning about it — run the feature and observe the output. This means:
   - For API endpoints: make real HTTP requests and check the response body
   - For CLI commands: run them and inspect stdout/stderr
   - For data pipelines: run the sync/backfill, then query the database to confirm records exist with correct values
   - For background jobs: trigger the job and verify its side effects (DB rows, files, logs)
   - For UI changes: not applicable here, skip
4. Write and run tests for any gaps in coverage you find. Prefer tests that call real code paths over additional mocks.
5. Test edge cases and error conditions by running them, not just reading the code
6. Verify no regressions by running the full test suite
7. **Fix every issue you find.** Do not just report problems — actually edit the code to resolve them. Commit your fixes.
8. Run all relevant checks/tests after your fixes to confirm they pass
{{#if prior_stage_summary}}

## Implementation Summary
{{prior_stage_summary}}
{{/if}}
`

const fixChecksTemplate = `# Fix Check Failures: {{issue_title}}

> **Do not invoke any skills or slash commands** (e.g. /superpowers, /commit, or any /command). Use only built-in tools.

## Issue #{{issue_number}}
Stage: {{stage_id}} (attempt {{attempt}})
Working in: {{worktree_path}}
Branch: {{branch}}

## Failed Checks
{{check_failures}}

## Instructions
1. Read the check failure details above
2. Fix each failing check
3. Run the checks again to verify they pass
4. Do not introduce new failures while fixing existing ones
`

const mergeTemplate = `# Merge: {{issue_title}}

> **Do not invoke any skills or slash commands** (e.g. /superpowers, /commit, or any /command). Use only built-in tools.

## Issue #{{issue_number}}
{{issue_body}}

## Repository Context
Working in: {{worktree_path}}
Branch: {{branch}}
Stage: {{stage_id}} (attempt {{attempt}})

{{#if git_diff_summary}}
## Changes Summary
{{git_diff_summary}}
{{/if}}

{{#if files_changed}}
### Files Changed
{{files_changed}}
{{/if}}
{{#if prior_stage_summary}}

## Stage History
{{prior_stage_summary}}
{{/if}}

## Merge Instructions
1. Verify all checks pass
2. Create a pull request with a clear description
3. Include the issue reference (Closes #{{issue_number}})
4. Review the diff one final time before merging
`

const agentMergeTemplate = `# Resolve and Merge: {{issue_title}}

> **Do not invoke any skills or slash commands.** Use only built-in tools.

## Context
The automated merge stage failed on branch ` + "`{{branch}}`" + ` (issue #{{issue_number}}, attempt {{attempt}}).
Your job is to get this PR merged. The most likely cause is that other PRs landed on main
after this branch was cut, creating "both added" conflicts on shared files.

Worktree: {{worktree_path}}
Repo root: {{repo_root}}
Branch: {{branch}}

## Step 1 — Diagnose

` + "```" + `bash
cd {{worktree_path}}
git status                   # is a rebase already in progress?
git log --oneline -5         # what's on this branch?
git log --oneline origin/main -5  # what landed on main?
` + "```" + `

If a rebase is in progress (` + "`git status`" + ` shows "rebase in progress"), abort it first:
` + "`git rebase --abort`" + `

## Step 2 — Rebase onto main

` + "```" + `bash
git fetch origin
git rebase origin/main
` + "```" + `

If the rebase exits cleanly (no conflicts), skip to Step 4.

## Step 3 — Resolve conflicts (if any)

For each conflicting file shown by ` + "`git status`" + `, apply the rules below, then
` + "`git add <file>`" + ` and ` + "`git rebase --continue`" + `. Repeat until the rebase finishes.

**Rule 1 — "Both added" (` + "`AA`" + `) conflicts** (most common):
Both this branch and main added the same file. This happens when an earlier slice
landed on main after this branch was cut.

- Check if main already has an authoritative version: ` + "`git show origin/main:<path>`" + `
  - If yes, and your version duplicates structs/types/migrations already in main:
    take origin/main's version → ` + "`git checkout --ours <file>`" + `
    *(in a rebase, ` + "`--ours`" + ` = origin/main = the target branch)*
  - If the file is genuinely net-new on this branch (main has nothing at that path):
    keep your version → ` + "`git checkout --theirs <file>`" + `

**Rule 2 — "Both modified" conflicts:**
- Generated files (` + "`*.sql.go`" + `, ` + "`models.go`" + `): take ` + "`--ours`" + ` (origin/main).
- Migration files (` + "`migrations/*.sql`" + `): take ` + "`--ours`" + ` (origin/main).
- Application code: open the file, read the conflict markers, and merge manually —
  keep origin/main's structure while preserving the new code this branch adds.

**After each file:** ` + "`git add <file>`" + `

**When all files in a commit are resolved:** ` + "`git rebase --continue`" + `

## Step 4 — Build and test

` + "```" + `bash
go build ./...
go test ./...
` + "```" + `

If the build fails, re-examine the conflicting files. Fix build errors before proceeding.

## Step 5 — Push

` + "```" + `bash
git push --force-with-lease -u origin {{branch}}
` + "```" + `

## Step 6 — Create PR (if none exists)

` + "```" + `bash
gh pr list --head {{branch}} --json url --limit 1
# If the output is "[]", create the PR:
gh pr create --title "#{{issue_number}}: {{issue_title}}" \
  --body "Closes #{{issue_number}}

Automated merge via pipeline."
` + "```" + `

## Step 7 — Merge

The merge requires the worktree to be removed first (git refuses to delete a branch
that is checked out in a worktree). Do this sequence:

` + "```" + `bash
cd {{repo_root}}
git worktree remove {{worktree_path}} --force 2>/dev/null || true
gh pr merge {{branch}} --squash --delete-branch
` + "```" + `

{{#if prior_stage_summary}}
## Stage History
{{prior_stage_summary}}
{{/if}}
`
