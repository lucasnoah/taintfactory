package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/lucasnoah/taintfactory/internal/pipeline"
	"github.com/lucasnoah/taintfactory/internal/worktree"
	"github.com/spf13/cobra"
)

var worktreeCmd = &cobra.Command{
	Use:   "worktree",
	Short: "Manage git worktrees for issues",
}

var worktreeCreateCmd = &cobra.Command{
	Use:   "create [issue-number]",
	Short: "Create a git worktree for an issue",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		issue, err := strconv.Atoi(args[0])
		if err != nil {
			return fmt.Errorf("invalid issue number %q: %w", args[0], err)
		}
		branch, _ := cmd.Flags().GetString("branch")

		repoDir, err := findRepoRoot()
		if err != nil {
			return err
		}
		baseDir := filepath.Join(repoDir, "worktrees")

		mgr := worktree.NewManager(&worktree.ExecGit{}, repoDir, baseDir)
		result, err := mgr.Create(worktree.CreateOpts{
			Issue:  issue,
			Branch: branch,
		})
		if err != nil {
			return err
		}

		// Update pipeline state if it exists
		store, storeErr := pipeline.DefaultStore()
		if storeErr == nil {
			_ = store.Update(issue, func(ps *pipeline.PipelineState) {
				ps.Worktree = result.Path
				ps.Branch = result.Branch
			})
		}

		fmt.Fprintf(cmd.OutOrStdout(), "Worktree created: %s (branch: %s)\n", result.Path, result.Branch)
		return nil
	},
}

var worktreeRemoveCmd = &cobra.Command{
	Use:   "remove [issue-number]",
	Short: "Remove a git worktree",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		issue, err := strconv.Atoi(args[0])
		if err != nil {
			return fmt.Errorf("invalid issue number %q: %w", args[0], err)
		}
		deleteBranch, _ := cmd.Flags().GetBool("delete-branch")

		repoDir, err := findRepoRoot()
		if err != nil {
			return err
		}
		baseDir := filepath.Join(repoDir, "worktrees")

		mgr := worktree.NewManager(&worktree.ExecGit{}, repoDir, baseDir)
		if err := mgr.Remove(issue, deleteBranch); err != nil {
			return err
		}

		fmt.Fprintf(cmd.OutOrStdout(), "Worktree removed for issue %d\n", issue)
		return nil
	},
}

var worktreePathCmd = &cobra.Command{
	Use:   "path [issue-number]",
	Short: "Print the worktree path for an issue",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		issue, err := strconv.Atoi(args[0])
		if err != nil {
			return fmt.Errorf("invalid issue number %q: %w", args[0], err)
		}

		// Try pipeline state first
		store, storeErr := pipeline.DefaultStore()
		if storeErr == nil {
			ps, psErr := store.Get(issue)
			if psErr == nil && ps.Worktree != "" {
				fmt.Fprintln(cmd.OutOrStdout(), ps.Worktree)
				return nil
			}
		}

		// Fall back to computed path
		repoDir, err := findRepoRoot()
		if err != nil {
			return err
		}
		path := filepath.Join(repoDir, "worktrees", fmt.Sprintf("issue-%d", issue))
		fmt.Fprintln(cmd.OutOrStdout(), path)
		return nil
	},
}

func init() {
	worktreeCreateCmd.Flags().String("branch", "", "Override the auto-generated branch name")
	worktreeRemoveCmd.Flags().Bool("delete-branch", true, "Also delete the git branch")

	worktreeCmd.AddCommand(worktreeCreateCmd)
	worktreeCmd.AddCommand(worktreeRemoveCmd)
	worktreeCmd.AddCommand(worktreePathCmd)
}

// findRepoRoot finds the git repository root.
func findRepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("get working dir: %w", err)
	}

	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("not in a git repository")
		}
		dir = parent
	}
}
