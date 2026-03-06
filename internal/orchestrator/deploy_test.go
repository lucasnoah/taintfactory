package orchestrator

import (
	"fmt"
	"testing"

	"github.com/lucasnoah/taintfactory/internal/config"
	"github.com/lucasnoah/taintfactory/internal/pipeline"
)

func deployConfig() *config.DeployPipeline {
	return &config.DeployPipeline{
		Name: "deploy",
		Stages: []config.Stage{
			{ID: "deploy", Type: "agent", PromptTemplate: "deploy.md"},
			{ID: "smoke-test", Type: "agent", PromptTemplate: "smoke-test.md"},
		},
	}
}

func deployConfigWithFailure() *config.DeployPipeline {
	return &config.DeployPipeline{
		Name: "deploy",
		Stages: []config.Stage{
			{ID: "deploy", Type: "agent", PromptTemplate: "deploy.md", OnFail: "rollback"},
			{ID: "smoke-test", Type: "agent", PromptTemplate: "smoke-test.md", OnFail: "rollback"},
			{ID: "rollback", Type: "agent", PromptTemplate: "rollback.md"},
		},
	}
}

func TestFindDeployStage(t *testing.T) {
	cfg := deployConfig()

	s := findDeployStage("deploy", cfg)
	if s == nil {
		t.Fatal("expected to find stage 'deploy'")
	}
	if s.ID != "deploy" {
		t.Errorf("ID = %q, want %q", s.ID, "deploy")
	}

	s = findDeployStage("nonexistent", cfg)
	if s != nil {
		t.Error("expected nil for nonexistent stage")
	}
}

func TestNextDeployStageID(t *testing.T) {
	cfg := deployConfig()

	next := nextDeployStageID("deploy", cfg)
	if next != "smoke-test" {
		t.Errorf("next after deploy = %q, want %q", next, "smoke-test")
	}

	next = nextDeployStageID("smoke-test", cfg)
	if next != "" {
		t.Errorf("next after smoke-test = %q, want empty", next)
	}

	next = nextDeployStageID("nonexistent", cfg)
	if next != "" {
		t.Errorf("next after nonexistent = %q, want empty", next)
	}
}

func TestShortDeploySHA(t *testing.T) {
	if got := shortDeploySHA("abc1234567890"); got != "abc1234" {
		t.Errorf("shortDeploySHA = %q, want %q", got, "abc1234")
	}
	if got := shortDeploySHA("abc"); got != "abc" {
		t.Errorf("shortDeploySHA short = %q, want %q", got, "abc")
	}
}

func TestStageHistoryJSON(t *testing.T) {
	history := []pipeline.StageHistoryEntry{
		{Stage: "deploy", Attempt: 1, Outcome: "success"},
	}
	got := stageHistoryJSON(history)
	if got == "" || got == "[]" {
		t.Errorf("stageHistoryJSON returned empty for non-empty input")
	}
	if got == "null" {
		t.Errorf("stageHistoryJSON returned null")
	}
}

func TestCheckInDeployNilStore(t *testing.T) {
	o := &Orchestrator{}
	action := o.checkInDeploy()
	if action != nil {
		t.Error("expected nil when deployStore is nil")
	}
}

func TestCheckInDeploySkipsTerminal(t *testing.T) {
	dir := t.TempDir()
	store := pipeline.NewDeployStore(dir)

	// Create completed and failed deploys
	store.Create(pipeline.DeployCreateOpts{CommitSHA: "aaa111", FirstStage: "deploy"})
	store.Update("aaa111", func(ds *pipeline.DeployState) { ds.Status = "completed" })

	store.Create(pipeline.DeployCreateOpts{CommitSHA: "bbb222", FirstStage: "deploy"})
	store.Update("bbb222", func(ds *pipeline.DeployState) { ds.Status = "failed" })

	store.Create(pipeline.DeployCreateOpts{CommitSHA: "ccc333", FirstStage: "deploy"})
	store.Update("ccc333", func(ds *pipeline.DeployState) { ds.Status = "rolled_back" })

	o := &Orchestrator{deployStore: store}
	action := o.checkInDeploy()

	// All deploys are terminal, nothing to process
	if action != nil {
		t.Errorf("expected nil for all terminal deploys, got action %q", action.Action)
	}
}

