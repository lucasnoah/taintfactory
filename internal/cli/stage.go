package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

var stageCmd = &cobra.Command{
	Use:   "stage",
	Short: "Run individual pipeline stages (internal)",
}

var stageRunCmd = &cobra.Command{
	Use:   "run [issue] [stage]",
	Short: "Run a single stage lifecycle (agent → checks → fix loop)",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Printf("factory stage run %s %s — not implemented\n", args[0], args[1])
		return nil
	},
}

func init() {
	stageCmd.AddCommand(stageRunCmd)
}
