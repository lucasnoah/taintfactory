package orchestrator

import (
	"fmt"
	"time"

	"github.com/lucasnoah/taintfactory/internal/config"
	appctx "github.com/lucasnoah/taintfactory/internal/context"
	"github.com/lucasnoah/taintfactory/internal/db"
	"github.com/lucasnoah/taintfactory/internal/github"
	"github.com/lucasnoah/taintfactory/internal/pipeline"
	"github.com/lucasnoah/taintfactory/internal/session"
	"github.com/lucasnoah/taintfactory/internal/stage"
	"github.com/lucasnoah/taintfactory/internal/worktree"
)

// Orchestrator composes pipeline lifecycle operations.
type Orchestrator struct {
	store   *pipeline.Store
	db      *db.DB
	gh      *github.Client
	wt      *worktree.Manager
	sessions *session.Manager
	engine  *stage.Engine
	builder *appctx.Builder
	cfg     *config.PipelineConfig
}

// NewOrchestrator creates an Orchestrator.
func NewOrchestrator(
	store *pipeline.Store,
	database *db.DB,
	gh *github.Client,
	wt *worktree.Manager,
	sessions *session.Manager,
	engine *stage.Engine,
	builder *appctx.Builder,
	cfg *config.PipelineConfig,
) *Orchestrator {
	return &Orchestrator{
		store:    store,
		db:       database,
		gh:       gh,
		wt:       wt,
		sessions: sessions,
		engine:   engine,
		builder:  builder,
		cfg:      cfg,
	}
}

// CreateOpts holds options for creating a pipeline.
type CreateOpts struct {
	Issue int
}

// Create initializes a new pipeline: fetch issue, create worktree, init state.
func (o *Orchestrator) Create(opts CreateOpts) (*pipeline.PipelineState, error) {
	if opts.Issue <= 0 {
		return nil, fmt.Errorf("invalid issue number %d: must be positive", opts.Issue)
	}

	// Fetch issue metadata from GitHub
	issue, err := o.gh.GetIssue(opts.Issue)
	if err != nil {
		return nil, fmt.Errorf("fetch issue: %w", err)
	}

	// Create worktree
	wtResult, err := o.wt.Create(worktree.CreateOpts{
		Issue: opts.Issue,
		Title: issue.Title,
	})
	if err != nil {
		return nil, fmt.Errorf("create worktree: %w", err)
	}

	// Determine first stage
	firstStage := ""
	if len(o.cfg.Pipeline.Stages) > 0 {
		firstStage = o.cfg.Pipeline.Stages[0].ID
	}

	// Build goal gates from stages with goal_gate=true
	goalGates := make(map[string]string)
	for _, s := range o.cfg.Pipeline.Stages {
		if s.GoalGate {
			goalGates[s.ID] = ""
		}
	}

	// Create pipeline state (this creates the issue directory)
	ps, err := o.store.Create(opts.Issue, issue.Title, wtResult.Branch, wtResult.Path, firstStage, goalGates)
	if err != nil {
		// Clean up orphaned worktree on store failure
		_ = o.wt.Remove(opts.Issue, true)
		return nil, fmt.Errorf("create pipeline: %w", err)
	}

	// Cache issue JSON to disk (directory now exists from store.Create)
	pipelineDir := fmt.Sprintf("%s/%d", o.store.BaseDir(), opts.Issue)
	_, _ = o.gh.CacheIssue(opts.Issue, pipelineDir)

	_ = o.db.LogPipelineEvent(opts.Issue, "created", firstStage, 1, "")
	return ps, nil
}

// AdvanceResult describes what happened during an advance.
type AdvanceResult struct {
	Issue       int    `json:"issue"`
	Action      string `json:"action"` // "advanced", "completed", "failed", "escalated", "retry", "routed"
	Stage       string `json:"stage"`
	NextStage   string `json:"next_stage,omitempty"`
	Outcome     string `json:"outcome,omitempty"`
	FixRounds   int    `json:"fix_rounds,omitempty"`
	Message     string `json:"message,omitempty"`
}

