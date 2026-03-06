<!--
Sync Impact Report
===================
Version change: 0.0.0 (template) → 1.0.0
Bump rationale: MAJOR — initial ratification, all principles new.

Modified principles: none (first version)

Added principles:
  - Principle I: Crash-Safe Pipeline State
  - Principle II: CLI-First Interface
  - Principle III: Sequential Pipeline Processing
  - Principle IV: Testability via Interfaces
  - Principle V: Append-Only Event Log
  - Principle VI: Pipeline Configuration as Data
  - Principle VII: Prompt Transparency
  - Principle VIII: Simplicity

Added sections: Security & Data Handling, Development Workflow, Governance
Removed sections: none

Templates requiring updates:
  - .specify/templates/plan-template.md — ✅ reviewed, compatible
    (Constitution Check section present; principles are generic enough
    to apply without template changes)
  - .specify/templates/spec-template.md — ✅ reviewed, compatible
    (User Scenarios & Testing section aligns with issue format
    expectations)
  - .specify/templates/tasks-template.md — ✅ reviewed, compatible
    (Phase-based structure works with sequential processing model)

Follow-up TODOs: none

Note: Database references use PostgreSQL (pgx/v5), not SQLite.
The project migrated from go-sqlite3 to pgx before constitution
ratification.
-->

# TaintFactory Constitution

## Core Principles

### I. Crash-Safe Pipeline State

The factory MUST survive crashes, kills, and bad runs without losing
progress. Pipeline state on disk is the source of truth. Every stage
attempt MUST get its own directory containing the rendered prompt,
captured session log, check output, and structured outcome. No
pipeline operation may silently discard prior attempt data.

- `~/.factory/pipelines/{issue}/pipeline.json` is the canonical record
  of where a pipeline stands — current stage, attempt number, fix
  round, status, and full stage history.
- Each stage attempt directory (`attempt-N/`) MUST be self-contained:
  prompt, session log, outcome, and check results.
- If a pipeline is interrupted mid-advance, re-running `check-in`
  MUST safely resume from the persisted state — never re-execute a
  completed stage or overwrite a prior attempt.
- Worktree removal, session cleanup, and file writes MUST be ordered
  so that a crash at any point leaves the pipeline in a recoverable
  state.

### II. CLI-First Interface

All functionality MUST be accessible via the `factory` CLI built with
Cobra. The CLI is the primary interface for both humans and
automation. There is no API server for pipeline control — the web UI
is read-only monitoring.

- Every capability (pipeline control, session management, checks,
  queue, triage, analytics) MUST have a corresponding `factory`
  subcommand.
- Commands MUST write structured output to stdout and errors to
  stderr. Commands that produce tabular data MUST support
  `--format json` for machine consumption.
- The `factory serve` web UI MUST NOT expose mutation endpoints that
  bypass CLI command logic. All state changes flow through the same
  code paths the CLI uses.

### III. Sequential Pipeline Processing

The orchestrator MUST process one pipeline at a time. If the active
pipeline is blocked or in-progress, the factory pauses — nothing else
runs. This constraint is intentional: simplicity and predictability
over parallelism.

- `factory orchestrator check-in` MUST be idempotent — calling it
  while a pipeline is in-progress MUST be a no-op, not a second
  concurrent run.
- Queue dependencies (`--depends-on`) MUST be evaluated at dequeue
  time. An item with unresolved dependencies MUST NOT be started.
- Stage transitions within a pipeline are strictly sequential:
  implement -> review -> qa -> verify -> merge (or as configured).
  No stage may run concurrently with another stage in the same
  pipeline.

### IV. Testability via Interfaces

All external dependencies (tmux, git, GitHub CLI, filesystem) MUST be
accessed through interfaces that can be mocked in tests. The full
test suite MUST run with zero external dependencies.

- tmux operations MUST go through the `TmuxRunner` interface. Tests
  MUST use `mockTmux` with configurable return values.
- Database tests MUST use a test PostgreSQL instance or mock the
  database layer via interfaces. No test may depend on production
  data.
- Test files MUST live alongside the code they test (Go convention:
  `foo.go` -> `foo_test.go`).
- `make test` MUST pass in a clean checkout with only Go and
  PostgreSQL available — no tmux, no git repos, no GitHub
  credentials.

### V. Append-Only Event Log

The PostgreSQL event log is the system's operational memory. It
records session state transitions, check run results, and pipeline
events as an append-only time-series. Nothing is updated in place.

- Every session state change (started -> active -> idle -> exited)
  MUST be logged via `factory event log`.
- Every check run MUST record: check name, exit code, duration,
  stage, issue, and timestamp.
- The orchestrator MUST make decisions by querying the event log, not
  by polling external state that could be stale.
- Analytics commands (`factory analytics`) MUST derive all metrics
  from the event log — no secondary data stores.

### VI. Pipeline Configuration as Data

