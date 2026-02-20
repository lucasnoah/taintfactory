package analytics

import (
	"database/sql"
	"fmt"
	"math"
	"sort"
	"time"
)

// DB is the interface for database queries used by analytics.
type DB interface {
	Conn() *sql.DB
}

// StageDuration holds duration stats for a stage.
type StageDuration struct {
	Stage string  `json:"stage"`
	Count int     `json:"count"`
	Avg   float64 `json:"avg_minutes"`
	P50   float64 `json:"p50_minutes"`
	P95   float64 `json:"p95_minutes"`
}

// timestamp formats to try when parsing timestamps from the database
var timestampFormats = []string{
	"2006-01-02 15:04:05",
	"2006-01-02T15:04:05Z",
	"2006-01-02T15:04:05",
	"2006-01-02 15:04:05.000",
}

func parseTimestamp(s string) (time.Time, error) {
	for _, f := range timestampFormats {
		if t, err := time.Parse(f, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unrecognized timestamp format: %q", s)
}

// QueryStageDurations returns average and percentile durations per stage.
// Each stage_advanced/completed event is paired with the most recent prior
// created/stage_advanced event for the same issue. Duration > 0 is attributed
// to the end event's stage.
func QueryStageDurations(database DB, since string) ([]StageDuration, error) {
	query := `
		SELECT pe1.issue, pe1.stage, pe1.timestamp as end_ts,
			(SELECT MAX(pe2.timestamp) FROM pipeline_events pe2
			 WHERE pe2.issue = pe1.issue
			 AND pe2.event IN ('created', 'stage_advanced')
			 AND pe2.id < pe1.id) as start_ts
		FROM pipeline_events pe1
		WHERE pe1.event IN ('stage_advanced', 'completed')
		AND pe1.stage != ''`

	args := []interface{}{}
	if since != "" {
		query += ` AND pe1.timestamp >= ?`
		args = append(args, since)
	}

	rows, err := database.Conn().Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("query stage durations: %w", err)
	}
	defer rows.Close()

	stageDurations := make(map[string][]float64)
	for rows.Next() {
		var issue int
		var stage string
		var endTS string
		var startTS sql.NullString
		if err := rows.Scan(&issue, &stage, &endTS, &startTS); err != nil {
			return nil, fmt.Errorf("scan stage duration: %w", err)
		}
		if !startTS.Valid {
			continue
		}
		start, err := parseTimestamp(startTS.String)
		if err != nil {
			continue
		}
		end, err := parseTimestamp(endTS)
		if err != nil {
			continue
		}
		minutes := end.Sub(start).Minutes()
		if minutes > 0 {
			stageDurations[stage] = append(stageDurations[stage], minutes)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var results []StageDuration
	for stage, durations := range stageDurations {
		sort.Float64s(durations)
		results = append(results, StageDuration{
			Stage: stage,
			Count: len(durations),
			Avg:   avg(durations),
			P50:   percentile(durations, 50),
			P95:   percentile(durations, 95),
		})
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].Stage < results[j].Stage
	})
	return results, nil
}

// CheckFailureRate holds check failure stats per stage.
type CheckFailureRate struct {
	Stage     string  `json:"stage"`
	Total     int     `json:"total"`
	FirstPass float64 `json:"first_pass_pct"`
	AfterFix  float64 `json:"after_fix_pct"`
	Escalated float64 `json:"escalated_pct"`
}

// QueryCheckFailureRates returns check failure rates by stage.
// All percentages use pipeline_events total as denominator for consistency.
func QueryCheckFailureRates(database DB, since string) ([]CheckFailureRate, error) {
	// Terminal event counts per stage from pipeline_events
	query := `
		SELECT stage,
			COUNT(*) as total,
			SUM(CASE WHEN event IN ('stage_advanced', 'completed') THEN 1 ELSE 0 END) as successes,
			SUM(CASE WHEN event = 'escalated' THEN 1 ELSE 0 END) as escalated
		FROM pipeline_events
		WHERE event IN ('stage_advanced', 'completed', 'fix_loop_exhausted', 'max_attempts_reached', 'escalated')
		AND stage != ''`

	args := []interface{}{}
	if since != "" {
		query += ` AND timestamp >= ?`
		args = append(args, since)
	}
	query += ` GROUP BY stage`

	rows, err := database.Conn().Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("query check failure rates: %w", err)
	}
	defer rows.Close()

	type stageInfo struct {
		total, successes, escalated int
	}
	stageData := make(map[string]*stageInfo)
	for rows.Next() {
		var stage string
		var total, successes, escalated int
		if err := rows.Scan(&stage, &total, &successes, &escalated); err != nil {
			return nil, fmt.Errorf("scan check failure rate: %w", err)
		}
		stageData[stage] = &stageInfo{total: total, successes: successes, escalated: escalated}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// First-pass success counts from check_runs
	fpQuery := `
		SELECT stage,
			SUM(CASE WHEN all_passed = 1 THEN 1 ELSE 0 END) as first_pass
		FROM (
			SELECT issue, stage, attempt,
				MIN(CAST(passed AS INTEGER)) as all_passed
			FROM check_runs
			WHERE fix_round = 0`

	fpArgs := []interface{}{}
	if since != "" {
		fpQuery += ` AND timestamp >= ?`
		fpArgs = append(fpArgs, since)
	}
	fpQuery += `
			GROUP BY issue, stage, attempt
		) sub
		GROUP BY stage`

	fpRows, err := database.Conn().Query(fpQuery, fpArgs...)
	if err != nil {
		return nil, fmt.Errorf("query first-pass rates: %w", err)
	}
	defer fpRows.Close()

	firstPassCounts := make(map[string]int)
	for fpRows.Next() {
		var stage string
		var firstPass int
		if err := fpRows.Scan(&stage, &firstPass); err != nil {
			return nil, fmt.Errorf("scan first-pass rate: %w", err)
		}
		firstPassCounts[stage] = firstPass
	}
	if err := fpRows.Err(); err != nil {
		return nil, err
	}

	// Compute rates using pipeline_events total as denominator
	var results []CheckFailureRate
	for stage, info := range stageData {
		fp := firstPassCounts[stage]
		if fp > info.successes {
			fp = info.successes // cap first-pass at successes
		}
		afterFix := info.successes - fp
		results = append(results, CheckFailureRate{
			Stage:     stage,
			Total:     info.total,
			FirstPass: pct(fp, info.total),
			AfterFix:  pct(afterFix, info.total),
			Escalated: pct(info.escalated, info.total),
		})
	}
	sort.Slice(results, func(i, j int) bool {
		return results[i].Stage < results[j].Stage
	})
	return results, nil
}

// CheckFailure holds failure stats for a specific check.
type CheckFailure struct {
	Check       string  `json:"check"`
	Total       int     `json:"total"`
	FailRate    float64 `json:"fail_rate_pct"`
	AutoFixRate float64 `json:"auto_fix_rate_pct"`
	CommonRules string  `json:"common_rules"`
}

// QueryCheckFailures returns which checks fail most and their auto-fix rates.
func QueryCheckFailures(database DB, since string) ([]CheckFailure, error) {
	query := `
		SELECT check_name,
			COUNT(*) as total,
			SUM(CASE WHEN passed = 0 THEN 1 ELSE 0 END) as failed,
			SUM(CASE WHEN auto_fixed = 1 AND passed = 0 THEN 1 ELSE 0 END) as auto_fixed
		FROM check_runs
		WHERE fix_round = 0`

	args := []interface{}{}
	if since != "" {
		query += ` AND timestamp >= ?`
		args = append(args, since)
	}
	query += ` GROUP BY check_name ORDER BY failed DESC`

	rows, err := database.Conn().Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("query check failures: %w", err)
	}
	defer rows.Close()

	var results []CheckFailure
	for rows.Next() {
		var checkName string
		var total, failed, autoFixed int
		if err := rows.Scan(&checkName, &total, &failed, &autoFixed); err != nil {
			return nil, fmt.Errorf("scan check failure: %w", err)
		}
		results = append(results, CheckFailure{
			Check:       checkName,
			Total:       total,
			FailRate:    pct(failed, total),
			AutoFixRate: pct(autoFixed, max(failed, 1)),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Get common failure summaries per check
	for i := range results {
		summaryQuery := `
			SELECT summary, COUNT(*) as cnt
			FROM check_runs
			WHERE check_name = ? AND passed = 0 AND summary != ''`
		sArgs := []interface{}{results[i].Check}
		if since != "" {
			summaryQuery += ` AND timestamp >= ?`
			sArgs = append(sArgs, since)
		}
		summaryQuery += ` GROUP BY summary ORDER BY cnt DESC LIMIT 3`

		sRows, err := database.Conn().Query(summaryQuery, sArgs...)
		if err != nil {
			continue
		}
		var rules []string
		for sRows.Next() {
			var summary string
			var cnt int
			if err := sRows.Scan(&summary, &cnt); err != nil {
				break
			}
			if summary != "" {
				rules = append(rules, summary)
			}
		}
		_ = sRows.Err()
		sRows.Close()
		if len(rules) > 0 {
			results[i].CommonRules = rules[0]
			if len(rules) > 1 {
				results[i].CommonRules += ", " + rules[1]
			}
		}
	}

	return results, nil
}

// FixRoundDist holds fix round distribution for a stage.
type FixRoundDist struct {
	Stage     string  `json:"stage"`
	Total     int     `json:"total"`
	Zero      float64 `json:"zero_rounds_pct"`
	One       float64 `json:"one_round_pct"`
	Two       float64 `json:"two_rounds_pct"`
	ThreePlus float64 `json:"three_plus_pct"`
}

// QueryFixRounds returns distribution of fix rounds per stage.
func QueryFixRounds(database DB, since string) ([]FixRoundDist, error) {
	query := `
		SELECT stage, detail
		FROM pipeline_events
		WHERE event IN ('stage_advanced', 'completed', 'fix_loop_exhausted')
		AND stage != ''`

	args := []interface{}{}
	if since != "" {
		query += ` AND timestamp >= ?`
		args = append(args, since)
	}

	rows, err := database.Conn().Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("query fix rounds: %w", err)
	}
	defer rows.Close()

	type roundCount struct {
		zero, one, two, threePlus, total int
	}
	stageRounds := make(map[string]*roundCount)

	for rows.Next() {
		var stage string
		var detail sql.NullString
		if err := rows.Scan(&stage, &detail); err != nil {
			return nil, fmt.Errorf("scan fix round: %w", err)
		}

		if _, ok := stageRounds[stage]; !ok {
			stageRounds[stage] = &roundCount{}
		}
		rc := stageRounds[stage]
		rc.total++

		rounds := 0
		if detail.Valid {
			fmt.Sscanf(detail.String, "rounds=%d", &rounds)
		}

		switch {
		case rounds == 0:
			rc.zero++
		case rounds == 1:
			rc.one++
		case rounds == 2:
			rc.two++
		default:
			rc.threePlus++
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var results []FixRoundDist
	for stage, rc := range stageRounds {
		results = append(results, FixRoundDist{
			Stage:     stage,
			Total:     rc.total,
			Zero:      pct(rc.zero, rc.total),
			One:       pct(rc.one, rc.total),
			Two:       pct(rc.two, rc.total),
			ThreePlus: pct(rc.threePlus, rc.total),
		})
	}
	sort.Slice(results, func(i, j int) bool {
		return results[i].Stage < results[j].Stage
	})
	return results, nil
}

// PipelineThroughput holds pipeline throughput for a time period.
type PipelineThroughput struct {
	Period      string  `json:"period"`
	Created     int     `json:"created"`
	Completed   int     `json:"completed"`
	Failed      int     `json:"failed"`
	Escalated   int     `json:"escalated"`
	AvgDuration float64 `json:"avg_duration_hours"`
}

// QueryPipelineThroughput returns pipeline metrics grouped by week.
func QueryPipelineThroughput(database DB, since string) ([]PipelineThroughput, error) {
	query := `
		SELECT
			strftime('%Y-W%W', timestamp) as period,
			SUM(CASE WHEN event = 'created' THEN 1 ELSE 0 END) as created,
			SUM(CASE WHEN event = 'completed' THEN 1 ELSE 0 END) as completed,
			SUM(CASE WHEN event IN ('failed', 'max_attempts_reached') THEN 1 ELSE 0 END) as failed,
			SUM(CASE WHEN event = 'escalated' THEN 1 ELSE 0 END) as escalated
		FROM pipeline_events
		WHERE event IN ('created', 'completed', 'failed', 'max_attempts_reached', 'escalated')`

	args := []interface{}{}
	if since != "" {
		query += ` AND timestamp >= ?`
		args = append(args, since)
	}
	query += ` GROUP BY period ORDER BY period DESC LIMIT 10`

	rows, err := database.Conn().Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("query pipeline throughput: %w", err)
	}
	defer rows.Close()

	var results []PipelineThroughput
	for rows.Next() {
		var pt PipelineThroughput
		if err := rows.Scan(&pt.Period, &pt.Created, &pt.Completed, &pt.Failed, &pt.Escalated); err != nil {
			return nil, fmt.Errorf("scan throughput: %w", err)
		}
		results = append(results, pt)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Compute avg duration: pair each created with the nearest subsequent completed
	for i := range results {
		durQuery := `
			SELECT AVG(
				(julianday(
					(SELECT MIN(pe2.timestamp) FROM pipeline_events pe2
					 WHERE pe2.issue = pe1.issue AND pe2.event = 'completed'
					 AND pe2.timestamp > pe1.timestamp)
				) - julianday(pe1.timestamp)) * 24
			) as avg_hours
			FROM pipeline_events pe1
			WHERE pe1.event = 'created'
			AND strftime('%Y-W%W',
				(SELECT MIN(pe2.timestamp) FROM pipeline_events pe2
				 WHERE pe2.issue = pe1.issue AND pe2.event = 'completed'
				 AND pe2.timestamp > pe1.timestamp)
			) = ?`
		dArgs := []interface{}{results[i].Period}

		var avgHours sql.NullFloat64
		if err := database.Conn().QueryRow(durQuery, dArgs...).Scan(&avgHours); err == nil && avgHours.Valid {
			results[i].AvgDuration = math.Round(avgHours.Float64*10) / 10
		}
	}

	return results, nil
}

// IssueEvent holds a single event for issue-detail view.
type IssueEvent struct {
	Timestamp string `json:"timestamp"`
	Type      string `json:"type"`
	Event     string `json:"event"`
	Stage     string `json:"stage,omitempty"`
	Attempt   int    `json:"attempt,omitempty"`
	Detail    string `json:"detail,omitempty"`
}

// QueryIssueDetail returns the full timeline for a specific issue.
func QueryIssueDetail(database DB, issue int) ([]IssueEvent, error) {
	var results []IssueEvent

	// Pipeline events
	peRows, err := database.Conn().Query(
		`SELECT timestamp, event, stage, attempt, detail
		 FROM pipeline_events WHERE issue = ? ORDER BY timestamp, id`,
		issue,
	)
	if err != nil {
		return nil, fmt.Errorf("query pipeline events: %w", err)
	}
	defer peRows.Close()

	for peRows.Next() {
		var e IssueEvent
		var stage, detail sql.NullString
		var attempt sql.NullInt64
		if err := peRows.Scan(&e.Timestamp, &e.Event, &stage, &attempt, &detail); err != nil {
			return nil, fmt.Errorf("scan pipeline event: %w", err)
		}
		e.Type = "pipeline"
		if stage.Valid {
			e.Stage = stage.String
		}
		if attempt.Valid {
			e.Attempt = int(attempt.Int64)
		}
		if detail.Valid {
			e.Detail = detail.String
		}
		results = append(results, e)
	}
	if err := peRows.Err(); err != nil {
		return nil, err
	}

	// Check runs
	crRows, err := database.Conn().Query(
		`SELECT timestamp, check_name, stage, attempt, fix_round, passed, auto_fixed, duration_ms, summary
		 FROM check_runs WHERE issue = ? ORDER BY timestamp, id`,
		issue,
	)
	if err != nil {
		return nil, fmt.Errorf("query check runs: %w", err)
	}
	defer crRows.Close()

	for crRows.Next() {
		var ts, checkName, stage string
		var attempt, fixRound, durationMs int
		var passed, autoFixed bool
		var summary sql.NullString
		if err := crRows.Scan(&ts, &checkName, &stage, &attempt, &fixRound, &passed, &autoFixed, &durationMs, &summary); err != nil {
			return nil, fmt.Errorf("scan check run: %w", err)
		}

		status := "PASS"
		if !passed {
			status = "FAIL"
		}
		if autoFixed {
			status += " (auto-fixed)"
		}

		detail := fmt.Sprintf("%s: %s (round %d, %dms)", checkName, status, fixRound, durationMs)
		if summary.Valid && summary.String != "" {
			detail += " — " + summary.String
		}

		results = append(results, IssueEvent{
			Timestamp: ts,
			Type:      "check",
			Event:     checkName,
			Stage:     stage,
			Attempt:   attempt,
			Detail:    detail,
		})
	}
	if err := crRows.Err(); err != nil {
		return nil, err
	}

	// Session events
	seRows, err := database.Conn().Query(
		`SELECT timestamp, session_id, event, stage
		 FROM session_events WHERE issue = ? ORDER BY timestamp, id`,
		issue,
	)
	if err != nil {
		return nil, fmt.Errorf("query session events: %w", err)
	}
	defer seRows.Close()

	for seRows.Next() {
		var ts, sessionID, event, stage string
		if err := seRows.Scan(&ts, &sessionID, &event, &stage); err != nil {
			return nil, fmt.Errorf("scan session event: %w", err)
		}
		results = append(results, IssueEvent{
			Timestamp: ts,
			Type:      "session",
			Event:     event,
			Stage:     stage,
			Detail:    fmt.Sprintf("session=%s", sessionID),
		})
	}
	if err := seRows.Err(); err != nil {
		return nil, err
	}

	// Sort all events by timestamp
	sort.SliceStable(results, func(i, j int) bool {
		return results[i].Timestamp < results[j].Timestamp
	})

	return results, nil
}

// --- helpers ---

func avg(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sum := 0.0
	for _, v := range values {
		sum += v
	}
	return math.Round(sum/float64(len(values))*10) / 10
}

func percentile(sorted []float64, p int) float64 {
	if len(sorted) == 0 {
		return 0
	}
	rank := float64(p) / 100.0 * float64(len(sorted)-1)
	lower := int(math.Floor(rank))
	upper := int(math.Ceil(rank))
	if lower == upper || upper >= len(sorted) {
		return math.Round(sorted[lower]*10) / 10
	}
	weight := rank - float64(lower)
	return math.Round((sorted[lower]*(1-weight)+sorted[upper]*weight)*10) / 10
}

func pct(n, total int) float64 {
	if total == 0 {
		return 0
	}
	return math.Round(float64(n)/float64(total)*1000) / 10
}
