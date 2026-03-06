package orchestrator

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/lucasnoah/taintfactory/internal/config"
	"github.com/lucasnoah/taintfactory/internal/pipeline"
	"github.com/lucasnoah/taintfactory/internal/prompt"
	"github.com/lucasnoah/taintfactory/internal/session"
)

// DeployCheckInAction represents one action taken during a deploy check-in.
type DeployCheckInAction struct {
	CommitSHA string `json:"commit_sha"`
	Action    string `json:"action"`
	Stage     string `json:"stage,omitempty"`
	Message   string `json:"message,omitempty"`
}

// checkInDeploy finds the first pending/in_progress deploy and advances it.
func (o *Orchestrator) checkInDeploy() *DeployCheckInAction {
	if o.deployStore == nil {
		return nil
	}

	deploys, err := o.deployStore.List("")
	if err != nil {
		o.logf("deploy check-in: list error: %v", err)
		return nil
	}

	for i := range deploys {
		ds := &deploys[i]
		if ds.Status == "completed" || ds.Status == "failed" || ds.Status == "rolled_back" {
			continue
		}

		return o.advanceDeploy(ds)
	}

	return nil
}

// advanceDeploy processes a single deploy: checks session state and runs stages.
func (o *Orchestrator) advanceDeploy(ds *pipeline.DeployState) *DeployCheckInAction {
	sha := ds.CommitSHA
	sha7 := shortDeploySHA(sha)

	// If there's an active session, check its state
	if ds.CurrentSession != "" {
		si, err := o.sessions.Status(ds.CurrentSession)
		if err == nil {
			if !si.TmuxAlive {
				o.logf("deploy %s: session %q dead, clearing", sha7, ds.CurrentSession)
				_ = o.deployStore.Update(sha, func(ds *pipeline.DeployState) {
					ds.CurrentSession = ""
				})
			} else {
				switch si.State {
				case "started", "active", "steer", "factory_send":
					return &DeployCheckInAction{
						CommitSHA: sha,
						Action:    "skip",
						Stage:     ds.CurrentStage,
						Message:   "session active",
					}
				case "idle":
					// Session finished — clear it and advance
					o.logf("deploy %s: session %q idle, advancing", sha7, ds.CurrentSession)
					_ = o.deployStore.Update(sha, func(ds *pipeline.DeployState) {
						ds.CurrentSession = ""
					})
				case "exited":
					o.logf("deploy %s: session %q exited", sha7, ds.CurrentSession)
					_ = o.deployStore.Update(sha, func(ds *pipeline.DeployState) {
						ds.CurrentSession = ""
					})
				}
			}
		} else {
			// Session lookup failed — clear stale reference
			_ = o.deployStore.Update(sha, func(ds *pipeline.DeployState) {
				ds.CurrentSession = ""
			})
		}
	}

	// Re-read state (may have been updated above)
	ds, err := o.deployStore.Get(sha)
	if err != nil {
		return &DeployCheckInAction{CommitSHA: sha, Action: "error", Message: err.Error()}
	}

	// If session was just cleared and stage is in_progress, record success and advance
	if ds.Status == "in_progress" && ds.CurrentSession == "" {
		return o.advanceDeployToNext(ds)
	}

	// Run the current stage
	cfg, err := o.deployConfig(ds)
	if err != nil {
		return &DeployCheckInAction{CommitSHA: sha, Action: "error", Message: err.Error()}
	}

	stageCfg := findDeployStage(ds.CurrentStage, cfg)
	if stageCfg == nil {
		return &DeployCheckInAction{
			CommitSHA: sha,
			Action:    "error",
			Message:   fmt.Sprintf("stage %q not found in deploy config", ds.CurrentStage),
		}
	}

	// Mark as in_progress
	_ = o.deployStore.Update(sha, func(ds *pipeline.DeployState) {
		ds.Status = "in_progress"
	})
	o.logDeployDB(func() {
		_ = o.db.DeployUpdateStatus(sha, "in_progress", ds.CurrentStage, stageHistoryJSON(ds.StageHistory))
		_ = o.db.LogDeployEvent(sha, ds.Namespace, "stage_started", ds.CurrentStage, ds.CurrentAttempt, "")
	})

	// Run the deploy stage
	sessionName, err := o.runDeployStage(ds, stageCfg, cfg)
	if err != nil {
		o.logf("deploy %s: stage %q error: %v", sha7, ds.CurrentStage, err)
		_ = o.deployStore.Update(sha, func(ds *pipeline.DeployState) {
			ds.Status = "pending"
			ds.CurrentSession = ""
		})
		return &DeployCheckInAction{
			CommitSHA: sha,
			Action:    "error",
			Stage:     ds.CurrentStage,
			Message:   fmt.Sprintf("run error: %v", err),
		}
	}

	// Store session reference for next check-in to monitor
	_ = o.deployStore.Update(sha, func(ds *pipeline.DeployState) {
		ds.CurrentSession = sessionName
	})

	return &DeployCheckInAction{
		CommitSHA: sha,
		Action:    "running",
		Stage:     ds.CurrentStage,
		Message:   fmt.Sprintf("session %s started", sessionName),
	}
}

