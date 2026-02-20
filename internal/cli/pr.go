package cli

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/lucasnoah/taintfactory/internal/github"
	"github.com/lucasnoah/taintfactory/internal/pipeline"
	"github.com/spf13/cobra"
)

var prCmd = &cobra.Command{
	Use:   "pr",
	Short: "Manage pull requests",
}

var prCreateCmd = &cobra.Command{
	Use:   "create [issue-number]",
	Short: "Create a pull request for an issue",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		issue, err := strconv.Atoi(args[0])
		if err != nil {
			return fmt.Errorf("invalid issue number %q: %w", args[0], err)
		}

		store, err := pipeline.DefaultStore()
		if err != nil {
			return fmt.Errorf("open pipeline store: %w", err)
		}

		ps, err := store.Get(issue)
		if err != nil {
			return fmt.Errorf("get pipeline state: %w", err)
		}

		// Push branch first
		runner := &github.ExecRunner{}
		gh := github.NewClientWithGit(runner, runner)
		if err := gh.PushBranch(ps.Worktree, ps.Branch); err != nil {
			return fmt.Errorf("push branch: %w", err)
		}

		// Build PR body from pipeline data
		title := fmt.Sprintf("Issue #%d: %s", ps.Issue, ps.Title)
		body := buildPRBody(ps)

		result, err := gh.CreatePR(github.PRCreateOpts{
			Title:  title,
			Body:   body,
			Branch: ps.Branch,
			Base:   "main",
		})
		if err != nil {
			return err
		}

		// Log pipeline event
		d, cleanup, dbErr := openDB()
		if dbErr == nil {
			defer cleanup()
			_ = d.LogPipelineEvent(issue, "pr_created", ps.CurrentStage, ps.CurrentAttempt, result.URL)
		}

		fmt.Fprintf(cmd.OutOrStdout(), "PR created: %s\n", result.URL)
		return nil
	},
}

var prMergeCmd = &cobra.Command{
	Use:   "merge [issue-number]",
	Short: "Merge a pull request for an issue",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		issue, err := strconv.Atoi(args[0])
		if err != nil {
			return fmt.Errorf("invalid issue number %q: %w", args[0], err)
		}
		strategy, _ := cmd.Flags().GetString("strategy")

		store, err := pipeline.DefaultStore()
		if err != nil {
			return fmt.Errorf("open pipeline store: %w", err)
		}

		ps, err := store.Get(issue)
		if err != nil {
			return fmt.Errorf("get pipeline state: %w", err)
		}

		gh := github.NewClient(&github.ExecRunner{})
		if err := gh.MergePR(ps.Branch, strategy); err != nil {
			return err
		}

		// Update pipeline status
		_ = store.Update(issue, func(ps *pipeline.PipelineState) {
			ps.Status = "completed"
		})

		// Log pipeline event
		d, cleanup, dbErr := openDB()
		if dbErr == nil {
			defer cleanup()
			_ = d.LogPipelineEvent(issue, "pr_merged", ps.CurrentStage, ps.CurrentAttempt, "")
		}

		fmt.Fprintf(cmd.OutOrStdout(), "PR merged for issue %d\n", issue)
		return nil
	},
}

func init() {
	prMergeCmd.Flags().String("strategy", "squash", "Merge strategy: squash, merge, or rebase")

	prCmd.AddCommand(prCreateCmd)
	prCmd.AddCommand(prMergeCmd)
}

// buildPRBody constructs a PR description from pipeline state.
func buildPRBody(ps *pipeline.PipelineState) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Closes #%d\n\n", ps.Issue))
	sb.WriteString("## Summary\n")
	sb.WriteString(fmt.Sprintf("Pipeline run for: %s\n\n", ps.Title))

	if len(ps.StageHistory) > 0 {
		sb.WriteString("## Stage History\n")
		for _, entry := range ps.StageHistory {
			sb.WriteString(fmt.Sprintf("- **%s** (attempt %d): %s", entry.Stage, entry.Attempt, entry.Outcome))
			if entry.FixRounds > 0 {
				sb.WriteString(fmt.Sprintf(" (%d fix rounds)", entry.FixRounds))
			}
			sb.WriteString("\n")
		}
	}

	return sb.String()
}

// openDB is defined in check.go and shared across the cli package.
