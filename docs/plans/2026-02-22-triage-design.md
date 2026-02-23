# Triage Pipeline — Design Doc

_Date: 2026-02-22_

## Overview

A parallel pipeline system alongside the existing dev pipeline. Where the dev pipeline
executes development work on issues, the triage pipeline processes GitHub issues to
determine if they are still valid before anyone starts working on them.

Triage pipelines:
- Run agents in the main repo (no worktrees, no code changes)
- Have no check runners or fix loops
- Are async — advanced by the same `factory orchestrator check-in` loop as dev pipelines
- Output GitHub actions (comments, labels, close) via `gh` CLI

Initial stages: `stale_context` and `already_implemented`.

---

## Architecture

### Approach: Clean separation (Option B)

New `internal/triage` package owns its own config, state, and runner. The orchestrator's
`check-in` command calls `triage.Runner.Advance()` alongside existing pipeline advancement.
Reuses `internal/session`, `internal/github`, and the template system without touching
their internals.

### New files

| File | Purpose |
|---|---|
| `internal/triage/config.go` | `TriageConfig`, `TriageStage` types; YAML loading from `triage.yaml` |
| `internal/triage/state.go` | `TriageState` type; JSON load/save to `~/.factory/triage/{repo_slug}/{issue}.json` |
| `internal/triage/runner.go` | `Runner.Enqueue(issue)` and `Runner.Advance()` — the check-in hook |
| `internal/triage/templates/` | Embedded default prompt templates |
| `internal/cli/triage.go` | `factory triage run/status/list` commands |

### Modified files

- `internal/orchestrator/orchestrator.go` — `CheckIn()` calls `triage.Runner.Advance()`
- `internal/cli/orchestrator.go` — wires triage runner into check-in command
- `cmd/factory/main.go` + `internal/cli/root.go` — registers `triage` command

---

## Config: `triage.yaml` (in the target repo root)

```yaml
triage:
  name: "My Repo"
  repo: "owner/repo"

stages:
  - id: stale_context
    prompt_template: triage/stale-context.md   # optional override; falls back to embedded default
    timeout: 15m
    outcomes:
      stale: done
      clean: already_implemented

  - id: already_implemented
    prompt_template: triage/already-implemented.md
    timeout: 15m
    outcomes:
      implemented: done
      not_implemented: done
```

---

## State: `~/.factory/triage/{repo_slug}/{issue}.json`

```json
{
  "issue": 42,
  "repo": "owner/repo",
  "current_stage": "stale_context",
  "status": "pending|in_progress|completed|blocked",
  "current_session": "triage-42-stale_context",
  "stage_history": [
    { "stage": "stale_context", "outcome": "clean", "duration": "45s" }
  ],
  "updated_at": "2026-02-22T..."
}
```

### Outcome file: `~/.factory/triage/{repo_slug}/{issue}/{stage_id}.outcome.json`

Agent writes this as its final act. Runner reads it after `WaitIdle` returns.

```json
{ "outcome": "clean", "summary": "All referenced symbols found" }
```

---

## Runner logic

### `Runner.Enqueue(issue)`
Called by `factory triage run <issue>`:
1. Fetch issue title/body via `gh issue view`
2. Init state: `status=pending`, `current_stage=` first stage in config
3. Immediately kick off first session (enqueue + start in one step)

### `Runner.Advance()` (called each orchestrator check-in)
1. Load all triage states with `status=in_progress`
2. For each: check if `current_session` has gone idle (via DB `GetSessionState`)
3. If idle → read `{stage_id}.outcome.json`
4. Record outcome + duration in `stage_history`
5. Look up next stage from `outcomes` map; `"done"` → mark `completed`
6. If routing to next stage: create new session, send rendered prompt, set `status=in_progress`

---

## Prompt templates

Templates are markdown files rendered with `text/template`. Default templates are embedded
in the binary via `go:embed`; repos can override by placing files at `triage/<stage_id>.md`
in the repo root.

### Template variables

| Var | Value |
|---|---|
| `{{.issue_number}}` | e.g. `42` |
| `{{.issue_title}}` | from `gh issue view` |
| `{{.issue_body}}` | from `gh issue view` |
| `{{.repo_root}}` | absolute path to the repo |
| `{{.outcome_file}}` | absolute path where agent must write outcome JSON |
| `{{.stage_id}}` | e.g. `stale_context` |

### Agent contract

The agent's final act is to write `{"outcome": "<key>", "summary": "..."}` to `{{.outcome_file}}`.

### Sessions

Named `triage-{issue}-{stage_id}`, run in the repo root (no worktree).

---

## CLI

```
factory triage run <issue>     # enqueue and start triage pipeline
factory triage status <issue>  # show current stage and history
factory triage list            # list all triage pipelines for this repo
```