Pipeline behavior MUST be defined in YAML configuration, not in
application code. The factory provides the engine; users provide the
pipeline definition.

- Stages, checks, failure routing (`on_fail`), prompt templates,
  context modes, timeouts, and notification settings MUST all be
  configurable in `pipeline.yaml`.
- Adding a new check or stage MUST NOT require code changes — only
  YAML edits and (optionally) a new prompt template file.
- Configuration MUST be validated at load time (`factory config
  validate`). Invalid YAML MUST produce clear error messages
  referencing the problematic field.
- Triage pipelines follow the same principle: `triage.yaml` defines
  stages, outcomes, and routing.

### VII. Prompt Transparency

Every prompt sent to a Claude Code session MUST be rendered from a
template with explicit variable substitution. No hidden or
dynamically-constructed prompts.

- Templates use `{{variable}}` substitution and
  `{{#if variable}}...{{/if}}` conditionals — no arbitrary code
  execution in templates.
- Template lookup follows a defined precedence: project worktree >
  `~/.factory/templates/` > built-in compiled defaults.
- The rendered prompt MUST be saved to
  `stages/{stage}/attempt-{n}/prompt.md` before the session starts.
  If the session crashes, the exact prompt is always recoverable.
- All template variables injected by the context builder MUST be
  documented. Custom `vars` from pipeline config are passed through
  verbatim.

### VIII. Simplicity

No abstractions without immediate justification. PostgreSQL is the
sole database. JSON files on disk for pipeline state. Bash loops for
scheduling.

- Single PostgreSQL instance for all event data. No additional
  databases, caches, or message brokers.
- Pipeline state lives in plain JSON files — human-readable,
  `jq`-friendly, and trivially debuggable.
- New Go dependencies require justification. Prefer stdlib where
  reasonable.
- `internal/` packages MUST have clear, single responsibilities. No
  god-packages that mix concerns.
- Three similar lines of code are preferred over a premature
  abstraction.

## Security & Data Handling

- OAuth tokens MUST be stored in environment variables or
  `~/.factory/.env` (gitignored) — never in code, pipeline state
  files, logs, or commit history.
- Session logs (`session.log`) may contain sensitive data from the
  target repo. Pipeline artifacts at `~/.factory/` are user-local
  and MUST NOT be committed to any repository.
- The PostgreSQL event log MUST NOT store secrets, tokens, or prompt
  content — only structural metadata (event type, timestamps, issue
  numbers, exit codes).
- `--dangerously-skip-permissions` is a pipeline configuration
  choice, not a factory default. Its use MUST be explicit in
  `pipeline.yaml` under `defaults.flags`.

## Development Workflow

- **Go 1.21+** with Cobra for CLI structure
- **PostgreSQL** via `pgx/v5` for event storage
- **tmux** for session management (accessed via `TmuxRunner`
  interface)
- **git worktrees** for issue isolation

**Go boundary file convention:**
Every Go package under `internal/` MUST have a boundary file named
`<package>.go` (e.g., `checks/checks.go`). This file is the
package's public API surface.

- Boundary file contains: package doc comment, all exported type
  definitions (structs, interfaces, type aliases, constants,
  exported vars/errors), and constructor functions (`New*`).
- Boundary file MUST NOT contain: method implementations, unexported
  types, or business logic.
- The boundary file name MUST match the package directory name.
- New packages MUST create their boundary file before any
  implementation files.

**Testing workflow:**
1. Write test (`foo_test.go`) using mock interfaces and test
   PostgreSQL
2. Run `make test` — confirm the test fails
3. Implement the minimum code to pass
4. Run `make test` — confirm green
5. Commit test and implementation together

**Commits:** Clear messages, conventional commit style preferred.
PRs use branch `feature/issue-N` and reference `Closes #N` in the
body.

**Build commands:**
- `make build` — compile to `bin/factory`
- `make test` — run all tests
- `make lint` — `go vet`
- `make install` — install to `$GOPATH/bin`

## Governance

This constitution defines the non-negotiable architectural principles
and technical constraints for the taintfactory project. All
implementation decisions MUST be evaluated against these principles.

- **Precedence:** This constitution supersedes ad-hoc decisions.
  Deviations MUST be documented with justification in the relevant
  plan or spec document.
- **Amendments:** Changes to principles require updating this
  document with a version bump, rationale, and sync impact report.
  MAJOR version for principle removals or redefinitions. MINOR for
  new principles or material expansions. PATCH for clarifications.
- **Compliance:** All PRs and code reviews MUST verify alignment
  with these principles. The plan template's "Constitution Check"
  section MUST reference the current principle set.
- **Runtime guidance:** Use `CLAUDE.md` for session-specific
  development conventions (e.g., decision tracking, ADR format) that
  supplement but do not override this constitution.

**Version**: 1.1.0 | **Ratified**: 2026-03-06 | **Last Amended**: 2026-03-06
