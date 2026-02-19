package cli

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/lucasnoah/taintfactory/internal/db"
	"github.com/lucasnoah/taintfactory/internal/pipeline"
	"github.com/lucasnoah/taintfactory/internal/session"
	"github.com/spf13/cobra"
)

var sessionCmd = &cobra.Command{
	Use:   "session",
	Short: "Manage Claude Code tmux sessions",
}

var sessionCreateCmd = &cobra.Command{
	Use:   "create [session-name]",
	Short: "Create a new Claude Code session in tmux",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		workdir, _ := cmd.Flags().GetString("workdir")
		flags, _ := cmd.Flags().GetString("flags")
		issue, _ := cmd.Flags().GetInt("issue")
		stage, _ := cmd.Flags().GetString("stage")
		interactive, _ := cmd.Flags().GetBool("interactive")

		mgr, cleanup, err := newSessionManager()
		if err != nil {
			return err
		}
		defer cleanup()

		if err := mgr.Create(session.CreateOpts{
			Name:        name,
			Workdir:     workdir,
			Flags:       flags,
			Issue:       issue,
			Stage:       stage,
			Interactive: interactive,
		}); err != nil {
			return err
		}

		fmt.Printf("Session %q created (issue=%d stage=%s)\n", name, issue, stage)
		return nil
	},
}

var sessionKillCmd = &cobra.Command{
	Use:   "kill [session-name]",
	Short: "Kill a tmux session and capture logs",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]

		mgr, cleanup, err := newSessionManager()
		if err != nil {
			return err
		}
		defer cleanup()

		log, err := mgr.Kill(name)
		if err != nil {
			return err
		}

		fmt.Printf("Session %q killed. Captured output:\n%s", name, log)
		return nil
	},
}

var sessionListCmd = &cobra.Command{
	Use:   "list",
	Short: "List active sessions",
	RunE: func(cmd *cobra.Command, args []string) error {
		issueFilter, _ := cmd.Flags().GetInt("issue")

		mgr, cleanup, err := newSessionManager()
		if err != nil {
			return err
		}
		defer cleanup()

		sessions, err := mgr.List(issueFilter)
		if err != nil {
			return err
		}

		if len(sessions) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "No active sessions.")
			return nil
		}

		w := cmd.OutOrStdout()
		fmt.Fprintf(w, "%-20s %-8s %-12s %-10s %-12s %s\n", "NAME", "ISSUE", "STAGE", "STATE", "ELAPSED", "ORPHAN")
		fmt.Fprintf(w, "%-20s %-8s %-12s %-10s %-12s %s\n",
			strings.Repeat("-", 20),
			strings.Repeat("-", 8),
			strings.Repeat("-", 12),
			strings.Repeat("-", 10),
			strings.Repeat("-", 12),
			strings.Repeat("-", 6))
		for _, s := range sessions {
			issueStr := ""
			if s.Issue > 0 {
				issueStr = fmt.Sprintf("%d", s.Issue)
			}
			fmt.Fprintf(w, "%-20s %-8s %-12s %-10s %-12s %s\n",
				s.Name, issueStr, s.Stage, s.State, s.Elapsed, s.Orphan)
		}
		return nil
	},
}

var sessionSendCmd = &cobra.Command{
	Use:   "send [session-name] [prompt]",
	Short: "Send a prompt to a running session",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		fromFile, _ := cmd.Flags().GetString("from-file")
		fromCheckFailures, _ := cmd.Flags().GetString("from-check-failures")

		mgr, cleanup, err := newSessionManager()
		if err != nil {
			return err
		}
		defer cleanup()

		switch {
		case fromFile != "":
			if err := mgr.SendFromFile(name, fromFile); err != nil {
				return err
			}
			fmt.Printf("Sent prompt from file %q to session %q\n", fromFile, name)

		case fromCheckFailures != "":
			issue, stage, err := parseIssueStage(fromCheckFailures)
			if err != nil {
				return fmt.Errorf("invalid --from-check-failures format (expected issue:stage): %w", err)
			}
			if err := mgr.SendFromCheckFailures(name, issue, stage); err != nil {
				return err
			}
			fmt.Printf("Sent fix prompt for issue %d stage %q to session %q\n", issue, stage, name)

		default:
			if len(args) < 2 {
				return fmt.Errorf("provide a prompt string, --from-file, or --from-check-failures")
			}
			prompt := strings.Join(args[1:], " ")
			if err := mgr.Send(name, prompt); err != nil {
				return err
			}
			fmt.Printf("Sent prompt to session %q\n", name)
		}

		return nil
	},
}

var sessionSteerCmd = &cobra.Command{
	Use:   "steer [session-name] [message]",
	Short: "Send a steering message to an active session",
	Args:  cobra.MinimumNArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		message := strings.Join(args[1:], " ")

		mgr, cleanup, err := newSessionManager()
		if err != nil {
			return err
		}
		defer cleanup()

		if err := mgr.Steer(name, message); err != nil {
			return err
		}

		fmt.Printf("Steered session %q\n", name)
		return nil
	},
}

