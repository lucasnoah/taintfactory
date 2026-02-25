package pipeline

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	return NewStore(t.TempDir())
}

func c(issue int, title, branch, worktree, firstStage string, gates map[string]string) CreateOpts {
	return CreateOpts{Issue: issue, Title: title, Branch: branch, Worktree: worktree, FirstStage: firstStage, GoalGates: gates}
}

func TestCreateAndGet(t *testing.T) {
	s := newTestStore(t)

	gates := map[string]string{"lint": "pass", "test": "pass"}
	ps, err := s.Create(c(42, "Add widget", "feature/42", "/tmp/wt-42", "plan", gates))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if ps.Issue != 42 {
		t.Errorf("Issue = %d, want 42", ps.Issue)
	}
	if ps.Title != "Add widget" {
		t.Errorf("Title = %q, want %q", ps.Title, "Add widget")
	}
	if ps.Branch != "feature/42" {
		t.Errorf("Branch = %q, want %q", ps.Branch, "feature/42")
	}
	if ps.Worktree != "/tmp/wt-42" {
		t.Errorf("Worktree = %q, want %q", ps.Worktree, "/tmp/wt-42")
	}
	if ps.CurrentStage != "plan" {
		t.Errorf("CurrentStage = %q, want %q", ps.CurrentStage, "plan")
	}
	if ps.CurrentAttempt != 1 {
		t.Errorf("CurrentAttempt = %d, want 1", ps.CurrentAttempt)
	}
	if ps.Status != "pending" {
		t.Errorf("Status = %q, want %q", ps.Status, "pending")
	}
	if ps.CreatedAt == "" {
		t.Error("CreatedAt should not be empty")
	}
	if len(ps.GoalGates) != 2 {
		t.Errorf("GoalGates has %d entries, want 2", len(ps.GoalGates))
	}

	// Round-trip through disk.
	got, err := s.Get(42)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Title != "Add widget" {
		t.Errorf("Get Title = %q, want %q", got.Title, "Add widget")
	}
	if got.GoalGates["lint"] != "pass" {
		t.Errorf("GoalGates[lint] = %q, want %q", got.GoalGates["lint"], "pass")
	}
}

func TestCreateDuplicate(t *testing.T) {
	s := newTestStore(t)

	_, err := s.Create(c(1, "First", "b", "w", "plan", nil))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	_, err = s.Create(c(1, "Duplicate", "b", "w", "plan", nil))
	if err == nil {
		t.Fatal("expected error creating duplicate pipeline")
	}
}

func TestGetNotFound(t *testing.T) {
	s := newTestStore(t)

	_, err := s.Get(999)
	if err == nil {
		t.Fatal("expected error for non-existent pipeline")
	}
}

func TestUpdate(t *testing.T) {
	s := newTestStore(t)

	_, err := s.Create(c(10, "Test", "b", "w", "plan", nil))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	err = s.Update(10, func(ps *PipelineState) {
		ps.Status = "in_progress"
		ps.CurrentStage = "code"
		ps.CurrentAttempt = 2
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}

	got, err := s.Get(10)
	if err != nil {
		t.Fatalf("Get after Update: %v", err)
	}
	if got.Status != "in_progress" {
		t.Errorf("Status = %q, want %q", got.Status, "in_progress")
	}
	if got.CurrentStage != "code" {
		t.Errorf("CurrentStage = %q, want %q", got.CurrentStage, "code")
	}
	if got.CurrentAttempt != 2 {
		t.Errorf("CurrentAttempt = %d, want 2", got.CurrentAttempt)
	}
	if got.UpdatedAt == "" {
		t.Error("UpdatedAt should not be empty after Update")
	}
}

func TestUpdateNotFound(t *testing.T) {
	s := newTestStore(t)

	err := s.Update(999, func(ps *PipelineState) {
		ps.Status = "failed"
	})
	if err == nil {
		t.Fatal("expected error updating non-existent pipeline")
	}
}

func TestListAll(t *testing.T) {
	s := newTestStore(t)

	_, _ = s.Create(c(1, "A", "b1", "w1", "plan", nil))
	_, _ = s.Create(c(2, "B", "b2", "w2", "plan", nil))
	_ = s.Update(2, func(ps *PipelineState) { ps.Status = "in_progress" })
	_, _ = s.Create(c(3, "C", "b3", "w3", "plan", nil))

	all, err := s.List("")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("List returned %d, want 3", len(all))
	}
	// Verify sorted by issue number.
	for i := 0; i < len(all)-1; i++ {
		if all[i].Issue >= all[i+1].Issue {
			t.Errorf("List not sorted: issue %d before %d", all[i].Issue, all[i+1].Issue)
		}
	}
}

