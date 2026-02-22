package orchestrator

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
	"github.com/lucasnoah/taintfactory/internal/stage"
	"github.com/lucasnoah/taintfactory/internal/worktree"
)

// --- Mocks ---

type mockGhCmd struct {
	calls   [][]string
	results []mockCmdResult
	idx     int
}

type mockCmdResult struct {
	output string
	err    error
}

func (m *mockGhCmd) Run(args ...string) (string, error) {
	m.calls = append(m.calls, args)
	if m.idx >= len(m.results) {
		return "", nil
	}
	r := m.results[m.idx]
	m.idx++
	return r.output, r.err
}

func (m *mockGhCmd) RunGit(dir string, args ...string) (string, error) {
	return m.Run(args...)
}

type mockGitRunner struct{}

func (m *mockGitRunner) Run(dir string, args ...string) (string, error) {
	return "", nil
}

type mockTmux struct {
	sessions map[string]bool
}

func newMockTmux() *mockTmux {
	return &mockTmux{sessions: make(map[string]bool)}
}

func (m *mockTmux) NewSession(name string) error {
	m.sessions[name] = true
	return nil
}

func (m *mockTmux) SendKeys(sess string, keys string) error    { return nil }
func (m *mockTmux) SendBuffer(sess string, content string) error { return nil }
func (m *mockTmux) KillSession(name string) error            { delete(m.sessions, name); return nil }
func (m *mockTmux) CapturePane(name string) (string, error)  { return "", nil }
func (m *mockTmux) CapturePaneLines(name string, lines int) (string, error) { return "", nil }
func (m *mockTmux) ListSessions() ([]string, error) {
	var names []string
	for n := range m.sessions {
		names = append(names, n)
	}
	return names, nil
}
func (m *mockTmux) HasSession(name string) (bool, error) { return m.sessions[name], nil }

type mockCheckCmd struct {
	results []cmdResult
	idx     int
}

type cmdResult struct {
	exitCode int
}

func (m *mockCheckCmd) Run(ctx context.Context, dir string, command string) (string, string, int, error) {
	if m.idx >= len(m.results) {
		return "", "", 0, nil
	}
	r := m.results[m.idx]
	m.idx++
	return "", "", r.exitCode, nil
}

type mockContextGit struct{}

func (m *mockContextGit) Diff(dir string) (string, error)        { return "", nil }
func (m *mockContextGit) DiffSummary(dir string) (string, error)  { return "", nil }
func (m *mockContextGit) FilesChanged(dir string) (string, error) { return "", nil }
func (m *mockContextGit) Log(dir string) (string, error)          { return "", nil }

// --- Test helpers ---

type testEnv struct {
	orch     *Orchestrator
	store    *pipeline.Store
	database *db.DB
	ghCmd    *mockGhCmd
	tmux     *mockTmux
	checkCmd *mockCheckCmd
}

