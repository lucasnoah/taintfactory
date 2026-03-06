# Feature Specification: Deploy Pipeline

**Feature Branch**: `002-deploy-pipeline`
**Created**: 2026-03-06
**Status**: Draft
**Input**: User description: "Add a manually-triggered deploy pipeline system that builds, pushes, smoke-tests, and can debug/rollback deployments via Claude agent sessions"

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Trigger a Deploy (Priority: P1)

An operator wants to deploy a specific commit to production. They run a CLI command specifying the commit SHA, and the system creates a deploy pipeline that automatically advances through configurable stages (build, push, smoke-test) using Claude agent sessions.

**Why this priority**: This is the core value proposition -- without the ability to trigger and run a deploy, no other feature matters.

**Independent Test**: Can be fully tested by running `factory deploy create <sha>` and observing that a deploy pipeline is created with correct initial state, then the orchestrator advances it through defined stages.

**Acceptance Scenarios**:

1. **Given** a project with a `deploy:` section in `pipeline.yaml`, **When** the operator runs `factory deploy create <sha>`, **Then** a deploy pipeline is created in "pending" status with the first stage set from the config.
2. **Given** a pending deploy exists, **When** the orchestrator's check-in loop runs, **Then** the deploy is advanced to the first stage and a Claude agent session is launched.
3. **Given** a deploy stage completes successfully, **When** the orchestrator checks in again, **Then** the deploy advances to the next stage automatically.
4. **Given** all deploy stages complete successfully, **When** the final stage finishes, **Then** the deploy is marked as "completed".

---

### User Story 2 - Automatic Failure Routing and Rollback (Priority: P1)

When a deploy stage fails, the system automatically routes to a configured failure handler (e.g., a debug stage or rollback stage). If a rollback stage is defined, the system can revert the deployment to the previous known-good commit.

**Why this priority**: Failure handling is essential for production safety -- a deploy system without rollback capability is dangerous to use.

**Independent Test**: Can be tested by configuring `on_fail: rollback` on a deploy stage and simulating a stage failure, then verifying the rollback stage is triggered.

**Acceptance Scenarios**:

1. **Given** a deploy stage with `on_fail: rollback` configured, **When** the stage fails, **Then** the deploy transitions to the "rollback" stage.
2. **Given** a deploy stage with `on_fail: debug` configured, **When** the stage fails, **Then** the deploy transitions to the "debug" stage.
3. **Given** the rollback stage completes successfully, **When** the orchestrator checks in, **Then** the deploy is marked as "rolled_back".
4. **Given** the rollback stage itself fails, **When** the orchestrator checks in, **Then** the deploy is marked as "failed" (no infinite loop).
5. **Given** a stage has no `on_fail` configured, **When** the stage fails, **Then** the deploy is marked as "failed" immediately.

---

### User Story 3 - Monitor Deploy Status (Priority: P2)

An operator wants to check the current status of a deploy -- which stage it's on, whether it succeeded or failed, and its history. They can do this via CLI commands or through the web UI.

**Why this priority**: Visibility into deploy state is critical for operations, but the system functions without it (logs exist).

**Independent Test**: Can be tested by creating a deploy, then running `factory deploy status <sha>` and `factory deploy list` to verify correct output. Web UI can be tested by navigating to `/deploys`.

**Acceptance Scenarios**:

1. **Given** one or more deploys exist, **When** the operator runs `factory deploy list`, **Then** a table of recent deploys is displayed showing SHA, status, stage, namespace, and creation time.
2. **Given** a deploy exists for SHA "abc123", **When** the operator runs `factory deploy status abc123`, **Then** detailed deploy information is shown including status, current stage, attempt count, previous SHA, and stage history.
3. **Given** deploys exist in the database, **When** the operator visits `/deploys` in the web UI, **Then** a table of deploys is rendered with status badges.

---

### User Story 4 - Configure Deploy Stages (Priority: P2)

A project maintainer defines the deploy pipeline stages in `pipeline.yaml` under a `deploy:` section. Each stage specifies a prompt template, timeout, and optional failure routing. The deploy stages reuse the same stage configuration format as implementation pipelines.

**Why this priority**: Configuration flexibility is needed to adapt the system to different projects, but a reasonable default stage set covers most cases.

**Independent Test**: Can be tested by writing a `pipeline.yaml` with a `deploy:` section and verifying it loads correctly, including stage defaults inheritance.

**Acceptance Scenarios**:

1. **Given** a `pipeline.yaml` with a `deploy:` section containing stages, **When** the config is loaded, **Then** the deploy stages are parsed with correct IDs, prompt templates, timeouts, and on_fail targets.
2. **Given** a `pipeline.yaml` without a `deploy:` section, **When** the config is loaded, **Then** the deploy configuration is nil and no error occurs.
3. **Given** pipeline-level defaults for model and flags, **When** deploy stages are loaded, **Then** the defaults are applied to deploy stages that don't override them.

