package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

var dbCmd = &cobra.Command{
	Use:   "db",
	Short: "Database management",
}

var dbMigrateCmd = &cobra.Command{
	Use:   "migrate",
	Short: "Apply database schema migrations",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println("factory db migrate — not implemented")
		return nil
	},
}

var dbResetCmd = &cobra.Command{
	Use:   "reset",
	Short: "Reset the database (destructive!)",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println("factory db reset — not implemented")
		return nil
	},
}

func init() {
	dbCmd.AddCommand(dbMigrateCmd)
	dbCmd.AddCommand(dbResetCmd)
}