func setupTest(t *testing.T, cfg *config.PipelineConfig) *testEnv {
	t.Helper()

	tmpDir := t.TempDir()
	store := pipeline.NewStore(filepath.Join(tmpDir, "pipelines"))

	dbPath := filepath.Join(tmpDir, "test.db")
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := database.Migrate(); err != nil {
		t.Fatalf("migrate db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	ghCmd := &mockGhCmd{}
	ghClient := github.NewClient(ghCmd)

	gitRunner := &mockGitRunner{}
	wtDir := filepath.Join(tmpDir, "worktrees")
	os.MkdirAll(wtDir, 0o755)
	wt := worktree.NewManager(gitRunner, tmpDir, wtDir)

	tmux := newMockTmux()
	sessions := session.NewManager(tmux, database, store)

	checkCmd := &mockCheckCmd{}
	checker := checks.NewRunner(checkCmd)

	builder := appctx.NewBuilder(store, &mockContextGit{})

	engine := stage.NewEngine(sessions, checker, builder, store, database, cfg)
	engine.SetPollInterval(50 * time.Millisecond)
	engine.SetBootDelay(0)

	orch := NewOrchestrator(store, database, ghClient, wt, sessions, engine, builder, cfg)

	return &testEnv{
		orch:     orch,
		store:    store,
		database: database,
		ghCmd:    ghCmd,
		tmux:     tmux,
		checkCmd: checkCmd,
	}
}

func defaultConfig() *config.PipelineConfig {
	return &config.PipelineConfig{
		Pipeline: config.Pipeline{
			MaxFixRounds:      2,
			FreshSessionAfter: 10,
			Defaults: config.StageDefaults{
				Flags:   "--dangerously-skip-permissions",
				Timeout: "5m",
			},
			Checks: map[string]config.Check{
				"lint": {Command: "echo ok", Parser: "generic"},
			},
			Stages: []config.Stage{
				{ID: "implement", Type: "agent", ChecksAfter: []string{"lint"}, PromptTemplate: "impl.md"},
				{ID: "review", Type: "agent", GoalGate: true, ChecksAfter: []string{"lint"}, PromptTemplate: "review.md"},
				{ID: "qa", Type: "checks_only", Checks: []string{"lint"}},
			},
		},
	}
}

func mockIssueJSON(number int, title string) string {
	issue := map[string]interface{}{
		"number": number,
		"title":  title,
		"body":   "Issue body",
		"state":  "OPEN",
		"labels": []map[string]string{},
	}
	data, _ := json.Marshal(issue)
	return string(data)
}

// --- Tests ---

func TestCreate(t *testing.T) {
	cfg := defaultConfig()
	env := setupTest(t, cfg)

	// Mock gh issue view responses (GetIssue + CacheIssue both call gh)
	env.ghCmd.results = []mockCmdResult{
		{output: mockIssueJSON(42, "Add auth")},
		{output: mockIssueJSON(42, "Add auth")},
	}

	ps, err := env.orch.Create(CreateOpts{Issue: 42})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if ps.Issue != 42 {
		t.Errorf("expected issue 42, got %d", ps.Issue)
	}
	if ps.Title != "Add auth" {
		t.Errorf("expected title 'Add auth', got %q", ps.Title)
	}
	if ps.CurrentStage != "implement" {
		t.Errorf("expected first stage 'implement', got %q", ps.CurrentStage)
	}
	if ps.Branch == "" {
		t.Error("expected branch to be set")
	}

	// Verify goal gates initialized
	if _, ok := ps.GoalGates["review"]; !ok {
		t.Error("expected review goal gate to be initialized")
	}
}

func TestCreate_InvalidIssue(t *testing.T) {
	env := setupTest(t, defaultConfig())

	_, err := env.orch.Create(CreateOpts{Issue: 0})
	if err == nil {
		t.Fatal("expected error for issue 0")
	}
}

func TestAdvance_CompletedPipeline(t *testing.T) {
	cfg := defaultConfig()
	env := setupTest(t, cfg)

	// Create pipeline directly
	worktreeDir := t.TempDir()
	env.store.Create(42, "Test", "feature/test", worktreeDir, "implement", nil)
	env.store.Update(42, func(ps *pipeline.PipelineState) {
		ps.Status = "completed"
	})

	result, err := env.orch.Advance(42)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Action != "completed" {
		t.Errorf("expected 'completed', got %q", result.Action)
	}
}

func TestAdvance_FailedPipeline(t *testing.T) {
	cfg := defaultConfig()
	env := setupTest(t, cfg)

	worktreeDir := t.TempDir()
	env.store.Create(42, "Test", "feature/test", worktreeDir, "implement", nil)
	env.store.Update(42, func(ps *pipeline.PipelineState) {
		ps.Status = "failed"
	})

	result, err := env.orch.Advance(42)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Action != "failed" {
		t.Errorf("expected 'failed', got %q", result.Action)
	}
}

func TestAdvance_ChecksOnlyPass(t *testing.T) {
	cfg := &config.PipelineConfig{
		Pipeline: config.Pipeline{
			Checks: map[string]config.Check{
				"lint": {Command: "echo ok", Parser: "generic"},
			},
			Stages: []config.Stage{
				{ID: "validate", Type: "checks_only", Checks: []string{"lint"}},
			},
		},
	}
	env := setupTest(t, cfg)

	// checks pass
	env.checkCmd.results = []cmdResult{{exitCode: 0}}

	worktreeDir := t.TempDir()
	env.store.Create(42, "Test", "feature/test", worktreeDir, "validate", nil)
	env.store.Update(42, func(ps *pipeline.PipelineState) {
		ps.CurrentAttempt = 1
	})

	result, err := env.orch.Advance(42)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Action != "completed" {
		t.Errorf("expected 'completed' (single stage), got %q", result.Action)
	}
}

func TestAdvance_MultiStage(t *testing.T) {
	cfg := &config.PipelineConfig{
		Pipeline: config.Pipeline{
			Checks: map[string]config.Check{
				"lint": {Command: "echo ok", Parser: "generic"},
			},
			Stages: []config.Stage{
				{ID: "validate", Type: "checks_only", Checks: []string{"lint"}},
				{ID: "final", Type: "checks_only", Checks: []string{"lint"}},
			},
		},
	}
	env := setupTest(t, cfg)

	env.checkCmd.results = []cmdResult{{exitCode: 0}}

	worktreeDir := t.TempDir()
	env.store.Create(42, "Test", "feature/test", worktreeDir, "validate", nil)
	env.store.Update(42, func(ps *pipeline.PipelineState) {
		ps.CurrentAttempt = 1
	})

	result, err := env.orch.Advance(42)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Action != "advanced" {
		t.Errorf("expected 'advanced', got %q", result.Action)
	}
	if result.NextStage != "final" {
		t.Errorf("expected next stage 'final', got %q", result.NextStage)
	}

	// Verify pipeline state updated
	ps, _ := env.store.Get(42)
	if ps.CurrentStage != "final" {
		t.Errorf("expected current stage 'final', got %q", ps.CurrentStage)
	}
}

func TestAdvance_StageFailure_Retry(t *testing.T) {
	cfg := &config.PipelineConfig{
		Pipeline: config.Pipeline{
			Checks: map[string]config.Check{
				"lint": {Command: "lint", Parser: "generic"},
			},
			Stages: []config.Stage{
				{ID: "validate", Type: "checks_only", Checks: []string{"lint"}},
			},
		},
	}
	env := setupTest(t, cfg)

	// Check fails
	env.checkCmd.results = []cmdResult{{exitCode: 1}}

	worktreeDir := t.TempDir()
	env.store.Create(42, "Test", "feature/test", worktreeDir, "validate", nil)
	env.store.Update(42, func(ps *pipeline.PipelineState) {
		ps.CurrentAttempt = 1
	})

	result, err := env.orch.Advance(42)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Outcome != "fail" {
		t.Errorf("expected outcome 'fail', got %q", result.Outcome)
	}

	// Should increment attempt
	ps, _ := env.store.Get(42)
	if ps.CurrentAttempt != 2 {
		t.Errorf("expected attempt 2, got %d", ps.CurrentAttempt)
	}
}

func TestAdvance_StageFailure_MaxAttempts(t *testing.T) {
	cfg := &config.PipelineConfig{
		Pipeline: config.Pipeline{
			Checks: map[string]config.Check{
				"lint": {Command: "lint", Parser: "generic"},
			},
			Stages: []config.Stage{
				{ID: "validate", Type: "checks_only", Checks: []string{"lint"}},
			},
		},
	}
	env := setupTest(t, cfg)

	env.checkCmd.results = []cmdResult{{exitCode: 1}}

	worktreeDir := t.TempDir()
	env.store.Create(42, "Test", "feature/test", worktreeDir, "validate", nil)
	env.store.Update(42, func(ps *pipeline.PipelineState) {
		ps.CurrentAttempt = 3 // at max
	})

	result, err := env.orch.Advance(42)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Action != "failed" {
		t.Errorf("expected 'failed' at max attempts, got %q", result.Action)
	}

	ps, _ := env.store.Get(42)
	if ps.Status != "failed" {
		t.Errorf("expected status 'failed', got %q", ps.Status)
	}
}

func TestAdvance_OnFail_Escalate(t *testing.T) {
	cfg := &config.PipelineConfig{
		Pipeline: config.Pipeline{
			Checks: map[string]config.Check{
				"lint": {Command: "lint", Parser: "generic"},
			},
			Stages: []config.Stage{
				{ID: "validate", Type: "checks_only", Checks: []string{"lint"}, OnFail: "escalate"},
			},
		},
	}
	env := setupTest(t, cfg)

	env.checkCmd.results = []cmdResult{{exitCode: 1}}

	worktreeDir := t.TempDir()
	env.store.Create(42, "Test", "feature/test", worktreeDir, "validate", nil)
	env.store.Update(42, func(ps *pipeline.PipelineState) {
		ps.CurrentAttempt = 1
	})

	result, err := env.orch.Advance(42)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Action != "escalated" {
		t.Errorf("expected 'escalated', got %q", result.Action)
	}

	ps, _ := env.store.Get(42)
	if ps.Status != "blocked" {
		t.Errorf("expected status 'blocked', got %q", ps.Status)
	}
}

func TestAdvance_OnFail_RouteToStage(t *testing.T) {
	cfg := &config.PipelineConfig{
		Pipeline: config.Pipeline{
			Checks: map[string]config.Check{
				"lint": {Command: "lint", Parser: "generic"},
			},
			Stages: []config.Stage{
				{ID: "implement", Type: "checks_only", Checks: []string{"lint"}},
				{ID: "review", Type: "checks_only", Checks: []string{"lint"}, OnFail: "implement"},
			},
		},
	}
	env := setupTest(t, cfg)

	env.checkCmd.results = []cmdResult{{exitCode: 1}}

	worktreeDir := t.TempDir()
	env.store.Create(42, "Test", "feature/test", worktreeDir, "review", nil)
	env.store.Update(42, func(ps *pipeline.PipelineState) {
		ps.CurrentAttempt = 1
	})

	result, err := env.orch.Advance(42)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.NextStage != "implement" {
		t.Errorf("expected routing to 'implement', got %q", result.NextStage)
	}

	ps, _ := env.store.Get(42)
	if ps.CurrentStage != "implement" {
		t.Errorf("expected current stage 'implement', got %q", ps.CurrentStage)
	}
}

func TestAdvance_GoalGate(t *testing.T) {
	cfg := &config.PipelineConfig{
		Pipeline: config.Pipeline{
			Checks: map[string]config.Check{
				"lint": {Command: "echo ok", Parser: "generic"},
			},
			Stages: []config.Stage{
				{ID: "review", Type: "checks_only", GoalGate: true, Checks: []string{"lint"}},
			},
		},
	}
	env := setupTest(t, cfg)

	env.checkCmd.results = []cmdResult{{exitCode: 0}}

	worktreeDir := t.TempDir()
	goalGates := map[string]string{"review": ""}
	env.store.Create(42, "Test", "feature/test", worktreeDir, "review", goalGates)
	env.store.Update(42, func(ps *pipeline.PipelineState) {
		ps.CurrentAttempt = 1
	})

	result, err := env.orch.Advance(42)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Action != "completed" {
		t.Errorf("expected 'completed', got %q", result.Action)
	}

	// Verify goal gate was marked
	ps, _ := env.store.Get(42)
	if ps.GoalGates["review"] != "success" {
		t.Errorf("expected goal gate 'success', got %q", ps.GoalGates["review"])
	}
}

func TestAdvance_GoalGate_Unsatisfied(t *testing.T) {
	cfg := &config.PipelineConfig{
		Pipeline: config.Pipeline{
			Checks: map[string]config.Check{
				"lint": {Command: "echo ok", Parser: "generic"},
			},
			Stages: []config.Stage{
				{ID: "implement", Type: "checks_only", Checks: []string{"lint"}},
				// review has goal_gate but we skip it and go to qa
			},
		},
	}

	// Test checkGoalGates directly
	env := setupTest(t, cfg)
	worktreeDir := t.TempDir()
	goalGates := map[string]string{"review": ""}
	env.store.Create(42, "Test", "feature/test", worktreeDir, "implement", goalGates)

	// Add a goal gate stage to the config AFTER creating orchestrator
	cfg.Pipeline.Stages = append(cfg.Pipeline.Stages, config.Stage{ID: "review", GoalGate: true})

	err := env.orch.checkGoalGates(42)
	if err == nil {
		t.Fatal("expected error for unsatisfied goal gate")
	}
	if !strings.Contains(err.Error(), "not satisfied") {
		t.Errorf("expected 'not satisfied' in error, got %q", err.Error())
	}
}

func TestRetry(t *testing.T) {
	env := setupTest(t, defaultConfig())

	worktreeDir := t.TempDir()
	env.store.Create(42, "Test", "feature/test", worktreeDir, "implement", nil)
	env.store.Update(42, func(ps *pipeline.PipelineState) {
		ps.CurrentAttempt = 1
		ps.Status = "failed"
	})

	err := env.orch.Retry(RetryOpts{Issue: 42, Reason: "try again"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ps, _ := env.store.Get(42)
	if ps.CurrentAttempt != 2 {
		t.Errorf("expected attempt 2, got %d", ps.CurrentAttempt)
	}
	if ps.Status != "in_progress" {
		t.Errorf("expected status 'in_progress', got %q", ps.Status)
	}
}

func TestRetry_CompletedPipeline(t *testing.T) {
	env := setupTest(t, defaultConfig())

	worktreeDir := t.TempDir()
	env.store.Create(42, "Test", "feature/test", worktreeDir, "implement", nil)
	env.store.Update(42, func(ps *pipeline.PipelineState) {
		ps.Status = "completed"
	})

	err := env.orch.Retry(RetryOpts{Issue: 42})
	if err == nil {
		t.Fatal("expected error for completed pipeline")
	}
}

func TestFail(t *testing.T) {
	env := setupTest(t, defaultConfig())

	worktreeDir := t.TempDir()
	env.store.Create(42, "Test", "feature/test", worktreeDir, "implement", nil)
	env.store.Update(42, func(ps *pipeline.PipelineState) {
		ps.CurrentAttempt = 1
	})

	err := env.orch.Fail(FailOpts{Issue: 42, Reason: "broken"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ps, _ := env.store.Get(42)
	if ps.Status != "failed" {
		t.Errorf("expected status 'failed', got %q", ps.Status)
	}
}

func TestAbort(t *testing.T) {
	env := setupTest(t, defaultConfig())

	worktreeDir := t.TempDir()
	env.store.Create(42, "Test", "feature/test", worktreeDir, "implement", nil)
	env.store.Update(42, func(ps *pipeline.PipelineState) {
		ps.CurrentAttempt = 1
	})

	err := env.orch.Abort(AbortOpts{Issue: 42})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ps, _ := env.store.Get(42)
	if ps.Status != "failed" {
		t.Errorf("expected status 'failed', got %q", ps.Status)
	}
}

func TestStatus(t *testing.T) {
	env := setupTest(t, defaultConfig())

	worktreeDir := t.TempDir()
	env.store.Create(42, "Test", "feature/test", worktreeDir, "implement", nil)
	env.store.Update(42, func(ps *pipeline.PipelineState) {
		ps.CurrentAttempt = 1
	})

	info, err := env.orch.Status(42)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if info.Issue != 42 {
		t.Errorf("expected issue 42, got %d", info.Issue)
	}
	if info.Stage != "implement" {
		t.Errorf("expected stage 'implement', got %q", info.Stage)
	}
}

func TestStatusAll(t *testing.T) {
	env := setupTest(t, defaultConfig())

	worktreeDir1 := t.TempDir()
	worktreeDir2 := t.TempDir()
	env.store.Create(42, "Issue A", "branch-a", worktreeDir1, "implement", nil)
	env.store.Create(43, "Issue B", "branch-b", worktreeDir2, "review", nil)

	infos, err := env.orch.StatusAll()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(infos) != 2 {
		t.Fatalf("expected 2 pipelines, got %d", len(infos))
	}
}

func TestAdvance_OnFail_InvalidTarget(t *testing.T) {
	cfg := &config.PipelineConfig{
		Pipeline: config.Pipeline{
			Checks: map[string]config.Check{
				"lint": {Command: "lint", Parser: "generic"},
			},
			Stages: []config.Stage{
				{ID: "validate", Type: "checks_only", Checks: []string{"lint"}, OnFail: "nonexistent_stage"},
			},
		},
	}
	env := setupTest(t, cfg)

	env.checkCmd.results = []cmdResult{{exitCode: 1}}

	worktreeDir := t.TempDir()
	env.store.Create(42, "Test", "feature/test", worktreeDir, "validate", nil)

	_, err := env.orch.Advance(42)
	if err == nil {
		t.Fatal("expected error for on_fail routing to nonexistent stage")
	}
	if !strings.Contains(err.Error(), "not found in config") {
		t.Errorf("expected 'not found in config' error, got %q", err.Error())
	}
}

func TestRetry_BlockedPipeline(t *testing.T) {
	env := setupTest(t, defaultConfig())

	worktreeDir := t.TempDir()
	env.store.Create(42, "Test", "feature/test", worktreeDir, "implement", nil)
	env.store.Update(42, func(ps *pipeline.PipelineState) {
		ps.CurrentAttempt = 1
		ps.Status = "blocked"
	})

	err := env.orch.Retry(RetryOpts{Issue: 42, Reason: "manual override"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ps, _ := env.store.Get(42)
	if ps.CurrentAttempt != 2 {
		t.Errorf("expected attempt 2, got %d", ps.CurrentAttempt)
	}
	if ps.Status != "in_progress" {
		t.Errorf("expected status 'in_progress', got %q", ps.Status)
	}
}

func TestResolveOnFail(t *testing.T) {
	tests := []struct {
		name     string
		input    interface{}
		expected string
	}{
		{"nil", nil, ""},
		{"string", "implement", "implement"},
		{"escalate", "escalate", "escalate"},
		{"map with default", map[string]interface{}{"default": "targeted_fix"}, "targeted_fix"},
		{"empty map", map[string]interface{}{}, ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveOnFail(tc.input)
			if got != tc.expected {
				t.Errorf("resolveOnFail(%v) = %q, want %q", tc.input, got, tc.expected)
			}
		})
	}
}

func TestNextStageID(t *testing.T) {
	cfg := defaultConfig()
	env := setupTest(t, cfg)

	if next := env.orch.nextStageID("implement"); next != "review" {
		t.Errorf("expected 'review', got %q", next)
	}
	if next := env.orch.nextStageID("review"); next != "qa" {
		t.Errorf("expected 'qa', got %q", next)
	}
	if next := env.orch.nextStageID("qa"); next != "" {
		t.Errorf("expected empty for last stage, got %q", next)
	}
	if next := env.orch.nextStageID("nonexistent"); next != "" {
		t.Errorf("expected empty for nonexistent, got %q", next)
	}
}

func TestCheckIn_NoPipelines(t *testing.T) {
	env := setupTest(t, defaultConfig())

	result, err := env.orch.CheckIn()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Actions) != 0 {
		t.Errorf("expected 0 actions, got %d", len(result.Actions))
	}
}

func TestCheckIn_SkipsCompletedAndFailed(t *testing.T) {
	env := setupTest(t, defaultConfig())

	wtDir1 := t.TempDir()
	wtDir2 := t.TempDir()
	env.store.Create(42, "Completed", "b-a", wtDir1, "implement", nil)
	env.store.Update(42, func(ps *pipeline.PipelineState) {
		ps.Status = "completed"
	})
	env.store.Create(43, "Failed", "b-b", wtDir2, "implement", nil)
	env.store.Update(43, func(ps *pipeline.PipelineState) {
		ps.Status = "failed"
	})

	result, err := env.orch.CheckIn()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Actions) != 0 {
		t.Errorf("expected 0 actions for completed/failed pipelines, got %d", len(result.Actions))
	}
}

func TestCheckIn_BlockedSkipped(t *testing.T) {
	env := setupTest(t, defaultConfig())

	wtDir := t.TempDir()
	env.store.Create(42, "Blocked", "b-a", wtDir, "review", nil)
	env.store.Update(42, func(ps *pipeline.PipelineState) {
		ps.Status = "blocked"
	})

	result, err := env.orch.CheckIn()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Actions) != 1 {
		t.Fatalf("expected 1 action, got %d", len(result.Actions))
	}
	if result.Actions[0].Action != "skip" {
		t.Errorf("expected 'skip' for blocked, got %q", result.Actions[0].Action)
	}
}

func TestCheckIn_AdvancesPendingNoSession(t *testing.T) {
	cfg := &config.PipelineConfig{
		Pipeline: config.Pipeline{
			Checks: map[string]config.Check{
				"lint": {Command: "echo ok", Parser: "generic"},
			},
			Stages: []config.Stage{
				{ID: "validate", Type: "checks_only", Checks: []string{"lint"}},
				{ID: "final", Type: "checks_only", Checks: []string{"lint"}},
			},
		},
	}
	env := setupTest(t, cfg)

	// checks pass
	env.checkCmd.results = []cmdResult{{exitCode: 0}}

	wtDir := t.TempDir()
	env.store.Create(42, "Test", "b-a", wtDir, "validate", nil)
	env.store.Update(42, func(ps *pipeline.PipelineState) {
		ps.CurrentAttempt = 1
	})

	result, err := env.orch.CheckIn()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Actions) != 1 {
		t.Fatalf("expected 1 action, got %d", len(result.Actions))
	}
	if result.Actions[0].Action != "advanced" {
		t.Errorf("expected 'advanced', got %q", result.Actions[0].Action)
	}
}

func TestCheckIn_ActiveSessionWithinTimeout(t *testing.T) {
	cfg := &config.PipelineConfig{
		Pipeline: config.Pipeline{
			Defaults: config.StageDefaults{Timeout: "30m"},
			Stages:   []config.Stage{{ID: "implement", Type: "agent"}},
		},
	}
	env := setupTest(t, cfg)

	wtDir := t.TempDir()
	env.store.Create(42, "Test", "b-a", wtDir, "implement", nil)
	env.store.Update(42, func(ps *pipeline.PipelineState) {
		ps.CurrentSession = "sess-42"
		ps.CurrentAttempt = 1
	})

	// Create session in tmux mock + DB
	env.tmux.sessions["sess-42"] = true
	env.database.LogSessionEvent("sess-42", 42, "implement", "started", nil, "")

	result, err := env.orch.CheckIn()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Actions) != 1 {
		t.Fatalf("expected 1 action, got %d", len(result.Actions))
	}
	if result.Actions[0].Action != "skip" {
		t.Errorf("expected 'skip' for active within timeout, got %q", result.Actions[0].Action)
	}
}

func TestCheckIn_IdleSessionAdvances(t *testing.T) {
	cfg := &config.PipelineConfig{
		Pipeline: config.Pipeline{
			Checks: map[string]config.Check{
				"lint": {Command: "echo ok", Parser: "generic"},
			},
			Stages: []config.Stage{
				{ID: "validate", Type: "checks_only", Checks: []string{"lint"}},
				{ID: "final", Type: "checks_only", Checks: []string{"lint"}},
			},
		},
	}
	env := setupTest(t, cfg)

	// checks pass
	env.checkCmd.results = []cmdResult{{exitCode: 0}}

	wtDir := t.TempDir()
	env.store.Create(42, "Test", "b-a", wtDir, "validate", nil)
	env.store.Update(42, func(ps *pipeline.PipelineState) {
		ps.CurrentSession = "sess-42"
		ps.CurrentAttempt = 1
	})

	env.tmux.sessions["sess-42"] = true
	env.database.LogSessionEvent("sess-42", 42, "validate", "idle", nil, "")

	result, err := env.orch.CheckIn()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Actions) != 1 {
		t.Fatalf("expected 1 action, got %d", len(result.Actions))
	}
	if result.Actions[0].Action != "advanced" {
		t.Errorf("expected 'advanced' for idle session, got %q", result.Actions[0].Action)
	}
}

func TestCheckIn_ExitedSessionAdvances(t *testing.T) {
	cfg := &config.PipelineConfig{
		Pipeline: config.Pipeline{
			Checks: map[string]config.Check{
				"lint": {Command: "echo ok", Parser: "generic"},
			},
			Stages: []config.Stage{
				{ID: "validate", Type: "checks_only", Checks: []string{"lint"}},
			},
		},
	}
	env := setupTest(t, cfg)

	env.checkCmd.results = []cmdResult{{exitCode: 0}}

	wtDir := t.TempDir()
	env.store.Create(42, "Test", "b-a", wtDir, "validate", nil)
	env.store.Update(42, func(ps *pipeline.PipelineState) {
		ps.CurrentSession = "sess-42"
		ps.CurrentAttempt = 1
	})

	env.tmux.sessions["sess-42"] = true
	env.database.LogSessionEvent("sess-42", 42, "validate", "exited", nil, "")

	result, err := env.orch.CheckIn()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Actions) != 1 {
		t.Fatalf("expected 1 action, got %d", len(result.Actions))
	}
	if result.Actions[0].Action != "completed" {
		t.Errorf("expected 'completed' for exited session, got %q", result.Actions[0].Action)
	}
}

