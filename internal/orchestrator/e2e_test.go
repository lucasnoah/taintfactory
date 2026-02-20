package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lucasnoah/taintfactory/internal/config"
	"github.com/lucasnoah/taintfactory/internal/db"
	"github.com/lucasnoah/taintfactory/internal/pipeline"
)

// TestE2E_FullPipelineRun exercises the full pipeline lifecycle in mock mode:
// create → implement (agent, checks pass) → review (agent, fix loop) →
// qa (checks_only, pass) → final_gate (checks_only, pass) → completed.
func TestE2E_FullPipelineRun(t *testing.T) {
	cfg := &config.PipelineConfig{
		Pipeline: config.Pipeline{
			MaxFixRounds:      3,
			FreshSessionAfter: 10,
			Defaults: config.StageDefaults{
				Flags:   "--dangerously-skip-permissions",
				Timeout: "5m",
			},
			Checks: map[string]config.Check{
				"lint": {Command: "echo ok", Parser: "generic"},
				"test": {Command: "echo ok", Parser: "generic"},
			},
			Stages: []config.Stage{
				{
					ID:             "implement",
					Type:           "agent",
					ChecksAfter:    []string{"lint"},
					PromptTemplate: "impl.md",
				},
				{
					ID:             "review",
					Type:           "agent",
					GoalGate:       true,
					ChecksAfter:    []string{"lint", "test"},
					PromptTemplate: "review.md",
				},
				{
					ID:     "qa",
					Type:   "checks_only",
					Checks: []string{"lint", "test"},
				},
				{
					ID:     "final_gate",
					Type:   "checks_only",
					Checks: []string{"lint", "test"},
				},
			},
		},
	}

	env := setupTest(t, cfg)

	// Set up check results: cmdResult uses exitCode field
	env.checkCmd.results = []cmdResult{
		// implement: lint pass
		{exitCode: 0},
		// review: lint fail, test fail
		{exitCode: 1},
		{exitCode: 1},
		// review fix round 1: lint pass, test pass
		{exitCode: 0},
		{exitCode: 0},
		// qa: lint pass, test pass
		{exitCode: 0},
		{exitCode: 0},
		// final_gate: lint pass, test pass
		{exitCode: 0},
		{exitCode: 0},
	}

	// Mock gh issue view (GetIssue + CacheIssue)
	env.ghCmd.results = []mockCmdResult{
		{output: mockIssueJSON(42, "Add user authentication")},
		{output: mockIssueJSON(42, "Add user authentication")},
	}

	// ================================
	// Step 1: Create Pipeline
	// ================================
	t.Log("Step 1: Create pipeline")
	ps, err := env.orch.Create(CreateOpts{Issue: 42})
	if err != nil {
		t.Fatalf("create pipeline: %v", err)
	}

	if ps.Issue != 42 {
		t.Errorf("expected issue 42, got %d", ps.Issue)
	}
	if ps.CurrentStage != "implement" {
		t.Errorf("expected first stage 'implement', got %q", ps.CurrentStage)
	}
	if ps.Status != "pending" {
		t.Errorf("expected status 'pending', got %q", ps.Status)
	}
	if _, ok := ps.GoalGates["review"]; !ok {
		t.Error("expected review goal gate to be initialized")
	}

	// Install prompt templates in the worktree
	installWorktreeTemplate(t, ps.Worktree, "impl.md", "Implement issue #{{issue_number}}: {{issue_title}}")
	installWorktreeTemplate(t, ps.Worktree, "review.md", "Review issue #{{issue_number}}: {{issue_title}}")

	// ================================
	// Step 2: Advance through implement (agent, lint passes)
	// ================================
	t.Log("Step 2: Advance implement stage")
	go simulateSessionIdle(env.database, "42-implement-1", 42, "implement", 100*time.Millisecond)

	result, err := env.orch.Advance(42)
	if err != nil {
		t.Fatalf("advance implement: %v", err)
	}

	if result.Action != "advanced" {
		t.Errorf("expected 'advanced' after implement, got %q", result.Action)
	}
	if result.NextStage != "review" {
		t.Errorf("expected next stage 'review', got %q", result.NextStage)
	}
	if result.FixRounds != 0 {
		t.Errorf("expected 0 fix rounds for implement, got %d", result.FixRounds)
	}

	ps, _ = env.store.Get(42)
	if ps.CurrentStage != "review" {
		t.Errorf("expected current stage 'review', got %q", ps.CurrentStage)
	}

	// ================================
	// Step 3: Advance through review (agent, fix loop: fail → fix → pass)
	// ================================
	t.Log("Step 3: Advance review stage (with fix loop)")
	go simulateSessionIdle(env.database, "42-review-1", 42, "review", 100*time.Millisecond)
	go simulateSessionIdle(env.database, "42-review-1", 42, "review", 350*time.Millisecond)

	result, err = env.orch.Advance(42)
	if err != nil {
		t.Fatalf("advance review: %v", err)
	}

	if result.Action != "advanced" {
		t.Errorf("expected 'advanced' after review, got %q", result.Action)
	}
	if result.NextStage != "qa" {
		t.Errorf("expected next stage 'qa', got %q", result.NextStage)
	}
	if result.FixRounds != 1 {
		t.Errorf("expected 1 fix round for review, got %d", result.FixRounds)
	}

	ps, _ = env.store.Get(42)
	if ps.GoalGates["review"] != "success" {
		t.Errorf("expected review goal gate 'success', got %q", ps.GoalGates["review"])
	}

	// ================================
	// Step 4: Advance through qa (checks_only, pass)
	// ================================
	t.Log("Step 4: Advance qa stage")
	result, err = env.orch.Advance(42)
	if err != nil {
		t.Fatalf("advance qa: %v", err)
	}
	if result.Action != "advanced" {
		t.Errorf("expected 'advanced' after qa, got %q", result.Action)
	}
	if result.NextStage != "final_gate" {
		t.Errorf("expected next stage 'final_gate', got %q", result.NextStage)
	}

	// ================================
	// Step 5: Advance through final_gate (checks_only, pass → completed)
	// ================================
	t.Log("Step 5: Advance final_gate stage")
	result, err = env.orch.Advance(42)
	if err != nil {
		t.Fatalf("advance final_gate: %v", err)
	}
	if result.Action != "completed" {
		t.Errorf("expected 'completed' after final_gate, got %q", result.Action)
	}

	// ================================
	// Step 6: Verify final pipeline state
	// ================================
	t.Log("Step 6: Verify final state")
	ps, err = env.store.Get(42)
	if err != nil {
		t.Fatalf("get final state: %v", err)
	}

	if ps.Status != "completed" {
		t.Errorf("expected status 'completed', got %q", ps.Status)
	}
	if len(ps.StageHistory) != 4 {
		t.Fatalf("expected 4 stage history entries, got %d", len(ps.StageHistory))
	}

	// implement: success, 0 fix rounds, first pass
	assertStageHistory(t, ps.StageHistory[0], "implement", "success", 0, true)
	// review: success, 1 fix round, not first pass
	assertStageHistory(t, ps.StageHistory[1], "review", "success", 1, false)
	// qa: success
	assertStageHistory(t, ps.StageHistory[2], "qa", "success", 0, true)
	// final_gate: success
	assertStageHistory(t, ps.StageHistory[3], "final_gate", "success", 0, true)

	// ================================
	// Step 7: Verify DB records
	// ================================
	t.Log("Step 7: Verify DB records")

	events, err := env.database.GetPipelineHistory(42)
	if err != nil {
		t.Fatalf("get pipeline history: %v", err)
	}

	eventTypes := countEventTypes(events)
	if eventTypes["created"] != 1 {
		t.Errorf("expected 1 'created' event, got %d", eventTypes["created"])
	}
	if eventTypes["completed"] != 1 {
		t.Errorf("expected 1 'completed' event, got %d", eventTypes["completed"])
	}
	if eventTypes["stage_advanced"] < 3 {
		t.Errorf("expected at least 3 'stage_advanced' events, got %d", eventTypes["stage_advanced"])
	}
	if eventTypes["fix_round_start"] != 1 {
		t.Errorf("expected 1 'fix_round_start' event, got %d", eventTypes["fix_round_start"])
	}

	checkHistory, err := env.database.GetCheckHistory(42)
	if err != nil {
		t.Fatalf("get check history: %v", err)
	}
	if len(checkHistory) < 7 {
		t.Errorf("expected at least 7 check runs, got %d", len(checkHistory))
	}

	var reviewFails, reviewPasses int
	for _, cr := range checkHistory {
		if cr.Stage == "review" {
			if cr.Passed {
				reviewPasses++
			} else {
				reviewFails++
			}
		}
	}
	if reviewFails != 2 {
		t.Errorf("expected 2 review check failures, got %d", reviewFails)
	}
	if reviewPasses != 2 {
		t.Errorf("expected 2 review check passes, got %d", reviewPasses)
	}
}

