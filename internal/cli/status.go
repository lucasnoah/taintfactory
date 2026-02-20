package cli

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show status of all in-flight pipelines",
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
			data, _ := json.MarshalIndent(infos, "", "  ")
			fmt.Fprintln(cmd.OutOrStdout(), string(data))
			return nil
		}

		if len(infos) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "No pipelines found.")
			return nil
		}

		w := cmd.OutOrStdout()
		fmt.Fprintf(w, "%-8s %-12s %-14s %-4s %-20s %s\n", "ISSUE", "STATUS", "STAGE", "ATT", "SESSION", "TITLE")
		fmt.Fprintf(w, "%-8s %-12s %-14s %-4s %-20s %s\n",
			strings.Repeat("-", 8),
			strings.Repeat("-", 12),
			strings.Repeat("-", 14),
			strings.Repeat("-", 4),
			strings.Repeat("-", 20),
			strings.Repeat("-", 5))
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
			fmt.Fprintf(w, "%-8d %-12s %-14s %-4d %-20s %s\n",
				info.Issue, info.Status, info.Stage, info.Attempt, sessionStr, title)
		}
		return nil
	},
}

func init() {
	statusCmd.Flags().String("format", "text", "Output format: text or json")
}
