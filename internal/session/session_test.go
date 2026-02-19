package session

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lucasnoah/taintfactory/internal/db"
)

// mockTmux records calls and returns configurable results.
type mockTmux struct {
	calls       []string
	sessions    map[string]bool // live sessions
	captureText string
	listErr     error
}

func newMockTmux() *mockTmux {
	return &mockTmux{sessions: make(map[string]bool)}
}

func (m *mockTmux) NewSession(name string) error {
	m.calls = append(m.calls, fmt.Sprintf("new-session %s", name))
	m.sessions[name] = true
	return nil
}

func (m *mockTmux) SendKeys(session string, keys string) error {
	m.calls = append(m.calls, fmt.Sprintf("send-keys %s %q", session, keys))
	return nil
}

func (m *mockTmux) KillSession(name string) error {
	m.calls = append(m.calls, fmt.Sprintf("kill-session %s", name))
	delete(m.sessions, name)
	return nil
}

func (m *mockTmux) CapturePane(name string) (string, error) {
	m.calls = append(m.calls, fmt.Sprintf("capture-pane %s", name))
	return m.captureText, nil
}

func (m *mockTmux) ListSessions() ([]string, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	var names []string
	for n := range m.sessions {
		names = append(names, n)
	}
	return names, nil
}

func (m *mockTmux) HasSession(name string) (bool, error) {
	return m.sessions[name], nil
}

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