// TestE2E_PipelineFailure tests: checks fail every time → max attempts → failed.
func TestE2E_PipelineFailure(t *testing.T) {
	cfg := &config.PipelineConfig{
		Pipeline: config.Pipeline{
			MaxFixRounds:      1,
			FreshSessionAfter: 10,
			Defaults: config.StageDefaults{
				Flags:   "--dangerously-skip-permissions",
				Timeout: "5m",
			},
			Checks: map[string]config.Check{
				"lint": {Command: "lint cmd", Parser: "generic"},
			},
			Stages: []config.Stage{
				{
					ID:             "implement",
					Type:           "agent",
					ChecksAfter:    []string{"lint"},
					PromptTemplate: "impl.md",
				},
			},
		},
	}

	env := setupTest(t, cfg)

	env.checkCmd.results = []cmdResult{
		{exitCode: 1}, {exitCode: 1}, // attempt 1: initial fail, fix round fail
		{exitCode: 1}, {exitCode: 1}, // attempt 2
		{exitCode: 1}, {exitCode: 1}, // attempt 3
	}

	env.ghCmd.results = []mockCmdResult{
		{output: mockIssueJSON(42, "Failing feature")},
		{output: mockIssueJSON(42, "Failing feature")},
	}

	ps, err := env.orch.Create(CreateOpts{Issue: 42})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	installWorktreeTemplate(t, ps.Worktree, "impl.md", "Implement {{issue_number}}")

	// Attempt 1: fail → retry
	go simulateSessionIdle(env.database, "42-implement-1", 42, "implement", 100*time.Millisecond)
	go simulateSessionIdle(env.database, "42-implement-1", 42, "implement", 300*time.Millisecond)
	result, err := env.orch.Advance(42)
	if err != nil {
		t.Fatalf("advance 1: %v", err)
	}
	if result.Action != "retry" {
		t.Errorf("expected 'retry', got %q", result.Action)
	}

	// Attempt 2: fail → retry
	go simulateSessionIdle(env.database, "42-implement-2", 42, "implement", 100*time.Millisecond)
	go simulateSessionIdle(env.database, "42-implement-2", 42, "implement", 300*time.Millisecond)
	result, err = env.orch.Advance(42)
	if err != nil {
		t.Fatalf("advance 2: %v", err)
	}
	if result.Action != "retry" {
		t.Errorf("expected 'retry', got %q", result.Action)
	}

	// Attempt 3: fail → failed (max attempts)
	go simulateSessionIdle(env.database, "42-implement-3", 42, "implement", 100*time.Millisecond)
	go simulateSessionIdle(env.database, "42-implement-3", 42, "implement", 300*time.Millisecond)
	result, err = env.orch.Advance(42)
	if err != nil {
		t.Fatalf("advance 3: %v", err)
	}
	if result.Action != "failed" {
		t.Errorf("expected 'failed', got %q", result.Action)
	}

	ps, _ = env.store.Get(42)
	if ps.Status != "failed" {
		t.Errorf("expected status 'failed', got %q", ps.Status)
	}
	if len(ps.StageHistory) != 3 {
		t.Errorf("expected 3 stage history entries, got %d", len(ps.StageHistory))
	}
	for i, h := range ps.StageHistory {
		if h.Outcome != "fail" {
			t.Errorf("history[%d]: expected 'fail', got %q", i, h.Outcome)
		}
	}
}