func TestListWithFilter(t *testing.T) {
	s := newTestStore(t)

	_, _ = s.Create(c(1, "A", "b1", "w1", "plan", nil))
	_, _ = s.Create(c(2, "B", "b2", "w2", "plan", nil))
	_ = s.Update(2, func(ps *PipelineState) { ps.Status = "in_progress" })

	pending, err := s.List("pending")
	if err != nil {
		t.Fatalf("List pending: %v", err)
	}
	if len(pending) != 1 {
		t.Errorf("List pending returned %d, want 1", len(pending))
	}
	if len(pending) > 0 && pending[0].Issue != 1 {
		t.Errorf("pending[0].Issue = %d, want 1", pending[0].Issue)
	}

	inProgress, err := s.List("in_progress")
	if err != nil {
		t.Fatalf("List in_progress: %v", err)
	}
	if len(inProgress) != 1 {
		t.Errorf("List in_progress returned %d, want 1", len(inProgress))
	}

	completed, err := s.List("completed")
	if err != nil {
		t.Fatalf("List completed: %v", err)
	}
	if len(completed) != 0 {
		t.Errorf("List completed returned %d, want 0", len(completed))
	}
}

func TestListEmpty(t *testing.T) {
	s := newTestStore(t)

	all, err := s.List("")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 0 {
		t.Errorf("List returned %d, want 0", len(all))
	}
}

func TestDelete(t *testing.T) {
	s := newTestStore(t)

	_, _ = s.Create(c(5, "Delete me", "b", "w", "plan", nil))

	err := s.Delete(5)
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}

	_, err = s.Get(5)
	if err == nil {
		t.Fatal("expected error after Delete")
	}

	// Verify directory is gone.
	if _, err := os.Stat(s.issueDir("", 5)); !os.IsNotExist(err) {
		t.Error("issue directory should not exist after Delete")
	}
}

func TestDeleteNotFound(t *testing.T) {
	s := newTestStore(t)

	err := s.Delete(999)
	if err == nil {
		t.Fatal("expected error deleting non-existent pipeline")
	}
}

func TestInitStageAttempt(t *testing.T) {
	s := newTestStore(t)

	_, _ = s.Create(c(7, "Stage test", "b", "w", "code", nil))
	err := s.InitStageAttempt(7, "code", 1)
	if err != nil {
		t.Fatalf("InitStageAttempt: %v", err)
	}

	dir := s.stageAttemptDir(7, "code", 1)
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		t.Error("attempt directory should exist")
	}
	checksDir := filepath.Join(dir, "checks")
	if _, err := os.Stat(checksDir); os.IsNotExist(err) {
		t.Error("checks subdirectory should exist")
	}
}

func TestInitStageAttemptPipelineNotFound(t *testing.T) {
	s := newTestStore(t)

	err := s.InitStageAttempt(999, "code", 1)
	if err == nil {
		t.Fatal("expected error for non-existent pipeline")
	}
}

func TestSaveAndGetStageOutcome(t *testing.T) {
	s := newTestStore(t)

	_, _ = s.Create(c(8, "Outcome test", "b", "w", "lint", nil))

	outcome := &StageOutcome{
		Status:       "success",
		Summary:      "All checks passed",
		FilesChanged: []string{"main.go", "util.go"},
		DiffSummary:  "+10 -3",
		Findings: []Finding{
			{File: "main.go", Line: 42, Severity: "warning", Message: "unused var", Rule: "SA4006"},
		},
		ContextUpdates: map[string]string{"coverage": "92%"},
	}

	err := s.SaveStageOutcome(8, "lint", 1, outcome)
	if err != nil {
		t.Fatalf("SaveStageOutcome: %v", err)
	}

	got, err := s.GetStageOutcome(8, "lint", 1)
	if err != nil {
		t.Fatalf("GetStageOutcome: %v", err)
	}
	if got.Status != "success" {
		t.Errorf("Status = %q, want %q", got.Status, "success")
	}
	if got.Summary != "All checks passed" {
		t.Errorf("Summary = %q, want %q", got.Summary, "All checks passed")
	}
	if len(got.FilesChanged) != 2 {
		t.Errorf("FilesChanged has %d entries, want 2", len(got.FilesChanged))
	}
	if len(got.Findings) != 1 {
		t.Errorf("Findings has %d entries, want 1", len(got.Findings))
	}
	if got.Findings[0].Rule != "SA4006" {
		t.Errorf("Findings[0].Rule = %q, want %q", got.Findings[0].Rule, "SA4006")
	}
	if got.ContextUpdates["coverage"] != "92%" {
		t.Errorf("ContextUpdates[coverage] = %q, want %q", got.ContextUpdates["coverage"], "92%")
	}
}

