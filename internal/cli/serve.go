package cli

import (
	"fmt"

	"github.com/lucasnoah/taintfactory/internal/db"
	"github.com/lucasnoah/taintfactory/internal/pipeline"
	"github.com/lucasnoah/taintfactory/internal/web"
	"github.com/spf13/cobra"
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the local web UI",
	Long: `Start a read-only browser UI on localhost showing pipeline state, history, and check results.

Pipeline configs (pipeline.yaml) are discovered automatically from each pipeline's
worktree path, so the server works correctly regardless of which directory it is
started from.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		port, _ := cmd.Flags().GetInt("port")

		dbPath, err := db.DefaultDBPath()
		if err != nil {
			return fmt.Errorf("db path: %w", err)
		}
		database, err := db.Open(dbPath)
		if err != nil {
			return fmt.Errorf("open db: %w", err)
		}
		defer database.Close()

		if err := database.Migrate(); err != nil {
			return fmt.Errorf("migrate: %w", err)
		}

		store, err := pipeline.DefaultStore()
		if err != nil {
			return fmt.Errorf("store: %w", err)
		}

		return web.NewServer(store, database, port).Start()
	},
}

func init() {
	serveCmd.Flags().Int("port", 8080, "Port to listen on")
}
