package context

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lucasnoah/taintfactory/internal/config"
	"github.com/lucasnoah/taintfactory/internal/pipeline"
)

// mockGit implements GitRunner for testing.
type mockGit struct {
	diff            string
	diffSummary     string
	filesChanged    string
	log             string
	diffErr         error
	diffSummaryErr  error
	filesChangedErr error
}

func (m *mockGit) Diff(dir string) (string, error) {
	return m.diff, m.diffErr
}

func (m *mockGit) DiffSummary(dir string) (string, error) {
	if m.diffSummaryErr != nil {
		return "", m.diffSummaryErr
	}
	return m.diffSummary, m.diffErr
}

func (m *mockGit) FilesChanged(dir string) (string, error) {
	if m.filesChangedErr != nil {
		return "", m.filesChangedErr
	}
	return m.filesChanged, m.diffErr
}

func (m *mockGit) Log(dir string) (string, error) {
	return m.log, nil
}

func newTestStore(t *testing.T) *pipeline.Store {
	t.Helper()
	return pipeline.NewStore(t.TempDir())
}

func newTestPipeline(t *testing.T, store *pipeline.Store) *pipeline.PipelineState {
	t.Helper()
	ps, err := store.Create(pipeline.CreateOpts{
		Issue: 42, Title: "Add auth", Branch: "feature/42", Worktree: "/tmp/worktree",
		FirstStage: "implement", GoalGates: map[string]string{"implement": "User can log in"},
	})
	if err != nil {
		t.Fatalf("create pipeline: %v", err)
	}
	return ps
}