// Advance runs the current stage and advances the pipeline on success.
func (o *Orchestrator) Advance(issue int) (*AdvanceResult, error) {
	ps, err := o.store.Get(issue)
	if err != nil {
		return nil, fmt.Errorf("get pipeline: %w", err)
	}

	if ps.Status == "completed" {
		return &AdvanceResult{Issue: issue, Action: "completed", Message: "pipeline already completed"}, nil
	}
	if ps.Status == "failed" || ps.Status == "blocked" {
		return &AdvanceResult{Issue: issue, Action: "failed", Message: fmt.Sprintf("pipeline is %s", ps.Status)}, nil
	}

	currentStage := ps.CurrentStage
	currentAttempt := ps.CurrentAttempt

	stageCfg := o.findStage(currentStage)
	if stageCfg == nil {
		return nil, fmt.Errorf("stage %q not found in config", currentStage)
	}

	// Update status to in_progress
	if err := o.store.Update(issue, func(ps *pipeline.PipelineState) {
		ps.Status = "in_progress"
	}); err != nil {
		return nil, fmt.Errorf("update status: %w", err)
	}

	// Determine timeout
	timeout := 30 * time.Minute
	if o.cfg.Pipeline.Defaults.Timeout != "" {
		if d, err := time.ParseDuration(o.cfg.Pipeline.Defaults.Timeout); err == nil {
			timeout = d
		}
	}

	// Run the stage lifecycle
	runResult, err := o.engine.Run(stage.RunOpts{
		Issue:   issue,
		Stage:   currentStage,
		Timeout: timeout,
	})
	if err != nil {
		// Reset status on engine failure so pipeline isn't stuck as in_progress
		_ = o.store.Update(issue, func(ps *pipeline.PipelineState) {
			ps.Status = "pending"
		})
		return nil, fmt.Errorf("run stage: %w", err)
	}

	// Record stage history
	if err := o.store.Update(issue, func(ps *pipeline.PipelineState) {
		ps.StageHistory = append(ps.StageHistory, pipeline.StageHistoryEntry{
			Stage:           runResult.Stage,
			Attempt:         runResult.Attempt,
			Outcome:         runResult.Outcome,
			Duration:        runResult.TotalDuration.String(),
			FixRounds:       runResult.FixRounds,
			ChecksFirstPass: runResult.ChecksFirstPass,
		})
	}); err != nil {
		return nil, fmt.Errorf("record stage history: %w", err)
	}

	// Checkpoint the stage outcome
	_ = o.builder.Checkpoint(issue, currentStage, currentAttempt, appctx.CheckpointOpts{
		Status:  runResult.Outcome,
		Summary: formatCheckStateSummary(runResult.FinalCheckState),
	})

	// Update goal gate if applicable
	if stageCfg.GoalGate && runResult.Outcome == "success" {
		if err := o.store.Update(issue, func(ps *pipeline.PipelineState) {
			if ps.GoalGates == nil {
				ps.GoalGates = make(map[string]string)
			}
			ps.GoalGates[currentStage] = "success"
		}); err != nil {
			return nil, fmt.Errorf("update goal gate: %w", err)
		}
	}

	if runResult.Outcome == "success" {
		return o.advanceToNextStage(issue, currentStage, runResult)
	}

	// Stage failed — route via on_fail
	return o.handleStageFailure(issue, currentStage, currentAttempt, stageCfg, runResult)
}

// advanceToNextStage moves the pipeline to the next stage or completes it.
func (o *Orchestrator) advanceToNextStage(issue int, currentStage string, runResult *stage.RunResult) (*AdvanceResult, error) {
	nextStage := o.nextStageID(currentStage)

	if nextStage == "" {
		// No more stages — check goal gates before completing
		if err := o.checkGoalGates(issue); err != nil {
			return &AdvanceResult{
				Issue:   issue,
				Action:  "failed",
				Stage:   currentStage,
				Message: err.Error(),
			}, nil
		}

		if err := o.store.Update(issue, func(ps *pipeline.PipelineState) {
			ps.Status = "completed"
		}); err != nil {
			return nil, fmt.Errorf("update completed status: %w", err)
		}
		_ = o.db.LogPipelineEvent(issue, "completed", currentStage, runResult.Attempt, "")

		return &AdvanceResult{
			Issue:   issue,
			Action:  "completed",
			Stage:   currentStage,
			Outcome: "success",
		}, nil
	}

	// Move to next stage
	if err := o.store.Update(issue, func(ps *pipeline.PipelineState) {
		ps.CurrentStage = nextStage
		ps.CurrentAttempt = 1
		ps.CurrentFixRound = 0
		ps.CurrentSession = ""
	}); err != nil {
		return nil, fmt.Errorf("advance to next stage: %w", err)
	}
	_ = o.db.LogPipelineEvent(issue, "stage_advanced", nextStage, 1, fmt.Sprintf("from=%s", currentStage))

	return &AdvanceResult{
		Issue:     issue,
		Action:    "advanced",
		Stage:     currentStage,
		NextStage: nextStage,
		Outcome:   "success",
		FixRounds: runResult.FixRounds,
	}, nil
}

