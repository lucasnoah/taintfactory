# Deploy Pipeline Design

## Goal

Add a manually-triggered deploy pipeline to the factory system. Deploy pipelines are a second pipeline type alongside implementation pipelines, with their own stages, state, and lifecycle. They run agent sessions that can build, push, smoke-test, debug, and rollback deployments.

## Trigger

Deploys are manually triggered via `factory deploy <commit-sha>`. No auto-deploy on merge — the operator decides when to deploy.

## Config Schema

A `deploy:` section is added to the existing `pipeline.yaml`:

```yaml
pipeline:
  name: my-app
  # ... existing implementation stages ...

deploy:
  name: my-app-deploy
  timeout: "10m"
  rollback_timeout: "5m"
  stages:
    - id: deploy
      type: agent
      prompt_template: "templates/deploy.md"
      timeout: "10m"
      on_fail: rollback

    - id: smoke-test
      type: agent
      prompt_template: "templates/smoke-test.md"
      timeout: "5m"
      on_fail: debug

    - id: debug
      type: agent
      prompt_template: "templates/debug-deploy.md"
      timeout: "5m"
      on_fail: rollback

    - id: rollback
      type: agent
      prompt_template: "templates/rollback.md"
      timeout: "5m"
```

Reuses the existing `config.Stage` struct. Deploy stages are agent sessions — the agent runs make commands, kubectl, etc. and can debug failures interactively within the timeout.

## State & Storage

- **State directory:** `~/.factory/deploys/{sha}/pipeline.json`
- **State struct:** `DeployState` — keyed by commit SHA, not issue number:

```go
type DeployState struct {
    CommitSHA      string              `json:"commit_sha"`
    Namespace      string              `json:"namespace,omitempty"`
    CurrentStage   string              `json:"current_stage"`
    CurrentAttempt int                 `json:"current_attempt"`
    CurrentSession string              `json:"current_session"`
    StageHistory   []StageHistoryEntry `json:"stage_history"`
    Status         string              `json:"status"` // pending, in_progress, completed, failed, rolled_back
    PreviousSHA    string              `json:"previous_sha"`
    CreatedAt      string              `json:"created_at"`
    UpdatedAt      string              `json:"updated_at"`
    ConfigPath     string              `json:"config_path,omitempty"`
    RepoDir        string              `json:"repo_dir,omitempty"`
}
```

- `PreviousSHA` is populated at create time from the last completed deploy — used by the rollback stage.
- `rolled_back` is a distinct terminal status from `failed`.

## CLI

```
factory deploy <commit-sha>     # Start a deploy pipeline
factory deploy status [sha]     # Show deploy status (latest if no SHA)
factory deploy list             # List recent deploys
factory deploy rollback         # Manually trigger rollback to previous SHA
```

## Orchestrator Integration

The orchestrator's `CheckIn()` loop gains a deploy step:

1. Check active issue pipelines, advance them (existing)
2. Process issue queue (existing)
3. **New: Check pending/in-progress deploy pipelines, advance them**

Deploy pipelines use the same `stage.Engine.Run()`. Only one deploy can be active per project.

### on_fail Routing

- `deploy` fails → `rollback`
- `smoke-test` fails → `debug` (agent attempts fix, 5m timeout)
- `debug` fails/times out → `rollback`
- `rollback` fails → `failed` (human intervention)
- `rollback` succeeds → `rolled_back`

### Template Variables

Agent sessions receive these vars for use in prompt templates:

- `{{.CommitSHA}}` — SHA being deployed
- `{{.PreviousSHA}}` — last known-good SHA for rollback
- `{{.Namespace}}` — project namespace
- `{{.RepoDir}}` — path to repo checkout

## Database

New `deploys` table for listing/querying:

```sql
CREATE TABLE IF NOT EXISTS deploys (
    id INTEGER PRIMARY KEY,
    namespace TEXT NOT NULL DEFAULT '',
    commit_sha TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending',
    previous_sha TEXT DEFAULT '',
    current_stage TEXT DEFAULT '',
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);
```

Detailed stage state lives in JSON on disk (consistent with issue pipelines).

## Web UI

New `/deploys` page showing recent deploys with status, SHA, timestamps. Sidebar link alongside existing Repos/Queue pages.
