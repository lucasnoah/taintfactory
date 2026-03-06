# Implementation Plan: Deploy Pipeline

**Branch**: `002-deploy-pipeline` | **Date**: 2026-03-06 | **Spec**: [spec.md](spec.md)
**Input**: Feature specification from `/specs/002-deploy-pipeline/spec.md`

## Summary

Add a manually-triggered deploy pipeline system to taintfactory. Deploy pipelines are a second pipeline type alongside implementation pipelines, keyed by commit SHA instead of issue number. They reuse the existing stage engine for Claude agent sessions and the orchestrator's check-in loop for advancement. Stages are configured in a `deploy:` section of `pipeline.yaml`. The system supports configurable failure routing with rollback capability and provides CLI commands (`factory deploy create/list/status`) plus a web UI page (`/deploys`) for monitoring.

## Technical Context

**Language/Version**: Go 1.21+
**Primary Dependencies**: Cobra (CLI), pgx/v5 (PostgreSQL), gopkg.in/yaml.v3 (config), html/template (web UI)
**Storage**: PostgreSQL (deploys table) + JSON files on disk (`~/.factory/deploys/{sha}/deploy.json`)
**Testing**: `go test` with mock interfaces and temp directories (no external dependencies)
**Target Platform**: macOS / Linux (CLI + local web UI)
**Project Type**: CLI tool with web monitoring UI
**Performance Goals**: CLI response <1s, check-in loop advancement <2min cycle
**Constraints**: Sequential processing (one deploy at a time), crash-safe state on disk
**Scale/Scope**: Single operator, single deploy at a time per orchestrator instance

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| Principle | Status | Notes |
|-----------|--------|-------|
| I. Crash-Safe Pipeline State | PASS | Deploy state persisted as JSON on disk at `~/.factory/deploys/{sha}/deploy.json`. Stage attempt directories follow existing pattern. Re-running check-in safely resumes from persisted state. |
| II. CLI-First Interface | PASS | All deploy operations via `factory deploy create/list/status` commands. Web UI is read-only. |
| III. Sequential Pipeline Processing | PASS | One deploy at a time. Deploy check-in integrated into existing sequential check-in loop. |
| IV. Testability via Interfaces | PASS | DeployStore testable with `t.TempDir()`. Stage engine already mockable. No new external dependencies. |
| V. Append-Only Event Log | PASS | `deploys` table is a state table (like `issue_queue`) with UPDATE operations. Deploy lifecycle events logged to dedicated `deploy_events` table (append-only). Separate table needed because `pipeline_events.issue` is `INTEGER NOT NULL` (ADR 0016). |
| VI. Pipeline Configuration as Data | PASS | Deploy stages configured in `deploy:` section of `pipeline.yaml`. No code changes needed to add/modify stages. |
| VII. Prompt Transparency | PASS | Deploy prompt templates use `{{variable}}` substitution. Rendered prompts saved to attempt directories. |
| VIII. Simplicity | WATCH | `DeployStore` mirrors `pipeline.Store` patterns. Dedicated deploy runner duplicates some session lifecycle code from the engine, but avoids coupling deploy evolution to the pipeline engine (ADR 0015). New `deploy_events` table duplicates `pipeline_events` structure (ADR 0016). Both are justified tradeoffs for isolation. |

**Gate result**: PASS (Principle VIII is WATCH due to justified duplication, no violations)

**Post-Phase 1 re-check**: PASS (design revised per adversarial review -- dedicated runner and separate events table introduce controlled duplication for isolation; see ADRs 0015-0018)

## Project Structure

### Documentation (this feature)

```text
specs/002-deploy-pipeline/
├── plan.md              # This file
├── research.md          # Phase 0: research decisions
├── data-model.md        # Phase 1: entity definitions and state machine
├── quickstart.md        # Phase 1: usage guide
├── contracts/           # Phase 1: CLI and config contracts
│   ├── cli.md           # factory deploy command schemas
│   └── config.md        # pipeline.yaml deploy section schema
└── tasks.md             # Phase 2 output (created by /speckit.tasks)
```

### Source Code (repository root)

```text
internal/
├── config/
│   ├── types.go              # MODIFY: add DeployPipeline struct, Deploy field to PipelineConfig
│   ├── loader.go             # MODIFY: extend applyDefaults for deploy stages
│   ├── validate.go           # MODIFY: validate deploy stage IDs unique, on_fail targets exist
│   └── config_test.go        # MODIFY: add deploy config loading and validation tests
├── pipeline/
│   ├── types.go              # MODIFY: add DeployState, DeployCreateOpts types
│   ├── deploy_store.go       # CREATE: DeployStore (Create, Get, Update, List)
│   └── deploy_store_test.go  # CREATE: DeployStore unit tests
├── orchestrator/
│   ├── orchestrator.go       # MODIFY: add DeployStore field, checkInDeploy, advanceDeploy methods
│   ├── deploy.go             # CREATE: dedicated deploy runner (runDeployStage: session create, prompt render, wait, cleanup)
│   └── orchestrator_test.go  # MODIFY: add deploy check-in, advance, failure routing, and cycle detection tests
├── db/
│   ├── db.go                 # MODIFY: add deploys + deploy_events tables to schema, Reset tables list
│   └── queries.go            # MODIFY: add DeployRecord, DeployInsert, DeployUpdateStatus, DeployList, DeployGetLatestCompleted, LogDeployEvent
├── cli/
│   ├── deploy.go             # CREATE: factory deploy create/list/status commands
│   ├── root.go               # MODIFY: register deployCmd
│   └── pipeline.go           # MODIFY: wire DeployStore into newOrchestrator
└── web/
    ├── server.go             # MODIFY: add deploysTmpl, /deploys route
    ├── handlers.go           # MODIFY: add DeploysPageData, handleDeploys handler
    └── templates/
        ├── base.html         # MODIFY: add Deploys sidebar link
        └── deploys.html      # CREATE: deploy list page template

config/
└── pipeline.example.yaml    # MODIFY: add deploy section example
```

**Structure Decision**: This feature extends the existing Go project structure under `internal/`. No new packages are created -- deploy functionality is distributed across existing packages following established patterns (config in config, state in pipeline, advancement in orchestrator, commands in cli, UI in web).

## Key Architectural Decisions

> See ADRs 0015-0018 for full rationale.

1. **Dedicated deploy runner** (ADR 0015): Deploy stages use a `runDeployStage()` method in the orchestrator rather than reusing `stage.Engine.Run()`. The engine is deeply coupled to `PipelineState` and `pipeline.Store`; a dedicated runner avoids that coupling and keeps deploy lifecycle self-contained.

2. **Concurrent execution** (ADR 0015): Deploys run concurrently with dev pipelines. Deploy tmux sessions use a `deploy-{sha}-{stage}-{attempt}` naming prefix to avoid collisions with pipeline sessions (`{issue}-{stage}-{attempt}`).

3. **Separate `deploy_events` table** (ADR 0016): Deploy events go to a dedicated `deploy_events` table with `commit_sha TEXT` instead of `issue INTEGER`. The existing `pipeline_events` table is not modified.

4. **Cycle detection, not hop limit** (ADR 0017): Failure routing uses a visited-set to detect cycles at runtime. No arbitrary hop limit. Any acyclic failure chain is valid.

5. **Full SHA normalization** (ADR 0018): `factory deploy create` resolves input to a full 40-char SHA via `git rev-parse`. All storage uses the canonical SHA.

## Complexity Tracking

> No violations to justify. All constitution principles pass without deviation.
