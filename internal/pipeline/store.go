package pipeline

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"
)

// Store manages pipeline state on disk.
type Store struct {
	baseDir string // defaults to ~/.factory/pipelines
}

// NewStore creates a Store rooted at baseDir.
func NewStore(baseDir string) *Store {
	return &Store{baseDir: baseDir}
}

// DefaultStore returns a Store at ~/.factory/pipelines, creating the directory if needed.
func DefaultStore() (*Store, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("get home dir: %w", err)
	}
	dir := filepath.Join(home, ".factory", "pipelines")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir %s: %w", dir, err)
	}
	return &Store{baseDir: dir}, nil
}

// BaseDir returns the store's root directory.
func (s *Store) BaseDir() string {
	return s.baseDir
}

// issueDir returns the directory for a given namespace+issue.
// If namespace is empty, uses the legacy flat path.
func (s *Store) issueDir(namespace string, issue int) string {
	if namespace != "" {
		return filepath.Join(s.baseDir, namespace, strconv.Itoa(issue))
	}
	return filepath.Join(s.baseDir, strconv.Itoa(issue))
}

// pipelinePathFor returns the path to pipeline.json using the state's Namespace.
func (s *Store) pipelinePathFor(ps *PipelineState) string {
	return filepath.Join(s.issueDir(ps.Namespace, ps.Issue), "pipeline.json")
}

// stageAttemptDir returns the directory for a specific stage attempt.
// It resolves the namespace from disk so callers do not need to pass it.
func (s *Store) stageAttemptDir(issue int, stage string, attempt int) string {
	namespace := ""
	if ps, err := s.Get(issue); err == nil {
		namespace = ps.Namespace
	}
	return filepath.Join(s.issueDir(namespace, issue), "stages", stage, fmt.Sprintf("attempt-%d", attempt))
}

// CheckOutputDir returns the directory for storing raw check output.
func (s *Store) CheckOutputDir(issue int, stage string, attempt int, checkName string) string {
	return filepath.Join(s.stageAttemptDir(issue, stage, attempt), "checks", checkName)
}

// GateResultDir returns the directory for storing gate result JSON.
func (s *Store) GateResultDir(issue int, stage string, attempt int, fixRound int) string {
	return filepath.Join(s.stageAttemptDir(issue, stage, attempt), "checks", fmt.Sprintf("post-gate-%d", fixRound))
}

// CreateOpts holds options for creating a new pipeline on disk.
type CreateOpts struct {
	Issue      int
	Title      string
	Branch     string
	Worktree   string
	FirstStage string
	GoalGates  map[string]string
	// Multi-project fields (optional; empty for legacy single-project pipelines)
	ConfigPath string
	RepoDir    string
	Namespace  string
}

// Create initialises a new pipeline on disk.
func (s *Store) Create(opts CreateOpts) (*PipelineState, error) {
	dir := s.issueDir(opts.Namespace, opts.Issue)
	if _, err := os.Stat(dir); err == nil {
		return nil, fmt.Errorf("pipeline %d already exists", opts.Issue)
	}

	if err := os.MkdirAll(filepath.Join(dir, "stages"), 0o755); err != nil {
		return nil, fmt.Errorf("mkdir stages: %w", err)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	ps := &PipelineState{
		Issue:          opts.Issue,
		Title:          opts.Title,
		Branch:         opts.Branch,
		Worktree:       opts.Worktree,
		CurrentStage:   opts.FirstStage,
		CurrentAttempt: 1,
		StageHistory:   []StageHistoryEntry{},
		GoalGates:      opts.GoalGates,
		Status:         "pending",
		CreatedAt:      now,
		UpdatedAt:      now,
		ConfigPath:     opts.ConfigPath,
		RepoDir:        opts.RepoDir,
		Namespace:      opts.Namespace,
	}

	if err := WriteJSON(s.pipelinePathFor(ps), ps); err != nil {
		return nil, fmt.Errorf("write pipeline.json: %w", err)
	}
	return ps, nil
}

// Get reads the pipeline state for an issue.
// It first tries the legacy flat path, then walks for a namespaced path.
func (s *Store) Get(issue int) (*PipelineState, error) {
	// Try legacy flat path first (fast path for existing pipelines)
	flat := filepath.Join(s.baseDir, strconv.Itoa(issue), "pipeline.json")
	var ps PipelineState
	if err := ReadJSON(flat, &ps); err == nil {
		return &ps, nil
	}

	// Walk for namespaced path: baseDir/{namespace}/{issue}/pipeline.json
	issueStr := strconv.Itoa(issue)
	var found *PipelineState
	_ = filepath.WalkDir(s.baseDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || d.Name() != "pipeline.json" {
			return nil
		}
		if filepath.Base(filepath.Dir(path)) == issueStr {
			var p PipelineState
			if readErr := ReadJSON(path, &p); readErr == nil {
				found = &p
			}
			return filepath.SkipAll
		}
		return nil
	})
	if found != nil {
		return found, nil
	}
	return nil, fmt.Errorf("pipeline %d not found", issue)
}

