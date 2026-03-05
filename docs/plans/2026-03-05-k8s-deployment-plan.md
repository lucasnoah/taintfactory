# K8s Deployment Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Deploy taintfactory to a personal k8s cluster on DigitalOcean with always-on automation, a public dashboard with auth, and PostgreSQL replacing SQLite.

**Architecture:** Single StatefulSet pod running `factory serve --with-orchestrator` alongside a `docker:dind` sidecar. Postgres runs in a separate single-replica StatefulSet. Web UI exposed via Ingress with basic-auth. See `docs/plans/2026-03-05-k8s-deployment-design.md` for full design.

**Tech Stack:** Go (pgx/v5/stdlib), PostgreSQL 16, Docker (multi-stage build), Kubernetes (StatefulSet, Ingress, Secrets), DigitalOcean Container Registry.

---

### Task 1: FACTORY_DATA_DIR env var

Adds support for `FACTORY_DATA_DIR` env var (default: `~/.factory`) so the container can use `/data` instead.

**Files:**
- Modify: `internal/db/db.go:19-29` (DefaultDBPath)
- Modify: `internal/pipeline/store.go:23-33` (DefaultStore)
- Modify: `internal/triage/state.go:60-66` (DefaultTriageDir)
- Modify: `internal/triage/state.go:69-77` (DefaultStore)
- Modify: `internal/session/env.go:12-21` (loadOAuthToken)
- Create: `internal/config/datadir.go`
- Test: `internal/config/datadir_test.go`

**Step 1: Write the failing test**

Create `internal/config/datadir_test.go`:

```go
package config

import (
	"os"
	"testing"
)

func TestDataDir_Default(t *testing.T) {
	os.Unsetenv("FACTORY_DATA_DIR")
	dir := DataDir()
	home, _ := os.UserHomeDir()
	if dir != home+"/.factory" {
		t.Errorf("DataDir() = %q, want %q", dir, home+"/.factory")
	}
}

func TestDataDir_EnvOverride(t *testing.T) {
	t.Setenv("FACTORY_DATA_DIR", "/data")
	dir := DataDir()
	if dir != "/data" {
		t.Errorf("DataDir() = %q, want /data", dir)
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/config/ -run TestDataDir -v`
Expected: FAIL — `DataDir` not defined

**Step 3: Write minimal implementation**

Create `internal/config/datadir.go`:

```go
package config

import (
	"os"
	"path/filepath"
)

// DataDir returns the base directory for factory state.
// Uses FACTORY_DATA_DIR env var if set, otherwise ~/.factory.
func DataDir() string {
	if v := os.Getenv("FACTORY_DATA_DIR"); v != "" {
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".factory"
	}
	return filepath.Join(home, ".factory")
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./internal/config/ -run TestDataDir -v`
Expected: PASS

**Step 5: Update callers to use DataDir()**

Update `internal/db/db.go` — replace `DefaultDBPath()`:

```go
func DefaultDBPath() (string, error) {
	dir := config.DataDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create directory %s: %w", dir, err)
	}
	return filepath.Join(dir, "factory.db"), nil
}
```

Add import: `"github.com/lucasnoah/taintfactory/internal/config"`

Update `internal/pipeline/store.go` — replace `DefaultStore()`:

```go
func DefaultStore() (*Store, error) {
	dir := filepath.Join(config.DataDir(), "pipelines")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir %s: %w", dir, err)
	}
	return &Store{baseDir: dir}, nil
}
```

Add import: `"github.com/lucasnoah/taintfactory/internal/config"`

Update `internal/triage/state.go` — `DefaultTriageDir()`:

```go
func DefaultTriageDir() (string, error) {
	return filepath.Join(config.DataDir(), "triage"), nil
}
```

Add import: `"github.com/lucasnoah/taintfactory/internal/config"`

Update `internal/session/env.go` — `loadOAuthToken()`:

```go
func loadOAuthToken() string {
	if v := os.Getenv("CLAUDE_CODE_OAUTH_TOKEN"); v != "" {
		return v
	}
	return readEnvFileVar(filepath.Join(config.DataDir(), ".env"), "CLAUDE_CODE_OAUTH_TOKEN")
}
```

Add import: `"github.com/lucasnoah/taintfactory/internal/config"`

**Step 6: Run full test suite**

Run: `go test ./... -short`
Expected: PASS (all callers still work because env var isn't set, so it falls back to `~/.factory`)

**Step 7: Commit**

```bash
git add internal/config/datadir.go internal/config/datadir_test.go \
        internal/db/db.go internal/pipeline/store.go \
        internal/triage/state.go internal/session/env.go
git commit -m "feat: add FACTORY_DATA_DIR env var for configurable data directory"
```

---

### Task 2: Add /healthz endpoint

**Files:**
- Modify: `internal/web/server.go:317-339` (Start method, add route)
- Test: `internal/web/server_test.go`

**Step 1: Write the failing test**

Add to `internal/web/server_test.go` (or create a new test):

```go
func TestHealthz(t *testing.T) {
	s := NewServer(nil, nil, 0, "")
	mux := s.buildMux()

	req := httptest.NewRequest("GET", "/healthz", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("content-type = %q, want application/json", ct)
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/web/ -run TestHealthz -v`
Expected: FAIL — `buildMux` not defined (or route not registered)

**Step 3: Refactor Start() to extract buildMux()**

In `internal/web/server.go`, extract the mux construction from `Start()` into `buildMux()`:

```go
func (s *Server) buildMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHealthz)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// ... existing routing logic ...
	})
	mux.HandleFunc("/queue", s.handleQueue)
	mux.HandleFunc("/config", s.handleConfig)
	return mux
}

func (s *Server) Start() error {
	mux := s.buildMux()
	addr := fmt.Sprintf(":%d", s.port)
	log.Printf("TaintFactory UI: http://localhost%s", addr)
	return http.ListenAndServe(addr, mux)
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"status":"ok"}`))
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./internal/web/ -run TestHealthz -v`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/web/server.go internal/web/server_test.go
git commit -m "feat(web): add /healthz endpoint for k8s probes"
```

