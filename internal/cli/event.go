package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

var eventCmd = &cobra.Command{
	Use:   "event",
	Short: "Log events from Claude Code hooks",
}

var eventLogCmd = &cobra.Command{
	Use:   "log",
	Short: "Log a session event (called by Claude Code hooks)",
	RunE: func(cmd *cobra.Command, args []string) error {
		session, _ := cmd.Flags().GetString("session")
		event, _ := cmd.Flags().GetString("event")
		fmt.Printf("factory event log --session=%s --event=%s — not implemented\n", session, event)
		return nil
	},
}

func init() {
	eventLogCmd.Flags().String("session", "", "Session ID")
	eventLogCmd.Flags().String("event", "", "Event type: started, active, idle, exited")
	eventLogCmd.Flags().Int("exit-code", 0, "Exit code (for exited events)")
	eventLogCmd.Flags().String("metadata", "", "JSON metadata")
	eventLogCmd.MarkFlagRequired("session")
	eventLogCmd.MarkFlagRequired("event")

	eventCmd.AddCommand(eventLogCmd)
}