func TestCheckIn_MultiplePipelines(t *testing.T) {
	cfg := &config.PipelineConfig{
		Pipeline: config.Pipeline{
			Checks: map[string]config.Check{
				"lint": {Command: "echo ok", Parser: "generic"},
			},
			Stages: []config.Stage{
				{ID: "validate", Type: "checks_only", Checks: []string{"lint"}},
			},
		},
	}
	env := setupTest(t, cfg)

	// Only one check needed per check-in (sequential: first pipeline only)
	env.checkCmd.results = []cmdResult{{exitCode: 0}}

	wtDir1 := t.TempDir()
	wtDir2 := t.TempDir()
	env.store.Create(42, "Issue A", "b-a", wtDir1, "validate", nil)
	env.store.Update(42, func(ps *pipeline.PipelineState) {
		ps.CurrentAttempt = 1
	})
	env.store.Create(43, "Issue B", "b-b", wtDir2, "validate", nil)
	env.store.Update(43, func(ps *pipeline.PipelineState) {
		ps.CurrentAttempt = 1
	})

	result, err := env.orch.CheckIn()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Sequential mode: only one pipeline processed per check-in (issue 42 first)
	if len(result.Actions) != 1 {
		t.Fatalf("expected 1 action (sequential), got %d", len(result.Actions))
	}
	if result.Actions[0].Issue != 42 {
		t.Errorf("expected issue 42 to be processed first, got %d", result.Actions[0].Issue)
	}
}