// advanceDeployToNext records stage success and moves to the next stage or marks completed.
func (o *Orchestrator) advanceDeployToNext(ds *pipeline.DeployState) *DeployCheckInAction {
	sha := ds.CommitSHA
	sha7 := shortDeploySHA(sha)

	cfg, err := o.deployConfig(ds)
	if err != nil {
		return &DeployCheckInAction{CommitSHA: sha, Action: "error", Message: err.Error()}
	}

	// Record stage history
	_ = o.deployStore.Update(sha, func(ds *pipeline.DeployState) {
		ds.StageHistory = append(ds.StageHistory, pipeline.StageHistoryEntry{
			Stage:   ds.CurrentStage,
			Attempt: ds.CurrentAttempt,
			Outcome: "success",
		})
	})

	// Re-read after update
	ds, _ = o.deployStore.Get(sha)
	o.logDeployDB(func() {
		_ = o.db.LogDeployEvent(sha, ds.Namespace, "stage_completed", ds.CurrentStage, ds.CurrentAttempt, "success")
	})

	// If this stage was reached via failure routing and is a rollback stage,
	// mark the deploy as rolled_back instead of advancing further.
	if len(ds.FailureVisited) > 0 && isRollbackStage(ds.CurrentStage) {
		o.logf("deploy %s: rollback stage %q succeeded — marking rolled_back", sha7, ds.CurrentStage)
		_ = o.deployStore.Update(sha, func(ds *pipeline.DeployState) {
			ds.Status = "rolled_back"
		})
		o.logDeployDB(func() {
			_ = o.db.DeployUpdateStatus(sha, "rolled_back", ds.CurrentStage, stageHistoryJSON(ds.StageHistory))
			_ = o.db.LogDeployEvent(sha, ds.Namespace, "rolled_back", ds.CurrentStage, 0, "")
		})
		return &DeployCheckInAction{
			CommitSHA: sha,
			Action:    "rolled_back",
			Stage:     ds.CurrentStage,
		}
	}

	// Find next stage
	nextStage := nextDeployStageID(ds.CurrentStage, cfg)

	if nextStage == "" {
		// No more stages — completed
		o.logf("deploy %s: all stages completed", sha7)
		_ = o.deployStore.Update(sha, func(ds *pipeline.DeployState) {
			ds.Status = "completed"
		})
		o.logDeployDB(func() {
			_ = o.db.DeployUpdateStatus(sha, "completed", ds.CurrentStage, stageHistoryJSON(ds.StageHistory))
			_ = o.db.LogDeployEvent(sha, ds.Namespace, "completed", "", 0, "")
		})
		return &DeployCheckInAction{
			CommitSHA: sha,
			Action:    "completed",
			Stage:     ds.CurrentStage,
		}
	}

	// Advance to next stage
	o.logf("deploy %s: advancing %s → %s", sha7, ds.CurrentStage, nextStage)
	currentStage := ds.CurrentStage
	_ = o.deployStore.Update(sha, func(ds *pipeline.DeployState) {
		ds.CurrentStage = nextStage
		ds.CurrentAttempt = 1
		ds.CurrentSession = ""
	})
	o.logDeployDB(func() {
		_ = o.db.DeployUpdateStatus(sha, "in_progress", nextStage, stageHistoryJSON(ds.StageHistory))
		_ = o.db.LogDeployEvent(sha, ds.Namespace, "stage_advanced", nextStage, 1, fmt.Sprintf("from=%s", currentStage))
	})

	return &DeployCheckInAction{
		CommitSHA: sha,
		Action:    "advanced",
		Stage:     nextStage,
		Message:   fmt.Sprintf("advanced from %s", ds.CurrentStage),
	}
}

