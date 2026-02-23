package triage

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/lucasnoah/taintfactory/internal/db"
	"github.com/lucasnoah/taintfactory/internal/session"
)

// ---- config helpers ----

// testSinglePrintConfig: one print-mode stage that routes to done.
func testSinglePrintConfig() *TriageConfig {
	return &TriageConfig{
		Triage: TriageMeta{Name: "test", Repo: "owner/test"},
		Stages: []TriageStage{
			{
				ID:      "classifier_a",
				Mode:    "print",
				Timeout: "5m",
				Label:   "label-a",
				Outcomes: map[string]string{
					"yes": "done",
					"no":  "done",
				},
			},
		},
	}
}

// testChainedPrintConfig: two print-mode stages chained.
func testChainedPrintConfig() *TriageConfig {
	return &TriageConfig{
		Triage: TriageMeta{Name: "test", Repo: "owner/test"},
		Stages: []TriageStage{
			{
				ID:      "classifier_a",
				Mode:    "print",
				Timeout: "5m",
				Label:   "label-a",
				Outcomes: map[string]string{
					"yes": "classifier_b",
					"no":  "classifier_b",
				},
			},
			{
				ID:      "classifier_b",
				Mode:    "print",
				Timeout: "5m",
				Label:   "label-b",
				Outcomes: map[string]string{
					"yes": "done",
					"no":  "done",
				},
			},
		},
	}
}

// testAsyncThenPrintConfig: one async stage that routes to one print-mode stage.
func testAsyncThenPrintConfig() *TriageConfig {
	return &TriageConfig{
		Triage: TriageMeta{Name: "test", Repo: "owner/test"},
		Stages: []TriageStage{
			{
				ID:      "stale_context",
				Timeout: "10m",
				Outcomes: map[string]string{
					"stale": "done",
					"clean": "classifier_a",
				},
			},
			{
				ID:      "classifier_a",
				Mode:    "print",
				Timeout: "5m",
				Label:   "label-a",
				Outcomes: map[string]string{
					"yes": "done",
					"no":  "done",
				},
			},
		},
	}
}

// ---- setup helpers ----

// setupPrintRunnerWith builds a Runner wired to in-memory DB, mockTmux, and mockGHClient.
// Returns runner, store, database, gh mock, and repoRoot.
func setupPrintRunnerWith(t *testing.T, cfg *TriageConfig) (*Runner, *Store, *db.DB, *mockGHClient, string) {
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
	gh := &mockGHClient{}
	repoRoot := t.TempDir()

	runner := NewRunner(cfg, store, database, sessions, gh, repoRoot)
	runner.bootWait = 0

	return runner, store, database, gh, repoRoot
}

// writePrintStageTemplate writes a minimal Go template file for a print-mode stage.
func writePrintStageTemplate(t *testing.T, repoRoot, stageID string) {
	t.Helper()
	dir := filepath.Join(repoRoot, "triage")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir triage dir: %v", err)
	}
	content := "Issue #{{.issue_number}}: {{.issue_title}}\n{{.issue_body}}"
	if err := os.WriteFile(filepath.Join(dir, stageID+".md"), []byte(content), 0o644); err != nil {
		t.Fatalf("write template %s: %v", stageID, err)
	}
}

// ---- parseOutcomeFromOutput tests ----

func TestParseOutcomeFromOutput_CleanJSON(t *testing.T) {
	out := `{"outcome":"yes","summary":"schema change required"}`
	got, err := parseOutcomeFromOutput(out)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Outcome != "yes" {
		t.Errorf("Outcome = %q, want yes", got.Outcome)
	}
	if got.Summary != "schema change required" {
		t.Errorf("Summary = %q, want %q", got.Summary, "schema change required")
	}
}

func TestParseOutcomeFromOutput_JsonAfterPreamble(t *testing.T) {
	out := "Sure, here is my analysis:\nThe issue requires a migration.\n{\"outcome\":\"no\",\"summary\":\"additive only\"}"
	got, err := parseOutcomeFromOutput(out)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Outcome != "no" {
		t.Errorf("Outcome = %q, want no", got.Outcome)
	}
	if got.Summary != "additive only" {
		t.Errorf("Summary = %q, want %q", got.Summary, "additive only")
	}
}

func TestParseOutcomeFromOutput_NoJSON_ReturnsError(t *testing.T) {
	_, err := parseOutcomeFromOutput("This is just plain text with no JSON")
	if err == nil {
		t.Error("expected error for output with no JSON, got nil")
	}
}