func TestCheckIn_HumanInterventionSkipped(t *testing.T) {
	cfg := &config.PipelineConfig{
		Pipeline: config.Pipeline{
			Stages: []config.Stage{{ID: "implement", Type: "agent"}},
		},
	}
	env := setupTest(t, cfg)

	wtDir := t.TempDir()
	env.store.Create(42, "Test", "b-a", wtDir, "implement", nil)
	env.store.Update(42, func(ps *pipeline.PipelineState) {
		ps.CurrentSession = "sess-42"
		ps.CurrentAttempt = 1
	})

	env.tmux.sessions["sess-42"] = true
	// Log started, then active WITHOUT a factory_send → human intervention
	env.database.LogSessionEvent("sess-42", 42, "implement", "started", nil, "")
	env.database.LogSessionEvent("sess-42", 42, "implement", "active", nil, "")

	result, err := env.orch.CheckIn()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Actions) != 1 {
		t.Fatalf("expected 1 action, got %d", len(result.Actions))
	}
	if result.Actions[0].Action != "skip" {
		t.Errorf("expected 'skip' for human intervention, got %q", result.Actions[0].Action)
	}
	if !strings.Contains(result.Actions[0].Message, "human") {
		t.Errorf("expected human-related message, got %q", result.Actions[0].Message)
	}
}

