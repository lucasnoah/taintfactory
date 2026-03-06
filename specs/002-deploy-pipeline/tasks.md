# Tasks: Deploy Pipeline

**Input**: Design documents from `/specs/002-deploy-pipeline/`
**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/

**Tests**: Included per project constitution (TDD workflow required).

**Organization**: Tasks are grouped by user story to enable independent implementation and testing of each story.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies)
- **[Story]**: Which user story this task belongs to (e.g., US1, US2, US3)
- Include exact file paths in descriptions

## Phase 1: Setup

**Purpose**: No project initialization needed -- this feature extends the existing Go project.

*(No tasks -- existing project structure is sufficient)*

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: Shared types, storage, and DB schema that ALL user stories depend on.

**CRITICAL**: No user story work can begin until this phase is complete.

- [ ] T001 [P] Add DeployPipeline struct (Name string, Stages []Stage) and Deploy *DeployPipeline field to PipelineConfig in internal/config/types.go
- [ ] T002 [P] Extend applyDefaults to apply pipeline.defaults model/flags to deploy stages in internal/config/loader.go
- [ ] T003 Add tests for deploy config loading: with deploy section, without deploy section (nil), defaults inheritance in internal/config/config_test.go
- [ ] T004 [P] Add DeployState type (with FailureVisited []string field per ADR 0017) and DeployCreateOpts type to internal/pipeline/types.go
- [ ] T005 Create DeployStore with NewDeployStore, DefaultDeployStore, Create, Get, Update, List methods in internal/pipeline/deploy_store.go
- [ ] T006 Add DeployStore unit tests (create+get, duplicate rejection, update, list with filter, get-not-found) in internal/pipeline/deploy_store_test.go
- [ ] T007 [P] Add deploys table (id SERIAL, namespace TEXT, commit_sha TEXT NOT NULL UNIQUE, status TEXT with CHECK, previous_sha TEXT, current_stage TEXT, stage_history JSONB DEFAULT '[]', created_at/updated_at TIMESTAMPTZ) and deploy_events table (id SERIAL, commit_sha TEXT, namespace TEXT, event TEXT, stage TEXT, attempt INTEGER, detail TEXT, timestamp TIMESTAMPTZ) to schema in internal/db/db.go; add both tables to Reset() tables list
- [ ] T008 [P] Add DeployRecord struct, DeployInsert, DeployUpdateStatus, DeployList, DeployGetLatestCompleted, and LogDeployEvent query functions in internal/db/queries.go

**Checkpoint**: Foundation ready -- all shared types, stores, and DB schema in place. User story implementation can begin.

---

## Phase 3: User Story 1 - Trigger a Deploy (Priority: P1) MVP

**Goal**: Operator runs `factory deploy create <sha>` to create a deploy pipeline that the orchestrator automatically advances through stages to completion.

**Independent Test**: Run `factory deploy create <sha>`, verify deploy.json created on disk and DB record inserted. Then run orchestrator check-in and verify stages advance to completion.

### Implementation for User Story 1

- [ ] T009 [P] [US1] Create `factory deploy create` command with SHA normalization via `git rev-parse`, namespace flag, config validation, DeployStore.Create call, and DB insert in internal/cli/deploy.go
- [ ] T010 [US1] Register deployCmd in rootCmd in internal/cli/root.go init()
- [ ] T011 [P] [US1] Add deployStore *pipeline.DeployStore field and SetDeployStore setter method to Orchestrator struct in internal/orchestrator/orchestrator.go
- [ ] T012 [P] [US1] Create dedicated deploy runner: runDeployStage method with deploy-{sha7}-{stage}-{attempt} session naming, prompt template loading and rendering with deploy vars, session create, wait-idle, and cleanup in internal/orchestrator/deploy.go
- [ ] T013 [US1] Add checkInDeploy (find pending/in_progress deploy, skip terminal statuses), advanceDeploy (load config, check session status, run stage, record history), and advanceDeployToNext (advance or mark completed) methods in internal/orchestrator/orchestrator.go
- [ ] T014 [US1] Wire checkInDeploy call into CheckIn() after pipeline/queue processing and before triage in internal/orchestrator/orchestrator.go
- [ ] T015 [P] [US1] Wire DeployStore creation and orch.SetDeployStore call into newOrchestrator helper in internal/cli/pipeline.go
- [ ] T016 [US1] Add tests for deploy check-in (pending picked up, completed skipped, session active skipped) and happy-path advancement (stage success advances to next, final stage marks completed) in internal/orchestrator/orchestrator_test.go

**Checkpoint**: User Story 1 complete -- `factory deploy create` triggers a deploy that advances through all stages to completion via check-in loop.

