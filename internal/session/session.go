package session

import (
	"fmt"
	"strings"
	"time"

	"github.com/lucasnoah/taintfactory/internal/db"
	"github.com/lucasnoah/taintfactory/internal/pipeline"
)

// CreateOpts holds parameters for creating a new session.
type CreateOpts struct {
	Name        string
	Workdir     string
	Flags       string
	Issue       int
	Stage       string
	Interactive bool
}

// SessionInfo represents a session in the list output.
type SessionInfo struct {
	Name      string
	Issue     int
	Stage     string
	State     string // latest DB event
	StartedAt string
	Elapsed   string
	Orphan    string // "" | "no-db" | "no-tmux"
}

// Manager handles session lifecycle operations.
type Manager struct {
	tmux  TmuxRunner
	db    *db.DB
	store *pipeline.Store // nil if unavailable
}

// NewManager creates a Manager with the given tmux runner, database, and optional pipeline store.
func NewManager(tmux TmuxRunner, database *db.DB, store *pipeline.Store) *Manager {
	return &Manager{tmux: tmux, db: database, store: store}
}

// Create spins up a new tmux session running Claude Code.
func (m *Manager) Create(opts CreateOpts) error {
	// Check that tmux is reachable
	if _, err := m.tmux.ListSessions(); err != nil {
		return fmt.Errorf("tmux not available: %w", err)
	}

	// Check session doesn't already exist
	exists, err := m.tmux.HasSession(opts.Name)
	if err != nil {
		return fmt.Errorf("check session: %w", err)
	}
	if exists {
		return fmt.Errorf("session %q already exists", opts.Name)
	}

	// Create tmux session
	if err := m.tmux.NewSession(opts.Name); err != nil {
		return fmt.Errorf("create session: %w", err)
	}

	// Write hooks config if workdir and issue are specified
	if opts.Workdir != "" && opts.Issue > 0 {
		cfg := GenerateHooksConfig(opts.Name, opts.Issue, opts.Stage)
		if _, err := WriteHooksFile(opts.Workdir, cfg); err != nil {
			return fmt.Errorf("write hooks config: %w", err)
		}
	}

	// cd to workdir if specified
	if opts.Workdir != "" {
		if err := m.tmux.SendKeys(opts.Name, "cd "+opts.Workdir); err != nil {
			return fmt.Errorf("send cd: %w", err)
		}
	}

	// Build and send claude command
	cmd := buildClaudeCommand(opts)
	if err := m.tmux.SendKeys(opts.Name, cmd); err != nil {
		return fmt.Errorf("send claude command: %w", err)
	}

	// Log "started" event to DB
	if err := m.db.LogSessionEvent(opts.Name, opts.Issue, opts.Stage, "started", nil, ""); err != nil {
		return fmt.Errorf("log started event: %w", err)
	}

	// Update pipeline.json current_session if we have a store and issue
	if m.store != nil && opts.Issue > 0 {
		_ = m.store.Update(opts.Issue, func(ps *pipeline.PipelineState) {
			ps.CurrentSession = opts.Name
		})
	}

	return nil
}

// Kill terminates a tmux session, captures its output, and logs the exit.
func (m *Manager) Kill(name string) (string, error) {
	// Check session exists
	exists, err := m.tmux.HasSession(name)
	if err != nil {
		return "", fmt.Errorf("check session: %w", err)
	}
	if !exists {
		return "", fmt.Errorf("session %q does not exist", name)
	}

	// Capture pane buffer
	log, err := m.tmux.CapturePane(name)
	if err != nil {
		return "", fmt.Errorf("capture pane: %w", err)
	}

	// Kill tmux session
	if err := m.tmux.KillSession(name); err != nil {
		return "", fmt.Errorf("kill session: %w", err)
	}

	// Log "exited" event — look up issue/stage from DB
	state, err := m.db.GetSessionState(name)
	if err != nil {
		return log, fmt.Errorf("get session state: %w", err)
	}
	issue := 0
	stage := ""
	if state != nil {
		issue = state.Issue
		stage = state.Stage
	}
	if err := m.db.LogSessionEvent(name, issue, stage, "exited", nil, ""); err != nil {
		return log, fmt.Errorf("log exited event: %w", err)
	}

	return log, nil
}

// List returns sessions cross-referenced between the DB and live tmux.
func (m *Manager) List(issueFilter int) ([]SessionInfo, error) {
	// Get DB sessions that haven't exited
	dbSessions, err := m.db.GetAllActiveSessions()
	if err != nil {
		return nil, fmt.Errorf("get active sessions: %w", err)
	}

	// Get live tmux sessions
	tmuxNames, err := m.tmux.ListSessions()
	if err != nil {
		return nil, fmt.Errorf("list tmux sessions: %w", err)
	}
	tmuxSet := make(map[string]bool, len(tmuxNames))
	for _, n := range tmuxNames {
		tmuxSet[n] = true
	}

	// Build result from DB sessions
	seen := make(map[string]bool)
	var result []SessionInfo
	for _, se := range dbSessions {
		if issueFilter > 0 && se.Issue != issueFilter {
			continue
		}
		seen[se.SessionID] = true

		orphan := ""
		if !tmuxSet[se.SessionID] {
			orphan = "no-tmux"
		}

		result = append(result, SessionInfo{
			Name:      se.SessionID,
			Issue:     se.Issue,
			Stage:     se.Stage,
			State:     se.Event,
			StartedAt: se.Timestamp,
			Elapsed:   elapsed(se.Timestamp),
			Orphan:    orphan,
		})
	}

	// Add tmux sessions not in DB (orphans)
	for _, name := range tmuxNames {
		if seen[name] {
			continue
		}
		result = append(result, SessionInfo{
			Name:   name,
			Orphan: "no-db",
		})
	}

	return result, nil
}

// StatusInfo holds the result of a session status query.
type StatusInfo struct {
	Name             string
	Issue            int
	Stage            string
	State            string
	Timestamp        string
	Elapsed          string
	TmuxAlive        bool
	HumanIntervened  bool
}

// Status returns the current state of a session from DB + tmux.
func (m *Manager) Status(name string) (*StatusInfo, error) {
	state, err := m.db.GetSessionState(name)
	if err != nil {
		return nil, fmt.Errorf("get session state: %w", err)
	}
	if state == nil {
		return nil, fmt.Errorf("session %q not found in database", name)
	}

	alive, _ := m.tmux.HasSession(name)

	return &StatusInfo{
		Name:      name,
		Issue:     state.Issue,
		Stage:     state.Stage,
		State:     state.Event,
		Timestamp: state.Timestamp,
		Elapsed:   elapsed(state.Timestamp),
		TmuxAlive: alive,
	}, nil
}

// DetectHuman checks if a human has intervened in the session.
func (m *Manager) DetectHuman(name string) (bool, error) {
	return m.db.DetectHumanIntervention(name)
}

// buildClaudeCommand constructs the claude CLI invocation string.
func buildClaudeCommand(opts CreateOpts) string {
	parts := []string{"claude"}
	if opts.Flags != "" {
		parts = append(parts, opts.Flags)
	}
	if !opts.Interactive {
		parts = append(parts, "--print")
	}
	return strings.Join(parts, " ")
}

// elapsed computes a human-friendly duration from a timestamp to now.
func elapsed(timestamp string) string {
	t, err := time.Parse("2006-01-02 15:04:05", timestamp)
	if err != nil {
		return ""
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	default:
		return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
	}
}
