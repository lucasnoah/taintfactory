package cli

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/lucasnoah/taintfactory/internal/checks"
	"github.com/lucasnoah/taintfactory/internal/config"
	appctx "github.com/lucasnoah/taintfactory/internal/context"
	"github.com/lucasnoah/taintfactory/internal/db"
	"github.com/lucasnoah/taintfactory/internal/github"
	"github.com/lucasnoah/taintfactory/internal/orchestrator"
	"github.com/lucasnoah/taintfactory/internal/pipeline"
	"github.com/lucasnoah/taintfactory/internal/session"
	"github.com/lucasnoah/taintfactory/internal/stage"
	"github.com/lucasnoah/taintfactory/internal/worktree"
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
		issue, err := strconv.Atoi(args[0])
		if err != nil {
			return fmt.Errorf("invalid issue number: %s", args[0])
		}

		orch, cleanup, err := newOrchestrator()
		if err != nil {
			return err
		}
		defer cleanup()

		ps, err := orch.Create(orchestrator.CreateOpts{Issue: issue})
		if err != nil {
			return err
		}

		w := cmd.OutOrStdout()
		fmt.Fprintf(w, "Pipeline #%d created\n", ps.Issue)
		fmt.Fprintf(w, "  Title:    %s\n", ps.Title)
		fmt.Fprintf(w, "  Branch:   %s\n", ps.Branch)
		fmt.Fprintf(w, "  Worktree: %s\n", ps.Worktree)
		fmt.Fprintf(w, "  Stage:    %s\n", ps.CurrentStage)
		return nil
	},
}

var pipelineAdvanceCmd = &cobra.Command{
	Use:   "advance [issue-number]",
	Short: "Advance a pipeline to the next stage",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		issue, err := strconv.Atoi(args[0])
		if err != nil {
			return fmt.Errorf("invalid issue number: %s", args[0])
		}

		orch, cleanup, err := newOrchestrator()
		if err != nil {
			return err
		}
		defer cleanup()

		result, err := orch.Advance(issue)
		if err != nil {
			return err
		}

		format, _ := cmd.Flags().GetString("format")
		if format == "json" {
			data, _ := json.MarshalIndent(result, "", "  ")
			fmt.Fprintln(cmd.OutOrStdout(), string(data))
			return nil
		}

		w := cmd.OutOrStdout()
		fmt.Fprintf(w, "Pipeline #%d: %s\n", result.Issue, result.Action)
		if result.Stage != "" {
			fmt.Fprintf(w, "  Stage:     %s\n", result.Stage)
		}
		if result.NextStage != "" {
			fmt.Fprintf(w, "  Next:      %s\n", result.NextStage)
		}
		if result.Outcome != "" {
			fmt.Fprintf(w, "  Outcome:   %s\n", result.Outcome)
		}
		if result.FixRounds > 0 {
			fmt.Fprintf(w, "  Fix Rounds: %d\n", result.FixRounds)
		}
		if result.Message != "" {
			fmt.Fprintf(w, "  Message:   %s\n", result.Message)
		}
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

		orch, cleanup, err := newOrchestrator()
		if err != nil {
			return err
		}
		defer cleanup()

		format, _ := cmd.Flags().GetString("format")
		info, err := orch.Status(issue)
		if err != nil {
			return err
		}

		if format == "json" {
			data, _ := json.MarshalIndent(info, "", "  ")
			fmt.Fprintln(cmd.OutOrStdout(), string(data))
			return nil
		}

		w := cmd.OutOrStdout()
		fmt.Fprintf(w, "Pipeline #%d: %s\n", info.Issue, info.Title)
		fmt.Fprintf(w, "  Status:        %s\n", info.Status)
		fmt.Fprintf(w, "  Branch:        %s\n", info.Branch)
		fmt.Fprintf(w, "  Current Stage: %s (attempt %d)\n", info.Stage, info.Attempt)
		if info.Session != "" {
			fmt.Fprintf(w, "  Session:       %s (%s)\n", info.Session, info.SessionState)
		}
		if info.FixRound > 0 {
			fmt.Fprintf(w, "  Fix Round:     %d\n", info.FixRound)
		}

		if len(info.GoalGates) > 0 {
			fmt.Fprintln(w, "  Goal Gates:")
			for k, v := range info.GoalGates {
				status := v
				if status == "" {
					status = "pending"
				}
				fmt.Fprintf(w, "    %s: %s\n", k, status)
			}
		}

		if len(info.StageHistory) > 0 {
			fmt.Fprintln(w, "  Stage History:")
			for _, h := range info.StageHistory {
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
		issue, err := strconv.Atoi(args[0])
		if err != nil {
			return fmt.Errorf("invalid issue number: %s", args[0])
		}

		reason, _ := cmd.Flags().GetString("reason")

		orch, cleanup, err := newOrchestrator()
		if err != nil {
			return err
		}
		defer cleanup()

		if err := orch.Retry(orchestrator.RetryOpts{Issue: issue, Reason: reason}); err != nil {
			return err
		}

		fmt.Fprintf(cmd.OutOrStdout(), "Pipeline #%d: retry queued\n", issue)
		return nil
	},
}

var pipelineFailCmd = &cobra.Command{
	Use:   "fail [issue-number]",
	Short: "Mark a pipeline as failed",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		issue, err := strconv.Atoi(args[0])
		if err != nil {
			return fmt.Errorf("invalid issue number: %s", args[0])
		}

		reason, _ := cmd.Flags().GetString("reason")

		orch, cleanup, err := newOrchestrator()
		if err != nil {
			return err
		}
		defer cleanup()

		if err := orch.Fail(orchestrator.FailOpts{Issue: issue, Reason: reason}); err != nil {
			return err
		}

		fmt.Fprintf(cmd.OutOrStdout(), "Pipeline #%d: marked as failed\n", issue)
		return nil
	},
}

