package cli

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/lucasnoah/taintfactory/internal/analytics"
	"github.com/lucasnoah/taintfactory/internal/db"
	"github.com/spf13/cobra"
)

var analyticsCmd = &cobra.Command{
	Use:   "analytics",
	Short: "Query pipeline performance analytics",
}

func openAnalyticsDB() (*db.DB, error) {
	dbPath, err := db.DefaultDBPath()
	if err != nil {
		return nil, err
	}
	d, err := db.Open(dbPath)
	if err != nil {
		return nil, err
	}
	if err := d.Migrate(); err != nil {
		d.Close()
		return nil, err
	}
	return d, nil
}

func writeJSON(cmd *cobra.Command, v interface{}) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal json: %w", err)
	}
	fmt.Fprintln(cmd.OutOrStdout(), string(data))
	return nil
}

var analyticsStageDurationCmd = &cobra.Command{
	Use:   "stage-duration",
	Short: "Average and percentile durations per stage",
	RunE: func(cmd *cobra.Command, args []string) error {
		d, err := openAnalyticsDB()
		if err != nil {
			return err
		}
		defer d.Close()

		since, _ := cmd.Flags().GetString("since")
		results, err := analytics.QueryStageDurations(d, since)
		if err != nil {
			return err
		}

		format, _ := cmd.Flags().GetString("format")
		if format == "json" {
			return writeJSON(cmd, results)
		}

		w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "STAGE\tCOUNT\tAVG (min)\tP50 (min)\tP95 (min)")
		for _, r := range results {
			fmt.Fprintf(w, "%s\t%d\t%.1f\t%.1f\t%.1f\n", r.Stage, r.Count, r.Avg, r.P50, r.P95)
		}
		return w.Flush()
	},
}

var analyticsCheckFailureRateCmd = &cobra.Command{
	Use:   "check-failure-rate",
	Short: "Check failure rates by stage",
	RunE: func(cmd *cobra.Command, args []string) error {
		d, err := openAnalyticsDB()
		if err != nil {
			return err
		}
		defer d.Close()

		since, _ := cmd.Flags().GetString("since")
		results, err := analytics.QueryCheckFailureRates(d, since)
		if err != nil {
			return err
		}

		format, _ := cmd.Flags().GetString("format")
		if format == "json" {
			return writeJSON(cmd, results)
		}

		w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "STAGE\tTOTAL\tFIRST PASS\tAFTER FIX\tESCALATED")
		for _, r := range results {
			fmt.Fprintf(w, "%s\t%d\t%.1f%%\t%.1f%%\t%.1f%%\n", r.Stage, r.Total, r.FirstPass, r.AfterFix, r.Escalated)
		}
		return w.Flush()
	},
}

var analyticsCheckFailuresCmd = &cobra.Command{
	Use:   "check-failures",
	Short: "Which checks fail most and their auto-fix rates",
	RunE: func(cmd *cobra.Command, args []string) error {
		d, err := openAnalyticsDB()
		if err != nil {
			return err
		}
		defer d.Close()

		since, _ := cmd.Flags().GetString("since")
		results, err := analytics.QueryCheckFailures(d, since)
		if err != nil {
			return err
		}

		format, _ := cmd.Flags().GetString("format")
		if format == "json" {
			return writeJSON(cmd, results)
		}

		w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "CHECK\tTOTAL\tFAIL RATE\tAUTO-FIX\tCOMMON RULES")
		for _, r := range results {
			rules := r.CommonRules
			if len(rules) > 50 {
				rules = rules[:47] + "..."
			}
			fmt.Fprintf(w, "%s\t%d\t%.1f%%\t%.1f%%\t%s\n", r.Check, r.Total, r.FailRate, r.AutoFixRate, rules)
		}
		return w.Flush()
	},
}

var analyticsFixRoundsCmd = &cobra.Command{
	Use:   "fix-rounds",
	Short: "Distribution of fix rounds per stage",
	RunE: func(cmd *cobra.Command, args []string) error {
		d, err := openAnalyticsDB()
		if err != nil {
			return err
		}
		defer d.Close()

		since, _ := cmd.Flags().GetString("since")
		results, err := analytics.QueryFixRounds(d, since)
		if err != nil {
			return err
		}

		format, _ := cmd.Flags().GetString("format")
		if format == "json" {
			return writeJSON(cmd, results)
		}

		w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "STAGE\tTOTAL\t0 ROUNDS\t1 ROUND\t2 ROUNDS\t3+ ROUNDS")
		for _, r := range results {
			fmt.Fprintf(w, "%s\t%d\t%.1f%%\t%.1f%%\t%.1f%%\t%.1f%%\n", r.Stage, r.Total, r.Zero, r.One, r.Two, r.ThreePlus)
		}
		return w.Flush()
	},
}

var analyticsPipelineThroughputCmd = &cobra.Command{
	Use:   "pipeline-throughput",
	Short: "Weekly pipeline throughput metrics",
	RunE: func(cmd *cobra.Command, args []string) error {
		d, err := openAnalyticsDB()
		if err != nil {
			return err
		}
		defer d.Close()

		since, _ := cmd.Flags().GetString("since")
		results, err := analytics.QueryPipelineThroughput(d, since)
		if err != nil {
			return err
		}

		format, _ := cmd.Flags().GetString("format")
		if format == "json" {
			return writeJSON(cmd, results)
		}

		w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "PERIOD\tCREATED\tCOMPLETED\tFAILED\tESCALATED\tAVG DURATION (h)")
		for _, r := range results {
			fmt.Fprintf(w, "%s\t%d\t%d\t%d\t%d\t%.1f\n", r.Period, r.Created, r.Completed, r.Failed, r.Escalated, r.AvgDuration)
		}
		return w.Flush()
	},
}

var analyticsIssueDetailCmd = &cobra.Command{
	Use:   "issue-detail [issue-number]",
	Short: "Full event timeline for an issue",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		issue, err := strconv.Atoi(args[0])
		if err != nil {
			return fmt.Errorf("invalid issue number: %s", args[0])
		}

		d, err := openAnalyticsDB()
		if err != nil {
			return err
		}
		defer d.Close()

		results, err := analytics.QueryIssueDetail(d, issue)
		if err != nil {
			return err
		}

		format, _ := cmd.Flags().GetString("format")
		if format == "json" {
			return writeJSON(cmd, results)
		}

		w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
		fmt.Fprintf(cmd.OutOrStdout(), "Issue #%d timeline:\n", issue)
		fmt.Fprintln(w, "TIMESTAMP\tTYPE\tEVENT\tSTAGE\tDETAIL")
		for _, e := range results {
			detail := e.Detail
			if len(detail) > 60 {
				detail = detail[:57] + "..."
			}
			detail = strings.ReplaceAll(detail, "\t", " ")
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", e.Timestamp, e.Type, e.Event, e.Stage, detail)
		}
		return w.Flush()
	},
}

func init() {
	sinceCommands := []*cobra.Command{
		analyticsStageDurationCmd,
		analyticsCheckFailureRateCmd,
		analyticsCheckFailuresCmd,
		analyticsFixRoundsCmd,
		analyticsPipelineThroughputCmd,
	}
	for _, cmd := range sinceCommands {
		cmd.Flags().String("format", "text", "Output format: text or json")
		cmd.Flags().String("since", "", "Filter events since date (YYYY-MM-DD)")
		analyticsCmd.AddCommand(cmd)
	}

	// issue-detail only gets --format (--since doesn't apply)
	analyticsIssueDetailCmd.Flags().String("format", "text", "Output format: text or json")
	analyticsCmd.AddCommand(analyticsIssueDetailCmd)
}
