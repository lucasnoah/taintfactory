# GitHub Label Poller + Repo Registry Design

## Problem

Issues currently enter the pipeline via manual CLI commands (`factory pipeline create`, `factory queue add`). There's no automated way to watch a GitHub repo for new work and pull it into the factory.

## Solution

Two features:

1. **Repo Registry** — A DB-backed registry of repos the factory manages, with CLI and web UI for CRUD.
2. **Label Poller** — The orchestrator periodically polls registered repos for issues with a configured label and auto-enqueues them.

## Repo Registry

### DB Table: `repos`

| Column | Type | Description |
|--------|------|-------------|
| id | SERIAL PK | Auto-increment |
| namespace | TEXT UNIQUE NOT NULL | e.g. `mbrucker/deathcookies` |
| repo_url | TEXT NOT NULL | e.g. `github.com/mbrucker/deathcookies` |
| local_path | TEXT NOT NULL | e.g. `/data/repos/deathcookies` |
| config_path | TEXT NOT NULL | Path to `pipeline.yaml` |
| poll_label | TEXT | GitHub label to watch (NULL = no polling) |
| poll_interval | INT DEFAULT 120 | Seconds between polls |
| active | BOOLEAN DEFAULT true | Enable/disable without deleting |
| added_at | TIMESTAMP | When registered |

### CLI

- `factory repo add <repo_url> --local-path /data/repos/X --config /path/to/pipeline.yaml --label "implementation" --poll-interval 120`
- `factory repo list [--format json|table]`
- `factory repo remove <namespace>`
- `factory repo update <namespace> --label "..." --active true|false`

### Web UI

The existing "Config" sidebar link becomes a repo management page:
- Table of registered repos: namespace, repo URL, poll label, active toggle, last polled
- "Add Repo" form
- Remove/disable actions

### Startup Behavior

- Entrypoint still clones repos via `FACTORY_PRIMARY_REPO` / `FACTORY_REPOS`
- On first boot, if the `repos` table is empty, auto-register discovered repos by scanning `/data/repos/*/pipeline.yaml`
- After initial registration, the DB is the source of truth

### Migration Path

Existing code that walks the filesystem for `pipeline.yaml` files (orchestrator config loading, web UI project list) should read from the `repos` table instead.

## Label Poller

### How It Works

1. Orchestrator check-in loop runs every 10s
2. Every ~12th check-in (~2 minutes), run the label poller
3. For each registered repo where `poll_label IS NOT NULL AND active = true`:
   - Call `gh issue list --repo <repo> --label <poll_label> --state open --json number,title,body`
   - For each returned issue, check dedup:
     - Already in `issue_queue` for this namespace? Skip.
     - Already has a pipeline on disk? Skip.
   - For new issues:
     - Derive feature intent via `github.DeriveFeatureIntent()`
     - `QueueAdd()` with namespace, intent, config_path from the repo record
4. `processQueue()` picks up new items on subsequent check-ins and creates pipelines

### Deduplication

The poller is idempotent. Before enqueuing, it checks:
- `db.QueueList()` filtered by namespace — is the issue already queued?
- `store.Get(issue)` or `store.GetForNamespace(namespace, issue)` — does a pipeline exist?

If either is true, skip. Closed issues returned by mistake are filtered by the `--state open` flag.

### New Code

**`internal/github/github.go`:**
- `ListLabeledIssues(repo, label string) ([]IssueSummary, error)` — calls `gh issue list`
- Returns slice of `{Number, Title, Body}`

**`internal/orchestrator/orchestrator.go`:**
- `pollLabeledIssues()` method — reads repos from DB, calls `ListLabeledIssues`, deduplicates, enqueues
- Called from `CheckIn()` with a tick counter for the 2-min interval

**`internal/db/db.go` + `queries.go`:**
- `RepoAdd()`, `RepoList()`, `RepoRemove()`, `RepoUpdate()`, `RepoGetActive()` queries
- Migration for `repos` table

**`internal/cli/repo.go`:**
- `factory repo add|list|remove|update` commands

**`internal/web/`:**
- Config/repo management page handlers
- Template for repo table + add form

### Configuration

No new env vars. Everything is in the DB via `factory repo add` or the web UI.

The existing `pipeline.yaml` fields (`pipeline.repo`, `pipeline.name`) remain for pipeline-level config. The repo registry is factory-level config about which repos to manage and poll.

## Data Flow

```
GitHub repo (issues with "implementation" label)
  │
  ▼
Orchestrator pollLabeledIssues() [every ~2 min]
  │ gh issue list --label "implementation" --state open
  │
  ▼
Dedup check (queue + pipeline store)
  │
  ▼
DeriveFeatureIntent() [LLM]
  │
  ▼
QueueAdd() → issue_queue table
  │
  ▼
processQueue() [next orchestrator check-in]
  │
  ▼
Pipeline created → implement → review → qa → verify → merge
```

## Error Handling

- GitHub API failure: log warning, retry on next poll cycle
- Intent derivation failure: log warning, enqueue with empty intent (orchestrator will derive on activation)
- Rate limiting: `gh` CLI handles GitHub rate limits with backoff. 2-min interval keeps us well under 5000 req/hr

## Testing

- Unit test `ListLabeledIssues` with mock `gh` output
- Unit test `pollLabeledIssues` with mock DB + mock GitHub (verify dedup, enqueue)
- Unit test repo CRUD DB queries
- Integration: register repo, add labeled issue, verify it appears in queue
