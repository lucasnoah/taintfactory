package prompt

// builtinTemplates maps template filename to content.
var builtinTemplates = map[string]string{
	"implement.md":  implementTemplate,
	"review.md":     reviewTemplate,
	"qa.md":         qaTemplate,
	"fix-checks.md": fixChecksTemplate,
	"merge.md":      mergeTemplate,
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
