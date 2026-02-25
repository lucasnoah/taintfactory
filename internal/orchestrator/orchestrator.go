package orchestrator

import (
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/lucasnoah/taintfactory/internal/config"
	appctx "github.com/lucasnoah/taintfactory/internal/context"
	"github.com/lucasnoah/taintfactory/internal/db"
	"github.com/lucasnoah/taintfactory/internal/github"
	"github.com/lucasnoah/taintfactory/internal/pipeline"
	"github.com/lucasnoah/taintfactory/internal/session"
	"github.com/lucasnoah/taintfactory/internal/stage"
	"github.com/lucasnoah/taintfactory/internal/triage"
	"github.com/lucasnoah/taintfactory/internal/worktree"
)

// Orchestrator composes pipeline lifecycle operations.
type Orchestrator struct {
	store        *pipeline.Store
	db           *db.DB
	gh           *github.Client
	wt           *worktree.Manager
	sessions     *session.Manager
	engine       *stage.Engine
	builder      *appctx.Builder
	cfg          *config.PipelineConfig
	claudeFn     github.LLMFunc
	progress     io.Writer       // live progress output; nil = silent
	triageRunner *triage.Runner  // optional; nil if no triage.yaml
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

// SetClaudeFn configures the LLM function used for intent derivation.
func (o *Orchestrator) SetClaudeFn(fn github.LLMFunc) {
	o.claudeFn = fn
}

// SetProgress sets a writer for live progress output (e.g. os.Stderr).
func (o *Orchestrator) SetProgress(w io.Writer) {
	o.progress = w
}

// SetTriageRunner configures an optional triage runner to advance alongside dev pipelines.
func (o *Orchestrator) SetTriageRunner(r *triage.Runner) {
	o.triageRunner = r
}

// logf prints a progress line if a progress writer is configured.
func (o *Orchestrator) logf(format string, args ...interface{}) {
	if o.progress != nil {
		fmt.Fprintf(o.progress, "  → "+format+"\n", args...)
	}
}

// CreateOpts holds options for creating a pipeline.
type CreateOpts struct {
	Issue         int
	FeatureIntent string
	ConfigPath    string // optional: absolute path to pipeline.yaml for multi-project support
}

// configFor returns the effective config for a pipeline.
// If ps.ConfigPath is set, loads and returns that config; otherwise returns o.cfg.
func (o *Orchestrator) configFor(ps *pipeline.PipelineState) (*config.PipelineConfig, error) {
	if ps.ConfigPath == "" {
		return o.cfg, nil
	}
	return config.Load(ps.ConfigPath)
}

// worktreeFor returns a worktree.Manager scoped to the pipeline's repo.
// If ps.RepoDir is set, derives a new manager (sharing the same git runner) for that dir;
// otherwise returns o.wt.
func (o *Orchestrator) worktreeFor(ps *pipeline.PipelineState) *worktree.Manager {
	if ps.RepoDir == "" || o.wt == nil {
		return o.wt
	}
	return o.wt.WithRepoDir(ps.RepoDir)
}

// namespaceFromRepo derives "{org}/{repo}" from a pipeline repo URL.
// e.g. "github.com/myorg/myapp" → "myorg/myapp"
func namespaceFromRepo(repo string) string {
	repo = strings.TrimPrefix(repo, "https://")
	repo = strings.TrimPrefix(repo, "http://")
	parts := strings.SplitN(repo, "/", 2)
	if len(parts) >= 2 {
		return parts[1]
	}
	return repo
}

// Create initializes a new pipeline: fetch issue, create worktree, init state.
func (o *Orchestrator) Create(opts CreateOpts) (*pipeline.PipelineState, error) {
	if opts.Issue <= 0 {
		return nil, fmt.Errorf("invalid issue number %d: must be positive", opts.Issue)
	}

	// Load per-pipeline config if a config path is specified; otherwise use default.
	cfg := o.cfg
	repoDir := ""
	namespace := ""
	if opts.ConfigPath != "" {
		var err error
		cfg, err = config.Load(opts.ConfigPath)
		if err != nil {
			return nil, fmt.Errorf("load pipeline config %s: %w", opts.ConfigPath, err)
		}
		repoDir = filepath.Dir(opts.ConfigPath)
		namespace = namespaceFromRepo(cfg.Pipeline.Repo)
	}

	// Select worktree manager scoped to the repo (reuses the same git runner as o.wt)
	wt := o.wt
	if repoDir != "" && o.wt != nil {
		wt = o.wt.WithRepoDir(repoDir)
	}

	// Fetch issue metadata from GitHub
	issue, err := o.gh.GetIssue(opts.Issue)
	if err != nil {
		return nil, fmt.Errorf("fetch issue: %w", err)
	}

	// Create worktree
	wtResult, err := wt.Create(worktree.CreateOpts{
		Issue: opts.Issue,
		Title: issue.Title,
	})
	if err != nil {
		return nil, fmt.Errorf("create worktree: %w", err)
	}

	// Run setup commands in the worktree (e.g. install dependencies)
	if err := o.runSetupWith(wtResult.Path, cfg); err != nil {
		_ = wt.Remove(opts.Issue, true)
		return nil, fmt.Errorf("worktree setup: %w", err)
	}

	// Determine first stage
	firstStage := ""
	if len(cfg.Pipeline.Stages) > 0 {
		firstStage = cfg.Pipeline.Stages[0].ID
	}

	// Build goal gates from stages with goal_gate=true
	goalGates := make(map[string]string)
	for _, s := range cfg.Pipeline.Stages {
		if s.GoalGate {
			goalGates[s.ID] = ""
		}
	}

	// Create pipeline state (this creates the issue directory)
	ps, err := o.store.Create(pipeline.CreateOpts{
		Issue:      opts.Issue,
		Title:      issue.Title,
		Branch:     wtResult.Branch,
		Worktree:   wtResult.Path,
		FirstStage: firstStage,
		GoalGates:  goalGates,
		ConfigPath: opts.ConfigPath,
		RepoDir:    repoDir,
		Namespace:  namespace,
	})
	if err != nil {
		// Clean up orphaned worktree on store failure
		_ = wt.Remove(opts.Issue, true)
		return nil, fmt.Errorf("create pipeline: %w", err)
	}

	// Store feature intent on pipeline state
	if opts.FeatureIntent != "" {
		_ = o.store.Update(opts.Issue, func(ps *pipeline.PipelineState) {
			ps.FeatureIntent = opts.FeatureIntent
		})
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
	Session     string `json:"session,omitempty"`
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

	// Load per-pipeline config (falls back to o.cfg for legacy pipelines)
	cfg, err := o.configFor(ps)
	if err != nil {
		return nil, fmt.Errorf("load pipeline config: %w", err)
	}

	currentStage := ps.CurrentStage
	currentAttempt := ps.CurrentAttempt
	o.logf("pipeline #%d: advancing stage %q (attempt %d)", issue, currentStage, currentAttempt)

	stageCfg := o.findStage(currentStage, cfg)
	if stageCfg == nil {
		return nil, fmt.Errorf("stage %q not found in config", currentStage)
	}

	// Update status to in_progress
	if err := o.store.Update(issue, func(ps *pipeline.PipelineState) {
		ps.Status = "in_progress"
	}); err != nil {
		return nil, fmt.Errorf("update status: %w", err)
	}

	// Determine timeout from per-pipeline config
	timeout := 30 * time.Minute
	if cfg.Pipeline.Defaults.Timeout != "" {
		if d, err := time.ParseDuration(cfg.Pipeline.Defaults.Timeout); err == nil {
			timeout = d
		}
	}

	// Run the stage lifecycle
	var runResult *stage.RunResult
	if stageCfg.Type == "merge" {
		runResult, err = o.runMerge(issue, ps, stageCfg)
	} else {
		runResult, err = o.engine.Run(stage.RunOpts{
			Issue:   issue,
			Stage:   currentStage,
			Timeout: timeout,
			Config:  cfg,
		})
	}
	if err != nil {
		// Reset status on engine failure so pipeline isn't stuck as in_progress
		_ = o.store.Update(issue, func(ps *pipeline.PipelineState) {
			ps.Status = "pending"
		})
		return nil, fmt.Errorf("run stage: %w", err)
	}

	// Rate limit: pause pipeline and wait for next check-in to retry.
	// Don't record history — this isn't a real stage completion.
	if runResult.Outcome == "rate_limited" {
		o.logf("pipeline #%d: rate limited at stage %q — will retry on next check-in", issue, currentStage)
		if err := o.store.Update(issue, func(ps *pipeline.PipelineState) {
			ps.Status = "rate_limited"
		}); err != nil {
			return nil, fmt.Errorf("update rate_limited status: %w", err)
		}
		_ = o.db.LogPipelineEvent(issue, "rate_limited", currentStage, currentAttempt, "")
		return &AdvanceResult{
			Issue:   issue,
			Action:  "rate_limited",
			Stage:   currentStage,
			Message: "anthropic rate limit detected — will retry automatically on next check-in",
		}, nil
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
		// After any merge-path stage succeeds (merge itself or the agent-merge
		// fallback), prepare runtime vars for the contract-check stage.
		if stageCfg.Type == "merge" || currentStage == o.findMergeOnFailTarget(cfg) {
			o.preparePostMerge(issue, ps)
		}
		return o.advanceToNextStage(issue, currentStage, stageCfg, runResult, cfg)
	}

	// Stage failed — route via on_fail
	return o.handleStageFailure(issue, currentStage, currentAttempt, stageCfg, runResult, cfg)
}

// advanceToNextStage moves the pipeline to the next stage or completes it.
// stageCfg is the config of the just-completed stage; it is used to skip
// the on_fail fallback stage when a merge stage succeeds (the fallback is
// only relevant on failure and should not run on the happy path).
func (o *Orchestrator) advanceToNextStage(issue int, currentStage string, stageCfg *config.Stage, runResult *stage.RunResult, cfg *config.PipelineConfig) (*AdvanceResult, error) {
	nextStage := o.nextStageID(currentStage, cfg)

	// When a merge stage succeeds, skip past its on_fail fallback stage (e.g.
	// agent-merge) so the pipeline proceeds directly to the post-merge stage
	// (e.g. contract-check) or completes if no further stages remain.
	if stageCfg != nil && stageCfg.Type == "merge" && nextStage != "" {
		if onFailTarget := resolveOnFail(stageCfg.OnFail); nextStage == onFailTarget {
			nextStage = o.nextStageID(nextStage, cfg)
		}
	}

	// Skip the contract-check stage when there are no downstream dependents —
	// preparePostMerge already queried the queue and stored the result in
	// RuntimeVars; if it's empty there's nothing for the agent to do.
	if nextStage != "" {
		if nextCfg := o.findStage(nextStage, cfg); nextCfg != nil && nextCfg.PromptTemplate == "contract-check.md" {
			if ps, err := o.store.Get(issue); err == nil && ps.RuntimeVars["dependent_issues"] == "" {
				o.logf("pipeline #%d: skipping contract-check — no downstream dependents", issue)
				nextStage = o.nextStageID(nextStage, cfg)
			}
		}
	}

	if nextStage == "" {
		o.logf("pipeline #%d: no more stages, checking goal gates", issue)
		// No more stages — check goal gates before completing
		if err := o.checkGoalGates(issue, cfg); err != nil {
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
		o.logf("pipeline #%d: completed!", issue)
		_ = o.db.LogPipelineEvent(issue, "completed", currentStage, runResult.Attempt, "")
		_ = o.db.QueueUpdateStatus(issue, "completed")

		return &AdvanceResult{
			Issue:   issue,
			Action:  "completed",
			Stage:   currentStage,
			Session: runResult.Session,
			Outcome: "success",
		}, nil
	}

	// Move to next stage
	o.logf("pipeline #%d: advancing %s → %s", issue, currentStage, nextStage)
	if err := o.store.Update(issue, func(ps *pipeline.PipelineState) {
		ps.Status = "pending"
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
		Session:   runResult.Session,
		NextStage: nextStage,
		Outcome:   "success",
		FixRounds: runResult.FixRounds,
	}, nil
}

// handleStageFailure routes the pipeline based on on_fail config.
// Uses captured values from the start of Advance() to avoid stale-snapshot issues.
func (o *Orchestrator) handleStageFailure(issue int, currentStage string, currentAttempt int, stageCfg *config.Stage, runResult *stage.RunResult, cfg *config.PipelineConfig) (*AdvanceResult, error) {
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
		if o.findStage(target, cfg) == nil {
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
			_ = o.db.QueueUpdateStatus(issue, "failed")

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
			ps.Status = "pending"
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
		ps.Status = "pending"
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
	Session string `json:"session,omitempty"`
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

	hasActive := false
	for i := range pipelines {
		ps := &pipelines[i]
		// Only process active pipelines
		if ps.Status == "completed" || ps.Status == "failed" {
			continue
		}

		hasActive = true

		action := o.checkInPipeline(ps)
		result.Actions = append(result.Actions, action)
		break // strict sequential: only process one pipeline per check-in
	}

	// If no active pipelines, check the queue for the next issue to process
	if !hasActive {
		if action := o.processQueue(); action != nil {
			result.Actions = append(result.Actions, *action)
		}
	}

	// Advance triage pipelines (if configured)
	if o.triageRunner != nil {
		triageActions, err := o.triageRunner.Advance()
		if err != nil {
			o.logf("triage advance error: %v", err)
		}
		for _, a := range triageActions {
			result.Actions = append(result.Actions, CheckInAction{
				Issue:   a.Issue,
				Action:  "triage:" + a.Action,
				Stage:   a.Stage,
				Message: a.Message,
			})
		}
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
			Message: "blocked, waiting for human intervention",
		}
	}

	// Rate-limited pipelines: retry automatically — the stage will re-detect
	// the limit if it's still active, or proceed normally once it has cleared.
	if ps.Status == "rate_limited" {
		o.logf("pipeline #%d: retrying rate-limited stage %q", ps.Issue, ps.CurrentStage)
		return o.handleAdvance(ps)
	}

	// Check for human intervention on active session
	if ps.CurrentSession != "" {
		human, err := o.sessions.DetectHuman(ps.CurrentSession)
		if err != nil {
			// DB error during human detection: be conservative, skip
			return CheckInAction{
				Issue:   ps.Issue,
				Action:  "skip",
				Stage:   ps.CurrentStage,
				Message: "human detection error, skipping conservatively",
			}
		}
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
				return o.handleActiveSession(ps)
			case "idle":
				return o.handleIdleSession(ps)
			case "exited":
				return o.handleExitedSession(ps)
			case "steer", "factory_send":
				// These are intermediate states logged by the factory.
				// The session is still running, treat as active.
				return o.handleActiveSession(ps)
			case "human_input":
				// Human is typing in the session, don't interfere.
				return CheckInAction{
					Issue:   ps.Issue,
					Action:  "skip",
					Stage:   ps.CurrentStage,
					Message: "human input detected in session, skipping",
				}
			default:
				// Unknown session state - skip to avoid interfering
				return CheckInAction{
					Issue:   ps.Issue,
					Action:  "skip",
					Stage:   ps.CurrentStage,
					Message: fmt.Sprintf("unknown session state %q, skipping", si.State),
				}
			}
		}
		// Session lookup failed - session may be orphaned.
		// Clear the session reference and try to advance.
		_ = o.store.Update(ps.Issue, func(ps *pipeline.PipelineState) {
			ps.CurrentSession = ""
		})
	}

	// No session or orphaned session cleared - try to advance
	return o.handleAdvance(ps)
}

// handleActiveSession decides what to do with a running session.
func (o *Orchestrator) handleActiveSession(ps *pipeline.PipelineState) CheckInAction {
	timeout := 30 * time.Minute
	if cfg, err := o.configFor(ps); err == nil && cfg.Pipeline.Defaults.Timeout != "" {
		if d, err := time.ParseDuration(cfg.Pipeline.Defaults.Timeout); err == nil {
			timeout = d
		}
	}

	// Use the session's original "started" timestamp for timeout comparison,
	// not the latest event timestamp (which would be reset by steers/sends).
	startedAt, err := o.db.GetSessionStartedAt(ps.CurrentSession)
	if err != nil {
		return CheckInAction{
			Issue:   ps.Issue,
			Action:  "skip",
			Stage:   ps.CurrentStage,
			Message: "active session, cannot find start time",
		}
	}

	startTime, err := time.Parse("2006-01-02 15:04:05", startedAt)
	if err != nil {
		return CheckInAction{
			Issue:   ps.Issue,
			Action:  "skip",
			Stage:   ps.CurrentStage,
			Message: "active session, cannot parse start timestamp",
		}
	}

	if time.Since(startTime) <= timeout {
		return CheckInAction{
			Issue:   ps.Issue,
			Action:  "skip",
			Stage:   ps.CurrentStage,
			Message: "session active, within timeout",
		}
	}

	// Session exceeded timeout. Check if we already sent a recent steer
	// to avoid flooding the session with repeated messages.
	recentlysteered, _ := o.db.HasRecentSteer(ps.CurrentSession, "-10 minutes")
	if recentlysteered {
		return CheckInAction{
			Issue:   ps.Issue,
			Action:  "skip",
			Stage:   ps.CurrentStage,
			Message: "session exceeded timeout, steer already sent recently",
		}
	}

	if err := o.sessions.Steer(ps.CurrentSession, "Please wrap up your current work and finalize changes."); err != nil {
		return CheckInAction{
			Issue:   ps.Issue,
			Action:  "skip",
			Stage:   ps.CurrentStage,
			Message: fmt.Sprintf("session exceeded timeout, steer failed: %v", err),
		}
	}

	return CheckInAction{
		Issue:   ps.Issue,
		Action:  "steer",
		Stage:   ps.CurrentStage,
		Message: "session exceeded timeout, sent wrap-up steer",
	}
}

// handleIdleSession handles a session that finished and is waiting.
func (o *Orchestrator) handleIdleSession(ps *pipeline.PipelineState) CheckInAction {
	// The session finished but the advance process was killed before it could
	// record the result. Kill the idle session so engine.Run() can create a
	// fresh one, then re-run the stage.
	if ps.CurrentSession != "" {
		_, _ = o.sessions.Kill(ps.CurrentSession)
		_ = o.store.Update(ps.Issue, func(p *pipeline.PipelineState) {
			p.CurrentSession = ""
			p.Status = "pending"
		})
	}
	return o.handleAdvance(ps)
}

// handleExitedSession handles a session that has exited.
func (o *Orchestrator) handleExitedSession(ps *pipeline.PipelineState) CheckInAction {
	// Same as idle: clean up so engine.Run() can start fresh.
	if ps.CurrentSession != "" {
		_, _ = o.sessions.Kill(ps.CurrentSession)
		_ = o.store.Update(ps.Issue, func(p *pipeline.PipelineState) {
			p.CurrentSession = ""
			p.Status = "pending"
		})
	}
	return o.handleAdvance(ps)
}

// handleAdvance attempts to advance a pipeline.
func (o *Orchestrator) handleAdvance(ps *pipeline.PipelineState) CheckInAction {
	advResult, err := o.Advance(ps.Issue)
	if err != nil {
		// Mark pipeline as blocked so it doesn't loop forever on errors
		_ = o.store.Update(ps.Issue, func(ps *pipeline.PipelineState) {
			ps.Status = "blocked"
		})
		_ = o.db.LogPipelineEvent(ps.Issue, "escalated", ps.CurrentStage, ps.CurrentAttempt, fmt.Sprintf("check-in advance error: %v", err))
		return CheckInAction{
			Issue:   ps.Issue,
			Action:  "escalate",
			Stage:   ps.CurrentStage,
			Message: fmt.Sprintf("advance error, escalated: %v", err),
		}
	}

	return CheckInAction{
		Issue:   ps.Issue,
		Action:  advResult.Action,
		Stage:   ps.CurrentStage,
		Session: advResult.Session,
		Message: advResult.Message,
	}
}

// processQueue pops the next pending item from the queue and starts a pipeline.
func (o *Orchestrator) processQueue() *CheckInAction {
	item, err := o.db.QueueNext()
	if err != nil || item == nil {
		return nil
	}
	o.logf("queue: processing issue #%d", item.Issue)

	// Try to derive feature intent from GitHub metadata via LLM if not explicitly set
	if item.FeatureIntent == "" && o.claudeFn != nil {
		o.logf("queue: deriving feature intent for #%d via LLM...", item.Issue)
		issue, err := o.gh.GetIssue(item.Issue)
		if err != nil {
			return &CheckInAction{
				Issue:   item.Issue,
				Action:  "skip",
				Message: fmt.Sprintf("queue: failed to fetch issue #%d for intent derivation: %v", item.Issue, err),
			}
		}
		derived, err := github.DeriveFeatureIntent(issue, o.claudeFn)
		if err != nil {
			return &CheckInAction{
				Issue:   item.Issue,
				Action:  "skip",
				Message: fmt.Sprintf("queue: intent derivation failed for #%d: %v", item.Issue, err),
			}
		}
		if derived != "" {
			item.FeatureIntent = derived
			_ = o.db.QueueSetIntent(item.Issue, derived)
		}
	}

	// Reject issues without a feature intent even after derivation attempt
	if item.FeatureIntent == "" {
		return &CheckInAction{
			Issue:   item.Issue,
			Action:  "skip",
			Message: "queue: issue missing feature_intent — use `factory queue set-intent` or ensure the issue has clear user-facing intent",
		}
	}

	if err := o.db.QueueUpdateStatus(item.Issue, "active"); err != nil {
		return nil
	}

	o.logf("queue: creating pipeline for issue #%d", item.Issue)
	_, err = o.Create(CreateOpts{Issue: item.Issue, FeatureIntent: item.FeatureIntent, ConfigPath: item.ConfigPath})
	if err != nil {
		_ = o.db.QueueUpdateStatus(item.Issue, "failed")
		return &CheckInAction{
			Issue:   item.Issue,
			Action:  "fail",
			Message: fmt.Sprintf("queue: failed to create pipeline: %v", err),
		}
	}

	return &CheckInAction{
		Issue:   item.Issue,
		Action:  "queue_started",
		Message: fmt.Sprintf("queue: started pipeline for issue #%d", item.Issue),
	}
}

// runSetupWith runs the pipeline.setup commands from cfg inside the worktree directory.
func (o *Orchestrator) runSetupWith(worktreePath string, cfg *config.PipelineConfig) error {
	for _, cmdStr := range cfg.Pipeline.Setup {
		o.logf("setup: running %q in %s", cmdStr, worktreePath)
		cmd := exec.Command("sh", "-c", cmdStr)
		cmd.Dir = worktreePath
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("command %q failed: %s: %w", cmdStr, strings.TrimSpace(string(out)), err)
		}
	}
	return nil
}

// runMerge handles the merge stage: push branch, create PR, merge PR.
func (o *Orchestrator) runMerge(issue int, ps *pipeline.PipelineState, stageCfg *config.Stage) (*stage.RunResult, error) {
	start := time.Now()
	o.logf("pipeline #%d: running merge stage", issue)

	result := &stage.RunResult{
		Issue:           issue,
		Stage:           stageCfg.ID,
		Attempt:         ps.CurrentAttempt,
		FinalCheckState: make(map[string]string),
		AutoFixes:       make(map[string]int),
		AgentFixes:      make(map[string]int),
	}

	// Rebase onto main before pushing to surface divergence early.
	// This handles the common case where other PRs merged after this branch
	// was cut, causing a "both added" or content conflict at gh pr merge time.
	o.logf("rebasing %s onto origin/main", ps.Branch)
	conflicted, rebaseErr := o.gh.RebaseOntoMain(ps.Worktree)
	if rebaseErr != nil {
		o.logf("rebase failed: %v", rebaseErr)
		result.Outcome = "fail"
		result.TotalDuration = time.Since(start)
		return result, nil
	}
	if conflicted {
		o.logf("merge conflicts detected during rebase onto origin/main; manual resolution required")
		result.Outcome = "fail"
		result.TotalDuration = time.Since(start)
		return result, nil
	}

	// Push branch (force-with-lease to handle any history rewrite from the rebase)
	o.logf("pushing branch %s from %s", ps.Branch, ps.Worktree)
	if err := o.gh.ForcePushBranch(ps.Worktree, ps.Branch); err != nil {
		o.logf("push failed: %v", err)
		result.Outcome = "fail"
		result.TotalDuration = time.Since(start)
		return result, nil
	}

	// Check for existing PR before creating a new one (idempotent retry)
	existing, err := o.gh.FindPRByBranch(ps.Branch)
	if err != nil {
		o.logf("find existing PR failed: %v", err)
		result.Outcome = "fail"
		result.TotalDuration = time.Since(start)
		return result, nil
	}

	if existing != nil {
		o.logf("reusing existing PR: %s", existing.URL)
	} else {
		// Create PR
		prTitle := fmt.Sprintf("#%d: %s", issue, ps.Title)
		prBody := fmt.Sprintf("Closes #%d\n\nAutomated merge via pipeline.", issue)
		o.logf("creating PR: %s", prTitle)
		_, err = o.gh.CreatePR(github.PRCreateOpts{
			Title:  prTitle,
			Body:   prBody,
			Branch: ps.Branch,
		})
		if err != nil {
			o.logf("create PR failed: %v", err)
			result.Outcome = "fail"
			result.TotalDuration = time.Since(start)
			return result, nil
		}
	}

	// Remove worktree before merging so that --delete-branch can delete the
	// local branch (git refuses to delete a branch checked out in a worktree).
	if wt := o.worktreeFor(ps); wt != nil {
		if err := wt.Remove(issue, false); err != nil {
			o.logf("warning: remove worktree before merge: %v", err)
		}
	}

	// Merge PR
	strategy := stageCfg.MergeStrategy
	if strategy == "" {
		strategy = "squash"
	}
	o.logf("merging PR on branch %s with strategy %s", ps.Branch, strategy)
	if err := o.gh.MergePR(ps.Branch, strategy); err != nil {
		o.logf("merge PR failed: %v", err)
		result.Outcome = "fail"
		result.TotalDuration = time.Since(start)
		return result, nil
	}

	o.logf("pipeline #%d: merge successful", issue)
	result.Outcome = "success"
	result.TotalDuration = time.Since(start)
	return result, nil
}

// CleanupResult describes what happened during a single pipeline cleanup.
type CleanupResult struct {
	Issue   int    `json:"issue"`
	Removed bool   `json:"removed"`
	Message string `json:"message"`
}

// Cleanup removes the worktree, branch, and pipeline data for a terminal pipeline.
func (o *Orchestrator) Cleanup(issue int) (*CleanupResult, error) {
	ps, err := o.store.Get(issue)
	if err != nil {
		return nil, fmt.Errorf("get pipeline: %w", err)
	}

	if ps.Status != "completed" && ps.Status != "failed" {
		return nil, fmt.Errorf("pipeline %d has status %q: only completed or failed pipelines can be cleaned up", issue, ps.Status)
	}

	// Kill active session defensively
	if ps.CurrentSession != "" {
		_, _ = o.sessions.Kill(ps.CurrentSession)
	}

	// Remove worktree + branch
	if o.wt != nil {
		if err := o.wt.Remove(issue, true); err != nil {
			// If worktree is already gone, log and continue
			if !strings.Contains(err.Error(), "not a working tree") && !strings.Contains(err.Error(), "No such file") {
				return nil, fmt.Errorf("remove worktree: %w", err)
			}
			o.logf("pipeline #%d: worktree already removed, continuing", issue)
		}
	}

	// Remove pipeline data from disk
	if err := o.store.Delete(issue); err != nil {
		return nil, fmt.Errorf("delete pipeline data: %w", err)
	}

	return &CleanupResult{
		Issue:   issue,
		Removed: true,
		Message: fmt.Sprintf("pipeline #%d cleaned up (worktree + data removed)", issue),
	}, nil
}

// CleanupAll removes worktrees and pipeline data for all terminal pipelines.
func (o *Orchestrator) CleanupAll() ([]CleanupResult, error) {
	completed, err := o.store.List("completed")
	if err != nil {
		return nil, fmt.Errorf("list completed pipelines: %w", err)
	}
	failed, err := o.store.List("failed")
	if err != nil {
		return nil, fmt.Errorf("list failed pipelines: %w", err)
	}

	var results []CleanupResult
	for _, ps := range append(completed, failed...) {
		r, err := o.Cleanup(ps.Issue)
		if err != nil {
			results = append(results, CleanupResult{
				Issue:   ps.Issue,
				Removed: false,
				Message: fmt.Sprintf("cleanup failed: %v", err),
			})
			continue
		}
		results = append(results, *r)
	}
	return results, nil
}

// --- Helpers ---

// preparePostMerge is called after a merge (or merge-fallback agent) stage
// succeeds. It:
//  1. Derives the repo root from the feature worktree path (2 levels up) and
//     updates ps.Worktree so the next stage (contract-check) runs there.
//  2. Queries the queue for issues that depend on the just-merged issue and
//     stores them as ps.RuntimeVars["dependent_issues"] for the template.
func (o *Orchestrator) preparePostMerge(issue int, ps *pipeline.PipelineState) {
	repoRoot := filepath.Dir(filepath.Dir(ps.Worktree))

	dependents, err := o.db.QueueDependents(issue)
	var depText string
	if err == nil && len(dependents) > 0 {
		var sb strings.Builder
		for _, dep := range dependents {
			if dep.FeatureIntent != "" {
				fmt.Fprintf(&sb, "- #%d: %s\n", dep.Issue, dep.FeatureIntent)
			} else {
				fmt.Fprintf(&sb, "- #%d (no feature intent set)\n", dep.Issue)
			}
		}
		depText = strings.TrimSpace(sb.String())
	}

	_ = o.store.Update(issue, func(p *pipeline.PipelineState) {
		if p.RuntimeVars == nil {
			p.RuntimeVars = make(map[string]string)
		}
		p.RuntimeVars["dependent_issues"] = depText
		p.Worktree = repoRoot
	})
}

// findMergeOnFailTarget returns the on_fail routing target of the first
// merge-type stage in cfg, or "" if none exists.
func (o *Orchestrator) findMergeOnFailTarget(cfg *config.PipelineConfig) string {
	for _, s := range cfg.Pipeline.Stages {
		if s.Type == "merge" {
			return resolveOnFail(s.OnFail)
		}
	}
	return ""
}

// findStage finds a stage config by ID in cfg.
func (o *Orchestrator) findStage(stageID string, cfg *config.PipelineConfig) *config.Stage {
	for i := range cfg.Pipeline.Stages {
		if cfg.Pipeline.Stages[i].ID == stageID {
			return &cfg.Pipeline.Stages[i]
		}
	}
	return nil
}

// nextStageID returns the stage ID after the given one in cfg, or "" if last.
func (o *Orchestrator) nextStageID(currentID string, cfg *config.PipelineConfig) string {
	for i, s := range cfg.Pipeline.Stages {
		if s.ID == currentID && i+1 < len(cfg.Pipeline.Stages) {
			return cfg.Pipeline.Stages[i+1].ID
		}
	}
	return ""
}

// checkGoalGates verifies all goal_gate stages in cfg have passed.
func (o *Orchestrator) checkGoalGates(issue int, cfg *config.PipelineConfig) error {
	ps, err := o.store.Get(issue)
	if err != nil {
		return err
	}

	for _, s := range cfg.Pipeline.Stages {
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
