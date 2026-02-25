package web

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lucasnoah/taintfactory/internal/pipeline"
)

// ---- sidebarData tests ----

func TestSidebarData_GroupsByNamespace(t *testing.T) {
	dir := t.TempDir()
	store := pipeline.NewStore(dir)
	store.Create(pipeline.CreateOpts{Issue: 101, Title: "A", Branch: "b", Worktree: "w", FirstStage: "impl", Namespace: "org/repo-a", ConfigPath: "/x/pipeline.yaml", RepoDir: "/x"})
	store.Create(pipeline.CreateOpts{Issue: 102, Title: "B", Branch: "b", Worktree: "w", FirstStage: "impl", Namespace: "org/repo-a", ConfigPath: "/x/pipeline.yaml", RepoDir: "/x"})
	store.Create(pipeline.CreateOpts{Issue: 103, Title: "C", Branch: "b", Worktree: "w", FirstStage: "impl", Namespace: "org/repo-b", ConfigPath: "/y/pipeline.yaml", RepoDir: "/y"})

	s := NewServer(store, nil, 0, "")
	sd := s.sidebarData("")

	if len(sd.Projects) != 2 {
		t.Fatalf("expected 2 projects, got %d", len(sd.Projects))
	}
	if sd.Projects[0].Namespace != "org/repo-a" {
		t.Errorf("expected org/repo-a first (sorted), got %q", sd.Projects[0].Namespace)
	}
}

func TestSidebarData_CountsOnlyActive(t *testing.T) {
	dir := t.TempDir()
	store := pipeline.NewStore(dir)
	store.Create(pipeline.CreateOpts{Issue: 201, Title: "A", Branch: "b", Worktree: "w", FirstStage: "impl", Namespace: "org/app", ConfigPath: "/x/pipeline.yaml", RepoDir: "/x"})
	store.Update(201, func(p *pipeline.PipelineState) { p.Status = "in_progress" })
	store.Create(pipeline.CreateOpts{Issue: 202, Title: "B", Branch: "b", Worktree: "w", FirstStage: "impl", Namespace: "org/app", ConfigPath: "/x/pipeline.yaml", RepoDir: "/x"})
	// issue 202 stays pending

	s := NewServer(store, nil, 0, "")
	sd := s.sidebarData("")

	if len(sd.Projects) != 1 {
		t.Fatalf("expected 1 project, got %d", len(sd.Projects))
	}
	if sd.Projects[0].ActiveCount != 1 {
		t.Errorf("expected ActiveCount=1, got %d", sd.Projects[0].ActiveCount)
	}
}

func TestSidebarData_MarksSelected(t *testing.T) {
	dir := t.TempDir()
	store := pipeline.NewStore(dir)
	store.Create(pipeline.CreateOpts{Issue: 301, Title: "A", Branch: "b", Worktree: "w", FirstStage: "impl", Namespace: "org/app", ConfigPath: "/x/pipeline.yaml", RepoDir: "/x"})
	store.Create(pipeline.CreateOpts{Issue: 302, Title: "B", Branch: "b", Worktree: "w", FirstStage: "impl", Namespace: "org/other", ConfigPath: "/y/pipeline.yaml", RepoDir: "/y"})

	s := NewServer(store, nil, 0, "")
	sd := s.sidebarData("org/app")

	var foundSelected bool
	for _, p := range sd.Projects {
		if p.Namespace == "org/app" && p.IsSelected {
			foundSelected = true
		}
		if p.Namespace == "org/other" && p.IsSelected {
			t.Error("org/other should not be selected")
		}
	}
	if !foundSelected {
		t.Error("org/app should be marked IsSelected")
	}
	if sd.CurrentProject != "org/app" {
		t.Errorf("CurrentProject = %q, want org/app", sd.CurrentProject)
	}
}

func TestSidebarData_ExcludesLegacyPipelines(t *testing.T) {
	dir := t.TempDir()
	store := pipeline.NewStore(dir)
	store.Create(pipeline.CreateOpts{Issue: 401, Title: "Legacy", Branch: "b", Worktree: "/some/worktree", FirstStage: "impl"})

	s := NewServer(store, nil, 0, "")
	sd := s.sidebarData("")

	if len(sd.Projects) != 0 {
		t.Errorf("expected 0 projects (legacy has no namespace), got %d", len(sd.Projects))
	}
}

// ---- repoToNamespace tests ----

func TestRepoToNamespace(t *testing.T) {
	cases := []struct {
		repo string
		want string
	}{
		{"github.com/myorg/myapp", "myorg/myapp"},
		{"https://github.com/myorg/myapp", "myorg/myapp"},
		{"http://github.com/myorg/myapp", "myorg/myapp"},
		{"", ""},
	}
	for _, c := range cases {
		got := repoToNamespace(c.repo)
		if got != c.want {
			t.Errorf("repoToNamespace(%q) = %q, want %q", c.repo, got, c.want)
		}
	}
}

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
