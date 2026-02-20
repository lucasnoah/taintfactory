# Orchestrator Integration Guide

This guide documents how an orchestrator agent (e.g. Claude Code running as a long-lived agent) integrates with the `factory` CLI to drive pipelines autonomously.

## Overview

The orchestrator is a stateless agent that wakes up on a schedule, inspects all in-flight pipelines, and takes action. Between ticks it retains no memory — all state lives in the factory's SQLite database and pipeline JSON files.

```
┌──────────────┐       ┌─────────────┐       ┌──────────┐
│  Cron / Timer │──────▶│ Orchestrator │──────▶│ factory  │
│  (every 5m)   │       │   Agent      │       │   CLI    │
└──────────────┘       └─────────────┘       └──────────┘
                              │                     │
                              │  factory orchestrator status    │
                              │  factory pipeline advance       │
                              │  factory orchestrator check-in  │
                              ▼                     ▼
                        ┌──────────┐         ┌──────────┐
                        │  SQLite  │         │  tmux    │
                        │  (~/.factory/db)   │ sessions │
                        └──────────┘         └──────────┘
```

## Quick Start

### 1. Install and configure

```bash
# Build the factory CLI
make build

# Create a pipeline config (see pipeline.yaml.example)
cp pipeline.yaml.example ~/.factory/pipeline.yaml

# Verify the config
./bin/factory config validate
```

### 2. Single-command check-in

The simplest integration is a single command per tick:

```bash
factory orchestrator check-in --format=json
```

This runs the full decision tree for all pipelines and returns a JSON summary:

```json
{
  "actions": [
    {"issue": 42, "action": "advanced", "stage": "implement", "message": ""},
    {"issue": 43, "action": "skip", "stage": "review", "message": "session active, within timeout"}
  ]
}
```

### 3. Cron setup

```bash
# Run every 5 minutes
*/5 * * * * /path/to/factory orchestrator check-in --format=json >> /var/log/factory-checkin.log 2>&1
```

For a Claude Code orchestrator agent, configure the check-in as a tool call on a timer rather than a system cron.

## Decision Tree

The `factory orchestrator check-in` command implements this decision tree for each active pipeline:

```
For each pipeline where status NOT IN (completed, failed, in_progress):

  IF status == "blocked":
    → SKIP (human intervention needed)

  IF current_session exists:
    IF human detection fails (DB error):
      → SKIP (conservative: assume human present)

    IF human intervention detected:
      → SKIP (human is driving)

    IF session state == "started" or "active" or "steer" or "factory_send":
      IF elapsed since session start < timeout:
        → SKIP (let it work)
      ELSE IF steer already sent in last 10 minutes:
        → SKIP (avoid repeated steers)
      ELSE:
        → STEER "wrap up" (session exceeded timeout)

    IF session state == "idle":
      → ADVANCE pipeline

    IF session state == "exited":
      → ADVANCE pipeline

    IF session state == "human_input":
      → SKIP (human is typing)

    IF session state unknown:
      → SKIP (avoid interfering)

  IF session lookup fails (orphaned reference):
    → Clear session reference, then ADVANCE

  ELSE (no session):
    → ADVANCE pipeline

  IF ADVANCE errors:
    → ESCALATE (mark pipeline blocked to prevent infinite loops)
```

The `ADVANCE` action calls `factory pipeline advance <issue>`, which internally:
1. Runs the current stage (agent session or checks-only gate)
2. Runs check gates after the stage
3. Handles fix loops (auto-fix + agent fix rounds)
4. On success: moves to the next stage
5. On failure: retries, routes, or escalates based on `on_fail` config

## Manual Integration (Advanced)

Instead of `check-in`, you can call individual commands:

```bash
# Get full status of all pipelines
factory status --format=json

# Advance a specific pipeline
factory pipeline advance 42 --format=json

# Retry a failed stage
factory pipeline retry 42 --reason "dependency fixed"

# Steer a running session
factory session steer sess-42 "Please wrap up"

# Mark a pipeline as failed
factory pipeline fail 42 --reason "requirements changed"

# Abort and clean up
factory pipeline abort 42 --remove-worktree
```

## Orchestrator System Prompt

Below is an example system prompt for a Claude Code agent acting as the orchestrator:

```
You are the factory orchestrator. Your job is to manage software development
pipelines using the `factory` CLI. You run on a 5-minute timer.

On each tick:
1. Run: factory orchestrator check-in --format=json
2. Review the actions taken
3. If any pipeline was escalated, create a GitHub issue comment requesting help
4. If all pipelines are idle, check for new issues to start

To start a new pipeline:
  factory pipeline create <issue-number>

To check pipeline status:
  factory status --format=json

To see detailed status for one pipeline:
  factory pipeline status <issue> --format=json

To manually retry a failed stage:
  factory pipeline retry <issue> --reason "<reason>"

Rules:
- Never run more than 3 pipelines concurrently
- If a pipeline is blocked for more than 1 hour, comment on the GitHub issue
- If a stage fails 3 times, escalate to human
- Let active sessions work until their timeout expires
- If a human has intervened in a session, do not interfere
```

## Monitoring and Alerting

### Log monitoring

The check-in command returns structured JSON. Monitor for these action types:

| Action | Meaning | Alert? |
|--------|---------|--------|
| `skip` | Normal — session running or human driving | No |
| `advanced` | Pipeline moved to next stage | No |
| `completed` | Pipeline finished successfully | Info |
| `steer` | Session exceeded timeout, sent wrap-up | Warning |
| `retry` | Stage failed, retrying | Warning |
| `escalate` | Max retries exceeded, needs human | Alert |
| `fail` | Unrecoverable error | Alert |

### Analytics queries

Use the analytics commands to monitor long-term health:

```bash
# Average stage durations
factory analytics stage-duration --format=json

# Check failure rates
factory analytics check-failure-rate --format=json

# Weekly throughput
factory analytics pipeline-throughput --since 2024-01-01
```

### Session health

```bash
# List all sessions with orphan detection
factory session list

# Peek at a running session's output
factory session peek <session-name> --lines 50
```

## Troubleshooting

### Stale sessions

**Symptom**: `factory session list` shows sessions with `orphan: no-tmux` (DB says active, but tmux session is gone).

**Fix**: The session crashed or was killed externally. The next `check-in` will try to advance past it. If the pipeline is stuck, manually retry:

```bash
factory pipeline retry <issue> --reason "stale session cleanup"
```

### Orphaned tmux sessions

**Symptom**: `factory session list` shows sessions with `orphan: no-db` (tmux running but no DB record).

**Fix**: These are tmux sessions not managed by factory. Kill them manually:

```bash
tmux kill-session -t <session-name>
```

### DB lock contention

**Symptom**: `database is locked` errors.

**Fix**: SQLite uses file-level locking. Ensure only one `factory` process writes at a time. The check-in command is designed to be the single writer. If you run multiple processes, use WAL mode (already enabled by default).

### Pipeline stuck in `in_progress`

**Symptom**: A pipeline shows `in_progress` but no session is running.

**Fix**: The process may have crashed mid-advance. Reset and retry:

```bash
factory pipeline retry <issue> --reason "stuck in_progress recovery"
```

### Max fix rounds exceeded

**Symptom**: Stage keeps failing after fix attempts.

**Fix**: Check what's failing:

```bash
factory analytics check-failures --format=json
factory analytics issue-detail <issue> --format=json
```

Then either fix the underlying issue manually and retry, or adjust the pipeline config to increase `max_fix_rounds`.
