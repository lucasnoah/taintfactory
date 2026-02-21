package db

import (
	"database/sql"
	"fmt"
	"strings"
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

// GetSessionStartedAt returns the timestamp of the first "started" event for a session.
func (d *DB) GetSessionStartedAt(sessionID string) (string, error) {
	var timestamp string
	err := d.conn.QueryRow(
		`SELECT timestamp FROM session_events
		 WHERE session_id = ? AND event = 'started'
		 ORDER BY id ASC LIMIT 1`,
		sessionID,
	).Scan(&timestamp)
	if err == sql.ErrNoRows {
		return "", fmt.Errorf("no started event for session %q", sessionID)
	}
	if err != nil {
		return "", fmt.Errorf("get session started_at: %w", err)
	}
	return timestamp, nil
}

// HasRecentSteer returns true if a steer event was logged for the session within the given duration.
func (d *DB) HasRecentSteer(sessionID string, within string) (bool, error) {
	var count int
	err := d.conn.QueryRow(
		`SELECT COUNT(*) FROM session_events
		 WHERE session_id = ? AND event = 'steer'
		 AND timestamp >= datetime('now', ?)`,
		sessionID, within,
	).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("check recent steer: %w", err)
	}
	return count > 0, nil
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

// QueueItem represents a row in the issue_queue table.
type QueueItem struct {
	ID            int
	Issue         int
	Status        string
	Position      int
	FeatureIntent string
	AddedAt       string
	StartedAt     string
	FinishedAt    string
}

// QueueAddItem holds an issue number and its feature intent for queue insertion.
type QueueAddItem struct {
	Issue         int
	FeatureIntent string
}

// QueueAdd inserts issues into the queue with sequential positions.
func (d *DB) QueueAdd(items []QueueAddItem) error {
	tx, err := d.conn.Begin()
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	var maxPos sql.NullInt64
	if err := tx.QueryRow("SELECT MAX(position) FROM issue_queue").Scan(&maxPos); err != nil {
		return fmt.Errorf("get max position: %w", err)
	}
	nextPos := 1
	if maxPos.Valid {
		nextPos = int(maxPos.Int64) + 1
	}

	stmt, err := tx.Prepare("INSERT INTO issue_queue (issue, position, feature_intent) VALUES (?, ?, ?)")
	if err != nil {
		return fmt.Errorf("prepare insert: %w", err)
	}
	defer stmt.Close()

	for _, item := range items {
		if _, err := stmt.Exec(item.Issue, nextPos, item.FeatureIntent); err != nil {
			if strings.Contains(err.Error(), "UNIQUE") {
				return fmt.Errorf("issue %d is already in the queue", item.Issue)
			}
			return fmt.Errorf("insert issue %d: %w", item.Issue, err)
		}
		nextPos++
	}

	return tx.Commit()
}

// QueueSetIntent updates the feature intent for an existing queue item.
func (d *DB) QueueSetIntent(issue int, intent string) error {
	res, err := d.conn.Exec("UPDATE issue_queue SET feature_intent = ? WHERE issue = ?", intent, issue)
	if err != nil {
		return fmt.Errorf("set intent: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("check rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("issue %d not found in queue", issue)
	}
	return nil
}

// QueueList returns all queue items ordered by position.
func (d *DB) QueueList() ([]QueueItem, error) {
	rows, err := d.conn.Query(
		`SELECT id, issue, status, position, feature_intent, added_at, started_at, finished_at
		 FROM issue_queue ORDER BY position`)
	if err != nil {
		return nil, fmt.Errorf("list queue: %w", err)
	}
	defer rows.Close()

	var items []QueueItem
	for rows.Next() {
		var item QueueItem
		var startedAt, finishedAt sql.NullString
		if err := rows.Scan(&item.ID, &item.Issue, &item.Status, &item.Position, &item.FeatureIntent, &item.AddedAt, &startedAt, &finishedAt); err != nil {
			return nil, fmt.Errorf("scan queue item: %w", err)
		}
		if startedAt.Valid {
			item.StartedAt = startedAt.String
		}
		if finishedAt.Valid {
			item.FinishedAt = finishedAt.String
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// QueueNext returns the next pending item (lowest position), or nil if none.
func (d *DB) QueueNext() (*QueueItem, error) {
	row := d.conn.QueryRow(
		`SELECT id, issue, status, position, feature_intent, added_at, started_at, finished_at
		 FROM issue_queue WHERE status = 'pending' ORDER BY position ASC LIMIT 1`)

	var item QueueItem
	var startedAt, finishedAt sql.NullString
	err := row.Scan(&item.ID, &item.Issue, &item.Status, &item.Position, &item.FeatureIntent, &item.AddedAt, &startedAt, &finishedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get next queue item: %w", err)
	}
	if startedAt.Valid {
		item.StartedAt = startedAt.String
	}
	if finishedAt.Valid {
		item.FinishedAt = finishedAt.String
	}
	return &item, nil
}

// QueueUpdateStatus updates the status of a queue item by issue number.
// Sets started_at when transitioning to "active", finished_at for "completed"/"failed".
func (d *DB) QueueUpdateStatus(issue int, status string) error {
	var res sql.Result
	var err error

	switch status {
	case "active":
		res, err = d.conn.Exec(
			`UPDATE issue_queue SET status = ?, started_at = datetime('now') WHERE issue = ?`,
			status, issue)
	case "completed", "failed":
		res, err = d.conn.Exec(
			`UPDATE issue_queue SET status = ?, finished_at = datetime('now') WHERE issue = ?`,
			status, issue)
	default:
		res, err = d.conn.Exec(
			`UPDATE issue_queue SET status = ? WHERE issue = ?`,
			status, issue)
	}

	if err != nil {
		return fmt.Errorf("update queue status: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("check rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("issue %d not found in queue", issue)
	}
	return nil
}

// QueueRemove deletes a queue item by issue number.
func (d *DB) QueueRemove(issue int) error {
	res, err := d.conn.Exec("DELETE FROM issue_queue WHERE issue = ?", issue)
	if err != nil {
		return fmt.Errorf("remove from queue: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("check rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("issue %d not found in queue", issue)
	}
	return nil
}

// QueueClear deletes all items from the queue, returning the count deleted.
func (d *DB) QueueClear() (int, error) {
	res, err := d.conn.Exec("DELETE FROM issue_queue")
	if err != nil {
		return 0, fmt.Errorf("clear queue: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("check rows affected: %w", err)
	}
	return int(n), nil
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
