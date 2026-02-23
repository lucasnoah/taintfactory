package triage

import (
	"os"
	"path/filepath"
	"testing"
)

const testYAML = `
triage:
  name: "My Repo"
  repo: "owner/my-repo"

stages:
  - id: stale_context
    timeout: 10m
    outcomes:
      stale: done
      clean: already_implemented

  - id: already_implemented
    timeout: 15m
    outcomes:
      implemented: done
      not_implemented: done
`

func writeTempYAML(t *testing.T, content string) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "triage-*.yaml")
	if err != nil {
		t.Fatalf("creating temp file: %v", err)
	}
	if _, err := f.WriteString(content); err != nil {
		t.Fatalf("writing temp file: %v", err)
	}
	f.Close()
	return f.Name()
}

func TestLoad_ValidConfig(t *testing.T) {
	path := writeTempYAML(t, testYAML)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.Triage.Name != "My Repo" {
		t.Errorf("name = %q, want %q", cfg.Triage.Name, "My Repo")
	}
	if cfg.Triage.Repo != "owner/my-repo" {
		t.Errorf("repo = %q, want %q", cfg.Triage.Repo, "owner/my-repo")
	}
	if len(cfg.Stages) != 2 {
		t.Fatalf("len(stages) = %d, want 2", len(cfg.Stages))
	}

	s0 := cfg.Stages[0]
	if s0.ID != "stale_context" {
		t.Errorf("stages[0].id = %q, want %q", s0.ID, "stale_context")
	}
	if s0.Outcomes["stale"] != "done" {
		t.Errorf("stages[0].outcomes[stale] = %q, want %q", s0.Outcomes["stale"], "done")
	}
	if s0.Outcomes["clean"] != "already_implemented" {
		t.Errorf("stages[0].outcomes[clean] = %q, want %q", s0.Outcomes["clean"], "already_implemented")
	}

	s1 := cfg.Stages[1]
	if s1.ID != "already_implemented" {
		t.Errorf("stages[1].id = %q, want %q", s1.ID, "already_implemented")
	}
	if s1.Outcomes["implemented"] != "done" {
		t.Errorf("stages[1].outcomes[implemented] = %q, want %q", s1.Outcomes["implemented"], "done")
	}
}

func TestLoad_DefaultTimeout(t *testing.T) {
	noTimeoutYAML := `
triage:
  name: "Test"
  repo: "owner/test"

stages:
  - id: stage_no_timeout
    outcomes:
      done: done
`
	path := writeTempYAML(t, noTimeoutYAML)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if len(cfg.Stages) != 1 {
		t.Fatalf("len(stages) = %d, want 1", len(cfg.Stages))
	}
	if cfg.Stages[0].Timeout != "15m" {
		t.Errorf("timeout = %q, want %q", cfg.Stages[0].Timeout, "15m")
	}
}

func TestLoad_MissingFile(t *testing.T) {
	_, err := Load("/nonexistent/path/triage.yaml")
	if err == nil {
		t.Error("Load() expected error for missing file, got nil")
	}
}

func TestLoadDefault_FindsTriageYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "triage.yaml")
	if err := os.WriteFile(path, []byte(testYAML), 0644); err != nil {
		t.Fatalf("writing triage.yaml: %v", err)
	}

	cfg, err := LoadDefault(dir)
	if err != nil {
		t.Fatalf("LoadDefault() error: %v", err)
	}
	if cfg.Triage.Name != "My Repo" {
		t.Errorf("name = %q, want %q", cfg.Triage.Name, "My Repo")
	}
}

func TestLoadDefault_NotFound(t *testing.T) {
	dir := t.TempDir()
	_, err := LoadDefault(dir)
	if err == nil {
		t.Error("LoadDefault() expected error for empty dir, got nil")
	}
}

func TestLoad_PromptTemplateField(t *testing.T) {
	path := writeTempYAML(t, `
triage:
  name: "Test"
  repo: "owner/test"
stages:
  - id: stale_context
    prompt_template: triage/custom.md
    outcomes:
      stale: done
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.Stages[0].PromptTemplate != "triage/custom.md" {
		t.Errorf("PromptTemplate = %q, want %q", cfg.Stages[0].PromptTemplate, "triage/custom.md")
	}
}
