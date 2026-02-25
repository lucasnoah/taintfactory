package worktree

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// GitRunner provides git commands. Interface for testing.
type GitRunner interface {
	Run(dir string, args ...string) (string, error)
}

// ExecGit implements GitRunner using exec.Command.
type ExecGit struct{}

func (g *ExecGit) Run(dir string, args ...string) (string, error) {
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

// Manager handles git worktree operations.
type Manager struct {
	git     GitRunner
	baseDir string // where worktrees are created (repo-root/worktrees/)
	repoDir string // git repo root
}

// NewManager creates a worktree manager.
func NewManager(git GitRunner, repoDir string, baseDir string) *Manager {
	return &Manager{git: git, repoDir: repoDir, baseDir: baseDir}
}

// WithRepoDir creates a new Manager for a different repo root, reusing the same GitRunner.
// The baseDir for worktrees is set to <repoDir>/worktrees.
func (m *Manager) WithRepoDir(repoDir string) *Manager {
	return &Manager{git: m.git, repoDir: repoDir, baseDir: filepath.Join(repoDir, "worktrees")}
}

// CreateOpts holds options for creating a worktree.
type CreateOpts struct {
	Issue  int
	Title  string
	Branch string // override auto-generated branch name
}

// CreateResult holds the result of creating a worktree.
type CreateResult struct {
	Path   string
	Branch string
}

// Create creates a new git worktree for an issue.
func (m *Manager) Create(opts CreateOpts) (*CreateResult, error) {
	if opts.Issue <= 0 {
		return nil, fmt.Errorf("invalid issue number %d: must be positive", opts.Issue)
	}

	branch := opts.Branch
	if branch == "" {
		branch = sanitizeBranch(fmt.Sprintf("feature/issue-%d", opts.Issue))
	} else {
		branch = sanitizeBranch(branch)
	}

	worktreePath := filepath.Join(m.baseDir, fmt.Sprintf("issue-%d", opts.Issue))

	// Best-effort fetch to ensure we branch from up-to-date main
	m.git.Run(m.repoDir, "fetch", "origin", "main")

	// Create the worktree branching explicitly from origin/main, not the local
	// HEAD (which may lag behind if the local branch hasn't been fast-forwarded).
	_, err := m.git.Run(m.repoDir, "worktree", "add", worktreePath, "-b", branch, "origin/main")
	if err != nil {
		// If branch already exists, try without -b
		if strings.Contains(err.Error(), "already exists") {
			_, err = m.git.Run(m.repoDir, "worktree", "add", worktreePath, branch)
			if err != nil {
				return nil, fmt.Errorf("create worktree: %w", err)
			}
		} else {
			return nil, fmt.Errorf("create worktree: %w", err)
		}
	}

	return &CreateResult{
		Path:   worktreePath,
		Branch: branch,
	}, nil
}

// Remove removes a git worktree and optionally deletes the branch.
func (m *Manager) Remove(issue int, deleteBranch bool) error {
	if issue <= 0 {
		return fmt.Errorf("invalid issue number %d: must be positive", issue)
	}

	worktreePath := filepath.Join(m.baseDir, fmt.Sprintf("issue-%d", issue))

	// Get the branch name before removing
	var branch string
	if deleteBranch {
		out, err := m.git.Run(worktreePath, "rev-parse", "--abbrev-ref", "HEAD")
		if err == nil {
			branch = out
		}
	}

	// Remove the worktree (without --force to protect uncommitted work)
	_, err := m.git.Run(m.repoDir, "worktree", "remove", worktreePath)
	if err != nil {
		return fmt.Errorf("remove worktree: %w", err)
	}

	// Delete the branch if requested
	if deleteBranch && branch != "" && branch != "main" && branch != "master" {
		if _, err := m.git.Run(m.repoDir, "branch", "-d", branch); err != nil {
			return fmt.Errorf("delete branch %q: %w", branch, err)
		}
	}

	return nil
}

// Path returns the worktree path for an issue.
func (m *Manager) Path(issue int) string {
	return filepath.Join(m.baseDir, fmt.Sprintf("issue-%d", issue))
}

var nonAlphaNum = regexp.MustCompile(`[^a-zA-Z0-9/_-]+`)

// sanitizeBranch cleans up a branch name.
func sanitizeBranch(name string) string {
	s := nonAlphaNum.ReplaceAllString(name, "-")
	s = strings.Trim(s, "-")
	if len(s) > 100 {
		s = s[:100]
	}
	return s
}