---

### Task 3: Add --with-orchestrator flag to serve command

**Files:**
- Modify: `internal/cli/serve.go`
- Modify: `internal/cli/pipeline.go:366-426` (export newOrchestrator or extract shared setup)

**Step 1: Add the flag and orchestrator goroutine**

Update `internal/cli/serve.go`:

```go
var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the local web UI",
	Long: `Start a read-only browser UI showing pipeline state, history, and check results.

With --with-orchestrator, also runs the orchestrator check-in loop on a configurable
interval, combining the web UI and the automation loop in a single process.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		port, _ := cmd.Flags().GetInt("port")
		withOrch, _ := cmd.Flags().GetBool("with-orchestrator")
		orchInterval, _ := cmd.Flags().GetInt("orchestrator-interval")

		dbPath, err := db.DefaultDBPath()
		if err != nil {
			return fmt.Errorf("db path: %w", err)
		}
		database, err := db.Open(dbPath)
		if err != nil {
			return fmt.Errorf("open db: %w", err)
		}
		defer database.Close()

		if err := database.Migrate(); err != nil {
			return fmt.Errorf("migrate: %w", err)
		}

		store, err := pipeline.DefaultStore()
		if err != nil {
			return fmt.Errorf("store: %w", err)
		}

		if withOrch {
			orch, cleanup, err := newOrchestrator()
			if err != nil {
				return fmt.Errorf("init orchestrator: %w", err)
			}
			defer cleanup()

			go runOrchestratorLoop(orch, time.Duration(orchInterval)*time.Second)
		}

		triageDir, _ := triage.DefaultTriageDir()
		return web.NewServer(store, database, port, triageDir).Start()
	},
}

func runOrchestratorLoop(orch *orchestrator.Orchestrator, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	log.Printf("Orchestrator loop started (interval: %s)", interval)
	for range ticker.C {
		if _, err := orch.CheckIn(); err != nil {
			log.Printf("orchestrator check-in error: %v", err)
		}
		if err := discordPollTick(); err != nil {
			log.Printf("discord poll: %v", err)
		}
	}
}

func init() {
	serveCmd.Flags().Int("port", 17432, "Port to listen on")
	serveCmd.Flags().Bool("with-orchestrator", false, "Run orchestrator check-in loop alongside web server")
	serveCmd.Flags().Int("orchestrator-interval", 120, "Orchestrator check-in interval in seconds")
}
```

Add imports: `"log"`, `"time"`, plus packages for orchestrator and triage.

**Step 2: Verify it compiles**

Run: `go build ./cmd/factory/`
Expected: SUCCESS

**Step 3: Manual smoke test**

Run: `./bin/factory serve --help`
Expected: Shows `--with-orchestrator` and `--orchestrator-interval` flags

**Step 4: Commit**

```bash
git add internal/cli/serve.go
git commit -m "feat(cli): add --with-orchestrator flag to serve command"
```

---

### Task 4: PostgreSQL schema and driver swap

This is the core database migration. Replace `go-sqlite3` with `pgx/v5/stdlib`.

**Files:**
- Modify: `go.mod` (replace go-sqlite3 with pgx/v5)
- Modify: `internal/db/db.go` (driver, Open, schema, Migrate)
- Modify: `internal/db/queries.go` (all queries)
- Modify: `internal/db/db_test.go` (test helper)
- Modify: `internal/web/queries.go` (web-specific queries)

**Step 1: Update go.mod**

```bash
go get github.com/jackc/pgx/v5
```

Remove `github.com/mattn/go-sqlite3` from go.mod (after all code changes are made).

**Step 2: Rewrite `internal/db/db.go`**

Replace the entire file. Key changes:

1. Driver: `sqlite3` → `pgx` (via `pgx/v5/stdlib`)
2. `Open(path)` → `Open(connStr)` where connStr is a `postgres://` URL
3. Schema: rewrite with Postgres types
4. `Migrate()`: simplified — just run the schema DDL (Postgres supports `IF NOT EXISTS` natively, `CREATE INDEX IF NOT EXISTS`, etc.)
5. Remove all SQLite PRAGMAs

New Postgres schema (single version, clean start):

