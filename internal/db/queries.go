package db

import (
	"database/sql"
	"fmt"
)

// SessionEvent represents a row in the session_events table.
type SessionEvent struct {
	ID        int
	SessionID string
	Issue     int
	Stage     string
	Event     string
	ExitCode  *int
	Timestamp string
	Metadata  string
}

// CheckRun represents a row in the check_runs table.
type CheckRun struct {
	ID         int
	Issue      int
	Stage      string
	Attempt    int
	FixRound   int
	CheckName  string
	Passed     bool
	AutoFixed  bool
	ExitCode   int
	DurationMs int
	Summary    string
	Findings   string
	Timestamp  string
}

// PipelineEvent represents a row in the pipeline_events table.
type PipelineEvent struct {
	ID        int
	Issue     int
	Event     string
	Stage     string
	Attempt   int
	Detail    string
	Timestamp string
}

// LogSessionEvent inserts a session event.
func (d *DB) LogSessionEvent(sessionID string, issue int, stage string, event string, exitCode *int, metadata string) error {
	_, err := d.conn.Exec(
		`INSERT INTO session_events (session_id, issue, stage, event, exit_code, metadata) VALUES (?, ?, ?, ?, ?, ?)`,
		sessionID, issue, stage, event, exitCode, metadata,
	)
	if err != nil {
		return fmt.Errorf("log session event: %w", err)
	}
	return nil
}

// GetSessionState returns the most recent event for a session.
func (d *DB) GetSessionState(sessionID string) (*SessionEvent, error) {
	row := d.conn.QueryRow(
		`SELECT id, session_id, issue, stage, event, exit_code, timestamp, metadata
		 FROM session_events WHERE session_id = ? ORDER BY timestamp DESC, id DESC LIMIT 1`,
		sessionID,
	)
	var e SessionEvent
	var exitCode sql.NullInt64
	var metadata sql.NullString
	err := row.Scan(&e.ID, &e.SessionID, &e.Issue, &e.Stage, &e.Event, &exitCode, &e.Timestamp, &metadata)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get session state: %w", err)
	}
	if exitCode.Valid {
		v := int(exitCode.Int64)
		e.ExitCode = &v
	}
	if metadata.Valid {
		e.Metadata = metadata.String
	}
	return &e, nil
}

// GetAllActiveSessions returns sessions whose most recent event is 'started' or 'active'.
func (d *DB) GetAllActiveSessions() ([]SessionEvent, error) {
	rows, err := d.conn.Query(`
		SELECT se.id, se.session_id, se.issue, se.stage, se.event, se.exit_code, se.timestamp, se.metadata
		FROM session_events se
		INNER JOIN (
			SELECT session_id, MAX(id) as max_id
			FROM session_events
			GROUP BY session_id
		) latest ON se.id = latest.max_id
		WHERE se.event IN ('started', 'active', 'idle')
	`)
	if err != nil {
		return nil, fmt.Errorf("get active sessions: %w", err)
	}
	defer rows.Close()

	var sessions []SessionEvent
	for rows.Next() {
		var e SessionEvent
		var exitCode sql.NullInt64
		var metadata sql.NullString
		if err := rows.Scan(&e.ID, &e.SessionID, &e.Issue, &e.Stage, &e.Event, &exitCode, &e.Timestamp, &metadata); err != nil {
			return nil, fmt.Errorf("scan session event: %w", err)
		}
		if exitCode.Valid {
			v := int(exitCode.Int64)
			e.ExitCode = &v
		}
		if metadata.Valid {
			e.Metadata = metadata.String
		}
		sessions = append(sessions, e)
	}
	return sessions, rows.Err()
}

// DetectHumanIntervention returns true if the session went active
// without a preceding factory_send event (meaning a human typed something).
func (d *DB) DetectHumanIntervention(sessionID string) (bool, error) {
	var activeTimestamp string
	err := d.conn.QueryRow(
		`SELECT timestamp FROM session_events
		 WHERE session_id = ? AND event = 'active'
		 ORDER BY timestamp DESC, id DESC LIMIT 1`,
		sessionID,
	).Scan(&activeTimestamp)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("find active event: %w", err)
	}

	var count int
	err = d.conn.QueryRow(
		`SELECT COUNT(*) FROM session_events
		 WHERE session_id = ? AND event = 'factory_send'
		 AND timestamp BETWEEN datetime(?, '-5 seconds') AND ?`,
		sessionID, activeTimestamp, activeTimestamp,
	).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("check factory_send: %w", err)
	}

	return count == 0, nil
}

