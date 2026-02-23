package triage

import (
	"bufio"
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"text/template"
	"time"

	"github.com/lucasnoah/taintfactory/internal/db"
	"github.com/lucasnoah/taintfactory/internal/github"
	"github.com/lucasnoah/taintfactory/internal/session"
)

//go:embed templates/stale-context.md
var defaultStaleContextTmpl string

//go:embed templates/already-implemented.md
var defaultAlreadyImplementedTmpl string

var defaultTemplates = map[string]string{
	"stale_context":       defaultStaleContextTmpl,
	"already_implemented": defaultAlreadyImplementedTmpl,
}

// TriageAction describes what the runner did for one triage pipeline on a check-in.
type TriageAction struct {
	Issue   int
	Stage   string
	Action  string // "skip", "advance", "completed", "error", "started"
	Message string
}

// GHClient is the subset of github.Client the runner needs.
type GHClient interface {
	GetIssue(number int) (*github.Issue, error)
}

// Runner advances triage pipelines on each orchestrator check-in.
type Runner struct {
	cfg      *TriageConfig
	store    *Store
	db       *db.DB
	sessions *session.Manager
	gh       GHClient
	repoRoot string
	progress io.Writer
	bootWait time.Duration // defaults to 15s; set to 0 in tests

	// printExec executes claude --print for a stage and returns stdout.
	// Defaults to the real exec.Command implementation; overridable in tests.
	printExec func(stageCfg *TriageStage, prompt string) (string, error)

	// labelExec applies a GitHub label to an issue.
	// Defaults to the real gh CLI implementation; overridable in tests.
	labelExec func(repo string, issue int, label string) error
}

// NewRunner creates a new Runner. bootWait defaults to 15s.
func NewRunner(cfg *TriageConfig, store *Store, database *db.DB, sessions *session.Manager, gh GHClient, repoRoot string) *Runner {
	r := &Runner{
		cfg:      cfg,
		store:    store,
		db:       database,
		sessions: sessions,
		gh:       gh,
		repoRoot: repoRoot,
		bootWait: 15 * time.Second,
	}
	r.printExec = r.defaultExecPrint
	r.labelExec = r.defaultApplyLabel
	return r
}

// SetProgress sets the writer for progress logging.
func (r *Runner) SetProgress(w io.Writer) {
	r.progress = w
}

// FirstStageID returns the ID of the first stage in the config, or "" if none.
func (r *Runner) FirstStageID() string {
	if len(r.cfg.Stages) == 0 {
		return ""
	}
	return r.cfg.Stages[0].ID
}

// logf prints a formatted message to the progress writer if one is set.
func (r *Runner) logf(format string, args ...any) {
	if r.progress != nil {
		fmt.Fprintf(r.progress, format+"\n", args...)
	}
}

// Enqueue saves an initial pending state for the issue. If no triage is currently
// in_progress, it starts the first stage immediately; otherwise it queues for serial pickup.
func (r *Runner) Enqueue(issue int, issueTitle, issueBody string) error {
	firstStage := r.FirstStageID()
	if firstStage == "" {
		return fmt.Errorf("triage config has no stages")
	}

	st := &TriageState{
		Issue:        issue,
		Repo:         r.cfg.Triage.Repo,
		RepoRoot:     r.repoRoot,
		CurrentStage: firstStage,
		Status:       "pending",
		StageHistory: []TriageStageHistoryEntry{},
	}
	if err := r.store.Save(st); err != nil {
		return fmt.Errorf("save initial state for issue %d: %w", issue, err)
	}

	// Only start immediately if no other triage is in_progress (serial execution).
	active, err := r.store.List("in_progress")
	if err != nil {
		return fmt.Errorf("check active triage: %w", err)
	}
	if len(active) > 0 {
		r.logf("triage issue %d: queued (issue %d in progress)", issue, active[0].Issue)
		return nil
	}

	stageCfg := r.cfg.StageByID(firstStage)
	if stageCfg != nil && stageCfg.Mode == "print" {
		// Print-mode: run synchronously inline (no tmux session needed).
		r.runPrintStage(st)
		return nil
	}

	if err := r.startStage(issue, firstStage, issueTitle, issueBody); err != nil {
		return fmt.Errorf("start first stage for issue %d: %w", issue, err)
	}

	return nil
}

