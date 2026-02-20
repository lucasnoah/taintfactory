package orchestrator

import (
	"context"
	"encoding/json"
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

func (m *mockTmux) SendKeys(sess string, keys string) error  { return nil }
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
