package context

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/lucasnoah/taintfactory/internal/config"
	"github.com/lucasnoah/taintfactory/internal/pipeline"
	"github.com/lucasnoah/taintfactory/internal/prompt"
)

// FidelityMode controls how much prior-stage context is included.
type FidelityMode string

const (
	ModeFull         FidelityMode = "full"
	ModeCodeOnly     FidelityMode = "code_only"
	ModeFindingsOnly FidelityMode = "findings_only"
	ModeMinimal      FidelityMode = "minimal"
)

// ValidModes lists all valid fidelity modes.
var ValidModes = []FidelityMode{ModeFull, ModeCodeOnly, ModeFindingsOnly, ModeMinimal}

// IsValidMode checks whether a string is a valid fidelity mode.
func IsValidMode(s string) bool {
	for _, m := range ValidModes {
		if string(m) == s {
			return true
		}
	}
	return false
}

// GitRunner provides git operations for context building.
type GitRunner interface {
	Diff(dir string) (string, error)
	DiffSummary(dir string) (string, error)
	FilesChanged(dir string) (string, error)
	Log(dir string) (string, error)
}

// Builder assembles context/prompt for a pipeline stage.
type Builder struct {
	store *pipeline.Store
	git   GitRunner
}

// NewBuilder creates a Builder.
func NewBuilder(store *pipeline.Store, git GitRunner) *Builder {
	return &Builder{store: store, git: git}
}

// BuildOpts configures what context to build.
type BuildOpts struct {
	Issue        int
	Stage        string
	StageCfg     *config.Stage
	IssueBody    string
	PipelineVars map[string]string // project-level custom vars from pipeline config
}

// BuildResult holds the assembled context.
type BuildResult struct {
	Vars     prompt.Vars
	Mode     FidelityMode
	Template string
}

// Build assembles template variables for a stage based on its fidelity mode.
func (b *Builder) Build(ps *pipeline.PipelineState, opts BuildOpts) (*BuildResult, error) {
	mode := resolveMode(opts.StageCfg)
	if !IsValidMode(string(mode)) {
		return nil, fmt.Errorf("invalid context_mode %q for stage %q", mode, opts.Stage)
	}

	vars := prompt.Vars{
		"issue_number":   strconv.Itoa(ps.Issue),
		"issue_title":    ps.Title,
		"issue_body":     opts.IssueBody,
		"feature_intent": ps.FeatureIntent,
		"worktree_path":  ps.Worktree,
		"repo_root":      filepath.Dir(filepath.Dir(ps.Worktree)),
		"branch":         ps.Branch,
		"stage_id":       opts.Stage,
		"attempt":        strconv.Itoa(ps.CurrentAttempt),
		"goal":           buildGoal(ps),
	}

	// Merge runtime vars injected by the orchestrator (e.g. dependent_issues after merge).
	// These override base vars but are themselves overridden by pipeline/stage vars.
	for k, v := range ps.RuntimeVars {
		vars[k] = v
	}

	// Merge custom vars: pipeline-level first, then stage-level overrides.
	for k, v := range opts.PipelineVars {
		vars[k] = v
	}
	if opts.StageCfg != nil {
		for k, v := range opts.StageCfg.Vars {
			vars[k] = v
		}
	}

	switch mode {
	case ModeFull:
		b.addFullContext(ps, opts, vars)
	case ModeCodeOnly:
		b.addCodeOnlyContext(ps, opts, vars)
	case ModeFindingsOnly:
		b.addFindingsOnlyContext(ps, opts, vars)
	case ModeMinimal:
		// minimal: just the base vars above
	}

	// Check failures are always included regardless of mode
	b.addCheckFailures(ps, opts, vars)

	// Resolve template path
	tmplPath := opts.StageCfg.PromptTemplate
	if tmplPath == "" {
		tmplPath = opts.Stage + ".md"
	}

	return &BuildResult{
		Vars:     vars,
		Mode:     mode,
		Template: tmplPath,
	}, nil
}

// addFullContext includes everything from prior stages.
func (b *Builder) addFullContext(ps *pipeline.PipelineState, opts BuildOpts, vars prompt.Vars) {
	// Git context — commits, summary, and file list (no full diff)
	if b.git != nil {
		if log, err := b.git.Log(ps.Worktree); err == nil && log != "" {
			vars["git_commits"] = log
		}
		if summary, err := b.git.DiffSummary(ps.Worktree); err == nil && summary != "" {
			vars["git_diff_summary"] = summary
		}
		if files, err := b.git.FilesChanged(ps.Worktree); err == nil && files != "" {
			vars["files_changed"] = files
		}
	}

	// Prior stage summaries
	priorSummary := b.collectPriorStageSummaries(ps)
	if priorSummary != "" {
		vars["prior_stage_summary"] = priorSummary
	}

	// Acceptance criteria from stage config goals
	if ac, ok := ps.GoalGates[opts.Stage]; ok && ac != "" {
		vars["acceptance_criteria"] = ac
	}
}

// addCodeOnlyContext includes commits and file list but strips reasoning.
func (b *Builder) addCodeOnlyContext(ps *pipeline.PipelineState, opts BuildOpts, vars prompt.Vars) {
	// Git context — commits, summary, and file list (no full diff)
	if b.git != nil {
		if log, err := b.git.Log(ps.Worktree); err == nil && log != "" {
			vars["git_commits"] = log
		}
		if summary, err := b.git.DiffSummary(ps.Worktree); err == nil && summary != "" {
			vars["git_diff_summary"] = summary
		}
		if files, err := b.git.FilesChanged(ps.Worktree); err == nil && files != "" {
			vars["files_changed"] = files
		}
	}

	// Acceptance criteria (not reasoning)
	if ac, ok := ps.GoalGates[opts.Stage]; ok && ac != "" {
		vars["acceptance_criteria"] = ac
	}

	// No prior_stage_summary — fresh eyes on the code
}

