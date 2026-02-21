# taintfactory

A CLI-driven software factory that orchestrates Claude Code sessions through configurable pipelines — automating the full lifecycle from GitHub issue to merged PR.

You define a pipeline with stages (implement, review, QA), checks (lint, test), and failure recovery strategies. A cron-driven orchestrator evaluates all in-flight issues, spawns Claude Code sessions in tmux, runs check gates, and routes failures back through the pipeline automatically. You can attach to any session at any time. Everything is persisted, so a crash or a bad run never loses progress — you pick up exactly where things left off.

## How It Works

```
Cron (every ~5min)
  └─▶ factory orchestrator check-in
        ├─ For each in-flight pipeline, decide: skip / steer / advance
        └─ factory pipeline advance [issue]
              ├─ Render and save prompt for current stage
              ├─ Spawn Claude Code session in tmux
              ├─ Wait for session to go idle
              ├─ Run check gate (lint, test, etc.)
              │    ├─ PASS ──▶ record outcome, move to next stage
              │    └─ FAIL ──▶ send fix prompt, retry (up to max_fix_rounds)
              │         └─ still failing ──▶ on_fail: jump to earlier stage
              └─ Repeat until merge or blocked
```

Claude Code sessions run in tmux. Hooks inside each session call `factory event log` to record state transitions (started → active → idle → exited) into SQLite. The orchestrator reads this state to know when a session has finished and whether to advance, steer, or wait.

### Failure recovery

taintfactory is designed around the assumption that things will go wrong. There are three layers of recovery:

**1. Fix rounds** — when checks fail after a stage, the orchestrator sends Claude a new prompt containing the structured check failures and asks it to fix them. This repeats up to `max_fix_rounds` times before escalating.

**2. Stage routing** — if fix rounds are exhausted, `on_fail` in the stage config routes the pipeline back to an earlier stage (e.g. a failed review stage drops back to implement). The attempt counter increments so history is never overwritten.

**3. Blocked status** — if a pipeline can't proceed automatically (e.g. a stage fails with no `on_fail` configured, or a goal gate requires human sign-off), it's marked `blocked`. You address it manually and resume:

```bash
factory pipeline retry 42           # re-run the current stage from scratch
factory pipeline retry 42 --reason "switched to a different auth approach"
factory session send my-session "try a different approach to the auth middleware"
```

Every prior attempt's prompt, session log, check output, and outcome is preserved on disk, so you can always inspect what happened and why.

### Persistence

State is stored in two layers that serve different purposes:

**Pipeline state (JSON files)** — `~/.factory/pipelines/{issue}/pipeline.json` is the source of truth for where a pipeline stands. It records the current stage, attempt number, fix round, status, and the full stage history. Each stage attempt gets its own directory with everything needed to understand what happened:

```
~/.factory/pipelines/42/
  pipeline.json                        ← current stage, status, full history
  stages/
    implement/
      attempt-1/
        prompt.md                      ← exact prompt sent to Claude
        session.log                    ← full tmux scrollback captured on session end
        outcome.json                   ← success/fail, summary, files changed
        summary.json                   ← fix rounds, durations, auto-fix counts
        checks/
          lint/                        ← raw lint output
          post-gate-0/                 ← gate result after agent run
          post-gate-1/                 ← gate result after fix round 1
      attempt-2/                       ← after on_fail retry, history intact
        ...
```

**Session output** — when a session ends (or is killed), taintfactory runs `tmux capture-pane -S -` to capture the entire scrollback buffer and writes it to `session.log`. This is the complete terminal output from the Claude session — every tool call, every file it read, every decision it made. While a session is still running, you can sample the live output without attaching:

```bash
factory session peek my-session          # last 50 lines
factory session peek my-session --lines 200
```

After the session ends, the log is always at:
```
~/.factory/pipelines/{issue}/stages/{stage}/attempt-{n}/session.log
```

**SQLite event log** — `~/.factory/factory.db` records a time-series of everything that happens at the system level: session state transitions (started → active → idle → exited), individual check run results with exit codes and durations, and pipeline events (checks passed, PR created, merged). This is what the orchestrator queries to make decisions and what `factory analytics` queries for performance metrics. It's append-only — nothing is updated in place.

## Installation

```bash
git clone https://github.com/lucasnoah/taintfactory
cd taintfactory
make install   # installs to $GOPATH/bin/factory
```