var pipelineAbortCmd = &cobra.Command{
	Use:   "abort [issue-number]",
	Short: "Abort a pipeline and clean up",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		issue, err := strconv.Atoi(args[0])
		if err != nil {
			return fmt.Errorf("invalid issue number: %s", args[0])
		}

		removeWorktree, _ := cmd.Flags().GetBool("remove-worktree")

		orch, cleanup, err := newOrchestrator()
		if err != nil {
			return err
		}
		defer cleanup()

		if err := orch.Abort(orchestrator.AbortOpts{Issue: issue, RemoveWorktree: removeWorktree}); err != nil {
			return err
		}

		fmt.Fprintf(cmd.OutOrStdout(), "Pipeline #%d: aborted\n", issue)
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
	pipelineStatusCmd.Flags().String("format", "text", "Output format: text or json")
	pipelineAdvanceCmd.Flags().String("format", "text", "Output format: text or json")
	pipelineRetryCmd.Flags().String("reason", "", "Reason for retry")
	pipelineFailCmd.Flags().String("reason", "", "Reason for failure")
	pipelineAbortCmd.Flags().Bool("remove-worktree", false, "Remove the worktree after aborting")
}

// newOrchestrator builds a fully-wired Orchestrator from default paths/config.
func newOrchestrator() (*orchestrator.Orchestrator, func(), error) {
	cfg, err := config.LoadDefault()
	if err != nil {
		return nil, nil, fmt.Errorf("load config: %w", err)
	}

	dbPath, err := db.DefaultDBPath()
	if err != nil {
		return nil, nil, err
	}
	database, err := db.Open(dbPath)
	if err != nil {
		return nil, nil, err
	}
	if err := database.Migrate(); err != nil {
		database.Close()
		return nil, nil, err
	}

	store, err := pipeline.DefaultStore()
	if err != nil {
		database.Close()
		return nil, nil, fmt.Errorf("open store: %w", err)
	}

	runner := &github.ExecRunner{}
	ghClient := github.NewClientWithGit(runner, runner)

	repoDir, err := findRepoRoot()
	if err != nil {
		database.Close()
		return nil, nil, err
	}
	wtDir := filepath.Join(repoDir, "worktrees")
	wt := worktree.NewManager(&worktree.ExecGit{}, repoDir, wtDir)

	tmux := session.NewExecTmux()
	sessions := session.NewManager(tmux, database, store)

	checker := checks.NewRunner(&checks.ExecRunner{})
	builder := appctx.NewBuilder(store, &appctx.ExecGit{})
	engine := stage.NewEngine(sessions, checker, builder, store, database, cfg)

	orch := orchestrator.NewOrchestrator(store, database, ghClient, wt, sessions, engine, builder, cfg)

	cleanup := func() { database.Close() }
	return orch, cleanup, nil
}
