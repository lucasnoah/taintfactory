package analytics

import (
	"database/sql"
	"strings"
	"testing"

	"github.com/lucasnoah/taintfactory/internal/db"
)

func testDB(t *testing.T) *db.DB {
	t.Helper()
	d, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := d.Migrate(); err != nil {
		t.Fatalf("migrate test db: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func exec(t *testing.T, conn *sql.DB, query string, args ...interface{}) {
	t.Helper()
	if _, err := conn.Exec(query, args...); err != nil {
		t.Fatalf("exec %q: %v", query, err)
	}
}

// --- QueryStageDurations ---

func TestQueryStageDurations(t *testing.T) {
	d := testDB(t)
	c := d.Conn()

	// Issue 1: plan stage takes 10 min (created → completed)
	exec(t, c, `INSERT INTO pipeline_events (issue, event, stage, attempt, timestamp) VALUES (1, 'created', 'plan', 1, '2024-06-01 10:00:00')`)
	exec(t, c, `INSERT INTO pipeline_events (issue, event, stage, attempt, timestamp) VALUES (1, 'completed', 'plan', 1, '2024-06-01 10:10:00')`)

	// Issue 2: plan stage takes 20 min
	exec(t, c, `INSERT INTO pipeline_events (issue, event, stage, attempt, timestamp) VALUES (2, 'created', 'plan', 1, '2024-06-02 10:00:00')`)
	exec(t, c, `INSERT INTO pipeline_events (issue, event, stage, attempt, timestamp) VALUES (2, 'completed', 'plan', 1, '2024-06-02 10:20:00')`)

	results, err := QueryStageDurations(d, "")
	if err != nil {
		t.Fatalf("QueryStageDurations: %v", err)
	}

	if len(results) != 1 {
		t.Fatalf("expected 1 stage duration result, got %d", len(results))
	}

	planResult := results[0]
	if planResult.Stage != "plan" {
		t.Errorf("stage = %q, want plan", planResult.Stage)
	}
	if planResult.Count != 2 {
		t.Errorf("plan count = %d, want 2", planResult.Count)
	}
	if planResult.Avg != 15.0 {
		t.Errorf("plan avg = %f, want 15.0", planResult.Avg)
	}
}

func TestQueryStageDurations_MultiStage(t *testing.T) {
	d := testDB(t)
	c := d.Conn()

	// Issue 1: created → stage_advanced(plan) 10 min → stage_advanced(code) 30 min
	exec(t, c, `INSERT INTO pipeline_events (issue, event, stage, attempt, timestamp) VALUES (1, 'created', 'plan', 1, '2024-06-01 10:00:00')`)
	exec(t, c, `INSERT INTO pipeline_events (issue, event, stage, attempt, timestamp) VALUES (1, 'stage_advanced', 'plan', 1, '2024-06-01 10:10:00')`)
	exec(t, c, `INSERT INTO pipeline_events (issue, event, stage, attempt, timestamp) VALUES (1, 'stage_advanced', 'code', 1, '2024-06-01 10:40:00')`)

	results, err := QueryStageDurations(d, "")
	if err != nil {
		t.Fatalf("QueryStageDurations: %v", err)
	}

	// Should get 2 stages: plan (10min) and code (30min)
	if len(results) != 2 {
		t.Fatalf("expected 2 stage results, got %d", len(results))
	}

	stageMap := map[string]StageDuration{}
	for _, r := range results {
		stageMap[r.Stage] = r
	}

	if stageMap["plan"].Count != 1 || stageMap["plan"].Avg != 10.0 {
		t.Errorf("plan: count=%d avg=%.1f, want 1/10.0", stageMap["plan"].Count, stageMap["plan"].Avg)
	}
	if stageMap["code"].Count != 1 || stageMap["code"].Avg != 30.0 {
		t.Errorf("code: count=%d avg=%.1f, want 1/30.0", stageMap["code"].Count, stageMap["code"].Avg)
	}
}

func TestQueryStageDurations_NoDoubleCount(t *testing.T) {
	d := testDB(t)
	c := d.Conn()

	// Two stage_advanced events for same issue — should not create phantom duration
	exec(t, c, `INSERT INTO pipeline_events (issue, event, stage, attempt, timestamp) VALUES (1, 'created', 'plan', 1, '2024-06-01 10:00:00')`)
	exec(t, c, `INSERT INTO pipeline_events (issue, event, stage, attempt, timestamp) VALUES (1, 'stage_advanced', 'plan', 1, '2024-06-01 10:10:00')`)
	exec(t, c, `INSERT INTO pipeline_events (issue, event, stage, attempt, timestamp) VALUES (1, 'stage_advanced', 'code', 1, '2024-06-01 11:05:00')`)

	results, err := QueryStageDurations(d, "")
	if err != nil {
		t.Fatalf("QueryStageDurations: %v", err)
	}

	// plan should have exactly 1 measurement (10 min), not 2
	for _, r := range results {
		if r.Stage == "plan" && r.Count != 1 {
			t.Errorf("plan should have count=1, got %d (double-counting bug)", r.Count)
		}
	}
}

func TestQueryStageDurations_Since(t *testing.T) {
	d := testDB(t)
	c := d.Conn()

	exec(t, c, `INSERT INTO pipeline_events (issue, event, stage, attempt, timestamp) VALUES (1, 'created', 'plan', 1, '2024-01-01 10:00:00')`)
	exec(t, c, `INSERT INTO pipeline_events (issue, event, stage, attempt, timestamp) VALUES (1, 'stage_advanced', 'plan', 1, '2024-01-01 10:10:00')`)

	exec(t, c, `INSERT INTO pipeline_events (issue, event, stage, attempt, timestamp) VALUES (2, 'created', 'plan', 1, '2024-06-01 10:00:00')`)
	exec(t, c, `INSERT INTO pipeline_events (issue, event, stage, attempt, timestamp) VALUES (2, 'stage_advanced', 'plan', 1, '2024-06-01 10:30:00')`)

	results, err := QueryStageDurations(d, "2024-06-01")
	if err != nil {
		t.Fatalf("QueryStageDurations: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result with since filter, got %d", len(results))
	}
	if results[0].Avg != 30.0 {
		t.Errorf("avg = %f, want 30.0", results[0].Avg)
	}
}

func TestQueryStageDurations_Empty(t *testing.T) {
	d := testDB(t)

	results, err := QueryStageDurations(d, "")
	if err != nil {
		t.Fatalf("QueryStageDurations: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}

// --- QueryCheckFailureRates ---

func TestQueryCheckFailureRates(t *testing.T) {
	d := testDB(t)
	c := d.Conn()

	// Pipeline events: 2 successes, 1 escalation for 'code' stage
	exec(t, c, `INSERT INTO pipeline_events (issue, event, stage, attempt, timestamp) VALUES (1, 'stage_advanced', 'code', 1, '2024-06-01 10:00:00')`)
	exec(t, c, `INSERT INTO pipeline_events (issue, event, stage, attempt, timestamp) VALUES (2, 'stage_advanced', 'code', 1, '2024-06-02 10:00:00')`)
	exec(t, c, `INSERT INTO pipeline_events (issue, event, stage, attempt, timestamp) VALUES (3, 'escalated', 'code', 1, '2024-06-03 10:00:00')`)

	// Check runs (fix_round=0): 2 first-pass pass, 1 first-pass fail
	exec(t, c, `INSERT INTO check_runs (issue, stage, attempt, fix_round, check_name, passed, auto_fixed, duration_ms, timestamp) VALUES (1, 'code', 1, 0, 'lint', 1, 0, 100, '2024-06-01 09:55:00')`)
	exec(t, c, `INSERT INTO check_runs (issue, stage, attempt, fix_round, check_name, passed, auto_fixed, duration_ms, timestamp) VALUES (2, 'code', 1, 0, 'lint', 1, 0, 100, '2024-06-02 09:55:00')`)
	exec(t, c, `INSERT INTO check_runs (issue, stage, attempt, fix_round, check_name, passed, auto_fixed, duration_ms, timestamp) VALUES (3, 'code', 1, 0, 'lint', 0, 0, 100, '2024-06-03 09:55:00')`)

	results, err := QueryCheckFailureRates(d, "")
	if err != nil {
		t.Fatalf("QueryCheckFailureRates: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Total != 3 {
		t.Errorf("total = %d, want 3", results[0].Total)
	}
	// All percentages should use pipeline_events total (3) as denominator
	// FirstPass=2/3=66.7%, AfterFix=0/3=0%, Escalated=1/3=33.3%
	sum := results[0].FirstPass + results[0].AfterFix + results[0].Escalated
	if sum > 100.1 {
		t.Errorf("percentages sum to %.1f, should not exceed 100%%", sum)
	}
}

func TestQueryCheckFailureRates_ConsistentDenominator(t *testing.T) {
	d := testDB(t)
	c := d.Conn()

	// 10 pipeline events, 8 check runs — denominators should match
	for i := 1; i <= 7; i++ {
		exec(t, c, `INSERT INTO pipeline_events (issue, event, stage, attempt, timestamp) VALUES (?, 'stage_advanced', 'code', 1, '2024-06-01 10:00:00')`, i)
	}
	for i := 8; i <= 10; i++ {
		exec(t, c, `INSERT INTO pipeline_events (issue, event, stage, attempt, timestamp) VALUES (?, 'escalated', 'code', 1, '2024-06-01 10:00:00')`, i)
	}

	// Only 8 of 10 have check runs
	for i := 1; i <= 8; i++ {
		passed := 1
		if i > 6 {
			passed = 0
		}
		exec(t, c, `INSERT INTO check_runs (issue, stage, attempt, fix_round, check_name, passed, auto_fixed, duration_ms, timestamp) VALUES (?, 'code', 1, 0, 'lint', ?, 0, 100, '2024-06-01 09:55:00')`, i, passed)
	}

	results, err := QueryCheckFailureRates(d, "")
	if err != nil {
		t.Fatalf("QueryCheckFailureRates: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}

	sum := results[0].FirstPass + results[0].AfterFix + results[0].Escalated
	if sum > 100.1 {
		t.Errorf("percentages sum to %.1f%%, should not exceed 100%%", sum)
	}
}

// --- QueryCheckFailures ---

func TestQueryCheckFailures(t *testing.T) {
	d := testDB(t)
	c := d.Conn()

	exec(t, c, `INSERT INTO check_runs (issue, stage, attempt, fix_round, check_name, passed, auto_fixed, duration_ms, summary, timestamp) VALUES (1, 'code', 1, 0, 'lint', 1, 0, 100, '', '2024-06-01 10:00:00')`)
	exec(t, c, `INSERT INTO check_runs (issue, stage, attempt, fix_round, check_name, passed, auto_fixed, duration_ms, summary, timestamp) VALUES (2, 'code', 1, 0, 'lint', 0, 1, 200, 'trailing whitespace', '2024-06-02 10:00:00')`)
	exec(t, c, `INSERT INTO check_runs (issue, stage, attempt, fix_round, check_name, passed, auto_fixed, duration_ms, summary, timestamp) VALUES (3, 'code', 1, 0, 'lint', 1, 0, 100, '', '2024-06-03 10:00:00')`)

	exec(t, c, `INSERT INTO check_runs (issue, stage, attempt, fix_round, check_name, passed, auto_fixed, duration_ms, summary, timestamp) VALUES (1, 'code', 1, 0, 'test', 0, 0, 5000, 'nil pointer', '2024-06-01 10:01:00')`)
	exec(t, c, `INSERT INTO check_runs (issue, stage, attempt, fix_round, check_name, passed, auto_fixed, duration_ms, summary, timestamp) VALUES (2, 'code', 1, 0, 'test', 0, 0, 4800, 'nil pointer', '2024-06-02 10:01:00')`)

	results, err := QueryCheckFailures(d, "")
	if err != nil {
		t.Fatalf("QueryCheckFailures: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	if results[0].Check != "test" {
		t.Errorf("results[0].Check = %q, want test", results[0].Check)
	}
	if results[0].FailRate != 100.0 {
		t.Errorf("test fail rate = %f, want 100.0", results[0].FailRate)
	}
	if results[0].CommonRules != "nil pointer" {
		t.Errorf("test common rules = %q, want 'nil pointer'", results[0].CommonRules)
	}

	if results[1].Check != "lint" {
		t.Errorf("results[1].Check = %q, want lint", results[1].Check)
	}
}

func TestQueryCheckFailures_AutoFixRate(t *testing.T) {
	d := testDB(t)
	c := d.Conn()

	// auto_fixed=1 with passed=1 should NOT count toward auto-fix rate
	exec(t, c, `INSERT INTO check_runs (issue, stage, attempt, fix_round, check_name, passed, auto_fixed, duration_ms, timestamp) VALUES (1, 'code', 1, 0, 'lint', 0, 1, 100, '2024-06-01 10:00:00')`)
	exec(t, c, `INSERT INTO check_runs (issue, stage, attempt, fix_round, check_name, passed, auto_fixed, duration_ms, timestamp) VALUES (2, 'code', 1, 0, 'lint', 1, 1, 100, '2024-06-02 10:00:00')`)
	exec(t, c, `INSERT INTO check_runs (issue, stage, attempt, fix_round, check_name, passed, auto_fixed, duration_ms, timestamp) VALUES (3, 'code', 1, 0, 'lint', 1, 0, 100, '2024-06-03 10:00:00')`)

	results, err := QueryCheckFailures(d, "")
	if err != nil {
		t.Fatalf("QueryCheckFailures: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}

	// 1 failure, 1 auto-fixed failure → AutoFixRate = 100%
	if results[0].AutoFixRate > 100.1 {
		t.Errorf("AutoFixRate = %.1f%%, should not exceed 100%%", results[0].AutoFixRate)
	}
}

func TestQueryCheckFailures_Empty(t *testing.T) {
	d := testDB(t)

	results, err := QueryCheckFailures(d, "")
	if err != nil {
		t.Fatalf("QueryCheckFailures: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}

// --- QueryFixRounds ---

func TestQueryFixRounds(t *testing.T) {
	d := testDB(t)
	c := d.Conn()

	exec(t, c, `INSERT INTO pipeline_events (issue, event, stage, attempt, detail, timestamp) VALUES (1, 'stage_advanced', 'code', 1, 'rounds=0', '2024-06-01 10:00:00')`)
	exec(t, c, `INSERT INTO pipeline_events (issue, event, stage, attempt, detail, timestamp) VALUES (2, 'stage_advanced', 'code', 1, 'rounds=2', '2024-06-02 10:00:00')`)
	exec(t, c, `INSERT INTO pipeline_events (issue, event, stage, attempt, detail, timestamp) VALUES (3, 'completed', 'code', 1, 'rounds=1', '2024-06-03 10:00:00')`)
	exec(t, c, `INSERT INTO pipeline_events (issue, event, stage, attempt, detail, timestamp) VALUES (4, 'fix_loop_exhausted', 'code', 1, 'rounds=5', '2024-06-04 10:00:00')`)

	results, err := QueryFixRounds(d, "")
	if err != nil {
		t.Fatalf("QueryFixRounds: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 stage result, got %d", len(results))
	}

	r := results[0]
	if r.Total != 4 {
		t.Errorf("total = %d, want 4", r.Total)
	}
	if r.Zero != 25.0 {
		t.Errorf("zero = %f, want 25.0", r.Zero)
	}
	if r.One != 25.0 {
		t.Errorf("one = %f, want 25.0", r.One)
	}
	if r.Two != 25.0 {
		t.Errorf("two = %f, want 25.0", r.Two)
	}
	if r.ThreePlus != 25.0 {
		t.Errorf("three_plus = %f, want 25.0", r.ThreePlus)
	}
}

func TestQueryFixRounds_NoDetail(t *testing.T) {
	d := testDB(t)
	c := d.Conn()

	exec(t, c, `INSERT INTO pipeline_events (issue, event, stage, attempt, timestamp) VALUES (1, 'stage_advanced', 'code', 1, '2024-06-01 10:00:00')`)

	results, err := QueryFixRounds(d, "")
	if err != nil {
		t.Fatalf("QueryFixRounds: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Zero != 100.0 {
		t.Errorf("zero = %f, want 100.0", results[0].Zero)
	}
}

// --- QueryPipelineThroughput ---

func TestQueryPipelineThroughput(t *testing.T) {
	d := testDB(t)
	c := d.Conn()

	exec(t, c, `INSERT INTO pipeline_events (issue, event, stage, attempt, timestamp) VALUES (1, 'created', 'plan', 1, '2024-06-03 10:00:00')`)
	exec(t, c, `INSERT INTO pipeline_events (issue, event, stage, attempt, timestamp) VALUES (2, 'created', 'plan', 1, '2024-06-03 11:00:00')`)
	exec(t, c, `INSERT INTO pipeline_events (issue, event, stage, attempt, timestamp) VALUES (1, 'completed', 'code', 1, '2024-06-04 10:00:00')`)
	exec(t, c, `INSERT INTO pipeline_events (issue, event, stage, attempt, timestamp) VALUES (2, 'failed', 'code', 1, '2024-06-04 11:00:00')`)

	results, err := QueryPipelineThroughput(d, "")
	if err != nil {
		t.Fatalf("QueryPipelineThroughput: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least 1 result")
	}

	r := results[0]
	// Verify period is an actual year-week, not a literal string
	if !strings.HasPrefix(r.Period, "2024-W") {
		t.Errorf("period = %q, want format 2024-WNN", r.Period)
	}
	if r.Created != 2 {
		t.Errorf("created = %d, want 2", r.Created)
	}
	if r.Completed != 1 {
		t.Errorf("completed = %d, want 1", r.Completed)
	}
	if r.Failed != 1 {
		t.Errorf("failed = %d, want 1", r.Failed)
	}
}

func TestQueryPipelineThroughput_WeeklyGrouping(t *testing.T) {
	d := testDB(t)
	c := d.Conn()

	// Events in two different weeks
	exec(t, c, `INSERT INTO pipeline_events (issue, event, stage, attempt, timestamp) VALUES (1, 'created', 'plan', 1, '2024-06-03 10:00:00')`)
	exec(t, c, `INSERT INTO pipeline_events (issue, event, stage, attempt, timestamp) VALUES (2, 'created', 'plan', 1, '2024-06-10 10:00:00')`)

	results, err := QueryPipelineThroughput(d, "")
	if err != nil {
		t.Fatalf("QueryPipelineThroughput: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("expected 2 weekly periods, got %d", len(results))
	}
}

func TestQueryPipelineThroughput_Empty(t *testing.T) {
	d := testDB(t)

	results, err := QueryPipelineThroughput(d, "")
	if err != nil {
		t.Fatalf("QueryPipelineThroughput: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}

// --- QueryIssueDetail ---

func TestQueryIssueDetail(t *testing.T) {
	d := testDB(t)
	c := d.Conn()

	exec(t, c, `INSERT INTO pipeline_events (issue, event, stage, attempt, detail, timestamp) VALUES (42, 'created', 'plan', 1, '', '2024-06-01 10:00:00')`)
	exec(t, c, `INSERT INTO pipeline_events (issue, event, stage, attempt, detail, timestamp) VALUES (42, 'stage_advanced', 'plan', 1, 'done', '2024-06-01 10:10:00')`)
	exec(t, c, `INSERT INTO check_runs (issue, stage, attempt, fix_round, check_name, passed, auto_fixed, duration_ms, summary, timestamp) VALUES (42, 'plan', 1, 0, 'lint', 1, 0, 200, 'ok', '2024-06-01 10:08:00')`)
	exec(t, c, `INSERT INTO session_events (session_id, issue, stage, event, timestamp) VALUES ('s1', 42, 'plan', 'started', '2024-06-01 10:00:01')`)
	exec(t, c, `INSERT INTO session_events (session_id, issue, stage, event, timestamp) VALUES ('s1', 42, 'plan', 'exited', '2024-06-01 10:09:00')`)
	exec(t, c, `INSERT INTO pipeline_events (issue, event, stage, attempt, timestamp) VALUES (99, 'created', 'plan', 1, '2024-06-01 10:00:00')`)

	results, err := QueryIssueDetail(d, 42)
	if err != nil {
		t.Fatalf("QueryIssueDetail: %v", err)
	}
	if len(results) != 5 {
		t.Fatalf("expected 5 events, got %d", len(results))
	}

	for i := 1; i < len(results); i++ {
		if results[i].Timestamp < results[i-1].Timestamp {
			t.Errorf("events not in order: [%d]=%s > [%d]=%s", i-1, results[i-1].Timestamp, i, results[i].Timestamp)
		}
	}

	types := map[string]int{}
	for _, e := range results {
		types[e.Type]++
	}
	if types["pipeline"] != 2 {
		t.Errorf("pipeline events = %d, want 2", types["pipeline"])
	}
	if types["check"] != 1 {
		t.Errorf("check events = %d, want 1", types["check"])
	}
	if types["session"] != 2 {
		t.Errorf("session events = %d, want 2", types["session"])
	}
}

func TestQueryIssueDetail_Empty(t *testing.T) {
	d := testDB(t)

	results, err := QueryIssueDetail(d, 999)
	if err != nil {
		t.Fatalf("QueryIssueDetail: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}

// --- Helper tests ---

func TestParseTimestamp(t *testing.T) {
	tests := []struct {
		input string
		valid bool
	}{
		{"2024-06-01 10:00:00", true},
		{"2024-06-01T10:00:00Z", true},
		{"2024-06-01T10:00:00", true},
		{"2024-06-01 10:00:00.000", true},
		{"not-a-date", false},
	}
	for _, tc := range tests {
		_, err := parseTimestamp(tc.input)
		if tc.valid && err != nil {
			t.Errorf("parseTimestamp(%q) = error %v, want success", tc.input, err)
		}
		if !tc.valid && err == nil {
			t.Errorf("parseTimestamp(%q) = success, want error", tc.input)
		}
	}
}

func TestAvg(t *testing.T) {
	if v := avg([]float64{10, 20, 30}); v != 20.0 {
		t.Errorf("avg([10,20,30]) = %f, want 20.0", v)
	}
	if v := avg(nil); v != 0.0 {
		t.Errorf("avg(nil) = %f, want 0.0", v)
	}
}

func TestPercentile(t *testing.T) {
	values := []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	p50 := percentile(values, 50)
	if p50 < 5.0 || p50 > 6.0 {
		t.Errorf("p50 = %f, expected ~5.5", p50)
	}
	p95 := percentile(values, 95)
	if p95 < 9.0 || p95 > 10.0 {
		t.Errorf("p95 = %f, expected ~9.6", p95)
	}
	if v := percentile(nil, 50); v != 0.0 {
		t.Errorf("percentile(nil, 50) = %f, want 0.0", v)
	}
}

func TestPct(t *testing.T) {
	if v := pct(1, 4); v != 25.0 {
		t.Errorf("pct(1,4) = %f, want 25.0", v)
	}
	if v := pct(0, 0); v != 0.0 {
		t.Errorf("pct(0,0) = %f, want 0.0", v)
	}
}
