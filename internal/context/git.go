package context

import (
	"os/exec"
	"strings"
)

// ExecGit implements GitRunner by calling git commands.
type ExecGit struct{}

func (g *ExecGit) Diff(dir string) (string, error) {
	return runGit(dir, "diff", "HEAD")
}

func (g *ExecGit) DiffSummary(dir string) (string, error) {
	return runGit(dir, "diff", "--stat", "HEAD")
}

func (g *ExecGit) FilesChanged(dir string) (string, error) {
	return runGit(dir, "diff", "--name-only", "HEAD")
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