// TestE2E_Escalation tests on_fail: escalate.
func TestE2E_Escalation(t *testing.T) {
	cfg := &config.PipelineConfig{
		Pipeline: config.Pipeline{
			Checks: map[string]config.Check{
				"lint": {Command: "lint cmd", Parser: "generic"},
			},
			Stages: []config.Stage{
				{ID: "validate", Type: "checks_only", Checks: []string{"lint"}, OnFail: "escalate"},
			},
		},
	}

	env := setupTest(t, cfg)
	env.checkCmd.results = []cmdResult{{exitCode: 1}}
	env.ghCmd.results = []mockCmdResult{
		{output: mockIssueJSON(42, "Escalation test")},
		{output: mockIssueJSON(42, "Escalation test")},
	}

	_, err := env.orch.Create(CreateOpts{Issue: 42})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	result, err := env.orch.Advance(42)
	if err != nil {
		t.Fatalf("advance: %v", err)
	}
	if result.Action != "escalated" {
		t.Errorf("expected 'escalated', got %q", result.Action)
	}

	ps, _ := env.store.Get(42)
	if ps.Status != "blocked" {
		t.Errorf("expected 'blocked', got %q", ps.Status)
	}

	events, _ := env.database.GetPipelineHistory(42)
	if !hasEvent(events, "escalated") {
		t.Error("expected escalation event in history")
	}
}

