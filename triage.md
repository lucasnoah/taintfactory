# factory triage — Design Doc

## Concept

A parallel pipeline system alongside the existing dev pipeline. Where the dev pipeline
executes development work on issues, the triage pipeline processes GitHub issues to
determine if they are still valid before anyone starts working on them.

Triage pipelines:
- Run agents in the main repo (no worktrees, no code changes)
- Have no check runners or fix loops
- Run synchronously (`factory triage run <issue>`)
- Output GitHub actions (comments, labels, close) via `gh` CLI

---

## Triage Stage Ideas

### Completion / Staleness Detection

- **Stale context reaper** — Extract file paths, function names, type names, CLI
  commands mentioned in the issue body. Check if each still exists. If stale: comment
  with list of what's gone, suggest updating or closing.

- **Already-implemented detector** — Read the issue's described behavior, search the
  codebase for related code. If behavior already exists: comment with evidence and close.

- **Regression checker** — For bug reports, check if the bug was fixed in a commit and
  later re-introduced via git log + blame. Attach history context to the issue.

- **Version archaeologist** — Determine which commit first introduced the reported bug
  (git bisect-style analysis). Post the commit SHA, author, and message as a comment.

### Deduplication / Relationship Mapping

- **Semantic duplicate finder** — Embed all open issues and cluster by similarity;
  propose merges or add `duplicate` label with a link.
- **Dependency inferrer** — Detect blocked-by/blocks relationships from issue text +
  code overlap; add cross-references.
- **PR orphan linker** — Find open PRs that address an issue but aren't linked; add
  cross-references.

### Triage / Classification

- **Label suggester** — Analyze issue body against file ownership, module names, and
  past label patterns; apply labels.
- **Security classifier** — Flag issues with potential security implications (auth,
  injection, data exposure keywords + code scan).
- **Breaking change detector** — For feature requests, scan if the ask would require
  changing public API signatures or contracts.
- **Documentation gap classifier** — "How do I X" issues that could be closed by a
  doc addition rather than code.

### Effort / Impact Scoring

- **Blast radius estimator** — Count files, callers, and test coverage in the affected
  area to estimate change scope.
- **Assignee suggester** — Run git blame on the most relevant files; suggest the top
  contributor(s).
- **Acceptance criteria generator** — Using the issue description + similar past closed
  issues, draft testable ACs and post as a comment.

### Lifecycle / Hygiene

- **Idle commenter** — For issues with no activity in N days, post a "still relevant?"
  ping and set a close timer.
- **Needs-info enforcer** — Detect issues missing reproduction steps/environment info;
  post a template request and label `needs-info`.
- **Milestone aligner** — Given open milestones and their themes, suggest which
  milestone an issue belongs to.

---

## Architecture

### Config: `triage.yaml` (in the target repo root)

```yaml
triage:
  name: "My Repo"
  repo: "owner/repo"

stages:
  - id: stale_context
    prompt_template: triage/stale-context.md
    on_stale: done              # stop here; agent already commented/labeled
    on_clean: already_implemented

  - id: already_implemented
    prompt_template: triage/already-implemented.md
```

### State: `~/.factory/triage/{repo_slug}/{issue}.json`

```json
{
  "issue": 42,
  "repo": "owner/repo",
  "current_stage": "stale_context",
  "status": "pending|in_progress|completed|blocked",
  "stage_history": [
    { "stage": "stale_context", "outcome": "clean" }
  ],
  "updated_at": "2026-02-22T..."
}
```

### Outcome handoff: `~/.factory/triage/{repo_slug}/{issue}/{stage_id}.outcome.json`

Agent writes this file as its final act. Runner reads it after session goes idle.

```json
{ "outcome": "clean", "summary": "All referenced symbols found" }
```

---

## New Files

| File | Purpose |
|---|---|
| `internal/triage/config.go` | `TriageConfig`, `TriageStage` types; YAML loading |
| `internal/triage/state.go` | `TriagePipelineState` type; JSON load/save |
| `internal/triage/runner.go` | `Run(issue)` — stage execution and pipeline advancement |
| `internal/cli/triage.go` | `factory triage run/status/list` commands |

Modified: `cmd/factory/main.go`, `internal/cli/root.go`

---

## Stage Execution Flow

```
Run(issue int):
  1. Load triage.yaml config
  2. Load or init triage state
  3. Fetch issue: gh issue view {issue} --json title,body,labels
  4. Render prompt template with: {issue_number, title, body, repo_root}
  5. Create tmux session at repo_root (reuse internal/session)
  6. Send rendered prompt to Claude Code session
  7. WaitIdle (reuse internal/session.WaitIdle)
  8. Read outcome JSON file written by agent
  9. Persist outcome to state, log stage history
  10. Route to next stage or mark completed
```

Reused packages: `internal/session`, `internal/github.GetIssue()`, `internal/prompt.Render()`

---

## Prompt Template Behavior

### `triage/stale-context.md`
1. Extract file paths, function names, type names, CLI commands from the issue
2. Check each with `ls`, `grep`, `go doc`, etc.
3. If stale: `gh issue comment {n} --body "..."`, write `{"outcome":"stale"}`
4. If clean: write `{"outcome":"clean"}`

### `triage/already-implemented.md`
1. Read the issue's described behavior
2. Search the codebase (grep, read, etc.)
3. If implemented: `gh issue comment {n}`, `gh issue close {n}`, write `{"outcome":"implemented"}`
4. If not found: write `{"outcome":"not_implemented"}`

Both templates receive `{outcome_file}` as a variable so Claude knows where to write the result.

---

## CLI

```
factory triage run <issue>     # run triage pipeline synchronously
factory triage status <issue>  # show current stage and history
factory triage list            # list all triaged issues for this repo
```