func TestGetStageOutcomeNotFound(t *testing.T) {
	s := newTestStore(t)

	_, err := s.GetStageOutcome(999, "code", 1)
	if err == nil {
		t.Fatal("expected error for non-existent outcome")
	}
}

func TestSaveStageSummary(t *testing.T) {
	s := newTestStore(t)

	_, _ = s.Create(c(9, "Summary test", "b", "w", "code", nil))

	summary := &StageSummary{
		Stage:           "code",
		Attempt:         1,
		Outcome:         "success",
		AgentDuration:   "2m30s",
		TotalDuration:   "3m15s",
		FixRounds:       2,
		ChecksFirstPass: false,
		AutoFixes:       map[string]int{"gofmt": 3},
		AgentFixes:      map[string]int{"test": 1},
		FinalCheckState: map[string]string{"lint": "pass", "test": "pass"},
	}

	err := s.SaveStageSummary(9, "code", 1, summary)
	if err != nil {
		t.Fatalf("SaveStageSummary: %v", err)
	}

	// Verify the file exists and is valid JSON.
	var got StageSummary
	path := filepath.Join(s.stageAttemptDir(9, "code", 1), "summary.json")
	if err := ReadJSON(path, &got); err != nil {
		t.Fatalf("ReadJSON summary: %v", err)
	}
	if got.FixRounds != 2 {
		t.Errorf("FixRounds = %d, want 2", got.FixRounds)
	}
	if got.FinalCheckState["test"] != "pass" {
		t.Errorf("FinalCheckState[test] = %q, want %q", got.FinalCheckState["test"], "pass")
	}
}

func TestSaveAndGetPrompt(t *testing.T) {
	s := newTestStore(t)

	_, _ = s.Create(c(11, "Prompt test", "b", "w", "plan", nil))

	prompt := "# Stage: plan\n\nImplement the widget feature.\n\n## Requirements\n- Must be fast\n- Must be correct\n"

	err := s.SavePrompt(11, "plan", 1, prompt)
	if err != nil {
		t.Fatalf("SavePrompt: %v", err)
	}

	got, err := s.GetPrompt(11, "plan", 1)
	if err != nil {
		t.Fatalf("GetPrompt: %v", err)
	}
	if got != prompt {
		t.Errorf("GetPrompt returned %q, want %q", got, prompt)
	}
}

func TestGetPromptNotFound(t *testing.T) {
	s := newTestStore(t)

	_, err := s.GetPrompt(999, "plan", 1)
	if err == nil {
		t.Fatal("expected error for non-existent prompt")
	}
}

func TestAtomicWriteCleanup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.json")

	data := []byte(`{"key": "value"}`)
	if err := WriteAtomic(path, data); err != nil {
		t.Fatalf("WriteAtomic: %v", err)
	}

	// Verify the final file has correct content.
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != string(data) {
		t.Errorf("file content = %q, want %q", got, data)
	}

	// Verify no temp files remain.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if e.Name() != "test.json" {
			t.Errorf("unexpected file remaining: %s", e.Name())
		}
	}
}

func TestWriteAndReadJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "data.json")

	type testData struct {
		Name  string `json:"name"`
		Count int    `json:"count"`
	}

	input := testData{Name: "hello", Count: 42}
	if err := WriteJSON(path, &input); err != nil {
		t.Fatalf("WriteJSON: %v", err)
	}

	var output testData
	if err := ReadJSON(path, &output); err != nil {
		t.Fatalf("ReadJSON: %v", err)
	}
	if output.Name != "hello" || output.Count != 42 {
		t.Errorf("ReadJSON got %+v, want %+v", output, input)
	}
}

