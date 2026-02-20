package github

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type mockCmd struct {
	calls   [][]string
	results []mockResult
	idx     int
}

type mockResult struct {
	output string
	err    error
}

func (m *mockCmd) Run(args ...string) (string, error) {
	m.calls = append(m.calls, args)
	if m.idx >= len(m.results) {
		return "", nil
	}
	r := m.results[m.idx]
	m.idx++
	return r.output, r.err
}

type mockGitRunner struct {
	calls   []gitCall
	results []mockResult
	idx     int
}

type gitCall struct {
	Dir  string
	Args []string
}

func (m *mockGitRunner) RunGit(dir string, args ...string) (string, error) {
	m.calls = append(m.calls, gitCall{Dir: dir, Args: args})
	if m.idx >= len(m.results) {
		return "", nil
	}
	r := m.results[m.idx]
	m.idx++
	return r.output, r.err
}

func TestGetIssue(t *testing.T) {
	issueJSON := `{
		"number": 42,
		"title": "Add authentication",
		"body": "Implement auth.\n\n## Acceptance Criteria\n- [ ] Login works\n- [ ] Logout works",
		"state": "OPEN",
		"labels": [{"name": "feature"}]
	}`

	mock := &mockCmd{
		results: []mockResult{{output: issueJSON}},
	}

	client := NewClient(mock)
	issue, err := client.GetIssue(42)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if issue.Number != 42 {
		t.Errorf("expected number 42, got %d", issue.Number)
	}
	if issue.Title != "Add authentication" {
		t.Errorf("expected title, got %q", issue.Title)
	}
	if issue.State != "OPEN" {
		t.Errorf("expected OPEN, got %q", issue.State)
	}
	if len(issue.Labels) != 1 || issue.Labels[0].Name != "feature" {
		t.Errorf("expected feature label, got %v", issue.Labels)
	}

	// Verify acceptance criteria parsed
	if !strings.Contains(issue.AcceptanceCriteria, "Login works") {
		t.Errorf("expected AC to contain 'Login works', got %q", issue.AcceptanceCriteria)
	}
}

func TestGetIssue_InvalidNumber(t *testing.T) {
	mock := &mockCmd{}
	client := NewClient(mock)

	_, err := client.GetIssue(0)
	if err == nil {
		t.Fatal("expected error for issue 0")
	}

	_, err = client.GetIssue(-1)
	if err == nil {
		t.Fatal("expected error for negative issue")
	}

	// Should not have made any gh calls
	if len(mock.calls) != 0 {
		t.Errorf("expected 0 calls for invalid issue numbers, got %d", len(mock.calls))
	}
}

