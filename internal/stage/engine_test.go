package stage

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lucasnoah/taintfactory/internal/checks"
	"github.com/lucasnoah/taintfactory/internal/config"
	appctx "github.com/lucasnoah/taintfactory/internal/context"
	"github.com/lucasnoah/taintfactory/internal/db"
	"github.com/lucasnoah/taintfactory/internal/github"
	"github.com/lucasnoah/taintfactory/internal/pipeline"
	"github.com/lucasnoah/taintfactory/internal/session"
)

// --- Mock TmuxRunner ---

type mockTmux struct {
	sessions     map[string]bool
	sent         []tmuxSend
	captureCount int // incremented on each CapturePaneLines call for unique content
}

type tmuxSend struct {
	Session string
	Keys    string
}

func newMockTmux() *mockTmux {
	return &mockTmux{sessions: make(map[string]bool)}
}

func (m *mockTmux) NewSession(name string) error {
	m.sessions[name] = true
	return nil
}

func (m *mockTmux) SendKeys(sess string, keys string) error {
	if !m.sessions[sess] {
		return fmt.Errorf("session %q not found", sess)
	}
	m.sent = append(m.sent, tmuxSend{Session: sess, Keys: keys})
	return nil
}

func (m *mockTmux) SendBuffer(sess string, content string) error {
	if !m.sessions[sess] {
		return fmt.Errorf("session %q not found", sess)
	}
	m.sent = append(m.sent, tmuxSend{Session: sess, Keys: content})
	return nil
}

func (m *mockTmux) KillSession(name string) error {
	if !m.sessions[name] {
		return fmt.Errorf("session %q not found", name)
	}
	delete(m.sessions, name)
	return nil
}

func (m *mockTmux) CapturePane(name string) (string, error) {
	return "captured output", nil
}

func (m *mockTmux) CapturePaneLines(name string, lines int) (string, error) {
	m.captureCount++
	return fmt.Sprintf("captured lines %d", m.captureCount), nil
}

func (m *mockTmux) ListSessions() ([]string, error) {
	var names []string
	for n := range m.sessions {
		names = append(names, n)
	}
	return names, nil
}

func (m *mockTmux) HasSession(name string) (bool, error) {
	return m.sessions[name], nil
}

// --- Mock CommandRunner for checks ---

type mockCheckCmd struct {
	commands []string
	results  []cmdResult
	idx      int
}

type cmdResult struct {
	stdout   string
	stderr   string
	exitCode int
	err      error
}

func (m *mockCheckCmd) Run(ctx context.Context, dir string, command string) (string, string, int, error) {
	m.commands = append(m.commands, command)
	if m.idx >= len(m.results) {
		return "", "", 0, nil // default: pass
	}
	r := m.results[m.idx]
	m.idx++
	return r.stdout, r.stderr, r.exitCode, r.err
}

// --- Mock GitRunner for context.Builder ---

type mockGit struct{}

func (m *mockGit) Diff(dir string) (string, error)        { return "", nil }
func (m *mockGit) DiffSummary(dir string) (string, error)  { return "", nil }
func (m *mockGit) FilesChanged(dir string) (string, error) { return "", nil }
func (m *mockGit) Log(dir string) (string, error)          { return "", nil }

// --- Test helpers ---