func TestParseOutcomeFromOutput_JSONMissingOutcomeField_ReturnsError(t *testing.T) {
	// Valid JSON but no "outcome" field — should be rejected.
	_, err := parseOutcomeFromOutput(`{"result":true,"reasoning":"something"}`)
	if err == nil {
		t.Error("expected error for JSON missing 'outcome' field, got nil")
	}
}

func TestParseOutcomeFromOutput_WhitespaceWrapped(t *testing.T) {
	out := "  \n  {\"outcome\":\"yes\",\"summary\":\"ok\"}  \n  "
	got, err := parseOutcomeFromOutput(out)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Outcome != "yes" {
		t.Errorf("Outcome = %q, want yes", got.Outcome)
	}
}

// ---- Config field tests ----

func TestLoad_ModeAndLabelFields(t *testing.T) {
	path := writeTempYAML(t, `
triage:
  name: "Test"
  repo: "owner/test"
stages:
  - id: classifier_a
    mode: print
    label: schema-change
    timeout: 5m
    outcomes:
      yes: done
      no: done
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if len(cfg.Stages) != 1 {
		t.Fatalf("len(stages) = %d, want 1", len(cfg.Stages))
	}
	s := cfg.Stages[0]
	if s.Mode != "print" {
		t.Errorf("Mode = %q, want print", s.Mode)
	}
	if s.Label != "schema-change" {
		t.Errorf("Label = %q, want schema-change", s.Label)
	}
}

// ---- runPrintStage tests ----

func TestRunner_runPrintStage_YesOutcome_AppliesLabel(t *testing.T) {
	cfg := testSinglePrintConfig()
	runner, store, _, _, repoRoot := setupPrintRunnerWith(t, cfg)
	writePrintStageTemplate(t, repoRoot, "classifier_a")

	var labelsApplied []string
	runner.labelExec = func(repo string, issue int, label string) error {
		labelsApplied = append(labelsApplied, label)
		return nil
	}
	runner.printExec = func(stageCfg *TriageStage, prompt string) (string, error) {
		return `{"outcome":"yes","summary":"needs schema change"}`, nil
	}

	st := &TriageState{
		Issue: 1, Repo: "owner/test",
		CurrentStage: "classifier_a",
		Status:       "in_progress",
		StageHistory: []TriageStageHistoryEntry{},
	}
	if err := store.Save(st); err != nil {
		t.Fatalf("Save: %v", err)
	}

	action := runner.runPrintStage(st)

	if action.Action != "completed" {
		t.Errorf("Action = %q, want completed (outcome routes to done)", action.Action)
	}
	if len(labelsApplied) != 1 || labelsApplied[0] != "label-a" {
		t.Errorf("labelsApplied = %v, want [label-a]", labelsApplied)
	}
}

func TestRunner_runPrintStage_NoOutcome_SkipsLabel(t *testing.T) {
	cfg := testSinglePrintConfig()
	runner, store, _, _, repoRoot := setupPrintRunnerWith(t, cfg)
	writePrintStageTemplate(t, repoRoot, "classifier_a")

	var labelsApplied []string
	runner.labelExec = func(repo string, issue int, label string) error {
		labelsApplied = append(labelsApplied, label)
		return nil
	}
	runner.printExec = func(stageCfg *TriageStage, prompt string) (string, error) {
		return `{"outcome":"no","summary":"additive only"}`, nil
	}

	st := &TriageState{
		Issue: 2, Repo: "owner/test",
		CurrentStage: "classifier_a",
		Status:       "in_progress",
		StageHistory: []TriageStageHistoryEntry{},
	}
	if err := store.Save(st); err != nil {
		t.Fatalf("Save: %v", err)
	}

	action := runner.runPrintStage(st)

	if action.Action != "completed" {
		t.Errorf("Action = %q, want completed", action.Action)
	}
	if len(labelsApplied) != 0 {
		t.Errorf("labelsApplied = %v, want empty (no-outcome should not trigger label)", labelsApplied)
	}
}

func TestRunner_runPrintStage_WritesOutcomeFile(t *testing.T) {
	cfg := testSinglePrintConfig()
	runner, store, _, _, repoRoot := setupPrintRunnerWith(t, cfg)
	writePrintStageTemplate(t, repoRoot, "classifier_a")

	runner.labelExec = func(repo string, issue int, label string) error { return nil }
	runner.printExec = func(stageCfg *TriageStage, prompt string) (string, error) {
		return `{"outcome":"yes","summary":"audit trail test"}`, nil
	}

	st := &TriageState{
		Issue: 3, Repo: "owner/test",
		CurrentStage: "classifier_a",
		Status:       "in_progress",
		StageHistory: []TriageStageHistoryEntry{},
	}
	if err := store.Save(st); err != nil {
		t.Fatalf("Save: %v", err)
	}

	runner.runPrintStage(st)

	data, err := os.ReadFile(store.OutcomePath(3, "classifier_a"))
	if err != nil {
		t.Fatalf("outcome file not written: %v", err)
	}
	var outcome TriageOutcome
	if err := json.Unmarshal(data, &outcome); err != nil {
		t.Fatalf("unmarshal outcome file: %v", err)
	}
	if outcome.Outcome != "yes" {
		t.Errorf("outcome.Outcome = %q, want yes", outcome.Outcome)
	}
	if outcome.Summary != "audit trail test" {
		t.Errorf("outcome.Summary = %q, want 'audit trail test'", outcome.Summary)
	}
}

func TestRunner_runPrintStage_ExecError_ReturnsErrorAction(t *testing.T) {
	cfg := testSinglePrintConfig()
	runner, store, _, _, repoRoot := setupPrintRunnerWith(t, cfg)
	writePrintStageTemplate(t, repoRoot, "classifier_a")

	runner.printExec = func(stageCfg *TriageStage, prompt string) (string, error) {
		return "", fmt.Errorf("claude not available")
	}

	st := &TriageState{
		Issue: 4, Repo: "owner/test",
		CurrentStage: "classifier_a",
		Status:       "in_progress",
		StageHistory: []TriageStageHistoryEntry{},
	}
	if err := store.Save(st); err != nil {
		t.Fatalf("Save: %v", err)
	}

	action := runner.runPrintStage(st)

	if action.Action != "error" {
		t.Errorf("Action = %q, want error", action.Action)
	}
}

func TestRunner_runPrintStage_SetsStageHistoryWithDuration(t *testing.T) {
	cfg := testSinglePrintConfig()
	runner, store, _, _, repoRoot := setupPrintRunnerWith(t, cfg)
	writePrintStageTemplate(t, repoRoot, "classifier_a")

	runner.labelExec = func(repo string, issue int, label string) error { return nil }
	runner.printExec = func(stageCfg *TriageStage, prompt string) (string, error) {
		return `{"outcome":"yes","summary":"history test"}`, nil
	}

	st := &TriageState{
		Issue: 5, Repo: "owner/test",
		CurrentStage: "classifier_a",
		Status:       "in_progress",
		StageHistory: []TriageStageHistoryEntry{},
	}
	if err := store.Save(st); err != nil {
		t.Fatalf("Save: %v", err)
	}

	runner.runPrintStage(st)

	got, err := store.Get(5)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(got.StageHistory) != 1 {
		t.Fatalf("len(StageHistory) = %d, want 1", len(got.StageHistory))
	}
	h := got.StageHistory[0]
	if h.Stage != "classifier_a" {
		t.Errorf("StageHistory[0].Stage = %q, want classifier_a", h.Stage)
	}
	if h.Outcome != "yes" {
		t.Errorf("StageHistory[0].Outcome = %q, want yes", h.Outcome)
	}
	if h.Duration == "" {
		t.Error("StageHistory[0].Duration is empty, want non-empty")
	}
}

func TestRunner_runPrintStage_UsesModelFromConfig(t *testing.T) {
	cfg := &TriageConfig{
		Triage: TriageMeta{Name: "test", Repo: "owner/test"},
		Stages: []TriageStage{
			{
				ID:      "classifier_a",
				Mode:    "print",
				Model:   "claude-sonnet-4-6",
				Timeout: "5m",
				Outcomes: map[string]string{
					"yes": "done",
					"no":  "done",
				},
			},
		},
	}
	runner, store, _, _, repoRoot := setupPrintRunnerWith(t, cfg)
	writePrintStageTemplate(t, repoRoot, "classifier_a")

	var capturedStageCfg *TriageStage
	runner.labelExec = func(repo string, issue int, label string) error { return nil }
	runner.printExec = func(stageCfg *TriageStage, prompt string) (string, error) {
		capturedStageCfg = stageCfg
		return `{"outcome":"yes","summary":"model check"}`, nil
	}

	st := &TriageState{
		Issue: 6, Repo: "owner/test",
		CurrentStage: "classifier_a",
		Status:       "in_progress",
		StageHistory: []TriageStageHistoryEntry{},
	}
	if err := store.Save(st); err != nil {
		t.Fatalf("Save: %v", err)
	}

	runner.runPrintStage(st)

	if capturedStageCfg == nil {
		t.Fatal("printExec was not called")
	}
	if capturedStageCfg.Model != "claude-sonnet-4-6" {
		t.Errorf("stageCfg.Model = %q, want claude-sonnet-4-6", capturedStageCfg.Model)
	}
}

// ---- Advance() with print-mode stages ----

func TestRunner_Advance_PendingPrintStage_RunsImmediately(t *testing.T) {
	cfg := testSinglePrintConfig()
	runner, store, _, _, repoRoot := setupPrintRunnerWith(t, cfg)
	writePrintStageTemplate(t, repoRoot, "classifier_a")

	runner.labelExec = func(repo string, issue int, label string) error { return nil }
	runner.printExec = func(stageCfg *TriageStage, prompt string) (string, error) {
		return `{"outcome":"yes","summary":"immediate run"}`, nil
	}

	st := &TriageState{
		Issue: 10, Repo: "owner/test",
		CurrentStage: "classifier_a",
		Status:       "pending",
		StageHistory: []TriageStageHistoryEntry{},
	}
	if err := store.Save(st); err != nil {
		t.Fatalf("Save: %v", err)
	}

	actions, err := runner.Advance()
	if err != nil {
		t.Fatalf("Advance() error: %v", err)
	}

	if len(actions) == 0 {
		t.Fatal("Advance() returned no actions")
	}
	last := actions[len(actions)-1]
	if last.Action != "completed" {
		t.Errorf("last action = %q, want completed", last.Action)
	}

	got, err := store.Get(10)
	if err != nil {
		t.Fatalf("Get after Advance: %v", err)
	}
	if got.Status != "completed" {
		t.Errorf("Status = %q, want completed", got.Status)
	}
}

func TestRunner_Advance_PendingPrintStage_NoTmuxSessionCreated(t *testing.T) {
	cfg := testSinglePrintConfig()
	runner, store, _, _, repoRoot := setupPrintRunnerWith(t, cfg)
	writePrintStageTemplate(t, repoRoot, "classifier_a")

	runner.labelExec = func(repo string, issue int, label string) error { return nil }
	runner.printExec = func(stageCfg *TriageStage, prompt string) (string, error) {
		return `{"outcome":"no","summary":"no session test"}`, nil
	}

	st := &TriageState{
		Issue: 11, Repo: "owner/test",
		CurrentStage: "classifier_a",
		Status:       "pending",
		StageHistory: []TriageStageHistoryEntry{},
	}
	if err := store.Save(st); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if _, err := runner.Advance(); err != nil {
		t.Fatalf("Advance() error: %v", err)
	}

	// No tmux session should have been created for a print-mode stage.
	got, err := store.Get(11)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.CurrentSession != "" {
		t.Errorf("CurrentSession = %q, want empty (print mode never creates a session)", got.CurrentSession)
	}
}

func TestRunner_Advance_ChainsPrintStages_SingleCall(t *testing.T) {
	cfg := testChainedPrintConfig()
	runner, store, _, _, repoRoot := setupPrintRunnerWith(t, cfg)
	writePrintStageTemplate(t, repoRoot, "classifier_a")
	writePrintStageTemplate(t, repoRoot, "classifier_b")

	runner.labelExec = func(repo string, issue int, label string) error { return nil }
	callCount := 0
	runner.printExec = func(stageCfg *TriageStage, prompt string) (string, error) {
		callCount++
		return `{"outcome":"yes","summary":"chained"}`, nil
	}

	st := &TriageState{
		Issue: 12, Repo: "owner/test",
		CurrentStage: "classifier_a",
		Status:       "pending",
		StageHistory: []TriageStageHistoryEntry{},
	}
	if err := store.Save(st); err != nil {
		t.Fatalf("Save: %v", err)
	}

	actions, err := runner.Advance()
	if err != nil {
		t.Fatalf("Advance() error: %v", err)
	}

	// Both stages should run in a single Advance() call.
	if callCount != 2 {
		t.Errorf("printExec called %d times, want 2", callCount)
	}
	if len(actions) != 2 {
		t.Errorf("len(actions) = %d, want 2", len(actions))
	}
	if actions[0].Stage != "classifier_a" {
		t.Errorf("actions[0].Stage = %q, want classifier_a", actions[0].Stage)
	}
	if actions[1].Stage != "classifier_b" {
		t.Errorf("actions[1].Stage = %q, want classifier_b", actions[1].Stage)
	}
	if actions[1].Action != "completed" {
		t.Errorf("actions[1].Action = %q, want completed", actions[1].Action)
	}

	got, err := store.Get(12)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != "completed" {
		t.Errorf("Status = %q, want completed", got.Status)
	}
	if len(got.StageHistory) != 2 {
		t.Errorf("len(StageHistory) = %d, want 2", len(got.StageHistory))
	}
}

// TestRunner_Advance_AsyncToprint: when an async stage routes to a print-mode stage,
// advanceOne does NOT create a session for the print stage. The state is left with
// CurrentStage=print-mode and no CurrentSession. A subsequent Advance() call runs it.
func TestRunner_Advance_AsyncToPrint_NoSessionCreatedForPrintStage(t *testing.T) {
	cfg := testAsyncThenPrintConfig()
	runner, store, database, gh, repoRoot := setupPrintRunnerWith(t, cfg)
	_ = gh
	writePrintStageTemplate(t, repoRoot, "classifier_a")

	runner.labelExec = func(repo string, issue int, label string) error { return nil }
	runner.printExec = func(stageCfg *TriageStage, prompt string) (string, error) {
		return `{"outcome":"yes","summary":"follows async"}`, nil
	}

	sessionName := "triage-20-stale_context"
	st := &TriageState{
		Issue: 20, Repo: "owner/test",
		CurrentStage:   "stale_context",
		Status:         "in_progress",
		CurrentSession: sessionName,
		StageHistory:   []TriageStageHistoryEntry{},
	}
	if err := store.Save(st); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := database.LogSessionEvent(sessionName, 20, "stale_context", "started", nil, ""); err != nil {
		t.Fatalf("log started: %v", err)
	}
	if err := database.LogSessionEvent(sessionName, 20, "stale_context", "idle", nil, ""); err != nil {
		t.Fatalf("log idle: %v", err)
	}
	writeOutcomeFile(t, store, 20, "stale_context", "clean", "all good")

	// First Advance(): processes async stale_context, advances state to classifier_a.
	actions, err := runner.Advance()
	if err != nil {
		t.Fatalf("first Advance() error: %v", err)
	}
	if len(actions) == 0 {
		t.Fatal("first Advance() returned no actions")
	}

	got, err := store.Get(20)
	if err != nil {
		t.Fatalf("Get after first Advance: %v", err)
	}
	if got.CurrentStage != "classifier_a" {
		t.Errorf("CurrentStage = %q, want classifier_a", got.CurrentStage)
	}
	// Print-mode stage must not have a session assigned to it.
	if got.CurrentSession != "" {
		t.Errorf("CurrentSession = %q after async→print transition, want empty", got.CurrentSession)
	}
}

func TestRunner_Advance_AsyncToPrint_SecondCallRunsPrintStage(t *testing.T) {
	cfg := testAsyncThenPrintConfig()
	runner, store, database, _, repoRoot := setupPrintRunnerWith(t, cfg)
	writePrintStageTemplate(t, repoRoot, "classifier_a")

	runner.labelExec = func(repo string, issue int, label string) error { return nil }
	runner.printExec = func(stageCfg *TriageStage, prompt string) (string, error) {
		return `{"outcome":"no","summary":"second call test"}`, nil
	}

	sessionName := "triage-21-stale_context"
	st := &TriageState{
		Issue: 21, Repo: "owner/test",
		CurrentStage:   "stale_context",
		Status:         "in_progress",
		CurrentSession: sessionName,
		StageHistory:   []TriageStageHistoryEntry{},
	}
	if err := store.Save(st); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := database.LogSessionEvent(sessionName, 21, "stale_context", "started", nil, ""); err != nil {
		t.Fatalf("log started: %v", err)
	}
	if err := database.LogSessionEvent(sessionName, 21, "stale_context", "idle", nil, ""); err != nil {
		t.Fatalf("log idle: %v", err)
	}
	writeOutcomeFile(t, store, 21, "stale_context", "clean", "all good")

	// First call: advances async stage.
	if _, err := runner.Advance(); err != nil {
		t.Fatalf("first Advance() error: %v", err)
	}

	// Second call: runs print stage from the in_progress, no-session state.
	actions, err := runner.Advance()
	if err != nil {
		t.Fatalf("second Advance() error: %v", err)
	}
	if len(actions) == 0 {
		t.Fatal("second Advance() returned no actions")
	}
	if actions[0].Action != "completed" {
		t.Errorf("actions[0].Action = %q, want completed", actions[0].Action)
	}

	got, err := store.Get(21)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != "completed" {
		t.Errorf("Status = %q, want completed", got.Status)
	}
	if len(got.StageHistory) != 2 {
		t.Errorf("len(StageHistory) = %d, want 2 (stale_context + classifier_a)", len(got.StageHistory))
	}
}
