package cli

import (
	"fmt"

	"github.com/lucasnoah/taintfactory/internal/db"
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
		sessionID, _ := cmd.Flags().GetString("session")
		event, _ := cmd.Flags().GetString("event")
		issue, _ := cmd.Flags().GetInt("issue")
		stage, _ := cmd.Flags().GetString("stage")
		metadata, _ := cmd.Flags().GetString("metadata")

		var exitCode *int
		if cmd.Flags().Changed("exit-code") {
			v, _ := cmd.Flags().GetInt("exit-code")
			exitCode = &v
		}

		dbPath, err := db.DefaultDBPath()
		if err != nil {
			return err
		}
		d, err := db.Open(dbPath)
		if err != nil {
			return err
		}
		defer d.Close()

		if err := d.Migrate(); err != nil {
			return err
		}

		// If issue/stage not provided, look them up from the session's last event
		if !cmd.Flags().Changed("issue") || !cmd.Flags().Changed("stage") {
			state, err := d.GetSessionState(sessionID)
			if err == nil && state != nil {
				if !cmd.Flags().Changed("issue") {
					issue = state.Issue
				}
				if !cmd.Flags().Changed("stage") {
					stage = state.Stage
				}
			}
		}

		if err := d.LogSessionEvent(sessionID, issue, stage, event, exitCode, metadata); err != nil {
			return err
		}
		fmt.Printf("Logged event: session=%s event=%s\n", sessionID, event)
		return nil
	},
}

func init() {
	eventLogCmd.Flags().String("session", "", "Session ID")
	eventLogCmd.Flags().String("event", "", "Event type: started, active, idle, exited, factory_send, steer, human_input")
	eventLogCmd.Flags().Int("issue", 0, "Issue number")
	eventLogCmd.Flags().String("stage", "", "Pipeline stage")
	eventLogCmd.Flags().Int("exit-code", 0, "Exit code (for exited events)")
	eventLogCmd.Flags().String("metadata", "", "JSON metadata")
	eventLogCmd.MarkFlagRequired("session")
	eventLogCmd.MarkFlagRequired("event")

	eventCmd.AddCommand(eventLogCmd)
}
