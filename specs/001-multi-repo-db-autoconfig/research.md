# Research: Multi-Repo Database Autoconfiguration

## R1: Idempotent PostgreSQL Provisioning

**Decision**: Use `CREATE ROLE ... IF NOT EXISTS` for user creation and `CREATE DATABASE` with error detection for "already exists" (PG error code 42P04) for database creation.

**Rationale**: PostgreSQL supports `CREATE ROLE IF NOT EXISTS` (9.0+), which handles user idempotency natively. However, `CREATE DATABASE IF NOT EXISTS` does not exist in PostgreSQL, so database creation uses execute-and-catch: run `CREATE DATABASE`, catch error code 42P04 ("duplicate_database"), and treat it as success. This avoids a query-then-create race condition.

**Alternatives considered**:
- `SELECT FROM pg_database WHERE datname = ...` then conditionally create: Rejected — race condition between check and create.
- PL/pgSQL `DO $$ BEGIN ... EXCEPTION WHEN ... END $$`: Rejected — adds complexity for no benefit, and `CREATE DATABASE` cannot run inside a transaction block.
- `CREATE USER` with error code 42710 catch: Viable but unnecessary — `CREATE ROLE IF NOT EXISTS` is cleaner. Use `CREATE ROLE ... WITH LOGIN PASSWORD ...` instead.

## R2: Password Escaping in SQL Statements

**Decision**: Escape single quotes in passwords by doubling them (`'` → `''`). This is standard SQL escaping.

**Rationale**: Passwords are user-provided strings that may contain single quotes. The provisioning SQL uses `CREATE USER "name" WITH PASSWORD 'pass'` — the password must be SQL-escaped.

**Alternatives considered**:
- Use parameterized queries: Rejected — `CREATE USER` and `CREATE DATABASE` are DDL statements that don't support parameterized values in most PostgreSQL drivers.
- Base64-encode passwords: Rejected — unnecessary complexity, PostgreSQL expects plaintext in `WITH PASSWORD`.

## R3: Env Var Export Order in tmux

**Decision**: Sort env var keys alphabetically before exporting. Each var is exported as a separate `tmux send-keys` call: `export KEY=VALUE`.

**Rationale**: Deterministic ordering makes debugging reproducible — the same config always produces the same sequence of export commands. Separate `send-keys` calls (vs. a single concatenated command) are more robust in tmux and easier to trace in session logs.

**Alternatives considered**:
- Single `send-keys` with all exports chained by `&&`: Rejected — long lines can be truncated in tmux, harder to debug.
- `tmux set-environment`: Rejected — sets vars on the tmux server, not in the shell session. Claude would not see them.

## R4: Setup Command Execution Strategy

**Decision**: Extend the orchestrator's existing `runSetupWith()` to accept and inject env vars (DATABASE_URL + custom env vars) into the subprocess environment. Setup runs via `sh -c` in the worktree directory. Fail pipeline creation on any non-zero exit.

**Rationale**: The orchestrator already calls `runSetupWith()` during `Create()` — this runs setup commands exactly once per pipeline, before the first stage. Extending it with env var injection is the minimal change. `sh -c` handles pipes, redirects, and multi-word commands naturally. The worktree directory is the correct CWD since setup commands (like `npm install` or `make migrate`) operate on the project. Failing hard on setup errors prevents Claude from running in a broken environment.

**Alternatives considered**:
- Run setup in tmux (same session as Claude): Rejected — setup should complete before Claude starts, and failures should prevent the session from starting.
- Move setup to the stage engine (`Engine.Run()`): Rejected — the stage engine runs for every stage (implement, review, QA, merge), which would execute setup commands multiple times. Non-idempotent migrations would be destructive. The orchestrator's `Create()` is the correct location for once-per-pipeline work.
- Create a parallel setup mechanism in the stage engine: Rejected — duplicates the existing `runSetupWith` and risks double execution.

## R5: Admin Connection for Provisioning

**Decision**: Use the factory's `DATABASE_URL` environment variable as the base connection string, but **parse and override the database component to `postgres`** before connecting. The factory's PostgreSQL user must have `CREATEDB` and `CREATEROLE` privileges.

**Rationale**: `CREATE DATABASE` is DDL that must be executed while connected to an existing database — the `postgres` maintenance database is the standard target. The factory's `DATABASE_URL` may point to the factory's own database (e.g., `factory_events`), which might not exist yet on a cold pod start. Parsing the URL and overriding only the database name to `postgres` ensures provisioning works even when the factory DB itself hasn't been created. The existing `pgx/v5` driver (registered as `"pgx"` in `internal/db/db.go`) must be used — not `"postgres"` (which would require the unused `lib/pq` driver).

**Alternatives considered**:
- Separate `ADMIN_DATABASE_URL` env var: Rejected — unnecessary for the single-sidecar architecture. Can be added later if multi-instance support is needed.
- Pass `DATABASE_URL` directly without override: Rejected — fails on cold start when the factory DB doesn't exist yet, and is semantically wrong (DDL should target the maintenance DB).

## R6: Shell Quoting for Env Var Values

**Decision**: Reuse the existing `shellQuote` helper in `internal/session/session.go` (line 62) that wraps values in single quotes and escapes embedded single quotes (`'` → `'\''`). This handles spaces, special characters, and shell metacharacters.

**Rationale**: The helper already exists and is tested (`session_test.go`). Env var values (especially DATABASE_URL with passwords) may contain characters that the shell would interpret. Single-quoting with proper escaping is the most robust approach for `tmux send-keys`. No new code needed — just call the existing function.

**Alternatives considered**:
- Double-quoting with `\"` escaping: Rejected — double quotes still expand `$`, `` ` ``, and `\`, creating injection risk.
- No quoting: Rejected — values with spaces or special characters would break.
