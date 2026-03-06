# Multi-Repo Database Autoconfiguration Design

## Status
Accepted

## Date
2026-03-06

## Goal

Each repo gets its own PostgreSQL database on the shared sidecar. The factory
auto-provisions it on `factory repo add`, re-checks on pod restart, injects
`DATABASE_URL` into tmux sessions, and runs setup commands before the first
stage of each issue.

## Config Schema

```yaml
# pipeline.yaml
pipeline:
  name: wptl
  repo: lucasnoah/wptl

  database:
    name: wptl_dev        # CREATE DATABASE
    user: wptl            # CREATE USER
    password: wptl_dev    # user password
    migrate: "make migrate"  # optional migration command

  env:                    # general env vars injected into sessions
    SOME_API_KEY: "value"

  setup:
    - "go mod download"
    - "cd web && npm install"
```

`database` is optional — repos without DB needs skip it. `env` is also optional.

## Provisioning Flow

### On `factory repo add`

1. Clone repo, read `pipeline.yaml`
2. If `database:` section exists:
   - Connect to sidecar Postgres (using factory's `DATABASE_URL`)
   - `CREATE USER IF NOT EXISTS <user> WITH PASSWORD '<password>'`
   - `CREATE DATABASE IF NOT EXISTS <name> OWNER <user>`
   - `GRANT ALL PRIVILEGES ON DATABASE <name> TO <user>`
3. Register repo in factory DB (existing behavior)

### On pod restart (entrypoint.sh)

- After cloning/pulling repos, iterate registered repos via `factory repo list`
- For each with a `database` config: idempotent `CREATE DATABASE/USER IF NOT EXISTS`
- Do NOT run migrations here (that's per-issue)

## Session Launch Flow

When a new issue enters its first stage:

1. Create worktree `issue-{N}`
2. Construct `DATABASE_URL` from `database:` config:
   `postgres://{user}:{password}@localhost:5432/{name}?sslmode=disable`
3. In the tmux session, before launching claude:
   - `export DATABASE_URL="postgres://..."`
   - Export any `env:` map vars
4. Run `setup` commands (`go mod download`, `npm install`, etc.)
5. If `database.migrate` is set, run it
6. Launch claude

## What Changes

| Component | Change |
|---|---|
| `internal/config/types.go` | Add `Database` struct and `Env` map to pipeline config |
| `internal/config/loader.go` | Parse new fields |
| `internal/db/provision.go` (new) | `ProvisionDatabase(connStr, dbConfig)` — CREATE USER/DB |
| `internal/cli/repo.go` | Call provisioner during `repo add` |
| `internal/session/session.go` | Accept env vars map, export them in tmux before claude |
| `internal/stage/engine.go` | Execute setup commands in worktree before session, pass env vars |
| `deploy/entrypoint.sh` | Add DB re-check loop after repo cloning |

## What Doesn't Change

- Factory's own `DATABASE_URL` and `factory` database
- Pipeline state storage (still JSON on disk)
- Session event logging (still factory's Postgres)
- The Postgres sidecar itself — just hosts more databases

## Key Decisions

1. **Separate databases, not schemas** — strongest isolation, matches how repos expect their DB
2. **Formal `database:` field in pipeline.yaml** — auto-provisions DB/user on the sidecar
3. **Provision on `factory repo add`** — immediate feedback, validates config early
4. **Idempotent re-check on pod restart** — entrypoint runs CREATE IF NOT EXISTS
5. **tmux export for env injection** — per-session, works for claude and all commands it runs
6. **General `env:` map** — repos can declare arbitrary env vars alongside database config
7. **Setup commands run before each issue's first stage** — ensures deps and migrations are current
