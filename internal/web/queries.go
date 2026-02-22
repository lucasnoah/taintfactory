package web

import (
	"database/sql"
	"fmt"

	"github.com/lucasnoah/taintfactory/internal/db"
)

// recentActivity returns the most recent pipeline events across all issues.
func (s *Server) recentActivity(limit int) ([]db.PipelineEvent, error) {
	rows, err := s.db.Conn().Query(
		`SELECT id, issue, event, stage, attempt, detail, timestamp
		 FROM pipeline_events ORDER BY id DESC LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("recent activity: %w", err)
	}
	defer rows.Close()

	var events []db.PipelineEvent
	for rows.Next() {
		var e db.PipelineEvent
		var stage, detail sql.NullString
		var attempt sql.NullInt64
		if err := rows.Scan(&e.ID, &e.Issue, &e.Event, &stage, &attempt, &detail, &e.Timestamp); err != nil {
			return nil, fmt.Errorf("scan event: %w", err)
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

// checkRunsForAttempt returns all check runs for a specific stage attempt.
func (s *Server) checkRunsForAttempt(issue int, stage string, attempt int) ([]db.CheckRun, error) {
	rows, err := s.db.Conn().Query(
		`SELECT id, issue, stage, attempt, fix_round, check_name, passed, auto_fixed,
		        exit_code, duration_ms, summary, findings, timestamp
		 FROM check_runs
		 WHERE issue = ? AND stage = ? AND attempt = ?
		 ORDER BY fix_round, id`,
		issue, stage, attempt,
	)
	if err != nil {
		return nil, fmt.Errorf("check runs for attempt: %w", err)
	}
	defer rows.Close()

	var runs []db.CheckRun
	for rows.Next() {
		var r db.CheckRun
		var exitCode, durationMs sql.NullInt64
		var summary, findings sql.NullString
		if err := rows.Scan(&r.ID, &r.Issue, &r.Stage, &r.Attempt, &r.FixRound, &r.CheckName,
			&r.Passed, &r.AutoFixed, &exitCode, &durationMs, &summary, &findings, &r.Timestamp); err != nil {
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

// sessionEventLabel converts a session event name to a dot color.
func sessionEventLabel(event string) string {
	switch event {
	case "started", "active":
		return "green"
	case "idle":
		return "yellow"
	case "exited":
		return "grey"
	}
	return ""
}