// handleDeployFailure routes a failed stage via on_fail config or marks the deploy as failed.
// Uses visited-set cycle detection per ADR 0017.
func (o *Orchestrator) handleDeployFailure(ds *pipeline.DeployState, stageCfg *config.Stage, cfg *config.DeployPipeline) *DeployCheckInAction {
	sha := ds.CommitSHA
	sha7 := shortDeploySHA(sha)

	// Record failure in stage history
	_ = o.deployStore.Update(sha, func(ds *pipeline.DeployState) {
		ds.StageHistory = append(ds.StageHistory, pipeline.StageHistoryEntry{
			Stage:   ds.CurrentStage,
			Attempt: ds.CurrentAttempt,
			Outcome: "fail",
		})
	})
	ds, _ = o.deployStore.Get(sha)

	o.logDeployDB(func() {
		_ = o.db.LogDeployEvent(sha, ds.Namespace, "stage_failed", ds.CurrentStage, ds.CurrentAttempt, "")
	})

	// Check for on_fail routing
	target := resolveOnFail(stageCfg.OnFail)
	if target == "" {
		// No on_fail configured — mark deploy as failed
		o.logf("deploy %s: stage %q failed, no on_fail configured", sha7, ds.CurrentStage)
		_ = o.deployStore.Update(sha, func(ds *pipeline.DeployState) {
			ds.Status = "failed"
		})
		o.logDeployDB(func() {
			_ = o.db.DeployUpdateStatus(sha, "failed", ds.CurrentStage, stageHistoryJSON(ds.StageHistory))
			_ = o.db.LogDeployEvent(sha, ds.Namespace, "failed", ds.CurrentStage, 0, "no on_fail target")
		})
		return &DeployCheckInAction{
			CommitSHA: sha,
			Action:    "failed",
			Stage:     ds.CurrentStage,
			Message:   "stage failed, no on_fail configured",
		}
	}

	// Check visited-set for cycle detection (ADR 0017)
	for _, visited := range ds.FailureVisited {
		if visited == target {
			o.logf("deploy %s: cycle detected — %q already visited", sha7, target)
			_ = o.deployStore.Update(sha, func(ds *pipeline.DeployState) {
				ds.Status = "failed"
			})
			o.logDeployDB(func() {
				_ = o.db.DeployUpdateStatus(sha, "failed", ds.CurrentStage, stageHistoryJSON(ds.StageHistory))
				_ = o.db.LogDeployEvent(sha, ds.Namespace, "failed", ds.CurrentStage, 0, fmt.Sprintf("cycle detected: %s already visited", target))
			})
			return &DeployCheckInAction{
				CommitSHA: sha,
				Action:    "failed",
				Stage:     ds.CurrentStage,
				Message:   fmt.Sprintf("cycle detected: %s already visited", target),
			}
		}
	}

	// Verify target exists in config
	if findDeployStage(target, cfg) == nil {
		o.logf("deploy %s: on_fail target %q not found in config", sha7, target)
		_ = o.deployStore.Update(sha, func(ds *pipeline.DeployState) {
			ds.Status = "failed"
		})
		o.logDeployDB(func() {
			_ = o.db.DeployUpdateStatus(sha, "failed", ds.CurrentStage, stageHistoryJSON(ds.StageHistory))
		})
		return &DeployCheckInAction{
			CommitSHA: sha,
			Action:    "failed",
			Stage:     ds.CurrentStage,
			Message:   fmt.Sprintf("on_fail target %q not found", target),
		}
	}

	// Route to failure target
	o.logf("deploy %s: routing to on_fail target %q", sha7, target)
	_ = o.deployStore.Update(sha, func(ds *pipeline.DeployState) {
		ds.FailureVisited = append(ds.FailureVisited, target)
		ds.CurrentStage = target
		ds.CurrentAttempt = 1
		ds.CurrentSession = ""
		ds.Status = "pending"
	})
	o.logDeployDB(func() {
		_ = o.db.LogDeployEvent(sha, ds.Namespace, "failure_routed", target, 1, fmt.Sprintf("from=%s", ds.CurrentStage))
	})

	return &DeployCheckInAction{
		CommitSHA: sha,
		Action:    "failure_routed",
		Stage:     target,
		Message:   fmt.Sprintf("routed from %s to %s", ds.CurrentStage, target),
	}
}

// isRollbackStage checks if a stage is a rollback-type stage.
func isRollbackStage(stageID string) bool {
	return stageID == "rollback" || strings.Contains(stageID, "rollback")
}

