package cli

import (
	"encoding/json"
	"fmt"
	"strconv"
	"text/tabwriter"

	"github.com/lucasnoah/taintfactory/internal/db"
	"github.com/lucasnoah/taintfactory/internal/github"
	"github.com/spf13/cobra"
)

var queueCmd = &cobra.Command{
	Use:   "queue",
	Short: "Manage the issue processing queue",
}

var queueAddCmd = &cobra.Command{
	Use:   "add <issue>...",
	Short: "Add issues to the queue",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		intent, _ := cmd.Flags().GetString("intent")

		ghClient := github.NewClient(&github.ExecRunner{})

		items := make([]db.QueueAddItem, 0, len(args))
		for _, arg := range args {
			n, err := strconv.Atoi(arg)
			if err != nil || n <= 0 {
				return fmt.Errorf("invalid issue number %q: must be a positive integer", arg)
			}

			itemIntent := intent
			if itemIntent == "" {
				// Fetch issue and derive intent via LLM
				issue, err := ghClient.GetIssue(n)
				if err != nil {
					return fmt.Errorf("issue #%d: failed to fetch from GitHub: %w", n, err)
				}
				fmt.Fprintf(cmd.OutOrStdout(), "issue #%d: deriving feature intent...\n", n)
				derived, err := github.DeriveFeatureIntent(issue, github.DefaultClaudeFn)
				if err != nil {
					return fmt.Errorf("issue #%d: intent derivation failed: %w", n, err)
				}
				if derived == "" {
					return fmt.Errorf("issue #%d: could not derive feature intent — pass --intent or ensure the issue describes clear user-facing value", n)
				}
				fmt.Fprintf(cmd.OutOrStdout(), "issue #%d: %s\n", n, derived)
				itemIntent = derived
			}

			items = append(items, db.QueueAddItem{Issue: n, FeatureIntent: itemIntent})
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

		if err := d.QueueAdd(items); err != nil {
			return err
		}

		fmt.Fprintf(cmd.OutOrStdout(), "Added %d issue(s) to the queue\n", len(items))
		return nil
	},
}

var queueSetIntentCmd = &cobra.Command{
	Use:   "set-intent <issue> <intent>",
	Short: "Set or update the feature intent for a queued issue",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		issue, err := strconv.Atoi(args[0])
		if err != nil || issue <= 0 {
			return fmt.Errorf("invalid issue number %q: must be a positive integer", args[0])
		}
		intent := args[1]

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

		if err := d.QueueSetIntent(issue, intent); err != nil {
			return err
		}

		fmt.Fprintf(cmd.OutOrStdout(), "Set feature intent for issue #%d\n", issue)
		return nil
	},
}

var queueListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all items in the queue",
	RunE: func(cmd *cobra.Command, args []string) error {
		format, _ := cmd.Flags().GetString("format")

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

		items, err := d.QueueList()
		if err != nil {
			return err
		}

		if format == "json" {
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			return enc.Encode(items)
		}

		if len(items) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "Queue is empty")
			return nil
		}

		w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "POS\tISSUE\tSTATUS\tINTENT\tADDED")
		for _, item := range items {
			intent := item.FeatureIntent
			if len(intent) > 50 {
				intent = intent[:47] + "..."
			}
			if intent == "" {
				intent = "(none)"
			}
			fmt.Fprintf(w, "%d\t#%d\t%s\t%s\t%s\n", item.Position, item.Issue, item.Status, intent, item.AddedAt)
		}
		return w.Flush()
	},
}

var queueRemoveCmd = &cobra.Command{
	Use:   "remove <issue>",
	Short: "Remove an issue from the queue",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		issue, err := strconv.Atoi(args[0])
		if err != nil || issue <= 0 {
			return fmt.Errorf("invalid issue number %q: must be a positive integer", args[0])
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

		if err := d.QueueRemove(issue); err != nil {
			return err
		}

		fmt.Fprintf(cmd.OutOrStdout(), "Removed issue #%d from the queue\n", issue)
		return nil
	},
}

var queueClearCmd = &cobra.Command{
	Use:   "clear",
	Short: "Remove all items from the queue",
	RunE: func(cmd *cobra.Command, args []string) error {
		confirm, _ := cmd.Flags().GetBool("confirm")
		if !confirm {
			return fmt.Errorf("use --confirm to clear the entire queue")
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

		count, err := d.QueueClear()
		if err != nil {
			return err
		}

		fmt.Fprintf(cmd.OutOrStdout(), "Cleared %d item(s) from the queue\n", count)
		return nil
	},
}

func init() {
	queueAddCmd.Flags().String("intent", "", "Feature intent: what value this brings to the end user")
	queueListCmd.Flags().String("format", "table", "Output format: table or json")
	queueClearCmd.Flags().Bool("confirm", false, "Confirm clearing the entire queue")

	queueCmd.AddCommand(queueAddCmd)
	queueCmd.AddCommand(queueListCmd)
	queueCmd.AddCommand(queueRemoveCmd)
	queueCmd.AddCommand(queueClearCmd)
	queueCmd.AddCommand(queueSetIntentCmd)
}