func TestCacheIssue(t *testing.T) {
	issueJSON := `{"number": 42, "title": "Test", "body": "body", "state": "OPEN", "labels": []}`
	mock := &mockCmd{
		results: []mockResult{{output: issueJSON}},
	}

	tmpDir := t.TempDir()
	pipelineDir := filepath.Join(tmpDir, "42")

	client := NewClient(mock)
	issue, err := client.CacheIssue(42, pipelineDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if issue.Title != "Test" {
		t.Errorf("expected title 'Test', got %q", issue.Title)
	}

	// Verify file was written
	data, err := os.ReadFile(filepath.Join(pipelineDir, "issue.json"))
	if err != nil {
		t.Fatalf("read cached issue: %v", err)
	}

	var cached Issue
	if err := json.Unmarshal(data, &cached); err != nil {
		t.Fatalf("parse cached issue: %v", err)
	}
	if cached.Number != 42 {
		t.Errorf("expected cached number 42, got %d", cached.Number)
	}
}

func TestLoadCachedIssue(t *testing.T) {
	tmpDir := t.TempDir()
	issue := &Issue{Number: 42, Title: "Cached", Body: "body", State: "OPEN"}
	data, _ := json.Marshal(issue)
	if err := os.WriteFile(filepath.Join(tmpDir, "issue.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}

	loaded, err := LoadCachedIssue(tmpDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if loaded.Title != "Cached" {
		t.Errorf("expected 'Cached', got %q", loaded.Title)
	}
}

func TestLoadCachedIssue_NotFound(t *testing.T) {
	_, err := LoadCachedIssue(t.TempDir())
	if err == nil {
		t.Fatal("expected error for missing issue.json")
	}
}

func TestCreatePR(t *testing.T) {
	mock := &mockCmd{
		results: []mockResult{{output: "https://github.com/org/repo/pull/1"}},
	}

	client := NewClient(mock)
	result, err := client.CreatePR(PRCreateOpts{
		Title:  "Add auth",
		Body:   "Implements authentication",
		Branch: "feature/issue-42",
		Base:   "main",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.URL != "https://github.com/org/repo/pull/1" {
		t.Errorf("expected URL, got %q", result.URL)
	}

	if len(mock.calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(mock.calls))
	}
	args := strings.Join(mock.calls[0], " ")
	if !strings.Contains(args, "--title") || !strings.Contains(args, "--base main") {
		t.Errorf("unexpected args: %s", args)
	}
}

func TestMergePR(t *testing.T) {
	mock := &mockCmd{
		results: []mockResult{{output: ""}},
	}

	client := NewClient(mock)
	err := client.MergePR("feature/issue-42", "squash")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(mock.calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(mock.calls))
	}
	args := strings.Join(mock.calls[0], " ")
	if !strings.Contains(args, "--squash") || !strings.Contains(args, "--delete-branch") {
		t.Errorf("expected squash merge args, got: %s", args)
	}
}

func TestMergePR_DefaultStrategy(t *testing.T) {
	mock := &mockCmd{
		results: []mockResult{{output: ""}},
	}

	client := NewClient(mock)
	err := client.MergePR("feature/issue-42", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	args := strings.Join(mock.calls[0], " ")
	if !strings.Contains(args, "--squash") {
		t.Errorf("expected default squash strategy, got: %s", args)
	}
}

func TestMergePR_InvalidStrategy(t *testing.T) {
	mock := &mockCmd{}
	client := NewClient(mock)

	err := client.MergePR("feature/issue-42", "admin")
	if err == nil {
		t.Fatal("expected error for invalid strategy")
	}
	if !strings.Contains(err.Error(), "invalid merge strategy") {
		t.Errorf("expected 'invalid merge strategy' in error, got %q", err.Error())
	}

	// Should not have made any gh calls
	if len(mock.calls) != 0 {
		t.Errorf("expected 0 calls for invalid strategy, got %d", len(mock.calls))
	}
}

func TestMergePR_ValidStrategies(t *testing.T) {
	for _, strategy := range []string{"squash", "merge", "rebase"} {
		mock := &mockCmd{
			results: []mockResult{{output: ""}},
		}
		client := NewClient(mock)
		if err := client.MergePR("feature/issue-42", strategy); err != nil {
			t.Errorf("strategy %q should be valid, got error: %v", strategy, err)
		}
	}
}

func TestPushBranch(t *testing.T) {
	gitMock := &mockGitRunner{
		results: []mockResult{{output: ""}},
	}

	client := NewClientWithGit(&mockCmd{}, gitMock)
	err := client.PushBranch("/tmp/worktree", "feature/issue-42")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(gitMock.calls) != 1 {
		t.Fatalf("expected 1 git call, got %d", len(gitMock.calls))
	}
	call := gitMock.calls[0]
	if call.Dir != "/tmp/worktree" {
		t.Errorf("expected dir /tmp/worktree, got %q", call.Dir)
	}
	expectedArgs := []string{"push", "-u", "origin", "feature/issue-42"}
	if len(call.Args) != len(expectedArgs) {
		t.Fatalf("expected args %v, got %v", expectedArgs, call.Args)
	}
	for i, arg := range expectedArgs {
		if call.Args[i] != arg {
			t.Errorf("arg[%d]: expected %q, got %q", i, arg, call.Args[i])
		}
	}
}

func TestPushBranch_RejectsDashPrefix(t *testing.T) {
	client := NewClientWithGit(&mockCmd{}, &mockGitRunner{})
	err := client.PushBranch("/tmp", "--delete")
	if err == nil {
		t.Fatal("expected error for branch starting with -")
	}
	if !strings.Contains(err.Error(), "must not start with -") {
		t.Errorf("expected rejection message, got %q", err.Error())
	}
}

func TestPushBranch_NoGitRunner(t *testing.T) {
	client := NewClient(&mockCmd{}) // mockCmd doesn't implement GitRunner
	err := client.PushBranch("/tmp", "feature/issue-42")
	if err == nil {
		t.Fatal("expected error when git runner not configured")
	}
	if !strings.Contains(err.Error(), "git runner not configured") {
		t.Errorf("expected 'git runner not configured', got %q", err.Error())
	}
}

func TestExtractAcceptanceCriteria_Header(t *testing.T) {
	body := `## Overview
Some intro.

## Acceptance Criteria
- [ ] Login works
- [ ] Logout works
- [x] Session persists

## Dependencies
Some deps.`

	ac := extractAcceptanceCriteria(body)
	if !strings.Contains(ac, "Login works") {
		t.Errorf("expected Login works in AC, got %q", ac)
	}
	if !strings.Contains(ac, "Logout works") {
		t.Errorf("expected Logout works in AC, got %q", ac)
	}
	if strings.Contains(ac, "Dependencies") {
		t.Errorf("AC should not include Dependencies section, got %q", ac)
	}
}

func TestExtractAcceptanceCriteria_CheckboxFallback(t *testing.T) {
	body := `Do these things:
- [ ] First thing
- [ ] Second thing
- [x] Third thing`

	ac := extractAcceptanceCriteria(body)
	if !strings.Contains(ac, "First thing") {
		t.Errorf("expected First thing in AC, got %q", ac)
	}
}

func TestExtractAcceptanceCriteria_IndentedCheckboxes(t *testing.T) {
	body := `Tasks:
  - [ ] Indented item
  - [X] Uppercase X item`

	ac := extractAcceptanceCriteria(body)
	if !strings.Contains(ac, "Indented item") {
		t.Errorf("expected indented checkbox in AC, got %q", ac)
	}
	if !strings.Contains(ac, "Uppercase X item") {
		t.Errorf("expected uppercase X checkbox in AC, got %q", ac)
	}
}

func TestExtractAcceptanceCriteria_NoAC(t *testing.T) {
	body := "Just a plain description with no criteria."
	ac := extractAcceptanceCriteria(body)
	if ac != "" {
		t.Errorf("expected empty AC, got %q", ac)
	}
}

func TestValidateIssueNumber(t *testing.T) {
	if err := ValidateIssueNumber(1); err != nil {
		t.Errorf("expected no error for 1, got %v", err)
	}
	if err := ValidateIssueNumber(0); err == nil {
		t.Error("expected error for 0")
	}
	if err := ValidateIssueNumber(-1); err == nil {
		t.Error("expected error for -1")
	}
}
