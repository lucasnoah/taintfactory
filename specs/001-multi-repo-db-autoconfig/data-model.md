# Data Model: Multi-Repo Database Autoconfiguration

## Entities

### DatabaseConfig

Declares a repo's PostgreSQL database requirements. Optional — pipelines without this section operate identically to today.

| Field    | Type   | Required | Description |
|----------|--------|----------|-------------|
| Name     | string | yes      | Database name to create (e.g., `wptl_dev`) |
| User     | string | yes      | PostgreSQL user to create as database owner |
| Password | string | yes      | Password for the created user |
| Migrate  | string | no       | Shell command to run migrations after setup (e.g., `make migrate`) |

**Derived**: `URL()` method returns `postgres://{User}:{URL-encoded Password}@localhost:5432/{Name}?sslmode=disable`. The password is URL-encoded via `net/url.PathEscape()` to handle special characters (`@`, `:`, `/`, `#`, `%`, `?`) that would otherwise produce a malformed connection string.

**Lifecycle**: Read-only after config load. Never persisted beyond the YAML file.

### Pipeline (extended)

Two new optional fields on the existing `Pipeline` struct:

| Field    | Type              | Required | Description |
|----------|-------------------|----------|-------------|
| Database | *DatabaseConfig   | no       | Per-repo database declaration |
| Env      | map[string]string | no       | Custom env vars injected into Claude sessions |

### CreateOpts (extended)

One new field on the existing session `CreateOpts` struct:

| Field | Type              | Required | Description |
|-------|-------------------|----------|-------------|
| Env   | map[string]string | no       | Env vars to export in tmux before launching Claude |

## Relationships

```
PipelineConfig
  └── Pipeline
        ├── Database → DatabaseConfig (0..1)
        ├── Env → map[string]string (0..1)
        └── Stages[] → Stage (1..N)

Orchestrator.Create()
  └── reads Pipeline.Database + Pipeline.Env
        ├── passes to Provisioner (on repo add / restart)
        ├── builds merged env map (DATABASE_URL from Database + custom Env)
        ├── passes merged env to runSetupWith (once, before first stage)
        └── stores merged env for stage engine to pass to session.CreateOpts.Env

Stage Engine
  └── reads merged env from orchestrator
        └── passes to session.CreateOpts.Env (on each session create)
```

**Env merge precedence**: If `Pipeline.Env` contains a `DATABASE_URL` key and `Pipeline.Database` is also set, the auto-generated `DATABASE_URL` from `DatabaseConfig.URL()` takes precedence. A warning is logged when this override occurs.

## State Transitions

DatabaseConfig has no state — it's a pure value read from YAML.

Provisioning is stateless and idempotent:
- **Not provisioned** → `Provision()` → **Provisioned** (creates user, DB, grants)
- **Already provisioned** → `Provision()` → **Provisioned** (no-op, "already exists" ignored)

## Validation Rules

These rules are enforced in `internal/config/validate.go` during config loading:

- `DatabaseConfig.Name` must be a valid PostgreSQL identifier: `^[a-zA-Z_][a-zA-Z0-9_]*$`
- `DatabaseConfig.User` must be a valid PostgreSQL role name: `^[a-zA-Z_][a-zA-Z0-9_]*$`
- `DatabaseConfig.Password` may contain any characters (URL-encoded in connection strings, SQL-escaped in provisioning DDL)
- `Pipeline.Env` keys must be valid shell variable names: `^[a-zA-Z_][a-zA-Z0-9_]*$`
- `Pipeline.Env` keys that conflict with auto-generated keys (`DATABASE_URL` when `Database` is set) produce a warning, not an error
