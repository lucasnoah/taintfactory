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

// ============================================================================
// Adversarial / edge-case tests below
// ============================================================================

// BUG: Nested conditionals produce corrupted output.
// The non-greedy regex matches the INNER {{/if}} first, leaving a dangling
// {{/if}} in the output for the outer block.
func TestRender_NestedConditionals_Bug(t *testing.T) {
	tmpl := "{{#if a}}outer {{#if b}}inner{{/if}} end{{/if}}"
	vars := Vars{"a": "yes", "b": "yes"}

	result, err := Render(tmpl, vars)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Correct behavior: "outer inner end"
	// Actual (buggy): "outer inner end{{/if}}" due to non-greedy regex
	if strings.Contains(result, "{{/if}}") {
		t.Errorf("nested conditionals leave dangling {{/if}} in output: %q", result)
	}
	expected := "outer inner end"
	if result != expected {
		t.Errorf("nested conditionals: expected %q, got %q", expected, result)
	}
}

// BUG: Nested conditional with outer absent leaves garbage.
func TestRender_NestedConditionals_OuterAbsent_Bug(t *testing.T) {
	tmpl := "START{{#if a}}outer {{#if b}}inner{{/if}} end{{/if}}FINISH"
	vars := Vars{} // neither a nor b

	result, err := Render(tmpl, vars)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Correct: "STARTFINISH" (both blocks removed)
	// Buggy: "START end{{/if}}FINISH" (inner {{/if}} consumed, outer leaks)
	if strings.Contains(result, "{{/if}}") {
		t.Errorf("nested conditionals (outer absent) leave garbage: %q", result)
	}
	if result != "STARTFINISH" {
		t.Errorf("expected %q, got %q", "STARTFINISH", result)
	}
}

// BUG: Path traversal in LoadTemplate — "../" escapes the workdir.
func TestLoadTemplate_PathTraversal(t *testing.T) {
	// Create a temp directory structure:
	//   workdir/
	//   outside/secret.txt
	tmpDir := t.TempDir()
	workdir := filepath.Join(tmpDir, "workdir")
	if err := os.MkdirAll(workdir, 0o755); err != nil {
		t.Fatal(err)
	}
	outsideFile := filepath.Join(tmpDir, "secret.txt")
	if err := os.WriteFile(outsideFile, []byte("TOP SECRET DATA"), 0o644); err != nil {
		t.Fatal(err)
	}

	// This should NOT succeed — it reads a file outside the workdir
	content, err := LoadTemplate("../secret.txt", workdir)
	if err == nil {
		t.Errorf("path traversal succeeded: LoadTemplate read file outside workdir: %q", content)
	}
}

// BUG: Absolute path in templatePath bypasses workdir entirely.
func TestLoadTemplate_AbsolutePath(t *testing.T) {
	tmpDir := t.TempDir()
	secretFile := filepath.Join(tmpDir, "secret.txt")
	if err := os.WriteFile(secretFile, []byte("SECRET"), 0o644); err != nil {
		t.Fatal(err)
	}

	// filepath.Join with an absolute second arg returns the absolute path,
	// completely ignoring the workdir. This is a path traversal variant.
	content, err := LoadTemplate(secretFile, "/some/workdir")
	if err == nil {
		t.Errorf("absolute path bypassed workdir restriction: %q", content)
	}
}

// Variable values containing template syntax are inserted literally.
// This is by design: values are not re-expanded to prevent injection.
func TestRender_VarValueContainsTemplateSyntax(t *testing.T) {
	tmpl := "Hello {{name}}"
	vars := Vars{"name": "{{evil}}"}

	result, err := Render(tmpl, vars)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Values are inserted literally — no re-expansion
	if result != "Hello {{evil}}" {
		t.Errorf("expected literal insertion, got %q", result)
	}
}

// Variable values referencing other variables are not re-expanded.
// This prevents injection and is intentional single-pass behavior.
func TestRender_VarValueReferencesAnotherVar(t *testing.T) {
	tmpl := "{{a}} and {{b}}"
	vars := Vars{"a": "{{b}}", "b": "hello"}

	result, err := Render(tmpl, vars)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Single-pass: a's value is inserted literally, not re-expanded
	if result != "{{b}} and hello" {
		t.Errorf("expected '{{b}} and hello', got %q", result)
	}
}

