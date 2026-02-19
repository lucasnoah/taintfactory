package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const validConfig = `
pipeline:
  name: my-app
  repo: github.com/example/my-app
  max_fix_rounds: 3
  fresh_session_after: 2
  defaults:
    model: sonnet
    timeout: "5m"
    flags: "--verbose"
  default_checks:
    - lint
    - typecheck
  checks:
    lint:
      command: "npm run lint"
      parser: eslint
      timeout: "2m"
      auto_fix: true
      fix_command: "npm run lint -- --fix"
    typecheck:
      command: "npx tsc --noEmit"
      parser: typescript
      timeout: "3m"
    test:
      command: "npm test"
      parser: vitest
      timeout: "5m"
  stages:
    - id: scaffold
      type: agent
      prompt_template: "templates/scaffold.md"
      context_mode: full
      goal_gate: true
      session_mode: fresh
    - id: implement
      type: agent
      prompt_template: "templates/implement.md"
      model: opus
      context_mode: code_only
      on_fail: scaffold
      extra_checks:
        - test
    - id: final-checks
      type: checks_only
      checks:
        - lint
        - typecheck
        - test
`

func writeTestConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "pipeline.yaml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadValidConfig(t *testing.T) {
	path := writeTestConfig(t, validConfig)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.Pipeline.Name != "my-app" {
		t.Errorf("Name = %q, want %q", cfg.Pipeline.Name, "my-app")
	}
	if cfg.Pipeline.Repo != "github.com/example/my-app" {
		t.Errorf("Repo = %q, want %q", cfg.Pipeline.Repo, "github.com/example/my-app")
	}
	if cfg.Pipeline.MaxFixRounds != 3 {
		t.Errorf("MaxFixRounds = %d, want 3", cfg.Pipeline.MaxFixRounds)
	}
	if len(cfg.Pipeline.Stages) != 3 {
		t.Fatalf("len(Stages) = %d, want 3", len(cfg.Pipeline.Stages))
	}
	if len(cfg.Pipeline.Checks) != 3 {
		t.Errorf("len(Checks) = %d, want 3", len(cfg.Pipeline.Checks))
	}
}

func TestDefaultsMerge(t *testing.T) {
	path := writeTestConfig(t, validConfig)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	// scaffold has no model set — should inherit default "sonnet"
	scaffold := cfg.Pipeline.Stages[0]
	if scaffold.Model != "sonnet" {
		t.Errorf("scaffold.Model = %q, want %q (from defaults)", scaffold.Model, "sonnet")
	}

	// scaffold has no flags — should inherit default "--verbose"
	if scaffold.Flags != "--verbose" {
		t.Errorf("scaffold.Flags = %q, want %q (from defaults)", scaffold.Flags, "--verbose")
	}

	// implement has explicit model "opus" — should NOT be overridden
	implement := cfg.Pipeline.Stages[1]
	if implement.Model != "opus" {
		t.Errorf("implement.Model = %q, want %q (explicit)", implement.Model, "opus")
	}
}

func TestDefaultChecksResolution(t *testing.T) {
	path := writeTestConfig(t, validConfig)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	// scaffold has no checks_after and no skip_checks — should get default_checks
	scaffold := cfg.Pipeline.Stages[0]
	if len(scaffold.ChecksAfter) != 2 {
		t.Fatalf("scaffold.ChecksAfter = %v, want [lint typecheck]", scaffold.ChecksAfter)
	}
	if scaffold.ChecksAfter[0] != "lint" || scaffold.ChecksAfter[1] != "typecheck" {
		t.Errorf("scaffold.ChecksAfter = %v, want [lint typecheck]", scaffold.ChecksAfter)
	}

	// final-checks is checks_only — should NOT get default_checks
	finalChecks := cfg.Pipeline.Stages[2]
	if len(finalChecks.ChecksAfter) != 0 {
		t.Errorf("final-checks.ChecksAfter = %v, want [] (checks_only stage)", finalChecks.ChecksAfter)
	}
}

func TestDefaultChecksSkipped(t *testing.T) {
	yaml := `
pipeline:
  name: test
  repo: github.com/test/test
  default_checks:
    - lint
  checks:
    lint:
      command: "npm run lint"
      parser: eslint
  stages:
    - id: stage1
      type: agent
      skip_checks: true
`
	path := writeTestConfig(t, yaml)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if len(cfg.Pipeline.Stages[0].ChecksAfter) != 0 {
		t.Errorf("ChecksAfter = %v, want [] (skip_checks=true)", cfg.Pipeline.Stages[0].ChecksAfter)
	}
}

func TestValidateValidConfig(t *testing.T) {
	path := writeTestConfig(t, validConfig)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	errs := Validate(cfg)
	if len(errs) != 0 {
		t.Errorf("Validate() returned %d errors for valid config:", len(errs))
		for _, e := range errs {
			t.Errorf("  - %s", e)
		}
	}
}

