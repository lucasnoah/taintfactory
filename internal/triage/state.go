package triage

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/lucasnoah/taintfactory/internal/pipeline"
)

// TriageState is the persisted state for a single issue's triage pipeline.
type TriageState struct {
	Issue          int                       `json:"issue"`
	Repo           string                    `json:"repo"`
	RepoRoot       string                    `json:"repo_root,omitempty"` // local filesystem path to repo root
	CurrentStage   string                    `json:"current_stage"`
	Status         string                    `json:"status"` // pending, in_progress, completed
	CurrentSession string                    `json:"current_session,omitempty"`
	StageHistory   []TriageStageHistoryEntry `json:"stage_history"`
	UpdatedAt      string                    `json:"updated_at,omitempty"`
}

// TriageStageHistoryEntry records the outcome of one completed stage.
type TriageStageHistoryEntry struct {
	Stage    string `json:"stage"`
	Outcome  string `json:"outcome"`
	Summary  string `json:"summary,omitempty"`
	Duration string `json:"duration,omitempty"`
}

// TriageOutcome is the JSON file the agent writes as its final act.
type TriageOutcome struct {
	Outcome string `json:"outcome"`
	Summary string `json:"summary,omitempty"`
}

// Store manages triage state files on disk under a single base directory.
// Typically baseDir = ~/.factory/triage/{repo_slug}
type Store struct {
	baseDir string
}

// NewStore creates a Store rooted at baseDir.
func NewStore(baseDir string) *Store {
	return &Store{baseDir: baseDir}
}

// DefaultTriageDir returns the base directory for triage state (~/.factory/triage).
func DefaultTriageDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("home dir: %w", err)
	}
	return filepath.Join(home, ".factory", "triage"), nil
}

// DefaultStore returns a Store at ~/.factory/triage/{repoSlug}, creating the directory if needed.
func DefaultStore(repoSlug string) (*Store, error) {
	base, err := DefaultTriageDir()
	if err != nil {
		return nil, fmt.Errorf("get home dir: %w", err)
	}
	dir := filepath.Join(base, repoSlug)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir %s: %w", dir, err)
	}
	return &Store{baseDir: dir}, nil
}

// statePath returns the path to the state JSON file for an issue.
func (s *Store) statePath(issue int) string {
	return filepath.Join(s.baseDir, strconv.Itoa(issue)+".json")
}

// OutcomePath returns the path to the agent outcome file for a given issue and stage.
func (s *Store) OutcomePath(issue int, stageID string) string {
	return filepath.Join(s.baseDir, strconv.Itoa(issue), stageID+".outcome.json")
}

// EnsureOutcomeDir creates the per-issue subdirectory used for outcome files.
func (s *Store) EnsureOutcomeDir(issue int) error {
	dir := filepath.Join(s.baseDir, strconv.Itoa(issue))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}
	return nil
}

// Save marshals state and writes it to disk atomically, creating directories as needed.
func (s *Store) Save(state *TriageState) error {
	if err := os.MkdirAll(s.baseDir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", s.baseDir, err)
	}
	if err := pipeline.WriteJSON(s.statePath(state.Issue), state); err != nil {
		return fmt.Errorf("write state: %w", err)
	}
	return nil
}

// Get reads and unmarshals the state for the given issue.
func (s *Store) Get(issue int) (*TriageState, error) {
	data, err := os.ReadFile(s.statePath(issue))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("triage state %d not found", issue)
		}
		return nil, fmt.Errorf("read state %d: %w", issue, err)
	}
	var st TriageState
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, fmt.Errorf("unmarshal state %d: %w", issue, err)
	}
	return &st, nil
}

// Update performs a read-modify-write of the triage state, setting UpdatedAt.
func (s *Store) Update(issue int, fn func(*TriageState)) error {
	st, err := s.Get(issue)
	if err != nil {
		return err
	}
	fn(st)
	st.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	return s.Save(st)
}

// List returns all triage states, optionally filtered by status.
// Pass "" for statusFilter to return all states. Results are sorted by issue number.
func (s *Store) List(statusFilter string) ([]TriageState, error) {
	entries, err := os.ReadDir(s.baseDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read dir %s: %w", s.baseDir, err)
	}

	var states []TriageState
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		// Only process files matching the pattern "<number>.json"
		if !strings.HasSuffix(name, ".json") {
			continue
		}
		stem := strings.TrimSuffix(name, ".json")
		issue, err := strconv.Atoi(stem)
		if err != nil {
			continue // skip non-numeric filenames
		}
		st, err := s.Get(issue)
		if err != nil {
			continue // skip broken entries
		}
		if statusFilter == "" || st.Status == statusFilter {
			states = append(states, *st)
		}
	}

	sort.Slice(states, func(i, j int) bool {
		return states[i].Issue < states[j].Issue
	})
	return states, nil
}

// ReadOutcome reads the agent outcome file for the given issue and stage.
func (s *Store) ReadOutcome(issue int, stageID string) (*TriageOutcome, error) {
	data, err := os.ReadFile(s.OutcomePath(issue, stageID))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("outcome for issue %d stage %q not found", issue, stageID)
		}
		return nil, fmt.Errorf("read outcome: %w", err)
	}
	var outcome TriageOutcome
	if err := json.Unmarshal(data, &outcome); err != nil {
		return nil, fmt.Errorf("unmarshal outcome: %w", err)
	}
	return &outcome, nil
}
