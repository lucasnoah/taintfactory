package cli

import (
	"fmt"
	"log"
	"time"

	"github.com/lucasnoah/taintfactory/internal/db"
	"github.com/lucasnoah/taintfactory/internal/orchestrator"
	"github.com/lucasnoah/taintfactory/internal/pipeline"
	"github.com/lucasnoah/taintfactory/internal/triage"
	"github.com/lucasnoah/taintfactory/internal/web"
	"github.com/spf13/cobra"
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the local web UI",
	Long: `Start a read-only browser UI showing pipeline state, history, and check results.

With --with-orchestrator, also runs the orchestrator check-in loop on a configurable
interval, combining the web UI and the automation loop in a single process.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		port, _ := cmd.Flags().GetInt("port")
		withOrch, _ := cmd.Flags().GetBool("with-orchestrator")
		orchInterval, _ := cmd.Flags().GetInt("orchestrator-interval")

		connStr, err := db.DefaultConnStr()
		if err != nil {
			return fmt.Errorf("db connection: %w", err)
		}
		database, err := db.Open(connStr)
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

		if withOrch {
			orch, cleanup, err := newOrchestrator()
			if err != nil {
				return fmt.Errorf("init orchestrator: %w", err)
			}
			defer cleanup()

			go runOrchestratorLoop(orch, time.Duration(orchInterval)*time.Second)
			go runDeployLoop(orch, 30*time.Second)
		}

		triageDir, _ := triage.DefaultTriageDir()
		deployStore, _ := pipeline.DefaultDeployStore()
		return web.NewServer(store, database, port, triageDir, deployStore).Start()
	},
}

func runOrchestratorLoop(orch *orchestrator.Orchestrator, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	log.Printf("Orchestrator loop started (interval: %s)", interval)
	for range ticker.C {
		if _, err := orch.CheckIn(); err != nil {
			log.Printf("orchestrator check-in error: %v", err)
		}
		if err := discordPollTick(); err != nil {
			log.Printf("discord poll: %v", err)
		}
	}
}

func runDeployLoop(orch *orchestrator.Orchestrator, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	log.Printf("Deploy loop started (interval: %s)", interval)
	for range ticker.C {
		if action := orch.DeployCheckIn(); action != nil {
			log.Printf("deploy check-in: %s (stage: %s)", action.Action, action.Stage)
		}
	}
}

func init() {
	serveCmd.Flags().Int("port", 17432, "Port to listen on")
	serveCmd.Flags().Bool("with-orchestrator", false, "Run orchestrator check-in loop alongside web server")
	serveCmd.Flags().Int("orchestrator-interval", 120, "Orchestrator check-in interval in seconds")
}
