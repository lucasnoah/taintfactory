package cli

import (
	"encoding/json"
	"fmt"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

var orchestratorCmd = &cobra.Command{
	Use:   "orchestrator",
	Short: "Orchestrator utilities for cron-driven pipeline management",
}

var orchestratorCheckInCmd = &cobra.Command{
	Use:   "check-in",
	Short: "Run the full decision loop for all in-flight pipelines",
	Long: `Evaluates every active pipeline and takes the appropriate action:
  - Active session within timeout: skip
  - Active session past timeout: steer to wrap up
  - Idle/exited session: advance pipeline
  - Blocked pipeline: skip (human intervention needed)
  - Human intervention detected: skip

Designed to be called on a cron schedule (e.g. every 5 minutes).`,
	RunE: func(cmd *cobra.Command, args []string) error {
		orch, cleanup, err := newOrchestrator()
		if err != nil {
			return err
		}
		defer cleanup()

		result, err := orch.CheckIn()
		if err != nil {
			return err
		}

		format, _ := cmd.Flags().GetString("format")
		if format == "json" {
			return writeJSON(cmd, result)
		}

		w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
		if len(result.Actions) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "No active pipelines.")
			return nil
		}

		fmt.Fprintln(w, "ISSUE\tACTION\tSTAGE\tMESSAGE")
		for _, a := range result.Actions {
			msg := a.Message
			if len(msg) > 60 {
				msg = msg[:57] + "..."
			}
			fmt.Fprintf(w, "%d\t%s\t%s\t%s\n", a.Issue, a.Action, a.Stage, msg)
		}
		return w.Flush()
	},
}

var orchestratorStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show all pipeline statuses in orchestrator-friendly format",
	RunE: func(cmd *cobra.Command, args []string) error {
		orch, cleanup, err := newOrchestrator()
		if err != nil {
			return err
		}
		defer cleanup()

		infos, err := orch.StatusAll()
		if err != nil {
			return err
		}

		format, _ := cmd.Flags().GetString("format")
		if format == "json" {
			data, err := json.MarshalIndent(infos, "", "  ")
			if err != nil {
				return fmt.Errorf("marshal json: %w", err)
			}
			fmt.Fprintln(cmd.OutOrStdout(), string(data))
			return nil
		}

		if len(infos) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "No pipelines found.")
			return nil
		}

		w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "ISSUE\tSTATUS\tSTAGE\tATTEMPT\tSESSION\tTITLE")
		for _, info := range infos {
			sessionStr := ""
			if info.Session != "" {
				sessionStr = info.Session
				if info.SessionState != "" {
					sessionStr = fmt.Sprintf("%s(%s)", info.Session, info.SessionState)
				}
			}
			title := info.Title
			if len(title) > 40 {
				title = title[:37] + "..."
			}
			fmt.Fprintf(w, "%d\t%s\t%s\t%d\t%s\t%s\n",
				info.Issue, info.Status, info.Stage, info.Attempt, sessionStr, title)
		}
		return w.Flush()
	},
}

func init() {
	orchestratorCheckInCmd.Flags().String("format", "text", "Output format: text or json")
	orchestratorStatusCmd.Flags().String("format", "text", "Output format: text or json")
	orchestratorCmd.AddCommand(orchestratorCheckInCmd)
	orchestratorCmd.AddCommand(orchestratorStatusCmd)
}