// setupEngine creates a test Engine with all mocked dependencies.
// The mockCheckCmd controls what checks return.
// After session creation, the test must manually log an "idle" event to satisfy WaitIdle.
func setupEngine(t *testing.T, cfg *config.PipelineConfig, checkCmd *mockCheckCmd) (*Engine, *mockTmux, *db.DB, *pipeline.Store) {
	t.Helper()

	// Pipeline store in temp dir
	tmpDir := t.TempDir()
	store := pipeline.NewStore(tmpDir)

	// In-memory SQLite
	dbPath := filepath.Join(t.TempDir(), "test.db")
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := database.Migrate(); err != nil {
		t.Fatalf("migrate db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	// Mock tmux
	tmux := newMockTmux()

	// Session manager
	sessions := session.NewManager(tmux, database, store)

	// Checks runner with mock command runner
	checker := checks.NewRunner(checkCmd)

	// Context builder (with mock git)
	builder := appctx.NewBuilder(store, &mockGit{})

	engine := NewEngine(sessions, checker, builder, store, database, cfg)
	engine.SetPollInterval(50 * time.Millisecond) // fast polling for tests
	engine.SetBootDelay(0)                         // no boot delay in tests
	return engine, tmux, database, store
}

// createTestPipeline creates a pipeline state for testing.
func createTestPipeline(t *testing.T, store *pipeline.Store, issue int) {
	t.Helper()
	worktreeDir := t.TempDir()
	if _, err := store.Create(pipeline.CreateOpts{Issue: issue, Title: "Test Issue", Branch: "feature/test", Worktree: worktreeDir, FirstStage: "impl", GoalGates: nil}); err != nil {
		t.Fatalf("create pipeline: %v", err)
	}
	if err := store.Update(issue, func(ps *pipeline.PipelineState) {
		ps.CurrentAttempt = 1
	}); err != nil {
		t.Fatalf("update pipeline: %v", err)
	}
}

// installTemplate writes a template file into the worktree directory.
// LoadTemplate looks at <workdir>/<templatePath> first.
func installTemplate(t *testing.T, store *pipeline.Store, issue int, name string, content string) {
	t.Helper()
	ps, err := store.Get(issue)
	if err != nil {
		t.Fatalf("get pipeline: %v", err)
	}
	path := filepath.Join(ps.Worktree, name)
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write template: %v", err)
	}
}

// simulateIdleAfterCreate logs an idle event after the session is created,
// to satisfy WaitIdle's polling loop.
func simulateIdleAfterCreate(t *testing.T, database *db.DB, sessionName string, issue int, stage string) {
	t.Helper()
	if err := database.LogSessionEvent(sessionName, issue, stage, "idle", nil, ""); err != nil {
		t.Fatalf("log idle event: %v", err)
	}
}

// waitForEvent polls until the session's latest event matches the target, or times out.
func waitForEvent(database *db.DB, sessionName string, target string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		state, _ := database.GetSessionState(sessionName)
		if state != nil && state.Event == target {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

// simulateWorkIdle waits for factory_send then fires an idle event
// to simulate Claude finishing work. With bootDelay=0 in tests, no boot idle is needed.
func simulateWorkIdle(t *testing.T, database *db.DB, sessionName string, issue int, stage string) {
	t.Helper()
	if !waitForEvent(database, sessionName, "factory_send", 2*time.Second) {
		t.Log("warning: factory_send not observed, firing idle anyway")
	}
	time.Sleep(10 * time.Millisecond)
	simulateIdleAfterCreate(t, database, sessionName, issue, stage)
}

// --- Tests ---

func testConfig(stages []config.Stage, checksMap map[string]config.Check) *config.PipelineConfig {
	if checksMap == nil {
		checksMap = make(map[string]config.Check)
	}
	return &config.PipelineConfig{
		Pipeline: config.Pipeline{
			MaxFixRounds:      3,
			FreshSessionAfter: 2,
			Defaults: config.StageDefaults{
				Flags: "--dangerously-skip-permissions",
			},
			Checks: checksMap,
			Stages: stages,
		},
	}
}

func TestFindStageConfig(t *testing.T) {
	cfg := testConfig([]config.Stage{
		{ID: "impl", Type: "agent"},
		{ID: "review", Type: "agent"},
	}, nil)

	engine := &Engine{cfg: cfg}

	stage, err := engine.findStageConfig("impl", cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stage.ID != "impl" {
		t.Errorf("expected impl, got %q", stage.ID)
	}

	_, err = engine.findStageConfig("nonexistent", cfg)
	if err == nil {
		t.Fatal("expected error for nonexistent stage")
	}
}

func TestResolvePostChecks(t *testing.T) {
	engine := &Engine{}

	// With checks_after and extra_checks, deduped
	stage := &config.Stage{
		ChecksAfter: []string{"lint", "test"},
		ExtraChecks: []string{"test", "typecheck"},
	}
	checks := engine.resolvePostChecks(stage)
	if len(checks) != 3 {
		t.Fatalf("expected 3 checks, got %d: %v", len(checks), checks)
	}
	expected := []string{"lint", "test", "typecheck"}
	for i, c := range checks {
		if c != expected[i] {
			t.Errorf("check[%d]: expected %q, got %q", i, expected[i], c)
		}
	}

	// With skip_checks
	skipStage := &config.Stage{
		SkipChecks:  true,
		ChecksAfter: []string{"lint"},
	}
	if len(engine.resolvePostChecks(skipStage)) != 0 {
		t.Error("expected empty checks when skip_checks is true")
	}

	// Empty
	emptyStage := &config.Stage{}
	if len(engine.resolvePostChecks(emptyStage)) != 0 {
		t.Error("expected empty checks for empty stage")
	}
}

func TestFormatGateFailures(t *testing.T) {
	gate := &checks.GateResult{
		RemainingFailures: map[string]checks.GateFailure{
			"lint": {Summary: "3 errors"},
		},
	}

	result := formatGateFailures(gate)
	if !strings.Contains(result, "lint") || !strings.Contains(result, "3 errors") {
		t.Errorf("unexpected format: %q", result)
	}

	// Empty failures
	emptyGate := &checks.GateResult{
		RemainingFailures: map[string]checks.GateFailure{},
	}
	if formatGateFailures(emptyGate) != "checks failed" {
		t.Errorf("expected default message for empty failures")
	}
}

func TestRunChecksOnly_Pass(t *testing.T) {
	checkCmd := &mockCheckCmd{
		results: []cmdResult{
			{exitCode: 0}, // lint passes
		},
	}

	cfg := testConfig(
		[]config.Stage{{ID: "validate", Type: "checks_only", Checks: []string{"lint"}}},
		map[string]config.Check{"lint": {Command: "echo ok", Parser: "generic"}},
	)

	engine, _, _, store := setupEngine(t, cfg, checkCmd)
	createTestPipeline(t, store, 1)

	result, err := engine.Run(RunOpts{Issue: 1, Stage: "validate", Timeout: 10 * time.Second})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Outcome != "success" {
		t.Errorf("expected success, got %q", result.Outcome)
	}
	if !result.ChecksFirstPass {
		t.Error("expected checks_first_pass = true")
	}
	if result.FinalCheckState["lint"] != "pass" {
		t.Errorf("expected lint=pass, got %q", result.FinalCheckState["lint"])
	}
}

func TestRunChecksOnly_Fail(t *testing.T) {
	checkCmd := &mockCheckCmd{
		results: []cmdResult{
			{exitCode: 1, stdout: "errors found"}, // lint fails
		},
	}

	cfg := testConfig(
		[]config.Stage{{ID: "validate", Type: "checks_only", Checks: []string{"lint"}}},
		map[string]config.Check{"lint": {Command: "lint cmd", Parser: "generic"}},
	)

	engine, _, _, store := setupEngine(t, cfg, checkCmd)
	createTestPipeline(t, store, 1)

	result, err := engine.Run(RunOpts{Issue: 1, Stage: "validate", Timeout: 10 * time.Second})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Outcome != "fail" {
		t.Errorf("expected fail, got %q", result.Outcome)
	}
	if result.FinalCheckState["lint"] != "fail" {
		t.Errorf("expected lint=fail, got %q", result.FinalCheckState["lint"])
	}
}

func TestRunChecksOnly_NoChecks(t *testing.T) {
	cfg := testConfig(
		[]config.Stage{{ID: "validate", Type: "checks_only"}},
		nil,
	)

	engine, _, _, store := setupEngine(t, cfg, &mockCheckCmd{})
	createTestPipeline(t, store, 1)

	result, err := engine.Run(RunOpts{Issue: 1, Stage: "validate", Timeout: 10 * time.Second})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Outcome != "success" {
		t.Errorf("expected success for empty checks, got %q", result.Outcome)
	}
}

func TestRunAgent_NoChecks_Success(t *testing.T) {
	checkCmd := &mockCheckCmd{}

	cfg := testConfig(
		[]config.Stage{{ID: "impl", Type: "agent", SkipChecks: true, PromptTemplate: "impl.md"}},
		nil,
	)

	engine, _, database, store := setupEngine(t, cfg, checkCmd)
	createTestPipeline(t, store, 1)
	installTemplate(t, store, 1, "impl.md", "Implement issue {{issue_number}}: {{issue_title}}")

	// Simulate idle events: boot idle then work-complete idle (after factory_send).
	go func() {
		time.Sleep(50 * time.Millisecond)
		simulateWorkIdle(t, database, "1-impl-1", 1, "impl")
	}()

	result, err := engine.Run(RunOpts{Issue: 1, Stage: "impl", Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Outcome != "success" {
		t.Errorf("expected success, got %q", result.Outcome)
	}
	if !result.ChecksFirstPass {
		t.Error("expected checks_first_pass = true when no checks")
	}
	if result.AgentDuration == 0 {
		t.Error("expected non-zero agent duration")
	}
}

func TestRunAgent_ChecksPass(t *testing.T) {
	checkCmd := &mockCheckCmd{
		results: []cmdResult{
			{exitCode: 0}, // lint passes
		},
	}

	cfg := testConfig(
		[]config.Stage{{ID: "impl", Type: "agent", ChecksAfter: []string{"lint"}, PromptTemplate: "impl.md"}},
		map[string]config.Check{"lint": {Command: "lint cmd", Parser: "generic"}},
	)

	engine, _, database, store := setupEngine(t, cfg, checkCmd)
	createTestPipeline(t, store, 1)
	installTemplate(t, store, 1, "impl.md", "Implement {{issue_number}}")

	go func() {
		time.Sleep(50 * time.Millisecond)
		simulateWorkIdle(t, database, "1-impl-1", 1, "impl")
	}()

	result, err := engine.Run(RunOpts{Issue: 1, Stage: "impl", Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Outcome != "success" {
		t.Errorf("expected success, got %q", result.Outcome)
	}
	if !result.ChecksFirstPass {
		t.Error("expected checks_first_pass = true")
	}
}

func TestRunAgent_ChecksFail_FixLoop_Success(t *testing.T) {
	checkCmd := &mockCheckCmd{
		results: []cmdResult{
			{exitCode: 1, stdout: "lint errors"},  // first check: fail
			{exitCode: 0},                          // fix round 1 re-check: pass
		},
	}

	cfg := testConfig(
		[]config.Stage{{ID: "impl", Type: "agent", ChecksAfter: []string{"lint"}, PromptTemplate: "impl.md"}},
		map[string]config.Check{"lint": {Command: "lint cmd", Parser: "generic"}},
	)

	engine, _, database, store := setupEngine(t, cfg, checkCmd)
	createTestPipeline(t, store, 1)
	installTemplate(t, store, 1, "impl.md", "Implement {{issue_number}}")

	// Simulate idle events: boot idle, work idle, fix round idle
	go func() {
		time.Sleep(50 * time.Millisecond)
		simulateWorkIdle(t, database, "1-impl-1", 1, "impl")
		// Fix round: wait for the next factory_send then fire idle
		if !waitForEvent(database, "1-impl-1", "factory_send", 2*time.Second) {
			t.Log("warning: factory_send not observed for fix round")
		}
		time.Sleep(10 * time.Millisecond)
		_ = database.LogSessionEvent("1-impl-1", 1, "impl", "idle", nil, "")
	}()

	result, err := engine.Run(RunOpts{Issue: 1, Stage: "impl", Timeout: 10 * time.Second})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Outcome != "success" {
		t.Errorf("expected success after fix, got %q", result.Outcome)
	}
	if result.ChecksFirstPass {
		t.Error("expected checks_first_pass = false when fix loop was needed")
	}
	if result.FixRounds != 1 {
		t.Errorf("expected 1 fix round, got %d", result.FixRounds)
	}
}

func TestRunAgent_FixLoop_Exhausted(t *testing.T) {
	// All checks fail every time
	checkCmd := &mockCheckCmd{
		results: []cmdResult{
			{exitCode: 1, stdout: "errors"},   // initial check
			{exitCode: 1, stdout: "errors"},   // fix round 1
			{exitCode: 1, stdout: "errors"},   // fix round 2
		},
	}

	cfg := testConfig(
		[]config.Stage{{ID: "impl", Type: "agent", ChecksAfter: []string{"lint"}, PromptTemplate: "impl.md"}},
		map[string]config.Check{"lint": {Command: "lint cmd", Parser: "generic"}},
	)
	cfg.Pipeline.MaxFixRounds = 2
	cfg.Pipeline.FreshSessionAfter = 10 // don't trigger fresh sessions in this test

	engine, _, database, store := setupEngine(t, cfg, checkCmd)
	createTestPipeline(t, store, 1)
	installTemplate(t, store, 1, "impl.md", "Implement {{issue_number}}")

	// Simulate idle events: boot idle, work idle, then fix round idles
	go func() {
		time.Sleep(50 * time.Millisecond)
		simulateWorkIdle(t, database, "1-impl-1", 1, "impl")
		for i := 0; i < 2; i++ {
			if !waitForEvent(database, "1-impl-1", "factory_send", 2*time.Second) {
				t.Logf("warning: factory_send not observed for fix round %d", i+1)
			}
			time.Sleep(10 * time.Millisecond)
			_ = database.LogSessionEvent("1-impl-1", 1, "impl", "idle", nil, "")
		}
	}()

	result, err := engine.Run(RunOpts{Issue: 1, Stage: "impl", Timeout: 10 * time.Second})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Outcome != "fail" {
		t.Errorf("expected fail, got %q", result.Outcome)
	}
	if result.FixRounds != 2 {
		t.Errorf("expected 2 fix rounds, got %d", result.FixRounds)
	}
}

func TestRunAgent_FreshSession(t *testing.T) {
	// Checks fail initially and on round 1, then pass on round 2 (fresh session)
	checkCmd := &mockCheckCmd{
		results: []cmdResult{
			{exitCode: 1, stdout: "errors"},   // initial check
			{exitCode: 1, stdout: "errors"},   // fix round 1
			{exitCode: 0},                      // fix round 2 (fresh session): pass
		},
	}

	cfg := testConfig(
		[]config.Stage{{ID: "impl", Type: "agent", ChecksAfter: []string{"lint"}, PromptTemplate: "impl.md"}},
		map[string]config.Check{"lint": {Command: "lint cmd", Parser: "generic"}},
	)
	cfg.Pipeline.MaxFixRounds = 3
	cfg.Pipeline.FreshSessionAfter = 1 // fresh session after round 1

	engine, _, database, store := setupEngine(t, cfg, checkCmd)
	createTestPipeline(t, store, 1)
	installTemplate(t, store, 1, "impl.md", "Implement {{issue_number}}")
	installTemplate(t, store, 1, "fix-checks.md", "Fix these: {{check_failures}}")

	// Simulate idle events using polling to avoid timing races
	go func() {
		time.Sleep(50 * time.Millisecond)
		// Initial session: boot idle + work idle
		simulateWorkIdle(t, database, "1-impl-1", 1, "impl")
		// Fix round 1 (existing session): wait for factory_send, then idle
		if !waitForEvent(database, "1-impl-1", "factory_send", 2*time.Second) {
			t.Log("warning: factory_send not observed for fix round 1")
		}
		time.Sleep(10 * time.Millisecond)
		_ = database.LogSessionEvent("1-impl-1", 1, "impl", "idle", nil, "")
		// Fix round 2 (fresh session "1-impl-1-fix-2"): boot idle + work idle
		// Wait for new session to be created
		time.Sleep(200 * time.Millisecond)
		simulateWorkIdle(t, database, "1-impl-1-fix-2", 1, "impl")
	}()

	result, err := engine.Run(RunOpts{Issue: 1, Stage: "impl", Timeout: 10 * time.Second})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Outcome != "success" {
		t.Errorf("expected success after fresh session fix, got %q", result.Outcome)
	}
	if result.FixRounds != 2 {
		t.Errorf("expected 2 fix rounds, got %d", result.FixRounds)
	}
}

func TestRunAgent_ChecksBefore_Fail(t *testing.T) {
	checkCmd := &mockCheckCmd{
		results: []cmdResult{
			{exitCode: 1, stdout: "pre-check failed"}, // checks_before fails
		},
	}

	cfg := testConfig(
		[]config.Stage{{ID: "impl", Type: "agent", ChecksBefore: []string{"precheck"}, PromptTemplate: "impl.md"}},
		map[string]config.Check{"precheck": {Command: "precheck cmd", Parser: "generic"}},
	)

	engine, _, _, store := setupEngine(t, cfg, checkCmd)
	createTestPipeline(t, store, 1)
	installTemplate(t, store, 1, "impl.md", "Implement {{issue_number}}")

	result, err := engine.Run(RunOpts{Issue: 1, Stage: "impl", Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Outcome != "fail" {
		t.Errorf("expected fail from checks_before, got %q", result.Outcome)
	}
}

func TestRunAgent_ChecksBefore_Pass(t *testing.T) {
	checkCmd := &mockCheckCmd{
		results: []cmdResult{
			{exitCode: 0}, // checks_before passes
			{exitCode: 0}, // post-check passes
		},
	}

	cfg := testConfig(
		[]config.Stage{{ID: "impl", Type: "agent", ChecksBefore: []string{"precheck"}, ChecksAfter: []string{"lint"}, PromptTemplate: "impl.md"}},
		map[string]config.Check{
			"precheck": {Command: "precheck cmd", Parser: "generic"},
			"lint":     {Command: "lint cmd", Parser: "generic"},
		},
	)

	engine, _, database, store := setupEngine(t, cfg, checkCmd)
	createTestPipeline(t, store, 1)
	installTemplate(t, store, 1, "impl.md", "Implement {{issue_number}}")

	go func() {
		time.Sleep(50 * time.Millisecond)
		simulateWorkIdle(t, database, "1-impl-1", 1, "impl")
	}()

	result, err := engine.Run(RunOpts{Issue: 1, Stage: "impl", Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Outcome != "success" {
		t.Errorf("expected success, got %q", result.Outcome)
	}
}

func TestRun_StageNotFound(t *testing.T) {
	cfg := testConfig([]config.Stage{{ID: "impl"}}, nil)

	engine, _, _, store := setupEngine(t, cfg, &mockCheckCmd{})
	createTestPipeline(t, store, 1)

	_, err := engine.Run(RunOpts{Issue: 1, Stage: "nonexistent", Timeout: 5 * time.Second})
	if err == nil {
		t.Fatal("expected error for nonexistent stage")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'not found' in error, got %q", err.Error())
	}
}

func TestRun_PipelineNotFound(t *testing.T) {
	cfg := testConfig([]config.Stage{{ID: "impl"}}, nil)

	engine, _, _, _ := setupEngine(t, cfg, &mockCheckCmd{})

	_, err := engine.Run(RunOpts{Issue: 999, Stage: "impl", Timeout: 5 * time.Second})
	if err == nil {
		t.Fatal("expected error for nonexistent pipeline")
	}
	if !strings.Contains(err.Error(), "pipeline state") {
		t.Errorf("expected pipeline state error, got %q", err.Error())
	}
}

func TestRunGate_UndefinedCheck(t *testing.T) {
	cfg := testConfig(
		[]config.Stage{{ID: "validate", Type: "checks_only", Checks: []string{"nonexistent"}}},
		nil,
	)

	engine, _, _, store := setupEngine(t, cfg, &mockCheckCmd{})
	createTestPipeline(t, store, 1)

	_, err := engine.Run(RunOpts{Issue: 1, Stage: "validate", Timeout: 5 * time.Second})
	if err == nil {
		t.Fatal("expected error for undefined check")
	}
	if !strings.Contains(err.Error(), "not defined") {
		t.Errorf("expected 'not defined' in error, got %q", err.Error())
	}
}

func TestRunResult_Fields(t *testing.T) {
	checkCmd := &mockCheckCmd{
		results: []cmdResult{
			{exitCode: 0}, // lint passes
		},
	}

	cfg := testConfig(
		[]config.Stage{{ID: "validate", Type: "checks_only", Checks: []string{"lint"}}},
		map[string]config.Check{"lint": {Command: "echo ok", Parser: "generic"}},
	)

	engine, _, _, store := setupEngine(t, cfg, checkCmd)
	createTestPipeline(t, store, 1)

	result, err := engine.Run(RunOpts{Issue: 1, Stage: "validate", Timeout: 10 * time.Second})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Issue != 1 {
		t.Errorf("expected issue 1, got %d", result.Issue)
	}
	if result.Stage != "validate" {
		t.Errorf("expected stage 'validate', got %q", result.Stage)
	}
	if result.Attempt != 1 {
		t.Errorf("expected attempt 1, got %d", result.Attempt)
	}
	if result.TotalDuration == 0 {
		t.Error("expected non-zero total duration")
	}
}

func TestRunAgent_IssueBodyInPrompt(t *testing.T) {
	checkCmd := &mockCheckCmd{}

	cfg := testConfig(
		[]config.Stage{{ID: "impl", Type: "agent", SkipChecks: true, PromptTemplate: "impl.md"}},
		nil,
	)

	engine, tmuxMock, database, store := setupEngine(t, cfg, checkCmd)
	createTestPipeline(t, store, 1)
	installTemplate(t, store, 1, "impl.md", "Issue: {{issue_title}}\n\n{{issue_body}}")

	// Write a cached issue.json into the pipeline directory
	pipelineDir := fmt.Sprintf("%s/%d", store.BaseDir(), 1)
	issue := &github.Issue{
		Number: 1,
		Title:  "Test Issue",
		Body:   "This is the detailed issue description.\n\n## Acceptance Criteria\n- [ ] Thing works",
	}
	data, _ := json.Marshal(issue)
	os.WriteFile(filepath.Join(pipelineDir, "issue.json"), data, 0o644)

	go func() {
		time.Sleep(50 * time.Millisecond)
		simulateWorkIdle(t, database, "1-impl-1", 1, "impl")
	}()

	result, err := engine.Run(RunOpts{Issue: 1, Stage: "impl", Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Outcome != "success" {
		t.Errorf("expected success, got %q", result.Outcome)
	}

	// Verify the prompt was sent with the issue body
	_ = tmuxMock // tmux mock received the prompt via SendBuffer
	savedPrompt, err := store.GetPrompt(1, "impl", 1)
	if err != nil {
		t.Fatalf("get prompt: %v", err)
	}
	if !strings.Contains(savedPrompt, "detailed issue description") {
		t.Errorf("prompt should contain issue body, got:\n%s", savedPrompt)
	}
	if !strings.Contains(savedPrompt, "Acceptance Criteria") {
		t.Errorf("prompt should contain acceptance criteria, got:\n%s", savedPrompt)
	}
}

func TestCfgFor_ReturnsOptsConfigWhenSet(t *testing.T) {
	defaultCfg := testConfig([]config.Stage{{ID: "impl", Type: "agent"}}, nil)
	defaultCfg.Pipeline.MaxFixRounds = 1

	overrideCfg := testConfig([]config.Stage{{ID: "impl", Type: "agent"}}, nil)
	overrideCfg.Pipeline.MaxFixRounds = 7

	engine, _, _, _ := setupEngine(t, defaultCfg, &mockCheckCmd{})

	// With Config set in opts, cfgFor should return that config
	opts := RunOpts{Config: overrideCfg}
	got := engine.cfgFor(opts)
	if got.Pipeline.MaxFixRounds != 7 {
		t.Errorf("cfgFor with Config override: MaxFixRounds = %d, want 7", got.Pipeline.MaxFixRounds)
	}

	// Without Config set, cfgFor should fall back to engine default
	opts2 := RunOpts{}
	got2 := engine.cfgFor(opts2)
	if got2.Pipeline.MaxFixRounds != 1 {
		t.Errorf("cfgFor without override: MaxFixRounds = %d, want 1", got2.Pipeline.MaxFixRounds)
	}
}
