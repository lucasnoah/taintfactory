package cli

import (
	"fmt"

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
		fmt.Printf("factory worktree create %s — not implemented\n", args[0])
		return nil
	},
}

var worktreeRemoveCmd = &cobra.Command{
	Use:   "remove [issue-number]",
	Short: "Remove a git worktree",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Printf("factory worktree remove %s — not implemented\n", args[0])
		return nil
	},
}

var worktreePathCmd = &cobra.Command{
	Use:   "path [issue-number]",
	Short: "Print the worktree path for an issue",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Printf("factory worktree path %s — not implemented\n", args[0])
		return nil
	},
}

func init() {
	worktreeCmd.AddCommand(worktreeCreateCmd)
	worktreeCmd.AddCommand(worktreeRemoveCmd)
	worktreeCmd.AddCommand(worktreePathCmd)
}