// handleStageFailure routes the pipeline based on on_fail config.
// Uses captured values from the start of Advance() to avoid stale-snapshot issues.
func (o *Orchestrator) handleStageFailure(issue int, currentStage string, currentAttempt int, stageCfg *config.Stage, runResult *stage.RunResult) (*AdvanceResult, error) {
	target := resolveOnFail(stageCfg.OnFail)

	if target == "escalate" {
		if err := o.store.Update(issue, func(ps *pipeline.PipelineState) {
			ps.Status = "blocked"
		}); err != nil {
			return nil, fmt.Errorf("update blocked status: %w", err)
		}
		_ = o.db.LogPipelineEvent(issue, "escalated", currentStage, currentAttempt, "")

		return &AdvanceResult{
			Issue:   issue,
			Action:  "escalated",
			Stage:   currentStage,
			Outcome: "fail",
			Message: "stage escalated, human intervention required",
		}, nil
	}

	// Validate on_fail target stage exists (if routing to a different stage)
	if target != "" && target != currentStage {
		if o.findStage(target) == nil {
			return nil, fmt.Errorf("on_fail target stage %q not found in config", target)
		}
	}

	// Check max attempts
	maxAttempts := 3
	if target == currentStage || target == "" {
		// Retrying same stage
		if currentAttempt >= maxAttempts {
			if err := o.store.Update(issue, func(ps *pipeline.PipelineState) {
				ps.Status = "failed"
			}); err != nil {
				return nil, fmt.Errorf("update failed status: %w", err)
			}
			_ = o.db.LogPipelineEvent(issue, "max_attempts_reached", currentStage, currentAttempt, "")

			return &AdvanceResult{
				Issue:   issue,
				Action:  "failed",
				Stage:   currentStage,
				Outcome: "fail",
				Message: fmt.Sprintf("max attempts (%d) reached for stage %q", maxAttempts, currentStage),
			}, nil
		}

		// Increment attempt
		newAttempt := currentAttempt + 1
		if err := o.store.Update(issue, func(ps *pipeline.PipelineState) {
			ps.CurrentAttempt = newAttempt
			ps.CurrentFixRound = 0
			ps.CurrentSession = ""
		}); err != nil {
			return nil, fmt.Errorf("increment attempt: %w", err)
		}
		_ = o.db.LogPipelineEvent(issue, "retry", currentStage, newAttempt, "auto")

		return &AdvanceResult{
			Issue:     issue,
			Action:    "retry",
			Stage:     currentStage,
			Outcome:   "fail",
			FixRounds: runResult.FixRounds,
			Message:   fmt.Sprintf("stage failed, will retry (attempt %d)", newAttempt),
		}, nil
	}

	// Route to different stage
	if err := o.store.Update(issue, func(ps *pipeline.PipelineState) {
		ps.CurrentStage = target
		ps.CurrentAttempt = 1
		ps.CurrentFixRound = 0
		ps.CurrentSession = ""
	}); err != nil {
		return nil, fmt.Errorf("route to stage %q: %w", target, err)
	}
	_ = o.db.LogPipelineEvent(issue, "on_fail_routed", target, 1, fmt.Sprintf("from=%s", currentStage))

	return &AdvanceResult{
		Issue:     issue,
		Action:    "routed",
		Stage:     currentStage,
		NextStage: target,
		Outcome:   "fail",
		FixRounds: runResult.FixRounds,
		Message:   fmt.Sprintf("stage failed, routing to %q", target),
	}, nil
}

// RetryOpts holds options for retrying a pipeline stage.
type RetryOpts struct {
	Issue  int
	Reason string
}

