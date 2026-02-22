package session

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lucasnoah/taintfactory/internal/db"
)

// mockTmux records calls and returns configurable results.
type mockTmux struct {
	calls            []string
	sessions         map[string]bool // live sessions
	captureText      string
	captureLinesFn   func() string // if set, overrides captureText for CapturePaneLines
	listErr          error
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

func (m *mockTmux) SendBuffer(session string, content string) error {
	m.calls = append(m.calls, fmt.Sprintf("send-buffer %s %q", session, content))
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

func (m *mockTmux) CapturePaneLines(name string, lines int) (string, error) {
	m.calls = append(m.calls, fmt.Sprintf("capture-pane-lines %s %d", name, lines))
	if m.captureLinesFn != nil {
		return m.captureLinesFn(), nil
	}
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

	// Verify tmux calls: new-session, cd, unset CLAUDECODE, claude command
	if len(tmux.calls) < 4 {
		t.Fatalf("expected at least 4 tmux calls, got %d: %v", len(tmux.calls), tmux.calls)
	}
	if tmux.calls[0] != "new-session test-1" {
		t.Errorf("call[0] = %q, want %q", tmux.calls[0], "new-session test-1")
	}
	if !strings.Contains(tmux.calls[1], "cd '/tmp/myproject'") {
		t.Errorf("call[1] = %q, want shell-quoted cd command", tmux.calls[1])
	}
	if !strings.Contains(tmux.calls[2], "unset CLAUDECODE") {
		t.Errorf("call[2] = %q, want unset CLAUDECODE", tmux.calls[2])
	}
	if !strings.Contains(tmux.calls[3], "claude") {
		t.Errorf("call[3] = %q, want claude command", tmux.calls[3])
	}
	// Non-interactive should include --print
	if !strings.Contains(tmux.calls[3], "--print") {
		t.Errorf("call[3] = %q, want --print flag for non-interactive", tmux.calls[3])
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

	// Should have new-session + unset CLAUDECODE + claude command (no cd)
	if len(tmux.calls) != 3 {
		t.Fatalf("expected 3 tmux calls (no cd), got %d: %v", len(tmux.calls), tmux.calls)
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

	// Should include only sess-1 (issue 10) — sess-2 is in DB so not an orphan
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d: %v", len(sessions), sessions)
	}
	if sessions[0].Name != "sess-1" {
		t.Errorf("expected sess-1, got %q", sessions[0].Name)
	}
	if sessions[0].Issue != 10 {
		t.Errorf("expected issue 10, got %d", sessions[0].Issue)
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

	// Verify settings.local.json was written with hooks
	data, err := os.ReadFile(filepath.Join(tmpDir, ".claude", "settings.local.json"))
	if err != nil {
		t.Fatalf("read settings.local.json: %v", err)
	}
	if !strings.Contains(string(data), "--session 42-impl") {
		t.Errorf("settings.local.json missing session name: %s", data)
	}
	if !strings.Contains(string(data), "--issue 42") {
		t.Errorf("settings.local.json missing issue: %s", data)
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

	// settings.local.json should NOT be written when issue is 0
	_, err = os.Stat(filepath.Join(tmpDir, ".claude", "settings.local.json"))
	if err == nil {
		t.Error("settings.local.json should not exist when issue is 0")
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

func TestSend_HappyPath(t *testing.T) {
	tmux := newMockTmux()
	tmux.sessions["test-send"] = true
	d := testDB(t)
	mgr := NewManager(tmux, d, nil)

	d.LogSessionEvent("test-send", 5, "impl", "started", nil, "")

	err := mgr.Send("test-send", "Fix the bug in auth.go")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	// Verify factory_send event logged
	state, _ := d.GetSessionState("test-send")
	if state.Event != "factory_send" {
		t.Errorf("latest event = %q, want %q", state.Event, "factory_send")
	}

	// Verify send-keys was called with the prompt
	found := false
	for _, c := range tmux.calls {
		if strings.Contains(c, "Fix the bug in auth.go") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("prompt not found in tmux calls: %v", tmux.calls)
	}
}

func TestSend_Nonexistent(t *testing.T) {
	tmux := newMockTmux()
	d := testDB(t)
	mgr := NewManager(tmux, d, nil)

	err := mgr.Send("nonexistent", "hello")
	if err == nil {
		t.Fatal("expected error for nonexistent session")
	}
	if !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("error = %q, want 'does not exist'", err.Error())
	}
}

func TestSendFromFile(t *testing.T) {
	tmux := newMockTmux()
	tmux.sessions["test-file"] = true
	d := testDB(t)
	mgr := NewManager(tmux, d, nil)

	d.LogSessionEvent("test-file", 1, "plan", "started", nil, "")

	// Write temp prompt file
	tmpFile := filepath.Join(t.TempDir(), "prompt.md")
	os.WriteFile(tmpFile, []byte("Fix all lint errors\n"), 0o644)

	err := mgr.SendFromFile("test-file", tmpFile)
	if err != nil {
		t.Fatalf("SendFromFile: %v", err)
	}

	found := false
	for _, c := range tmux.calls {
		if strings.Contains(c, "Fix all lint errors") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("file contents not found in tmux calls: %v", tmux.calls)
	}
}

func TestSendFromCheckFailures(t *testing.T) {
	tmux := newMockTmux()
	tmux.sessions["test-fix"] = true
	d := testDB(t)
	mgr := NewManager(tmux, d, nil)

	d.LogSessionEvent("test-fix", 10, "impl", "started", nil, "")

	// Insert failed check runs
	d.LogCheckRun(10, "impl", 1, 0, "lint", false, false, 1, 500, "3 errors found", "src/auth.go:12: unused var")
	d.LogCheckRun(10, "impl", 1, 0, "test", false, false, 1, 2000, "2 tests failed", "TestLogin FAIL")

	err := mgr.SendFromCheckFailures("test-fix", 10, "impl")
	if err != nil {
		t.Fatalf("SendFromCheckFailures: %v", err)
	}

	// Verify factory_send was logged
	state, _ := d.GetSessionState("test-fix")
	if state.Event != "factory_send" {
		t.Errorf("latest event = %q, want %q", state.Event, "factory_send")
	}

	// Verify the prompt was sent containing check names
	found := false
	for _, c := range tmux.calls {
		if strings.Contains(c, "lint") && strings.Contains(c, "test") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("check failure info not found in tmux calls: %v", tmux.calls)
	}
}

func TestSendFromCheckFailures_NoneFound(t *testing.T) {
	tmux := newMockTmux()
	tmux.sessions["test-nofail"] = true
	d := testDB(t)
	mgr := NewManager(tmux, d, nil)

	err := mgr.SendFromCheckFailures("test-nofail", 99, "impl")
	if err == nil {
		t.Fatal("expected error when no failures found")
	}
	if !strings.Contains(err.Error(), "no failed checks") {
		t.Errorf("error = %q, want 'no failed checks'", err.Error())
	}
}

func TestSteer_HappyPath(t *testing.T) {
	tmux := newMockTmux()
	tmux.sessions["test-steer"] = true
	d := testDB(t)
	mgr := NewManager(tmux, d, nil)

	d.LogSessionEvent("test-steer", 5, "impl", "started", nil, "")

	err := mgr.Steer("test-steer", "Focus on auth module")
	if err != nil {
		t.Fatalf("Steer: %v", err)
	}

	// Verify steer event logged (not factory_send)
	state, _ := d.GetSessionState("test-steer")
	if state.Event != "steer" {
		t.Errorf("latest event = %q, want %q", state.Event, "steer")
	}

	// Verify message was sent
	found := false
	for _, c := range tmux.calls {
		if strings.Contains(c, "Focus on auth module") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("steer message not found in tmux calls: %v", tmux.calls)
	}
}

func TestSteer_Nonexistent(t *testing.T) {
	tmux := newMockTmux()
	d := testDB(t)
	mgr := NewManager(tmux, d, nil)

	err := mgr.Steer("nonexistent", "hello")
	if err == nil {
		t.Fatal("expected error for nonexistent session")
	}
}

func TestPeek_HappyPath(t *testing.T) {
	tmux := newMockTmux()
	tmux.sessions["test-peek"] = true
	tmux.captureText = "line 1\nline 2\nline 3\n"
	d := testDB(t)
	mgr := NewManager(tmux, d, nil)

	output, err := mgr.Peek("test-peek", 50)
	if err != nil {
		t.Fatalf("Peek: %v", err)
	}

	if !strings.Contains(output, "line 1") {
		t.Errorf("output = %q, want captured text", output)
	}

	// Verify capture-pane-lines was called with correct line count
	found := false
	for _, c := range tmux.calls {
		if strings.Contains(c, "capture-pane-lines test-peek 50") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("capture-pane-lines not found in tmux calls: %v", tmux.calls)
	}
}

func TestPeek_Nonexistent(t *testing.T) {
	tmux := newMockTmux()
	d := testDB(t)
	mgr := NewManager(tmux, d, nil)

	_, err := mgr.Peek("nonexistent", 50)
	if err == nil {
		t.Fatal("expected error for nonexistent session")
	}
}

func TestBuildFixPrompt(t *testing.T) {
	failures := []db.CheckRun{
		{CheckName: "lint", ExitCode: 1, Summary: "3 errors", Findings: "auth.go:12: unused"},
		{CheckName: "test", ExitCode: 1, Summary: "2 failed", Findings: ""},
	}

	prompt := buildFixPrompt(failures)

	if !strings.Contains(prompt, "lint") {
		t.Error("prompt missing lint check name")
	}
	if !strings.Contains(prompt, "3 errors") {
		t.Error("prompt missing lint summary")
	}
	if !strings.Contains(prompt, "auth.go:12") {
		t.Error("prompt missing lint findings")
	}
	if !strings.Contains(prompt, "test") {
		t.Error("prompt missing test check name")
	}
}

func TestValidateSessionName(t *testing.T) {
	tests := []struct {
		name    string
		wantErr bool
	}{
		{"valid-name", false},
		{"42-impl", false},
		{"my_session.1", false},
		{"", true},
		{"has spaces", true},
		{"semi;colon", true},
		{"pipe|char", true},
		{"back`tick", true},
		{"dollar$var", true},
		{"-starts-with-dash", true},
		{".starts-with-dot", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateSessionName(tt.name)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateSessionName(%q) error = %v, wantErr %v", tt.name, err, tt.wantErr)
			}
		})
	}
}

func TestCreate_InvalidName(t *testing.T) {
	tmux := newMockTmux()
	d := testDB(t)
	mgr := NewManager(tmux, d, nil)

	err := mgr.Create(CreateOpts{
		Name:  "bad;name",
		Issue: 1,
		Stage: "plan",
	})
	if err == nil {
		t.Fatal("expected error for invalid session name")
	}
	if !strings.Contains(err.Error(), "invalid characters") {
		t.Errorf("error = %q, want 'invalid characters'", err.Error())
	}
}

func TestShellQuote(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"/tmp/simple", "'/tmp/simple'"},
		{"/tmp/with spaces", "'/tmp/with spaces'"},
		{"/tmp/it's", "'/tmp/it'\\''s'"},
		{"", "''"},
	}
	for _, tt := range tests {
		got := shellQuote(tt.input)
		if got != tt.want {
			t.Errorf("shellQuote(%q) = %q, want %q", tt.input, got, tt.want)
		}
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
			name: "with model",
			opts: CreateOpts{Model: "claude-opus-4-5"},
			want: "claude --model claude-opus-4-5 --print",
		},
		{
			name: "with model and flags",
			opts: CreateOpts{Model: "claude-sonnet-4-5", Flags: "--verbose"},
			want: "claude --model claude-sonnet-4-5 --verbose --print",
		},
		{
			name: "with flags no model",
			opts: CreateOpts{Flags: "--verbose"},
			want: "claude --verbose --print",
		},
		{
			name: "interactive with model",
			opts: CreateOpts{Model: "claude-opus-4-5", Interactive: true},
			want: "claude --model claude-opus-4-5",
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

func TestWaitIdle_PaneStabilityFallback(t *testing.T) {
	tmux := newMockTmux()
	tmux.sessions["test-pane"] = true
	d := testDB(t)
	mgr := NewManager(tmux, d, nil)

	// Seed a started event so WaitIdle finds the session
	d.LogSessionEvent("test-pane", 1, "impl", "started", nil, "")

	// Return static content from CapturePaneLines — simulating an idle pane
	tmux.captureLinesFn = func() string {
		return "❯ \n────────\n  bypass permissions on"
	}

	result, err := mgr.WaitIdle("test-pane", 5*time.Second, 20*time.Millisecond)
	if err != nil {
		t.Fatalf("WaitIdle: %v", err)
	}
	if result.State != "idle" {
		t.Errorf("state = %q, want %q", result.State, "idle")
	}

	// Verify the fallback logged an idle event with pane_stable metadata
	state, _ := d.GetSessionState("test-pane")
	if state.Event != "idle" {
		t.Errorf("DB event = %q, want %q", state.Event, "idle")
	}
}

func TestWaitIdle_PaneChanging_UsesDBEvent(t *testing.T) {
	tmux := newMockTmux()
	tmux.sessions["test-change"] = true
	d := testDB(t)
	mgr := NewManager(tmux, d, nil)

	d.LogSessionEvent("test-change", 1, "impl", "started", nil, "")

	// Return changing content — simulating an active session
	callCount := 0
	tmux.captureLinesFn = func() string {
		callCount++
		return fmt.Sprintf("Composing… (%ds)", callCount)
	}

	// Fire idle event via DB after a short delay
	go func() {
		time.Sleep(80 * time.Millisecond)
		d.LogSessionEvent("test-change", 1, "impl", "idle", nil, "")
	}()

	result, err := mgr.WaitIdle("test-change", 5*time.Second, 20*time.Millisecond)
	if err != nil {
		t.Fatalf("WaitIdle: %v", err)
	}
	if result.State != "idle" {
		t.Errorf("state = %q, want %q", result.State, "idle")
	}
}