// acquireAdvanceLock creates an exclusive lock file in baseDir to prevent two
// concurrent Advance() calls from processing the same stages simultaneously.
// Returns a release function and nil on success, or an error if the lock is
// already held. Stale lock files (> 30 min old) are removed automatically.
func acquireAdvanceLock(baseDir string) (release func(), err error) {
	lockPath := filepath.Join(baseDir, ".advance.lock")

	// Remove stale locks (e.g. from a crash).
	if info, statErr := os.Stat(lockPath); statErr == nil {
		if time.Since(info.ModTime()) > 30*time.Minute {
			_ = os.Remove(lockPath)
		}
	}

	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		if os.IsExist(err) {
			return nil, fmt.Errorf("advance lock already held")
		}
		return nil, fmt.Errorf("acquire advance lock: %w", err)
	}
	f.Close()

	return func() { os.Remove(lockPath) }, nil
}

// Advance processes at most one triage pipeline per call for async stages.
// Print-mode stages chain and run to completion within a single call.
func (r *Runner) Advance() ([]TriageAction, error) {
	release, err := acquireAdvanceLock(r.store.BaseDir())
	if err != nil {
		r.logf("advance: %v — skipping this check-in", err)
		return nil, nil
	}
	defer release()

	var actions []TriageAction

	for {
		// Check for an in_progress pipeline.
		active, err := r.store.List("in_progress")
		if err != nil {
			return actions, fmt.Errorf("list in_progress triage states: %w", err)
		}

		if len(active) > 0 {
			st := &active[0]
			stageCfg := r.cfg.StageByID(st.CurrentStage)

			// Print-mode stage with no active session: run synchronously.
			if stageCfg != nil && stageCfg.Mode == "print" && st.CurrentSession == "" {
				action := r.runPrintStage(st)
				actions = append(actions, action)
				if action.Action == "error" || action.Action == "completed" {
					return actions, nil
				}
				// Loop: the next stage might also be print-mode.
				continue
			}

			// Async stage: advance as normal (one stage per call).
			action := r.advanceOne(st)
			return append(actions, action), nil
		}

		// No in_progress pipeline — start the next pending one.
		pending, err := r.store.List("pending")
		if err != nil {
			return actions, fmt.Errorf("list pending triage states: %w", err)
		}
		if len(pending) == 0 {
			return actions, nil
		}

		next := &pending[0]
		stageCfg := r.cfg.StageByID(next.CurrentStage)

		// Print-mode first stage: run immediately without creating a session.
		if stageCfg != nil && stageCfg.Mode == "print" {
			action := r.runPrintStage(next)
			actions = append(actions, action)
			if action.Action == "error" || action.Action == "completed" {
				return actions, nil
			}
			// Loop: next stage might also be print-mode.
			continue
		}

		// Async first stage: fetch issue and start session.
		issueData, err := r.gh.GetIssue(next.Issue)
		if err != nil {
			return append(actions, TriageAction{Issue: next.Issue, Stage: next.CurrentStage, Action: "error", Message: fmt.Sprintf("fetch issue: %v", err)}), nil
		}
		if err := r.startStage(next.Issue, next.CurrentStage, issueData.Title, issueData.Body); err != nil {
			return append(actions, TriageAction{Issue: next.Issue, Stage: next.CurrentStage, Action: "error", Message: fmt.Sprintf("start stage: %v", err)}), nil
		}
		r.logf("triage issue %d: started from queue", next.Issue)
		return append(actions, TriageAction{Issue: next.Issue, Stage: next.CurrentStage, Action: "started", Message: "dequeued from pending"}), nil
	}
}

