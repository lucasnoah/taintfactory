# Data Model: Deploy Pipeline

**Feature**: 002-deploy-pipeline | **Date**: 2026-03-06

## Entities

### DeployPipeline (Config)

Defines the deploy pipeline configuration within `pipeline.yaml`.

| Field  | Type     | Description                                |
|--------|----------|--------------------------------------------|
| Name   | string   | Human-readable name for the deploy pipeline |
| Stages | []Stage  | Ordered list of deploy stages (reuses existing Stage type) |

**Relationship**: Optional child of `PipelineConfig`. Nil when `deploy:` section is absent from YAML.

### DeployState (On-Disk)

Persisted state for a single deploy pipeline, stored as JSON at `~/.factory/deploys/{sha}/deploy.json`.

| Field          | Type               | Description                                              |
|----------------|--------------------|----------------------------------------------------------|
| CommitSHA      | string             | Primary key -- the commit being deployed                 |
| Namespace      | string             | Project namespace (e.g., "myorg/myapp")                  |
| CurrentStage   | string             | ID of the currently active stage                         |
| CurrentAttempt | int                | Attempt number within the current stage                  |
| CurrentSession | string             | Active tmux session name (empty if no session)           |
| StageHistory   | []StageHistoryEntry| Completed stage records                                  |
| FailureVisited | []string           | Stage IDs entered via on_fail routing (cycle detection)  |
| Status         | string             | Lifecycle state (see state machine below)                |
| PreviousSHA    | string             | Last successfully deployed SHA (for rollback context)    |
| CreatedAt      | string             | RFC3339 creation timestamp                               |
| UpdatedAt      | string             | RFC3339 last-modified timestamp                          |
| ConfigPath     | string             | Path to pipeline.yaml (multi-project support)            |
| RepoDir        | string             | Repository root directory                                |

**Reuses**: `StageHistoryEntry` from existing `internal/pipeline/types.go` (Stage, Attempt, Outcome, Duration, FixRounds, ChecksFirstPass).

### DeployRecord (Database)

Row in the `deploys` PostgreSQL table for querying and web UI display.

| Column        | Type        | Constraints                                                |
|---------------|-------------|------------------------------------------------------------|
| id            | SERIAL      | Primary key                                                |
| namespace     | TEXT        | NOT NULL, DEFAULT ''                                       |
| commit_sha    | TEXT        | NOT NULL, UNIQUE                                           |
| status        | TEXT        | NOT NULL, CHECK IN (pending, in_progress, completed, failed, rolled_back) |
| previous_sha  | TEXT        | NOT NULL, DEFAULT ''                                       |
| current_stage | TEXT        | NOT NULL, DEFAULT ''                                       |
| stage_history | JSONB       | NOT NULL, DEFAULT '[]'                                     |
| created_at    | TIMESTAMPTZ | NOT NULL, DEFAULT NOW()                                    |
| updated_at    | TIMESTAMPTZ | NOT NULL, DEFAULT NOW()                                    |

**Indexes**: `idx_deploys_status` on (status).

### DeployEvent (Database)

Row in the `deploy_events` PostgreSQL table for deploy lifecycle logging (append-only).

| Column     | Type        | Constraints                      |
|------------|-------------|----------------------------------|
| id         | SERIAL      | Primary key                      |
| commit_sha | TEXT        | NOT NULL                         |
| namespace  | TEXT        | NOT NULL, DEFAULT ''             |
| event      | TEXT        | NOT NULL                         |
| stage      | TEXT        |                                  |
| attempt    | INTEGER     |                                  |
| detail     | TEXT        |                                  |
| timestamp  | TIMESTAMPTZ | NOT NULL, DEFAULT NOW()          |

**Indexes**: `idx_deploy_events_sha` on (commit_sha, timestamp DESC).

## State Machine: Deploy Status

```
                    +-----------+
                    |  pending  |
                    +-----+-----+
                          |
                   check-in loop
                          |
                    +-----v-----+
              +---->|in_progress +----+
              |     +-----+-----+    |
              |           |          |
         retry/route   success    fail (no on_fail)
              |           |          |
              |     +-----v-----+   +-----v-----+
              |     | completed |   |   failed   |
              |     +-----------+   +-----------+
              |                          ^
              |                          |
              +--- on_fail:rollback --+  |
                          |             |
                    +-----v------+     |
                    | rolled_back|     |
                    +------------+     |
                          |            |
                    rollback fails ----+
```

**Transitions**:
- `pending` → `in_progress`: Orchestrator picks up deploy in check-in
- `in_progress` → `in_progress`: Stage succeeds, advancing to next stage
- `in_progress` → `completed`: Final stage succeeds
- `in_progress` → `failed`: Stage fails with no on_fail, or cycle detected in failure routing (visited-set)
- `in_progress` → `rolled_back`: Rollback stage completes successfully
- `in_progress` → `pending`: Deploy runner error (retry on next check-in)

**Cycle detection**: `DeployState` tracks a `FailureVisited []string` field. When routing to an `on_fail` target, if the target is already in the visited set, the deploy is marked `failed`. This prevents infinite loops for any config shape without an arbitrary hop limit.

## On-Disk Layout

```
~/.factory/deploys/
└── {commit-sha}/
    ├── deploy.json          # DeployState
    └── stages/              # Reserved for stage attempt data
        └── {stage-id}/
            └── attempt-{n}/
                ├── prompt.md
                └── session.log
```