func TestCheckIn_InProgress_NoSession_Advances(t *testing.T) {
	// An in_progress pipeline with no session (orphaned by a killed runner) should
	// be picked up and re-advanced rather than skipped indefinitely.
	env := setupTest(t, defaultConfig())

	wtDir := t.TempDir()
	env.store.Create(42, "Test", "b-a", wtDir, "implement", nil)
	env.store.Update(42, func(ps *pipeline.PipelineState) {
		ps.Status = "in_progress"
		ps.CurrentAttempt = 1
		ps.CurrentSession = "" // no session — runner was killed before session was created
	})

	result, err := env.orch.CheckIn()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Actions) != 1 {
		t.Fatalf("expected 1 action, got %d", len(result.Actions))
	}
	// Should attempt to advance (not skip), landing as "advance" or "escalate" depending on engine
	if result.Actions[0].Action == "skip" {
		t.Errorf("orphaned in_progress pipeline should not be skipped, got skip with message %q", result.Actions[0].Message)
	}
}

func TestCheckIn_ActiveSessionPastTimeout_Steers(t *testing.T) {
	cfg := &config.PipelineConfig{
		Pipeline: config.Pipeline{
			Defaults: config.StageDefaults{Timeout: "1s"}, // 1-second timeout for testing
			Stages:   []config.Stage{{ID: "implement", Type: "agent"}},
		},
	}
	env := setupTest(t, cfg)

	wtDir := t.TempDir()
	env.store.Create(42, "Test", "b-a", wtDir, "implement", nil)
	env.store.Update(42, func(ps *pipeline.PipelineState) {
		ps.CurrentSession = "sess-42"
		ps.CurrentAttempt = 1
	})

	env.tmux.sessions["sess-42"] = true
	// Log started event in the past so timeout is exceeded
	env.database.LogSessionEvent("sess-42", 42, "implement", "started", nil, "")

	// Wait for timeout to expire
	time.Sleep(1100 * time.Millisecond)

	result, err := env.orch.CheckIn()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Actions) != 1 {
		t.Fatalf("expected 1 action, got %d", len(result.Actions))
	}
	if result.Actions[0].Action != "steer" {
		t.Errorf("expected 'steer' for timed-out session, got %q", result.Actions[0].Action)
	}
	if !strings.Contains(result.Actions[0].Message, "wrap-up") {
		t.Errorf("expected wrap-up message, got %q", result.Actions[0].Message)
	}
}

func TestCheckIn_ActiveSessionPastTimeout_SteerGuard(t *testing.T) {
	cfg := &config.PipelineConfig{
		Pipeline: config.Pipeline{
			Defaults: config.StageDefaults{Timeout: "1s"},
			Stages:   []config.Stage{{ID: "implement", Type: "agent"}},
		},
	}
	env := setupTest(t, cfg)

	wtDir := t.TempDir()
	env.store.Create(42, "Test", "b-a", wtDir, "implement", nil)
	env.store.Update(42, func(ps *pipeline.PipelineState) {
		ps.CurrentSession = "sess-42"
		ps.CurrentAttempt = 1
	})

	env.tmux.sessions["sess-42"] = true
	env.database.LogSessionEvent("sess-42", 42, "implement", "started", nil, "")
	// Log a recent steer event so the guard fires
	env.database.LogSessionEvent("sess-42", 42, "implement", "steer", nil, "")

	time.Sleep(1100 * time.Millisecond)

	result, err := env.orch.CheckIn()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Actions) != 1 {
		t.Fatalf("expected 1 action, got %d", len(result.Actions))
	}
	if result.Actions[0].Action != "skip" {
		t.Errorf("expected 'skip' (steer guard), got %q", result.Actions[0].Action)
	}
	if !strings.Contains(result.Actions[0].Message, "steer already sent") {
		t.Errorf("expected steer guard message, got %q", result.Actions[0].Message)
	}
}

func TestCheckIn_HumanInputState(t *testing.T) {
	cfg := &config.PipelineConfig{
		Pipeline: config.Pipeline{
			Stages: []config.Stage{{ID: "implement", Type: "agent"}},
		},
	}
	env := setupTest(t, cfg)

	wtDir := t.TempDir()
	env.store.Create(42, "Test", "b-a", wtDir, "implement", nil)
	env.store.Update(42, func(ps *pipeline.PipelineState) {
		ps.CurrentSession = "sess-42"
		ps.CurrentAttempt = 1
	})

	env.tmux.sessions["sess-42"] = true
	env.database.LogSessionEvent("sess-42", 42, "implement", "started", nil, "")
	// Most recent event is human_input — should skip
	env.database.LogSessionEvent("sess-42", 42, "implement", "human_input", nil, "")

	result, err := env.orch.CheckIn()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Actions) != 1 {
		t.Fatalf("expected 1 action, got %d", len(result.Actions))
	}
	if result.Actions[0].Action != "skip" {
		t.Errorf("expected 'skip' for human_input state, got %q", result.Actions[0].Action)
	}
	if !strings.Contains(result.Actions[0].Message, "human input") {
		t.Errorf("expected human input message, got %q", result.Actions[0].Message)
	}
}

func TestCheckIn_OrphanedSessionCleared(t *testing.T) {
	cfg := &config.PipelineConfig{
		Pipeline: config.Pipeline{
			Checks: map[string]config.Check{
				"lint": {Command: "echo ok", Parser: "generic"},
			},
			Stages: []config.Stage{
				{ID: "validate", Type: "checks_only", Checks: []string{"lint"}},
				{ID: "final", Type: "checks_only", Checks: []string{"lint"}},
			},
		},
	}
	env := setupTest(t, cfg)

	env.checkCmd.results = []cmdResult{{exitCode: 0}}

	wtDir := t.TempDir()
	env.store.Create(42, "Test", "b-a", wtDir, "validate", nil)
	env.store.Update(42, func(ps *pipeline.PipelineState) {
		ps.CurrentSession = "orphaned-sess"
		ps.CurrentAttempt = 1
	})

	// No tmux session, no DB events → Status will fail → orphan path
	// Don't add "orphaned-sess" to tmux mock or DB

	result, err := env.orch.CheckIn()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Actions) != 1 {
		t.Fatalf("expected 1 action, got %d", len(result.Actions))
	}
	// Should have cleared the orphan and then advanced
	if result.Actions[0].Action != "advanced" {
		t.Errorf("expected 'advanced' after orphan cleanup, got %q", result.Actions[0].Action)
	}

	// Verify session reference was cleared
	ps, _ := env.store.Get(42)
	if ps.CurrentSession != "" {
		t.Errorf("expected empty session after orphan cleanup, got %q", ps.CurrentSession)
	}
}

