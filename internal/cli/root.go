package cli

import (
	"github.com/spf13/cobra"
)

var version = "dev"

func SetVersion(v string) {
	version = v
}

var rootCmd = &cobra.Command{
	Use:   "factory",
	Short: "taintfactory — a CLI-driven software factory",
	Long: `taintfactory orchestrates Claude Code sessions through configurable pipelines
with automated checks, persistent sessions, and browser-based QA.

All state is stored in ~/.factory/ (SQLite for events, JSON for artifacts).
The orchestrator agent calls this CLI via cron to advance pipelines.`,
}

func Execute() error {
	return rootCmd.Execute()
}

func init() {
	rootCmd.AddCommand(versionCmd)
	rootCmd.AddCommand(pipelineCmd)
	rootCmd.AddCommand(sessionCmd)
	rootCmd.AddCommand(checkCmd)
	rootCmd.AddCommand(contextCmd)
	rootCmd.AddCommand(eventCmd)
	rootCmd.AddCommand(statusCmd)
	rootCmd.AddCommand(analyticsCmd)
	rootCmd.AddCommand(worktreeCmd)
	rootCmd.AddCommand(prCmd)
	rootCmd.AddCommand(configCmd)
	rootCmd.AddCommand(dbCmd)
	rootCmd.AddCommand(qaCmd)
	rootCmd.AddCommand(stageCmd)
	rootCmd.AddCommand(orchestratorCmd)
}