func TestValidateMissingName(t *testing.T) {
	yaml := `
pipeline:
  repo: github.com/test/test
  stages:
    - id: s1
      type: agent
`
	path := writeTestConfig(t, yaml)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	errs := Validate(cfg)
	found := false
	for _, e := range errs {
		if e.Field == "pipeline.name" {
			found = true
		}
	}
	if !found {
		t.Error("expected validation error for missing pipeline.name")
	}
}

func TestValidateMissingRepo(t *testing.T) {
	yaml := `
pipeline:
  name: test
  stages:
    - id: s1
      type: agent
`
	path := writeTestConfig(t, yaml)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	errs := Validate(cfg)
	found := false
	for _, e := range errs {
		if e.Field == "pipeline.repo" {
			found = true
		}
	}
	if !found {
		t.Error("expected validation error for missing pipeline.repo")
	}
}

func TestValidateEmptyStages(t *testing.T) {
	yaml := `
pipeline:
  name: test
  repo: github.com/test/test
  stages: []
`
	path := writeTestConfig(t, yaml)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	errs := Validate(cfg)
	found := false
	for _, e := range errs {
		if e.Field == "pipeline.stages" {
			found = true
		}
	}
	if !found {
		t.Error("expected validation error for empty stages")
	}
}

func TestValidateDuplicateStageIDs(t *testing.T) {
	yaml := `
pipeline:
  name: test
  repo: github.com/test/test
  stages:
    - id: dup
      type: agent
    - id: dup
      type: agent
`
	path := writeTestConfig(t, yaml)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	errs := Validate(cfg)
	found := false
	for _, e := range errs {
		if strings.Contains(e.Message, "duplicate stage ID") {
			found = true
		}
	}
	if !found {
		t.Error("expected validation error for duplicate stage IDs")
	}
}

func TestValidateInvalidOnFailTarget(t *testing.T) {
	yaml := `
pipeline:
  name: test
  repo: github.com/test/test
  stages:
    - id: s1
      type: agent
      on_fail: nonexistent
`
	path := writeTestConfig(t, yaml)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	errs := Validate(cfg)
	found := false
	for _, e := range errs {
		if strings.Contains(e.Message, "references undefined stage") {
			found = true
		}
	}
	if !found {
		t.Error("expected validation error for invalid on_fail target")
	}
}

func TestValidateUnknownCheckReference(t *testing.T) {
	yaml := `
pipeline:
  name: test
  repo: github.com/test/test
  default_checks:
    - nonexistent
  stages:
    - id: s1
      type: agent
`
	path := writeTestConfig(t, yaml)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	errs := Validate(cfg)
	found := false
	for _, e := range errs {
		if strings.Contains(e.Message, "references undefined check") {
			found = true
		}
	}
	if !found {
		t.Error("expected validation error for unknown check reference in default_checks")
	}
}

func TestValidateChecksOnlyWithoutChecksList(t *testing.T) {
	yaml := `
pipeline:
  name: test
  repo: github.com/test/test
  stages:
    - id: gate
      type: checks_only
`
	path := writeTestConfig(t, yaml)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	errs := Validate(cfg)
	found := false
	for _, e := range errs {
		if strings.Contains(e.Message, "checks_only stage must have") {
			found = true
		}
	}
	if !found {
		t.Error("expected validation error for checks_only stage without checks list")
	}
}

func TestValidateUnrecognizedParser(t *testing.T) {
	yaml := `
pipeline:
  name: test
  repo: github.com/test/test
  checks:
    mycheck:
      command: "run-check"
      parser: unknown_parser
  stages:
    - id: s1
      type: agent
`
	path := writeTestConfig(t, yaml)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	errs := Validate(cfg)
	found := false
	for _, e := range errs {
		if strings.Contains(e.Message, "unrecognized parser") {
			found = true
		}
	}
	if !found {
		t.Error("expected validation error for unrecognized parser")
	}
}

func TestValidateStageCheckReferences(t *testing.T) {
	yaml := `
pipeline:
  name: test
  repo: github.com/test/test
  checks:
    lint:
      command: "npm run lint"
      parser: eslint
  stages:
    - id: s1
      type: agent
      skip_checks: true
      checks_after:
        - lint
        - bogus
      checks_before:
        - also_bogus
`
	path := writeTestConfig(t, yaml)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	errs := Validate(cfg)
	bogusCount := 0
	for _, e := range errs {
		if strings.Contains(e.Message, "references undefined check") {
			bogusCount++
		}
	}
	if bogusCount != 2 {
		t.Errorf("expected 2 undefined check errors, got %d", bogusCount)
	}
}