func TestBuild_MinimalMode(t *testing.T) {
	store := newTestStore(t)
	ps := newTestPipeline(t, store)

	builder := NewBuilder(store, nil)
	result, err := builder.Build(ps, BuildOpts{
		Issue:     42,
		Stage:     "implement",
		StageCfg:  &config.Stage{ID: "implement", ContextMode: "minimal"},
		IssueBody: "Implement authentication.",
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Mode != ModeMinimal {
		t.Errorf("expected minimal mode, got %q", result.Mode)
	}
	if result.Vars["issue_title"] != "Add auth" {
		t.Errorf("expected issue title, got %q", result.Vars["issue_title"])
	}
	if result.Vars["issue_body"] != "Implement authentication." {
		t.Errorf("expected issue body, got %q", result.Vars["issue_body"])
	}
	// Minimal should NOT have git_diff or prior_stage_summary
	if _, ok := result.Vars["git_diff"]; ok {
		t.Error("minimal mode should not include git_diff")
	}
	if _, ok := result.Vars["prior_stage_summary"]; ok {
		t.Error("minimal mode should not include prior_stage_summary")
	}
}

func TestBuild_FullMode(t *testing.T) {
	store := newTestStore(t)
	ps := newTestPipeline(t, store)

	git := &mockGit{
		diff:         "+added line",
		diffSummary:  "1 file changed",
		filesChanged: "src/auth.ts",
		log:          "abc1234 feat: add auth",
	}

	builder := NewBuilder(store, git)
	result, err := builder.Build(ps, BuildOpts{
		Issue:     42,
		Stage:     "implement",
		StageCfg:  &config.Stage{ID: "implement", ContextMode: "full"},
		IssueBody: "Implement authentication.",
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Mode != ModeFull {
		t.Errorf("expected full mode, got %q", result.Mode)
	}
	if result.Vars["git_commits"] != "abc1234 feat: add auth" {
		t.Errorf("expected git commits, got %q", result.Vars["git_commits"])
	}
	if result.Vars["git_diff_summary"] != "1 file changed" {
		t.Errorf("expected diff summary, got %q", result.Vars["git_diff_summary"])
	}
	if result.Vars["files_changed"] != "src/auth.ts" {
		t.Errorf("expected files changed, got %q", result.Vars["files_changed"])
	}
	if result.Vars["acceptance_criteria"] != "User can log in" {
		t.Errorf("expected acceptance criteria, got %q", result.Vars["acceptance_criteria"])
	}
}

func TestBuild_CodeOnlyMode(t *testing.T) {
	store := newTestStore(t)
	ps := newTestPipeline(t, store)

	// Add a prior stage outcome to verify it's excluded
	ps.StageHistory = []pipeline.StageHistoryEntry{
		{Stage: "implement", Attempt: 1, Outcome: "success"},
	}
	_ = store.SaveStageOutcome(42, "implement", 1, &pipeline.StageOutcome{
		Status:  "success",
		Summary: "Implemented auth module with JWT tokens",
	})

	git := &mockGit{
		diff:         "+code change",
		diffSummary:  "2 files changed",
		filesChanged: "src/auth.ts\nsrc/login.ts",
		log:          "def5678 refactor: auth flow",
	}

	builder := NewBuilder(store, git)
	result, err := builder.Build(ps, BuildOpts{
		Issue:    42,
		Stage:    "review",
		StageCfg: &config.Stage{ID: "review", ContextMode: "code_only"},
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Mode != ModeCodeOnly {
		t.Errorf("expected code_only mode, got %q", result.Mode)
	}
	// Should have commits
	if result.Vars["git_commits"] != "def5678 refactor: auth flow" {
		t.Errorf("expected git commits, got %q", result.Vars["git_commits"])
	}
	// Should NOT have prior stage summary (fresh eyes)
	if _, ok := result.Vars["prior_stage_summary"]; ok {
		t.Error("code_only mode should not include prior_stage_summary")
	}
}

func TestBuild_FindingsOnlyMode(t *testing.T) {
	store := newTestStore(t)
	ps := newTestPipeline(t, store)

	// Add prior review stage with findings
	ps.StageHistory = []pipeline.StageHistoryEntry{
		{Stage: "review", Attempt: 1, Outcome: "fail"},
	}
	_ = store.SaveStageOutcome(42, "review", 1, &pipeline.StageOutcome{
		Status:  "fail",
		Summary: "Found security issues",
		Findings: []pipeline.Finding{
			{File: "src/auth.ts", Line: 42, Severity: "error", Message: "SQL injection", Rule: "no-sqli"},
			{File: "src/auth.ts", Line: 88, Severity: "warning", Message: "Missing rate limit"},
		},
	})

	builder := NewBuilder(store, &mockGit{diff: "should-not-appear"})
	result, err := builder.Build(ps, BuildOpts{
		Issue:    42,
		Stage:    "implement",
		StageCfg: &config.Stage{ID: "implement", ContextMode: "findings_only"},
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Mode != ModeFindingsOnly {
		t.Errorf("expected findings_only mode, got %q", result.Mode)
	}
	// Should NOT have git_diff
	if _, ok := result.Vars["git_diff"]; ok {
		t.Error("findings_only mode should not include git_diff")
	}
	// Should have findings as prior_stage_summary
	summary := result.Vars["prior_stage_summary"]
	if summary == "" {
		t.Fatal("expected prior_stage_summary with findings")
	}
	if !containsStr(summary, "SQL injection") {
		t.Errorf("expected SQL injection finding, got: %q", summary)
	}
	if !containsStr(summary, "Missing rate limit") {
		t.Errorf("expected rate limit finding, got: %q", summary)
	}
	if !containsStr(summary, "no-sqli") {
		t.Errorf("expected rule name in findings, got: %q", summary)
	}
}

func TestBuild_FindingsOnly_SummaryFallback(t *testing.T) {
	store := newTestStore(t)
	ps := newTestPipeline(t, store)

	// Prior stage with summary but no structured findings
	ps.StageHistory = []pipeline.StageHistoryEntry{
		{Stage: "review", Attempt: 1, Outcome: "fail"},
	}
	_ = store.SaveStageOutcome(42, "review", 1, &pipeline.StageOutcome{
		Status:  "fail",
		Summary: "Needs better error handling",
	})

	builder := NewBuilder(store, nil)
	result, err := builder.Build(ps, BuildOpts{
		Issue:    42,
		Stage:    "implement",
		StageCfg: &config.Stage{ID: "implement", ContextMode: "findings_only"},
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Vars["prior_stage_summary"] != "Needs better error handling" {
		t.Errorf("expected summary fallback, got: %q", result.Vars["prior_stage_summary"])
	}
}

func TestBuild_DefaultModeIsFull(t *testing.T) {
	store := newTestStore(t)
	ps := newTestPipeline(t, store)

	builder := NewBuilder(store, nil)
	result, err := builder.Build(ps, BuildOpts{
		Issue:    42,
		Stage:    "implement",
		StageCfg: &config.Stage{ID: "implement"}, // no ContextMode set
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Mode != ModeFull {
		t.Errorf("expected default mode to be full, got %q", result.Mode)
	}
}

func TestBuild_InvalidMode(t *testing.T) {
	store := newTestStore(t)
	ps := newTestPipeline(t, store)

	builder := NewBuilder(store, nil)
	_, err := builder.Build(ps, BuildOpts{
		Issue:    42,
		Stage:    "implement",
		StageCfg: &config.Stage{ID: "implement", ContextMode: "nonexistent"},
	})

	if err == nil {
		t.Fatal("expected error for invalid context mode")
	}
}

func TestBuild_TemplatePathDefault(t *testing.T) {
	store := newTestStore(t)
	ps := newTestPipeline(t, store)

	builder := NewBuilder(store, nil)
	result, err := builder.Build(ps, BuildOpts{
		Issue:    42,
		Stage:    "implement",
		StageCfg: &config.Stage{ID: "implement", ContextMode: "minimal"},
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Template != "implement.md" {
		t.Errorf("expected default template implement.md, got %q", result.Template)
	}
}

func TestBuild_CustomTemplatePath(t *testing.T) {
	store := newTestStore(t)
	ps := newTestPipeline(t, store)

	builder := NewBuilder(store, nil)
	result, err := builder.Build(ps, BuildOpts{
		Issue:    42,
		Stage:    "implement",
		StageCfg: &config.Stage{ID: "implement", ContextMode: "minimal", PromptTemplate: "custom/impl.md"},
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Template != "custom/impl.md" {
		t.Errorf("expected custom/impl.md, got %q", result.Template)
	}
}

func TestBuild_FullModeWithPriorStages(t *testing.T) {
	store := newTestStore(t)
	ps := newTestPipeline(t, store)

	// Add two prior stages
	ps.StageHistory = []pipeline.StageHistoryEntry{
		{Stage: "implement", Attempt: 1, Outcome: "success"},
		{Stage: "review", Attempt: 1, Outcome: "fail"},
	}
	_ = store.SaveStageOutcome(42, "implement", 1, &pipeline.StageOutcome{
		Status:       "success",
		Summary:      "Auth module implemented",
		FilesChanged: []string{"src/auth.ts", "src/login.ts"},
	})
	_ = store.SaveStageOutcome(42, "review", 1, &pipeline.StageOutcome{
		Status:  "fail",
		Summary: "Needs error handling",
	})

	git := &mockGit{diff: "+all changes"}
	builder := NewBuilder(store, git)
	result, err := builder.Build(ps, BuildOpts{
		Issue:    42,
		Stage:    "implement",
		StageCfg: &config.Stage{ID: "implement", ContextMode: "full"},
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	summary := result.Vars["prior_stage_summary"]
	if !containsStr(summary, "Auth module implemented") {
		t.Errorf("expected implement summary in prior stages, got: %q", summary)
	}
	if !containsStr(summary, "Needs error handling") {
		t.Errorf("expected review summary in prior stages, got: %q", summary)
	}
	if !containsStr(summary, "src/auth.ts") {
		t.Errorf("expected files changed in prior stages, got: %q", summary)
	}
}

func TestBuild_NilGitRunner(t *testing.T) {
	store := newTestStore(t)
	ps := newTestPipeline(t, store)

	builder := NewBuilder(store, nil)
	result, err := builder.Build(ps, BuildOpts{
		Issue:    42,
		Stage:    "implement",
		StageCfg: &config.Stage{ID: "implement", ContextMode: "full"},
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should work without git, just missing git-related vars
	if _, ok := result.Vars["git_diff"]; ok {
		t.Error("expected no git_diff when git is nil")
	}
}

func TestBuild_GoalString(t *testing.T) {
	store := newTestStore(t)
	ps := newTestPipeline(t, store)

	builder := NewBuilder(store, nil)
	result, err := builder.Build(ps, BuildOpts{
		Issue:    42,
		Stage:    "implement",
		StageCfg: &config.Stage{ID: "implement", ContextMode: "minimal"},
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Vars["goal"] != "#42: Add auth" {
		t.Errorf("expected '#42: Add auth', got %q", result.Vars["goal"])
	}
}

func TestIsValidMode(t *testing.T) {
	tests := []struct {
		input string
		valid bool
	}{
		{"full", true},
		{"code_only", true},
		{"findings_only", true},
		{"minimal", true},
		{"", false},
		{"unknown", false},
	}
	for _, tc := range tests {
		if got := IsValidMode(tc.input); got != tc.valid {
			t.Errorf("IsValidMode(%q) = %v, want %v", tc.input, got, tc.valid)
		}
	}
}

func TestCheckpoint(t *testing.T) {
	store := newTestStore(t)
	_ = newTestPipeline(t, store)

	git := &mockGit{
		diff:         "+new code",
		diffSummary:  "3 files changed",
		filesChanged: "a.go\nb.go\nc.go",
	}

	builder := NewBuilder(store, git)
	err := builder.Checkpoint(42, "implement", 1, CheckpointOpts{
		Status:  "success",
		Summary: "Implemented feature",
	})
	if err != nil {
		t.Fatalf("checkpoint error: %v", err)
	}

	// Verify outcome was saved
	outcome, err := store.GetStageOutcome(42, "implement", 1)
	if err != nil {
		t.Fatalf("get outcome: %v", err)
	}
	if outcome.Status != "success" {
		t.Errorf("expected status=success, got %q", outcome.Status)
	}
	if outcome.Summary != "Implemented feature" {
		t.Errorf("expected summary, got %q", outcome.Summary)
	}
	if outcome.DiffSummary != "3 files changed" {
		t.Errorf("expected diff summary, got %q", outcome.DiffSummary)
	}
	if len(outcome.FilesChanged) != 3 {
		t.Errorf("expected 3 files changed, got %d", len(outcome.FilesChanged))
	}
}

func TestCheckpoint_NilGit(t *testing.T) {
	store := newTestStore(t)
	_ = newTestPipeline(t, store)

	builder := NewBuilder(store, nil)
	err := builder.Checkpoint(42, "implement", 1, CheckpointOpts{
		Status:  "success",
		Summary: "Done",
	})
	if err != nil {
		t.Fatalf("checkpoint without git: %v", err)
	}

	outcome, err := store.GetStageOutcome(42, "implement", 1)
	if err != nil {
		t.Fatalf("get outcome: %v", err)
	}
	if outcome.Status != "success" {
		t.Errorf("expected status=success, got %q", outcome.Status)
	}
}

func TestReadContext(t *testing.T) {
	store := newTestStore(t)
	_ = newTestPipeline(t, store)

	// Save a prompt
	if err := store.SavePrompt(42, "implement", 1, "rendered prompt content"); err != nil {
		t.Fatalf("save prompt: %v", err)
	}

	builder := NewBuilder(store, nil)
	content, err := builder.ReadContext(42, "implement", 1)
	if err != nil {
		t.Fatalf("read context: %v", err)
	}
	if content != "rendered prompt content" {
		t.Errorf("expected prompt content, got %q", content)
	}
}

func TestReadContext_NotFound(t *testing.T) {
	store := newTestStore(t)
	_ = newTestPipeline(t, store)

	builder := NewBuilder(store, nil)
	_, err := builder.ReadContext(42, "implement", 1)
	if err == nil {
		t.Fatal("expected error for missing prompt")
	}
	if !os.IsNotExist(err) && !containsStr(err.Error(), "no such file") {
		t.Errorf("expected not-found error, got: %v", err)
	}
}

func TestBuild_CheckFailuresFromOutcome(t *testing.T) {
	store := newTestStore(t)
	ps := newTestPipeline(t, store)

	// Save a failed outcome for the current stage
	ps.StageHistory = []pipeline.StageHistoryEntry{
		{Stage: "implement", Attempt: 1, Outcome: "fail"},
	}
	_ = store.SaveStageOutcome(42, "implement", 1, &pipeline.StageOutcome{
		Status:  "fail",
		Summary: "lint: 3 errors\ntest: 2 failures",
	})

	builder := NewBuilder(store, nil)
	result, err := builder.Build(ps, BuildOpts{
		Issue:    42,
		Stage:    "implement",
		StageCfg: &config.Stage{ID: "implement", ContextMode: "minimal"},
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Vars["check_failures"] != "lint: 3 errors\ntest: 2 failures" {
		t.Errorf("expected check failures, got %q", result.Vars["check_failures"])
	}
}

func TestInstallAndLoadBuiltinTemplates(t *testing.T) {
	tmpDir := t.TempDir()
	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", origHome)

	// Verify built-in templates directory gets created
	tmplDir := filepath.Join(tmpDir, ".factory", "templates")
	if _, err := os.Stat(tmplDir); !os.IsNotExist(err) {
		t.Fatal("template dir should not exist yet")
	}
}

func TestBuild_CustomVars(t *testing.T) {
	store := newTestStore(t)
	ps := newTestPipeline(t, store)

	builder := NewBuilder(store, nil)
	result, err := builder.Build(ps, BuildOpts{
		Issue:    42,
		Stage:    "qa",
		StageCfg: &config.Stage{ID: "qa", ContextMode: "minimal", Vars: map[string]string{"stage_var": "from_stage"}},
		PipelineVars: map[string]string{
			"env_setup": "PostgreSQL on port 5433",
			"stage_var": "from_pipeline",
		},
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Pipeline var should be present
	if result.Vars["env_setup"] != "PostgreSQL on port 5433" {
		t.Errorf("expected pipeline var, got %q", result.Vars["env_setup"])
	}
	// Stage var should override pipeline var
	if result.Vars["stage_var"] != "from_stage" {
		t.Errorf("expected stage var to override pipeline var, got %q", result.Vars["stage_var"])
	}
}

func containsStr(s, substr string) bool {
	return strings.Contains(s, substr)
}
