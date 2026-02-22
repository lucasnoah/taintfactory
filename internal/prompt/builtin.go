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
1. Use git to explore the changes: ` + "`git log`" + `, ` + "`git show <commit>`" + `, ` + "`git diff main...HEAD`" + `, and read the changed files directly
2. Review all changed files for correctness, security, and edge cases
3. Check that the implementation matches the feature intent and acceptance criteria
4. Look for bugs, race conditions, missing error handling, and edge cases
5. Verify test coverage is adequate
6. **Fix every issue you find.** Do not just report problems — actually edit the code to resolve them. Commit your fixes.
7. If you find issues in the tests (wrong mocks, missing coverage), fix those too
8. Run all relevant checks/tests after your fixes to confirm they pass
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
3. Write and run tests that exercise each criterion
4. Test edge cases and error conditions
5. Verify no regressions in existing functionality
6. **Fix every issue you find.** Do not just report problems — actually edit the code to resolve them. Commit your fixes.
7. Run all relevant checks/tests after your fixes to confirm they pass
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
