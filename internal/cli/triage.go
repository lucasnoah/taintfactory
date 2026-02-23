package cli

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/lucasnoah/taintfactory/internal/db"
	"github.com/lucasnoah/taintfactory/internal/github"
	"github.com/lucasnoah/taintfactory/internal/session"
	"github.com/lucasnoah/taintfactory/internal/triage"
	"github.com/spf13/cobra"
)

var triageCmd = &cobra.Command{
	Use:   "triage",
	Short: "Triage GitHub issues using AI agents",
}

var triageRunCmd = &cobra.Command{
	Use:   "run <issue>",
	Short: "Enqueue and start a triage pipeline for a GitHub issue",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		issue, err := strconv.Atoi(args[0])
		if err != nil || issue <= 0 {
			return fmt.Errorf("invalid issue number %q: must be a positive integer", args[0])
		}

		runner, cleanup, err := newTriageRunner()
		if err != nil {
			return err
		}
		defer cleanup()

		ghClient := github.NewClient(&github.ExecRunner{})
		issueData, err := ghClient.GetIssue(issue)
		if err != nil {
			return fmt.Errorf("fetch issue #%d: %w", issue, err)
		}

		if err := runner.Enqueue(issue, issueData.Title, issueData.Body); err != nil {
			return fmt.Errorf("enqueue issue #%d: %w", issue, err)
		}

		fmt.Fprintf(cmd.OutOrStdout(), "  → triage #%d: starting %q\n", issue, runner.FirstStageID())
		return nil
	},
}

var triageStatusCmd = &cobra.Command{
	Use:   "status <issue>",
	Short: "Show current stage and history for a triage pipeline",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		issue, err := strconv.Atoi(args[0])
		if err != nil || issue <= 0 {
			return fmt.Errorf("invalid issue number %q: must be a positive integer", args[0])
		}

		_, store, cleanup, err := newTriageStore()
		if err != nil {
			return err
		}
		defer cleanup()

		st, err := store.Get(issue)
		if err != nil {
			return fmt.Errorf("get triage state for #%d: %w", issue, err)
		}

		w := cmd.OutOrStdout()
		fmt.Fprintf(w, "Issue:   #%d\n", st.Issue)
		fmt.Fprintf(w, "Status:  %s\n", st.Status)
		fmt.Fprintf(w, "Stage:   %s\n", st.CurrentStage)
		if st.CurrentSession != "" {
			fmt.Fprintf(w, "Session: %s\n", st.CurrentSession)
		}

		if len(st.StageHistory) > 0 {
			fmt.Fprintln(w)
			tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
			fmt.Fprintln(tw, "STAGE\tOUTCOME\tSUMMARY")
			for _, h := range st.StageHistory {
				fmt.Fprintf(tw, "%s\t%s\t%s\n", h.Stage, h.Outcome, h.Summary)
			}
			tw.Flush()
		}

		return nil
	},
}

var triageListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all triage pipelines for this repo",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		_, store, cleanup, err := newTriageStore()
		if err != nil {
			return err
		}
		defer cleanup()

		states, err := store.List("")
		if err != nil {
			return fmt.Errorf("list triage pipelines: %w", err)
		}

		w := cmd.OutOrStdout()
		if len(states) == 0 {
			fmt.Fprintln(w, "No triage pipelines found.")
			return nil
		}

		tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "ISSUE\tSTATUS\tSTAGE\tSESSION")
		for _, st := range states {
			fmt.Fprintf(tw, "#%d\t%s\t%s\t%s\n", st.Issue, st.Status, st.CurrentStage, st.CurrentSession)
		}
		return tw.Flush()
	},
}

// newTriageStore builds the config and store needed for read-only triage commands.
func newTriageStore() (*triage.TriageConfig, *triage.Store, func(), error) {
	repoRoot, err := findRepoRoot()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("find repo root: %w", err)
	}

	cfg, err := triage.LoadDefault(repoRoot)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("load triage config: %w", err)
	}

	slug := repoSlug(cfg.Triage.Repo)
	store, err := triage.DefaultStore(slug)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("open triage store: %w", err)
	}

	return cfg, store, func() {}, nil
}

// newTriageRunner builds a fully-wired triage Runner.
func newTriageRunner() (*triage.Runner, func(), error) {
	repoRoot, err := findRepoRoot()
	if err != nil {
		return nil, nil, fmt.Errorf("find repo root: %w", err)
	}

	cfg, err := triage.LoadDefault(repoRoot)
	if err != nil {
		return nil, nil, fmt.Errorf("load triage config: %w", err)
	}

	slug := repoSlug(cfg.Triage.Repo)
	store, err := triage.DefaultStore(slug)
	if err != nil {
		return nil, nil, fmt.Errorf("open triage store: %w", err)
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

	tmux := session.NewExecTmux()
	sessions := session.NewManager(tmux, database, nil)

	ghRunner := &github.ExecRunner{}
	ghClient := github.NewClient(ghRunner)

	runner := triage.NewRunner(cfg, store, database, sessions, ghClient, repoRoot)
	runner.SetProgress(os.Stderr)

	cleanup := func() { database.Close() }
	return runner, cleanup, nil
}

// repoSlug converts a "owner/repo" string into a filesystem-safe slug.
func repoSlug(repo string) string {
	return strings.ReplaceAll(repo, "/", "-")
}

func init() {
	triageCmd.AddCommand(triageRunCmd)
	triageCmd.AddCommand(triageStatusCmd)
	triageCmd.AddCommand(triageListCmd)
}