// advanceOne checks a single in_progress triage state and advances it if ready.
func (r *Runner) advanceOne(st *TriageState) TriageAction {
	base := TriageAction{Issue: st.Issue, Stage: st.CurrentStage}

	// 1. If no current session, nothing to do.
	if st.CurrentSession == "" {
		r.logf("triage issue %d stage %s: skip (no current session)", st.Issue, st.CurrentStage)
		base.Action = "skip"
		base.Message = "no current session"
		return base
	}

	// 2. Get session state from DB.
	sessionState, err := r.db.GetSessionState(st.CurrentSession)
	if err != nil || sessionState == nil {
		r.logf("triage issue %d stage %s: skip (session state unavailable)", st.Issue, st.CurrentStage)
		base.Action = "skip"
		base.Message = "session state unavailable"
		return base
	}

	// 3. If not idle or exited, the agent is still working.
	if sessionState.Event != "idle" && sessionState.Event != "exited" {
		r.logf("triage issue %d stage %s: skip (session %s is %s)", st.Issue, st.CurrentStage, st.CurrentSession, sessionState.Event)
		base.Action = "skip"
		base.Message = fmt.Sprintf("session is %s", sessionState.Event)
		return base
	}

	// 4. Read the outcome file written by the agent.
	outcome, err := r.store.ReadOutcome(st.Issue, st.CurrentStage)
	if err != nil {
		r.logf("triage issue %d stage %s: error reading outcome: %v", st.Issue, st.CurrentStage, err)
		base.Action = "error"
		base.Message = fmt.Sprintf("read outcome: %v", err)
		return base
	}

	// 5. Look up the outcome routing in the current stage config.
	stageCfg := r.cfg.StageByID(st.CurrentStage)

	nextStageID := ""
	if stageCfg != nil {
		nextStageID = stageCfg.Outcomes[outcome.Outcome]
	}

	var stageDuration string
	if st.StartedAt != "" {
		if started, err := time.Parse(time.RFC3339, st.StartedAt); err == nil {
			stageDuration = time.Since(started).Round(time.Second).String()
		}
	}

	historyEntry := TriageStageHistoryEntry{
		Stage:    st.CurrentStage,
		Outcome:  outcome.Outcome,
		Summary:  outcome.Summary,
		Duration: stageDuration,
	}

	// "done" or empty means the triage pipeline is complete for this issue.
	if nextStageID == "" || nextStageID == "done" {
		if err := r.store.Update(st.Issue, func(s *TriageState) {
			s.StageHistory = append(s.StageHistory, historyEntry)
			s.Status = "completed"
			s.CurrentSession = ""
			s.StartedAt = ""
		}); err != nil {
			base.Action = "error"
			base.Message = fmt.Sprintf("mark completed: %v", err)
			return base
		}
		r.logf("triage issue %d stage %s: completed (outcome=%s)", st.Issue, st.CurrentStage, outcome.Outcome)
		base.Action = "completed"
		base.Message = fmt.Sprintf("outcome=%s", outcome.Outcome)
		return base
	}

	// 6. Kill the old session before starting the next stage.
	if st.CurrentSession != "" {
		if _, err := r.sessions.Kill(st.CurrentSession); err != nil {
			r.logf("triage issue %d: warning: kill old session %s: %v", st.Issue, st.CurrentSession, err)
		}
	}

	// Update history and advance to next stage.
	if err := r.store.Update(st.Issue, func(s *TriageState) {
		s.StageHistory = append(s.StageHistory, historyEntry)
		s.CurrentStage = nextStageID
		s.CurrentSession = ""
		s.StartedAt = ""
	}); err != nil {
		base.Action = "error"
		base.Message = fmt.Sprintf("update history and stage: %v", err)
		return base
	}

	// 7. If the next stage is print-mode, don't start a session.
	// Advance() will pick it up and run it synchronously on the next loop iteration.
	nextStageCfg := r.cfg.StageByID(nextStageID)
	if nextStageCfg != nil && nextStageCfg.Mode == "print" {
		r.logf("triage issue %d: queued print stage %s (outcome=%s)", st.Issue, nextStageID, outcome.Outcome)
		base.Action = "advance"
		base.Message = fmt.Sprintf("→ %s (outcome=%s)", nextStageID, outcome.Outcome)
		return base
	}

	var issueTitle, issueBody string
	if r.gh != nil {
		ghIssue, err := r.gh.GetIssue(st.Issue)
		if err != nil {
			r.logf("triage issue %d: error fetching issue: %v", st.Issue, err)
			base.Action = "error"
			base.Message = fmt.Sprintf("fetch issue: %v", err)
			return base
		}
		issueTitle = ghIssue.Title
		issueBody = ghIssue.Body
	}

	if err := r.startStage(st.Issue, nextStageID, issueTitle, issueBody); err != nil {
		r.logf("triage issue %d: error starting stage %s: %v", st.Issue, nextStageID, err)
		base.Action = "error"
		base.Message = fmt.Sprintf("start stage %s: %v", nextStageID, err)
		return base
	}

	r.logf("triage issue %d: advanced to stage %s (outcome=%s)", st.Issue, nextStageID, outcome.Outcome)
	base.Action = "advance"
	base.Message = fmt.Sprintf("→ %s (outcome=%s)", nextStageID, outcome.Outcome)
	return base
}