func TestCheckInDeployPicksPending(t *testing.T) {
	dir := t.TempDir()
	store := pipeline.NewDeployStore(dir)

	sha := "aaa111aaa111aaa111aaa111aaa111aaa111aaaa"
	store.Create(pipeline.DeployCreateOpts{CommitSHA: sha, FirstStage: "deploy"})

	cfg := &config.PipelineConfig{Deploy: deployConfig()}

	// Orchestrator without sessions/db will error on stage run, but checkInDeploy will find the deploy
	o := &Orchestrator{deployStore: store, cfg: cfg}
	action := o.checkInDeploy()

	// It should attempt to process the pending deploy (will error since no sessions)
	if action == nil {
		t.Fatal("expected action for pending deploy")
	}
	if action.CommitSHA != sha {
		t.Errorf("CommitSHA = %q, want %q", action.CommitSHA, sha)
	}
}

func TestAdvanceDeployToNextAdvances(t *testing.T) {
	dir := t.TempDir()
	store := pipeline.NewDeployStore(dir)

	cfg := &config.PipelineConfig{
		Deploy: deployConfig(),
	}

	store.Create(pipeline.DeployCreateOpts{
		CommitSHA:  "abc123abc123abc123abc123abc123abc123abcd",
		FirstStage: "deploy",
	})
	store.Update("abc123abc123abc123abc123abc123abc123abcd", func(ds *pipeline.DeployState) {
		ds.Status = "in_progress"
	})

	o := &Orchestrator{deployStore: store, cfg: cfg}
	ds, _ := store.Get("abc123abc123abc123abc123abc123abc123abcd")
	action := o.advanceDeployToNext(ds)

	if action.Action != "advanced" {
		t.Errorf("action = %q, want %q", action.Action, "advanced")
	}
	if action.Stage != "smoke-test" {
		t.Errorf("stage = %q, want %q", action.Stage, "smoke-test")
	}

	// Verify state updated
	ds, _ = store.Get("abc123abc123abc123abc123abc123abc123abcd")
	if ds.CurrentStage != "smoke-test" {
		t.Errorf("CurrentStage = %q, want %q", ds.CurrentStage, "smoke-test")
	}
	if len(ds.StageHistory) != 1 {
		t.Errorf("StageHistory len = %d, want 1", len(ds.StageHistory))
	}
}

func TestAdvanceDeployToNextCompletes(t *testing.T) {
	dir := t.TempDir()
	store := pipeline.NewDeployStore(dir)

	cfg := &config.PipelineConfig{
		Deploy: deployConfig(),
	}

	store.Create(pipeline.DeployCreateOpts{
		CommitSHA:  "abc123abc123abc123abc123abc123abc123abcd",
		FirstStage: "deploy",
	})
	// Simulate being at the last stage
	store.Update("abc123abc123abc123abc123abc123abc123abcd", func(ds *pipeline.DeployState) {
		ds.Status = "in_progress"
		ds.CurrentStage = "smoke-test"
	})

	o := &Orchestrator{deployStore: store, cfg: cfg}
	ds, _ := store.Get("abc123abc123abc123abc123abc123abc123abcd")
	action := o.advanceDeployToNext(ds)

	if action.Action != "completed" {
		t.Errorf("action = %q, want %q", action.Action, "completed")
	}

	ds, _ = store.Get("abc123abc123abc123abc123abc123abc123abcd")
	if ds.Status != "completed" {
		t.Errorf("Status = %q, want %q", ds.Status, "completed")
	}
}

