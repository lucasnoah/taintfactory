package prompt

// builtinTemplates maps template filename to content.
var builtinTemplates = map[string]string{
	"implement.md":       implementTemplate,
	"review.md":          reviewTemplate,
	"qa.md":              qaTemplate,
	"fix-checks.md":      fixChecksTemplate,
	"merge.md":           mergeTemplate,
	"agent-merge.md":     agentMergeTemplate,
	"contract-check.md":  contractCheckTemplate,
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

### Step 0 — Read the Makefile first
Before doing anything else, read the Makefile in the repo root. It is the
authoritative reference for how to start the dev environment, database,
and any other services. Look for targets related to: dev server, database
startup, migrations, test, build. Use what you find there — do not guess
or invent commands.

### Step 1 — Explore the changes
Use git to understand what was built: ` + "`git log`" + `, ` + "`git show <commit>`" + `, ` + "`git diff main...HEAD`" + `, and read the changed files directly.

### Step 2 — Determine what runtime testing is required
Based on the changes and acceptance criteria, decide which of these apply:
- **New or modified API endpoints** → must start the database and API server, then make real HTTP requests with curl and verify response bodies
- **CLI commands** → must run them and inspect stdout/stderr
- **Data pipelines / background jobs** → must trigger them and verify side effects in the database
- **Pure library/utility changes with no runtime surface** → unit tests may suffice, but explain why

**Running unit tests alone is never sufficient for API endpoints.** The server must actually start and real HTTP requests must be made.

### Step 3 — Start the dev environment (if needed)
Use the Makefile targets you found in Step 0 to:
1. Start the database (typically a docker compose target)
2. Run migrations if there's a target for it
3. Copy or confirm the env file is present (check for ` + "`.env.example`" + ` and copy to ` + "`.env`" + ` if ` + "`.env`" + ` is missing)
4. Start the API server in the background (e.g. ` + "`make dev-api &`" + ` or ` + "`go run ./cmd/... &`" + `), wait a few seconds, then confirm it is listening

### Step 4 — Exercise each acceptance criterion end-to-end
Run the feature and observe real output. Do not reason about the code — observe the actual behavior. For API endpoints, use curl with any auth the Makefile or .env.example reveals (look for AUTH_SECRET, API_TOKEN, etc.).

### Step 5 — Test edge cases and error conditions
Run them — don't just read the code.

### Step 6 — Fill coverage gaps
Write and run tests for any gaps you find. Prefer tests that exercise real code paths over additional mocks.

### Step 7 — Verify no regressions
Run the full test suite using the Makefile test target.

### Step 8 — Fix everything you find
Do not just report problems — edit the code, fix them, commit. Run checks again to confirm.
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

const contractCheckTemplate = `# Contract Check: {{issue_title}} (#{{issue_number}})

> **Do not invoke any skills or slash commands** (e.g. /superpowers, /commit, or any /command). Use only built-in tools.

Issue #{{issue_number}} has just merged. Your job is to validate the Data
Contracts in every downstream issue that was written against it, diff them
against what was actually built, and update them so that
no implement agent runs from stale assumptions.

## What just merged

**Issue:** #{{issue_number}} — {{issue_title}}
**Repo:** {{worktree_path}}

Read the merged code now before doing anything else. Run:

` + "```" + `bash
cd {{worktree_path}}
git log --oneline -5
` + "```" + `

Focus on the types, function signatures, column names, and query names that
downstream issues are likely to reference. Do not rely on what the issue body
says was planned — read the actual files.

## Dependent issues to check

{{#if dependent_issues}}
The following queued issues depend on #{{issue_number}}:

{{dependent_issues}}
{{/if}}

Before proceeding, verify by running:
  factory queue list
Confirm which issues show #{{issue_number}} in their DEPS column. If none do,
output "No dependents found — nothing to validate." and stop.

## Process

For each dependent issue:

**Step 1 — Extract contracts**

Fetch the issue body:
  gh issue view {N} --json body -q .body

Find the Data Contracts section. Extract every concrete claim:
- Column names and types
- Go type field names and their types
- sqlc query function names and signatures
- HTTP response field names and types
- Any numeric constants or formulas cited (e.g. "covers × 0.85")

If an issue has no Data Contracts section, skip it and note that in your output.

**Step 2 — Check against the codebase**

For each claim, verify it against the actual merged code. Read the files —
do not guess. Check:
- Go types: do the field names and types match exactly?
- SQL schema: do the column names and types match the migration files?
- sqlc queries: do the function names and signatures match the generated
  code in the sqlcgen directory?
- HTTP shapes: do the JSON field names match the response struct tags?
- Formulas and constants: do they match the actual implementation logic?

**Step 3 — Classify any drift**

Drift is STRUCTURAL if the correct value is unambiguously readable from the
codebase — a field was renamed, a type changed, a column has a different
name. The fix is mechanical and safe to apply automatically.

Drift is SEMANTIC if the contract's meaning has shifted in a way that
requires the implement agent to make different choices:
- A field exists with the right name and type but the merged code uses it
  differently than the contract describes
- The contract references a concept that was implemented in a fundamentally
  different way than described
- New fields appear in the merged code that the contract didn't anticipate
  and that could change the downstream implementation approach
- A formula or constant changed in a way that affects business logic

When in doubt, treat drift as semantic and document it for the implement agent.

**Step 4 — Act**

For STRUCTURAL drift — patch the issue body:
1. Fetch the current body:
     gh issue view {N} --json body -q .body
2. Make the minimum edit — change only the drifted values, preserve all
   surrounding text and structure exactly.
3. Apply:
     gh issue edit {N} --body "$(cat <<'EOF'
     ...patched body...
     EOF
     )"
4. Add a comment documenting exactly what changed and why:
     gh issue comment {N} --body "..."

For SEMANTIC drift — update the issue with implementation notes:
1. Fetch the current body:
     gh issue view {N} --json body -q .body
2. Append an "## Implementation Notes" section (or add to an existing one)
   that explains precisely: what the contract says, what the merged code
   actually does, and what the implement agent must account for. Be concrete
   — name the struct, method, or field at issue and describe the required
   workaround or adaptation.
3. Apply:
     gh issue edit {N} --body "$(cat <<'EOF'
     ...updated body with implementation notes appended...
     EOF
     )"
4. Add a comment summarizing the drift and the note added:
     gh issue comment {N} --body "..."

Do NOT add any blocking label. The issue proceeds through the pipeline;
the implement agent will read the Implementation Notes and adapt accordingly.

## Output

When all dependent issues have been processed, write a summary:

---
Contract check complete — #{{issue_number}}

Dependents checked: N

| Issue | Title | Outcome |
|---|---|---|
| #N | ... | No drift |
| #N | ... | Structural — patched (X → Y) |
| #N | ... | Semantic — noted (implementation notes added: ...) |

Issues updated with notes: [list, or "none"]
---

Do not exit until every dependent issue has been checked and acted on.
`