// startStage creates a tmux session for the given issue/stage, sends the prompt,
// and updates the triage state to in_progress.
func (r *Runner) startStage(issue int, stageID, title, body string) error {
	// 1. Ensure the outcome directory exists.
	if err := r.store.EnsureOutcomeDir(issue); err != nil {
		return fmt.Errorf("ensure outcome dir: %w", err)
	}

	outPath := r.store.OutcomePath(issue, stageID)

	// 2. Render the prompt from template.
	stageCfg := r.cfg.StageByID(stageID)
	if stageCfg == nil {
		return fmt.Errorf("stage %q not found in config", stageID)
	}
	prompt, err := r.renderPrompt(issue, stageCfg, title, body, outPath)
	if err != nil {
		return fmt.Errorf("render prompt for stage %s: %w", stageID, err)
	}

	// 3. Create the tmux session.
	sessionName := fmt.Sprintf("triage-%d-%s", issue, stageID)
	opts := session.CreateOpts{
		Name:        sessionName,
		Workdir:     r.repoRoot,
		Flags:       "--dangerously-skip-permissions",
		Model:       stageCfg.Model,
		Issue:       issue,
		Stage:       stageID,
		Interactive: true,
	}
	if err := r.sessions.Create(opts); err != nil {
		return fmt.Errorf("create session %s: %w", sessionName, err)
	}

	// 4. Wait for Claude Code to boot before sending the prompt.
	if r.bootWait > 0 {
		time.Sleep(r.bootWait)
	}

	// 5. Send the prompt.
	if err := r.sessions.Send(sessionName, prompt); err != nil {
		_, _ = r.sessions.Kill(sessionName)
		return fmt.Errorf("send prompt to session %s: %w", sessionName, err)
	}

	// 6. Update triage state to in_progress with the new session name.
	if err := r.store.Update(issue, func(st *TriageState) {
		st.Status = "in_progress"
		st.CurrentStage = stageID
		st.CurrentSession = sessionName
		st.StartedAt = time.Now().UTC().Format(time.RFC3339)
	}); err != nil {
		return fmt.Errorf("update triage state for issue %d: %w", issue, err)
	}

	r.logf("triage issue %d: started stage %s in session %s", issue, stageID, sessionName)
	return nil
}