// Unclosed conditional blocks now produce an error.
func TestRender_UnclosedConditional(t *testing.T) {
	tmpl := "START{{#if x}}content with {{y}}MORE"
	vars := Vars{"x": "yes", "y": "val"}

	_, err := Render(tmpl, vars)
	if err == nil {
		t.Fatal("expected error for unclosed conditional block")
	}
	if !strings.Contains(err.Error(), "unclosed") {
		t.Errorf("expected unclosed error, got: %v", err)
	}
}

// BUG: Unclosed conditional — variables inside still required even though
// the block should arguably be ignored or cause an error.
func TestRender_UnclosedConditional_MissingVarInside(t *testing.T) {
	// If the conditional were properly processed and x is absent, the body
	// would be removed and {{y}} wouldn't be required. But since the block
	// is unclosed, it passes through and {{y}} is treated as required.
	tmpl := "START{{#if x}}content with {{y}}{{/if}}MORE"
	vars := Vars{} // x absent, y absent

	result, err := Render(tmpl, vars)
	if err != nil {
		t.Fatalf("should not error when both x and y are absent (block should be excluded): %v", err)
	}
	// Correct: "STARTMORE" (block excluded because x is absent)
	if result != "STARTMORE" {
		t.Errorf("expected %q, got %q", "STARTMORE", result)
	}
}

// BUG: Review template — files_changed required when only git_diff_summary
// is set, even though only git_diff_summary is the conditional gate.
func TestRender_ReviewTemplate_CoupledVars_Bug(t *testing.T) {
	vars := Vars{
		"issue_title":      "Fix bug",
		"issue_number":     "1",
		"issue_body":       "body",
		"worktree_path":    "/tmp/w",
		"branch":           "main",
		"stage_id":         "review",
		"attempt":          "1",
		"git_diff_summary": "1 file changed", // set this...
		// but DON'T set files_changed — it's inside the same conditional block
	}

	_, err := Render(reviewTemplate, vars)
	// This errors with "missing template variables: files_changed" because
	// files_changed is gated behind the git_diff_summary conditional but
	// isn't independently conditional.
	if err != nil {
		t.Errorf("review template requires files_changed when only git_diff_summary is set: %v", err)
	}
}

// BUG: Whitespace variations in conditional tags don't match.
func TestRender_ConditionalTrailingWhitespace_Bug(t *testing.T) {
	// Trailing space before }} in the #if tag
	tmpl := "{{#if x }}content{{/if}}"
	vars := Vars{"x": "yes"}

	result, err := Render(tmpl, vars)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// The trailing space causes the regex to NOT match (it expects }} immediately
	// after the variable name). The block passes through as literal text.
	if strings.Contains(result, "{{#if") {
		t.Errorf("trailing whitespace in conditional tag not handled: %q", result)
	}
	if result != "content" {
		t.Errorf("expected %q, got %q", "content", result)
	}
}

// BUG: Conditional block with multiline variable name whitespace.
func TestRender_ConditionalNewlineInTag(t *testing.T) {
	// Newline in the #if tag between the keyword and variable name
	tmpl := "{{#if\nx}}content{{/if}}"
	vars := Vars{"x": "yes"}

	result, err := Render(tmpl, vars)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// \s+ does match \n, so this DOES match the regex, which is surprising.
	// Template tags spanning multiple lines is probably unintended.
	// The test documents this behavior.
	if result != "content" {
		t.Errorf("newline in conditional tag: expected %q, got %q", "content", result)
	}
}

// Conditional body containing {{/if}} in a variable VALUE is fine because
// variable expansion happens after conditional processing.
func TestRender_ConditionalBodyLooksLikeEndTag(t *testing.T) {
	tmpl := `{{#if note}}Note: {{note}}{{/if}} done`
	vars := Vars{"note": "use {{/if}} carefully"}

	result, err := Render(tmpl, vars)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// The {{/if}} in the variable value is inserted after conditional processing,
	// so it doesn't corrupt parsing.
	if !strings.Contains(result, "use {{/if}} carefully") {
		t.Errorf("expected var value preserved, got: %q", result)
	}
}
