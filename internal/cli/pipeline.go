package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

var pipelineCmd = &cobra.Command{
	Use:   "pipeline",
	Short: "Manage pipelines for GitHub issues",
}

var pipelineCreateCmd = &cobra.Command{
	Use:   "create [issue-number]",
	Short: "Create a new pipeline for a GitHub issue",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Printf("factory pipeline create %s — not implemented\n", args[0])
		return nil
	},
}

var pipelineAdvanceCmd = &cobra.Command{
	Use:   "advance [issue-number]",
	Short: "Advance a pipeline to the next stage",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Printf("factory pipeline advance %s — not implemented\n", args[0])
		return nil
	},
}

var pipelineStatusCmd = &cobra.Command{
	Use:   "status [issue-number]",
	Short: "Show pipeline status",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) > 0 {
			fmt.Printf("factory pipeline status %s — not implemented\n", args[0])
		} else {
			fmt.Println("factory pipeline status — not implemented")
		}
		return nil
	},
}

var pipelineRetryCmd = &cobra.Command{
	Use:   "retry [issue-number]",
	Short: "Retry the current stage of a pipeline",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Printf("factory pipeline retry %s — not implemented\n", args[0])
		return nil
	},
}

var pipelineFailCmd = &cobra.Command{
	Use:   "fail [issue-number]",
	Short: "Mark a pipeline as failed",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Printf("factory pipeline fail %s — not implemented\n", args[0])
		return nil
	},
}

var pipelineAbortCmd = &cobra.Command{
	Use:   "abort [issue-number]",
	Short: "Abort a pipeline and clean up",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Printf("factory pipeline abort %s — not implemented\n", args[0])
		return nil
	},
}

func init() {
	pipelineCmd.AddCommand(pipelineCreateCmd)
	pipelineCmd.AddCommand(pipelineAdvanceCmd)
	pipelineCmd.AddCommand(pipelineStatusCmd)
	pipelineCmd.AddCommand(pipelineRetryCmd)
	pipelineCmd.AddCommand(pipelineFailCmd)
	pipelineCmd.AddCommand(pipelineAbortCmd)
}
