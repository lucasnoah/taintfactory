package stage

import (
	"fmt"
	"time"

	"github.com/lucasnoah/taintfactory/internal/checks"
	"github.com/lucasnoah/taintfactory/internal/config"
	appctx "github.com/lucasnoah/taintfactory/internal/context"
	"github.com/lucasnoah/taintfactory/internal/db"
	"github.com/lucasnoah/taintfactory/internal/pipeline"
	"github.com/lucasnoah/taintfactory/internal/prompt"
	"github.com/lucasnoah/taintfactory/internal/session"
)

// Engine executes the stage lifecycle: agent → checks → fix loop.
type Engine struct {
	sessions     *session.Manager
	checker      *checks.Runner
	builder      *appctx.Builder
	store        *pipeline.Store
	db           *db.DB
	cfg          *config.PipelineConfig
	pollInterval time.Duration // for WaitIdle; defaults to 30s
}

// NewEngine creates a stage engine.
func NewEngine(
	sessions *session.Manager,
	checker *checks.Runner,
	builder *appctx.Builder,
	store *pipeline.Store,
	database *db.DB,
	cfg *config.PipelineConfig,
) *Engine {
	return &Engine{
		sessions:     sessions,
		checker:      checker,
		builder:      builder,
		store:        store,
		db:           database,
		cfg:          cfg,
		pollInterval: 30 * time.Second,
	}
}

// SetPollInterval overrides the WaitIdle poll interval (for testing).
func (e *Engine) SetPollInterval(d time.Duration) {
	e.pollInterval = d
}

// RunOpts configures a stage run.
type RunOpts struct {
	Issue   int
	Stage   string
	Timeout time.Duration // overall timeout for the stage
}

// RunResult captures the outcome of a stage run.
type RunResult struct {
	Issue           int               `json:"issue"`
	Stage           string            `json:"stage"`
	Attempt         int               `json:"attempt"`
	Outcome         string            `json:"outcome"` // "success", "fail", "escalate"
	AgentDuration   time.Duration     `json:"agent_duration"`
	TotalDuration   time.Duration     `json:"total_duration"`
	FixRounds       int               `json:"fix_rounds"`
	ChecksFirstPass bool              `json:"checks_first_pass"`
	AutoFixes       map[string]int    `json:"auto_fixes"`
	AgentFixes      map[string]int    `json:"agent_fixes"`
	FinalCheckState map[string]string `json:"final_check_state"`
}