func TestValidateOnFailMap(t *testing.T) {
	yaml := `
pipeline:
  name: test
  repo: github.com/test/test
  stages:
    - id: s1
      type: agent
    - id: s2
      type: agent
      on_fail:
        lint_fail: s1
        test_fail: nonexistent
`
	path := writeTestConfig(t, yaml)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	errs := Validate(cfg)
	found := false
	for _, e := range errs {
		if strings.Contains(e.Message, "references undefined stage") && strings.Contains(e.Message, "nonexistent") {
			found = true
		}
	}
	if !found {
		t.Error("expected validation error for on_fail map with invalid target")
	}

	// s1 is valid — should NOT produce an error
	for _, e := range errs {
		if strings.Contains(e.Message, `"s1"`) {
			t.Error("unexpected error for valid on_fail target s1")
		}
	}
}

func TestLoadInvalidYAML(t *testing.T) {
	path := writeTestConfig(t, "not: [valid: yaml: !!!")
	_, err := Load(path)
	if err == nil {
		t.Error("expected error for invalid YAML")
	}
}

func TestLoadNonexistentFile(t *testing.T) {
	_, err := Load("/nonexistent/path/config.yaml")
	if err == nil {
		t.Error("expected error for nonexistent file")
	}
}

func TestLoadDefaultNotFound(t *testing.T) {
	// Change to temp dir so no pipeline.yaml is found
	orig, _ := os.Getwd()
	dir := t.TempDir()
	os.Chdir(dir)
	defer os.Chdir(orig)

	_, err := LoadDefault()
	if err == nil {
		t.Error("expected error when no config file found")
	}
}

func TestLoadDefaultFromCurrentDir(t *testing.T) {
	orig, _ := os.Getwd()
	dir := t.TempDir()
	os.Chdir(dir)
	defer os.Chdir(orig)

	content := `
pipeline:
  name: local
  repo: github.com/test/local
  stages:
    - id: s1
      type: agent
`
	os.WriteFile(filepath.Join(dir, "pipeline.yaml"), []byte(content), 0644)

	cfg, err := LoadDefault()
	if err != nil {
		t.Fatalf("LoadDefault() error: %v", err)
	}
	if cfg.Pipeline.Name != "local" {
		t.Errorf("Name = %q, want %q", cfg.Pipeline.Name, "local")
	}
}

func TestValidateRecognizedParsers(t *testing.T) {
	parsers := []string{"eslint", "prettier", "typescript", "vitest", "npm-audit", "generic"}
	for _, parser := range parsers {
		yaml := `
pipeline:
  name: test
  repo: github.com/test/test
  checks:
    c:
      command: "cmd"
      parser: ` + parser + `
  stages:
    - id: s1
      type: agent
`
		path := writeTestConfig(t, yaml)
		cfg, err := Load(path)
		if err != nil {
			t.Fatalf("Load() error for parser %q: %v", parser, err)
		}
		errs := Validate(cfg)
		for _, e := range errs {
			if strings.Contains(e.Message, "unrecognized parser") {
				t.Errorf("parser %q should be recognized but got error: %s", parser, e)
			}
		}
	}
}

func TestCheckFields(t *testing.T) {
	path := writeTestConfig(t, validConfig)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	lint, ok := cfg.Pipeline.Checks["lint"]
	if !ok {
		t.Fatal("missing check 'lint'")
	}
	if lint.Command != "npm run lint" {
		t.Errorf("lint.Command = %q", lint.Command)
	}
	if lint.Parser != "eslint" {
		t.Errorf("lint.Parser = %q", lint.Parser)
	}
	if !lint.AutoFix {
		t.Error("lint.AutoFix should be true")
	}
	if lint.FixCommand != "npm run lint -- --fix" {
		t.Errorf("lint.FixCommand = %q", lint.FixCommand)
	}
}

func TestStageFields(t *testing.T) {
	path := writeTestConfig(t, validConfig)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	scaffold := cfg.Pipeline.Stages[0]
	if scaffold.ID != "scaffold" {
		t.Errorf("ID = %q", scaffold.ID)
	}
	if scaffold.Type != "agent" {
		t.Errorf("Type = %q", scaffold.Type)
	}
	if scaffold.PromptTemplate != "templates/scaffold.md" {
		t.Errorf("PromptTemplate = %q", scaffold.PromptTemplate)
	}
	if scaffold.ContextMode != "full" {
		t.Errorf("ContextMode = %q", scaffold.ContextMode)
	}
	if !scaffold.GoalGate {
		t.Error("GoalGate should be true")
	}
	if scaffold.SessionMode != "fresh" {
		t.Errorf("SessionMode = %q", scaffold.SessionMode)
	}

	implement := cfg.Pipeline.Stages[1]
	if implement.OnFail != "scaffold" {
		t.Errorf("OnFail = %v, want %q", implement.OnFail, "scaffold")
	}
	if len(implement.ExtraChecks) != 1 || implement.ExtraChecks[0] != "test" {
		t.Errorf("ExtraChecks = %v", implement.ExtraChecks)
	}
}