func TestCreate_HappyPath(t *testing.T) {
	tmux := newMockTmux()
	d := testDB(t)
	mgr := NewManager(tmux, d, nil)

	err := mgr.Create(CreateOpts{
		Name:    "test-1",
		Workdir: "/tmp/myproject",
		Flags:   "--verbose",
		Issue:   42,
		Stage:   "impl",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Verify tmux calls
	if len(tmux.calls) < 3 {
		t.Fatalf("expected at least 3 tmux calls, got %d: %v", len(tmux.calls), tmux.calls)
	}
	if tmux.calls[0] != "new-session test-1" {
		t.Errorf("call[0] = %q, want %q", tmux.calls[0], "new-session test-1")
	}
	if !strings.Contains(tmux.calls[1], "cd /tmp/myproject") {
		t.Errorf("call[1] = %q, want cd command", tmux.calls[1])
	}
	if !strings.Contains(tmux.calls[2], "claude") {
		t.Errorf("call[2] = %q, want claude command", tmux.calls[2])
	}
	// Non-interactive should include --print
	if !strings.Contains(tmux.calls[2], "--print") {
		t.Errorf("call[2] = %q, want --print flag for non-interactive", tmux.calls[2])
	}

	// Verify DB event
	state, err := d.GetSessionState("test-1")
	if err != nil {
		t.Fatalf("GetSessionState: %v", err)
	}
	if state == nil {
		t.Fatal("expected non-nil session state")
	}
	if state.Event != "started" {
		t.Errorf("event = %q, want %q", state.Event, "started")
	}
	if state.Issue != 42 {
		t.Errorf("issue = %d, want 42", state.Issue)
	}
	if state.Stage != "impl" {
		t.Errorf("stage = %q, want %q", state.Stage, "impl")
	}
}

func TestCreate_Interactive(t *testing.T) {
	tmux := newMockTmux()
	d := testDB(t)
	mgr := NewManager(tmux, d, nil)

	err := mgr.Create(CreateOpts{
		Name:        "test-i",
		Issue:       1,
		Stage:       "plan",
		Interactive: true,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Interactive should NOT include --print
	claudeCall := tmux.calls[len(tmux.calls)-1]
	if strings.Contains(claudeCall, "--print") {
		t.Errorf("interactive session should not have --print: %q", claudeCall)
	}
}

func TestCreate_NoWorkdir(t *testing.T) {
	tmux := newMockTmux()
	d := testDB(t)
	mgr := NewManager(tmux, d, nil)

	err := mgr.Create(CreateOpts{
		Name:  "test-nwd",
		Issue: 1,
		Stage: "plan",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Should have new-session + claude command (no cd)
	if len(tmux.calls) != 2 {
		t.Fatalf("expected 2 tmux calls (no cd), got %d: %v", len(tmux.calls), tmux.calls)
	}
}

func TestCreate_AlreadyExists(t *testing.T) {
	tmux := newMockTmux()
	tmux.sessions["existing"] = true
	d := testDB(t)
	mgr := NewManager(tmux, d, nil)

	err := mgr.Create(CreateOpts{
		Name:  "existing",
		Issue: 1,
		Stage: "plan",
	})
	if err == nil {
		t.Fatal("expected error for existing session")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("error = %q, want 'already exists'", err.Error())
	}
}

func TestKill_HappyPath(t *testing.T) {
	tmux := newMockTmux()
	tmux.sessions["test-k"] = true
	tmux.captureText = "session output line 1\nsession output line 2\n"
	d := testDB(t)
	mgr := NewManager(tmux, d, nil)

	// First create a "started" event so Kill can look up issue/stage
	if err := d.LogSessionEvent("test-k", 42, "impl", "started", nil, ""); err != nil {
		t.Fatalf("seed event: %v", err)
	}

	log, err := mgr.Kill("test-k")
	if err != nil {
		t.Fatalf("Kill: %v", err)
	}

	if !strings.Contains(log, "session output line 1") {
		t.Errorf("log = %q, want captured output", log)
	}

	// Verify tmux calls: capture-pane then kill-session
	if len(tmux.calls) < 2 {
		t.Fatalf("expected at least 2 tmux calls, got %d: %v", len(tmux.calls), tmux.calls)
	}
	if !strings.Contains(tmux.calls[0], "capture-pane") {
		t.Errorf("call[0] = %q, want capture-pane", tmux.calls[0])
	}
	if !strings.Contains(tmux.calls[1], "kill-session") {
		t.Errorf("call[1] = %q, want kill-session", tmux.calls[1])
	}

	// Verify DB "exited" event
	state, err := d.GetSessionState("test-k")
	if err != nil {
		t.Fatalf("GetSessionState: %v", err)
	}
	if state == nil {
		t.Fatal("expected non-nil session state")
	}
	if state.Event != "exited" {
		t.Errorf("event = %q, want %q", state.Event, "exited")
	}
}

func TestKill_Nonexistent(t *testing.T) {
	tmux := newMockTmux()
	d := testDB(t)
	mgr := NewManager(tmux, d, nil)

	_, err := mgr.Kill("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent session")
	}
	if !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("error = %q, want 'does not exist'", err.Error())
	}
}

func TestList_OrphanDetection(t *testing.T) {
	tmux := newMockTmux()
	tmux.sessions["sess-both"] = true  // in tmux and DB
	tmux.sessions["sess-no-db"] = true // in tmux only
	d := testDB(t)
	mgr := NewManager(tmux, d, nil)

	// sess-both: has DB event
	d.LogSessionEvent("sess-both", 1, "plan", "started", nil, "")
	// sess-db-only: in DB but not tmux
	d.LogSessionEvent("sess-db-only", 2, "impl", "started", nil, "")

	sessions, err := mgr.List(0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	if len(sessions) != 3 {
		t.Fatalf("got %d sessions, want 3", len(sessions))
	}

	byName := make(map[string]SessionInfo)
	for _, s := range sessions {
		byName[s.Name] = s
	}

	// sess-both: in both, no orphan
	if s, ok := byName["sess-both"]; !ok {
		t.Error("missing sess-both")
	} else if s.Orphan != "" {
		t.Errorf("sess-both orphan = %q, want empty", s.Orphan)
	}

	// sess-db-only: in DB but not tmux
	if s, ok := byName["sess-db-only"]; !ok {
		t.Error("missing sess-db-only")
	} else if s.Orphan != "no-tmux" {
		t.Errorf("sess-db-only orphan = %q, want %q", s.Orphan, "no-tmux")
	}

	// sess-no-db: in tmux but not DB
	if s, ok := byName["sess-no-db"]; !ok {
		t.Error("missing sess-no-db")
	} else if s.Orphan != "no-db" {
		t.Errorf("sess-no-db orphan = %q, want %q", s.Orphan, "no-db")
	}
}

func TestList_IssueFilter(t *testing.T) {
	tmux := newMockTmux()
	tmux.sessions["sess-1"] = true
	tmux.sessions["sess-2"] = true
	d := testDB(t)
	mgr := NewManager(tmux, d, nil)

	d.LogSessionEvent("sess-1", 10, "plan", "started", nil, "")
	d.LogSessionEvent("sess-2", 20, "impl", "started", nil, "")

	sessions, err := mgr.List(10)
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	// Should include sess-1 (issue 10) + sess-2 as no-db orphan (filter only applies to DB sessions)
	dbSessions := 0
	for _, s := range sessions {
		if s.Issue == 10 {
			dbSessions++
		}
	}
	if dbSessions != 1 {
		t.Errorf("expected 1 DB session with issue 10, got %d", dbSessions)
	}

	// sess-2 (issue 20) should NOT appear from DB
	for _, s := range sessions {
		if s.Issue == 20 {
			t.Error("session with issue 20 should be filtered out")
		}
	}
}

func TestCreate_WritesHooksConfig(t *testing.T) {
	tmux := newMockTmux()
	d := testDB(t)
	mgr := NewManager(tmux, d, nil)

	tmpDir := t.TempDir()
	err := mgr.Create(CreateOpts{
		Name:    "42-impl",
		Workdir: tmpDir,
		Issue:   42,
		Stage:   "impl",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Verify hooks.json was written
	data, err := os.ReadFile(filepath.Join(tmpDir, ".claude", "hooks.json"))
	if err != nil {
		t.Fatalf("read hooks.json: %v", err)
	}
	if !strings.Contains(string(data), "--session 42-impl") {
		t.Errorf("hooks.json missing session name: %s", data)
	}
	if !strings.Contains(string(data), "--issue 42") {
		t.Errorf("hooks.json missing issue: %s", data)
	}
}

func TestCreate_NoHooksWithoutIssue(t *testing.T) {
	tmux := newMockTmux()
	d := testDB(t)
	mgr := NewManager(tmux, d, nil)

	tmpDir := t.TempDir()
	err := mgr.Create(CreateOpts{
		Name:    "no-issue",
		Workdir: tmpDir,
		Stage:   "plan",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// hooks.json should NOT be written when issue is 0
	_, err = os.Stat(filepath.Join(tmpDir, ".claude", "hooks.json"))
	if err == nil {
		t.Error("hooks.json should not exist when issue is 0")
	}
}

func TestStatus_HappyPath(t *testing.T) {
	tmux := newMockTmux()
	tmux.sessions["test-s"] = true
	d := testDB(t)
	mgr := NewManager(tmux, d, nil)

	d.LogSessionEvent("test-s", 10, "plan", "started", nil, "")

	info, err := mgr.Status("test-s")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}

	if info.Name != "test-s" {
		t.Errorf("Name = %q, want %q", info.Name, "test-s")
	}
	if info.State != "started" {
		t.Errorf("State = %q, want %q", info.State, "started")
	}
	if info.Issue != 10 {
		t.Errorf("Issue = %d, want 10", info.Issue)
	}
	if info.Stage != "plan" {
		t.Errorf("Stage = %q, want %q", info.Stage, "plan")
	}
	if !info.TmuxAlive {
		t.Error("TmuxAlive = false, want true")
	}
}

func TestStatus_TmuxDead(t *testing.T) {
	tmux := newMockTmux()
	// Don't add session to tmux — it's dead
	d := testDB(t)
	mgr := NewManager(tmux, d, nil)

	d.LogSessionEvent("dead-sess", 1, "impl", "started", nil, "")

	info, err := mgr.Status("dead-sess")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}

	if info.TmuxAlive {
		t.Error("TmuxAlive = true, want false (session not in tmux)")
	}
}

func TestStatus_NotFound(t *testing.T) {
	tmux := newMockTmux()
	d := testDB(t)
	mgr := NewManager(tmux, d, nil)

	_, err := mgr.Status("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent session")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error = %q, want 'not found'", err.Error())
	}
}

func TestDetectHuman(t *testing.T) {
	tmux := newMockTmux()
	d := testDB(t)
	mgr := NewManager(tmux, d, nil)

	// Active event without preceding factory_send → human
	d.Conn().Exec(`INSERT INTO session_events (session_id, issue, stage, event, timestamp) VALUES (?, ?, ?, ?, ?)`,
		"sess-h", 1, "plan", "started", "2024-01-15 10:00:00")
	d.Conn().Exec(`INSERT INTO session_events (session_id, issue, stage, event, timestamp) VALUES (?, ?, ?, ?, ?)`,
		"sess-h", 1, "plan", "active", "2024-01-15 10:00:10")

	human, err := mgr.DetectHuman("sess-h")
	if err != nil {
		t.Fatalf("DetectHuman: %v", err)
	}
	if !human {
		t.Error("expected human=true when no factory_send precedes active")
	}
}

func TestBuildClaudeCommand(t *testing.T) {
	tests := []struct {
		name string
		opts CreateOpts
		want string
	}{
		{
			name: "basic non-interactive",
			opts: CreateOpts{},
			want: "claude --print",
		},
		{
			name: "with flags",
			opts: CreateOpts{Flags: "--verbose --model opus"},
			want: "claude --verbose --model opus --print",
		},
		{
			name: "interactive",
			opts: CreateOpts{Interactive: true},
			want: "claude",
		},
		{
			name: "interactive with flags",
			opts: CreateOpts{Flags: "--verbose", Interactive: true},
			want: "claude --verbose",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildClaudeCommand(tt.opts)
			if got != tt.want {
				t.Errorf("buildClaudeCommand() = %q, want %q", got, tt.want)
			}
		})
	}
}