// runPrintStage executes a print-mode stage synchronously using claude --print.
// It renders the prompt, runs the command, parses stdout as the outcome JSON,
// writes the outcome file for audit, applies any configured GitHub label, and
// advances (or completes) the triage state — all in one call.
func (r *Runner) runPrintStage(st *TriageState) TriageAction {
	base := TriageAction{Issue: st.Issue, Stage: st.CurrentStage}

	stageCfg := r.cfg.StageByID(st.CurrentStage)
	if stageCfg == nil {
		base.Action = "error"
		base.Message = fmt.Sprintf("stage %q not found in config", st.CurrentStage)
		return base
	}

	// Record when this stage started.
	stageStart := time.Now().UTC()

	// Mark in_progress with a fresh StartedAt for this stage.
	if err := r.store.Update(st.Issue, func(s *TriageState) {
		s.Status = "in_progress"
		s.CurrentSession = ""
		s.StartedAt = stageStart.Format(time.RFC3339)
	}); err != nil {
		base.Action = "error"
		base.Message = fmt.Sprintf("mark in_progress: %v", err)
		return base
	}

	// Fetch issue data for template rendering.
	var issueTitle, issueBody string
	if r.gh != nil {
		ghIssue, err := r.gh.GetIssue(st.Issue)
		if err != nil {
			base.Action = "error"
			base.Message = fmt.Sprintf("fetch issue: %v", err)
			return base
		}
		issueTitle = ghIssue.Title
		issueBody = ghIssue.Body
	}

	// Ensure outcome directory and compute output path.
	if err := r.store.EnsureOutcomeDir(st.Issue); err != nil {
		base.Action = "error"
		base.Message = fmt.Sprintf("ensure outcome dir: %v", err)
		return base
	}
	outPath := r.store.OutcomePath(st.Issue, st.CurrentStage)

	// Render the prompt template.
	prompt, err := r.renderPrompt(st.Issue, stageCfg, issueTitle, issueBody, outPath)
	if err != nil {
		base.Action = "error"
		base.Message = fmt.Sprintf("render prompt: %v", err)
		return base
	}

	r.logf("triage issue %d: running print stage %s", st.Issue, st.CurrentStage)

	// Execute claude --print.
	stdout, err := r.printExec(stageCfg, prompt)
	if err != nil {
		base.Action = "error"
		base.Message = fmt.Sprintf("exec print: %v", err)
		return base
	}

	// Parse the outcome from stdout.
	outcome, err := parseOutcomeFromOutput(stdout)
	if err != nil {
		base.Action = "error"
		base.Message = fmt.Sprintf("parse outcome: %v (output: %.200s)", err, stdout)
		return base
	}

	// Write outcome file for audit trail.
	if data, jsonErr := json.Marshal(outcome); jsonErr == nil {
		_ = os.WriteFile(outPath, data, 0o644)
	}

	// Apply GitHub label if configured and outcome is affirmative.
	if stageCfg.Label != "" && outcome.Outcome == "yes" {
		if labelErr := r.labelExec(st.Repo, st.Issue, stageCfg.Label); labelErr != nil {
			r.logf("triage issue %d stage %s: warning: apply label %q: %v", st.Issue, st.CurrentStage, stageCfg.Label, labelErr)
		}
	}

	// Determine next stage and compute duration.
	nextStageID := stageCfg.Outcomes[outcome.Outcome]
	stageDuration := time.Since(stageStart).Round(time.Second).String()

	historyEntry := TriageStageHistoryEntry{
		Stage:    st.CurrentStage,
		Outcome:  outcome.Outcome,
		Summary:  outcome.Summary,
		Duration: stageDuration,
	}

	if nextStageID == "" || nextStageID == "done" {
		if err := r.store.Update(st.Issue, func(s *TriageState) {
			s.StageHistory = append(s.StageHistory, historyEntry)
			s.Status = "completed"
			s.CurrentSession = ""
			s.StartedAt = ""
		}); err != nil {
			base.Action = "error"
			base.Message = fmt.Sprintf("mark completed: %v", err)
			return base
		}
		r.logf("triage issue %d stage %s: completed (outcome=%s)", st.Issue, st.CurrentStage, outcome.Outcome)
		base.Action = "completed"
		base.Message = fmt.Sprintf("outcome=%s", outcome.Outcome)
		return base
	}

	if err := r.store.Update(st.Issue, func(s *TriageState) {
		s.StageHistory = append(s.StageHistory, historyEntry)
		s.CurrentStage = nextStageID
		s.CurrentSession = ""
		s.StartedAt = ""
	}); err != nil {
		base.Action = "error"
		base.Message = fmt.Sprintf("advance state: %v", err)
		return base
	}

	r.logf("triage issue %d: advanced to stage %s (outcome=%s)", st.Issue, nextStageID, outcome.Outcome)
	base.Action = "advance"
	base.Message = fmt.Sprintf("→ %s (outcome=%s)", nextStageID, outcome.Outcome)
	return base
}

// renderPrompt renders the prompt template for the given issue and stage.
// It checks stageCfg.PromptTemplate first, then a repo-local override at
// {repoRoot}/triage/{stageID}.md, then falls back to the embedded default templates.
func (r *Runner) renderPrompt(issue int, stageCfg *TriageStage, title, body, outcomePath string) (string, error) {
	var overridePath string
	if stageCfg != nil && stageCfg.PromptTemplate != "" {
		overridePath = filepath.Join(r.repoRoot, stageCfg.PromptTemplate)
	} else {
		overridePath = filepath.Join(r.repoRoot, "triage", stageCfg.ID+".md")
	}

	var tmplSrc string
	if data, err := os.ReadFile(overridePath); err == nil {
		tmplSrc = string(data)
	} else if def, ok := defaultTemplates[stageCfg.ID]; ok {
		tmplSrc = def
	} else {
		return "", fmt.Errorf("no template found for stage %q", stageCfg.ID)
	}

	tmpl, err := template.New(stageCfg.ID).Option("missingkey=error").Parse(tmplSrc)
	if err != nil {
		return "", fmt.Errorf("parse template for stage %q: %w", stageCfg.ID, err)
	}

	vars := map[string]any{
		"issue_number": issue,
		"issue_title":  title,
		"issue_body":   body,
		"repo_root":    r.repoRoot,
		"outcome_file": outcomePath,
		"stage_id":     stageCfg.ID,
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, vars); err != nil {
		return "", fmt.Errorf("execute template for stage %q: %w", stageCfg.ID, err)
	}

	return buf.String(), nil
}

