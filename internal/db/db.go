package db

import (
	"database/sql"
	"fmt"
	"os"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// DB wraps the PostgreSQL database connection.
type DB struct {
	conn *sql.DB
}

// DefaultConnStr returns the DATABASE_URL env var for connecting to PostgreSQL.
func DefaultConnStr() (string, error) {
	url := os.Getenv("DATABASE_URL")
	if url != "" {
		return url, nil
	}
	return "", fmt.Errorf("DATABASE_URL environment variable not set")
}

// Open opens a database connection using the given connection string.
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

// Close closes the database connection.
func (d *DB) Close() error {
	return d.conn.Close()
}

// Conn returns the underlying *sql.DB for advanced queries.
func (d *DB) Conn() *sql.DB {
	return d.conn
}

const schema = `
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
`

// Migrate applies the database schema.
func (d *DB) Migrate() error {
	if _, err := d.conn.Exec(schema); err != nil {
		return fmt.Errorf("apply schema: %w", err)
	}
	if _, err := d.conn.Exec("INSERT INTO schema_version (version) VALUES (1) ON CONFLICT DO NOTHING"); err != nil {
		return fmt.Errorf("record schema version: %w", err)
	}
	return nil
}

// Reset drops all tables and re-applies the schema.
func (d *DB) Reset() error {
	tables := []string{"issue_queue", "pipeline_events", "check_runs", "session_events", "schema_version"}
	for _, t := range tables {
		if _, err := d.conn.Exec("DROP TABLE IF EXISTS " + t + " CASCADE"); err != nil {
			return fmt.Errorf("drop table %s: %w", t, err)
		}
	}
	return d.Migrate()
}
