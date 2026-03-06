package pipeline

import (
	"testing"
)

func TestDeployStoreCreateAndGet(t *testing.T) {
	s := NewDeployStore(t.TempDir())

	ds, err := s.Create(DeployCreateOpts{
		CommitSHA:   "abc123",
		Namespace:   "myorg/myapp",
		FirstStage:  "deploy",
		PreviousSHA: "def456",
		ConfigPath:  "/data/repos/myapp/pipeline.yaml",
		RepoDir:     "/data/repos/myapp",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if ds.CommitSHA != "abc123" {
		t.Errorf("CommitSHA = %q, want %q", ds.CommitSHA, "abc123")
	}
	if ds.PreviousSHA != "def456" {
		t.Errorf("PreviousSHA = %q, want %q", ds.PreviousSHA, "def456")
	}
	if ds.Status != "pending" {
		t.Errorf("Status = %q, want %q", ds.Status, "pending")
	}
	if ds.CurrentStage != "deploy" {
		t.Errorf("CurrentStage = %q, want %q", ds.CurrentStage, "deploy")
	}

	got, err := s.Get("abc123")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.CommitSHA != "abc123" {
		t.Errorf("Get CommitSHA = %q, want %q", got.CommitSHA, "abc123")
	}
	if got.PreviousSHA != "def456" {
		t.Errorf("Get PreviousSHA = %q, want %q", got.PreviousSHA, "def456")
	}
}

func TestDeployStoreCreateDuplicate(t *testing.T) {
	s := NewDeployStore(t.TempDir())
	_, _ = s.Create(DeployCreateOpts{CommitSHA: "abc123", FirstStage: "deploy"})
	_, err := s.Create(DeployCreateOpts{CommitSHA: "abc123", FirstStage: "deploy"})
	if err == nil {
		t.Fatal("expected error creating duplicate deploy")
	}
}

func TestDeployStoreUpdate(t *testing.T) {
	s := NewDeployStore(t.TempDir())
	_, _ = s.Create(DeployCreateOpts{CommitSHA: "abc123", FirstStage: "deploy"})

	err := s.Update("abc123", func(ds *DeployState) {
		ds.Status = "in_progress"
		ds.CurrentStage = "smoke-test"
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}

	got, _ := s.Get("abc123")
	if got.Status != "in_progress" {
		t.Errorf("Status = %q, want %q", got.Status, "in_progress")
	}
	if got.CurrentStage != "smoke-test" {
		t.Errorf("CurrentStage = %q, want %q", got.CurrentStage, "smoke-test")
	}
}

func TestDeployStoreList(t *testing.T) {
	s := NewDeployStore(t.TempDir())
	_, _ = s.Create(DeployCreateOpts{CommitSHA: "aaa", FirstStage: "deploy"})
	_, _ = s.Create(DeployCreateOpts{CommitSHA: "bbb", FirstStage: "deploy"})
	_ = s.Update("bbb", func(ds *DeployState) { ds.Status = "completed" })

	all, _ := s.List("")
	if len(all) != 2 {
		t.Fatalf("List all = %d, want 2", len(all))
	}

	completed, _ := s.List("completed")
	if len(completed) != 1 {
		t.Fatalf("List completed = %d, want 1", len(completed))
	}
}

func TestDeployStoreGetNotFound(t *testing.T) {
	s := NewDeployStore(t.TempDir())
	_, err := s.Get("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent deploy")
	}
}

func TestDeployStoreStageAttemptDir(t *testing.T) {
	s := NewDeployStore(t.TempDir())
	_, _ = s.Create(DeployCreateOpts{CommitSHA: "abc123", FirstStage: "deploy"})

	err := s.InitStageAttempt("abc123", "deploy", 1)
	if err != nil {
		t.Fatalf("InitStageAttempt: %v", err)
	}

	err = s.SavePrompt("abc123", "deploy", 1, "Deploy this commit")
	if err != nil {
		t.Fatalf("SavePrompt: %v", err)
	}
}