// addFindingsOnlyContext includes only structured findings from prior stage.
func (b *Builder) addFindingsOnlyContext(ps *pipeline.PipelineState, opts BuildOpts, vars prompt.Vars) {
	// Acceptance criteria
	if ac, ok := ps.GoalGates[opts.Stage]; ok && ac != "" {
		vars["acceptance_criteria"] = ac
	}

	// Only findings from the most recent completed stage
	if len(ps.StageHistory) > 0 {
		lastEntry := ps.StageHistory[len(ps.StageHistory)-1]
		outcome, err := b.store.GetStageOutcome(ps.Issue, lastEntry.Stage, lastEntry.Attempt)
		if err == nil && outcome != nil {
			if len(outcome.Findings) > 0 {
				var findingsText strings.Builder
				for _, f := range outcome.Findings {
					fmt.Fprintf(&findingsText, "- %s:%d [%s] %s", f.File, f.Line, f.Severity, f.Message)
					if f.Rule != "" {
						fmt.Fprintf(&findingsText, " (%s)", f.Rule)
					}
					findingsText.WriteString("\n")
				}
				vars["prior_stage_summary"] = findingsText.String()
			}
			if outcome.Summary != "" && len(outcome.Findings) == 0 {
				vars["prior_stage_summary"] = outcome.Summary
			}
		}
	}

	// No git diff — agent should read files itself
}

// addCheckFailures includes check failure data (always included regardless of mode).
// It searches backward through stage history for the most recent failed attempt
// of the current stage, since the current attempt hasn't completed yet.
func (b *Builder) addCheckFailures(ps *pipeline.PipelineState, opts BuildOpts, vars prompt.Vars) {
	// Search stage history backward for the most recent failure of this stage
	for i := len(ps.StageHistory) - 1; i >= 0; i-- {
		entry := ps.StageHistory[i]
		if entry.Stage != opts.Stage {
			continue
		}
		outcome, err := b.store.GetStageOutcome(ps.Issue, entry.Stage, entry.Attempt)
		if err != nil {
			continue
		}
		if outcome.Status == "fail" && outcome.Summary != "" {
			vars["check_failures"] = outcome.Summary
		}
		return // found the most recent entry for this stage
	}

	// Also check current attempt in case outcome was saved before context build
	outcome, err := b.store.GetStageOutcome(ps.Issue, opts.Stage, ps.CurrentAttempt)
	if err != nil {
		return
	}
	if outcome != nil && outcome.Status == "fail" && outcome.Summary != "" {
		vars["check_failures"] = outcome.Summary
	}
}

// collectPriorStageSummaries builds a summary of all prior stage outcomes.
func (b *Builder) collectPriorStageSummaries(ps *pipeline.PipelineState) string {
	if len(ps.StageHistory) == 0 {
		return ""
	}

	var sb strings.Builder
	for _, entry := range ps.StageHistory {
		outcome, err := b.store.GetStageOutcome(ps.Issue, entry.Stage, entry.Attempt)
		if err != nil {
			continue
		}
		fmt.Fprintf(&sb, "### %s (attempt %d): %s\n", entry.Stage, entry.Attempt, entry.Outcome)
		if outcome.Summary != "" {
			fmt.Fprintf(&sb, "%s\n", outcome.Summary)
		}
		if len(outcome.FilesChanged) > 0 {
			fmt.Fprintf(&sb, "Files: %s\n", strings.Join(outcome.FilesChanged, ", "))
		}
		sb.WriteString("\n")
	}
	return strings.TrimSpace(sb.String())
}

// CheckpointOpts configures what to save in a checkpoint.
type CheckpointOpts struct {
	Status  string // "success", "fail", "escalate"
	Summary string
}

// Checkpoint saves a stage outcome for consumption by subsequent stages.
func (b *Builder) Checkpoint(issue int, stage string, attempt int, opts CheckpointOpts) error {
	outcome := &pipeline.StageOutcome{
		Status:  opts.Status,
		Summary: opts.Summary,
	}

	// Capture git state if available
	if b.git != nil {
		ps, err := b.store.Get(issue)
		if err == nil {
			if summary, err := b.git.DiffSummary(ps.Worktree); err == nil {
				outcome.DiffSummary = summary
			}
			if files, err := b.git.FilesChanged(ps.Worktree); err == nil && files != "" {
				outcome.FilesChanged = strings.Split(strings.TrimSpace(files), "\n")
			}
		}
	}

	return b.store.SaveStageOutcome(issue, stage, attempt, outcome)
}

// ReadContext retrieves the saved rendered prompt for a stage attempt.
func (b *Builder) ReadContext(issue int, stage string, attempt int) (string, error) {
	return b.store.GetPrompt(issue, stage, attempt)
}

func resolveMode(stageCfg *config.Stage) FidelityMode {
	if stageCfg == nil || stageCfg.ContextMode == "" {
		return ModeFull
	}
	return FidelityMode(stageCfg.ContextMode)
}

func buildGoal(ps *pipeline.PipelineState) string {
	if ps.Title != "" {
		return fmt.Sprintf("#%d: %s", ps.Issue, ps.Title)
	}
	return fmt.Sprintf("#%d", ps.Issue)
}