// runDeployStage creates a session, renders the prompt, and sends it.
// It does NOT block waiting for idle — the next check-in monitors the session.
// Returns the session name on success.
func (o *Orchestrator) runDeployStage(ds *pipeline.DeployState, stageCfg *config.Stage, cfg *config.DeployPipeline) (string, error) {
	sha7 := shortDeploySHA(ds.CommitSHA)

	// Session naming: deploy-{sha7}-{stage}-{attempt} (ADR 0015)
	sessionName := fmt.Sprintf("deploy-%s-%s-%d", sha7, ds.CurrentStage, ds.CurrentAttempt)

	// Build deploy vars map
	vars := prompt.Vars{
		"commit_sha":   ds.CommitSHA,
		"previous_sha": ds.PreviousSHA,
		"namespace":    ds.Namespace,
		"stage_id":     ds.CurrentStage,
		"attempt":      fmt.Sprintf("%d", ds.CurrentAttempt),
	}
	if ds.RepoDir != "" {
		vars["repo_dir"] = ds.RepoDir
	}

	// Merge stage-level vars (stage vars take precedence)
	for k, v := range stageCfg.Vars {
		vars[k] = v
	}

	// Determine workdir: use RepoDir if set, otherwise current dir
	workdir := ds.RepoDir
	if workdir == "" {
		workdir = "."
	}

	// Load and render prompt template
	tmplContent, err := prompt.LoadTemplate(stageCfg.PromptTemplate, workdir)
	if err != nil {
		return "", fmt.Errorf("load template %q: %w", stageCfg.PromptTemplate, err)
	}

	rendered, err := prompt.Render(tmplContent, vars)
	if err != nil {
		return "", fmt.Errorf("render template: %w", err)
	}

	// Save rendered prompt to attempt directory
	_ = o.deployStore.InitStageAttempt(ds.CommitSHA, ds.CurrentStage, ds.CurrentAttempt)
	_ = o.deployStore.SavePrompt(ds.CommitSHA, ds.CurrentStage, ds.CurrentAttempt, rendered)

	// Determine model and flags
	model := stageCfg.Model
	if model == "" {
		model = "claude-opus-4-6"
	}
	flags := stageCfg.Flags

	o.logf("deploy %s: creating session %s (stage: %s, model: %s)", sha7, sessionName, ds.CurrentStage, model)

	if err := o.sessions.Create(session.CreateOpts{
		Name:        sessionName,
		Workdir:     workdir,
		Flags:       flags,
		Model:       model,
		Issue:       0, // deploys don't have an issue number
		Stage:       ds.CurrentStage,
		Interactive: true,
	}); err != nil {
		return "", fmt.Errorf("create session: %w", err)
	}

	// Wait for Claude to boot
	if err := o.sessions.DismissStartupDialogs(sessionName); err != nil {
		o.logf("deploy %s: dialog dismissal warning: %v", sha7, err)
	}

	// Send prompt
	o.logf("deploy %s: sending prompt (%d bytes)", sha7, len(rendered))
	if err := o.sessions.Send(sessionName, rendered); err != nil {
		_, _ = o.sessions.Kill(sessionName)
		return "", fmt.Errorf("send prompt: %w", err)
	}

	// Non-blocking: session is now running. Next check-in will monitor it.
	o.logf("deploy %s: session %s launched, will monitor on next check-in", sha7, sessionName)
	return sessionName, nil
}

// deployConfig loads the deploy pipeline config for a deploy state.
func (o *Orchestrator) deployConfig(ds *pipeline.DeployState) (*config.DeployPipeline, error) {
	var cfg *config.PipelineConfig
	var err error
	if ds.ConfigPath != "" {
		cfg, err = config.Load(ds.ConfigPath)
	} else {
		cfg = o.cfg
	}
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}
	if cfg.Deploy == nil {
		return nil, fmt.Errorf("no deploy section in config")
	}
	return cfg.Deploy, nil
}

// findDeployStage finds a stage by ID in the deploy pipeline config.
func findDeployStage(stageID string, cfg *config.DeployPipeline) *config.Stage {
	for i := range cfg.Stages {
		if cfg.Stages[i].ID == stageID {
			return &cfg.Stages[i]
		}
	}
	return nil
}

// nextDeployStageID returns the stage ID after the given one, or "" if last.
func nextDeployStageID(currentID string, cfg *config.DeployPipeline) string {
	for i, s := range cfg.Stages {
		if s.ID == currentID && i+1 < len(cfg.Stages) {
			return cfg.Stages[i+1].ID
		}
	}
	return ""
}

// shortDeploySHA returns the first 7 chars of a SHA for display.
func shortDeploySHA(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

// logDeployDB is a nil-safe helper for DB deploy operations.
func (o *Orchestrator) logDeployDB(fn func()) {
	if o.db != nil {
		fn()
	}
}

// stageHistoryJSON serializes stage history to JSON for the DB column.
func stageHistoryJSON(history []pipeline.StageHistoryEntry) string {
	data, err := json.Marshal(history)
	if err != nil {
		return "[]"
	}
	return string(data)
}
