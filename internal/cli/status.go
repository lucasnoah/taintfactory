package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show status of all in-flight pipelines (orchestrator check-in)",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println("factory status — not implemented")
		return nil
	},
}

func init() {
	statusCmd.Flags().String("format", "text", "Output format: text or json")
}
