package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

var qaCmd = &cobra.Command{
	Use:   "qa",
	Short: "QA and browser testing utilities",
}

var qaDetectBrowserCmd = &cobra.Command{
	Use:   "detect-browser [issue-number]",
	Short: "Detect if an issue requires browser testing",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Printf("factory qa detect-browser %s — not implemented\n", args[0])
		return nil
	},
}

func init() {
	qaCmd.AddCommand(qaDetectBrowserCmd)
}
