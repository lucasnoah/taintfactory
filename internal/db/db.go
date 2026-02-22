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

const schemaV2 = `
CREATE TABLE IF NOT EXISTS issue_queue (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    issue       INTEGER NOT NULL UNIQUE,
    status      TEXT NOT NULL DEFAULT 'pending'
                CHECK(status IN ('pending','active','completed','failed')),
    position    INTEGER NOT NULL,
    added_at    TEXT NOT NULL DEFAULT (datetime('now')),
    started_at  TEXT,
    finished_at TEXT
);
CREATE INDEX IF NOT EXISTS idx_queue_status_position ON issue_queue(status, position);
`

const schemaV3 = `
ALTER TABLE issue_queue ADD COLUMN feature_intent TEXT NOT NULL DEFAULT '';
`

const schemaV4 = `
ALTER TABLE issue_queue ADD COLUMN depends_on TEXT NOT NULL DEFAULT '[]';
`

// Migrate applies the database schema.
func (d *DB) Migrate() error {
	// Apply v1 if needed
	var v1Count int
	err := d.conn.QueryRow("SELECT COUNT(*) FROM schema_version WHERE version = 1").Scan(&v1Count)
	if err != nil || v1Count == 0 {
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
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit v1: %w", err)
		}
	}

	// Apply v2 if needed
	var v2Count int
	err = d.conn.QueryRow("SELECT COUNT(*) FROM schema_version WHERE version = 2").Scan(&v2Count)
	if err != nil || v2Count == 0 {
		tx, err := d.conn.Begin()
		if err != nil {
			return fmt.Errorf("begin transaction: %w", err)
		}
		defer tx.Rollback()

		if _, err := tx.Exec(schemaV2); err != nil {
			return fmt.Errorf("apply schema v2: %w", err)
		}
		if _, err := tx.Exec("INSERT INTO schema_version (version) VALUES (2)"); err != nil {
			return fmt.Errorf("record schema version v2: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit v2: %w", err)
		}
	}

	// Apply v3 if needed
	var v3Count int
	err = d.conn.QueryRow("SELECT COUNT(*) FROM schema_version WHERE version = 3").Scan(&v3Count)
	if err != nil || v3Count == 0 {
		tx, err := d.conn.Begin()
		if err != nil {
			return fmt.Errorf("begin transaction: %w", err)
		}
		defer tx.Rollback()

		if _, err := tx.Exec(schemaV3); err != nil {
			return fmt.Errorf("apply schema v3: %w", err)
		}
		if _, err := tx.Exec("INSERT INTO schema_version (version) VALUES (3)"); err != nil {
			return fmt.Errorf("record schema version v3: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit v3: %w", err)
		}
	}

	// Apply v4 if needed
	var v4Count int
	err = d.conn.QueryRow("SELECT COUNT(*) FROM schema_version WHERE version = 4").Scan(&v4Count)
	if err != nil || v4Count == 0 {
		tx, err := d.conn.Begin()
		if err != nil {
			return fmt.Errorf("begin transaction: %w", err)
		}
		defer tx.Rollback()

		if _, err := tx.Exec(schemaV4); err != nil {
			return fmt.Errorf("apply schema v4: %w", err)
		}
		if _, err := tx.Exec("INSERT INTO schema_version (version) VALUES (4)"); err != nil {
			return fmt.Errorf("record schema version v4: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit v4: %w", err)
		}
	}

	return nil
}

// Reset drops all tables and re-applies the schema.
func (d *DB) Reset() error {
	tables := []string{"issue_queue", "pipeline_events", "check_runs", "session_events", "schema_version"}
	for _, t := range tables {
		if _, err := d.conn.Exec("DROP TABLE IF EXISTS " + t); err != nil {
			return fmt.Errorf("drop table %s: %w", t, err)
		}
	}
	return d.Migrate()
}