// Run executes the full stage lifecycle.
func (e *Engine) Run(opts RunOpts) (*RunResult, error) {
	start := time.Now()

	ps, err := e.store.Get(opts.Issue)
	if err != nil {
		return nil, fmt.Errorf("get pipeline state: %w", err)
	}

	stageCfg, err := e.findStageConfig(opts.Stage)
	if err != nil {
		return nil, err
	}

	result := &RunResult{
		Issue:           opts.Issue,
		Stage:           opts.Stage,
		Attempt:         ps.CurrentAttempt,
		AutoFixes:       make(map[string]int),
		AgentFixes:      make(map[string]int),
		FinalCheckState: make(map[string]string),
	}

	// For checks_only stages, skip agent and go straight to checks
	if stageCfg.Type == "checks_only" {
		return e.runChecksOnly(ps, stageCfg, opts, result, start)
	}

	// Run checks_before if configured
	if len(stageCfg.ChecksBefore) > 0 {
		gate, _, err := e.runGate(ps, opts, stageCfg.ChecksBefore, 0)
		if err != nil {
			return nil, fmt.Errorf("checks_before: %w", err)
		}
		if !gate.Passed {
			result.Outcome = "fail"
			result.TotalDuration = time.Since(start)
			_ = e.db.LogPipelineEvent(opts.Issue, "checks_before_failed", opts.Stage, ps.CurrentAttempt, "")
			return result, nil
		}
	}

	// Build context and render prompt
	agentStart := time.Now()
	rendered, err := e.buildAndRenderPrompt(ps, opts, stageCfg)
	if err != nil {
		return nil, fmt.Errorf("build prompt: %w", err)
	}

	// Save rendered prompt
	_ = e.store.SavePrompt(opts.Issue, opts.Stage, ps.CurrentAttempt, rendered)

	// Create session and send prompt
	sessionName := fmt.Sprintf("%d-%s-%d", opts.Issue, opts.Stage, ps.CurrentAttempt)
	if err := e.createAndRunSession(sessionName, ps, opts, stageCfg, rendered); err != nil {
		return nil, fmt.Errorf("run session: %w", err)
	}
	result.AgentDuration = time.Since(agentStart)

	// Run post-checks
	checkNames := e.resolvePostChecks(stageCfg)
	if len(checkNames) == 0 {
		result.Outcome = "success"
		result.ChecksFirstPass = true
		result.TotalDuration = time.Since(start)
		e.cleanupSession(sessionName)
		return result, nil
	}

	gate, _, err := e.runGate(ps, opts, checkNames, 0)
	if err != nil {
		e.cleanupSession(sessionName)
		return nil, fmt.Errorf("post-checks: %w", err)
	}

	// Record auto-fixes
	for _, c := range gate.Checks {
		if c.AutoFixed {
			result.AutoFixes[c.Check] = c.Runs - 1
		}
		if c.Passed {
			result.FinalCheckState[c.Check] = "pass"
		} else {
			result.FinalCheckState[c.Check] = "fail"
		}
	}

	if gate.Passed {
		result.Outcome = "success"
		result.ChecksFirstPass = true
		result.TotalDuration = time.Since(start)
		e.cleanupSession(sessionName)
		return result, nil
	}

	// Enter fix loop
	maxFixRounds := e.cfg.Pipeline.MaxFixRounds
	if maxFixRounds <= 0 {
		maxFixRounds = 3
	}
	freshAfter := e.cfg.Pipeline.FreshSessionAfter
	if freshAfter <= 0 {
		freshAfter = 2
	}

	for round := 1; round <= maxFixRounds; round++ {
		result.FixRounds = round
		_ = e.db.LogPipelineEvent(opts.Issue, "fix_round_start", opts.Stage, ps.CurrentAttempt, fmt.Sprintf("round=%d", round))

		// Determine if we need a fresh session
		if round > freshAfter {
			e.cleanupSession(sessionName)
			sessionName = fmt.Sprintf("%d-%s-%d-fix-%d", opts.Issue, opts.Stage, ps.CurrentAttempt, round)
			fixRendered, err := e.buildFixPrompt(ps, opts, stageCfg, gate)
			if err != nil {
				return nil, fmt.Errorf("build fix prompt: %w", err)
			}
			if err := e.createAndRunSession(sessionName, ps, opts, stageCfg, fixRendered); err != nil {
				return nil, fmt.Errorf("fix session: %w", err)
			}
		} else {
			// Send fix prompt to existing session
			if err := e.sessions.SendFromCheckFailures(sessionName, opts.Issue, opts.Stage); err != nil {
				return nil, fmt.Errorf("send fix prompt: %w", err)
			}
			// Wait for idle
			waitResult, err := e.sessions.WaitIdle(sessionName, opts.Timeout, e.pollInterval)
			if err != nil {
				return nil, fmt.Errorf("wait idle (fix): %w", err)
			}
			if waitResult.State == "exited" {
				e.cleanupSession(sessionName)
				return nil, fmt.Errorf("session exited during fix round %d", round)
			}
		}

		// Re-run checks
		gate, _, err = e.runGate(ps, opts, checkNames, round)
		if err != nil {
			e.cleanupSession(sessionName)
			return nil, fmt.Errorf("re-check (round %d): %w", round, err)
		}

		// Update check state
		for _, c := range gate.Checks {
			if c.Passed {
				result.FinalCheckState[c.Check] = "pass"
				if !c.AutoFixed {
					result.AgentFixes[c.Check]++
				}
			} else {
				result.FinalCheckState[c.Check] = "fail"
			}
		}

		if gate.Passed {
			result.Outcome = "success"
			result.TotalDuration = time.Since(start)
			e.cleanupSession(sessionName)
			return result, nil
		}
	}

	// Fix loop exhausted
	result.Outcome = "fail"
	result.TotalDuration = time.Since(start)
	e.cleanupSession(sessionName)
	_ = e.db.LogPipelineEvent(opts.Issue, "fix_loop_exhausted", opts.Stage, ps.CurrentAttempt, fmt.Sprintf("rounds=%d", result.FixRounds))
	return result, nil
}