```sql
CREATE TABLE IF NOT EXISTS schema_version (
    version    INTEGER PRIMARY KEY,
    applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS session_events (
    id          SERIAL PRIMARY KEY,
    session_id  TEXT NOT NULL,
    issue       INTEGER NOT NULL,
    stage       TEXT NOT NULL,
    event       TEXT NOT NULL CHECK(event IN ('started','active','idle','exited','factory_send','steer','human_input')),
    exit_code   INTEGER,
    timestamp   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    metadata    TEXT,
    namespace   TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_session_latest ON session_events(session_id, timestamp DESC);
CREATE INDEX IF NOT EXISTS idx_session_issue ON session_events(issue, stage);

CREATE TABLE IF NOT EXISTS check_runs (
    id          SERIAL PRIMARY KEY,
    namespace   TEXT NOT NULL DEFAULT '',
    issue       INTEGER NOT NULL,
    stage       TEXT NOT NULL,
    attempt     INTEGER NOT NULL,
    fix_round   INTEGER NOT NULL DEFAULT 0,
    check_name  TEXT NOT NULL,
    passed      BOOLEAN NOT NULL,
    auto_fixed  BOOLEAN DEFAULT FALSE,
    exit_code   INTEGER,
    duration_ms INTEGER,
    summary     TEXT,
    findings    TEXT,
    timestamp   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_check_issue_stage ON check_runs(issue, stage, fix_round);
CREATE INDEX IF NOT EXISTS idx_check_ns_issue ON check_runs(namespace, issue, stage, fix_round);

CREATE TABLE IF NOT EXISTS pipeline_events (
    id          SERIAL PRIMARY KEY,
    namespace   TEXT NOT NULL DEFAULT '',
    issue       INTEGER NOT NULL,
    event       TEXT NOT NULL,
    stage       TEXT,
    attempt     INTEGER,
    detail      TEXT,
    timestamp   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_pipeline_issue ON pipeline_events(issue, timestamp DESC);
CREATE INDEX IF NOT EXISTS idx_pipeline_ns_issue ON pipeline_events(namespace, issue, timestamp DESC);

CREATE TABLE IF NOT EXISTS issue_queue (
    id             SERIAL PRIMARY KEY,
    namespace      TEXT NOT NULL DEFAULT '',
    issue          INTEGER NOT NULL,
    status         TEXT NOT NULL DEFAULT 'pending'
                   CHECK(status IN ('pending','active','completed','failed')),
    position       INTEGER NOT NULL,
    feature_intent TEXT NOT NULL DEFAULT '',
    depends_on     JSONB NOT NULL DEFAULT '[]',
    config_path    TEXT NOT NULL DEFAULT '',
    added_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    started_at     TIMESTAMPTZ,
    finished_at    TIMESTAMPTZ,
    UNIQUE(namespace, issue)
);
CREATE INDEX IF NOT EXISTS idx_queue_status_position ON issue_queue(status, position);
CREATE INDEX IF NOT EXISTS idx_queue_ns_issue ON issue_queue(namespace, issue);
```

New `Open()`:

```go
import (
	"database/sql"
	"fmt"
	"os"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/lucasnoah/taintfactory/internal/config"
)

type DB struct {
	conn *sql.DB
}

// DefaultConnStr returns the DATABASE_URL env var.
func DefaultConnStr() (string, error) {
	url := os.Getenv("DATABASE_URL")
	if url != "" {
		return url, nil
	}
	return "", fmt.Errorf("DATABASE_URL not set")
}

func Open(connStr string) (*DB, error) {
	conn, err := sql.Open("pgx", connStr)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	if err := conn.Ping(); err != nil {
		conn.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return &DB{conn: conn}, nil
}
```

New `Migrate()`:

```go
func (d *DB) Migrate() error {
	_, err := d.conn.Exec(schema)
	if err != nil {
		return fmt.Errorf("apply schema: %w", err)
	}
	// Record version if not already present
	_, err = d.conn.Exec("INSERT INTO schema_version (version) VALUES (1) ON CONFLICT DO NOTHING")
	if err != nil {
		return fmt.Errorf("record schema version: %w", err)
	}
	return nil
}
```

New `Reset()`:

```go
func (d *DB) Reset() error {
	tables := []string{"issue_queue", "pipeline_events", "check_runs", "session_events", "schema_version"}
	for _, t := range tables {
		if _, err := d.conn.Exec("DROP TABLE IF EXISTS " + t + " CASCADE"); err != nil {
			return fmt.Errorf("drop table %s: %w", t, err)
		}
	}
	return d.Migrate()
}
```

**Step 3: Update all callers of `DefaultDBPath()` → `DefaultConnStr()`**

All callers in `internal/cli/` that call `db.DefaultDBPath()` then `db.Open(path)` need to become `db.DefaultConnStr()` then `db.Open(connStr)`.

Files to update:
- `internal/cli/serve.go` (already modified in Task 3)
- `internal/cli/pipeline.go:372-376` (in `newOrchestrator`)
- `internal/cli/db.go` (db commands)
- `internal/cli/event.go`
- `internal/cli/status.go`
- `internal/cli/analytics.go`
- `internal/cli/check.go`
- `internal/cli/queue.go`
- `internal/cli/discord.go`

Each follows the same pattern:
```go
// Before:
dbPath, err := db.DefaultDBPath()
database, err := db.Open(dbPath)

// After:
connStr, err := db.DefaultConnStr()
database, err := db.Open(connStr)
```

Search for all callers with: `grep -rn "DefaultDBPath\|db.Open" internal/cli/`

