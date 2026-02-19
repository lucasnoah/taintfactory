package cli

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/lucasnoah/taintfactory/internal/config"
	"github.com/lucasnoah/taintfactory/internal/pipeline"
	"github.com/lucasnoah/taintfactory/internal/prompt"
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
		issue, err := strconv.Atoi(args[0])
		if err != nil {
			return fmt.Errorf("invalid issue number %q: %w", args[0], err)
		}
		stage := args[1]
		save, _ := cmd.Flags().GetBool("save")

		store, err := pipeline.DefaultStore()
		if err != nil {
			return fmt.Errorf("open pipeline store: %w", err)
		}

		ps, err := store.Get(issue)
		if err != nil {
			return fmt.Errorf("get pipeline state: %w", err)
		}

		cfg, err := config.LoadDefault()
		if err != nil {
			return fmt.Errorf("load config: %w", err)
		}

		// Find the stage config
		stageCfg, err := findStageConfig(cfg, stage)
		if err != nil {
			return err
		}

		// Resolve template path from stage config, with sensible defaults
		templatePath := stageCfg.PromptTemplate
		if templatePath == "" {
			templatePath = stage + ".md"
		}

		// Load the template: project override → built-in
		tmplContent, err := prompt.LoadTemplate(templatePath, ps.Worktree)
		if err != nil {
			return fmt.Errorf("load template %q: %w", templatePath, err)
		}

		// Build template variables from pipeline state
		vars := prompt.Vars{
			"issue_number": strconv.Itoa(ps.Issue),
			"issue_title":  ps.Title,
			"issue_body":   "", // filled from GitHub if available in context build
			"worktree_path": ps.Worktree,
			"branch":        ps.Branch,
			"stage_id":      stage,
			"attempt":       strconv.Itoa(ps.CurrentAttempt),
			"goal":          buildGoal(ps),
		}

		// Add any extra vars from flags
		extraVars, _ := cmd.Flags().GetStringSlice("var")
		for _, kv := range extraVars {
			parts := strings.SplitN(kv, "=", 2)
			if len(parts) == 2 {
				vars[parts[0]] = parts[1]
			}
		}

		rendered, err := prompt.Render(tmplContent, vars)
		if err != nil {
			return fmt.Errorf("render template: %w", err)
		}

		// Optionally save the prompt to the pipeline store
		if save {
			if err := store.SavePrompt(issue, stage, ps.CurrentAttempt, rendered); err != nil {
				return fmt.Errorf("save prompt: %w", err)
			}
			fmt.Fprintln(cmd.ErrOrStderr(), "Prompt saved.")
		}

		fmt.Fprint(cmd.OutOrStdout(), rendered)
		return nil
	},
}

func init() {
	contextCheckpointCmd.Flags().String("summary", "", "Human-readable summary of the stage outcome")

	contextRenderCmd.Flags().Bool("save", false, "Save the rendered prompt to the pipeline store")
	contextRenderCmd.Flags().StringSlice("var", nil, "Extra template variables as key=value pairs")

	contextCmd.AddCommand(contextBuildCmd)
	contextCmd.AddCommand(contextCheckpointCmd)
	contextCmd.AddCommand(contextReadCmd)
	contextCmd.AddCommand(contextRenderCmd)
}

// findStageConfig looks up a stage by ID in the pipeline config.
func findStageConfig(cfg *config.PipelineConfig, stageID string) (*config.Stage, error) {
	for i := range cfg.Pipeline.Stages {
		if cfg.Pipeline.Stages[i].ID == stageID {
			return &cfg.Pipeline.Stages[i], nil
		}
	}
	return nil, fmt.Errorf("stage %q not found in pipeline config", stageID)
}

// buildGoal constructs the goal string from pipeline state.
func buildGoal(ps *pipeline.PipelineState) string {
	if ps.Title != "" {
		return fmt.Sprintf("#%d: %s", ps.Issue, ps.Title)
	}
	return fmt.Sprintf("#%d", ps.Issue)
}
