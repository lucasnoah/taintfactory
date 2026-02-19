package prompt

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRender_SimpleVars(t *testing.T) {
	tmpl := "Hello {{name}}, you are working on issue #{{issue_number}}."
	vars := Vars{
		"name":         "Alice",
		"issue_number": "42",
	}

	result, err := Render(tmpl, vars)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := "Hello Alice, you are working on issue #42."
	if result != expected {
		t.Errorf("expected %q, got %q", expected, result)
	}
}

func TestRender_MissingVar(t *testing.T) {
	tmpl := "Hello {{name}}, issue {{issue_number}}."
	vars := Vars{
		"name": "Alice",
	}

	_, err := Render(tmpl, vars)
	if err == nil {
		t.Fatal("expected error for missing variable")
	}
	if !strings.Contains(err.Error(), "issue_number") {
		t.Errorf("error should mention missing variable, got: %v", err)
	}
}

func TestRender_MultipleMissing(t *testing.T) {
	tmpl := "{{a}} and {{b}} and {{c}}"
	vars := Vars{}

	_, err := Render(tmpl, vars)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "a") || !strings.Contains(err.Error(), "b") || !strings.Contains(err.Error(), "c") {
		t.Errorf("error should mention all missing vars, got: %v", err)
	}
}

func TestRender_ConditionalBlock_Present(t *testing.T) {
	tmpl := "Start.{{#if git_diff}}\nDiff: {{git_diff}}\n{{/if}}End."
	vars := Vars{
		"git_diff": "some changes",
	}

	result, err := Render(tmpl, vars)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "Diff: some changes") {
		t.Errorf("expected conditional block to be included, got: %q", result)
	}
}

func TestRender_ConditionalBlock_Absent(t *testing.T) {
	tmpl := "Start.{{#if git_diff}}\nDiff: {{git_diff}}\n{{/if}}End."
	vars := Vars{}

	result, err := Render(tmpl, vars)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(result, "Diff:") {
		t.Errorf("expected conditional block to be excluded, got: %q", result)
	}
	if result != "Start.End." {
		t.Errorf("expected 'Start.End.', got: %q", result)
	}
}

func TestRender_ConditionalBlock_EmptyString(t *testing.T) {
	tmpl := "{{#if git_diff}}has diff{{/if}}"
	vars := Vars{
		"git_diff": "",
	}

	result, err := Render(tmpl, vars)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "" {
		t.Errorf("expected empty string for empty var, got: %q", result)
	}
}

func TestRender_MultipleConditionals(t *testing.T) {
	tmpl := "{{#if a}}A={{a}}{{/if}} {{#if b}}B={{b}}{{/if}}"
	vars := Vars{
		"a": "yes",
	}

	result, err := Render(tmpl, vars)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "A=yes") {
		t.Errorf("expected A block, got: %q", result)
	}
	if strings.Contains(result, "B=") {
		t.Errorf("expected B block excluded, got: %q", result)
	}
}

func TestRender_NoVars(t *testing.T) {
	tmpl := "No variables here."
	result, err := Render(tmpl, Vars{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != tmpl {
		t.Errorf("expected %q, got %q", tmpl, result)
	}
}

func TestRender_VarInConditional(t *testing.T) {
	tmpl := "{{#if check_failures}}Failures:\n{{check_failures}}{{/if}}"
	vars := Vars{
		"check_failures": "lint: 3 errors\ntest: 2 failures",
	}

	result, err := Render(tmpl, vars)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "lint: 3 errors") {
		t.Errorf("expected check failures content, got: %q", result)
	}
}

func TestRender_BuiltinTemplate(t *testing.T) {
	// Test that the implement template renders without error
	vars := Vars{
		"issue_title":   "Add auth",
		"issue_number":  "42",
		"issue_body":    "Implement authentication.",
		"worktree_path": "/tmp/worktree",
		"branch":        "feature/42",
		"stage_id":      "implement",
		"attempt":       "1",
		"goal":          "my-app: Add auth",
	}

	result, err := Render(implementTemplate, vars)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "Add auth") {
		t.Errorf("expected issue title in output")
	}
	if !strings.Contains(result, "/tmp/worktree") {
		t.Errorf("expected worktree path in output")
	}
}

func TestRender_ReviewTemplate(t *testing.T) {
	vars := Vars{
		"issue_title":    "Add auth",
		"issue_number":   "42",
		"issue_body":     "Implement authentication.",
		"worktree_path":  "/tmp/worktree",
		"branch":         "feature/42",
		"stage_id":       "review",
		"attempt":        "1",
		"git_diff":       "+added line",
		"git_diff_summary": "1 file changed",
		"files_changed":  "src/auth.ts",
	}

	result, err := Render(reviewTemplate, vars)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "+added line") {
		t.Errorf("expected git diff in output")
	}
}

func TestRender_FixChecksTemplate(t *testing.T) {
	vars := Vars{
		"issue_title":    "Add auth",
		"issue_number":   "42",
		"worktree_path":  "/tmp/worktree",
		"branch":         "feature/42",
		"stage_id":       "implement",
		"attempt":        "1",
		"check_failures": "lint: 3 errors\ntest: 2 failures",
	}

	result, err := Render(fixChecksTemplate, vars)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "lint: 3 errors") {
		t.Errorf("expected check failures in output")
	}
}

func TestLoadTemplate_ProjectOverride(t *testing.T) {
	workdir := t.TempDir()

	// Create project-level template
	tmplDir := filepath.Join(workdir, "templates")
	if err := os.MkdirAll(tmplDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmplDir, "custom.md"), []byte("custom template"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	result, err := LoadTemplate("templates/custom.md", workdir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "custom template" {
		t.Errorf("expected 'custom template', got %q", result)
	}
}

func TestLoadTemplate_NotFound(t *testing.T) {
	_, err := LoadTemplate("nonexistent.md", "")
	if err == nil {
		t.Fatal("expected error for missing template")
	}
}

func TestInstallBuiltinTemplates(t *testing.T) {
	// Use a temp dir to avoid writing to real home
	tmpDir := t.TempDir()
	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", origHome)

	if err := InstallBuiltinTemplates(); err != nil {
		t.Fatalf("install error: %v", err)
	}

	// Verify templates were written
	for name := range builtinTemplates {
		path := filepath.Join(tmpDir, ".factory", "templates", name)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			t.Errorf("template %q not installed", name)
		}
	}

	// Running again should not overwrite
	if err := InstallBuiltinTemplates(); err != nil {
		t.Fatalf("second install error: %v", err)
	}
}

func TestBuiltinTemplateNames(t *testing.T) {
	expected := []string{"implement.md", "review.md", "qa.md", "fix-checks.md", "merge.md"}
	for _, name := range expected {
		if _, ok := builtinTemplates[name]; !ok {
			t.Errorf("missing built-in template: %q", name)
		}
	}
}