---

## Phase 4: User Story 2 - Automatic Failure Routing and Rollback (Priority: P1)

**Goal**: When a deploy stage fails, the system routes to the configured on_fail target. Cycle detection prevents infinite loops. Successful rollback marks deploy as rolled_back.

**Independent Test**: Configure `on_fail: rollback` on a stage, simulate failure, verify rollback stage runs. Verify cycle detection terminates with failed status.

### Implementation for User Story 2

- [ ] T017 [US2] Add handleDeployFailure method: resolve on_fail target via resolveOnFail, check FailureVisited set for cycle detection, update FailureVisited, route to target stage or mark failed in internal/orchestrator/orchestrator.go
- [ ] T018 [US2] Add rolled_back terminal status handling: when a stage entered via on_fail routing completes successfully and is a rollback-type stage, mark deploy as rolled_back instead of advancing in internal/orchestrator/orchestrator.go
- [ ] T019 [US2] Add tests for failure routing (on_fail routes to target stage), cycle detection (visited-set prevents loops), no on_fail marks failed, rollback success marks rolled_back, rollback failure marks failed in internal/orchestrator/orchestrator_test.go

**Checkpoint**: User Stories 1 AND 2 complete -- deploys handle both success and failure paths with rollback capability.

---

## Phase 5: User Story 3 - Monitor Deploy Status (Priority: P2)

**Goal**: Operator can view deploy status via CLI (`factory deploy list`, `factory deploy status`) and web UI (`/deploys`).

**Independent Test**: Create deploys, run `factory deploy list` and `factory deploy status <sha>` to verify output. Navigate to `/deploys` in web UI to verify table rendering.

### Implementation for User Story 3

- [ ] T020 [US3] Add `factory deploy list` command with --limit and --format json flags, tabwriter output in internal/cli/deploy.go
- [ ] T021 [US3] Add `factory deploy status [sha]` command with --format json flag, detailed output with stage history in internal/cli/deploy.go
- [ ] T022 [P] [US3] Create deploys.html template with data table (SHA, status badge, stage, namespace, previous, created) in internal/web/templates/deploys.html
- [ ] T023 [US3] Add DeploysPageData struct and handleDeploys handler (query DeployList, build sidebar, render template) in internal/web/handlers.go
- [ ] T024 [US3] Add deploysTmpl field to Server struct, parse in NewServer, add /deploys route in buildMux in internal/web/server.go
- [ ] T025 [US3] Add Deploys sidebar link under Views section in internal/web/templates/base.html

**Checkpoint**: Full deploy monitoring available via CLI and web UI.

---

## Phase 6: User Story 4 - Configure Deploy Stages (Priority: P2)

**Goal**: Config validation catches invalid deploy stage configurations (duplicate IDs, invalid on_fail targets, on_fail cycles) at load time.

**Independent Test**: Write pipeline.yaml with invalid deploy config, run `factory config validate` and verify errors.

### Implementation for User Story 4

- [ ] T026 [US4] Add deploy stage validation: unique stage IDs within deploy section, on_fail targets reference existing deploy stage IDs, warn on obvious on_fail cycles in internal/config/validate.go
- [ ] T027 [US4] Add deploy validation tests (duplicate stage IDs error, invalid on_fail target error, cycle warning) in internal/config/config_test.go
- [ ] T028 [US4] Add deploy section example with deploy/smoke-test/debug/rollback stages to config/pipeline.example.yaml

**Checkpoint**: Deploy config validation catches misconfigurations before runtime.

---

## Phase 7: User Story 5 - Deploy Template Variables (Priority: P3)

**Goal**: Deploy prompt templates can reference deploy-specific variables (CommitSHA, PreviousSHA, Namespace, RepoDir) plus any stage-level Vars from config.

**Independent Test**: Create a deploy with a template referencing `{{.CommitSHA}}`, run the stage, verify rendered prompt contains actual SHA.

### Implementation for User Story 5

- [ ] T029 [US5] Ensure runDeployStage constructs full vars map from DeployState fields (CommitSHA, PreviousSHA, Namespace, RepoDir) and merges stage-level config.Stage.Vars, saves rendered prompt to attempt directory in internal/orchestrator/deploy.go
- [ ] T030 [US5] Add template variable rendering tests: verify all deploy vars present in rendered output, verify stage Vars override defaults, verify prompt saved to attempt dir in internal/orchestrator/orchestrator_test.go

**Checkpoint**: Deploy templates fully functional with all context variables available.

---

## Phase 8: Polish & Cross-Cutting Concerns

**Purpose**: Final validation and cleanup.