// parseOutcomeFromOutput extracts a TriageOutcome from claude --print stdout.
// It tries the full trimmed output first, then scans lines in reverse for the
// last line that parses as valid JSON with a non-empty "outcome" field.
func parseOutcomeFromOutput(output string) (*TriageOutcome, error) {
	trimmed := strings.TrimSpace(output)

	// Fast path: entire output is clean JSON.
	var outcome TriageOutcome
	if err := json.Unmarshal([]byte(trimmed), &outcome); err == nil && outcome.Outcome != "" {
		return &outcome, nil
	}

	// Scan lines in reverse for the last valid outcome JSON.
	lines := strings.Split(trimmed, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		var o TriageOutcome
		if err := json.Unmarshal([]byte(line), &o); err == nil && o.Outcome != "" {
			return &o, nil
		}
	}

	return nil, fmt.Errorf("no valid outcome JSON found in output (%.200s)", trimmed)
}

// defaultExecPrint is the real implementation of printExec: runs claude --print
// as a subprocess with the prompt as a positional argument and returns stdout.
func (r *Runner) defaultExecPrint(stageCfg *TriageStage, prompt string) (string, error) {
	timeout := 15 * time.Minute
	if stageCfg.Timeout != "" {
		if d, err := time.ParseDuration(stageCfg.Timeout); err == nil {
			timeout = d
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// Build args: [--model <m>] --dangerously-skip-permissions --print <prompt>
	args := []string{"--dangerously-skip-permissions", "--print", prompt}
	if stageCfg.Model != "" {
		args = append([]string{"--model", stageCfg.Model}, args...)
	}

	cmd := exec.CommandContext(ctx, "claude", args...)
	cmd.Env = buildClaudeEnv()

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("claude --print: %w (stderr: %s)", err, stderr.String())
	}

	return stdout.String(), nil
}

// buildClaudeEnv returns an environment for running claude as a subprocess:
// CLAUDECODE is removed (allows nested sessions) and CLAUDE_CODE_OAUTH_TOKEN
// is injected from ~/.factory/.env if not already set.
func buildClaudeEnv() []string {
	var env []string
	for _, e := range os.Environ() {
		if strings.HasPrefix(e, "CLAUDECODE=") {
			continue // must unset to allow nested claude sessions
		}
		env = append(env, e)
	}
	// Inject OAuth token from .env file if not already in environment.
	if os.Getenv("CLAUDE_CODE_OAUTH_TOKEN") == "" {
		if token := readFactoryEnvToken(); token != "" {
			env = append(env, "CLAUDE_CODE_OAUTH_TOKEN="+token)
		}
	}
	return env
}

// readFactoryEnvToken reads CLAUDE_CODE_OAUTH_TOKEN from ~/.factory/.env.
func readFactoryEnvToken() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	f, err := os.Open(filepath.Join(home, ".factory", ".env"))
	if err != nil {
		return ""
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		if k, v, ok := strings.Cut(line, "="); ok && strings.TrimSpace(k) == "CLAUDE_CODE_OAUTH_TOKEN" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// defaultApplyLabel is the real implementation of labelExec: ensures the label
// exists (creating it if needed) then applies it to the issue.
func (r *Runner) defaultApplyLabel(repo string, issue int, label string) error {
	// Idempotently create the label so gh issue edit doesn't fail.
	create := exec.Command("gh", "label", "create", label,
		"--repo", repo,
		"--color", "#0075ca",
		"--force",
	)
	_ = create.Run() // best-effort; failure here is non-fatal

	cmd := exec.Command("gh", "issue", "edit",
		strconv.Itoa(issue),
		"--add-label", label,
		"--repo", repo,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("gh issue edit: %w (output: %s)", err, string(out))
	}
	return nil
}
