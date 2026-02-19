package cli

import (
	"fmt"
	"strconv"
	"strings"

	appctx "github.com/lucasnoah/taintfactory/internal/context"
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
		issue, err := strconv.Atoi(args[0])
		if err != nil {
			return fmt.Errorf("invalid issue number %q: %w", args[0], err)
		}
		stage := args[1]
		issueBody, _ := cmd.Flags().GetString("issue-body")

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

		stageCfg, err := findStageConfig(cfg, stage)
		if err != nil {
			return err
		}

		builder := appctx.NewBuilder(store, &appctx.ExecGit{})
		result, err := builder.Build(ps, appctx.BuildOpts{
			Issue:     issue,
			Stage:     stage,
			StageCfg:  stageCfg,
			IssueBody: issueBody,
		})
		if err != nil {
			return fmt.Errorf("build context: %w", err)
		}

		// Load and render the template
		tmplContent, err := prompt.LoadTemplate(result.Template, ps.Worktree)
		if err != nil {
			return fmt.Errorf("load template %q: %w", result.Template, err)
		}

		rendered, err := prompt.Render(tmplContent, result.Vars)
		if err != nil {
			return fmt.Errorf("render template: %w", err)
		}

		// Save rendered prompt to disk
		if err := store.SavePrompt(issue, stage, ps.CurrentAttempt, rendered); err != nil {
			return fmt.Errorf("save prompt: %w", err)
		}

		fmt.Fprintf(cmd.ErrOrStderr(), "Context built (mode=%s) and saved to disk.\n", result.Mode)
		fmt.Fprint(cmd.OutOrStdout(), rendered)
		return nil
	},
}

var contextCheckpointCmd = &cobra.Command{
	Use:   "checkpoint [issue] [stage] [outcome]",
	Short: "Save stage outcome for context building",
	Args:  cobra.ExactArgs(3),
	RunE: func(cmd *cobra.Command, args []string) error {
		issue, err := strconv.Atoi(args[0])
		if err != nil {
			return fmt.Errorf("invalid issue number %q: %w", args[0], err)
		}
		stage := args[1]
		outcome := args[2]
		summary, _ := cmd.Flags().GetString("summary")

		store, err := pipeline.DefaultStore()
		if err != nil {
			return fmt.Errorf("open pipeline store: %w", err)
		}

		ps, err := store.Get(issue)
		if err != nil {
			return fmt.Errorf("get pipeline state: %w", err)
		}

		builder := appctx.NewBuilder(store, &appctx.ExecGit{})
		if err := builder.Checkpoint(issue, stage, ps.CurrentAttempt, appctx.CheckpointOpts{
			Status:  outcome,
			Summary: summary,
		}); err != nil {
			return fmt.Errorf("save checkpoint: %w", err)
		}

		fmt.Fprintf(cmd.OutOrStdout(), "Checkpoint saved: issue=%d stage=%s outcome=%s\n", issue, stage, outcome)
		return nil
	},
}

var contextReadCmd = &cobra.Command{
	Use:   "read [issue] [stage]",
	Short: "Read saved context for a stage",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		issue, err := strconv.Atoi(args[0])
		if err != nil {
			return fmt.Errorf("invalid issue number %q: %w", args[0], err)
		}
		stage := args[1]

		store, err := pipeline.DefaultStore()
		if err != nil {
			return fmt.Errorf("open pipeline store: %w", err)
		}

		ps, err := store.Get(issue)
		if err != nil {
			return fmt.Errorf("get pipeline state: %w", err)
		}

		builder := appctx.NewBuilder(store, nil)
		content, err := builder.ReadContext(issue, stage, ps.CurrentAttempt)
		if err != nil {
			return fmt.Errorf("read context: %w", err)
		}

		fmt.Fprint(cmd.OutOrStdout(), content)
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

		stageCfg, err := findStageConfig(cfg, stage)
		if err != nil {
			return err
		}

		templatePath := stageCfg.PromptTemplate
		if templatePath == "" {
			templatePath = stage + ".md"
		}

		tmplContent, err := prompt.LoadTemplate(templatePath, ps.Worktree)
		if err != nil {
			return fmt.Errorf("load template %q: %w", templatePath, err)
		}

		vars := prompt.Vars{
			"issue_number":  strconv.Itoa(ps.Issue),
			"issue_title":   ps.Title,
			"issue_body":    "",
			"worktree_path": ps.Worktree,
			"branch":        ps.Branch,
			"stage_id":      stage,
			"attempt":       strconv.Itoa(ps.CurrentAttempt),
			"goal":          buildGoal(ps),
		}

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
	contextBuildCmd.Flags().String("issue-body", "", "Issue body text (if not available from pipeline state)")

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