**Requirements:** Go 1.21+, tmux, git, [gh CLI](https://cli.github.com/)

## Pipeline Configuration

Pipelines are defined in `~/.factory/pipeline.yaml`. Each pipeline describes the repo, the checks available, and the ordered stages to run. Here's a real example from a 3-tier web app (Python ETL + Go API + Next.js):

```yaml
pipeline:
  name: deathcookies
  repo: github.com/mbrucker/deathcookies

  max_fix_rounds: 2          # max auto-fix cycles per stage before failing
  fresh_session_after: 10    # start a fresh Claude session after N stages

  setup:
    - "go mod download"
    - "cd web && npm install"
    - "uv sync"

  defaults:
    timeout: "45m"
    flags: "--dangerously-skip-permissions"

  vars:
    env_setup: |
      This is a 3-tier project: Python ETL, Go API, Next.js web.

      ### PostgreSQL
      - Start: `make db` (docker-compose, postgres:16-alpine on port 5433)
      - URL: `postgres://deathcookies:deathcookies@localhost:5433/deathcookies?sslmode=disable`
      - Migrate: `make migrate-up`

      ### Running the app
      - Go API: `make dev-api` (port 8080)
      - Next.js: `cd web && npm run dev` (port 3000)
      - All tests: `make test`

  checks:
    lint-py:
      command: "uv run ruff check ."
      parser: generic
      timeout: "2m"
    lint-go:
      command: "go vet ./..."
      parser: generic
      timeout: "2m"
    lint-web:
      command: "cd web && npm run lint"
      parser: generic
      timeout: "2m"
    test-py:
      command: "uv run pytest"
      parser: generic
      timeout: "5m"
    test-go:
      command: "go test ./..."
      parser: generic
      timeout: "5m"

  stages:
    - id: implement
      type: agent
      checks_after:
        - lint-py
        - lint-go
        - lint-web
        - test-py
        - test-go
      on_fail: implement        # retry this stage on check failure

    - id: review
      type: agent
      context_mode: code_only   # reviewer only sees code + findings
      checks_after:
        - lint-py
        - lint-go
        - lint-web
        - test-py
        - test-go
      on_fail: implement        # send back to implement on failure

    - id: qa
      type: agent
      context_mode: full
      checks_after:
        - lint-py
        - lint-go
        - lint-web
        - test-py
        - test-go
      on_fail: implement

    - id: verify
      type: checks_only         # no agent — just run the gates
      checks:
        - lint-py
        - lint-go
        - lint-web
        - test-py
        - test-go

    - id: merge
      type: merge
      merge_strategy: squash
```

### Schema Reference

| Field | Description |
|---|---|
| `name` | Pipeline/project name |
| `repo` | GitHub repo (`org/repo`) |
| `max_fix_rounds` | Max auto-fix iterations per stage |
| `fresh_session_after` | Start new Claude session after N stages |
| `setup` | Commands to run when creating a new worktree |
| `defaults.timeout` | Default stage timeout |
| `defaults.flags` | Default `claude` flags (e.g. `--dangerously-skip-permissions`) |
| `defaults.model` | Default Claude model |
| `vars` | Template variables injected into prompts |
| `checks` | Named checks with `command`, `parser`, `timeout`, optional `auto_fix`/`fix_command` |
| `stages[].id` | Stage identifier |
| `stages[].type` | `agent`, `checks_only`, or `merge` |
| `stages[].checks_before` | Checks to run before the agent |
| `stages[].checks_after` | Checks to run after the agent |
| `stages[].checks` | Checks for `checks_only` stages |
| `stages[].on_fail` | Stage to jump to on check failure |
| `stages[].context_mode` | What context to inject: `full`, `code_only`, `findings_only`, `minimal` |
| `stages[].merge_strategy` | `squash`, `merge`, or `rebase` for merge stages |

**Check parsers:** `generic`, `eslint`, `typescript`, `vitest`, `prettier`, `npm-audit`

## Prompt Templates

Each agent stage is driven by a prompt template. When the orchestrator advances a stage, it renders the template for that stage with context variables and sends the result to Claude Code as its initial prompt.

### Template lookup order

For a stage with `prompt_template: "templates/implement.md"`, the loader checks in order:

1. `{worktree}/templates/implement.md` — project-level override committed alongside your code
2. `~/.factory/templates/implement.md` — user-level override
3. Built-in compiled template — fallback if neither exists

If `prompt_template` is omitted from a stage config, the stage ID is used as the filename (e.g. stage `implement` → `implement.md`).

### Template syntax

Templates use `{{variable}}` for substitution and `{{#if variable}}...{{/if}}` for conditional blocks:

```
{{issue_title}}              → replaced with the variable value
{{#if check_failures}}
...                          → block included only if check_failures is non-empty
{{/if}}
```

### Built-in variables

These are automatically injected by the context builder. Which ones are populated depends on the stage's `context_mode`.

| Variable | Available in | Description |
|---|---|---|
| `issue_number` | all | GitHub issue number |
| `issue_title` | all | Issue title |
| `issue_body` | all | Full issue body text |
| `feature_intent` | all | LLM-derived intent summary |
| `worktree_path` | all | Absolute path to the git worktree |
| `branch` | all | Working branch name |
| `stage_id` | all | Current stage ID |
| `attempt` | all | Current attempt number (increments on retry) |
| `goal` | all | `#42: Issue title` shorthand |
| `acceptance_criteria` | full, code_only, findings_only | Goal gate criteria if set |
| `git_diff_summary` | full, code_only | `git diff --stat` summary |
| `files_changed` | full, code_only | List of changed files |
| `git_commits` | full, code_only | Recent commit log |
| `prior_stage_summary` | full, findings_only | Outcomes from completed stages |
| `check_failures` | all (when present) | Formatted check failure output from prior attempt |

Any keys defined under `vars` in your pipeline config (or stage config) are also injected and can be referenced in templates.

### Context modes

| Mode | What's included |
|---|---|
| `full` | All variables: git diff, commits, files, prior stage summaries, acceptance criteria |
| `code_only` | Git diff, commits, files, acceptance criteria — no prior stage reasoning |
| `findings_only` | Acceptance criteria + structured findings from the most recent stage only |
| `minimal` | Base variables only (issue, branch, stage, worktree) |

### Built-in templates

taintfactory ships with built-in templates for the standard stages. You can override any of them by placing a file at `~/.factory/templates/<name>` or in your project's worktree.

| Template | Stage |
|---|---|
| `implement.md` | Initial implementation |
| `review.md` | Code review with fix-in-place |
| `qa.md` | QA testing with fix-in-place |
| `fix-checks.md` | Sent on each check-failure fix round |
| `merge.md` | Final merge stage |

### Example: `implement.md`

```markdown
# Implement: {{issue_title}}

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
```

On a retry (after check failures), `check_failures` is populated with the structured output from the failing checks, so Claude knows exactly what to fix. On the first attempt it's omitted entirely.

You can reference any pipeline `vars` key here too. For example, the deathcookies pipeline defines an `env_setup` var with database URLs and run commands — adding `{{env_setup}}` to the template injects that into every prompt for that project.

## Issue Format

The pipeline is only as good as the issue it's working from. taintfactory expects GitHub issues to follow a structured format that gives Claude everything it needs — intent, testable user stories, requirements, and explicit scope boundaries. The full template with guidance and a worked example is in [`docs/feature-request-template.md`](docs/feature-request-template.md).

### Principles

- **No code, only requirements.** The issue describes what the system should do, not how to build it. No file paths, function names, or implementation details.
- **Testable from the outside.** User stories must be verifiable by someone who has never seen the codebase — just a browser and the UI.
- **User intent first.** Every feature exists because a real person has a real problem. Start there. If you can't articulate the pain, you don't understand the feature.
- **Explicit scope boundaries.** What you're NOT building is as important as what you are. Unstated non-requirements become scope creep.

### Structure

```markdown
### User Intent
Narrative description of the real-world problem and who it affects.

### User Stories
Testable scenarios written from the user's perspective. Each story has:
- Narrative: "As a [role], I [action] and [expected outcome]."
- Preconditions: what must exist before the story can be exercised
- Assertions: observable outcomes the tester checks

### Requirements
Functional spec organized by component or capability. Specific enough to
implement from, but not prescribing architecture.

### Affected Surfaces
Table of every user-facing touchpoint that changes.

### Non-Requirements
Explicitly what is out of scope.

### Open Questions
Decisions left for the implementer or a future conversation.
```

When you run `factory queue add [issue]`, the issue body is fetched from GitHub and passed as `{{issue_body}}` into every stage prompt. The `feature_intent` variable is derived from it by LLM — a tighter one-sentence summary used to orient each stage. Well-structured issues mean better-oriented agents.

## Running a Pipeline

**1. Add an issue to the queue:**
```bash
factory queue add 42
# TaintFactory fetches the issue from GitHub and derives intent via LLM
# Or provide intent manually:
factory queue add 42 --intent "Add dark mode toggle to settings page"
```

**2. Create the pipeline:**
```bash
factory pipeline create 42
# Creates a git worktree, initializes pipeline state
```

**3. Run the orchestrator loop:**

The recommended way to drive the factory is a bash loop in a dedicated tmux session. Each `check-in` call is blocking — it runs the current stage to completion before returning — so the sleep between iterations is just a gap between stages.

```bash
# Start a dedicated tmux session for the orchestrator
tmux new-session -d -s factory-runner

# Export OAuth token, then start the loop (2 min between stages)
tmux send-keys -t factory-runner "export CLAUDE_CODE_OAUTH_TOKEN=<your-token>" Enter
tmux send-keys -t factory-runner "cd /path/to/your/repo && while true; do factory orchestrator check-in; sleep 120; done" Enter
```

Or run directly in your current shell:
```bash
export CLAUDE_CODE_OAUTH_TOKEN=<your-token>
while true; do factory orchestrator check-in; sleep 120; done
```

The loop will:
- Advance in-flight pipelines stage by stage (implement → review → qa → verify → merge)
- Pick up the next queued issue automatically when all active pipelines complete
- Sleep 2 minutes between each stage transition

**OAuth token:** The factory reads `CLAUDE_CODE_OAUTH_TOKEN` from the environment when creating Claude sessions. You can also store it in `~/.factory/.env`:
```
CLAUDE_CODE_OAUTH_TOKEN=sk-ant-oat01-...
```
The factory will load it automatically from there if not already in your environment.

**Cron alternative** (if you prefer system cron over a loop):
```bash
*/2 * * * * export CLAUDE_CODE_OAUTH_TOKEN=<token> && /path/to/factory orchestrator check-in
```

**4. Monitor progress:**
```bash
factory status                    # all in-flight pipelines
factory pipeline status 42        # detailed view of issue #42
factory check history 42          # check results per stage
factory analytics stage-duration  # performance metrics
```

**5. Attach to a running session:**
```bash
factory session list              # see active sessions
tmux attach -t [session-name]     # watch Claude work in real time
```

**6. Send a message or steer:**
```bash
factory session send my-session "focus on the API layer, ignore the frontend for now"
factory session steer my-session "wrap up and push your changes"
```

## CLI Reference

### `factory pipeline`
```
create [issue]           Create a pipeline for a GitHub issue
advance [issue]          Advance to the next stage
list [--status]          List all pipelines
status [issue]           Detailed status for an issue
retry [issue]            Retry the current stage
fail [issue]             Mark a pipeline as failed
abort [issue]            Abort and clean up
cleanup [issue|--all]    Remove worktree and pipeline data
```

### `factory session`
```
create [name]            Spawn a Claude Code tmux session
kill [name]              Kill a session and capture logs
list [--issue]           List active sessions
send [name] [prompt]     Send a prompt to a running session
steer [name] [msg]       Send a steering message
peek [name]              Show recent session output
status [name]            Check session state
wait-idle [name]         Block until idle or exited
```

### `factory check`
```
run [issue] [checks...]  Run checks in the issue's worktree
gate [issue] [stage]     Run all checks for a stage
result [issue] [check]   Show latest check result
history [issue]          Show all check runs for an issue
```

### `factory context`
```
build [issue] [stage]    Build the prompt for a stage
render [issue] [stage]   Preview the rendered prompt
checkpoint [issue] [stage] [outcome]  Save stage outcome
read [issue] [stage]     Read saved context
```

### `factory queue`
```
add [issue...]           Add issues to the queue
list                     List queued issues
remove [issue]           Remove from queue
clear [--confirm]        Remove all items
set-intent [issue] [intent]  Set the feature intent
```

### `factory orchestrator`
```
check-in                 Run the full decision loop for all pipelines
status                   Show orchestrator-friendly pipeline status
```

### `factory pr`
```
create [issue]           Create a PR for the issue
merge [issue]            Merge the PR
```

### `factory analytics`
```
stage-duration           Avg and p95 duration per stage
check-failure-rate       Failure rate by stage
check-failures           Which checks fail most
fix-rounds               Distribution of fix rounds
pipeline-throughput      Weekly throughput
issue-detail [issue]     Full event timeline for an issue
```

### Other
```
factory worktree create/remove/path [issue]
factory config validate/show [-f pipeline.yaml]
factory event log [--session] [--event] [--issue] [--stage]
factory db migrate / db reset
factory status
factory version
```

## Data & Storage

| Path | Contents |
|---|---|
| `~/.factory/factory.db` | SQLite: sessions, events, check runs, queue, pipeline events |
| `~/.factory/pipeline.yaml` | Your pipeline configuration |
| `~/.factory/pipelines/{issue}/pipeline.json` | Per-issue pipeline state |
| `~/.factory/pipelines/{issue}/checks/` | Check output per stage/round |
| `{repo}/worktrees/issue-{n}/` | Git worktree for each issue |

## Development

```bash
make build      # build to bin/factory
make test       # run all tests
make lint       # go vet
make install    # install to $GOPATH/bin
make clean      # remove bin/
```

Tests use in-memory SQLite and a mock tmux runner — no external dependencies needed to run the test suite.

## Status

Under active development.
