package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Validate and inspect pipeline configuration",
}

var configValidateCmd = &cobra.Command{
	Use:   "validate",
	Short: "Validate the pipeline configuration file",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println("factory config validate — not implemented")
		return nil
	},
}

var configShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Show the resolved configuration with defaults merged",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println("factory config show — not implemented")
		return nil
	},
}

func init() {
	configCmd.AddCommand(configValidateCmd)
	configCmd.AddCommand(configShowCmd)
}