// TestE2E_OnFailRoute tests on_fail routing to a different stage.
func TestE2E_OnFailRoute(t *testing.T) {
	cfg := &config.PipelineConfig{
		Pipeline: config.Pipeline{
			Checks: map[string]config.Check{
				"lint": {Command: "lint cmd", Parser: "generic"},
			},
			Stages: []config.Stage{
				{ID: "implement", Type: "checks_only", Checks: []string{"lint"}},
				{ID: "review", Type: "checks_only", Checks: []string{"lint"}, OnFail: "implement"},
			},
		},
	}

	env := setupTest(t, cfg)
	env.checkCmd.results = []cmdResult{
		{exitCode: 0}, // implement pass
		{exitCode: 1}, // review fail → route to implement
		{exitCode: 0}, // implement pass (2nd time)
		{exitCode: 0}, // review pass
	}
	env.ghCmd.results = []mockCmdResult{
		{output: mockIssueJSON(42, "Route test")},
		{output: mockIssueJSON(42, "Route test")},
	}

	_, err := env.orch.Create(CreateOpts{Issue: 42})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// implement passes → review
	result, _ := env.orch.Advance(42)
	if result.Action != "advanced" || result.NextStage != "review" {
		t.Errorf("expected advanced→review, got %s→%s", result.Action, result.NextStage)
	}

	// review fails → routes to implement
	result, _ = env.orch.Advance(42)
	if result.Action != "routed" || result.NextStage != "implement" {
		t.Errorf("expected routed→implement, got %s→%s", result.Action, result.NextStage)
	}

	// implement passes → review
	result, _ = env.orch.Advance(42)
	if result.Action != "advanced" || result.NextStage != "review" {
		t.Errorf("expected advanced→review, got %s→%s", result.Action, result.NextStage)
	}

	// review passes → completed
	result, _ = env.orch.Advance(42)
	if result.Action != "completed" {
		t.Errorf("expected completed, got %q", result.Action)
	}

	ps, _ := env.store.Get(42)
	if ps.Status != "completed" {
		t.Errorf("expected completed, got %q", ps.Status)
	}
}

// TestE2E_CheckInDrivenPipeline tests a pipeline driven by CheckIn calls.
func TestE2E_CheckInDrivenPipeline(t *testing.T) {
	cfg := &config.PipelineConfig{
		Pipeline: config.Pipeline{
			Checks: map[string]config.Check{
				"lint": {Command: "echo ok", Parser: "generic"},
			},
			Stages: []config.Stage{
				{ID: "gate1", Type: "checks_only", Checks: []string{"lint"}},
				{ID: "gate2", Type: "checks_only", Checks: []string{"lint"}},
				{ID: "gate3", Type: "checks_only", Checks: []string{"lint"}},
			},
		},
	}

	env := setupTest(t, cfg)
	env.checkCmd.results = []cmdResult{
		{exitCode: 0},
		{exitCode: 0},
		{exitCode: 0},
	}
	env.ghCmd.results = []mockCmdResult{
		{output: mockIssueJSON(42, "CheckIn test")},
		{output: mockIssueJSON(42, "CheckIn test")},
	}

	_, err := env.orch.Create(CreateOpts{Issue: 42})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	for tick := 1; tick <= 3; tick++ {
		result, err := env.orch.CheckIn()
		if err != nil {
			t.Fatalf("tick %d: %v", tick, err)
		}
		if len(result.Actions) != 1 {
			t.Fatalf("tick %d: expected 1 action, got %d", tick, len(result.Actions))
		}
	}

	ps, _ := env.store.Get(42)
	if ps.Status != "completed" {
		t.Errorf("expected completed after 3 ticks, got %q", ps.Status)
	}

	// No more actions after completion
	result, _ := env.orch.CheckIn()
	if len(result.Actions) != 0 {
		t.Errorf("expected 0 actions post-completion, got %d", len(result.Actions))
	}
}

