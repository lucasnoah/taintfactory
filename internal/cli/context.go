package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

var contextCmd = &cobra.Command{
	Use:   "context",
	Short: "Build and manage stage context and prompts",
}

var contextBuildCmd = &cobra.Command{
	Use:   "build [issue] [stage]",
	Short: "Build context/prompt for a pipeline stage",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Printf("factory context build %s %s — not implemented\n", args[0], args[1])
		return nil
	},
}

var contextCheckpointCmd = &cobra.Command{
	Use:   "checkpoint [issue] [stage] [outcome]",
	Short: "Save stage outcome for context building",
	Args:  cobra.ExactArgs(3),
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Printf("factory context checkpoint %s %s %s — not implemented\n", args[0], args[1], args[2])
		return nil
	},
}

var contextReadCmd = &cobra.Command{
	Use:   "read [issue] [stage]",
	Short: "Read saved context for a stage",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Printf("factory context read %s %s — not implemented\n", args[0], args[1])
		return nil
	},
}

var contextRenderCmd = &cobra.Command{
	Use:   "render [issue] [stage]",
	Short: "Preview the rendered prompt for a stage",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Printf("factory context render %s %s — not implemented\n", args[0], args[1])
		return nil
	},
}

func init() {
	contextCheckpointCmd.Flags().String("summary", "", "Human-readable summary of the stage outcome")

	contextCmd.AddCommand(contextBuildCmd)
	contextCmd.AddCommand(contextCheckpointCmd)
	contextCmd.AddCommand(contextReadCmd)
	contextCmd.AddCommand(contextRenderCmd)
}