---

### User Story 5 - Deploy Template Variables (Priority: P3)

Deploy stage prompt templates need access to deploy-specific context variables (commit SHA, previous SHA, namespace, repo directory) so that Claude agent sessions can perform the correct deployment actions.

**Why this priority**: Templates without variables would require hardcoded values, but the system can function with basic templates initially.

**Independent Test**: Can be tested by running a deploy stage and verifying that template variables like `CommitSHA` and `PreviousSHA` are available in the rendered prompt.

**Acceptance Scenarios**:

1. **Given** a deploy stage with a prompt template referencing `{{.CommitSHA}}`, **When** the stage is executed, **Then** the template renders with the actual commit SHA.
2. **Given** a deploy created with a previous SHA, **When** a rollback stage template references `{{.PreviousSHA}}`, **Then** the template renders with the previous deployment's commit SHA.

---

### Edge Cases

- What happens when a deploy is created for a SHA that already has an active deploy? The system rejects duplicates with an error.
- What happens when the orchestrator finds no `deploy:` section in config for an active deploy? The deploy is skipped with a log message.
- What happens when a deploy stage references an `on_fail` target that doesn't exist in the stage list? The deploy fails rather than entering an invalid state.
- What happens when the operator runs `factory deploy status` with no deploys? A clear "no deploys found" message is shown.
- What happens when the rollback stage routes to itself via `on_fail`? The system detects the self-reference and marks the deploy as failed to prevent infinite loops.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: System MUST allow operators to create a deploy pipeline for a specific commit SHA via CLI command.
- **FR-002**: System MUST persist deploy state both on disk (as JSON files keyed by SHA) and in a database table for querying.
- **FR-003**: System MUST advance deploy pipelines through configured stages using the existing orchestrator check-in loop and stage engine.
- **FR-004**: System MUST support configurable failure routing via `on_fail` directives on each deploy stage.
- **FR-005**: System MUST prevent duplicate deploys for the same commit SHA.
- **FR-006**: System MUST automatically determine the previous deployment's commit SHA for rollback context.
- **FR-007**: System MUST track deploy status through lifecycle states: pending, in_progress, completed, failed, rolled_back.
- **FR-008**: System MUST provide CLI commands to list recent deploys and show detailed status for a specific deploy.
- **FR-009**: System MUST provide a web UI page listing deploys with status, stage, namespace, and timestamps.
- **FR-010**: System MUST inject deploy-specific variables (commit SHA, previous SHA, namespace, repo directory) into stage prompt templates.
- **FR-011**: System MUST support per-stage timeouts for deploy stages.
- **FR-012**: System MUST detect and prevent infinite failure routing loops (e.g., rollback failing and routing to itself).
- **FR-013**: System MUST record stage history (stage name, outcome, attempt number, duration) for each completed deploy stage.
- **FR-014**: System MUST load deploy pipeline configuration from the `deploy:` section of `pipeline.yaml`, reusing the same stage configuration format as implementation pipelines.

### Key Entities

- **Deploy**: Represents a single deployment attempt for a commit SHA. Has a status lifecycle, belongs to a namespace, tracks current stage and history. Keyed by commit SHA.
- **Deploy Stage**: A step in the deploy pipeline (e.g., build, smoke-test, rollback). Configured with a prompt template, timeout, and optional failure routing target.
- **Deploy Configuration**: The `deploy:` section of `pipeline.yaml` defining the ordered list of stages and their settings.
- **Stage History Entry**: A record of a completed stage execution including stage name, outcome, attempt number, and duration.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: Operators can trigger a deploy and have it advance through all stages to completion without manual intervention within the configured timeout windows.
- **SC-002**: When a deploy stage fails, the system routes to the configured failure handler within one orchestrator check-in cycle (default 2 minutes).
- **SC-003**: Rollback deploys restore the previous deployment's commit SHA context, allowing rollback templates to act on the correct version.
- **SC-004**: Deploy status is queryable via CLI within 1 second for both single-deploy and list views.
- **SC-005**: The web UI displays all deploys with correct status indicators, accessible within one page load.
- **SC-006**: No deploy can enter an infinite failure routing loop -- all failure chains terminate in a "failed" state when a cycle is detected (visited-set check).
- **SC-007**: Deploy pipelines operate independently from implementation pipelines -- creating a deploy does not interfere with in-progress issue pipelines.

## Assumptions

- The existing stage engine and tmux session manager are capable of running deploy stages without modification to their core interfaces (only additional input parameters).
- Deploy pipelines are manually triggered (not triggered by git push or CI events). Automatic triggering is out of scope.
- One deploy runs at a time per orchestrator instance. Concurrent deploy support is out of scope.
- The `deploy:` config section is optional -- projects without it simply cannot create deploys.
- Deploy stages use the same Claude agent session model as implementation pipeline stages.
- The orchestrator check-in loop interval (default 2 minutes) is acceptable latency for deploy stage transitions.