func TestCheckIn_AdvanceError_Escalates(t *testing.T) {
	cfg := &config.PipelineConfig{
		Pipeline: config.Pipeline{
			Stages: []config.Stage{
				// Reference a nonexistent stage config (only the listed stage exists).
				// The stage ID is "implement" but its prompt_template is empty and the
				// engine will likely error when trying to run it with no check. We need
				// the advance to error. Easiest: put the pipeline on a stage that
				// doesn't exist in the config.
				{ID: "implement", Type: "agent"},
			},
		},
	}
	env := setupTest(t, cfg)

	wtDir := t.TempDir()
	env.store.Create(42, "Test", "b-a", wtDir, "nonexistent_stage", nil)
	env.store.Update(42, func(ps *pipeline.PipelineState) {
		ps.CurrentAttempt = 1
	})

	result, err := env.orch.CheckIn()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Actions) != 1 {
		t.Fatalf("expected 1 action, got %d", len(result.Actions))
	}
	if result.Actions[0].Action != "escalate" {
		t.Errorf("expected 'escalate' on advance error, got %q", result.Actions[0].Action)
	}

	// Verify pipeline was marked as blocked
	ps, _ := env.store.Get(42)
	if ps.Status != "blocked" {
		t.Errorf("expected status 'blocked' after escalation, got %q", ps.Status)
	}
}

func TestCheckIn_SteerFactorySendState(t *testing.T) {
	cfg := &config.PipelineConfig{
		Pipeline: config.Pipeline{
			Defaults: config.StageDefaults{Timeout: "30m"},
			Stages:   []config.Stage{{ID: "implement", Type: "agent"}},
		},
	}
	env := setupTest(t, cfg)

	wtDir := t.TempDir()
	env.store.Create(42, "Test", "b-a", wtDir, "implement", nil)
	env.store.Update(42, func(ps *pipeline.PipelineState) {
		ps.CurrentSession = "sess-42"
		ps.CurrentAttempt = 1
	})

	env.tmux.sessions["sess-42"] = true
	env.database.LogSessionEvent("sess-42", 42, "implement", "started", nil, "")
	// Most recent event is factory_send — should be treated as active
	env.database.LogSessionEvent("sess-42", 42, "implement", "factory_send", nil, "")

	result, err := env.orch.CheckIn()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Actions) != 1 {
		t.Fatalf("expected 1 action, got %d", len(result.Actions))
	}
	// factory_send is treated as active; within timeout → skip
	if result.Actions[0].Action != "skip" {
		t.Errorf("expected 'skip' for factory_send state, got %q", result.Actions[0].Action)
	}
}

func TestCheckIn_QueuePopsWhenIdle(t *testing.T) {
	cfg := &config.PipelineConfig{
		Pipeline: config.Pipeline{
			Checks: map[string]config.Check{
				"lint": {Command: "echo ok", Parser: "generic"},
			},
			Stages: []config.Stage{
				{ID: "validate", Type: "checks_only", Checks: []string{"lint"}},
			},
		},
	}
	env := setupTest(t, cfg)

	// Add issues to queue
	if err := env.database.QueueAdd([]db.QueueAddItem{{Issue: 42, FeatureIntent: "test intent"}}); err != nil {
		t.Fatalf("queue add: %v", err)
	}

	// Mock gh issue view responses (GetIssue + CacheIssue)
	env.ghCmd.results = []mockCmdResult{
		{output: mockIssueJSON(42, "Queue Test")},
		{output: mockIssueJSON(42, "Queue Test")},
	}

	// No existing pipelines → should pop from queue
	result, err := env.orch.CheckIn()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Actions) != 1 {
		t.Fatalf("expected 1 action, got %d", len(result.Actions))
	}
	if result.Actions[0].Action != "queue_started" {
		t.Errorf("expected 'queue_started', got %q", result.Actions[0].Action)
	}
	if result.Actions[0].Issue != 42 {
		t.Errorf("expected issue 42, got %d", result.Actions[0].Issue)
	}

	// Verify queue item is now active
	items, _ := env.database.QueueList()
	if len(items) != 1 {
		t.Fatalf("expected 1 queue item, got %d", len(items))
	}
	if items[0].Status != "active" {
		t.Errorf("expected queue item status 'active', got %q", items[0].Status)
	}
}

func TestCheckIn_QueueWaitsForActivePipeline(t *testing.T) {
	cfg := &config.PipelineConfig{
		Pipeline: config.Pipeline{
			Checks: map[string]config.Check{
				"lint": {Command: "echo ok", Parser: "generic"},
			},
			Stages: []config.Stage{
				{ID: "validate", Type: "checks_only", Checks: []string{"lint"}},
			},
		},
	}
	env := setupTest(t, cfg)

	// Add issue to queue
	if err := env.database.QueueAdd([]db.QueueAddItem{{Issue: 99, FeatureIntent: "test intent"}}); err != nil {
		t.Fatalf("queue add: %v", err)
	}

	// Create an active pipeline (pending status)
	env.checkCmd.results = []cmdResult{{exitCode: 0}}
	wtDir := t.TempDir()
	env.store.Create(42, "Active", "b-a", wtDir, "validate", nil)
	env.store.Update(42, func(ps *pipeline.PipelineState) {
		ps.CurrentAttempt = 1
	})

	result, err := env.orch.CheckIn()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Queue should NOT have been popped — pipeline was active
	for _, a := range result.Actions {
		if a.Action == "queue_started" {
			t.Error("queue should not pop when active pipelines exist")
		}
	}

	// Verify queue item still pending
	item, _ := env.database.QueueNext()
	if item == nil {
		t.Fatal("expected queue item to still be pending")
	}
	if item.Issue != 99 {
		t.Errorf("expected issue 99 in queue, got %d", item.Issue)
	}
}

func TestCheckIn_QueueEmptyNoAction(t *testing.T) {
	env := setupTest(t, defaultConfig())

	// No pipelines, empty queue
	result, err := env.orch.CheckIn()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Actions) != 0 {
		t.Errorf("expected 0 actions for empty queue, got %d", len(result.Actions))
	}
}

func TestCheckIn_QueueCompletedUpdatesStatus(t *testing.T) {
	cfg := &config.PipelineConfig{
		Pipeline: config.Pipeline{
			Checks: map[string]config.Check{
				"lint": {Command: "echo ok", Parser: "generic"},
			},
			Stages: []config.Stage{
				{ID: "validate", Type: "checks_only", Checks: []string{"lint"}},
			},
		},
	}
	env := setupTest(t, cfg)

	// Add issue to queue and mark it active (as if processQueue ran)
	if err := env.database.QueueAdd([]db.QueueAddItem{{Issue: 42, FeatureIntent: "test intent"}}); err != nil {
		t.Fatalf("queue add: %v", err)
	}
	if err := env.database.QueueUpdateStatus(42, "active"); err != nil {
		t.Fatalf("update status: %v", err)
	}

	// Create the pipeline at the last stage, checks will pass → completed
	env.checkCmd.results = []cmdResult{{exitCode: 0}}
	wtDir := t.TempDir()
	env.store.Create(42, "Test", "b-a", wtDir, "validate", nil)
	env.store.Update(42, func(ps *pipeline.PipelineState) {
		ps.CurrentAttempt = 1
	})

	result, err := env.orch.CheckIn()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Actions) != 1 {
		t.Fatalf("expected 1 action, got %d", len(result.Actions))
	}
	if result.Actions[0].Action != "completed" {
		t.Errorf("expected 'completed', got %q", result.Actions[0].Action)
	}

	// Verify queue item was marked completed
	items, _ := env.database.QueueList()
	if len(items) != 1 {
		t.Fatalf("expected 1 queue item, got %d", len(items))
	}
	if items[0].Status != "completed" {
		t.Errorf("expected queue status 'completed', got %q", items[0].Status)
	}
}

