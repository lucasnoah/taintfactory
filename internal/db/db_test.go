package db

import (
	"testing"
)

func testDB(t *testing.T) *DB {
	t.Helper()
	d, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := d.Migrate(); err != nil {
		t.Fatalf("migrate test db: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func TestMigrate(t *testing.T) {
	d, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer d.Close()

	if err := d.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// Verify all tables exist
	tables := []string{"schema_version", "session_events", "check_runs", "pipeline_events"}
	for _, table := range tables {
		var name string
		err := d.conn.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&name)
		if err != nil {
			t.Errorf("table %s not found: %v", table, err)
		}
	}

	// Verify schema_version was recorded
	var version int
	if err := d.conn.QueryRow("SELECT version FROM schema_version").Scan(&version); err != nil {
		t.Fatalf("query schema_version: %v", err)
	}
	if version != 1 {
		t.Errorf("expected schema version 1, got %d", version)
	}

	// Migrate again should be idempotent
	if err := d.Migrate(); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
}

func TestReset(t *testing.T) {
	d := testDB(t)

	// Insert some data
	if err := d.LogSessionEvent("s1", 1, "plan", "started", nil, ""); err != nil {
		t.Fatalf("log event: %v", err)
	}

	if err := d.Reset(); err != nil {
		t.Fatalf("reset: %v", err)
	}

	// Data should be gone
	state, err := d.GetSessionState("s1")
	if err != nil {
		t.Fatalf("get state after reset: %v", err)
	}
	if state != nil {
		t.Error("expected nil state after reset")
	}

	// Tables should still exist (re-migrated)
	var name string
	err = d.conn.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name='session_events'").Scan(&name)
	if err != nil {
		t.Error("session_events table missing after reset")
	}
}

func TestLogSessionEvent_GetSessionState(t *testing.T) {
	d := testDB(t)

	exitCode := 0
	if err := d.LogSessionEvent("sess-1", 42, "plan", "started", &exitCode, `{"key":"val"}`); err != nil {
		t.Fatalf("log event: %v", err)
	}

	state, err := d.GetSessionState("sess-1")
	if err != nil {
		t.Fatalf("get state: %v", err)
	}
	if state == nil {
		t.Fatal("expected non-nil state")
	}
	if state.SessionID != "sess-1" {
		t.Errorf("session_id = %q, want %q", state.SessionID, "sess-1")
	}
	if state.Issue != 42 {
		t.Errorf("issue = %d, want 42", state.Issue)
	}
	if state.Stage != "plan" {
		t.Errorf("stage = %q, want %q", state.Stage, "plan")
	}
	if state.Event != "started" {
		t.Errorf("event = %q, want %q", state.Event, "started")
	}
	if state.ExitCode == nil || *state.ExitCode != 0 {
		t.Errorf("exit_code = %v, want 0", state.ExitCode)
	}
	if state.Metadata != `{"key":"val"}` {
		t.Errorf("metadata = %q, want %q", state.Metadata, `{"key":"val"}`)
	}

	// Nil exit code
	if err := d.LogSessionEvent("sess-2", 1, "code", "active", nil, ""); err != nil {
		t.Fatalf("log event: %v", err)
	}
	state2, err := d.GetSessionState("sess-2")
	if err != nil {
		t.Fatalf("get state: %v", err)
	}
	if state2.ExitCode != nil {
		t.Errorf("exit_code = %v, want nil", state2.ExitCode)
	}
}

func TestGetSessionState_NotFound(t *testing.T) {
	d := testDB(t)

	state, err := d.GetSessionState("nonexistent")
	if err != nil {
		t.Fatalf("get state: %v", err)
	}
	if state != nil {
		t.Error("expected nil for nonexistent session")
	}
}

func TestGetSessionState_ReturnsLatest(t *testing.T) {
	d := testDB(t)

	// Insert events with explicit timestamps to control ordering
	d.conn.Exec(`INSERT INTO session_events (session_id, issue, stage, event, timestamp) VALUES (?, ?, ?, ?, ?)`,
		"sess-1", 1, "plan", "started", "2024-01-15 10:00:00")
	d.conn.Exec(`INSERT INTO session_events (session_id, issue, stage, event, timestamp) VALUES (?, ?, ?, ?, ?)`,
		"sess-1", 1, "plan", "active", "2024-01-15 10:00:05")
	d.conn.Exec(`INSERT INTO session_events (session_id, issue, stage, event, timestamp) VALUES (?, ?, ?, ?, ?)`,
		"sess-1", 1, "plan", "idle", "2024-01-15 10:01:00")

	state, err := d.GetSessionState("sess-1")
	if err != nil {
		t.Fatalf("get state: %v", err)
	}
	if state.Event != "idle" {
		t.Errorf("event = %q, want %q", state.Event, "idle")
	}
}

func TestGetAllActiveSessions(t *testing.T) {
	d := testDB(t)

	// Session 1: started then active
	d.conn.Exec(`INSERT INTO session_events (session_id, issue, stage, event, timestamp) VALUES (?, ?, ?, ?, ?)`,
		"sess-1", 1, "plan", "started", "2024-01-15 10:00:00")
	d.conn.Exec(`INSERT INTO session_events (session_id, issue, stage, event, timestamp) VALUES (?, ?, ?, ?, ?)`,
		"sess-1", 1, "plan", "active", "2024-01-15 10:00:05")

	// Session 2: started then exited
	d.conn.Exec(`INSERT INTO session_events (session_id, issue, stage, event, timestamp) VALUES (?, ?, ?, ?, ?)`,
		"sess-2", 2, "code", "started", "2024-01-15 10:00:00")
	d.conn.Exec(`INSERT INTO session_events (session_id, issue, stage, event, timestamp) VALUES (?, ?, ?, ?, ?)`,
		"sess-2", 2, "code", "exited", "2024-01-15 10:05:00")

	// Session 3: just started
	d.conn.Exec(`INSERT INTO session_events (session_id, issue, stage, event, timestamp) VALUES (?, ?, ?, ?, ?)`,
		"sess-3", 3, "test", "started", "2024-01-15 10:00:00")

	// Session 4: active then idle
	d.conn.Exec(`INSERT INTO session_events (session_id, issue, stage, event, timestamp) VALUES (?, ?, ?, ?, ?)`,
		"sess-4", 4, "plan", "active", "2024-01-15 10:00:00")
	d.conn.Exec(`INSERT INTO session_events (session_id, issue, stage, event, timestamp) VALUES (?, ?, ?, ?, ?)`,
		"sess-4", 4, "plan", "idle", "2024-01-15 10:01:00")

	sessions, err := d.GetAllActiveSessions()
	if err != nil {
		t.Fatalf("get active sessions: %v", err)
	}

	// Should return sess-1 (active), sess-3 (started), and sess-4 (idle)
	// Idle sessions are still alive and should be included
	if len(sessions) != 3 {
		t.Fatalf("got %d active sessions, want 3", len(sessions))
	}

	ids := map[string]bool{}
	for _, s := range sessions {
		ids[s.SessionID] = true
	}
	if !ids["sess-1"] {
		t.Error("expected sess-1 in active sessions")
	}
	if !ids["sess-3"] {
		t.Error("expected sess-3 in active sessions")
	}
	if ids["sess-2"] {
		t.Error("sess-2 (exited) should not be active")
	}
	if !ids["sess-4"] {
		t.Error("expected sess-4 (idle) in active sessions")
	}
}

func TestLogCheckRun_GetCheckRuns(t *testing.T) {
	d := testDB(t)

	if err := d.LogCheckRun(1, "code", 1, 0, "lint", true, false, 0, 1500, "all passed", ""); err != nil {
		t.Fatalf("log check run: %v", err)
	}
	if err := d.LogCheckRun(1, "code", 1, 0, "test", false, false, 1, 5000, "3 failed", "test_foo.go:12"); err != nil {
		t.Fatalf("log check run: %v", err)
	}
	// Different fix round
	if err := d.LogCheckRun(1, "code", 1, 1, "test", true, true, 0, 4800, "all passed", ""); err != nil {
		t.Fatalf("log check run: %v", err)
	}

	runs, err := d.GetCheckRuns(1, "code", 0)
	if err != nil {
		t.Fatalf("get check runs: %v", err)
	}
	if len(runs) != 2 {
		t.Fatalf("got %d runs for fix_round=0, want 2", len(runs))
	}
	if runs[0].CheckName != "lint" || !runs[0].Passed {
		t.Errorf("run[0]: name=%q passed=%v, want lint/true", runs[0].CheckName, runs[0].Passed)
	}
	if runs[1].CheckName != "test" || runs[1].Passed {
		t.Errorf("run[1]: name=%q passed=%v, want test/false", runs[1].CheckName, runs[1].Passed)
	}
	if runs[1].ExitCode != 1 {
		t.Errorf("run[1].ExitCode = %d, want 1", runs[1].ExitCode)
	}
	if runs[1].DurationMs != 5000 {
		t.Errorf("run[1].DurationMs = %d, want 5000", runs[1].DurationMs)
	}

	// Fix round 1
	runs1, err := d.GetCheckRuns(1, "code", 1)
	if err != nil {
		t.Fatalf("get check runs round 1: %v", err)
	}
	if len(runs1) != 1 {
		t.Fatalf("got %d runs for fix_round=1, want 1", len(runs1))
	}
	if !runs1[0].AutoFixed {
		t.Error("expected auto_fixed=true for fix round 1")
	}
}

func TestGetLatestCheckRun(t *testing.T) {
	d := testDB(t)

	if err := d.LogCheckRun(1, "code", 1, 0, "lint", false, false, 1, 1000, "failed", "err1"); err != nil {
		t.Fatalf("log check run: %v", err)
	}
	if err := d.LogCheckRun(1, "code", 1, 1, "lint", true, true, 0, 900, "passed", ""); err != nil {
		t.Fatalf("log check run: %v", err)
	}

	run, err := d.GetLatestCheckRun(1, "lint")
	if err != nil {
		t.Fatalf("get latest: %v", err)
	}
	if run == nil {
		t.Fatal("expected non-nil run")
	}
	if !run.Passed {
		t.Error("expected latest run to be passed")
	}
	if run.FixRound != 1 {
		t.Errorf("fix_round = %d, want 1", run.FixRound)
	}

	// Nonexistent check
	run2, err := d.GetLatestCheckRun(1, "nonexistent")
	if err != nil {
		t.Fatalf("get latest nonexistent: %v", err)
	}
	if run2 != nil {
		t.Error("expected nil for nonexistent check")
	}
}

func TestLogPipelineEvent_GetPipelineHistory(t *testing.T) {
	d := testDB(t)

	if err := d.LogPipelineEvent(1, "pipeline_started", "plan", 1, "starting pipeline"); err != nil {
		t.Fatalf("log pipeline event: %v", err)
	}
	if err := d.LogPipelineEvent(1, "stage_completed", "plan", 1, "plan done"); err != nil {
		t.Fatalf("log pipeline event: %v", err)
	}
	if err := d.LogPipelineEvent(2, "pipeline_started", "code", 1, "issue 2"); err != nil {
		t.Fatalf("log pipeline event: %v", err)
	}

	history, err := d.GetPipelineHistory(1)
	if err != nil {
		t.Fatalf("get history: %v", err)
	}
	if len(history) != 2 {
		t.Fatalf("got %d events, want 2", len(history))
	}
	// Should be in descending order (most recent first)
	if history[0].Event != "stage_completed" {
		t.Errorf("history[0].Event = %q, want stage_completed", history[0].Event)
	}
	if history[1].Event != "pipeline_started" {
		t.Errorf("history[1].Event = %q, want pipeline_started", history[1].Event)
	}
	if history[0].Detail != "plan done" {
		t.Errorf("history[0].Detail = %q, want %q", history[0].Detail, "plan done")
	}

	// Issue 2 should have its own history
	history2, err := d.GetPipelineHistory(2)
	if err != nil {
		t.Fatalf("get history issue 2: %v", err)
	}
	if len(history2) != 1 {
		t.Fatalf("got %d events for issue 2, want 1", len(history2))
	}
}

func TestDetectHumanIntervention(t *testing.T) {
	t.Run("no active events", func(t *testing.T) {
		d := testDB(t)

		human, err := d.DetectHumanIntervention("sess-1")
		if err != nil {
			t.Fatalf("detect: %v", err)
		}
		if human {
			t.Error("expected false when no active events")
		}
	})

	t.Run("human triggered", func(t *testing.T) {
		d := testDB(t)

		// Active event with no preceding factory_send
		d.conn.Exec(`INSERT INTO session_events (session_id, issue, stage, event, timestamp) VALUES (?, ?, ?, ?, ?)`,
			"sess-1", 1, "plan", "started", "2024-01-15 10:00:00")
		d.conn.Exec(`INSERT INTO session_events (session_id, issue, stage, event, timestamp) VALUES (?, ?, ?, ?, ?)`,
			"sess-1", 1, "plan", "active", "2024-01-15 10:00:10")

		human, err := d.DetectHumanIntervention("sess-1")
		if err != nil {
			t.Fatalf("detect: %v", err)
		}
		if !human {
			t.Error("expected true for human-triggered active")
		}
	})

	t.Run("factory triggered", func(t *testing.T) {
		d := testDB(t)

		// factory_send 3 seconds before active
		d.conn.Exec(`INSERT INTO session_events (session_id, issue, stage, event, timestamp) VALUES (?, ?, ?, ?, ?)`,
			"sess-1", 1, "plan", "factory_send", "2024-01-15 10:00:07")
		d.conn.Exec(`INSERT INTO session_events (session_id, issue, stage, event, timestamp) VALUES (?, ?, ?, ?, ?)`,
			"sess-1", 1, "plan", "active", "2024-01-15 10:00:10")

		human, err := d.DetectHumanIntervention("sess-1")
		if err != nil {
			t.Fatalf("detect: %v", err)
		}
		if human {
			t.Error("expected false for factory-triggered active")
		}
	})

	t.Run("factory send too old", func(t *testing.T) {
		d := testDB(t)

		// factory_send 10 seconds before active (outside 5s window)
		d.conn.Exec(`INSERT INTO session_events (session_id, issue, stage, event, timestamp) VALUES (?, ?, ?, ?, ?)`,
			"sess-1", 1, "plan", "factory_send", "2024-01-15 10:00:00")
		d.conn.Exec(`INSERT INTO session_events (session_id, issue, stage, event, timestamp) VALUES (?, ?, ?, ?, ?)`,
			"sess-1", 1, "plan", "active", "2024-01-15 10:00:10")

		human, err := d.DetectHumanIntervention("sess-1")
		if err != nil {
			t.Fatalf("detect: %v", err)
		}
		if !human {
			t.Error("expected true when factory_send is older than 5 seconds")
		}
	})

	t.Run("factory send exactly at boundary", func(t *testing.T) {
		d := testDB(t)

		// factory_send exactly 5 seconds before active
		d.conn.Exec(`INSERT INTO session_events (session_id, issue, stage, event, timestamp) VALUES (?, ?, ?, ?, ?)`,
			"sess-1", 1, "plan", "factory_send", "2024-01-15 10:00:05")
		d.conn.Exec(`INSERT INTO session_events (session_id, issue, stage, event, timestamp) VALUES (?, ?, ?, ?, ?)`,
			"sess-1", 1, "plan", "active", "2024-01-15 10:00:10")

		human, err := d.DetectHumanIntervention("sess-1")
		if err != nil {
			t.Fatalf("detect: %v", err)
		}
		if human {
			t.Error("expected false when factory_send is exactly at 5-second boundary")
		}
	})
}

func TestMultipleSessionsIsolation(t *testing.T) {
	d := testDB(t)

	// Two sessions, different issues
	if err := d.LogSessionEvent("sess-A", 10, "plan", "started", nil, ""); err != nil {
		t.Fatalf("log A: %v", err)
	}
	if err := d.LogSessionEvent("sess-B", 20, "code", "active", nil, ""); err != nil {
		t.Fatalf("log B: %v", err)
	}

	stateA, err := d.GetSessionState("sess-A")
	if err != nil {
		t.Fatalf("get A: %v", err)
	}
	stateB, err := d.GetSessionState("sess-B")
	if err != nil {
		t.Fatalf("get B: %v", err)
	}

	if stateA.Issue != 10 || stateA.Event != "started" {
		t.Errorf("sess-A: issue=%d event=%s, want 10/started", stateA.Issue, stateA.Event)
	}
	if stateB.Issue != 20 || stateB.Event != "active" {
		t.Errorf("sess-B: issue=%d event=%s, want 20/active", stateB.Issue, stateB.Event)
	}

	// Check runs for different issues shouldn't interfere
	d.LogCheckRun(10, "plan", 1, 0, "lint", true, false, 0, 100, "", "")
	d.LogCheckRun(20, "code", 1, 0, "test", false, false, 1, 200, "", "")

	runs10, _ := d.GetCheckRuns(10, "plan", 0)
	runs20, _ := d.GetCheckRuns(20, "code", 0)
	if len(runs10) != 1 || runs10[0].CheckName != "lint" {
		t.Errorf("issue 10 check runs unexpected: %v", runs10)
	}
	if len(runs20) != 1 || runs20[0].CheckName != "test" {
		t.Errorf("issue 20 check runs unexpected: %v", runs20)
	}

	// Pipeline events for different issues
	d.LogPipelineEvent(10, "started", "plan", 1, "")
	d.LogPipelineEvent(20, "started", "code", 1, "")

	hist10, _ := d.GetPipelineHistory(10)
	hist20, _ := d.GetPipelineHistory(20)
	if len(hist10) != 1 {
		t.Errorf("issue 10 pipeline events: got %d, want 1", len(hist10))
	}
	if len(hist20) != 1 {
		t.Errorf("issue 20 pipeline events: got %d, want 1", len(hist20))
	}
}
