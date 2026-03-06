package pipeline

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/lucasnoah/taintfactory/internal/config"
)

// DeployStore manages deploy pipeline state on disk.
type DeployStore struct {
	baseDir string
}

// NewDeployStore creates a DeployStore rooted at baseDir.
func NewDeployStore(baseDir string) *DeployStore {
	return &DeployStore{baseDir: baseDir}
}

// DefaultDeployStore returns a DeployStore at {datadir}/deploys.
func DefaultDeployStore() (*DeployStore, error) {
	dir := filepath.Join(config.DataDir(), "deploys")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir %s: %w", dir, err)
	}
	return &DeployStore{baseDir: dir}, nil
}

// Create initialises a new deploy pipeline on disk.
func (s *DeployStore) Create(opts DeployCreateOpts) (*DeployState, error) {
	dir := filepath.Join(s.baseDir, opts.CommitSHA)
	if _, err := os.Stat(dir); err == nil {
		return nil, fmt.Errorf("deploy %s already exists", opts.CommitSHA)
	}
	if err := os.MkdirAll(filepath.Join(dir, "stages"), 0o755); err != nil {
		return nil, fmt.Errorf("mkdir stages: %w", err)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	ds := &DeployState{
		CommitSHA:      opts.CommitSHA,
		Namespace:      opts.Namespace,
		CurrentStage:   opts.FirstStage,
		CurrentAttempt: 1,
		StageHistory:   []StageHistoryEntry{},
		Status:         "pending",
		PreviousSHA:    opts.PreviousSHA,
		CreatedAt:      now,
		UpdatedAt:      now,
		ConfigPath:     opts.ConfigPath,
		RepoDir:        opts.RepoDir,
	}
	path := filepath.Join(dir, "deploy.json")
	if err := WriteJSON(path, ds); err != nil {
		return nil, fmt.Errorf("write deploy.json: %w", err)
	}
	return ds, nil
}

// Get reads the deploy state for a commit SHA.
func (s *DeployStore) Get(sha string) (*DeployState, error) {
	path := filepath.Join(s.baseDir, sha, "deploy.json")
	var ds DeployState
	if err := ReadJSON(path, &ds); err != nil {
		return nil, fmt.Errorf("deploy %s not found", sha)
	}
	return &ds, nil
}

// Update performs an atomic read-modify-write of the deploy state.
func (s *DeployStore) Update(sha string, fn func(*DeployState)) error {
	ds, err := s.Get(sha)
	if err != nil {
		return err
	}
	fn(ds)
	ds.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	path := filepath.Join(s.baseDir, sha, "deploy.json")
	return WriteJSON(path, ds)
}

// List returns all deploys, optionally filtered by status.
func (s *DeployStore) List(statusFilter string) ([]DeployState, error) {
	if _, err := os.Stat(s.baseDir); os.IsNotExist(err) {
		return nil, nil
	}
	entries, err := os.ReadDir(s.baseDir)
	if err != nil {
		return nil, fmt.Errorf("read deploys dir: %w", err)
	}

	var deploys []DeployState
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		path := filepath.Join(s.baseDir, e.Name(), "deploy.json")
		var ds DeployState
		if err := ReadJSON(path, &ds); err != nil {
			continue
		}
		if statusFilter == "" || ds.Status == statusFilter {
			deploys = append(deploys, ds)
		}
	}

	sort.Slice(deploys, func(i, j int) bool {
		return deploys[i].CreatedAt > deploys[j].CreatedAt
	})
	return deploys, nil
}

// StageAttemptDir returns the directory for a deploy stage attempt.
func (s *DeployStore) StageAttemptDir(sha, stage string, attempt int) string {
	return filepath.Join(s.baseDir, sha, "stages", stage, fmt.Sprintf("attempt-%d", attempt))
}

// InitStageAttempt creates the directory structure for a deploy stage attempt.
func (s *DeployStore) InitStageAttempt(sha, stage string, attempt int) error {
	dir := s.StageAttemptDir(sha, stage, attempt)
	return os.MkdirAll(dir, 0o755)
}

// SavePrompt writes the prompt markdown for a deploy stage attempt.
func (s *DeployStore) SavePrompt(sha, stage string, attempt int, prompt string) error {
	dir := s.StageAttemptDir(sha, stage, attempt)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir attempt dir: %w", err)
	}
	return WriteAtomic(filepath.Join(dir, "prompt.md"), []byte(prompt))
}
