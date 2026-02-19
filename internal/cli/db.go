package cli

import (
	"fmt"

	"github.com/lucasnoah/taintfactory/internal/db"
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
		fmt.Println("Database migrated successfully.")
		return nil
	},
}

var dbResetCmd = &cobra.Command{
	Use:   "reset",
	Short: "Reset the database (destructive!)",
	RunE: func(cmd *cobra.Command, args []string) error {
		confirm, _ := cmd.Flags().GetBool("confirm")
		if !confirm {
			fmt.Println("This will destroy all data. Pass --confirm to proceed.")
			return nil
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
		if err := d.Reset(); err != nil {
			return err
		}
		fmt.Println("Database reset successfully.")
		return nil
	},
}

func init() {
	dbResetCmd.Flags().Bool("confirm", false, "Confirm database reset")
	dbCmd.AddCommand(dbMigrateCmd)
	dbCmd.AddCommand(dbResetCmd)
}