// LogCheckRun inserts a check run record.
func (d *DB) LogCheckRun(issue int, stage string, attempt int, fixRound int, checkName string, passed bool, autoFixed bool, exitCode int, durationMs int, summary string, findings string) error {
	_, err := d.conn.Exec(
		`INSERT INTO check_runs (issue, stage, attempt, fix_round, check_name, passed, auto_fixed, exit_code, duration_ms, summary, findings)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		issue, stage, attempt, fixRound, checkName, passed, autoFixed, exitCode, durationMs, summary, findings,
	)
	if err != nil {
		return fmt.Errorf("log check run: %w", err)
	}
	return nil
}

// GetCheckRuns returns check runs for an issue, stage, and fix round.
func (d *DB) GetCheckRuns(issue int, stage string, fixRound int) ([]CheckRun, error) {
	rows, err := d.conn.Query(
		`SELECT id, issue, stage, attempt, fix_round, check_name, passed, auto_fixed, exit_code, duration_ms, summary, findings, timestamp
		 FROM check_runs WHERE issue = ? AND stage = ? AND fix_round = ? ORDER BY id`,
		issue, stage, fixRound,
	)
	if err != nil {
		return nil, fmt.Errorf("get check runs: %w", err)
	}
	defer rows.Close()

	var runs []CheckRun
	for rows.Next() {
		var r CheckRun
		var exitCode, durationMs sql.NullInt64
		var summary, findings sql.NullString
		if err := rows.Scan(&r.ID, &r.Issue, &r.Stage, &r.Attempt, &r.FixRound, &r.CheckName, &r.Passed, &r.AutoFixed, &exitCode, &durationMs, &summary, &findings, &r.Timestamp); err != nil {
			return nil, fmt.Errorf("scan check run: %w", err)
		}
		if exitCode.Valid {
			r.ExitCode = int(exitCode.Int64)
		}
		if durationMs.Valid {
			r.DurationMs = int(durationMs.Int64)
		}
		if summary.Valid {
			r.Summary = summary.String
		}
		if findings.Valid {
			r.Findings = findings.String
		}
		runs = append(runs, r)
	}
	return runs, rows.Err()
}

// GetLatestCheckRun returns the most recent check run for an issue and check name.
func (d *DB) GetLatestCheckRun(issue int, checkName string) (*CheckRun, error) {
	row := d.conn.QueryRow(
		`SELECT id, issue, stage, attempt, fix_round, check_name, passed, auto_fixed, exit_code, duration_ms, summary, findings, timestamp
		 FROM check_runs WHERE issue = ? AND check_name = ? ORDER BY id DESC LIMIT 1`,
		issue, checkName,
	)
	var r CheckRun
	var exitCode, durationMs sql.NullInt64
	var summary, findings sql.NullString
	err := row.Scan(&r.ID, &r.Issue, &r.Stage, &r.Attempt, &r.FixRound, &r.CheckName, &r.Passed, &r.AutoFixed, &exitCode, &durationMs, &summary, &findings, &r.Timestamp)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get latest check run: %w", err)
	}
	if exitCode.Valid {
		r.ExitCode = int(exitCode.Int64)
	}
	if durationMs.Valid {
		r.DurationMs = int(durationMs.Int64)
	}
	if summary.Valid {
		r.Summary = summary.String
	}
	if findings.Valid {
		r.Findings = findings.String
	}
	return &r, nil
}

// LogPipelineEvent inserts a pipeline event.
func (d *DB) LogPipelineEvent(issue int, event string, stage string, attempt int, detail string) error {
	_, err := d.conn.Exec(
		`INSERT INTO pipeline_events (issue, event, stage, attempt, detail) VALUES (?, ?, ?, ?, ?)`,
		issue, event, stage, attempt, detail,
	)
	if err != nil {
		return fmt.Errorf("log pipeline event: %w", err)
	}
	return nil
}

// GetPipelineHistory returns all pipeline events for an issue, ordered by timestamp descending.
func (d *DB) GetPipelineHistory(issue int) ([]PipelineEvent, error) {
	rows, err := d.conn.Query(
		`SELECT id, issue, event, stage, attempt, detail, timestamp
		 FROM pipeline_events WHERE issue = ? ORDER BY timestamp DESC, id DESC`,
		issue,
	)
	if err != nil {
		return nil, fmt.Errorf("get pipeline history: %w", err)
	}
	defer rows.Close()

	var events []PipelineEvent
	for rows.Next() {
		var e PipelineEvent
		var stage, detail sql.NullString
		var attempt sql.NullInt64
		if err := rows.Scan(&e.ID, &e.Issue, &e.Event, &stage, &attempt, &detail, &e.Timestamp); err != nil {
			return nil, fmt.Errorf("scan pipeline event: %w", err)
		}
		if stage.Valid {
			e.Stage = stage.String
		}
		if attempt.Valid {
			e.Attempt = int(attempt.Int64)
		}
		if detail.Valid {
			e.Detail = detail.String
		}
		events = append(events, e)
	}
	return events, rows.Err()
}

// GetLatestFailedChecks returns the most recent failed check runs for an issue and stage.
func (d *DB) GetLatestFailedChecks(issue int, stage string) ([]CheckRun, error) {
	rows, err := d.conn.Query(`
		SELECT cr.id, cr.issue, cr.stage, cr.attempt, cr.fix_round, cr.check_name,
		       cr.passed, cr.auto_fixed, cr.exit_code, cr.duration_ms, cr.summary, cr.findings, cr.timestamp
		FROM check_runs cr
		INNER JOIN (
			SELECT check_name, MAX(id) as max_id
			FROM check_runs
			WHERE issue = ? AND stage = ?
			GROUP BY check_name
		) latest ON cr.id = latest.max_id
		WHERE cr.passed = 0
		ORDER BY cr.check_name`,
		issue, stage,
	)
	if err != nil {
		return nil, fmt.Errorf("get latest failed checks: %w", err)
	}
	defer rows.Close()

	var runs []CheckRun
	for rows.Next() {
		var r CheckRun
		var exitCode, durationMs sql.NullInt64
		var summary, findings sql.NullString
		if err := rows.Scan(&r.ID, &r.Issue, &r.Stage, &r.Attempt, &r.FixRound, &r.CheckName, &r.Passed, &r.AutoFixed, &exitCode, &durationMs, &summary, &findings, &r.Timestamp); err != nil {
			return nil, fmt.Errorf("scan failed check: %w", err)
		}
		if exitCode.Valid {
			r.ExitCode = int(exitCode.Int64)
		}
		if durationMs.Valid {
			r.DurationMs = int(durationMs.Int64)
		}
		if summary.Valid {
			r.Summary = summary.String
		}
		if findings.Valid {
			r.Findings = findings.String
		}
		runs = append(runs, r)
	}
	return runs, rows.Err()
}

// GetCheckHistory returns all check runs for an issue, ordered by id descending.
func (d *DB) GetCheckHistory(issue int) ([]CheckRun, error) {
	rows, err := d.conn.Query(
		`SELECT id, issue, stage, attempt, fix_round, check_name, passed, auto_fixed, exit_code, duration_ms, summary, findings, timestamp
		 FROM check_runs WHERE issue = ? ORDER BY id DESC`,
		issue,
	)
	if err != nil {
		return nil, fmt.Errorf("get check history: %w", err)
	}
	defer rows.Close()

	var runs []CheckRun
	for rows.Next() {
		var r CheckRun
		var exitCode, durationMs sql.NullInt64
		var summary, findings sql.NullString
		if err := rows.Scan(&r.ID, &r.Issue, &r.Stage, &r.Attempt, &r.FixRound, &r.CheckName, &r.Passed, &r.AutoFixed, &exitCode, &durationMs, &summary, &findings, &r.Timestamp); err != nil {
			return nil, fmt.Errorf("scan check history: %w", err)
		}
		if exitCode.Valid {
			r.ExitCode = int(exitCode.Int64)
		}
		if durationMs.Valid {
			r.DurationMs = int(durationMs.Int64)
		}
		if summary.Valid {
			r.Summary = summary.String
		}
		if findings.Valid {
			r.Findings = findings.String
		}
		runs = append(runs, r)
	}
	return runs, rows.Err()
}