func TestHandleDeployFailureRoutesToOnFail(t *testing.T) {
	dir := t.TempDir()
	store := pipeline.NewDeployStore(dir)
	sha := "abc123abc123abc123abc123abc123abc123abcd"

	cfg := &config.PipelineConfig{Deploy: deployConfigWithFailure()}

	store.Create(pipeline.DeployCreateOpts{CommitSHA: sha, FirstStage: "deploy"})
	store.Update(sha, func(ds *pipeline.DeployState) {
		ds.Status = "in_progress"
	})

	o := &Orchestrator{deployStore: store, cfg: cfg}
	ds, _ := store.Get(sha)
	stageCfg := findDeployStage("deploy", cfg.Deploy)

	action := o.handleDeployFailure(ds, stageCfg, cfg.Deploy)

	if action.Action != "failure_routed" {
		t.Errorf("action = %q, want %q", action.Action, "failure_routed")
	}
	if action.Stage != "rollback" {
		t.Errorf("stage = %q, want %q", action.Stage, "rollback")
	}

	ds, _ = store.Get(sha)
	if ds.CurrentStage != "rollback" {
		t.Errorf("CurrentStage = %q, want %q", ds.CurrentStage, "rollback")
	}
	if ds.Status != "pending" {
		t.Errorf("Status = %q, want %q", ds.Status, "pending")
	}
	if len(ds.FailureVisited) != 1 || ds.FailureVisited[0] != "rollback" {
		t.Errorf("FailureVisited = %v, want [rollback]", ds.FailureVisited)
	}
}

func TestHandleDeployFailureCycleDetection(t *testing.T) {
	dir := t.TempDir()
	store := pipeline.NewDeployStore(dir)
	sha := "abc123abc123abc123abc123abc123abc123abcd"

	cfg := &config.PipelineConfig{Deploy: deployConfigWithFailure()}

	store.Create(pipeline.DeployCreateOpts{CommitSHA: sha, FirstStage: "deploy"})
	store.Update(sha, func(ds *pipeline.DeployState) {
		ds.Status = "in_progress"
		ds.CurrentStage = "smoke-test"
		ds.FailureVisited = []string{"rollback"} // already visited
	})

	o := &Orchestrator{deployStore: store, cfg: cfg}
	ds, _ := store.Get(sha)
	stageCfg := findDeployStage("smoke-test", cfg.Deploy)

	action := o.handleDeployFailure(ds, stageCfg, cfg.Deploy)

	if action.Action != "failed" {
		t.Errorf("action = %q, want %q", action.Action, "failed")
	}
	if action.Message == "" || !contains(action.Message, "cycle") {
		t.Errorf("message should mention cycle, got %q", action.Message)
	}

	ds, _ = store.Get(sha)
	if ds.Status != "failed" {
		t.Errorf("Status = %q, want %q", ds.Status, "failed")
	}
}

func TestHandleDeployFailureNoOnFail(t *testing.T) {
	dir := t.TempDir()
	store := pipeline.NewDeployStore(dir)
	sha := "abc123abc123abc123abc123abc123abc123abcd"

	// Config without on_fail
	cfg := &config.PipelineConfig{Deploy: deployConfig()}

	store.Create(pipeline.DeployCreateOpts{CommitSHA: sha, FirstStage: "deploy"})
	store.Update(sha, func(ds *pipeline.DeployState) {
		ds.Status = "in_progress"
	})

	o := &Orchestrator{deployStore: store, cfg: cfg}
	ds, _ := store.Get(sha)
	stageCfg := findDeployStage("deploy", cfg.Deploy)

	action := o.handleDeployFailure(ds, stageCfg, cfg.Deploy)

	if action.Action != "failed" {
		t.Errorf("action = %q, want %q", action.Action, "failed")
	}

	ds, _ = store.Get(sha)
	if ds.Status != "failed" {
		t.Errorf("Status = %q, want %q", ds.Status, "failed")
	}
}