// Retry manually retries the current stage. This intentionally overrides
// the automatic max-attempt limit to allow human-directed recovery.
func (o *Orchestrator) Retry(opts RetryOpts) error {
	ps, err := o.store.Get(opts.Issue)
	if err != nil {
		return fmt.Errorf("get pipeline: %w", err)
	}

	if ps.Status == "completed" {
		return fmt.Errorf("pipeline %d is already completed", opts.Issue)
	}

	newAttempt := ps.CurrentAttempt + 1
	if err := o.store.Update(opts.Issue, func(ps *pipeline.PipelineState) {
		ps.CurrentAttempt = newAttempt
		ps.CurrentFixRound = 0
		ps.CurrentSession = ""
		ps.Status = "in_progress"
	}); err != nil {
		return fmt.Errorf("update pipeline: %w", err)
	}

	detail := "manual"
	if opts.Reason != "" {
		detail = fmt.Sprintf("manual: %s", opts.Reason)
	}
	_ = o.db.LogPipelineEvent(opts.Issue, "retry", ps.CurrentStage, newAttempt, detail)

	return nil
}

// FailOpts holds options for failing a pipeline.
type FailOpts struct {
	Issue  int
	Reason string
}

// Fail marks a pipeline as failed and kills active sessions.
func (o *Orchestrator) Fail(opts FailOpts) error {
	ps, err := o.store.Get(opts.Issue)
	if err != nil {
		return fmt.Errorf("get pipeline: %w", err)
	}

	// Kill active session if any
	if ps.CurrentSession != "" {
		_, _ = o.sessions.Kill(ps.CurrentSession)
	}

	if err := o.store.Update(opts.Issue, func(ps *pipeline.PipelineState) {
		ps.Status = "failed"
	}); err != nil {
		return fmt.Errorf("update pipeline: %w", err)
	}

	detail := ""
	if opts.Reason != "" {
		detail = opts.Reason
	}
	_ = o.db.LogPipelineEvent(opts.Issue, "failed", ps.CurrentStage, ps.CurrentAttempt, detail)

	return nil
}

// AbortOpts holds options for aborting a pipeline.
type AbortOpts struct {
	Issue         int
	RemoveWorktree bool
}

// Abort terminates a pipeline, kills sessions, and optionally removes the worktree.
func (o *Orchestrator) Abort(opts AbortOpts) error {
	ps, err := o.store.Get(opts.Issue)
	if err != nil {
		return fmt.Errorf("get pipeline: %w", err)
	}

	// Kill active session
	if ps.CurrentSession != "" {
		_, _ = o.sessions.Kill(ps.CurrentSession)
	}

	// Optionally remove worktree
	if opts.RemoveWorktree && o.wt != nil {
		if err := o.wt.Remove(opts.Issue, true); err != nil {
			return fmt.Errorf("remove worktree: %w", err)
		}
	}

	if err := o.store.Update(opts.Issue, func(ps *pipeline.PipelineState) {
		ps.Status = "failed"
	}); err != nil {
		return fmt.Errorf("update pipeline: %w", err)
	}
	_ = o.db.LogPipelineEvent(opts.Issue, "aborted", ps.CurrentStage, ps.CurrentAttempt, "")

	return nil
}

// StatusInfo holds combined pipeline status from disk + DB.
type StatusInfo struct {
	Issue          int                       `json:"issue"`
	Title          string                    `json:"title"`
	Status         string                    `json:"status"`
	Stage          string                    `json:"stage"`
	Attempt        int                       `json:"attempt"`
	Session        string                    `json:"session"`
	SessionState   string                    `json:"session_state,omitempty"`
	Branch         string                    `json:"branch"`
	FixRound       int                       `json:"fix_round"`
	StageHistory   []pipeline.StageHistoryEntry `json:"stage_history,omitempty"`
	GoalGates      map[string]string         `json:"goal_gates,omitempty"`
}

