# CLI Command Contracts: Multi-Repo Database Autoconfiguration

## New Command: `factory repo provision-db`

```
Usage: factory repo provision-db [namespace]

Provision databases for registered repos that declare a database config.
If namespace is given, provisions only that repo. Otherwise provisions all.

Arguments:
  namespace    Optional. Repo namespace (e.g., "myorg/myrepo") to provision.

Environment:
  DATABASE_URL   Required. Admin PostgreSQL connection string with CREATEDB/CREATEROLE privileges.

Exit codes:
  0   All provisioning succeeded (or no repos need provisioning)
  1   DATABASE_URL not set or cannot connect to admin database
  2   One or more repos failed to provision (partial failure)

Output (stdout):
  Provisioned myorg/myrepo → database "myapp_dev"
  Provisioned otherorg/other → database "other_dev"

Warnings (stderr):
  warning: myorg/broken: could not load pipeline config: ...
  error: myorg/failing: provision failed: ...

Note: Per-repo failures do not prevent other repos from provisioning (FR-012),
but the exit code reflects that failures occurred (exit 2).
```

## Modified Behavior: `factory repo add`

When a repo is added and its pipeline config contains a `database:` section, provisioning runs automatically after registration. If `DATABASE_URL` is not set, a warning is emitted and provisioning is skipped (non-fatal).

## Pipeline YAML Contract

```yaml
pipeline:
  name: myproject
  repo: myorg/myrepo

  # NEW: optional database provisioning
  database:
    name: myproject_dev       # PostgreSQL database name
    user: myproject           # PostgreSQL user (owner)
    password: myproject_dev   # User password
    migrate: "make migrate"   # Optional: migration command

  # NEW: optional env vars injected into Claude sessions
  env:
    API_KEY: "secret123"
    DEBUG: "true"

  # ... existing fields unchanged
```

## Session Env Var Injection Contract

When a session is created with `CreateOpts.Env`, the session manager sends tmux commands in this order:

1. `unset CLAUDECODE` (existing)
2. `export CLAUDE_CODE_OAUTH_TOKEN=...` (existing, if set)
3. `export API_KEY='secret123'` (new — sorted alphabetically)
4. `export DATABASE_URL='postgres://...'` (new — from DatabaseConfig.URL())
5. `export DEBUG='true'` (new — sorted alphabetically)
6. `claude ...` (existing — the Claude launch command)

Values are shell-quoted using the existing `shellQuote` helper in `internal/session/session.go` (single quotes with escaped embedded quotes).

**Env merge precedence**: The env map is built by first copying `Pipeline.Env`, then setting `DATABASE_URL` from `DatabaseConfig.URL()` if `Pipeline.Database` is configured. This means the auto-generated `DATABASE_URL` always wins if both are present. A warning is logged when a user-provided `DATABASE_URL` in `env:` is overridden.
