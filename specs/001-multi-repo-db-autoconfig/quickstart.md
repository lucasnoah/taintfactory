# Quickstart: Multi-Repo Database Autoconfiguration

## Prerequisites

- Factory binary built (`go build -o /tmp/factory ./cmd/factory/` or `make build`)
- PostgreSQL running (shared sidecar or local)
- `DATABASE_URL` set pointing to the PostgreSQL instance. The user must have `CREATEDB` and `CREATEROLE` privileges. The provisioner overrides the database component to `postgres` internally, so `DATABASE_URL` can point to any database on the instance.

## 1. Add database config to your pipeline

Edit your repo's `pipeline.yaml`:

```yaml
pipeline:
  name: myproject
  repo: myorg/myrepo

  database:
    name: myproject_dev
    user: myproject
    password: myproject_dev
    migrate: "make migrate-up"

  env:
    SOME_API_KEY: "your-key"

  setup:
    - "go mod download"

  # ... rest of your pipeline config
```

## 2. Register the repo (auto-provisions)

```bash
export DATABASE_URL="postgres://admin:adminpass@localhost:5432/factory?sslmode=disable"
factory repo add myorg/myrepo --config /path/to/pipeline.yaml
# Output: Provisioned database "myproject_dev" (user: myproject)
```

> Note: The provisioner automatically connects to the `postgres` maintenance database for DDL operations, regardless of which database `DATABASE_URL` points to.

## 3. Verify

```bash
psql "postgres://myproject:myproject_dev@localhost:5432/myproject_dev" -c "SELECT 1"
# Should return 1
```

## 4. Run a pipeline

```bash
factory queue add 42
factory orchestrator check-in
# The orchestrator will:
#   1. Create worktree and provision database (if not already done)
#   2. Run setup commands (go mod download) with DATABASE_URL set
#   3. Run migrations (make migrate-up) with DATABASE_URL set
#   4. Create Claude session with DATABASE_URL + SOME_API_KEY exported
```

## 5. Manual re-provisioning

```bash
# All repos:
factory repo provision-db

# Single repo:
factory repo provision-db myorg/myrepo
```

## Troubleshooting

**"DATABASE_URL not set"** — Export your admin PostgreSQL connection before running factory commands. The user must have CREATEDB and CREATEROLE privileges.

**"already exists" during provisioning** — This is normal and expected. Provisioning is idempotent.

**Setup commands fail** — Check that setup commands work when run manually in the worktree directory with DATABASE_URL set. The orchestrator runs them with `sh -c` in the worktree during pipeline creation.
