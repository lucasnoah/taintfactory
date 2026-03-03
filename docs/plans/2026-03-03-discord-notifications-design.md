# Discord Stage Notifications

**Date**: 2026-03-03
**Status**: Design approved

## Problem

Pipeline stages complete silently. The only way to know a stage finished is to watch the web UI or check the runner pane. When multiple issues are in flight across projects, it's easy to miss a failure or a contract-check finding that needs follow-up.

## Design

A standalone `factory discord` poller command watches `pipeline_events` for new stage transitions and posts rich Discord embeds for each completed stage. The orchestrator is untouched — notifications are best-effort and fully decoupled from pipeline execution.

### Architecture

```
pipeline_events (SQLite)
        ↓  poll every N seconds
factory discord [run]
        ↓  for each unseen stage_advanced/completed/failed event
  1. load pipeline.yaml for namespace → read notifications.discord.webhook_url
  2. read session log from ~/.factory/pipelines/{ns}/{issue}/stages/{stage}/attempt-{n}/session.log
  3. read git diff for the stage (before/after branch commits)
  4. call claude --print with stage-type-specific prompt → 1-2 sentence summary
  5. POST rich embed to Discord webhook
  6. persist last-seen event ID to avoid re-posting
```

### pipeline.yaml Config Block

```yaml
notifications:
  discord:
    webhook_url: "https://discord.com/api/webhooks/XXXXXXX/YYYYYYY"
    thread_per_issue: true   # optional, default false
```

Projects without `notifications.discord` are silently skipped.

### Stage-Type Prompts

**implement** — focus on what was built:
> "In 2-3 sentences: what was implemented, which files were created or modified, and what tests were added. Be specific about function names or schema changes."

**review / qa** — focus on flags and changes:
> "In 2-3 sentences: what did the reviewer flag or change, and why? Highlight any bugs caught or design decisions reconsidered. List any open questions or TODOs left unresolved."

**contract-check** — focus on contract violations found:
> "In 2-3 sentences: what contract violations or gaps were found, what was fixed, and what open questions (if any) remain for the next implementer."

**verify / merge** — short status only, no session log needed:
> Use static message: "All checks passed." / "Merged to main."

### Discord Embed Shape

**Agent stages (implement / review / qa / contract-check):**

```
[color bar: green=success, red=fail, yellow=fix rounds > 0]
Title:   #285 implement ✅   mbrucker/deathcookies
Fields:
  Duration      │ 10m 32s
  Fix Rounds    │ 0
  Summary       │ Added pl_accounts, pl_targets, pl_actuals_manual tables
                │ with 9 sqlc queries and 11 integration tests.
  Changes       │ ListPLAccounts: added WHERE active=true filter.
                │ UpsertPLTarget: added ON CONFLICT clause.
  Open Questions│ —
Footer: stage 2 of 6
```

**verify / merge / completed:**

```
[color bar: green]
Title:   #285 completed ✅   mbrucker/deathcookies
Fields:
  Total Duration │ 49m 3s
  Stages         │ implement → review → qa → verify → merge → contract-check
Footer: P&L DB layer: schema + sqlc queries
```

**Thread-per-issue mode:** first stage notification creates a Discord thread named `#285 — <issue title>`. All subsequent stages post into the thread. Final `completed`/`failed` embed posts to the main channel as a summary.

### Poller State

Last-seen `pipeline_events.id` persisted to `~/.factory/discord_cursor.json`:

```json
{ "last_event_id": 4821 }
```

Poller runs as: `factory discord run --interval 15s`

### Data Sources Available to Poller

| Data | Source |
|------|--------|
| Stage outcome, duration | `pipeline.json` StageHistory |
| Fix rounds, checks passed | `pipeline.json` StageHistory |
| Session log (Claude output) | `~/.factory/pipelines/{ns}/{issue}/stages/{stage}/attempt-{n}/session.log` |
| Git diff for stage | `git diff` between stage-start and stage-end commits on worktree branch |
| Issue title | `pipeline.json` Title field |
| Config / webhook URL | `pipeline.yaml` at `config_path` from `issue_queue` |

## Implementation Scope

| Component | Location | What |
|-----------|----------|------|
| Config parsing | `internal/pipeline/config.go` | Add `NotificationsConfig` struct to PipelineConfig |
| Poller command | `internal/cli/discord.go` | `factory discord run` — poll, summarize, post |
| Summarizer | `internal/discord/summarize.go` | Stage-type-specific claude --print calls |
| Embed builder | `internal/discord/embed.go` | Construct Discord embed JSON payloads |
| Webhook poster | `internal/discord/webhook.go` | HTTP POST to Discord webhook URL |
| Cursor state | `internal/discord/cursor.go` | Read/write `~/.factory/discord_cursor.json` |

## Out of Scope

- DM notifications to specific users
- Slash commands / bot (webhook-only, no bot token needed)
- Editing previous messages on re-run
- Notification for check-level detail (per-check pass/fail) — stage level only
