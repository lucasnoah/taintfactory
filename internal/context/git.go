package context

import (
	"os/exec"
	"strings"
)

// ExecGit implements GitRunner by calling git commands.
// Diffs are computed against the merge-base with main, so they show
// what the feature branch changed rather than uncommitted edits.
type ExecGit struct{}

func (g *ExecGit) Diff(dir string) (string, error) {
	base, err := mergeBase(dir)
	if err != nil {
		// Fall back to HEAD diff if merge-base fails (e.g. no main branch)
		return runGit(dir, "diff", "HEAD")
	}
	return runGit(dir, "diff", base+"...HEAD")
}

func (g *ExecGit) DiffSummary(dir string) (string, error) {
	base, err := mergeBase(dir)
	if err != nil {
		return runGit(dir, "diff", "--stat", "HEAD")
	}
	return runGit(dir, "diff", "--stat", base+"...HEAD")
}

func (g *ExecGit) FilesChanged(dir string) (string, error) {
	base, err := mergeBase(dir)
	if err != nil {
		return runGit(dir, "diff", "--name-only", "HEAD")
	}
	return runGit(dir, "diff", "--name-only", base+"...HEAD")
}

func (g *ExecGit) Log(dir string) (string, error) {
	base, err := mergeBase(dir)
	if err != nil {
		return runGit(dir, "log", "--oneline", "-20")
	}
	return runGit(dir, "log", "--oneline", base+"..HEAD")
}

// mergeBase finds the common ancestor between HEAD and main/master.
func mergeBase(dir string) (string, error) {
	// Try main first, then master
	base, err := runGit(dir, "merge-base", "main", "HEAD")
	if err != nil {
		base, err = runGit(dir, "merge-base", "master", "HEAD")
	}
	return base, err
}

func runGit(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