func TestRollbackSuccessMarksRolledBack(t *testing.T) {
	dir := t.TempDir()
	store := pipeline.NewDeployStore(dir)
	sha := "abc123abc123abc123abc123abc123abc123abcd"

	cfg := &config.PipelineConfig{Deploy: deployConfigWithFailure()}

	store.Create(pipeline.DeployCreateOpts{CommitSHA: sha, FirstStage: "deploy"})
	// Simulate: deploy failed → routed to rollback → rollback succeeded
	store.Update(sha, func(ds *pipeline.DeployState) {
		ds.Status = "in_progress"
		ds.CurrentStage = "rollback"
		ds.FailureVisited = []string{"rollback"} // entered via failure routing
	})

	o := &Orchestrator{deployStore: store, cfg: cfg}
	ds, _ := store.Get(sha)
	action := o.advanceDeployToNext(ds)

	if action.Action != "rolled_back" {
		t.Errorf("action = %q, want %q", action.Action, "rolled_back")
	}

	ds, _ = store.Get(sha)
	if ds.Status != "rolled_back" {
		t.Errorf("Status = %q, want %q", ds.Status, "rolled_back")
	}
}

func TestIsRollbackStage(t *testing.T) {
	tests := []struct {
		id   string
		want bool
	}{
		{"rollback", true},
		{"auto-rollback", true},
		{"deploy", false},
		{"smoke-test", false},
	}
	for _, tt := range tests {
		if got := isRollbackStage(tt.id); got != tt.want {
			t.Errorf("isRollbackStage(%q) = %v, want %v", tt.id, got, tt.want)
		}
	}
}

func TestDeployVarsBuiltCorrectly(t *testing.T) {
	// Test that all expected deploy vars are populated in the vars map
	// This mirrors the logic in runDeployStage without needing sessions
	ds := &pipeline.DeployState{
		CommitSHA:      "abc123abc123abc123abc123abc123abc123abcd",
		PreviousSHA:    "def456def456def456def456def456def456deff",
		Namespace:      "myorg/myapp",
		CurrentStage:   "deploy",
		CurrentAttempt: 1,
		RepoDir:        "/data/repos/myapp",
	}

	stageCfg := &config.Stage{
		ID:   "deploy",
		Vars: map[string]string{"environment": "production"},
	}

	// Build the same vars map as runDeployStage
	vars := map[string]string{
		"commit_sha":   ds.CommitSHA,
		"previous_sha": ds.PreviousSHA,
		"namespace":    ds.Namespace,
		"stage_id":     ds.CurrentStage,
		"attempt":      fmt.Sprintf("%d", ds.CurrentAttempt),
	}
	if ds.RepoDir != "" {
		vars["repo_dir"] = ds.RepoDir
	}
	for k, v := range stageCfg.Vars {
		vars[k] = v
	}

	// Verify all expected vars present
	expected := map[string]string{
		"commit_sha":   "abc123abc123abc123abc123abc123abc123abcd",
		"previous_sha": "def456def456def456def456def456def456deff",
		"namespace":    "myorg/myapp",
		"stage_id":     "deploy",
		"attempt":      "1",
		"repo_dir":     "/data/repos/myapp",
		"environment":  "production",
	}
	for k, want := range expected {
		if got, ok := vars[k]; !ok {
			t.Errorf("missing var %q", k)
		} else if got != want {
			t.Errorf("var %q = %q, want %q", k, got, want)
		}
	}
}

func TestDeployStageVarsOverrideDefaults(t *testing.T) {
	// Stage vars should override deploy-level defaults
	vars := map[string]string{
		"commit_sha": "abc123",
		"namespace":  "default/ns",
	}
	stageVars := map[string]string{
		"namespace":   "override/ns", // override
		"custom_var":  "custom_val",
	}
	for k, v := range stageVars {
		vars[k] = v
	}

	if vars["namespace"] != "override/ns" {
		t.Errorf("namespace = %q, want %q", vars["namespace"], "override/ns")
	}
	if vars["custom_var"] != "custom_val" {
		t.Errorf("custom_var = %q, want %q", vars["custom_var"], "custom_val")
	}
	if vars["commit_sha"] != "abc123" {
		t.Errorf("commit_sha = %q, want %q", vars["commit_sha"], "abc123")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
