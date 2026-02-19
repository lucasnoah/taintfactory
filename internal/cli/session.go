package cli

import (
	"fmt"

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
		fmt.Printf("factory session create %s — not implemented\n", args[0])
		return nil
	},
}

var sessionKillCmd = &cobra.Command{
	Use:   "kill [session-name]",
	Short: "Kill a tmux session and capture logs",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Printf("factory session kill %s — not implemented\n", args[0])
		return nil
	},
}

var sessionListCmd = &cobra.Command{
	Use:   "list",
	Short: "List active sessions",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println("factory session list — not implemented")
		return nil
	},
}

var sessionSendCmd = &cobra.Command{
	Use:   "send [session-name] [prompt]",
	Short: "Send a prompt to a running session",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Printf("factory session send %s — not implemented\n", args[0])
		return nil
	},
}

var sessionSteerCmd = &cobra.Command{
	Use:   "steer [session-name] [message]",
	Short: "Send a steering message to an active session",
	Args:  cobra.MinimumNArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Printf("factory session steer %s — not implemented\n", args[0])
		return nil
	},
}

var sessionPeekCmd = &cobra.Command{
	Use:   "peek [session-name]",
	Short: "Read recent output from a session",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Printf("factory session peek %s — not implemented\n", args[0])
		return nil
	},
}

var sessionStatusCmd = &cobra.Command{
	Use:   "status [session-name]",
	Short: "Check session state (from DB events)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Printf("factory session status %s — not implemented\n", args[0])
		return nil
	},
}

var sessionWaitIdleCmd = &cobra.Command{
	Use:   "wait-idle [session-name]",
	Short: "Block until a session becomes idle or exits",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Printf("factory session wait-idle %s — not implemented\n", args[0])
		return nil
	},
}

func init() {
	sessionCreateCmd.Flags().String("workdir", "", "Working directory for the session")
	sessionCreateCmd.Flags().String("flags", "", "Flags to pass to Claude Code")
	sessionCreateCmd.Flags().Int("issue", 0, "Associated issue number")
	sessionCreateCmd.Flags().String("stage", "", "Associated pipeline stage")
	sessionCreateCmd.Flags().Bool("interactive", false, "Keep session in interactive mode")

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
