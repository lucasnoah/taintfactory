package web

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lucasnoah/taintfactory/internal/pipeline"
)

func TestConfigForPS_UsesConfigPath(t *testing.T) {
	dir := t.TempDir()
	cfgContent := `
pipeline:
  name: test-project
  repo: github.com/org/test-project
  stages:
    - id: impl
      type: agent
`
	cfgPath := filepath.Join(dir, "pipeline.yaml")
	if err := os.WriteFile(cfgPath, []byte(cfgContent), 0644); err != nil {
		t.Fatal(err)
	}

	s := NewServer(nil, nil, 0, "")

	ps := &pipeline.PipelineState{
		Issue:      1,
		ConfigPath: cfgPath,
		RepoDir:    dir,
	}

	cfg := s.configForPS(ps)
	if cfg == nil {
		t.Fatal("expected non-nil config when ConfigPath is set")
	}
	if cfg.Pipeline.Name != "test-project" {
		t.Errorf("expected name 'test-project', got %q", cfg.Pipeline.Name)
	}
}

func TestConfigForPS_CachesResult(t *testing.T) {
	dir := t.TempDir()
	cfgContent := `
pipeline:
  name: cached-project
  repo: github.com/org/cached
`
	cfgPath := filepath.Join(dir, "pipeline.yaml")
	if err := os.WriteFile(cfgPath, []byte(cfgContent), 0644); err != nil {
		t.Fatal(err)
	}

	s := NewServer(nil, nil, 0, "")
	ps := &pipeline.PipelineState{
		Issue:      2,
		ConfigPath: cfgPath,
		RepoDir:    dir,
	}

	cfg1 := s.configForPS(ps)
	// Remove the file to prove subsequent call is served from cache
	os.Remove(cfgPath)
	cfg2 := s.configForPS(ps)

	if cfg1 == nil || cfg2 == nil {
		t.Fatal("expected non-nil configs")
	}
	if cfg1 != cfg2 {
		t.Error("expected same pointer (cached)")
	}
}

func TestConfigForPS_FallsBackToWorktreeWhenNoConfigPath(t *testing.T) {
	s := NewServer(nil, nil, 0, "")

	// No ConfigPath and empty Worktree: should return nil gracefully
	ps := &pipeline.PipelineState{
		Issue:    3,
		Worktree: "",
	}

	cfg := s.configForPS(ps)
	if cfg != nil {
		t.Errorf("expected nil config when no ConfigPath and no Worktree, got %v", cfg)
	}
}

func TestConfigForPS_DerivesRepoDirFromConfigPath(t *testing.T) {
	dir := t.TempDir()
	cfgContent := `
pipeline:
  name: derived-dir-project
  repo: github.com/org/derived
`
	cfgPath := filepath.Join(dir, "pipeline.yaml")
	if err := os.WriteFile(cfgPath, []byte(cfgContent), 0644); err != nil {
		t.Fatal(err)
	}

	s := NewServer(nil, nil, 0, "")

	// RepoDir empty: should be derived from filepath.Dir(ConfigPath)
	ps := &pipeline.PipelineState{
		Issue:      4,
		ConfigPath: cfgPath,
		RepoDir:    "", // intentionally empty
	}

	cfg := s.configForPS(ps)
	if cfg == nil {
		t.Fatal("expected non-nil config when ConfigPath is set (even without RepoDir)")
	}
	if cfg.Pipeline.Name != "derived-dir-project" {
		t.Errorf("expected name 'derived-dir-project', got %q", cfg.Pipeline.Name)
	}
}
