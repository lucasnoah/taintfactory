package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

var prCmd = &cobra.Command{
	Use:   "pr",
	Short: "Manage pull requests",
}

var prCreateCmd = &cobra.Command{
	Use:   "create [issue-number]",
	Short: "Create a pull request for an issue",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Printf("factory pr create %s — not implemented\n", args[0])
		return nil
	},
}

var prMergeCmd = &cobra.Command{
	Use:   "merge [issue-number]",
	Short: "Merge a pull request for an issue",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Printf("factory pr merge %s — not implemented\n", args[0])
		return nil
	},
}

func init() {
	prCmd.AddCommand(prCreateCmd)
	prCmd.AddCommand(prMergeCmd)
}
