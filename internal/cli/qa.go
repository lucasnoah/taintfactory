package cli

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/lucasnoah/taintfactory/internal/config"
	appctx "github.com/lucasnoah/taintfactory/internal/context"
	"github.com/lucasnoah/taintfactory/internal/github"
	"github.com/lucasnoah/taintfactory/internal/pipeline"
	"github.com/lucasnoah/taintfactory/internal/qa"
	"github.com/spf13/cobra"
)

var qaCmd = &cobra.Command{
	Use:   "qa",
	Short: "QA and browser testing utilities",
}

var qaDetectBrowserCmd = &cobra.Command{
	Use:   "detect-browser [issue-number]",
	Short: "Detect if an issue requires browser testing",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		issue, err := strconv.Atoi(args[0])
		if err != nil {
			return fmt.Errorf("invalid issue number: %s", args[0])
		}
		if issue <= 0 {
			return fmt.Errorf("issue number must be positive, got %d", issue)
		}

		ghClient := github.NewClient(&github.ExecRunner{})

		// Load config for stage browser_check flag
		cfg, cfgErr := config.LoadDefault()
		forceFlag := false
		stageID, _ := cmd.Flags().GetString("stage")
		if cfgErr != nil && stageID != "" {
			fmt.Fprintf(cmd.ErrOrStderr(), "warning: could not load config: %v\n", cfgErr)
		}
		if cfg != nil && stageID != "" {
			for _, s := range cfg.Pipeline.Stages {
				if s.ID == stageID && s.BrowserCheck {
					forceFlag = true
				}
			}
		}

		// Fetch issue
		ghIssue, err := ghClient.GetIssue(issue)
		if err != nil {
			return fmt.Errorf("fetch issue: %w", err)
		}

		// Get files changed from worktree if available
		var filesChanged []string
		store, storeErr := pipeline.DefaultStore()
		if storeErr == nil {
			ps, psErr := store.Get(issue)
			if psErr == nil && ps.Worktree != "" {
				git := &appctx.ExecGit{}
				if output, err := git.FilesChanged(ps.Worktree); err == nil && output != "" {
					output = strings.TrimRight(output, "\n")
					filesChanged = strings.Split(output, "\n")
				}
			}
		}

		result := qa.DetectBrowserTest(qa.DetectOpts{
			Issue:        ghIssue,
			FilesChanged: filesChanged,
			ForceFlag:    forceFlag,
		})

		format, _ := cmd.Flags().GetString("format")
		if format == "json" {
			data, err := json.MarshalIndent(result, "", "  ")
			if err != nil {
				return fmt.Errorf("marshal result: %w", err)
			}
			fmt.Fprintln(cmd.OutOrStdout(), string(data))
			return nil
		}

		w := cmd.OutOrStdout()
		if result.BrowserTestNeeded {
			fmt.Fprintln(w, "Browser testing: NEEDED")
		} else {
			fmt.Fprintln(w, "Browser testing: not needed")
		}
		if len(result.Reasons) > 0 {
			fmt.Fprintln(w, "Reasons:")
			for _, r := range result.Reasons {
				fmt.Fprintf(w, "  - %s\n", r)
			}
		}
		if len(result.AffectedRoutes) > 0 {
			fmt.Fprintf(w, "Affected routes: %s\n", strings.Join(result.AffectedRoutes, ", "))
		}
		if len(result.AffectedComponents) > 0 {
			fmt.Fprintf(w, "Affected components: %s\n", strings.Join(result.AffectedComponents, ", "))
		}
		return nil
	},
}

func init() {
	qaDetectBrowserCmd.Flags().String("format", "text", "Output format: text or json")
	qaDetectBrowserCmd.Flags().String("stage", "", "Stage ID to check for browser_check config flag")
	qaCmd.AddCommand(qaDetectBrowserCmd)
}
