package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

var checkCmd = &cobra.Command{
	Use:   "check",
	Short: "Run and manage deterministic checks (lint, test, etc.)",
}

var checkRunCmd = &cobra.Command{
	Use:   "run [issue] [check-names...]",
	Short: "Run one or more checks in an issue's worktree",
	Args:  cobra.MinimumNArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Printf("factory check run %s %v — not implemented\n", args[0], args[1:])
		return nil
	},
}

var checkGateCmd = &cobra.Command{
	Use:   "gate [issue] [stage]",
	Short: "Run all checks for a pipeline stage",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Printf("factory check gate %s %s — not implemented\n", args[0], args[1])
		return nil
	},
}

var checkResultCmd = &cobra.Command{
	Use:   "result [issue] [check-name]",
	Short: "Show the latest result for a check",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Printf("factory check result %s %s — not implemented\n", args[0], args[1])
		return nil
	},
}

var checkHistoryCmd = &cobra.Command{
	Use:   "history [issue]",
	Short: "Show all check runs for an issue",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Printf("factory check history %s — not implemented\n", args[0])
		return nil
	},
}

func init() {
	checkRunCmd.Flags().Bool("fix", false, "Run auto-fix before re-checking")
	checkRunCmd.Flags().Bool("continue", false, "Continue running checks after failures")

	checkGateCmd.Flags().Bool("continue", false, "Run all checks even if some fail")
	checkGateCmd.Flags().Int("fix-round", 0, "Tag this gate run with fix round number")
	checkGateCmd.Flags().String("format", "text", "Output format: text or json")

	checkHistoryCmd.Flags().String("check", "", "Filter by check name")
	checkHistoryCmd.Flags().String("stage", "", "Filter by stage")

	checkCmd.AddCommand(checkRunCmd)
	checkCmd.AddCommand(checkGateCmd)
	checkCmd.AddCommand(checkResultCmd)
	checkCmd.AddCommand(checkHistoryCmd)
}
