package stage

import (
	"fmt"
	"io"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/lucasnoah/taintfactory/internal/checks"
	"github.com/lucasnoah/taintfactory/internal/config"
	appctx "github.com/lucasnoah/taintfactory/internal/context"
	"github.com/lucasnoah/taintfactory/internal/db"
	"github.com/lucasnoah/taintfactory/internal/github"
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
	bootDelay    time.Duration // delay after session create for Claude to boot; defaults to 15s
	progress     io.Writer     // live progress output; nil = silent
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
		bootDelay:    15 * time.Second,
	}
}

// SetPollInterval overrides the WaitIdle poll interval (for testing).
func (e *Engine) SetPollInterval(d time.Duration) {
	e.pollInterval = d
}

// SetBootDelay overrides the boot delay (for testing).
func (e *Engine) SetBootDelay(d time.Duration) {
	e.bootDelay = d
}

// SetProgress sets a writer for live progress output (e.g. os.Stderr).
func (e *Engine) SetProgress(w io.Writer) {
	e.progress = w
}

// logf prints a progress line if a progress writer is configured.
func (e *Engine) logf(format string, args ...interface{}) {
	if e.progress != nil {
		fmt.Fprintf(e.progress, "  → "+format+"\n", args...)
	}
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
	Session         string            `json:"session,omitempty"`
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
	e.logf("issue #%d: running stage %q", opts.Issue, opts.Stage)

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
		e.logf("stage type is checks_only, running checks directly")
		return e.runChecksOnly(ps, stageCfg, opts, result, start)
	}

	// Run checks_before if configured
	if len(stageCfg.ChecksBefore) > 0 {
		e.logf("running checks_before: %v", stageCfg.ChecksBefore)
		gate, _, err := e.runGate(ps, opts, stageCfg.ChecksBefore, 0)
		if err != nil {
			return nil, fmt.Errorf("checks_before: %w", err)
		}
		if !gate.Passed {
			e.logf("checks_before failed — stage aborted")
			result.Outcome = "fail"
			result.TotalDuration = time.Since(start)
			_ = e.db.LogPipelineEvent(opts.Issue, "checks_before_failed", opts.Stage, ps.CurrentAttempt, "")
			return result, nil
		}
		e.logf("checks_before passed")
	}

	// Build context and render prompt
	e.logf("building context and rendering prompt...")
	agentStart := time.Now()
	rendered, err := e.buildAndRenderPrompt(ps, opts, stageCfg)
	if err != nil {
		return nil, fmt.Errorf("build prompt: %w", err)
	}
	e.logf("prompt rendered (%d bytes)", len(rendered))

	// Save rendered prompt
	_ = e.store.SavePrompt(opts.Issue, opts.Stage, ps.CurrentAttempt, rendered)

	// Create session and send prompt
	sessionName := fmt.Sprintf("%d-%s-%d", opts.Issue, opts.Stage, ps.CurrentAttempt)
	result.Session = sessionName
	e.logf("creating agent session: %s", sessionName)
	if err := e.createAndRunSession(sessionName, ps, opts, stageCfg, rendered); err != nil {
		return nil, fmt.Errorf("run session: %w", err)
	}
	result.AgentDuration = time.Since(agentStart)
	e.logf("agent finished (%s)", result.AgentDuration.Round(time.Second))

	// Steer agent to commit any uncommitted changes
	e.ensureCommitted(sessionName, ps.Worktree, opts.Timeout)

	// Run post-checks
	checkNames := e.resolvePostChecks(stageCfg)
	if len(checkNames) == 0 {
		e.logf("no post-checks configured — stage passed")
		result.Outcome = "success"
		result.ChecksFirstPass = true
		result.TotalDuration = time.Since(start)
		e.cleanupSession(sessionName)
		return result, nil
	}

	e.logf("running post-checks: %v", checkNames)
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
		e.logf("all post-checks passed on first try")
		result.Outcome = "success"
		result.ChecksFirstPass = true
		result.TotalDuration = time.Since(start)
		e.cleanupSession(sessionName)
		return result, nil
	}

	e.logf("post-checks failed, entering fix loop")

	// Enter fix loop
	maxFixRounds := e.cfg.Pipeline.MaxFixRounds
	if maxFixRounds <= 0 {
		maxFixRounds = 3
	}
	freshAfter := e.cfg.Pipeline.FreshSessionAfter
	if freshAfter <= 0 {
		freshAfter = 2
	}

	// Track which checks were failing before the fix loop for accurate AgentFixes counting
	failingChecks := make(map[string]bool)
	for _, c := range gate.Checks {
		if !c.Passed {
			failingChecks[c.Check] = true
		}
	}

	for round := 1; round <= maxFixRounds; round++ {
		e.logf("fix round %d/%d", round, maxFixRounds)
		result.FixRounds = round
		_ = e.db.LogPipelineEvent(opts.Issue, "fix_round_start", opts.Stage, ps.CurrentAttempt, fmt.Sprintf("round=%d", round))

		// Determine if we need a fresh session
		if round > freshAfter {
			e.logf("creating fresh fix session (round > %d)", freshAfter)
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
			e.logf("sending fix prompt to existing session %s", sessionName)
			// Send fix prompt to existing session
			if err := e.sessions.SendFromCheckFailures(sessionName, opts.Issue, opts.Stage); err != nil {
				e.cleanupSession(sessionName)
				return nil, fmt.Errorf("send fix prompt: %w", err)
			}
			// Wait for idle
			waitResult, err := e.sessions.WaitIdle(sessionName, opts.Timeout, e.pollInterval)
			if err != nil {
				e.cleanupSession(sessionName)
				return nil, fmt.Errorf("wait idle (fix): %w", err)
			}
			if waitResult.State == "exited" {
				e.cleanupSession(sessionName)
				return nil, fmt.Errorf("session exited during fix round %d", round)
			}
		}

		// Steer agent to commit any uncommitted changes from the fix round
		e.ensureCommitted(sessionName, ps.Worktree, opts.Timeout)

		// Re-run checks
		e.logf("re-running checks after fix round %d", round)
		gate, _, err = e.runGate(ps, opts, checkNames, round)
		if err != nil {
			e.cleanupSession(sessionName)
			return nil, fmt.Errorf("re-check (round %d): %w", round, err)
		}

		// Update check state — only count AgentFixes for fail→pass transitions
		for _, c := range gate.Checks {
			if c.Passed {
				result.FinalCheckState[c.Check] = "pass"
				if failingChecks[c.Check] && !c.AutoFixed {
					result.AgentFixes[c.Check]++
					delete(failingChecks, c.Check)
				}
			} else {
				result.FinalCheckState[c.Check] = "fail"
				failingChecks[c.Check] = true
			}
		}

		if gate.Passed {
			e.logf("all checks passed after fix round %d", round)
			result.Outcome = "success"
			result.TotalDuration = time.Since(start)
			e.cleanupSession(sessionName)
			return result, nil
		}
		e.logf("checks still failing after fix round %d", round)
	}

	// Fix loop exhausted
	e.logf("fix loop exhausted after %d rounds — stage failed", maxFixRounds)
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
		e.logf("no checks configured — stage passed")
		result.Outcome = "success"
		result.ChecksFirstPass = true
		result.TotalDuration = time.Since(start)
		return result, nil
	}

	e.logf("running checks: %v", checkNames)
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
		e.logf("all checks passed")
		result.Outcome = "success"
		result.ChecksFirstPass = true
		result.TotalDuration = time.Since(start)
		return result, nil
	}

	e.logf("checks failed — stage failed")
	result.Outcome = "fail"
	result.TotalDuration = time.Since(start)
	return result, nil
}