**Step 4: Remove go-sqlite3 dependency**

```bash
# After all code changes compile:
go mod tidy
```

Verify `go-sqlite3` is gone from `go.mod` and `go.sum`.

**Step 5: Verify it compiles**

Run: `go build ./cmd/factory/`
Expected: SUCCESS (CGO no longer required!)

**Step 6: Commit**

```bash
git add go.mod go.sum internal/db/db.go internal/cli/*.go
git commit -m "feat(db): replace SQLite with PostgreSQL via pgx/v5"
```

---

### Task 5: Port all queries to PostgreSQL syntax

Replace `?` placeholders with `$N`, port SQLite datetime functions, port `json_each`.

**Files:**
- Modify: `internal/db/queries.go`
- Modify: `internal/web/queries.go`

**Detailed query changes in `internal/db/queries.go`:**

**LogSessionEvent** (line 53-62):
```go
// Before: VALUES (?, ?, ?, ?, ?, ?)
// After:
`INSERT INTO session_events (session_id, issue, stage, event, exit_code, metadata) VALUES ($1, $2, $3, $4, $5, $6)`
```

**GetSessionState** (line 65-89):
```go
// Before: WHERE session_id = ? ORDER BY ...
// After:
`SELECT id, session_id, issue, stage, event, exit_code, timestamp, metadata
 FROM session_events WHERE session_id = $1 ORDER BY timestamp DESC, id DESC LIMIT 1`
```

**GetSessionStartedAt** (line 92-107):
```go
// $1 for session_id
`SELECT timestamp FROM session_events WHERE session_id = $1 AND event = 'started' ORDER BY id ASC LIMIT 1`
```

**HasRecentSteer** (line 109-122):
```go
// Before: datetime('now', ?) — the caller passes "-10 minutes"
// After: NOW() + $2::interval — the caller still passes "-10 minutes" which is valid Postgres interval syntax
`SELECT COUNT(*) FROM session_events
 WHERE session_id = $1 AND event = 'steer'
 AND timestamp >= NOW() + $2::interval`
```

**GetAllActiveSessions** (line 125-159): No placeholder changes, but verify subquery syntax is Postgres-compatible (it is — standard SQL).

**DetectHumanIntervention** (line 163-190):
```go
// First query: $1
`SELECT timestamp FROM session_events
 WHERE session_id = $1 AND event = 'active'
 ORDER BY timestamp DESC, id DESC LIMIT 1`

// Second query:
// Before: datetime(?, '-5 seconds')
// After: $1::timestamptz - interval '5 seconds'
`SELECT COUNT(*) FROM session_events
 WHERE session_id = $1 AND event = 'factory_send'
 AND timestamp BETWEEN $2::timestamptz - interval '5 seconds' AND $2::timestamptz`
```

Note: the second query references `$2` twice (the `activeTimestamp` value). Adjust the args accordingly: pass `sessionID, activeTimestamp, activeTimestamp` → `sessionID, activeTimestamp`.

Wait — actually the BETWEEN uses the same value twice. In the current code:
```go
d.conn.QueryRow(`...BETWEEN datetime(?, '-5 seconds') AND ?`, sessionID, activeTimestamp, activeTimestamp)
```

For Postgres:
```go
d.conn.QueryRow(`...BETWEEN $2::timestamptz - interval '5 seconds' AND $2::timestamptz`, sessionID, activeTimestamp)
```

pgx with `database/sql` doesn't support reusing `$2` twice with one arg. You need to pass it twice:
```go
d.conn.QueryRow(`...BETWEEN $2::timestamptz - interval '5 seconds' AND $3::timestamptz`, sessionID, activeTimestamp, activeTimestamp)
```

**LogCheckRun** (line 193-203):
```go
`INSERT INTO check_runs (namespace, issue, stage, attempt, fix_round, check_name, passed, auto_fixed, exit_code, duration_ms, summary, findings)
 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`
```

**GetCheckRuns** (line 206-240):
```go
`SELECT ... FROM check_runs WHERE namespace = $1 AND issue = $2 AND stage = $3 AND fix_round = $4 ORDER BY id`
```

**GetLatestCheckRun** (line 243-272):
```go
`SELECT ... FROM check_runs WHERE namespace = $1 AND issue = $2 AND check_name = $3 ORDER BY id DESC LIMIT 1`
```

**LogPipelineEvent** (line 275-284):
```go
`INSERT INTO pipeline_events (namespace, issue, event, stage, attempt, detail) VALUES ($1, $2, $3, $4, $5, $6)`
```

**GetPipelineHistory** (line 287-318):
```go
`SELECT ... FROM pipeline_events WHERE namespace = $1 AND issue = $2 ORDER BY timestamp DESC, id DESC`
```

**GetLatestFailedChecks** (line 321-364):
```go
// Subquery uses $1, $2, $3 for namespace, issue, stage
`SELECT cr.id, ... FROM check_runs cr
 INNER JOIN (
     SELECT check_name, MAX(id) as max_id
     FROM check_runs
     WHERE namespace = $1 AND issue = $2 AND stage = $3
     GROUP BY check_name
 ) latest ON cr.id = latest.max_id
 WHERE cr.passed = false
 ORDER BY cr.check_name`
```

Note: `cr.passed = 0` → `cr.passed = false` (Postgres boolean).

