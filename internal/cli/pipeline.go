package cli

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/lucasnoah/taintfactory/internal/pipeline"
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

var pipelineListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all pipelines",
	RunE: func(cmd *cobra.Command, args []string) error {
		store, err := pipeline.DefaultStore()
		if err != nil {
			return fmt.Errorf("open store: %w", err)
		}

		statusFilter, _ := cmd.Flags().GetString("status")
		pipelines, err := store.List(statusFilter)
		if err != nil {
			return fmt.Errorf("list pipelines: %w", err)
		}

		if len(pipelines) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "No pipelines found.")
			return nil
		}

		w := cmd.OutOrStdout()
		fmt.Fprintf(w, "%-8s %-12s %-14s %-30s %s\n", "ISSUE", "STATUS", "STAGE", "BRANCH", "TITLE")
		fmt.Fprintf(w, "%-8s %-12s %-14s %-30s %s\n",
			strings.Repeat("-", 8),
			strings.Repeat("-", 12),
			strings.Repeat("-", 14),
			strings.Repeat("-", 30),
			strings.Repeat("-", 5))
		for _, p := range pipelines {
			fmt.Fprintf(w, "%-8d %-12s %-14s %-30s %s\n",
				p.Issue, p.Status, p.CurrentStage, p.Branch, p.Title)
		}
		return nil
	},
}

var pipelineStatusCmd = &cobra.Command{
	Use:   "status <issue-number>",
	Short: "Show detailed pipeline status",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		issue, err := strconv.Atoi(args[0])
		if err != nil {
			return fmt.Errorf("invalid issue number: %s", args[0])
		}

		store, err := pipeline.DefaultStore()
		if err != nil {
			return fmt.Errorf("open store: %w", err)
		}

		ps, err := store.Get(issue)
		if err != nil {
			return err
		}

		w := cmd.OutOrStdout()
		fmt.Fprintf(w, "Pipeline #%d: %s\n", ps.Issue, ps.Title)
		fmt.Fprintf(w, "  Status:        %s\n", ps.Status)
		fmt.Fprintf(w, "  Branch:        %s\n", ps.Branch)
		fmt.Fprintf(w, "  Worktree:      %s\n", ps.Worktree)
		fmt.Fprintf(w, "  Current Stage: %s (attempt %d)\n", ps.CurrentStage, ps.CurrentAttempt)
		if ps.CurrentSession != "" {
			fmt.Fprintf(w, "  Session:       %s\n", ps.CurrentSession)
		}
		if ps.CurrentFixRound > 0 {
			fmt.Fprintf(w, "  Fix Round:     %d\n", ps.CurrentFixRound)
		}
		fmt.Fprintf(w, "  Created:       %s\n", ps.CreatedAt)
		fmt.Fprintf(w, "  Updated:       %s\n", ps.UpdatedAt)

		if len(ps.GoalGates) > 0 {
			fmt.Fprintln(w, "  Goal Gates:")
			for k, v := range ps.GoalGates {
				fmt.Fprintf(w, "    %s: %s\n", k, v)
			}
		}

		if len(ps.StageHistory) > 0 {
			fmt.Fprintln(w, "  Stage History:")
			for _, h := range ps.StageHistory {
				firstPass := ""
				if h.ChecksFirstPass {
					firstPass = " (first-pass)"
				}
				fmt.Fprintf(w, "    %s attempt %d: %s (%s, %d fix rounds%s)\n",
					h.Stage, h.Attempt, h.Outcome, h.Duration, h.FixRounds, firstPass)
			}
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
	pipelineCmd.AddCommand(pipelineListCmd)
	pipelineCmd.AddCommand(pipelineStatusCmd)
	pipelineCmd.AddCommand(pipelineRetryCmd)
	pipelineCmd.AddCommand(pipelineFailCmd)
	pipelineCmd.AddCommand(pipelineAbortCmd)

	pipelineListCmd.Flags().String("status", "", "Filter by status (pending, in_progress, completed, failed, blocked)")
}
