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

func TestExtractAcceptanceCriteria_NoAC(t *testing.T) {
	body := "Just a plain description with no criteria."
	ac := extractAcceptanceCriteria(body)
	if ac != "" {
		t.Errorf("expected empty AC, got %q", ac)
	}
}