**QueueAdd** (line 391-432):
```go
// Prepared statement:
`INSERT INTO issue_queue (namespace, issue, position, feature_intent, depends_on, config_path) VALUES ($1, $2, $3, $4, $5::jsonb, $6)`
```

Note: `depends_on` column is now `JSONB`, so cast the string with `$5::jsonb`.

Also: the UNIQUE constraint error message may differ — check for "duplicate key" or use the `pq` error code. Or better, just check for `strings.Contains(err.Error(), "duplicate key")`.

**QueueSetIntent** (line 435-448):
```go
`UPDATE issue_queue SET feature_intent = $1 WHERE namespace = $2 AND issue = $3`
```

**QueueList** (line 451-482):
```go
`SELECT id, namespace, issue, status, position, feature_intent, depends_on, config_path, added_at, started_at, finished_at
 FROM issue_queue ORDER BY position`
```

Note: `depends_on` is now `JSONB`. When scanning, use `[]byte` or `string` — pgx can scan JSONB to string.

**QueueNext** (line 487-523):
```go
// Before: json_each(q.depends_on) je ON CAST(je.value AS INTEGER) = dep.issue
// After: jsonb_array_elements_text(q.depends_on) je(value) ON je.value::int = dep.issue
`SELECT q.id, q.namespace, q.issue, q.status, q.position, q.feature_intent, q.depends_on,
        q.config_path, q.added_at, q.started_at, q.finished_at
 FROM issue_queue q
 WHERE q.status = 'pending'
 AND NOT EXISTS (
     SELECT 1 FROM issue_queue dep
     JOIN jsonb_array_elements_text(q.depends_on) je(value) ON je.value::int = dep.issue
     WHERE dep.namespace = q.namespace
       AND dep.status != 'completed'
 )
 ORDER BY q.position ASC LIMIT 1`
```

**QueueUpdateStatus** (line 527-557):
```go
// "active" case:
`UPDATE issue_queue SET status = $1, started_at = NOW() WHERE namespace = $2 AND issue = $3`
// "completed"/"failed" case:
`UPDATE issue_queue SET status = $1, finished_at = NOW() WHERE namespace = $2 AND issue = $3`
// default:
`UPDATE issue_queue SET status = $1 WHERE namespace = $2 AND issue = $3`
```

**QueueRemove** (line 560-573):
```go
`DELETE FROM issue_queue WHERE namespace = $1 AND issue = $2`
```

**QueueDependents** (line 578-619):
```go
// Before: json_each(depends_on) je WHERE CAST(je.value AS INTEGER) = ?
// After:
`SELECT ... FROM issue_queue
 WHERE namespace = $1
 AND status IN ('pending', 'active')
 AND EXISTS (
     SELECT 1 FROM jsonb_array_elements_text(depends_on) je(value)
     WHERE je.value::int = $2
 )
 ORDER BY position`
```

**QueueClear** (line 622-632): No placeholder changes needed.

**GetPipelineEventsSince** (line 637-668):
```go
`SELECT id, issue, event, stage, attempt, detail, timestamp
 FROM pipeline_events WHERE id > $1 ORDER BY id ASC LIMIT $2`
```

**GetQueueItem** (line 671-699):
```go
`SELECT id, issue, status, position, feature_intent, depends_on, config_path, added_at, started_at, finished_at
 FROM issue_queue WHERE issue = $1`
```

**GetCheckHistory** (line 702-736):
```go
`SELECT ... FROM check_runs WHERE namespace = $1 AND issue = $2 ORDER BY id DESC`
```

**Changes in `internal/web/queries.go`:**

**recentActivity** (line 11-42):
```go
`SELECT id, namespace, issue, event, stage, attempt, detail, timestamp
 FROM pipeline_events ORDER BY id DESC LIMIT $1`
```

**checkRunsForAttempt** (line 45-83):
```go
`SELECT ... FROM check_runs
 WHERE namespace = $1 AND issue = $2 AND stage = $3 AND attempt = $4
 ORDER BY fix_round, id`
```

**Step 1: Make all the query changes**

Apply every change listed above in `internal/db/queries.go` and `internal/web/queries.go`.

**Step 2: Verify it compiles**

Run: `go build ./cmd/factory/`
Expected: SUCCESS

**Step 3: Commit**

```bash
git add internal/db/queries.go internal/web/queries.go
git commit -m "feat(db): port all SQL queries to PostgreSQL syntax"
```

---

### Task 6: Port tests to PostgreSQL

**Files:**
- Modify: `internal/db/db_test.go`
- Modify: `go.mod` (add testcontainers-go or embedded-postgres)

**Step 1: Add test dependency**

```bash
go get github.com/testcontainers/testcontainers-go
go get github.com/testcontainers/testcontainers-go/modules/postgres
```

**Step 2: Rewrite testDB helper**

Replace the `:memory:` SQLite helper with a Postgres container:

```go
package db

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

func testDB(t *testing.T) *DB {
	t.Helper()
	ctx := context.Background()

	pgContainer, err := postgres.Run(ctx,
		"postgres:16-alpine",
		postgres.WithDatabase("factory_test"),
		postgres.WithUsername("test"),
		postgres.WithPassword("test"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(5*time.Second),
		),
	)
	if err != nil {
		t.Fatalf("start postgres: %v", err)
	}
	t.Cleanup(func() { pgContainer.Terminate(ctx) })

	connStr, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}

	d, err := Open(connStr)
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := d.Migrate(); err != nil {
		t.Fatalf("migrate test db: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}
```