// Update performs an atomic read-modify-write of the pipeline state.
func (s *Store) Update(issue int, fn func(*PipelineState)) error {
	ps, err := s.Get(issue)
	if err != nil {
		return err
	}
	fn(ps)
	ps.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	return WriteJSON(s.pipelinePathFor(ps), ps)
}

// List returns all pipelines, optionally filtered by status.
// Pass "" for statusFilter to return all pipelines.
// It walks recursively to find both legacy flat and namespaced pipelines.
func (s *Store) List(statusFilter string) ([]PipelineState, error) {
	if _, err := os.Stat(s.baseDir); os.IsNotExist(err) {
		return nil, nil
	}

	var pipelines []PipelineState
	_ = filepath.WalkDir(s.baseDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || d.Name() != "pipeline.json" {
			return nil
		}
		var ps PipelineState
		if readErr := ReadJSON(path, &ps); readErr != nil {
			return nil
		}
		if statusFilter == "" || ps.Status == statusFilter {
			pipelines = append(pipelines, ps)
		}
		return nil
	})

	sort.Slice(pipelines, func(i, j int) bool {
		return pipelines[i].Issue < pipelines[j].Issue
	})
	return pipelines, nil
}

// Delete removes all data for a pipeline.
func (s *Store) Delete(issue int) error {
	ps, err := s.Get(issue)
	if err != nil {
		return fmt.Errorf("pipeline %d not found", issue)
	}
	return os.RemoveAll(s.issueDir(ps.Namespace, ps.Issue))
}

// InitStageAttempt creates the directory structure for a new stage attempt.
func (s *Store) InitStageAttempt(issue int, stage string, attempt int) error {
	if _, err := s.Get(issue); err != nil {
		return err
	}
	dir := s.stageAttemptDir(issue, stage, attempt)
	if err := os.MkdirAll(filepath.Join(dir, "checks"), 0o755); err != nil {
		return fmt.Errorf("mkdir attempt dir: %w", err)
	}
	return nil
}

// SaveStageOutcome writes the outcome JSON for a stage attempt.
func (s *Store) SaveStageOutcome(issue int, stage string, attempt int, outcome *StageOutcome) error {
	dir := s.stageAttemptDir(issue, stage, attempt)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir attempt dir: %w", err)
	}
	return WriteJSON(filepath.Join(dir, "outcome.json"), outcome)
}

// GetStageOutcome reads the outcome JSON for a stage attempt.
func (s *Store) GetStageOutcome(issue int, stage string, attempt int) (*StageOutcome, error) {
	var outcome StageOutcome
	path := filepath.Join(s.stageAttemptDir(issue, stage, attempt), "outcome.json")
	if err := ReadJSON(path, &outcome); err != nil {
		return nil, err
	}
	return &outcome, nil
}

// SaveStageSummary writes the summary JSON for a stage attempt.
func (s *Store) SaveStageSummary(issue int, stage string, attempt int, summary *StageSummary) error {
	dir := s.stageAttemptDir(issue, stage, attempt)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir attempt dir: %w", err)
	}
	return WriteJSON(filepath.Join(dir, "summary.json"), summary)
}

// GetStageSummary reads the summary JSON for a stage attempt.
func (s *Store) GetStageSummary(issue int, stage string, attempt int) (*StageSummary, error) {
	var summary StageSummary
	path := filepath.Join(s.stageAttemptDir(issue, stage, attempt), "summary.json")
	if err := ReadJSON(path, &summary); err != nil {
		return nil, err
	}
	return &summary, nil
}

// SavePrompt writes the prompt markdown for a stage attempt.
func (s *Store) SavePrompt(issue int, stage string, attempt int, prompt string) error {
	dir := s.stageAttemptDir(issue, stage, attempt)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir attempt dir: %w", err)
	}
	return WriteAtomic(filepath.Join(dir, "prompt.md"), []byte(prompt))
}

// SaveSessionLog saves the captured tmux pane output for a stage attempt.
func (s *Store) SaveSessionLog(issue int, stage string, attempt int, log string) error {
	dir := s.stageAttemptDir(issue, stage, attempt)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir attempt dir: %w", err)
	}
	return WriteAtomic(filepath.Join(dir, "session.log"), []byte(log))
}

// GetPrompt reads the prompt markdown for a stage attempt.
func (s *Store) GetPrompt(issue int, stage string, attempt int) (string, error) {
	path := filepath.Join(s.stageAttemptDir(issue, stage, attempt), "prompt.md")
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// GetSessionLog reads the session log for a stage attempt.
func (s *Store) GetSessionLog(issue int, stage string, attempt int) (string, error) {
	path := filepath.Join(s.stageAttemptDir(issue, stage, attempt), "session.log")
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(data), nil
}
