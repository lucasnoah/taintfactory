package triage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/lucasnoah/taintfactory/internal/db"
	"github.com/lucasnoah/taintfactory/internal/github"
	"github.com/lucasnoah/taintfactory/internal/session"
)

// mockTmux implements session.TmuxRunner for testing.
type mockTmux struct {
	sessions  map[string]bool
	sentKeys  []string
	sentBufs  []string
	listErr   error
	createErr error
}

func newMockTmux() *mockTmux {
	return &mockTmux{sessions: make(map[string]bool)}
}

func (m *mockTmux) NewSession(name string) error {
	if m.createErr != nil {
		return m.createErr
	}
	m.sessions[name] = true
	return nil
}

func (m *mockTmux) SendKeys(session string, keys string) error {
	m.sentKeys = append(m.sentKeys, keys)
	return nil
}

func (m *mockTmux) SendBuffer(session string, content string) error {
	m.sentBufs = append(m.sentBufs, content)
	return nil
}

func (m *mockTmux) KillSession(name string) error {
	delete(m.sessions, name)
	return nil
}

func (m *mockTmux) CapturePane(name string) (string, error) {
	return "", nil
}

func (m *mockTmux) CapturePaneLines(name string, lines int) (string, error) {
	return "", nil
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

// mockGHClient implements GHClient for testing.
type mockGHClient struct {
	issue *github.Issue
	err   error
}

func (m *mockGHClient) GetIssue(number int) (*github.Issue, error) {
	if m.err != nil {
		return nil, m.err
	}
	if m.issue != nil {
		return m.issue, nil
	}
	return &github.Issue{Number: number, Title: "Test Issue", Body: "Test body"}, nil
}

// testConfig returns a two-stage TriageConfig for use in tests.
func testConfig() *TriageConfig {
	return &TriageConfig{
		Triage: TriageMeta{Name: "test", Repo: "owner/test"},
		Stages: []TriageStage{
			{
				ID:      "stale_context",
				Timeout: "10m",
				Outcomes: map[string]string{
					"stale": "done",
					"clean": "already_implemented",
				},
			},
			{
				ID:      "already_implemented",
				Timeout: "15m",
				Outcomes: map[string]string{
					"implemented":     "done",
					"not_implemented": "done",
				},
			},
		},
	}
}

// setupRunner builds a Runner wired to in-memory DB and mockTmux.
// Returns runner, store, database, and the tmux mock.
func setupRunner(t *testing.T) (*Runner, *Store, *db.DB, *mockTmux) {
	t.Helper()

	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := database.Migrate(); err != nil {
		t.Fatalf("migrate db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	tmux := newMockTmux()
	sessions := session.NewManager(tmux, database, nil)

	store := NewStore(t.TempDir())
	cfg := testConfig()
	repoRoot := t.TempDir()

	runner := NewRunner(cfg, store, database, sessions, nil, repoRoot)
	runner.bootWait = 0

	return runner, store, database, tmux
}

// writeOutcomeFile writes a TriageOutcome JSON file into the store's outcome dir.
func writeOutcomeFile(t *testing.T, store *Store, issue int, stageID, outcome, summary string) {
	t.Helper()
	if err := store.EnsureOutcomeDir(issue); err != nil {
		t.Fatalf("EnsureOutcomeDir: %v", err)
	}
	data, _ := json.Marshal(TriageOutcome{Outcome: outcome, Summary: summary})
	if err := os.WriteFile(store.OutcomePath(issue, stageID), data, 0644); err != nil {
		t.Fatalf("write outcome file: %v", err)
	}
}

// TestRunner_Enqueue verifies that Enqueue saves initial state and starts the first stage.
func TestRunner_Enqueue(t *testing.T) {
	runner, store, _, tmux := setupRunner(t)

	if err := runner.Enqueue(42, "Test Issue", "Test body"); err != nil {
		t.Fatalf("Enqueue() error: %v", err)
	}

	st, err := store.Get(42)
	if err != nil {
		t.Fatalf("Get() after Enqueue(): %v", err)
	}

	if st.Status != "in_progress" {
		t.Errorf("Status = %q, want in_progress", st.Status)
	}
	if st.CurrentStage != "stale_context" {
		t.Errorf("CurrentStage = %q, want stale_context", st.CurrentStage)
	}

	wantSession := "triage-42-stale_context"
	if st.CurrentSession != wantSession {
		t.Errorf("CurrentSession = %q, want %q", st.CurrentSession, wantSession)
	}

	if !tmux.sessions[wantSession] {
		t.Errorf("tmux session %q was not created", wantSession)
	}
}

// TestRunner_Advance_SkipsActiveSession verifies that Advance skips sessions that are
// still active (i.e., the last event is neither "idle" nor "exited").
func TestRunner_Advance_SkipsActiveSession(t *testing.T) {
	runner, store, database, _ := setupRunner(t)

	// Save an in_progress state with a current session.
	sessionName := "triage-10-stale_context"
	st := &TriageState{
		Issue:          10,
		Repo:           "owner/test",
		CurrentStage:   "stale_context",
		Status:         "in_progress",
		CurrentSession: sessionName,
		StageHistory:   []TriageStageHistoryEntry{},
	}
	if err := store.Save(st); err != nil {
		t.Fatalf("Save(): %v", err)
	}

	// Log an "active" event — not idle/exited, so the runner should skip.
	if err := database.LogSessionEvent(sessionName, 10, "stale_context", "active", nil, ""); err != nil {
		t.Fatalf("LogSessionEvent: %v", err)
	}

	actions, err := runner.Advance()
	if err != nil {
		t.Fatalf("Advance() error: %v", err)
	}

	if len(actions) != 1 {
		t.Fatalf("len(actions) = %d, want 1", len(actions))
	}
	if actions[0].Action != "skip" {
		t.Errorf("Action = %q, want skip", actions[0].Action)
	}
	if actions[0].Issue != 10 {
		t.Errorf("Issue = %d, want 10", actions[0].Issue)
	}
}

// TestRunner_Advance_IdleSession_RoutesToNextStage verifies that when the session goes idle
// and the outcome maps to another stage, the runner starts that stage.
func TestRunner_Advance_IdleSession_RoutesToNextStage(t *testing.T) {
	repoRoot := t.TempDir()

	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := database.Migrate(); err != nil {
		t.Fatalf("migrate db: %v", err)
	}
	defer database.Close()

	tmux := newMockTmux()
	sessions := session.NewManager(tmux, database, nil)
	store := NewStore(t.TempDir())
	cfg := testConfig()

	gh := &mockGHClient{
		issue: &github.Issue{Number: 5, Title: "Test", Body: "body"},
	}

	runner := NewRunner(cfg, store, database, sessions, gh, repoRoot)
	runner.bootWait = 0

	sessionName := "triage-5-stale_context"

	// Create the session in tmux so that session.Create doesn't complain.
	// But we bypass session.Create here by setting up state manually.
	st := &TriageState{
		Issue:          5,
		Repo:           "owner/test",
		CurrentStage:   "stale_context",
		Status:         "in_progress",
		CurrentSession: sessionName,
		StageHistory:   []TriageStageHistoryEntry{},
	}
	if err := store.Save(st); err != nil {
		t.Fatalf("Save(): %v", err)
	}

	// Log a "started" event so the session exists in DB.
	if err := database.LogSessionEvent(sessionName, 5, "stale_context", "started", nil, ""); err != nil {
		t.Fatalf("LogSessionEvent started: %v", err)
	}
	// Then log an "idle" event.
	if err := database.LogSessionEvent(sessionName, 5, "stale_context", "idle", nil, ""); err != nil {
		t.Fatalf("LogSessionEvent idle: %v", err)
	}

	// Write outcome file: "clean" → routes to "already_implemented".
	writeOutcomeFile(t, store, 5, "stale_context", "clean", "all good")

	// Write the default template into the repo root as triage/already_implemented.md
	// so renderPrompt finds a template for the next stage.
	// (The embedded templates use underscores, and the stage ID is "already_implemented"
	// which matches the embedded template key.)

	actions, err := runner.Advance()
	if err != nil {
		t.Fatalf("Advance() error: %v", err)
	}

	if len(actions) != 1 {
		t.Fatalf("len(actions) = %d, want 1", len(actions))
	}
	if actions[0].Action != "advance" {
		t.Errorf("Action = %q, want advance", actions[0].Action)
	}

	// Re-read state and verify current stage was updated.
	got, err := store.Get(5)
	if err != nil {
		t.Fatalf("Get() after Advance: %v", err)
	}
	if got.CurrentStage != "already_implemented" {
		t.Errorf("CurrentStage = %q, want already_implemented", got.CurrentStage)
	}
	if len(got.StageHistory) != 1 {
		t.Fatalf("len(StageHistory) = %d, want 1", len(got.StageHistory))
	}
	if got.StageHistory[0].Stage != "stale_context" {
		t.Errorf("StageHistory[0].Stage = %q, want stale_context", got.StageHistory[0].Stage)
	}
	if got.StageHistory[0].Outcome != "clean" {
		t.Errorf("StageHistory[0].Outcome = %q, want clean", got.StageHistory[0].Outcome)
	}
}

// TestRunner_Advance_DoneOutcome_MarksCompleted verifies that when the outcome maps to "done",
// the triage state is marked as completed.
func TestRunner_Advance_DoneOutcome_MarksCompleted(t *testing.T) {
	runner, store, database, _ := setupRunner(t)

	sessionName := "triage-7-stale_context"

	st := &TriageState{
		Issue:          7,
		Repo:           "owner/test",
		CurrentStage:   "stale_context",
		Status:         "in_progress",
		CurrentSession: sessionName,
		StageHistory:   []TriageStageHistoryEntry{},
	}
	if err := store.Save(st); err != nil {
		t.Fatalf("Save(): %v", err)
	}

	// Log started then idle so the session has an idle state.
	if err := database.LogSessionEvent(sessionName, 7, "stale_context", "started", nil, ""); err != nil {
		t.Fatalf("LogSessionEvent started: %v", err)
	}
	if err := database.LogSessionEvent(sessionName, 7, "stale_context", "idle", nil, ""); err != nil {
		t.Fatalf("LogSessionEvent idle: %v", err)
	}

	// Write outcome "stale" which maps to "done" → should mark completed.
	writeOutcomeFile(t, store, 7, "stale_context", "stale", "context is stale")

	actions, err := runner.Advance()
	if err != nil {
		t.Fatalf("Advance() error: %v", err)
	}

	if len(actions) != 1 {
		t.Fatalf("len(actions) = %d, want 1", len(actions))
	}
	if actions[0].Action != "completed" {
		t.Errorf("Action = %q, want completed", actions[0].Action)
	}

	got, err := store.Get(7)
	if err != nil {
		t.Fatalf("Get() after Advance: %v", err)
	}
	if got.Status != "completed" {
		t.Errorf("Status = %q, want completed", got.Status)
	}
	if len(got.StageHistory) != 1 {
		t.Fatalf("len(StageHistory) = %d, want 1", len(got.StageHistory))
	}
	if got.StageHistory[0].Outcome != "stale" {
		t.Errorf("StageHistory[0].Outcome = %q, want stale", got.StageHistory[0].Outcome)
	}
}

// TestRunner_FirstStageID verifies FirstStageID returns the first stage's ID.
func TestRunner_FirstStageID(t *testing.T) {
	runner, _, _, _ := setupRunner(t)
	if got := runner.FirstStageID(); got != "stale_context" {
		t.Errorf("FirstStageID() = %q, want stale_context", got)
	}
}

// TestRunner_renderPrompt_usesEmbeddedTemplate verifies that renderPrompt uses
// the embedded template when no repo-local override exists.
func TestRunner_renderPrompt_usesEmbeddedTemplate(t *testing.T) {
	runner, store, _, _ := setupRunner(t)

	if err := store.EnsureOutcomeDir(1); err != nil {
		t.Fatal(err)
	}
	outPath := store.OutcomePath(1, "stale_context")

	stageCfg := runner.cfg.StageByID("stale_context")
	prompt, err := runner.renderPrompt(1, stageCfg, "My Title", "My body", outPath)
	if err != nil {
		t.Fatalf("renderPrompt() error: %v", err)
	}
	if len(prompt) == 0 {
		t.Error("renderPrompt() returned empty string")
	}
	// Should contain the issue number.
	if !containsStr(prompt, "1") {
		t.Errorf("prompt does not contain issue number; got: %q", prompt[:100])
	}
}

// TestRunner_renderPrompt_usesRepoOverride verifies that a file at triage/{stageID}.md
// takes precedence over the embedded template.
func TestRunner_renderPrompt_usesRepoOverride(t *testing.T) {
	repoRoot := t.TempDir()
	triageDir := filepath.Join(repoRoot, "triage")
	if err := os.MkdirAll(triageDir, 0755); err != nil {
		t.Fatal(err)
	}
	overridePath := filepath.Join(triageDir, "stale_context.md")
	if err := os.WriteFile(overridePath, []byte("custom prompt for {{.issue_number}}"), 0644); err != nil {
		t.Fatal(err)
	}

	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := database.Migrate(); err != nil {
		t.Fatalf("migrate db: %v", err)
	}
	defer database.Close()

	tmux := newMockTmux()
	sessions := session.NewManager(tmux, database, nil)
	store := NewStore(t.TempDir())
	cfg := testConfig()

	runner := NewRunner(cfg, store, database, sessions, nil, repoRoot)
	runner.bootWait = 0

	outPath := store.OutcomePath(99, "stale_context")
	stageCfg := cfg.StageByID("stale_context")
	prompt, err := runner.renderPrompt(99, stageCfg, "title", "body", outPath)
	if err != nil {
		t.Fatalf("renderPrompt() error: %v", err)
	}
	if prompt != "custom prompt for 99" {
		t.Errorf("renderPrompt() = %q, want %q", prompt, "custom prompt for 99")
	}
}

func containsStr(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && stringContains(s, sub))
}

func stringContains(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