**Step 3: Update test assertions**

Remove all references to `sqlite_master` and `pragma_table_info`. Replace with Postgres equivalents:

```go
// Before: SELECT name FROM sqlite_master WHERE type='table' AND name=?
// After:
`SELECT table_name FROM information_schema.tables WHERE table_schema='public' AND table_name=$1`

// Before: SELECT name FROM pragma_table_info('issue_queue') WHERE name = 'config_path'
// After:
`SELECT column_name FROM information_schema.columns WHERE table_name='issue_queue' AND column_name='config_path'`
```

Fix timestamp comparison tests — Postgres stores timestamps with timezone, so direct string comparisons with `2024-01-15 10:00:00` may need `::timestamptz` casts or use `time.Time` types instead.

For tests that insert with explicit timestamps, use:
```go
d.conn.Exec(`INSERT INTO session_events (session_id, issue, stage, event, timestamp) VALUES ($1, $2, $3, $4, $5::timestamptz)`,
    "sess-1", 1, "plan", "started", "2024-01-15T10:00:00Z")
```

The `GetSessionState` scan for timestamp should use `time.Time` instead of `string`, or format the timestamp from the `time.Time` back to string. However, if the `SessionEvent.Timestamp` field remains `string`, pgx will scan `timestamptz` into string format — verify this works or change the struct field to `time.Time`.

**Simplest approach:** Keep `Timestamp` as `string` in the structs. pgx via database/sql scans `timestamptz` into `string` as an ISO 8601 formatted timestamp. This may differ slightly from SQLite's format but is functionally equivalent.

**Step 4: Fix the LogPipelineEvent test signature issue**

Line 910 of db_test.go calls `d.LogPipelineEvent(1, ...)` with 5 args but the function takes 6 (namespace first). Fix: add `""` as the first arg:

```go
d.LogPipelineEvent("", 1, "stage_completed", "plan", 1, fmt.Sprintf("detail-%d", i))
```

**Step 5: Run tests**

Run: `go test ./internal/db/ -v -timeout 120s`
Expected: PASS (takes longer due to container startup; the container is reused within the same test binary run via testcontainers resource reuse)

Note: Tests require Docker to be running locally. If Docker is not available, tests skip.

**Step 6: Run full test suite**

Run: `go test ./... -timeout 300s`
Expected: PASS

**Step 7: Commit**

```bash
git add internal/db/db_test.go go.mod go.sum
git commit -m "test(db): port tests to PostgreSQL using testcontainers"
```

---

### Task 7: Dockerfile

**Files:**
- Create: `Dockerfile`

**Step 1: Write the Dockerfile**

```dockerfile
# Build stage
FROM golang:1.25-bookworm AS builder

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -ldflags "-X main.Version=$(git describe --tags --always --dirty 2>/dev/null || echo docker)" \
    -o /factory ./cmd/factory/

# Runtime stage
FROM debian:bookworm-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
    tmux git curl ca-certificates gnupg docker.io \
    && rm -rf /var/lib/apt/lists/*

# Install gh CLI
RUN curl -fsSL https://cli.github.com/packages/githubcli-archive-keyring.gpg \
    | gpg --dearmor -o /usr/share/keyrings/githubcli-archive-keyring.gpg \
    && echo "deb [arch=$(dpkg --print-architecture) signed-by=/usr/share/keyrings/githubcli-archive-keyring.gpg] https://cli.github.com/packages stable main" \
    > /etc/apt/sources.list.d/github-cli.list \
    && apt-get update && apt-get install -y gh && rm -rf /var/lib/apt/lists/*

# Install Node.js (for claude CLI)
RUN curl -fsSL https://deb.nodesource.com/setup_22.x | bash - \
    && apt-get install -y nodejs && rm -rf /var/lib/apt/lists/*

# Install Claude CLI
RUN npm install -g @anthropic-ai/claude-code

# Create non-root user
RUN useradd -m -s /bin/bash factory
USER factory
WORKDIR /home/factory

# Copy binary
COPY --from=builder /factory /usr/local/bin/factory

# Data directory
ENV FACTORY_DATA_DIR=/data
VOLUME ["/data"]

EXPOSE 17432

ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
```

**Step 2: Verify it builds**

Run: `docker build -t taintfactory:dev .`
Expected: SUCCESS

**Step 3: Commit**

```bash
git add Dockerfile
git commit -m "build: add multi-stage Dockerfile for k8s deployment"
```

---

### Task 8: Entrypoint script

**Files:**
- Create: `deploy/entrypoint.sh`

**Step 1: Write the entrypoint script**

