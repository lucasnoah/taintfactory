# Feature Specification: Multi-Repo Database Autoconfiguration

**Feature Branch**: `001-multi-repo-db-autoconfig`
**Created**: 2026-03-06
**Status**: Draft
**Input**: User description: "Auto-provision per-repo PostgreSQL databases on the shared sidecar and inject DATABASE_URL + env vars into Claude sessions."

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Declare Database Needs in Pipeline Config (Priority: P1)

As a factory operator managing multiple project repos, I add a `database` section to a repo's `pipeline.yaml` specifying a database name, user, and password. When the factory processes issues for that repo, it automatically provisions the database and makes the connection available to Claude sessions — I never manually run CREATE DATABASE or export DATABASE_URL.

**Why this priority**: Without config-driven database declaration, nothing else in this feature works. This is the foundational data contract between the operator and the factory.

**Independent Test**: Can be tested by adding a `database:` block to a pipeline.yaml, running config validation, and confirming the fields are parsed correctly.

**Acceptance Scenarios**:

1. **Given** a pipeline.yaml with a `database:` section containing name, user, password, and migrate fields, **When** the factory loads the config, **Then** all database fields are accessible and a connection URL can be derived from them.
2. **Given** a pipeline.yaml without a `database:` section, **When** the factory loads the config, **Then** the database config is absent and no provisioning is attempted.
3. **Given** a pipeline.yaml with a `database:` section and an `env:` section containing custom key-value pairs, **When** the factory loads the config, **Then** both database config and custom env vars are available for injection.

---

### User Story 2 - Automatic Database Provisioning (Priority: P1)

As a factory operator, when I register a new repo (or re-provision on pod restart), the factory connects to the shared PostgreSQL instance and creates the declared database and user if they don't already exist. I see confirmation output and the database is immediately usable.

**Why this priority**: Provisioning is the core automated action — it replaces the manual step of SSHing into Postgres and running DDL.

**Independent Test**: Can be tested by running the provisioning logic against a test PostgreSQL instance and confirming the database and user exist afterward. Running it twice confirms idempotency.

**Acceptance Scenarios**:

1. **Given** a repo with database config declaring name "myapp_dev" and user "myapp", **When** the factory provisions the database, **Then** a PostgreSQL database "myapp_dev" exists owned by user "myapp" with full privileges granted.
2. **Given** a database and user that already exist, **When** provisioning runs again, **Then** it completes without error (idempotent).
3. **Given** no admin database connection available, **When** provisioning is attempted, **Then** the factory emits a warning and skips provisioning without failing the overall operation.

---

### User Story 3 - Env Vars Injected into Claude Sessions (Priority: P1)

As an automated pipeline, when a Claude Code session is spawned for an issue, the factory exports `DATABASE_URL` (derived from the database config) and any custom `env:` vars into the tmux session before launching Claude. Claude can then use these variables to connect to the repo's database and access project-specific credentials.

**Why this priority**: This is the delivery mechanism — without env var injection, provisioning a database has no effect on the Claude session.

**Independent Test**: Can be tested by creating a session with env vars and verifying that export commands are sent to tmux before the Claude launch command.

**Acceptance Scenarios**:

1. **Given** a pipeline config with database config and custom env vars, **When** a Claude session is created, **Then** `DATABASE_URL` and all custom env vars are exported in the tmux session before the Claude command runs.
2. **Given** a pipeline config with no database or env config, **When** a Claude session is created, **Then** no export commands are sent (backward compatible).
3. **Given** multiple env vars, **When** they are exported, **Then** the export order is deterministic (sorted by key name) for reproducibility.

---

### User Story 4 - Setup Commands Run Before First Session (Priority: P2)

As a factory operator, I define `setup:` commands in pipeline.yaml (e.g., `go mod download`, `npm install`, `make migrate`). Before the first Claude session starts for an issue, the factory runs these commands in the worktree directory with the database and env vars available. If a migration command is specified in the database config, it runs after setup commands.

**Why this priority**: Setup commands ensure the worktree is ready for development. Without them, Claude sessions may fail on missing dependencies or un-migrated databases.

**Independent Test**: Can be tested by configuring setup commands, running the stage engine, and verifying the commands executed in the correct directory with the correct environment.

**Acceptance Scenarios**:

1. **Given** a pipeline with setup commands and a database with a migrate command, **When** the stage engine runs, **Then** setup commands execute in order, followed by the migration command, all in the worktree directory with DATABASE_URL available.
2. **Given** a setup command that fails (non-zero exit), **When** the stage engine runs, **Then** the stage fails with a clear error message including the command output.
3. **Given** no setup commands configured, **When** the stage engine runs, **Then** the session proceeds normally without any pre-commands.

---

### User Story 5 - Re-Provision on Pod Restart (Priority: P2)

