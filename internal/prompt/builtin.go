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

## Issue #{{issue_number}}
{{issue_body}}

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

## Issue #{{issue_number}}
{{issue_body}}

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

{{#if git_diff}}
## Full Diff
` + "```diff" + `
{{git_diff}}
` + "```" + `
{{/if}}

## Review Instructions
1. Review all changed files for correctness, security, and edge cases
2. Check that the implementation matches the acceptance criteria
3. Look for bugs, race conditions, missing error handling
4. Verify test coverage is adequate
5. Report findings with specific file:line references
`

const qaTemplate = `# QA Testing: {{issue_title}}

## Issue #{{issue_number}}
{{issue_body}}

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

## QA Instructions
1. Review the acceptance criteria carefully
2. Write and run tests that exercise each criterion
3. Test edge cases and error conditions
4. Verify no regressions in existing functionality
5. Report any issues found with reproduction steps
{{#if prior_stage_summary}}

## Implementation Summary
{{prior_stage_summary}}
{{/if}}
`

const fixChecksTemplate = `# Fix Check Failures: {{issue_title}}

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