func TestCheckIn_QueueAutoDerivesIntent(t *testing.T) {
	cfg := &config.PipelineConfig{
		Pipeline: config.Pipeline{
			Checks: map[string]config.Check{
				"lint": {Command: "echo ok", Parser: "generic"},
			},
			Stages: []config.Stage{
				{ID: "validate", Type: "checks_only", Checks: []string{"lint"}},
			},
		},
	}
	env := setupTest(t, cfg)

	// Set mock LLM that returns a derived intent
	env.orch.SetClaudeFn(func(prompt string) (string, error) {
		return "Let users download reports as CSV for offline analysis.", nil
	})

	// Add issue WITHOUT intent
	if err := env.database.QueueAdd([]db.QueueAddItem{{Issue: 42, FeatureIntent: ""}}); err != nil {
		t.Fatalf("queue add: %v", err)
	}

	// Mock gh responses: GetIssue for derivation + GetIssue + CacheIssue in Create
	issueJSON := `{"number":42,"title":"Add CSV export","body":"Users need CSV export.","state":"OPEN","labels":[]}`
	env.ghCmd.results = []mockCmdResult{
		{output: issueJSON},
		{output: issueJSON},
		{output: issueJSON},
	}

	result, err := env.orch.CheckIn()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Actions) != 1 {
		t.Fatalf("expected 1 action, got %d", len(result.Actions))
	}
	if result.Actions[0].Action != "queue_started" {
		t.Errorf("expected 'queue_started' after auto-derivation, got %q", result.Actions[0].Action)
	}

	// Verify intent was persisted to queue
	items, _ := env.database.QueueList()
	if items[0].FeatureIntent != "Let users download reports as CSV for offline analysis." {
		t.Errorf("expected derived intent persisted, got %q", items[0].FeatureIntent)
	}
}

func TestCheckIn_QueueSkipsNoIntent(t *testing.T) {
	cfg := &config.PipelineConfig{
		Pipeline: config.Pipeline{
			Stages: []config.Stage{
				{ID: "validate", Type: "checks_only"},
			},
		},
	}
	env := setupTest(t, cfg)

	// Set mock LLM that returns NO_INTENT
	env.orch.SetClaudeFn(func(prompt string) (string, error) {
		return "NO_INTENT", nil
	})

	// Add issue WITHOUT intent
	if err := env.database.QueueAdd([]db.QueueAddItem{{Issue: 42, FeatureIntent: ""}}); err != nil {
		t.Fatalf("queue add: %v", err)
	}

	// Mock gh response
	issueJSON := `{"number":42,"title":"Fix bug","body":"Internal cleanup.","state":"OPEN","labels":[]}`
	env.ghCmd.results = []mockCmdResult{
		{output: issueJSON},
	}

	result, err := env.orch.CheckIn()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Actions) != 1 {
		t.Fatalf("expected 1 action, got %d", len(result.Actions))
	}
	if result.Actions[0].Action != "skip" {
		t.Errorf("expected 'skip' for unresolvable intent, got %q", result.Actions[0].Action)
	}
	if !strings.Contains(result.Actions[0].Message, "feature_intent") {
		t.Errorf("expected feature_intent in skip message, got %q", result.Actions[0].Message)
	}
}

func TestCheckIn_QueueSkipsWhenNoClaudeFn(t *testing.T) {
	cfg := &config.PipelineConfig{
		Pipeline: config.Pipeline{
			Stages: []config.Stage{
				{ID: "validate", Type: "checks_only"},
			},
		},
	}
	env := setupTest(t, cfg)

	// No claudeFn set — should skip without attempting derivation

	if err := env.database.QueueAdd([]db.QueueAddItem{{Issue: 42, FeatureIntent: ""}}); err != nil {
		t.Fatalf("queue add: %v", err)
	}

	result, err := env.orch.CheckIn()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Actions) != 1 {
		t.Fatalf("expected 1 action, got %d", len(result.Actions))
	}
	if result.Actions[0].Action != "skip" {
		t.Errorf("expected 'skip' when no claudeFn, got %q", result.Actions[0].Action)
	}
}

func TestFormatCheckStateSummary(t *testing.T) {
	summary := formatCheckStateSummary(map[string]string{
		"lint": "pass",
	})
	if !strings.Contains(summary, "lint: pass") {
		t.Errorf("unexpected summary: %q", summary)
	}

	empty := formatCheckStateSummary(nil)
	if empty != "" {
		t.Errorf("expected empty for nil, got %q", empty)
	}
}

func TestAdvance_MergeStage_HappyPath(t *testing.T) {
	cfg := &config.PipelineConfig{
		Pipeline: config.Pipeline{
			Stages: []config.Stage{
				{ID: "merge", Type: "merge", MergeStrategy: "squash"},
			},
		},
	}
	env := setupTest(t, cfg)

	// Mock responses: PushBranch (via RunGit→Run), FindPRByBranch, CreatePR, MergePR
	env.ghCmd.results = []mockCmdResult{
		{output: ""},                          // push
		{output: "[]"},                        // FindPRByBranch (no existing PR)
		{output: "https://github.com/test/1"}, // create PR
		{output: ""},                          // merge PR
	}

	worktreeDir := t.TempDir()
	env.store.Create(42, "Test Feature", "feature/test", worktreeDir, "merge", nil)
	env.store.Update(42, func(ps *pipeline.PipelineState) {
		ps.CurrentAttempt = 1
	})

	result, err := env.orch.Advance(42)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Action != "completed" {
		t.Errorf("expected 'completed' (last stage), got %q", result.Action)
	}
	if result.Outcome != "success" {
		t.Errorf("expected outcome 'success', got %q", result.Outcome)
	}

	// Verify pipeline marked completed
	ps, _ := env.store.Get(42)
	if ps.Status != "completed" {
		t.Errorf("expected status 'completed', got %q", ps.Status)
	}

	// Verify the right commands were called
	if len(env.ghCmd.calls) != 4 {
		t.Fatalf("expected 4 gh calls, got %d", len(env.ghCmd.calls))
	}
	// Push
	if env.ghCmd.calls[0][0] != "push" {
		t.Errorf("expected first call to be push, got %v", env.ghCmd.calls[0])
	}
	// FindPRByBranch
	if env.ghCmd.calls[1][0] != "pr" || env.ghCmd.calls[1][1] != "list" {
		t.Errorf("expected second call to be pr list, got %v", env.ghCmd.calls[1])
	}
	// Create PR
	if env.ghCmd.calls[2][0] != "pr" || env.ghCmd.calls[2][1] != "create" {
		t.Errorf("expected third call to be pr create, got %v", env.ghCmd.calls[2])
	}
	// Merge PR
	if env.ghCmd.calls[3][0] != "pr" || env.ghCmd.calls[3][1] != "merge" {
		t.Errorf("expected fourth call to be pr merge, got %v", env.ghCmd.calls[3])
	}
}

func TestAdvance_MergeStage_PushFails(t *testing.T) {
	cfg := &config.PipelineConfig{
		Pipeline: config.Pipeline{
			Stages: []config.Stage{
				{ID: "merge", Type: "merge"},
			},
		},
	}
	env := setupTest(t, cfg)

	// Push fails
	env.ghCmd.results = []mockCmdResult{
		{output: "error", err: fmt.Errorf("push rejected")},
	}

	worktreeDir := t.TempDir()
	env.store.Create(42, "Test Feature", "feature/test", worktreeDir, "merge", nil)
	env.store.Update(42, func(ps *pipeline.PipelineState) {
		ps.CurrentAttempt = 1
	})

	result, err := env.orch.Advance(42)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Outcome != "fail" {
		t.Errorf("expected outcome 'fail', got %q", result.Outcome)
	}

	// Should retry (default on_fail behavior)
	ps, _ := env.store.Get(42)
	if ps.CurrentAttempt != 2 {
		t.Errorf("expected attempt 2 after failure, got %d", ps.CurrentAttempt)
	}
}