var sessionPeekCmd = &cobra.Command{
	Use:   "peek [session-name]",
	Short: "Read recent output from a session",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		lines, _ := cmd.Flags().GetInt("lines")

		mgr, cleanup, err := newSessionManager()
		if err != nil {
			return err
		}
		defer cleanup()

		output, err := mgr.Peek(name, lines)
		if err != nil {
			return err
		}

		fmt.Print(output)
		return nil
	},
}

var sessionStatusCmd = &cobra.Command{
	Use:   "status [session-name]",
	Short: "Check session state (from DB events)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		detectHuman, _ := cmd.Flags().GetBool("detect-human")

		mgr, cleanup, err := newSessionManager()
		if err != nil {
			return err
		}
		defer cleanup()

		info, err := mgr.Status(name)
		if err != nil {
			return err
		}

		w := cmd.OutOrStdout()
		fmt.Fprintf(w, "Session:   %s\n", info.Name)
		fmt.Fprintf(w, "State:     %s\n", info.State)
		fmt.Fprintf(w, "Issue:     %d\n", info.Issue)
		fmt.Fprintf(w, "Stage:     %s\n", info.Stage)
		fmt.Fprintf(w, "Since:     %s (%s ago)\n", info.Timestamp, info.Elapsed)
		tmuxStatus := "dead"
		if info.TmuxAlive {
			tmuxStatus = "alive"
		}
		fmt.Fprintf(w, "Tmux:      %s\n", tmuxStatus)

		if detectHuman {
			human, err := mgr.DetectHuman(name)
			if err != nil {
				return fmt.Errorf("detect human: %w", err)
			}
			fmt.Fprintf(w, "Human:     %v\n", human)
		}

		return nil
	},
}

var sessionWaitIdleCmd = &cobra.Command{
	Use:   "wait-idle [session-name]",
	Short: "Block until a session becomes idle or exits",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		timeout, _ := cmd.Flags().GetDuration("timeout")
		pollInterval, _ := cmd.Flags().GetDuration("poll-interval")

		mgr, cleanup, err := newSessionManager()
		if err != nil {
			return err
		}
		defer cleanup()

		result, err := mgr.WaitIdle(name, timeout, pollInterval)
		if err != nil {
			return err
		}

		jsonStr, err := result.JSON()
		if err != nil {
			return err
		}
		fmt.Println(jsonStr)
		return nil
	},
}

func init() {
	sessionCreateCmd.Flags().String("workdir", "", "Working directory for the session")
	sessionCreateCmd.Flags().String("flags", "", "Flags to pass to Claude Code")
	sessionCreateCmd.Flags().Int("issue", 0, "Associated issue number")
	sessionCreateCmd.Flags().String("stage", "", "Associated pipeline stage")
	sessionCreateCmd.Flags().Bool("interactive", false, "Keep session in interactive mode")

	sessionListCmd.Flags().Int("issue", 0, "Filter by issue number")

	sessionSendCmd.Flags().String("from-file", "", "Read prompt from file")
	sessionSendCmd.Flags().String("from-check-failures", "", "Generate fix prompt from check failures (issue:stage)")

	sessionPeekCmd.Flags().Int("lines", 50, "Number of lines to show")

	sessionWaitIdleCmd.Flags().Duration("timeout", 0, "Maximum time to wait")
	sessionWaitIdleCmd.Flags().Duration("poll-interval", 0, "Polling interval")

	sessionStatusCmd.Flags().Bool("detect-human", false, "Check for human intervention")

	sessionCmd.AddCommand(sessionCreateCmd)
	sessionCmd.AddCommand(sessionKillCmd)
	sessionCmd.AddCommand(sessionListCmd)
	sessionCmd.AddCommand(sessionSendCmd)
	sessionCmd.AddCommand(sessionSteerCmd)
	sessionCmd.AddCommand(sessionPeekCmd)
	sessionCmd.AddCommand(sessionStatusCmd)
	sessionCmd.AddCommand(sessionWaitIdleCmd)
}

// newSessionManager opens the DB, migrates, and returns a Manager + cleanup func.
func newSessionManager() (*session.Manager, func(), error) {
	dbPath, err := db.DefaultDBPath()
	if err != nil {
		return nil, nil, err
	}
	d, err := db.Open(dbPath)
	if err != nil {
		return nil, nil, err
	}
	if err := d.Migrate(); err != nil {
		d.Close()
		return nil, nil, err
	}

	// Pipeline store is best-effort — don't fail if unavailable
	store, _ := pipeline.DefaultStore()

	mgr := session.NewManager(session.NewExecTmux(), d, store)
	return mgr, func() { d.Close() }, nil
}

// parseIssueStage parses "issue:stage" format (e.g., "42:impl").
func parseIssueStage(s string) (int, string, error) {
	parts := strings.SplitN(s, ":", 2)
	if len(parts) != 2 {
		return 0, "", fmt.Errorf("expected format issue:stage, got %q", s)
	}
	issue, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, "", fmt.Errorf("invalid issue number %q: %w", parts[0], err)
	}
	return issue, parts[1], nil
}
