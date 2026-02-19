package db

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "github.com/mattn/go-sqlite3"
)

// DB wraps the SQLite database connection.
type DB struct {
	conn *sql.DB
	path string
}

// DefaultDBPath returns ~/.factory/factory.db, creating the directory if needed.
func DefaultDBPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("get home directory: %w", err)
	}
	dir := filepath.Join(home, ".factory")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create directory %s: %w", dir, err)
	}
	return filepath.Join(dir, "factory.db"), nil
}

// Open opens or creates the database at the given path.
func Open(path string) (*DB, error) {
	conn, err := sql.Open("sqlite3", path)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	conn.SetMaxOpenConns(1)
	if err := conn.Ping(); err != nil {
		conn.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	if _, err := conn.Exec("PRAGMA journal_mode=WAL"); err != nil {
		conn.Close()
		return nil, fmt.Errorf("set journal mode: %w", err)
	}
	if _, err := conn.Exec("PRAGMA foreign_keys=ON"); err != nil {
		conn.Close()
		return nil, fmt.Errorf("enable foreign keys: %w", err)
	}
	return &DB{conn: conn, path: path}, nil
}

// Close closes the database connection.
func (d *DB) Close() error {
	return d.conn.Close()
}

// Conn returns the underlying *sql.DB for advanced queries.
func (d *DB) Conn() *sql.DB {
	return d.conn
}

const schemaV1 = `
CREATE TABLE IF NOT EXISTS schema_version (
    version    INTEGER PRIMARY KEY,
    applied_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS session_events (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id  TEXT NOT NULL,
    issue       INTEGER NOT NULL,
    stage       TEXT NOT NULL,
    event       TEXT NOT NULL CHECK(event IN ('started','active','idle','exited','factory_send','steer','human_input')),
    exit_code   INTEGER,
    timestamp   TEXT NOT NULL DEFAULT (datetime('now')),
    metadata    TEXT
);
CREATE INDEX IF NOT EXISTS idx_session_latest ON session_events(session_id, timestamp DESC);
CREATE INDEX IF NOT EXISTS idx_session_issue ON session_events(issue, stage);

CREATE TABLE IF NOT EXISTS check_runs (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
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
    timestamp   TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_check_issue_stage ON check_runs(issue, stage, fix_round);

CREATE TABLE IF NOT EXISTS pipeline_events (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    issue       INTEGER NOT NULL,
    event       TEXT NOT NULL,
    stage       TEXT,
    attempt     INTEGER,
    detail      TEXT,
    timestamp   TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_pipeline_issue ON pipeline_events(issue, timestamp DESC);
`

// Migrate applies the database schema.
func (d *DB) Migrate() error {
	var count int
	err := d.conn.QueryRow("SELECT COUNT(*) FROM schema_version WHERE version = 1").Scan(&count)
	if err == nil && count > 0 {
		return nil
	}

	tx, err := d.conn.Begin()
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.Exec(schemaV1); err != nil {
		return fmt.Errorf("apply schema v1: %w", err)
	}
	if _, err := tx.Exec("INSERT INTO schema_version (version) VALUES (1)"); err != nil {
		return fmt.Errorf("record schema version: %w", err)
	}
	return tx.Commit()
}

// Reset drops all tables and re-applies the schema.
func (d *DB) Reset() error {
	tables := []string{"pipeline_events", "check_runs", "session_events", "schema_version"}
	for _, t := range tables {
		if _, err := d.conn.Exec("DROP TABLE IF EXISTS " + t); err != nil {
			return fmt.Errorf("drop table %s: %w", t, err)
		}
	}
	return d.Migrate()
}