// TestE2E_ManualRetryRecovery tests: fail → manual retry → succeed.
func TestE2E_ManualRetryRecovery(t *testing.T) {
	cfg := &config.PipelineConfig{
		Pipeline: config.Pipeline{
			Checks: map[string]config.Check{
				"lint": {Command: "lint cmd", Parser: "generic"},
			},
			Stages: []config.Stage{
				{ID: "validate", Type: "checks_only", Checks: []string{"lint"}},
			},
		},
	}

	env := setupTest(t, cfg)
	env.checkCmd.results = []cmdResult{
		{exitCode: 1}, // attempt 1
		{exitCode: 1}, // attempt 2
		{exitCode: 1}, // attempt 3 → failed
		{exitCode: 0}, // attempt 4 (retry) → pass
	}
	env.ghCmd.results = []mockCmdResult{
		{output: mockIssueJSON(42, "Retry test")},
		{output: mockIssueJSON(42, "Retry test")},
	}

	_, err := env.orch.Create(CreateOpts{Issue: 42})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// 3 failures → pipeline failed
	for i := 0; i < 3; i++ {
		env.orch.Advance(42)
	}
	ps, _ := env.store.Get(42)
	if ps.Status != "failed" {
		t.Errorf("expected failed, got %q", ps.Status)
	}

	// Manual retry
	if err := env.orch.Retry(RetryOpts{Issue: 42, Reason: "dependency fixed"}); err != nil {
		t.Fatalf("retry: %v", err)
	}

	result, err := env.orch.Advance(42)
	if err != nil {
		t.Fatalf("advance after retry: %v", err)
	}
	if result.Action != "completed" {
		t.Errorf("expected completed, got %q", result.Action)
	}

	events, _ := env.database.GetPipelineHistory(42)
	found := false
	for _, e := range events {
		if e.Event == "retry" && strings.Contains(e.Detail, "dependency fixed") {
			found = true
		}
	}
	if !found {
		t.Error("expected manual retry event with reason")
	}
}

// --- E2E helpers ---

func installWorktreeTemplate(t *testing.T, worktreeDir string, name string, content string) {
	t.Helper()
	path := filepath.Join(worktreeDir, name)
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %q: %v", dir, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %q: %v", path, err)
	}
}

func simulateSessionIdle(database *db.DB, sessionName string, issue int, stage string, delay time.Duration) {
	time.Sleep(delay)
	_ = database.LogSessionEvent(sessionName, issue, stage, "idle", nil, "")
}

func assertStageHistory(t *testing.T, h pipeline.StageHistoryEntry, stage, outcome string, fixRounds int, firstPass bool) {
	t.Helper()
	if h.Stage != stage {
		t.Errorf("stage: expected %q, got %q", stage, h.Stage)
	}
	if h.Outcome != outcome {
		t.Errorf("%s outcome: expected %q, got %q", stage, outcome, h.Outcome)
	}
	if h.FixRounds != fixRounds {
		t.Errorf("%s fix rounds: expected %d, got %d", stage, fixRounds, h.FixRounds)
	}
	if h.ChecksFirstPass != firstPass {
		t.Errorf("%s checks_first_pass: expected %v, got %v", stage, firstPass, h.ChecksFirstPass)
	}
}

func countEventTypes(events []db.PipelineEvent) map[string]int {
	m := make(map[string]int)
	for _, e := range events {
		m[e.Event]++
	}
	return m
}

func hasEvent(events []db.PipelineEvent, eventType string) bool {
	for _, e := range events {
		if e.Event == eventType {
			return true
		}
	}
	return false
}