```bash
#!/bin/bash
set -euo pipefail

# Ensure data directories exist
mkdir -p "${FACTORY_DATA_DIR:-/data}/pipelines"
mkdir -p "${FACTORY_DATA_DIR:-/data}/triage"

# Clone repos if not already present (configured via FACTORY_REPOS env var)
# Format: comma-separated list of git URLs
if [ -n "${FACTORY_REPOS:-}" ]; then
  IFS=',' read -ra REPOS <<< "$FACTORY_REPOS"
  for repo in "${REPOS[@]}"; do
    repo_name=$(basename "$repo" .git)
    repo_dir="${FACTORY_DATA_DIR:-/data}/repos/${repo_name}"
    if [ ! -d "$repo_dir/.git" ]; then
      echo "Cloning $repo → $repo_dir"
      git clone "$repo" "$repo_dir"
    else
      echo "Repo $repo_name already cloned, pulling latest"
      git -C "$repo_dir" pull --ff-only || true
    fi
  done
fi

# Start factory with orchestrator
exec factory serve \
  --port "${FACTORY_PORT:-17432}" \
  --with-orchestrator \
  --orchestrator-interval "${ORCHESTRATOR_INTERVAL:-120}"
```

**Step 2: Make executable**

```bash
chmod +x deploy/entrypoint.sh
```

**Step 3: Update Dockerfile to copy entrypoint**

Add before the `ENTRYPOINT` line:
```dockerfile
COPY deploy/entrypoint.sh /usr/local/bin/entrypoint.sh
```

And ensure the `COPY` happens before `USER factory` or use `--chown=factory`:
```dockerfile
COPY --chown=factory deploy/entrypoint.sh /usr/local/bin/entrypoint.sh
RUN chmod +x /usr/local/bin/entrypoint.sh
```

**Step 4: Commit**

```bash
git add deploy/entrypoint.sh Dockerfile
git commit -m "build: add entrypoint script for repo cloning and process startup"
```

---

### Task 9: Kubernetes manifests

**Files:**
- Create: `deploy/k8s/namespace.yaml`
- Create: `deploy/k8s/postgres.yaml`
- Create: `deploy/k8s/factory.yaml`
- Create: `deploy/k8s/ingress.yaml`
- Create: `deploy/k8s/secrets.yaml.example`

**Step 1: Create namespace**

`deploy/k8s/namespace.yaml`:
```yaml
apiVersion: v1
kind: Namespace
metadata:
  name: taintfactory
```

**Step 2: Create Postgres StatefulSet**

`deploy/k8s/postgres.yaml`:
```yaml
apiVersion: v1
kind: Service
metadata:
  name: factory-postgres
  namespace: taintfactory
spec:
  selector:
    app: factory-postgres
  ports:
    - port: 5432
      targetPort: 5432
  clusterIP: None
---
apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: factory-postgres
  namespace: taintfactory
spec:
  serviceName: factory-postgres
  replicas: 1
  selector:
    matchLabels:
      app: factory-postgres
  template:
    metadata:
      labels:
        app: factory-postgres
    spec:
      containers:
        - name: postgres
          image: postgres:16-alpine
          ports:
            - containerPort: 5432
          env:
            - name: POSTGRES_DB
              value: factory
            - name: POSTGRES_USER
              valueFrom:
                secretKeyRef:
                  name: factory-secrets
                  key: POSTGRES_USER
            - name: POSTGRES_PASSWORD
              valueFrom:
                secretKeyRef:
                  name: factory-secrets
                  key: POSTGRES_PASSWORD
          volumeMounts:
            - name: pg-data
              mountPath: /var/lib/postgresql/data
          livenessProbe:
            exec:
              command: ["pg_isready", "-U", "factory"]
            initialDelaySeconds: 10
            periodSeconds: 10
          readinessProbe:
            exec:
              command: ["pg_isready", "-U", "factory"]
            initialDelaySeconds: 5
            periodSeconds: 5
  volumeClaimTemplates:
    - metadata:
        name: pg-data
      spec:
        accessModes: ["ReadWriteOnce"]
        storageClassName: do-block-storage
        resources:
          requests:
            storage: 5Gi
```

**Step 3: Create Factory StatefulSet**

`deploy/k8s/factory.yaml`:
```yaml
apiVersion: v1
kind: Service
metadata:
  name: factory
  namespace: taintfactory
spec:
  selector:
    app: factory
  ports:
    - port: 80
      targetPort: 17432
---
apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: factory
  namespace: taintfactory
spec:
  serviceName: factory
  replicas: 1
  selector:
    matchLabels:
      app: factory
  template:
    metadata:
      labels:
        app: factory
    spec:
      containers:
        - name: factory
          image: registry.digitalocean.com/YOUR_REGISTRY/taintfactory:latest
          ports:
            - containerPort: 17432
          env:
            - name: FACTORY_DATA_DIR
              value: /data
            - name: DATABASE_URL
              valueFrom:
                secretKeyRef:
                  name: factory-secrets
                  key: DATABASE_URL
            - name: CLAUDE_CODE_OAUTH_TOKEN
              valueFrom:
                secretKeyRef:
                  name: factory-secrets
                  key: CLAUDE_CODE_OAUTH_TOKEN
            - name: GITHUB_TOKEN
              valueFrom:
                secretKeyRef:
                  name: factory-secrets
                  key: GITHUB_TOKEN
            - name: DOCKER_HOST
              value: tcp://localhost:2375
            - name: FACTORY_REPOS
              valueFrom:
                configMapKeyRef:
                  name: factory-config
                  key: FACTORY_REPOS
                  optional: true
          volumeMounts:
            - name: factory-data
              mountPath: /data
          livenessProbe:
            httpGet:
              path: /healthz
              port: 17432
            initialDelaySeconds: 10
            periodSeconds: 30
          readinessProbe:
            httpGet:
              path: /healthz
              port: 17432
            initialDelaySeconds: 5
            periodSeconds: 10
        - name: dind
          image: docker:dind
          securityContext:
            privileged: true
          env:
            - name: DOCKER_TLS_CERTDIR
              value: ""
          volumeMounts:
            - name: docker-storage
              mountPath: /var/lib/docker
      volumes:
        - name: docker-storage
          emptyDir: {}
  volumeClaimTemplates:
    - metadata:
        name: factory-data
      spec:
        accessModes: ["ReadWriteOnce"]
        storageClassName: do-block-storage
        resources:
          requests:
            storage: 20Gi
```

