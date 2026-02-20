package github

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// CmdRunner provides command execution. Interface for testing.
type CmdRunner interface {
	Run(args ...string) (string, error)
}

// GitRunner provides git command execution. Interface for testing.
type GitRunner interface {
	RunGit(dir string, args ...string) (string, error)
}

// ExecRunner runs gh commands via exec.
type ExecRunner struct{}

func (r *ExecRunner) Run(args ...string) (string, error) {
	cmd := exec.Command("gh", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return strings.TrimSpace(string(out)), fmt.Errorf("gh %s: %s: %w", strings.Join(args, " "), strings.TrimSpace(string(out)), err)
	}
	return strings.TrimSpace(string(out)), nil
}

// RunGit implements GitRunner using exec.Command.
func (r *ExecRunner) RunGit(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return strings.TrimSpace(string(out)), fmt.Errorf("git %s: %s: %w", strings.Join(args, " "), strings.TrimSpace(string(out)), err)
	}
	return strings.TrimSpace(string(out)), nil
}

// Client provides GitHub operations.
type Client struct {
	cmd CmdRunner
	git GitRunner
}

// NewClient creates a GitHub client. If cmd also implements GitRunner,
// it will be used for git operations (e.g., PushBranch).
func NewClient(cmd CmdRunner) *Client {
	c := &Client{cmd: cmd}
	if git, ok := cmd.(GitRunner); ok {
		c.git = git
	}
	return c
}

// NewClientWithGit creates a GitHub client with a separate git runner.
func NewClientWithGit(cmd CmdRunner, git GitRunner) *Client {
	return &Client{cmd: cmd, git: git}
}

// Issue represents a GitHub issue.
type Issue struct {
	Number             int     `json:"number"`
	Title              string  `json:"title"`
	Body               string  `json:"body"`
	State              string  `json:"state"`
	Labels             []Label `json:"labels"`
	AcceptanceCriteria string  `json:"acceptance_criteria,omitempty"`
}

// Label represents a GitHub label.
type Label struct {
	Name string `json:"name"`
}

// ValidateIssueNumber checks that an issue number is positive.
func ValidateIssueNumber(n int) error {
	if n <= 0 {
		return fmt.Errorf("invalid issue number %d: must be positive", n)
	}
	return nil
}

// GetIssue fetches a GitHub issue by number.
func (c *Client) GetIssue(number int) (*Issue, error) {
	if err := ValidateIssueNumber(number); err != nil {
		return nil, err
	}

	out, err := c.cmd.Run("issue", "view", fmt.Sprintf("%d", number), "--json", "number,title,body,state,labels")
	if err != nil {
		return nil, fmt.Errorf("get issue %d: %w", number, err)
	}

	var issue Issue
	if err := json.Unmarshal([]byte(out), &issue); err != nil {
		return nil, fmt.Errorf("parse issue JSON: %w", err)
	}

	issue.AcceptanceCriteria = extractAcceptanceCriteria(issue.Body)
	return &issue, nil
}

// CacheIssue fetches an issue and writes it to the pipeline's issue.json.
func (c *Client) CacheIssue(number int, pipelineDir string) (*Issue, error) {
	issue, err := c.GetIssue(number)
	if err != nil {
		return nil, err
	}

	data, err := json.MarshalIndent(issue, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal issue: %w", err)
	}

	if err := os.MkdirAll(pipelineDir, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir: %w", err)
	}

	path := filepath.Join(pipelineDir, "issue.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return nil, fmt.Errorf("write issue.json: %w", err)
	}

	return issue, nil
}

// LoadCachedIssue reads a previously cached issue from disk.
func LoadCachedIssue(pipelineDir string) (*Issue, error) {
	path := filepath.Join(pipelineDir, "issue.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var issue Issue
	if err := json.Unmarshal(data, &issue); err != nil {
		return nil, fmt.Errorf("parse cached issue: %w", err)
	}
	return &issue, nil
}

// PRCreateOpts holds options for creating a PR.
type PRCreateOpts struct {
	Title  string
	Body   string
	Branch string
	Base   string
}

// PRCreateResult holds the result of creating a PR.
type PRCreateResult struct {
	URL string
}

// CreatePR creates a pull request.
func (c *Client) CreatePR(opts PRCreateOpts) (*PRCreateResult, error) {
	args := []string{"pr", "create", "--title", opts.Title, "--body", opts.Body, "--head", opts.Branch}
	if opts.Base != "" {
		args = append(args, "--base", opts.Base)
	}

	out, err := c.cmd.Run(args...)
	if err != nil {
		return nil, fmt.Errorf("create PR: %w", err)
	}

	return &PRCreateResult{URL: out}, nil
}

// validMergeStrategies is the set of allowed merge strategies.
var validMergeStrategies = map[string]bool{
	"squash": true,
	"merge":  true,
	"rebase": true,
}

// MergePR merges a pull request by branch name.
func (c *Client) MergePR(branch string, strategy string) error {
	if strategy == "" {
		strategy = "squash"
	}
	if !validMergeStrategies[strategy] {
		return fmt.Errorf("invalid merge strategy %q: must be squash, merge, or rebase", strategy)
	}

	args := []string{"pr", "merge", branch, "--" + strategy, "--delete-branch"}
	_, err := c.cmd.Run(args...)
	if err != nil {
		return fmt.Errorf("merge PR: %w", err)
	}
	return nil
}

// PushBranch pushes a branch to the remote.
func (c *Client) PushBranch(dir string, branch string) error {
	if c.git == nil {
		return fmt.Errorf("git runner not configured")
	}
	if strings.HasPrefix(branch, "-") {
		return fmt.Errorf("invalid branch name %q: must not start with -", branch)
	}
	_, err := c.git.RunGit(dir, "push", "-u", "origin", branch)
	if err != nil {
		return fmt.Errorf("push branch: %w", err)
	}
	return nil
}

var acHeaderRe = regexp.MustCompile(`(?mi)^##\s+acceptance\s+criteria`)
var checkboxRe = regexp.MustCompile(`(?m)^\s*[-*]\s+\[[ xX]\]\s+(.+)$`)
var nextHeaderRe = regexp.MustCompile(`(?m)^##\s+`)

// extractAcceptanceCriteria parses acceptance criteria from an issue body.
// It looks for "## Acceptance Criteria" header or checkbox lists.
func extractAcceptanceCriteria(body string) string {
	// Try to find AC section header
	loc := acHeaderRe.FindStringIndex(body)
	if loc != nil {
		section := body[loc[1]:]
		// Find the next ## header or end of string
		nextLoc := nextHeaderRe.FindStringIndex(section)
		if nextLoc != nil {
			section = section[:nextLoc[0]]
		}
		return strings.TrimSpace(section)
	}

	// Fall back to checkbox list extraction
	matches := checkboxRe.FindAllStringSubmatch(body, -1)
	if len(matches) > 0 {
		var criteria []string
		for _, m := range matches {
			criteria = append(criteria, "- "+m[1])
		}
		return strings.Join(criteria, "\n")
	}

	return ""
}
