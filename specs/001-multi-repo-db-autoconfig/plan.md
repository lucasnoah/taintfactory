# Implementation Plan: Multi-Repo Database Autoconfiguration

**Branch**: `001-multi-repo-db-autoconfig` | **Date**: 2026-03-06 | **Spec**: [spec.md](spec.md)
**Input**: Feature specification from `/specs/001-multi-repo-db-autoconfig/spec.md`

## Summary

Add per-repo PostgreSQL database provisioning to the factory. Pipeline configs gain `database:` and `env:` sections. A new `dbprov` package provisions databases idempotently on the shared sidecar — connecting to the `postgres` maintenance database by overriding the database component of `DATABASE_URL`. The session manager injects env vars into tmux before launching Claude. The orchestrator's existing `runSetupWith` is extended to inject DATABASE_URL and custom env vars into setup command subprocesses (setup runs once per pipeline during `Create()`, not per-stage). The entrypoint re-provisions on pod restart.

## Technical Context

**Language/Version**: Go 1.21+
**Primary Dependencies**: Cobra (CLI), pgx/v5 (PostgreSQL driver, already imported), gopkg.in/yaml.v3 (config parsing)
**Storage**: PostgreSQL (shared sidecar, admin connection via DATABASE_URL env var)
**Testing**: `go test ./...` with mock interfaces and test PostgreSQL
**Target Platform**: Linux (Kubernetes pod) + macOS (local development)
**Project Type**: CLI tool
**Performance Goals**: Provisioning completes in under 5 seconds per database
**Constraints**: Must be idempotent. Must not block pipelines when admin DB unavailable.
**Scale/Scope**: 1-10 repos per factory instance, each with one database

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| Principle | Status | Notes |
|-----------|--------|-------|
| I. Crash-Safe Pipeline State | PASS | Provisioning is a pre-stage step; if it crashes, re-running check-in retries safely. No pipeline state is modified during provisioning. |
| II. CLI-First Interface | WATCH | New `factory repo provision-db` command exposed via Cobra. Currently missing `--format json` support for machine-consumable output — add if constitution requires it for all tabular commands. |
| III. Sequential Pipeline Processing | PASS | Provisioning runs synchronously in the orchestrator's `Create()`. Setup commands run once per pipeline (not per stage) via the existing `runSetupWith`, extended with env var injection. No concurrent access. |
| IV. Testability via Interfaces | PASS | `dbprov.BuildProvisionSQL` is pure (testable without DB). `Provision()` takes `*sql.DB` interface. Session env injection testable via mock tmux. |
| V. Append-Only Event Log | N/A | Provisioning doesn't interact with the event log. |
| VI. Pipeline Configuration as Data | PASS | `database:` and `env:` are new YAML config sections. Behavior driven by config, not code. |
| VII. Prompt Transparency | N/A | No prompt changes. |
| VIII. Simplicity | PASS | Single new package (`dbprov`). Uses `database/sql` stdlib with the existing `pgx/v5` driver — no new external dependencies added. |
| Boundary File Convention | PASS | New `dbprov` package will have `dbprov.go` boundary file containing all exported types and function signatures. |
| Security | WATCH | Database passwords in pipeline.yaml committed to repo. Accepted per spec assumptions — secrets management deferred. Additionally, `DatabaseConfig.URL()` must URL-encode the password to prevent malformed connection strings with special characters. |

**Gate result: PASS** — no violations.

## Project Structure

### Documentation (this feature)

```text
specs/001-multi-repo-db-autoconfig/
├── plan.md              # This file
├── research.md          # Phase 0 output
├── data-model.md        # Phase 1 output
├── quickstart.md        # Phase 1 output
├── contracts/           # Phase 1 output
│   └── cli-commands.md  # New CLI command contracts
└── tasks.md             # Phase 2 output (/speckit.tasks)
```

### Source Code (repository root)

```text
internal/
├── config/
│   ├── types.go            # MODIFY: add DatabaseConfig, Env to Pipeline
│   └── validate.go         # MODIFY: add DatabaseConfig field validation
├── dbprov/
│   ├── dbprov.go           # CREATE: boundary file (exported types + function signatures)
│   ├── provision.go        # CREATE: BuildProvisionSQL, Provision implementations
│   └── provision_test.go   # CREATE: tests (including special-char password cases)
├── session/
│   └── session.go          # MODIFY: add Env to CreateOpts, export in Create
├── orchestrator/
│   └── orchestrator.go     # MODIFY: extend runSetupWith to accept + inject env vars
├── stage/
│   └── engine.go           # MODIFY: wire env vars into session.CreateOpts.Env
├── cli/
│   └── repo.go             # MODIFY: add provision-db command, wire on repo add
└── ...

deploy/
└── entrypoint.sh           # MODIFY: add provision-db call after repo cloning
```

**Structure Decision**: Follows existing `internal/` package layout. One new package (`dbprov`) with single responsibility. All other changes modify existing files. Setup commands remain in the orchestrator's `runSetupWith` (called once per pipeline during `Create()`), not in the stage engine — the stage engine only wires env vars into `CreateOpts.Env` for session creation.