- [ ] T031 Verify `go build ./cmd/factory/` compiles cleanly with all changes
- [ ] T032 Run full test suite `go test ./...` and fix any failures
- [ ] T033 Run quickstart.md validation: create deploy, check status, list deploys

---

## Dependencies & Execution Order

### Phase Dependencies

- **Foundational (Phase 2)**: No dependencies -- can start immediately. BLOCKS all user stories.
- **US1 (Phase 3)**: Depends on Foundational completion.
- **US2 (Phase 4)**: Depends on US1 (extends orchestrator advancement with failure routing).
- **US3 (Phase 5)**: Depends on Foundational only (CLI list/status and web UI are independent of orchestrator advancement).
- **US4 (Phase 6)**: Depends on Foundational only (config validation is independent of orchestrator).
- **US5 (Phase 7)**: Depends on US1 (extends deploy runner with template vars).
- **Polish (Phase 8)**: Depends on all desired user stories being complete.

### User Story Dependencies

- **US1 (P1)**: Depends on Foundational (Phase 2) only. No dependencies on other stories.
- **US2 (P1)**: Depends on US1 (adds failure routing to advanceDeploy).
- **US3 (P2)**: Depends on Foundational only. Can run in PARALLEL with US1/US2.
- **US4 (P2)**: Depends on Foundational only. Can run in PARALLEL with US1/US2/US3.
- **US5 (P3)**: Depends on US1 (extends deploy runner).

### Within Each User Story

- Types and models before services and methods
- Core methods before integration (wiring)
- Implementation before tests (TDD: test written alongside implementation per constitution)
- Commit test and implementation together

### Parallel Opportunities

- **Phase 2**: T001, T002, T004, T007, T008 can all run in parallel (different files)
- **Phase 3**: T009 (cli/deploy.go), T011 (orchestrator.go), T012 (deploy.go), T015 (pipeline.go) can start in parallel (different files)
- **Phase 5 + Phase 3/4**: US3 (monitoring) can run entirely in parallel with US1/US2 (orchestrator work) since they touch different files
- **Phase 6 + Phase 3/4**: US4 (config validation) can run in parallel with US1/US2
- **Cross-story**: US3 + US4 can run in parallel with each other

---

## Parallel Example: Foundational Phase

```bash
# These can all run simultaneously (different files):
Task T001: "Add DeployPipeline struct in internal/config/types.go"
Task T004: "Add DeployState type in internal/pipeline/types.go"
Task T007: "Add deploys + deploy_events tables in internal/db/db.go"
Task T008: "Add deploy query functions in internal/db/queries.go"
```

## Parallel Example: After Foundational

```bash
# US1 orchestrator work + US3 CLI/web work + US4 config validation -- all in parallel:
Agent A (US1): T009 → T010 → T011 → T012 → T013 → T014 → T015 → T016
Agent B (US3): T020 → T021 → T022 → T023 → T024 → T025
Agent C (US4): T026 → T027 → T028
```

---

## Implementation Strategy

### MVP First (User Story 1 Only)

1. Complete Phase 2: Foundational (T001-T008)
2. Complete Phase 3: US1 - Trigger a Deploy (T009-T016)
3. **STOP and VALIDATE**: `factory deploy create <sha>`, verify orchestrator advances to completion
4. This alone delivers the core deploy capability

### Incremental Delivery

1. Foundational → Foundation ready
2. US1 (Trigger) → Core deploy works → **MVP!**
3. US2 (Failure Routing) → Deploys handle failures safely
4. US3 (Monitoring) → Operators can see deploy status (can run parallel with US2)
5. US4 (Config Validation) → Bad configs caught early (can run parallel with US2/US3)
6. US5 (Template Vars) → Full template context available
7. Polish → Final validation

### Parallel Team Strategy

With multiple developers after Foundational:

- **Developer A**: US1 → US2 → US5 (orchestrator path)
- **Developer B**: US3 (monitoring: CLI + web UI)
- **Developer C**: US4 (config validation)

Stories complete and integrate independently. US2 and US5 must wait for US1.

---

## Notes

- [P] tasks = different files, no shared state
- [Story] label maps task to specific user story
- Each user story is independently testable at its checkpoint
- Constitution requires TDD: write test, confirm fail, implement, confirm green
- Commit test and implementation together
- Session naming convention for deploys: `deploy-{sha7}-{stage}-{attempt}` (ADR 0015)
- SHA normalization: `git rev-parse` to full 40-char SHA on create (ADR 0018)
- Cycle detection via FailureVisited visited-set, not hop limit (ADR 0017)
- Deploy events go to dedicated deploy_events table, not pipeline_events (ADR 0016)