// runChecksOnly handles checks_only stage type.
func (e *Engine) runChecksOnly(ps *pipeline.PipelineState, stageCfg *config.Stage, opts RunOpts, result *RunResult, start time.Time) (*RunResult, error) {
	checkNames := stageCfg.Checks
	if len(checkNames) == 0 {
		result.Outcome = "success"
		result.ChecksFirstPass = true
		result.TotalDuration = time.Since(start)
		return result, nil
	}

	gate, _, err := e.runGate(ps, opts, checkNames, 0)
	if err != nil {
		return nil, fmt.Errorf("checks_only gate: %w", err)
	}

	for _, c := range gate.Checks {
		if c.Passed {
			result.FinalCheckState[c.Check] = "pass"
		} else {
			result.FinalCheckState[c.Check] = "fail"
		}
	}

	if gate.Passed {
		result.Outcome = "success"
		result.ChecksFirstPass = true
		result.TotalDuration = time.Since(start)
		return result, nil
	}

	result.Outcome = "fail"
	result.TotalDuration = time.Since(start)
	return result, nil
}

// buildAndRenderPrompt builds context and renders the stage prompt.
func (e *Engine) buildAndRenderPrompt(ps *pipeline.PipelineState, opts RunOpts, stageCfg *config.Stage) (string, error) {
	buildResult, err := e.builder.Build(ps, appctx.BuildOpts{
		Issue:    opts.Issue,
		Stage:    opts.Stage,
		StageCfg: stageCfg,
	})
	if err != nil {
		return "", err
	}

	tmplContent, err := prompt.LoadTemplate(buildResult.Template, ps.Worktree)
	if err != nil {
		return "", fmt.Errorf("load template %q: %w", buildResult.Template, err)
	}

	return prompt.Render(tmplContent, buildResult.Vars)
}

// buildFixPrompt builds a prompt for a fresh fix session.
func (e *Engine) buildFixPrompt(ps *pipeline.PipelineState, opts RunOpts, stageCfg *config.Stage, gate *checks.GateResult) (string, error) {
	// Build fix-checks template with failure details
	vars := prompt.Vars{
		"issue_title":    ps.Title,
		"issue_number":   fmt.Sprintf("%d", ps.Issue),
		"worktree_path":  ps.Worktree,
		"branch":         ps.Branch,
		"stage_id":       opts.Stage,
		"attempt":        fmt.Sprintf("%d", ps.CurrentAttempt),
		"check_failures": formatGateFailures(gate),
	}

	tmplContent, err := prompt.LoadTemplate("fix-checks.md", ps.Worktree)
	if err != nil {
		return "", err
	}

	return prompt.Render(tmplContent, vars)
}