func TestConcurrentUpdates(t *testing.T) {
	s := newTestStore(t)

	_, err := s.Create(c(20, "Concurrent", "b", "w", "plan", nil))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Run concurrent updates; verify no corruption.
	var wg sync.WaitGroup
	errs := make([]error, 10)
	for i := 0; i < 10; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = s.Update(20, func(ps *PipelineState) {
				ps.CurrentAttempt = i
			})
		}()
	}
	wg.Wait()

	// At least some should succeed; the final state should be valid JSON.
	got, err := s.Get(20)
	if err != nil {
		t.Fatalf("Get after concurrent updates: %v", err)
	}
	if got.Issue != 20 {
		t.Errorf("Issue = %d, want 20 (state corrupted)", got.Issue)
	}
	// Status should still be valid.
	if got.Status == "" {
		t.Error("Status should not be empty after concurrent updates")
	}
}

func TestDirectoryStructure(t *testing.T) {
	s := newTestStore(t)

	_, _ = s.Create(c(50, "Dir test", "b", "w", "code", nil))
	_ = s.InitStageAttempt(50, "code", 1)
	_ = s.SavePrompt(50, "code", 1, "test prompt")
	_ = s.SaveStageOutcome(50, "code", 1, &StageOutcome{Status: "success"})
	_ = s.SaveStageSummary(50, "code", 1, &StageSummary{Stage: "code"})

	// Verify expected directory structure.
	paths := []string{
		s.issueDir("", 50),
		filepath.Join(s.issueDir("", 50), "pipeline.json"),
		filepath.Join(s.issueDir("", 50), "stages"),
		filepath.Join(s.issueDir("", 50), "stages", "code"),
		s.stageAttemptDir(50, "code", 1),
		filepath.Join(s.stageAttemptDir(50, "code", 1), "checks"),
		filepath.Join(s.stageAttemptDir(50, "code", 1), "prompt.md"),
		filepath.Join(s.stageAttemptDir(50, "code", 1), "outcome.json"),
		filepath.Join(s.stageAttemptDir(50, "code", 1), "summary.json"),
	}
	for _, p := range paths {
		if _, err := os.Stat(p); os.IsNotExist(err) {
			t.Errorf("expected path to exist: %s", p)
		}
	}
}

func TestCreatePreservesConfigFields(t *testing.T) {
	s := newTestStore(t)

	ps, err := s.Create(CreateOpts{
		Issue:      55,
		Title:      "Multi-project test",
		Branch:     "feature/55",
		Worktree:   "/tmp/wt-55",
		FirstStage: "implement",
		GoalGates:  map[string]string{},
		ConfigPath: "/projects/myapp/pipeline.yaml",
		RepoDir:    "/projects/myapp",
		Namespace:  "myorg/myapp",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if ps.ConfigPath != "/projects/myapp/pipeline.yaml" {
		t.Errorf("ConfigPath = %q, want %q", ps.ConfigPath, "/projects/myapp/pipeline.yaml")
	}
	if ps.RepoDir != "/projects/myapp" {
		t.Errorf("RepoDir = %q, want %q", ps.RepoDir, "/projects/myapp")
	}
	if ps.Namespace != "myorg/myapp" {
		t.Errorf("Namespace = %q, want %q", ps.Namespace, "myorg/myapp")
	}

	// Round-trip through disk
	got, err := s.Get(55)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ConfigPath != "/projects/myapp/pipeline.yaml" {
		t.Errorf("after Get: ConfigPath = %q", got.ConfigPath)
	}
	if got.Namespace != "myorg/myapp" {
		t.Errorf("after Get: Namespace = %q", got.Namespace)
	}
}

func TestNamespacedStorageIsolatesIssues(t *testing.T) {
	s := newTestStore(t)

	// Two repos with the same issue number — should not collide
	_, err := s.Create(CreateOpts{Issue: 1, Title: "Repo A", Branch: "b", Worktree: "w", FirstStage: "impl", Namespace: "org/repo-a"})
	if err != nil {
		t.Fatalf("Create repo-a: %v", err)
	}
	_, err = s.Create(CreateOpts{Issue: 1, Title: "Repo B", Branch: "b", Worktree: "w", FirstStage: "impl", Namespace: "org/repo-b"})
	if err != nil {
		t.Fatalf("Create repo-b: %v", err)
	}
	// Both should be accessible (different namespaces)
	all, err := s.List("")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 2 {
		t.Errorf("List returned %d pipelines, want 2", len(all))
	}
}
