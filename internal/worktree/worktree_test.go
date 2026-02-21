package worktree

import (
	"fmt"
	"strings"
	"testing"
)

type mockGit struct {
	calls   []gitCall
	results []mockResult
	idx     int
}

type gitCall struct {
	Dir  string
	Args []string
}

type mockResult struct {
	Output string
	Err    error
}

func (m *mockGit) Run(dir string, args ...string) (string, error) {
	m.calls = append(m.calls, gitCall{Dir: dir, Args: args})
	if m.idx >= len(m.results) {
		return "", nil
	}
	r := m.results[m.idx]
	m.idx++
	return r.Output, r.Err
}

func TestCreate_HappyPath(t *testing.T) {
	git := &mockGit{
		results: []mockResult{
			{Output: ""}, // fetch origin main
			{Output: ""}, // worktree add
		},
	}

	mgr := NewManager(git, "/repo", "/repo/worktrees")
	result, err := mgr.Create(CreateOpts{Issue: 42, Title: "Add auth"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Path != "/repo/worktrees/issue-42" {
		t.Errorf("expected path /repo/worktrees/issue-42, got %q", result.Path)
	}
	if result.Branch != "feature/issue-42" {
		t.Errorf("expected branch feature/issue-42, got %q", result.Branch)
	}

	if len(git.calls) != 2 {
		t.Fatalf("expected 2 git calls, got %d", len(git.calls))
	}
	// First call: fetch origin main
	assertArgs(t, git.calls[0].Args, "fetch", "origin", "main")
	// Second call: worktree add
	call := git.calls[1]
	if call.Dir != "/repo" {
		t.Errorf("expected dir /repo, got %q", call.Dir)
	}
	assertArgs(t, call.Args, "worktree", "add", "/repo/worktrees/issue-42", "-b", "feature/issue-42")
}

func TestCreate_FetchFailsGracefully(t *testing.T) {
	git := &mockGit{
		results: []mockResult{
			{Err: fmt.Errorf("network unreachable")}, // fetch fails
			{Output: ""},                              // worktree add still succeeds
		},
	}

	mgr := NewManager(git, "/repo", "/repo/worktrees")
	result, err := mgr.Create(CreateOpts{Issue: 42})
	if err != nil {
		t.Fatalf("expected no error when fetch fails, got: %v", err)
	}

	if result.Branch != "feature/issue-42" {
		t.Errorf("expected branch, got %q", result.Branch)
	}

	// Should have 2 calls: fetch (failed) + worktree add
	if len(git.calls) != 2 {
		t.Fatalf("expected 2 git calls, got %d", len(git.calls))
	}
	assertArgs(t, git.calls[0].Args, "fetch", "origin", "main")
}

func TestCreate_CustomBranch(t *testing.T) {
	git := &mockGit{
		results: []mockResult{
			{Output: ""}, // fetch
			{Output: ""}, // worktree add
		},
	}

	mgr := NewManager(git, "/repo", "/repo/worktrees")
	result, err := mgr.Create(CreateOpts{Issue: 42, Branch: "custom/my-branch"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Branch != "custom/my-branch" {
		t.Errorf("expected custom branch, got %q", result.Branch)
	}
}

func TestCreate_CustomBranchSanitized(t *testing.T) {
	git := &mockGit{
		results: []mockResult{
			{Output: ""}, // fetch
			{Output: ""}, // worktree add
		},
	}

	mgr := NewManager(git, "/repo", "/repo/worktrees")
	result, err := mgr.Create(CreateOpts{Issue: 42, Branch: "my branch!!"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Branch != "my-branch" {
		t.Errorf("expected sanitized branch 'my-branch', got %q", result.Branch)
	}
}

func TestCreate_BranchAlreadyExists(t *testing.T) {
	git := &mockGit{
		results: []mockResult{
			{Output: ""},                         // fetch
			{Err: fmt.Errorf("already exists")}, // first attempt fails
			{Output: ""},                         // retry without -b
		},
	}

	mgr := NewManager(git, "/repo", "/repo/worktrees")
	result, err := mgr.Create(CreateOpts{Issue: 42})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(git.calls) != 3 {
		t.Fatalf("expected 3 git calls (fetch + retry), got %d", len(git.calls))
	}
	// Third call (retry) should NOT have -b flag
	thirdCall := git.calls[2]
	for _, arg := range thirdCall.Args {
		if arg == "-b" {
			t.Error("retry should not include -b flag")
		}
	}
	if result.Branch != "feature/issue-42" {
		t.Errorf("expected branch, got %q", result.Branch)
	}
}

func TestCreate_Error(t *testing.T) {
	git := &mockGit{
		results: []mockResult{
			{Output: ""},                         // fetch
			{Err: fmt.Errorf("some git error")}, // worktree add
		},
	}

	mgr := NewManager(git, "/repo", "/repo/worktrees")
	_, err := mgr.Create(CreateOpts{Issue: 42})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestCreate_InvalidIssueNumber(t *testing.T) {
	mgr := NewManager(&mockGit{}, "/repo", "/repo/worktrees")

	_, err := mgr.Create(CreateOpts{Issue: 0})
	if err == nil {
		t.Fatal("expected error for issue 0")
	}

	_, err = mgr.Create(CreateOpts{Issue: -1})
	if err == nil {
		t.Fatal("expected error for negative issue")
	}
}

func TestRemove_HappyPath(t *testing.T) {
	git := &mockGit{
		results: []mockResult{
			{Output: "feature/issue-42"}, // rev-parse HEAD
			{Output: ""},                 // worktree remove
			{Output: ""},                 // branch -d
		},
	}

	mgr := NewManager(git, "/repo", "/repo/worktrees")
	err := mgr.Remove(42, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(git.calls) != 3 {
		t.Fatalf("expected 3 git calls, got %d", len(git.calls))
	}

	// Verify worktree remove does NOT include --force
	removeCall := git.calls[1]
	for _, arg := range removeCall.Args {
		if arg == "--force" {
			t.Error("worktree remove should not use --force by default")
		}
	}
}

func TestRemove_NoBranchDelete(t *testing.T) {
	git := &mockGit{
		results: []mockResult{
			{Output: ""}, // worktree remove
		},
	}

	mgr := NewManager(git, "/repo", "/repo/worktrees")
	err := mgr.Remove(42, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should only have 1 call (no rev-parse, no branch -d)
	if len(git.calls) != 1 {
		t.Fatalf("expected 1 git call, got %d", len(git.calls))
	}
}

func TestRemove_BranchDeleteError(t *testing.T) {
	git := &mockGit{
		results: []mockResult{
			{Output: "feature/issue-42"},                            // rev-parse HEAD
			{Output: ""},                                            // worktree remove
			{Err: fmt.Errorf("branch has unmerged changes")},       // branch -d fails
		},
	}

	mgr := NewManager(git, "/repo", "/repo/worktrees")
	err := mgr.Remove(42, true)
	if err == nil {
		t.Fatal("expected error when branch delete fails")
	}
	if !strings.Contains(err.Error(), "delete branch") {
		t.Errorf("expected 'delete branch' in error, got %q", err.Error())
	}
}

func TestRemove_ProtectsMain(t *testing.T) {
	git := &mockGit{
		results: []mockResult{
			{Output: "main"}, // rev-parse HEAD returns main
			{Output: ""},     // worktree remove
		},
	}

	mgr := NewManager(git, "/repo", "/repo/worktrees")
	err := mgr.Remove(42, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should NOT attempt to delete main branch
	for _, call := range git.calls {
		if len(call.Args) >= 2 && call.Args[0] == "branch" && call.Args[1] == "-d" {
			t.Error("should not delete main branch")
		}
	}
}

func TestRemove_InvalidIssueNumber(t *testing.T) {
	mgr := NewManager(&mockGit{}, "/repo", "/repo/worktrees")

	err := mgr.Remove(0, false)
	if err == nil {
		t.Fatal("expected error for issue 0")
	}

	err = mgr.Remove(-1, false)
	if err == nil {
		t.Fatal("expected error for negative issue")
	}
}

func TestPath(t *testing.T) {
	mgr := NewManager(nil, "/repo", "/repo/worktrees")
	path := mgr.Path(42)
	if path != "/repo/worktrees/issue-42" {
		t.Errorf("expected /repo/worktrees/issue-42, got %q", path)
	}
}

func TestSanitizeBranch(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"feature/issue-42", "feature/issue-42"},
		{"feature/Add Auth!", "feature/Add-Auth"},
		{"test spaces  here", "test-spaces-here"},
		{strings.Repeat("a", 200), strings.Repeat("a", 100)},
	}
	for _, tc := range tests {
		got := sanitizeBranch(tc.input)
		if got != tc.expected {
			t.Errorf("sanitizeBranch(%q) = %q, want %q", tc.input, got, tc.expected)
		}
	}
}

// assertArgs verifies exact argument match (no substring false positives).
func assertArgs(t *testing.T, got []string, want ...string) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("args length mismatch: got %v, want %v", got, want)
		return
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("arg[%d] mismatch: got %q, want %q", i, got[i], want[i])
		}
	}
}