// createAndRunSession creates a tmux session, sends the prompt, and waits for idle.
func (e *Engine) createAndRunSession(name string, ps *pipeline.PipelineState, opts RunOpts, stageCfg *config.Stage, rendered string) error {
	flags := stageCfg.Flags
	if flags == "" {
		flags = e.cfg.Pipeline.Defaults.Flags
	}

	if err := e.sessions.Create(session.CreateOpts{
		Name:    name,
		Workdir: ps.Worktree,
		Flags:   flags,
		Issue:   opts.Issue,
		Stage:   opts.Stage,
	}); err != nil {
		return fmt.Errorf("create session: %w", err)
	}

	// Send the rendered prompt
	if err := e.sessions.Send(name, rendered); err != nil {
		return fmt.Errorf("send prompt: %w", err)
	}

	// Wait for session to become idle
	waitResult, err := e.sessions.WaitIdle(name, opts.Timeout, e.pollInterval)
	if err != nil {
		return fmt.Errorf("wait idle: %w", err)
	}

	if waitResult.State == "exited" {
		return fmt.Errorf("session exited unexpectedly")
	}

	return nil
}

// runGate runs the check gate for the given check names.
func (e *Engine) runGate(ps *pipeline.PipelineState, opts RunOpts, checkNames []string, fixRound int) (*checks.GateResult, []*checks.Result, error) {
	var gateChecks []checks.GateCheckConfig
	for _, name := range checkNames {
		chk, ok := e.cfg.Pipeline.Checks[name]
		if !ok {
			return nil, nil, fmt.Errorf("check %q not defined in config", name)
		}
		timeout := 2 * time.Minute
		if chk.Timeout != "" {
			if d, err := time.ParseDuration(chk.Timeout); err == nil {
				timeout = d
			}
		}
		gateChecks = append(gateChecks, checks.GateCheckConfig{
			Name:       name,
			Command:    chk.Command,
			Parser:     chk.Parser,
			Timeout:    timeout,
			AutoFix:    chk.AutoFix,
			FixCommand: chk.FixCommand,
		})
	}

	gate, results, err := e.checker.RunGate(ps.Worktree, checks.GateOpts{
		Issue:    opts.Issue,
		Stage:    opts.Stage,
		FixRound: fixRound,
		Attempt:  ps.CurrentAttempt,
		Worktree: ps.Worktree,
		Checks:   gateChecks,
		Continue: true,
	})

	// Log individual check results to DB (needed by SendFromCheckFailures)
	for _, r := range results {
		_ = e.db.LogCheckRun(
			opts.Issue, opts.Stage, ps.CurrentAttempt, fixRound,
			r.CheckName, r.Passed, r.AutoFixed, r.ExitCode,
			r.DurationMs, r.Summary, r.Findings,
		)
	}

	return gate, results, err
}

// resolvePostChecks determines which checks to run after the agent.
func (e *Engine) resolvePostChecks(stageCfg *config.Stage) []string {
	if stageCfg.SkipChecks {
		return nil
	}
	seen := make(map[string]bool)
	var result []string
	for _, c := range stageCfg.ChecksAfter {
		if !seen[c] {
			result = append(result, c)
			seen[c] = true
		}
	}
	for _, c := range stageCfg.ExtraChecks {
		if !seen[c] {
			result = append(result, c)
			seen[c] = true
		}
	}
	return result
}

// cleanupSession kills and cleans up a session, ignoring errors.
func (e *Engine) cleanupSession(name string) {
	_, _ = e.sessions.Kill(name)
}

// findStageConfig finds a stage in the pipeline config.
func (e *Engine) findStageConfig(stageID string) (*config.Stage, error) {
	for i := range e.cfg.Pipeline.Stages {
		if e.cfg.Pipeline.Stages[i].ID == stageID {
			return &e.cfg.Pipeline.Stages[i], nil
		}
	}
	return nil, fmt.Errorf("stage %q not found in config", stageID)
}

// formatGateFailures formats gate failures into a readable string.
func formatGateFailures(gate *checks.GateResult) string {
	var lines []string
	for name, failure := range gate.RemainingFailures {
		lines = append(lines, fmt.Sprintf("- %s: %s", name, failure.Summary))
	}
	if len(lines) == 0 {
		return "checks failed"
	}
	result := ""
	for _, l := range lines {
		result += l + "\n"
	}
	return result
}
