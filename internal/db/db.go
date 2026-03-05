package db

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	"github.com/lucasnoah/taintfactory/internal/config"
	_ "github.com/mattn/go-sqlite3"
)

// DB wraps the SQLite database connection.
type DB struct {
	conn *sql.DB
	path string
}

// DefaultDBPath returns the default database path, creating the directory if needed.
// Respects FACTORY_DATA_DIR env var (default: ~/.factory).
func DefaultDBPath() (string, error) {
	dir := config.DataDir()
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
    metadata    TEXT,
    namespace   TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_session_latest ON session_events(session_id, timestamp DESC);
CREATE INDEX IF NOT EXISTS idx_session_issue ON session_events(issue, stage);

CREATE TABLE IF NOT EXISTS check_runs (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
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
    timestamp   TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_check_issue_stage ON check_runs(issue, stage, fix_round);
CREATE INDEX IF NOT EXISTS idx_check_ns_issue ON check_runs(namespace, issue, stage, fix_round);

CREATE TABLE IF NOT EXISTS pipeline_events (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    namespace   TEXT NOT NULL DEFAULT '',
    issue       INTEGER NOT NULL,
    event       TEXT NOT NULL,
    stage       TEXT,
    attempt     INTEGER,
    detail      TEXT,
    timestamp   TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_pipeline_issue ON pipeline_events(issue, timestamp DESC);
CREATE INDEX IF NOT EXISTS idx_pipeline_ns_issue ON pipeline_events(namespace, issue, timestamp DESC);
`

const schemaV2 = `
CREATE TABLE IF NOT EXISTS issue_queue (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    namespace      TEXT NOT NULL DEFAULT '',
    issue          INTEGER NOT NULL,
    status         TEXT NOT NULL DEFAULT 'pending'
                   CHECK(status IN ('pending','active','completed','failed')),
    position       INTEGER NOT NULL,
    added_at       TEXT NOT NULL DEFAULT (datetime('now')),
    started_at     TEXT,
    finished_at    TEXT,
    UNIQUE(namespace, issue)
);
CREATE INDEX IF NOT EXISTS idx_queue_status_position ON issue_queue(status, position);
CREATE INDEX IF NOT EXISTS idx_queue_ns_issue ON issue_queue(namespace, issue);
`

const schemaV3 = `
ALTER TABLE issue_queue ADD COLUMN feature_intent TEXT NOT NULL DEFAULT '';
`

const schemaV4 = `
ALTER TABLE issue_queue ADD COLUMN depends_on TEXT NOT NULL DEFAULT '[]';
`

const schemaV5 = `
ALTER TABLE issue_queue ADD COLUMN config_path TEXT NOT NULL DEFAULT '';
`

const schemaV6 = `
ALTER TABLE pipeline_events ADD COLUMN namespace TEXT NOT NULL DEFAULT '';
ALTER TABLE check_runs ADD COLUMN namespace TEXT NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS idx_pipeline_ns_issue ON pipeline_events(namespace, issue, timestamp DESC);
CREATE INDEX IF NOT EXISTS idx_check_ns_issue ON check_runs(namespace, issue, stage, fix_round);
`

const schemaV7 = `
CREATE TABLE issue_queue_new (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    namespace      TEXT NOT NULL DEFAULT '',
    issue          INTEGER NOT NULL,
    status         TEXT NOT NULL DEFAULT 'pending'
                   CHECK(status IN ('pending','active','completed','failed')),
    position       INTEGER NOT NULL,
    feature_intent TEXT NOT NULL DEFAULT '',
    depends_on     TEXT NOT NULL DEFAULT '[]',
    config_path    TEXT NOT NULL DEFAULT '',
    added_at       TEXT NOT NULL DEFAULT (datetime('now')),
    started_at     TEXT,
    finished_at    TEXT,
    UNIQUE(namespace, issue)
);
INSERT INTO issue_queue_new
    SELECT id, '', issue, status, position, feature_intent, depends_on, config_path,
           added_at, started_at, finished_at
    FROM issue_queue;
DROP TABLE issue_queue;
ALTER TABLE issue_queue_new RENAME TO issue_queue;
CREATE INDEX IF NOT EXISTS idx_queue_status_position ON issue_queue(status, position);
CREATE INDEX IF NOT EXISTS idx_queue_ns_issue ON issue_queue(namespace, issue);
`

const schemaV8 = `
ALTER TABLE session_events ADD COLUMN namespace TEXT NOT NULL DEFAULT '';
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

	// Apply v5 if needed
	var v5Count int
	err = d.conn.QueryRow("SELECT COUNT(*) FROM schema_version WHERE version = 5").Scan(&v5Count)
	if err != nil || v5Count == 0 {
		tx, err := d.conn.Begin()
		if err != nil {
			return fmt.Errorf("begin transaction: %w", err)
		}
		defer tx.Rollback()

		if _, err := tx.Exec(schemaV5); err != nil {
			return fmt.Errorf("apply schema v5: %w", err)
		}
		if _, err := tx.Exec("INSERT INTO schema_version (version) VALUES (5)"); err != nil {
			return fmt.Errorf("record schema version v5: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit v5: %w", err)
		}
	}

	// Apply v6 if needed (namespace columns on pipeline_events and check_runs)
	var v6Count int
	err = d.conn.QueryRow("SELECT COUNT(*) FROM schema_version WHERE version = 6").Scan(&v6Count)
	if err != nil || v6Count == 0 {
		// For fresh DBs created with the new schemaV1, the columns already exist;
		// check before adding to avoid "duplicate column name" errors.
		var hasNsCol int
		_ = d.conn.QueryRow("SELECT COUNT(*) FROM pragma_table_info('pipeline_events') WHERE name='namespace'").Scan(&hasNsCol)
		if hasNsCol == 0 {
			tx, err := d.conn.Begin()
			if err != nil {
				return fmt.Errorf("begin transaction: %w", err)
			}
			defer tx.Rollback()
			for _, stmt := range []string{
				"ALTER TABLE pipeline_events ADD COLUMN namespace TEXT NOT NULL DEFAULT ''",
				"ALTER TABLE check_runs ADD COLUMN namespace TEXT NOT NULL DEFAULT ''",
				"CREATE INDEX IF NOT EXISTS idx_pipeline_ns_issue ON pipeline_events(namespace, issue, timestamp DESC)",
				"CREATE INDEX IF NOT EXISTS idx_check_ns_issue ON check_runs(namespace, issue, stage, fix_round)",
			} {
				if _, err := tx.Exec(stmt); err != nil {
					return fmt.Errorf("apply schema v6 stmt %q: %w", stmt, err)
				}
			}
			if _, err := tx.Exec("INSERT INTO schema_version (version) VALUES (6)"); err != nil {
				return fmt.Errorf("record schema version v6: %w", err)
			}
			if err := tx.Commit(); err != nil {
				return fmt.Errorf("commit v6: %w", err)
			}
		} else {
			// Columns already present (fresh DB); just record the version.
			if _, err := d.conn.Exec("INSERT OR IGNORE INTO schema_version (version) VALUES (6)"); err != nil {
				return fmt.Errorf("record schema version v6 (fresh): %w", err)
			}
		}
	}

	// Apply v7 if needed (rebuild issue_queue with UNIQUE(namespace,issue))
	var v7Count int
	err = d.conn.QueryRow("SELECT COUNT(*) FROM schema_version WHERE version = 7").Scan(&v7Count)
	if err != nil || v7Count == 0 {
		// Check if the new UNIQUE constraint already exists (fresh DB).
		var hasNsCol int
		_ = d.conn.QueryRow("SELECT COUNT(*) FROM pragma_table_info('issue_queue') WHERE name='namespace'").Scan(&hasNsCol)
		if hasNsCol == 0 {
			// Old schema — needs rebuild.
			tx, err := d.conn.Begin()
			if err != nil {
				return fmt.Errorf("begin transaction: %w", err)
			}
			defer tx.Rollback()
			if _, err := tx.Exec(schemaV7); err != nil {
				return fmt.Errorf("apply schema v7: %w", err)
			}
			if _, err := tx.Exec("INSERT INTO schema_version (version) VALUES (7)"); err != nil {
				return fmt.Errorf("record schema version v7: %w", err)
			}
			if err := tx.Commit(); err != nil {
				return fmt.Errorf("commit v7: %w", err)
			}
		} else {
			if _, err := d.conn.Exec("INSERT OR IGNORE INTO schema_version (version) VALUES (7)"); err != nil {
				return fmt.Errorf("record schema version v7 (fresh): %w", err)
			}
		}
	}

	// Apply v8 if needed (namespace column on session_events)
	var v8Count int
	err = d.conn.QueryRow("SELECT COUNT(*) FROM schema_version WHERE version = 8").Scan(&v8Count)
	if err != nil || v8Count == 0 {
		var hasNsCol int
		_ = d.conn.QueryRow("SELECT COUNT(*) FROM pragma_table_info('session_events') WHERE name='namespace'").Scan(&hasNsCol)
		if hasNsCol == 0 {
			tx, err := d.conn.Begin()
			if err != nil {
				return fmt.Errorf("begin transaction: %w", err)
			}
			defer tx.Rollback()
			if _, err := tx.Exec("ALTER TABLE session_events ADD COLUMN namespace TEXT NOT NULL DEFAULT ''"); err != nil {
				return fmt.Errorf("apply schema v8: %w", err)
			}
			if _, err := tx.Exec("INSERT INTO schema_version (version) VALUES (8)"); err != nil {
				return fmt.Errorf("record schema version v8: %w", err)
			}
			if err := tx.Commit(); err != nil {
				return fmt.Errorf("commit v8: %w", err)
			}
		} else {
			if _, err := d.conn.Exec("INSERT OR IGNORE INTO schema_version (version) VALUES (8)"); err != nil {
				return fmt.Errorf("record schema version v8 (fresh): %w", err)
			}
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