// Status returns combined status for a pipeline.
func (o *Orchestrator) Status(issue int) (*StatusInfo, error) {
	ps, err := o.store.Get(issue)
	if err != nil {
		return nil, fmt.Errorf("get pipeline: %w", err)
	}

	info := &StatusInfo{
		Issue:        ps.Issue,
		Title:        ps.Title,
		Status:       ps.Status,
		Stage:        ps.CurrentStage,
		Attempt:      ps.CurrentAttempt,
		Session:      ps.CurrentSession,
		Branch:       ps.Branch,
		FixRound:     ps.CurrentFixRound,
		StageHistory: ps.StageHistory,
		GoalGates:    ps.GoalGates,
	}

	// Enrich with session state from DB
	if ps.CurrentSession != "" {
		si, err := o.sessions.Status(ps.CurrentSession)
		if err == nil {
			info.SessionState = si.State
		}
	}

	return info, nil
}

// StatusAll returns summary status for all pipelines.
func (o *Orchestrator) StatusAll() ([]StatusInfo, error) {
	pipelines, err := o.store.List("")
	if err != nil {
		return nil, fmt.Errorf("list pipelines: %w", err)
	}

	var result []StatusInfo
	for _, ps := range pipelines {
		info := StatusInfo{
			Issue:        ps.Issue,
			Title:        ps.Title,
			Status:       ps.Status,
			Stage:        ps.CurrentStage,
			Attempt:      ps.CurrentAttempt,
			Session:      ps.CurrentSession,
			Branch:       ps.Branch,
			FixRound:     ps.CurrentFixRound,
			StageHistory: ps.StageHistory,
			GoalGates:    ps.GoalGates,
		}

		if ps.CurrentSession != "" {
			si, err := o.sessions.Status(ps.CurrentSession)
			if err == nil {
				info.SessionState = si.State
			}
		}

		result = append(result, info)
	}

	return result, nil
}

// CheckInAction describes a single action taken during a check-in tick.
type CheckInAction struct {
	Issue   int    `json:"issue"`
	Action  string `json:"action"`  // "skip", "steer", "advance", "retry", "escalate", "fail"
	Stage   string `json:"stage"`
	Message string `json:"message"`
}

// CheckInResult is the summary returned by a check-in tick.
type CheckInResult struct {
	Actions []CheckInAction `json:"actions"`
}

// CheckIn runs the orchestrator decision loop for all in-flight pipelines.
// This is meant to be called on a cron schedule (e.g. every 5 minutes).
func (o *Orchestrator) CheckIn() (*CheckInResult, error) {
	pipelines, err := o.store.List("")
	if err != nil {
		return nil, fmt.Errorf("list pipelines: %w", err)
	}

	result := &CheckInResult{Actions: []CheckInAction{}}

	for _, ps := range pipelines {
		// Only process active pipelines
		if ps.Status == "completed" || ps.Status == "failed" {
			continue
		}

		action := o.checkInPipeline(&ps)
		result.Actions = append(result.Actions, action)
	}

	return result, nil
}

// checkInPipeline evaluates a single pipeline and takes the appropriate action.
func (o *Orchestrator) checkInPipeline(ps *pipeline.PipelineState) CheckInAction {
	// Blocked pipelines need human intervention
	if ps.Status == "blocked" {
		return CheckInAction{
			Issue:   ps.Issue,
			Action:  "skip",
			Stage:   ps.CurrentStage,
			Message: "blocked — waiting for human intervention",
		}
	}

	// Check for human intervention on active session
	if ps.CurrentSession != "" {
		human, _ := o.sessions.DetectHuman(ps.CurrentSession)
		if human {
			return CheckInAction{
				Issue:   ps.Issue,
				Action:  "skip",
				Stage:   ps.CurrentStage,
				Message: "human intervention detected, skipping",
			}
		}
	}

	// Check session state
	if ps.CurrentSession != "" {
		si, err := o.sessions.Status(ps.CurrentSession)
		if err == nil {
			switch si.State {
			case "started", "active":
				return o.handleActiveSession(ps, si)
			case "idle":
				return o.handleIdleSession(ps)
			case "exited":
				return o.handleExitedSession(ps)
			}
		}
		// Session state unknown — try to advance
	}

	// No session or unknown state — try to advance
	return o.handleAdvance(ps)
}