**Step 4: Create Ingress**

`deploy/k8s/ingress.yaml`:
```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: factory
  namespace: taintfactory
  annotations:
    nginx.ingress.kubernetes.io/auth-type: basic
    nginx.ingress.kubernetes.io/auth-secret: factory-basic-auth
    nginx.ingress.kubernetes.io/auth-realm: "TaintFactory"
    cert-manager.io/cluster-issuer: letsencrypt-prod  # adjust to your issuer
spec:
  ingressClassName: nginx
  tls:
    - hosts:
        - factory.yourdomain.com
      secretName: factory-tls
  rules:
    - host: factory.yourdomain.com
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: factory
                port:
                  number: 80
```

**Step 5: Create secrets example**

`deploy/k8s/secrets.yaml.example`:
```yaml
# Copy to secrets.yaml, fill in values, apply with:
#   kubectl apply -f deploy/k8s/secrets.yaml
# DO NOT commit secrets.yaml!
apiVersion: v1
kind: Secret
metadata:
  name: factory-secrets
  namespace: taintfactory
type: Opaque
stringData:
  POSTGRES_USER: factory
  POSTGRES_PASSWORD: CHANGE_ME
  DATABASE_URL: postgres://factory:CHANGE_ME@factory-postgres:5432/factory?sslmode=disable
  CLAUDE_CODE_OAUTH_TOKEN: CHANGE_ME
  GITHUB_TOKEN: CHANGE_ME
---
# Basic auth secret for Ingress (generate with htpasswd)
# htpasswd -c auth admin
# kubectl create secret generic factory-basic-auth --from-file=auth -n taintfactory
```

**Step 6: Add .gitignore entry**

```bash
echo "deploy/k8s/secrets.yaml" >> .gitignore
```

**Step 7: Commit**

```bash
git add deploy/k8s/ .gitignore
git commit -m "deploy: add Kubernetes manifests for DigitalOcean cluster"
```

---

### Task 10: CI/CD — GitHub Actions (optional)

**Files:**
- Create: `.github/workflows/deploy.yaml`

**Step 1: Write workflow**

```yaml
name: Build & Push

on:
  push:
    branches: [main]

jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - name: Install doctl
        uses: digitalocean/action-doctl@v2
        with:
          token: ${{ secrets.DIGITALOCEAN_ACCESS_TOKEN }}

      - name: Login to DOCR
        run: doctl registry login

      - name: Build & push
        run: |
          docker build -t registry.digitalocean.com/${{ secrets.DOCR_REGISTRY }}/taintfactory:${{ github.sha }} .
          docker push registry.digitalocean.com/${{ secrets.DOCR_REGISTRY }}/taintfactory:${{ github.sha }}

      - name: Tag latest
        run: |
          docker tag registry.digitalocean.com/${{ secrets.DOCR_REGISTRY }}/taintfactory:${{ github.sha }} \
                     registry.digitalocean.com/${{ secrets.DOCR_REGISTRY }}/taintfactory:latest
          docker push registry.digitalocean.com/${{ secrets.DOCR_REGISTRY }}/taintfactory:latest
```

**Step 2: Commit**

```bash
git add .github/workflows/deploy.yaml
git commit -m "ci: add GitHub Actions workflow for Docker build and DOCR push"
```

---

## Deployment Runbook

After all tasks are complete, deploy with:

```bash
# 1. Create namespace
kubectl apply -f deploy/k8s/namespace.yaml

# 2. Create secrets (from your filled-in secrets.yaml)
kubectl apply -f deploy/k8s/secrets.yaml

# 3. Create basic-auth secret for ingress
htpasswd -c auth admin  # enter password
kubectl create secret generic factory-basic-auth --from-file=auth -n taintfactory

# 4. Deploy Postgres
kubectl apply -f deploy/k8s/postgres.yaml
kubectl -n taintfactory rollout status statefulset/factory-postgres

# 5. Build and push image
docker build -t registry.digitalocean.com/YOUR_REGISTRY/taintfactory:latest .
docker push registry.digitalocean.com/YOUR_REGISTRY/taintfactory:latest

# 6. Deploy Factory
kubectl apply -f deploy/k8s/factory.yaml
kubectl -n taintfactory rollout status statefulset/factory

# 7. Create Ingress
kubectl apply -f deploy/k8s/ingress.yaml

# 8. Verify
kubectl -n taintfactory logs factory-0 -c factory -f
curl -u admin:PASSWORD https://factory.yourdomain.com/healthz
```