func TestAdvance_MergeStage_DefaultStrategy(t *testing.T) {
	cfg := &config.PipelineConfig{
		Pipeline: config.Pipeline{
			Stages: []config.Stage{
				{ID: "merge", Type: "merge"}, // no merge_strategy set
			},
		},
	}
	env := setupTest(t, cfg)

	env.ghCmd.results = []mockCmdResult{
		{output: ""},                          // push
		{output: "[]"},                        // FindPRByBranch (no existing PR)
		{output: "https://github.com/test/1"}, // create PR
		{output: ""},                          // merge PR
	}

	worktreeDir := t.TempDir()
	env.store.Create(42, "Test Feature", "feature/test", worktreeDir, "merge", nil)
	env.store.Update(42, func(ps *pipeline.PipelineState) {
		ps.CurrentAttempt = 1
	})

	result, err := env.orch.Advance(42)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Action != "completed" {
		t.Errorf("expected 'completed', got %q", result.Action)
	}

	// Verify merge was called with --squash (default)
	mergeCall := env.ghCmd.calls[3]
	foundSquash := false
	for _, arg := range mergeCall {
		if arg == "--squash" {
			foundSquash = true
		}
	}
	if !foundSquash {
		t.Errorf("expected --squash in merge call, got %v", mergeCall)
	}
}

func TestAdvance_MergeStage_CreatePRFails(t *testing.T) {
	cfg := &config.PipelineConfig{
		Pipeline: config.Pipeline{
			Stages: []config.Stage{
				{ID: "merge", Type: "merge", OnFail: "escalate"},
			},
		},
	}
	env := setupTest(t, cfg)

	// Push succeeds, FindPRByBranch returns none, CreatePR fails
	env.ghCmd.results = []mockCmdResult{
		{output: ""},                                         // push
		{output: "[]"},                                       // FindPRByBranch (no existing PR)
		{output: "", err: fmt.Errorf("PR create error")},     // create PR fails
	}

	worktreeDir := t.TempDir()
	env.store.Create(42, "Test Feature", "feature/test", worktreeDir, "merge", nil)
	env.store.Update(42, func(ps *pipeline.PipelineState) {
		ps.CurrentAttempt = 1
	})

	result, err := env.orch.Advance(42)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Action != "escalated" {
		t.Errorf("expected 'escalated' (on_fail=escalate), got %q", result.Action)
	}

	ps, _ := env.store.Get(42)
	if ps.Status != "blocked" {
		t.Errorf("expected status 'blocked', got %q", ps.Status)
	}
}

func TestAdvance_MergeStage_ReusesExistingPR(t *testing.T) {
	cfg := &config.PipelineConfig{
		Pipeline: config.Pipeline{
			Stages: []config.Stage{
				{ID: "merge", Type: "merge", MergeStrategy: "squash"},
			},
		},
	}
	env := setupTest(t, cfg)

	// Push succeeds, FindPRByBranch finds existing PR, skip CreatePR, MergePR
	env.ghCmd.results = []mockCmdResult{
		{output: ""},                                                             // push
		{output: `[{"url":"https://github.com/org/repo/pull/99"}]`},              // FindPRByBranch (existing PR)
		{output: ""},                                                             // merge PR
	}

	worktreeDir := t.TempDir()
	env.store.Create(42, "Test Feature", "feature/test", worktreeDir, "merge", nil)
	env.store.Update(42, func(ps *pipeline.PipelineState) {
		ps.CurrentAttempt = 1
	})

	result, err := env.orch.Advance(42)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Action != "completed" {
		t.Errorf("expected 'completed', got %q", result.Action)
	}
	if result.Outcome != "success" {
		t.Errorf("expected outcome 'success', got %q", result.Outcome)
	}

	// Verify CreatePR was NOT called (only push, pr list, pr merge)
	if len(env.ghCmd.calls) != 3 {
		t.Fatalf("expected 3 gh calls (push, pr list, pr merge), got %d: %v", len(env.ghCmd.calls), env.ghCmd.calls)
	}
	// Push
	if env.ghCmd.calls[0][0] != "push" {
		t.Errorf("expected first call to be push, got %v", env.ghCmd.calls[0])
	}
	// FindPRByBranch
	if env.ghCmd.calls[1][0] != "pr" || env.ghCmd.calls[1][1] != "list" {
		t.Errorf("expected second call to be pr list, got %v", env.ghCmd.calls[1])
	}
	// Merge PR (not CreatePR)
	if env.ghCmd.calls[2][0] != "pr" || env.ghCmd.calls[2][1] != "merge" {
		t.Errorf("expected third call to be pr merge, got %v", env.ghCmd.calls[2])
	}
}

func TestCleanup_CompletedPipeline(t *testing.T) {
	env := setupTest(t, defaultConfig())

	worktreeDir := t.TempDir()
	env.store.Create(42, "Test", "feature/test", worktreeDir, "implement", nil)
	env.store.Update(42, func(ps *pipeline.PipelineState) {
		ps.Status = "completed"
	})

	result, err := env.orch.Cleanup(42)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Removed {
		t.Errorf("expected Removed=true, got false")
	}
	if result.Issue != 42 {
		t.Errorf("expected issue 42, got %d", result.Issue)
	}

	// Verify pipeline data is gone
	_, err = env.store.Get(42)
	if err == nil {
		t.Error("expected error getting deleted pipeline")
	}
}

func TestCleanup_FailedPipeline(t *testing.T) {
	env := setupTest(t, defaultConfig())

	worktreeDir := t.TempDir()
	env.store.Create(42, "Test", "feature/test", worktreeDir, "implement", nil)
	env.store.Update(42, func(ps *pipeline.PipelineState) {
		ps.Status = "failed"
	})

	result, err := env.orch.Cleanup(42)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Removed {
		t.Errorf("expected Removed=true, got false")
	}

	// Verify pipeline data is gone
	_, err = env.store.Get(42)
	if err == nil {
		t.Error("expected error getting deleted pipeline")
	}
}

func TestCleanup_ActivePipeline_Rejected(t *testing.T) {
	env := setupTest(t, defaultConfig())

	worktreeDir := t.TempDir()
	env.store.Create(42, "Test", "feature/test", worktreeDir, "implement", nil)

	// Test each non-terminal status
	for _, status := range []string{"pending", "in_progress", "blocked"} {
		env.store.Update(42, func(ps *pipeline.PipelineState) {
			ps.Status = status
		})

		_, err := env.orch.Cleanup(42)
		if err == nil {
			t.Errorf("expected error for status %q, got nil", status)
		}
		if !strings.Contains(err.Error(), "only completed or failed") {
			t.Errorf("expected rejection message for status %q, got %q", status, err.Error())
		}
	}
}

func TestCleanupAll(t *testing.T) {
	env := setupTest(t, defaultConfig())

	wtDir1 := t.TempDir()
	wtDir2 := t.TempDir()
	wtDir3 := t.TempDir()
	env.store.Create(42, "Completed", "b-a", wtDir1, "implement", nil)
	env.store.Update(42, func(ps *pipeline.PipelineState) {
		ps.Status = "completed"
	})
	env.store.Create(43, "Failed", "b-b", wtDir2, "implement", nil)
	env.store.Update(43, func(ps *pipeline.PipelineState) {
		ps.Status = "failed"
	})
	env.store.Create(44, "Active", "b-c", wtDir3, "implement", nil)
	env.store.Update(44, func(ps *pipeline.PipelineState) {
		ps.Status = "pending"
	})

	results, err := env.orch.CleanupAll()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(results) != 2 {
		t.Fatalf("expected 2 results (completed+failed), got %d", len(results))
	}

	for _, r := range results {
		if !r.Removed {
			t.Errorf("expected pipeline #%d to be removed, got message: %s", r.Issue, r.Message)
		}
	}

	// Verify terminal pipelines are gone
	_, err = env.store.Get(42)
	if err == nil {
		t.Error("expected pipeline 42 to be deleted")
	}
	_, err = env.store.Get(43)
	if err == nil {
		t.Error("expected pipeline 43 to be deleted")
	}

	// Verify active pipeline is untouched
	ps, err := env.store.Get(44)
	if err != nil {
		t.Fatalf("expected pipeline 44 to still exist: %v", err)
	}
	if ps.Status != "pending" {
		t.Errorf("expected pipeline 44 status 'pending', got %q", ps.Status)
	}
}
