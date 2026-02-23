package triage

import (
	"bytes"
	_ "embed"
	"fmt"
	"io"
	"os"
	"path/filepath"
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
	Action  string // "skip", "advance", "completed", "error"
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
}

// NewRunner creates a new Runner. bootWait defaults to 15s.
func NewRunner(cfg *TriageConfig, store *Store, database *db.DB, sessions *session.Manager, gh GHClient, repoRoot string) *Runner {
	return &Runner{
		cfg:      cfg,
		store:    store,
		db:       database,
		sessions: sessions,
		gh:       gh,
		repoRoot: repoRoot,
		bootWait: 15 * time.Second,
	}
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

// Enqueue saves an initial pending→in_progress state for the issue and starts the first stage.
func (r *Runner) Enqueue(issue int, issueTitle, issueBody string) error {
	firstStage := r.FirstStageID()
	if firstStage == "" {
		return fmt.Errorf("triage config has no stages")
	}

	st := &TriageState{
		Issue:        issue,
		Repo:         r.cfg.Triage.Repo,
		CurrentStage: firstStage,
		Status:       "pending",
		StageHistory: []TriageStageHistoryEntry{},
	}
	if err := r.store.Save(st); err != nil {
		return fmt.Errorf("save initial state for issue %d: %w", issue, err)
	}

	if err := r.startStage(issue, firstStage, issueTitle, issueBody); err != nil {
		return fmt.Errorf("start first stage for issue %d: %w", issue, err)
	}

	return nil
}

// Advance lists all in_progress triage states and advances each one.
func (r *Runner) Advance() ([]TriageAction, error) {
	states, err := r.store.List("in_progress")
	if err != nil {
		return nil, fmt.Errorf("list in_progress triage states: %w", err)
	}

	var actions []TriageAction
	for i := range states {
		action := r.advanceOne(&states[i])
		actions = append(actions, action)
	}
	return actions, nil
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

	historyEntry := TriageStageHistoryEntry{
		Stage:   st.CurrentStage,
		Outcome: outcome.Outcome,
		Summary: outcome.Summary,
	}

	// "done" or empty means the triage pipeline is complete for this issue.
	if nextStageID == "" || nextStageID == "done" {
		if err := r.store.Update(st.Issue, func(s *TriageState) {
			s.StageHistory = append(s.StageHistory, historyEntry)
			s.Status = "completed"
			s.CurrentSession = ""
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

	// 6. Route to next stage — append history + advance stage in one update,
	// then fetch fresh issue data and start the next stage.
	if err := r.store.Update(st.Issue, func(s *TriageState) {
		s.StageHistory = append(s.StageHistory, historyEntry)
		s.CurrentStage = nextStageID
	}); err != nil {
		base.Action = "error"
		base.Message = fmt.Sprintf("update history and stage: %v", err)
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
	prompt, err := r.renderPrompt(issue, stageID, title, body, outPath)
	if err != nil {
		return fmt.Errorf("render prompt for stage %s: %w", stageID, err)
	}

	// 3. Create the tmux session.
	sessionName := fmt.Sprintf("triage-%d-%s", issue, stageID)
	opts := session.CreateOpts{
		Name:    sessionName,
		Workdir: r.repoRoot,
		Flags:   "--dangerously-skip-permissions",
		Issue:   issue,
		Stage:   stageID,
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
	}); err != nil {
		return fmt.Errorf("update triage state for issue %d: %w", issue, err)
	}

	r.logf("triage issue %d: started stage %s in session %s", issue, stageID, sessionName)
	return nil
}

// renderPrompt renders the prompt template for the given issue and stage.
// It first checks for a repo-local override at {repoRoot}/triage/{stageID}.md,
// then falls back to the embedded default templates.
func (r *Runner) renderPrompt(issue int, stageID, title, body, outcomePath string) (string, error) {
	var tmplSrc string

	// Check for repo-local override.
	overridePath := filepath.Join(r.repoRoot, "triage", stageID+".md")
	if data, err := os.ReadFile(overridePath); err == nil {
		tmplSrc = string(data)
	} else if src, ok := defaultTemplates[stageID]; ok {
		tmplSrc = src
	} else {
		return "", fmt.Errorf("no prompt template found for stage %q (checked %s and embedded defaults)", stageID, overridePath)
	}

	tmpl, err := template.New(stageID).Option("missingkey=error").Parse(tmplSrc)
	if err != nil {
		return "", fmt.Errorf("parse template for stage %q: %w", stageID, err)
	}

	vars := map[string]any{
		"issue_number": issue,
		"issue_title":  title,
		"issue_body":   body,
		"outcome_file": outcomePath,
		"repo_root":    r.repoRoot,
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, vars); err != nil {
		return "", fmt.Errorf("execute template for stage %q: %w", stageID, err)
	}

	return buf.String(), nil
}