As a factory deployed on Kubernetes, when the pod restarts the entrypoint script re-provisions databases for all registered repos. This ensures databases exist even if the PostgreSQL sidecar was recreated or the persistent volume was lost.

**Why this priority**: Without restart resilience, a pod restart could leave repos without their databases, breaking all subsequent pipeline runs.

**Independent Test**: Can be tested by running `factory repo provision-db` and verifying it provisions databases for all registered repos that have database configs.

**Acceptance Scenarios**:

1. **Given** three registered repos (two with database configs, one without), **When** `factory repo provision-db` runs, **Then** it provisions databases for the two configured repos and skips the third.
2. **Given** the pod entrypoint script, **When** it executes on startup, **Then** it calls database provisioning after repo cloning and before starting the web server.
3. **Given** a repo whose config file is missing or invalid, **When** provisioning runs, **Then** it logs a warning for that repo and continues provisioning the others.

---

### User Story 6 - Standalone Re-Provision Command (Priority: P3)

As a factory operator, I can run `factory repo provision-db` to manually re-provision databases for all registered repos, or `factory repo provision-db [namespace]` for a specific repo. This is useful for recovery after database issues.

**Why this priority**: Manual re-provisioning is a recovery tool — less critical than automated flows but important for operational resilience.

**Independent Test**: Can be tested by running the command and verifying database existence.

**Acceptance Scenarios**:

1. **Given** a registered repo with database config, **When** I run `factory repo provision-db myorg/myrepo`, **Then** it provisions only that repo's database and prints confirmation.
2. **Given** no admin database connection available, **When** I run `factory repo provision-db`, **Then** it returns an error explaining the admin connection is required.

---

### Edge Cases

- What happens when the database password contains special characters (single quotes, backslashes)? Passwords MUST be properly escaped in provisioning statements.
- What happens when the admin database connection is unavailable during provisioning? The factory MUST emit a warning and continue without blocking the pipeline.
- What happens when two repos declare the same database name? The second provisioning attempt succeeds silently (idempotent), but both repos share the database — this is operator responsibility.
- What happens when setup commands need env vars that depend on the database being provisioned first? Database provisioning MUST complete before setup commands run.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: The pipeline config MUST accept an optional `database` section with fields: name, user, password, and migrate command.
- **FR-002**: The pipeline config MUST accept an optional `env` section as a key-value map of environment variables.
- **FR-003**: A connection URL MUST be derivable from the database config fields (host defaults to localhost, port defaults to 5432).
- **FR-004**: Database provisioning MUST create the PostgreSQL user, database, and grant privileges using the admin connection.
- **FR-005**: Database provisioning MUST be idempotent — running it multiple times MUST NOT produce errors for already-existing databases or users.
- **FR-006**: The session manager MUST export DATABASE_URL and all custom env vars into the tmux session before launching the Claude command.
- **FR-007**: Env var exports MUST occur in deterministic (sorted) order for reproducibility.
- **FR-008**: The stage engine MUST execute pipeline setup commands in the worktree directory with database and env vars available in the environment.
- **FR-009**: The stage engine MUST execute the database migrate command (if configured) after setup commands complete.
- **FR-010**: A CLI command `factory repo provision-db [namespace]` MUST exist for manual re-provisioning.
- **FR-011**: The Kubernetes entrypoint script MUST call database provisioning after repo cloning and before starting the serve command.
- **FR-012**: Provisioning failures for individual repos MUST NOT prevent other repos from being provisioned (fail-open per repo).
- **FR-013**: When the admin database connection is not available and provisioning is needed, the system MUST emit a warning rather than failing hard.

### Key Entities

- **DatabaseConfig**: Declares a repo's database needs — name, user, password, and optional migration command. Produces a connection URL.
- **Pipeline Env**: A map of key-value pairs representing environment variables to inject into Claude sessions alongside DATABASE_URL.
- **Provisioner**: Connects to the shared admin PostgreSQL instance and executes user/database/privilege creation idempotently.

## Assumptions

- The shared PostgreSQL sidecar is accessible at `localhost:5432` from the factory pod. No remote or multi-host database connections are needed.
- The admin connection string comes from an environment variable with CREATE USER/DATABASE privileges.
- Database passwords are stored in pipeline.yaml, which is committed to the repo. Operators accept this tradeoff for simplicity (secrets management is a future concern).
- Setup commands run synchronously and block the stage until complete. No timeout beyond the overall stage timeout.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: An operator can add a `database:` block to pipeline.yaml and have a working database available to Claude sessions within one pipeline run — zero manual database administration required.
- **SC-002**: Re-provisioning on pod restart completes for all registered repos without operator intervention.
- **SC-003**: Existing pipelines without database configuration continue to work identically — full backward compatibility with zero config changes.
- **SC-004**: Provisioning the same database twice produces no errors and no data loss.
- **SC-005**: Claude sessions for database-configured repos can connect to their provisioned database using the injected DATABASE_URL on the first stage attempt.