// handleActiveSession decides what to do with a running session.
func (o *Orchestrator) handleActiveSession(ps *pipeline.PipelineState, si *session.StatusInfo) CheckInAction {
	timeout := 30 * time.Minute
	if o.cfg.Pipeline.Defaults.Timeout != "" {
		if d, err := time.ParseDuration(o.cfg.Pipeline.Defaults.Timeout); err == nil {
			timeout = d
		}
	}

	elapsed, err := time.Parse("2006-01-02 15:04:05", si.Timestamp)
	if err != nil {
		return CheckInAction{
			Issue:   ps.Issue,
			Action:  "skip",
			Stage:   ps.CurrentStage,
			Message: "active session, cannot parse timestamp",
		}
	}

	if time.Since(elapsed) > timeout {
		_ = o.sessions.Steer(ps.CurrentSession, "Please wrap up your current work and finalize changes.")
		return CheckInAction{
			Issue:   ps.Issue,
			Action:  "steer",
			Stage:   ps.CurrentStage,
			Message: fmt.Sprintf("session exceeded timeout, sent wrap-up steer"),
		}
	}

	return CheckInAction{
		Issue:   ps.Issue,
		Action:  "skip",
		Stage:   ps.CurrentStage,
		Message: "session active, within timeout",
	}
}

// handleIdleSession handles a session that finished and is waiting.
func (o *Orchestrator) handleIdleSession(ps *pipeline.PipelineState) CheckInAction {
	return o.handleAdvance(ps)
}

// handleExitedSession handles a session that has exited.
func (o *Orchestrator) handleExitedSession(ps *pipeline.PipelineState) CheckInAction {
	// Session exited — try to advance the pipeline.
	// Advance will run checks and handle success/failure/retry internally.
	return o.handleAdvance(ps)
}

// handleAdvance attempts to advance a pipeline.
func (o *Orchestrator) handleAdvance(ps *pipeline.PipelineState) CheckInAction {
	advResult, err := o.Advance(ps.Issue)
	if err != nil {
		return CheckInAction{
			Issue:   ps.Issue,
			Action:  "fail",
			Stage:   ps.CurrentStage,
			Message: fmt.Sprintf("advance error: %v", err),
		}
	}

	return CheckInAction{
		Issue:   ps.Issue,
		Action:  advResult.Action,
		Stage:   ps.CurrentStage,
		Message: advResult.Message,
	}
}

// --- Helpers ---

// findStage finds a stage config by ID.
func (o *Orchestrator) findStage(stageID string) *config.Stage {
	for i := range o.cfg.Pipeline.Stages {
		if o.cfg.Pipeline.Stages[i].ID == stageID {
			return &o.cfg.Pipeline.Stages[i]
		}
	}
	return nil
}

// nextStageID returns the stage ID after the given one, or "" if last.
func (o *Orchestrator) nextStageID(currentID string) string {
	for i, s := range o.cfg.Pipeline.Stages {
		if s.ID == currentID && i+1 < len(o.cfg.Pipeline.Stages) {
			return o.cfg.Pipeline.Stages[i+1].ID
		}
	}
	return ""
}

// checkGoalGates verifies all goal_gate stages have passed.
func (o *Orchestrator) checkGoalGates(issue int) error {
	ps, err := o.store.Get(issue)
	if err != nil {
		return err
	}

	for _, s := range o.cfg.Pipeline.Stages {
		if !s.GoalGate {
			continue
		}
		if v, ok := ps.GoalGates[s.ID]; !ok || v != "success" {
			return fmt.Errorf("goal gate %q not satisfied", s.ID)
		}
	}
	return nil
}

// resolveOnFail determines the target stage from on_fail config.
func resolveOnFail(onFail interface{}) string {
	if onFail == nil {
		return "" // retry same stage
	}
	if s, ok := onFail.(string); ok {
		return s
	}
	if m, ok := onFail.(map[string]interface{}); ok {
		// Check for "default" key
		if def, ok := m["default"]; ok {
			if s, ok := def.(string); ok {
				return s
			}
		}
	}
	// YAML maps may parse as map[interface{}]interface{}
	if m, ok := onFail.(map[interface{}]interface{}); ok {
		if def, ok := m["default"]; ok {
			if s, ok := def.(string); ok {
				return s
			}
		}
	}
	return ""
}

// formatCheckStateSummary formats a check state map into a readable string.
func formatCheckStateSummary(state map[string]string) string {
	if len(state) == 0 {
		return ""
	}
	result := ""
	for name, status := range state {
		result += fmt.Sprintf("%s: %s\n", name, status)
	}
	return result
}