// buildAndRenderPrompt builds context and renders the stage prompt.
func (e *Engine) buildAndRenderPrompt(ps *pipeline.PipelineState, opts RunOpts, stageCfg *config.Stage) (string, error) {
	// Load cached issue body from pipeline directory
	var issueBody string
	pipelineDir := fmt.Sprintf("%s/%d", e.store.BaseDir(), opts.Issue)
	if issue, err := github.LoadCachedIssue(pipelineDir); err == nil {
		issueBody = issue.Body
	}

	buildResult, err := e.builder.Build(ps, appctx.BuildOpts{
		Issue:        opts.Issue,
		Stage:        opts.Stage,
		StageCfg:     stageCfg,
		IssueBody:    issueBody,
		PipelineVars: e.cfg.Pipeline.Vars,
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
	// Load cached issue body
	var issueBody string
	pipelineDir := fmt.Sprintf("%s/%d", e.store.BaseDir(), opts.Issue)
	if issue, err := github.LoadCachedIssue(pipelineDir); err == nil {
		issueBody = issue.Body
	}

	// Build fix-checks template with failure details
	vars := prompt.Vars{
		"issue_title":    ps.Title,
		"issue_number":   fmt.Sprintf("%d", ps.Issue),
		"issue_body":     issueBody,
		"feature_intent": ps.FeatureIntent,
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

	model := stageCfg.Model
	if model == "" {
		model = e.cfg.Pipeline.Defaults.Model
	}
	if model == "" {
		model = "claude-opus-4-6"
	}

	e.logf("creating tmux session %s in %s (model: %s)", name, ps.Worktree, model)
	if err := e.sessions.Create(session.CreateOpts{
		Name:        name,
		Workdir:     ps.Worktree,
		Flags:       flags,
		Model:       model,
		Issue:       opts.Issue,
		Stage:       opts.Stage,
		Interactive: true,
	}); err != nil {
		return fmt.Errorf("create session: %w", err)
	}

	// Wait for Claude to boot and become ready for input.
	// Claude Code takes ~5-10s to start; we use a fixed delay since
	// the Stop hook doesn't fire on initial boot (only after processing).
	e.logf("waiting %s for Claude to boot...", e.bootDelay)
	time.Sleep(e.bootDelay)

	// Send the rendered prompt
	e.logf("sending prompt to session %s", name)
	if err := e.sessions.Send(name, rendered); err != nil {
		e.cleanupSession(name)
		return fmt.Errorf("send prompt: %w", err)
	}

	// Wait for session to become idle after processing
	e.logf("waiting for session %s to become idle (timeout: %s)...", name, opts.Timeout)
	waitResult, err := e.sessions.WaitIdle(name, opts.Timeout, e.pollInterval)
	if err != nil {
		e.cleanupSession(name)
		return fmt.Errorf("wait idle: %w", err)
	}

	if waitResult.State == "exited" {
		return fmt.Errorf("session exited unexpectedly")
	}

	e.logf("session %s is idle", name)
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

	// Log individual check results and report progress
	for _, r := range results {
		status := "PASS"
		if !r.Passed {
			status = "FAIL"
		}
		e.logf("check %s: %s (%dms)", r.CheckName, status, r.DurationMs)
		if dbErr := e.db.LogCheckRun(
			opts.Issue, opts.Stage, ps.CurrentAttempt, fixRound,
			r.CheckName, r.Passed, r.AutoFixed, r.ExitCode,
			r.DurationMs, r.Summary, r.Findings,
		); dbErr != nil {
			return nil, nil, fmt.Errorf("log check run %q: %w", r.CheckName, dbErr)
		}
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

// ensureCommitted checks for uncommitted changes and steers the agent to commit them.
// This preserves the agent's context for writing meaningful commit messages.
func (e *Engine) ensureCommitted(sessionName string, worktree string, timeout time.Duration) {
	cmd := exec.Command("git", "status", "--porcelain")
	cmd.Dir = worktree
	out, err := cmd.Output()
	if err != nil || len(strings.TrimSpace(string(out))) == 0 {
		return // no changes or git error
	}

	e.logf("uncommitted changes detected — steering agent to commit")
	if err := e.sessions.Steer(sessionName, "You have uncommitted changes. Please commit all your work now with a descriptive commit message."); err != nil {
		e.logf("warning: steer to commit failed: %v", err)
		return
	}

	result, err := e.sessions.WaitIdle(sessionName, timeout, e.pollInterval)
	if err != nil {
		e.logf("warning: wait for commit idle failed: %v", err)
		return
	}
	if result.State == "exited" {
		e.logf("warning: session exited while committing")
		return
	}
	e.logf("agent committed changes")
}

// cleanupSession captures the session log, saves it, then kills the session.
func (e *Engine) cleanupSession(name string) {
	log, err := e.sessions.Kill(name)
	if err != nil {
		return
	}
	if log != "" {
		// Parse issue/stage/attempt from session name (format: {issue}-{stage}-{attempt})
		// Best-effort save — don't fail the pipeline if this doesn't work.
		_ = e.saveSessionLog(name, log)
	}
}

// saveSessionLog persists the captured tmux pane output to the pipeline store.
func (e *Engine) saveSessionLog(sessionName string, log string) error {
	// Look up session metadata from DB
	state, err := e.db.GetSessionState(sessionName)
	if err != nil || state == nil {
		return err
	}
	ps, err := e.store.Get(state.Issue)
	if err != nil {
		return err
	}
	return e.store.SaveSessionLog(state.Issue, state.Stage, ps.CurrentAttempt, log)
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

// formatGateFailures formats gate failures into a deterministic readable string.
func formatGateFailures(gate *checks.GateResult) string {
	if len(gate.RemainingFailures) == 0 {
		return "checks failed"
	}
	// Sort keys for deterministic output
	names := make([]string, 0, len(gate.RemainingFailures))
	for name := range gate.RemainingFailures {
		names = append(names, name)
	}
	sort.Strings(names)

	result := ""
	for _, name := range names {
		result += fmt.Sprintf("- %s: %s\n", name, gate.RemainingFailures[name].Summary)
	}
	return result
}
