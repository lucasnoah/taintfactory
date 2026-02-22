# Post-Merge Contract Check

**Date:** 2026-02-22
**Status:** Proposed

## Problem

When a feature is decomposed into dependent slices, the implementation issues for
downstream slices contain Data Contracts — Go type definitions, SQL column names,
sqlc query signatures, HTTP response shapes — that were written before the upstream
code exists. By the time the upstream slice merges, those contracts may no longer
match what was actually built. The implement agent for the downstream slice then
works from stale assumptions.

This is a version boundary problem. The issue body is a specification written at
planning time; the codebase is the ground truth written at implementation time.
Neither the Go compiler nor the test suite can see the gap between them, because
both only operate on the current codebase — not on the contracts embedded in issue
bodies.

Two kinds of drift occur:

**Structural drift** — a field was renamed, a type changed, a column has a
different name than specified. The implement agent uses the wrong name, the
compiler catches it, and a fix round is spent on a problem that was knowable
before the agent started.

**Semantic drift** — a field exists with the right name and type, but its meaning
shifted. `cut_signal_covers int` (a headcount ceiling) vs `cut_signal_threshold
float64` (a fractional multiplier) is an example: both compile, both look like
"the cut signal," but they represent entirely different concepts. No type checker
catches this. A review agent checking that "the staffing recommendation is
populated" passes both. This is the dangerous failure mode.

## Conceptual Frame

This is the development-time equivalent of the multi-version coexistence problem
in distributed systems. In production, a type checker verifies a single artifact
in isolation but cannot reason about two deployed versions interacting at a shared
boundary (a database schema, a message queue, an API). The interesting failures
live at those boundaries, not inside individual programs.

The same structure applies here. Each slice is a deployment. The Data Contracts
section of a downstream issue is a compatibility declaration against an upstream
deployment. Type checkers and tests verify slices in isolation — they cannot
verify that a downstream issue's contracts are compatible with what the upstream
slice actually built.

The key insight from schema registry design is that compatibility should be checked
at **registration time** (when a new version is introduced into the system), not
at **use time** (when a consumer tries to read it). The equivalent here: validate
downstream contracts when the upstream slice **merges**, not when the downstream
implement agent starts. At merge time you have maximum information about what was
built and zero wasted downstream work.

## Proposed Solution

Add a `contract-check` stage that fires on the merged issue's pipeline immediately
after the `merge` stage completes. It runs once, operates on all queued issues that
depend on the just-merged issue, and either patches them or blocks them before any
implement agent ever runs from a stale contract.

```
merge completes
    │
    └─► contract-check stage fires
              │
              ├─ for each dependent issue in queue:
              │     ├─ extract Data Contracts from issue body
              │     ├─ diff against merged codebase
              │     ├─ structural drift → patch issue body, add comment
              │     └─ semantic drift  → add label, block for human review
              │
              └─ output summary, close stage
```

The pipeline only proceeds to the next queued item after this stage completes. An
issue that received a patch enters the implement stage with correct contracts. An
issue that was blocked surfaces for human review before the implement agent runs.

## Implementation Notes

### New orchestrator variable: `dependent_issues`

The orchestrator needs to inject a list of queued issues whose `depends_on` field
includes the just-merged issue number. This is a query against the factory queue
at merge time:

```
factory queue list --depends-on {{issue_number}}
```

Rendered as a list and injected as `{{dependent_issues}}` in the template.

### Stage configuration

```yaml
- id: contract-check
  type: agent
  context_mode: minimal
  prompt_template: "templates/contract-check.md"
  checks_after: []
  on_fail: blocked
```

`on_fail: blocked` is intentional — if the contract-check agent itself fails or
produces an ambiguous result, a human should review rather than retrying
automatically.

### Label

A `needs-plan-review` label should exist in the target repo. The agent applies it
to issues where semantic drift was found. This is the signal that the planner
needs to re-examine the issue before it can enter the implement stage.

### Structural vs semantic: decision rule for the agent

The agent should treat drift as **structural** (safe to auto-patch) when:
- A field, column, or function name in the contract does not match the codebase
  and the correct value is unambiguously readable from the source
- A type is wrong (e.g. `int` vs `float64`) and the correct type is directly
  visible in the merged code

The agent should treat drift as **semantic** (block for human review) when:
- A value in the contract matches the codebase structurally but its described
  meaning differs from what the implementation does
- A concept from the contract was implemented in a fundamentally different way
  (e.g. a single SQL query vs multi-step Go logic)
- New fields or functions appear in the merged code that the contract did not
  anticipate and that could materially change how the downstream slice should
  be implemented
- When in doubt

## Template

The following is the prompt template for the `contract-check` stage. Place it at
`templates/contract-check.md` in the project worktree or at
`~/.factory/templates/contract-check.md` for global use.

---

```markdown
# Contract Check: {{issue_title}} (#{{issue_number}})

Issue #{{issue_number}} has just merged. Your job is to validate the Data
Contracts in every downstream issue that was written against it, diff them
against what was actually built, and either patch them or block them before
any implement agent runs from stale assumptions.

## What just merged

**Issue:** #{{issue_number}} — {{issue_title}}
**Repo:** {{worktree_path}}

Read the merged code now before doing anything else. Focus on the types,
function signatures, column names, and query names that downstream issues
are likely to reference. Do not rely on what the issue body says was planned
— read the actual files.

## Dependent issues to check

{{#if dependent_issues}}
{{dependent_issues}}
{{else}}
No queued issues depend on this merge. Verify by running:
  factory queue list
and confirm nothing shows this issue in its DEPS column. If the list is
empty, output "No dependents found — nothing to validate." and stop.
{{/if}}

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

If an issue has no Data Contracts section, skip it and note that in your
output.

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
requires a judgment call:
- A field exists with the right name and type but the merged code uses it
  differently than the contract describes
- The contract references a concept that was implemented in a fundamentally
  different way than described
- New fields appear in the merged code that the contract didn't anticipate
  and that could change the downstream implementation approach
- A formula or constant changed in a way that affects business logic

When in doubt, treat drift as semantic and flag for human review. Do not
auto-patch anything you are not certain about.

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

For SEMANTIC drift — block for human review:
1. Add a comment explaining precisely: what the contract says, what the
   merged code actually does, and why this requires a human decision:
     gh issue comment {N} --body "..."
2. Add the label:
     gh issue edit {N} --add-label "needs-plan-review"

## Output

When all dependent issues have been processed, write a summary:

---
Contract check complete — #{{issue_number}}

Dependents checked: N

| Issue | Title | Outcome |
|---|---|---|
| #N | ... | No drift |
| #N | ... | Structural — patched (X → Y) |
| #N | ... | Semantic — blocked (reason: ...) |

Issues requiring human review: [list, or "none"]
---

Do not exit until every dependent issue has been checked and acted on.
```
