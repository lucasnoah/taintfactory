# Triage Pipeline Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Build a parallel async triage pipeline system that runs `stale_context` and `already_implemented` agent stages against GitHub issues, advanced by the same `factory orchestrator check-in` loop as dev pipelines.

**Architecture:** New `internal/triage` package owns config, state, and runner. The orchestrator's `CheckIn()` calls `triage.Runner.Advance()` after processing dev pipelines. Sessions run in the repo root (no worktrees), no check/fix loops. Agent writes `{stage_id}.outcome.json` as its final act; runner reads it on the next check-in when the session is idle.

**Tech Stack:** Go, `gopkg.in/yaml.v3`, `text/template`, `go:embed`, existing `internal/session`, `internal/db`, `internal/github` packages.

---

### Task 1: `internal/triage/config.go` — Config types and YAML loading

**Files:**
- Create: `internal/triage/config.go`
- Create: `internal/triage/config_test.go`

**Step 1: Write the failing test**

```go
// internal/triage/config_test.go
package triage

import (
	"os"
	"path/filepath"
	"testing"
)

const validTriageYAML = `
triage:
  name: "My Repo"
  repo: "owner/my-repo"

stages:
  - id: stale_context
    timeout: 10m
    outcomes:
      stale: done
      clean: already_implemented

  - id: already_implemented
    timeout: 15m
    outcomes:
      implemented: done
      not_implemented: done
`

func writeTriageConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "triage.yaml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoad_ValidConfig(t *testing.T) {
	path := writeTriageConfig(t, validTriageYAML)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.Triage.Name != "My Repo" {
		t.Errorf("Name = %q, want %q", cfg.Triage.Name, "My Repo")
	}
	if cfg.Triage.Repo != "owner/my-repo" {
		t.Errorf("Repo = %q, want %q", cfg.Triage.Repo, "owner/my-repo")
	}
	if len(cfg.Stages) != 2 {
		t.Fatalf("len(Stages) = %d, want 2", len(cfg.Stages))
	}
	if cfg.Stages[0].ID != "stale_context" {
		t.Errorf("Stages[0].ID = %q, want %q", cfg.Stages[0].ID, "stale_context")
	}
	if cfg.Stages[0].Outcomes["stale"] != "done" {
		t.Errorf("Stages[0].Outcomes[stale] = %q, want %q", cfg.Stages[0].Outcomes["stale"], "done")
	}
	if cfg.Stages[0].Outcomes["clean"] != "already_implemented" {
		t.Errorf("Stages[0].Outcomes[clean] = %q, want %q", cfg.Stages[0].Outcomes["clean"], "already_implemented")
	}
}

func TestLoad_DefaultTimeout(t *testing.T) {
	path := writeTriageConfig(t, `
triage:
  name: "Test"
  repo: "owner/test"
stages:
  - id: stale_context
    outcomes:
      stale: done
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.Stages[0].Timeout != "15m" {
		t.Errorf("default timeout = %q, want %q", cfg.Stages[0].Timeout, "15m")
	}
}

func TestLoad_MissingFile(t *testing.T) {
	_, err := Load("/nonexistent/triage.yaml")
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}

func TestLoadDefault_FindsTriageYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "triage.yaml")
	if err := os.WriteFile(path, []byte(validTriageYAML), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadDefault(dir)
	if err != nil {
		t.Fatalf("LoadDefault() error: %v", err)
	}
	if cfg.Triage.Repo != "owner/my-repo" {
		t.Errorf("Repo = %q, want %q", cfg.Triage.Repo, "owner/my-repo")
	}
}

func TestLoadDefault_NotFound(t *testing.T) {
	dir := t.TempDir() // empty dir, no triage.yaml
	_, err := LoadDefault(dir)
	if err == nil {
		t.Fatal("expected error when triage.yaml not found")
	}
}
```

**Step 2: Run test to verify it fails**

```bash
cd /Users/lucas/Documents/taintfactory && go test ./internal/triage/... 2>&1 | head -20
```
Expected: compile error — package doesn't exist yet.

**Step 3: Implement `config.go`**

```go
// internal/triage/config.go
package triage

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// TriageConfig is the top-level structure parsed from triage.yaml.
type TriageConfig struct {
	Triage TriageMeta   `yaml:"triage"`
	Stages []TriageStage `yaml:"stages"`
}

// TriageMeta holds repository-level metadata.
type TriageMeta struct {
	Name string `yaml:"name"`
	Repo string `yaml:"repo"` // e.g. "owner/repo" — used as the state directory slug
}

// TriageStage defines one stage in the triage pipeline.
type TriageStage struct {
	ID             string            `yaml:"id"`
	PromptTemplate string            `yaml:"prompt_template"` // optional override path relative to repo root
	Timeout        string            `yaml:"timeout"`
	Outcomes       map[string]string `yaml:"outcomes"` // e.g. {"stale": "done", "clean": "already_implemented"}
}

// Load reads and parses a triage config from the given YAML file path.
func Load(path string) (*TriageConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading triage config: %w", err)
	}
	var cfg TriageConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing triage YAML: %w", err)
	}
	applyDefaults(&cfg)
	return &cfg, nil
}

// LoadDefault loads triage.yaml from the given directory (typically the repo root).
func LoadDefault(repoRoot string) (*TriageConfig, error) {
	path := filepath.Join(repoRoot, "triage.yaml")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil, fmt.Errorf("triage.yaml not found in %s", repoRoot)
	}
	return Load(path)
}

// StageByID returns the stage with the given ID, or nil if not found.
func (cfg *TriageConfig) StageByID(id string) *TriageStage {
	for i := range cfg.Stages {
		if cfg.Stages[i].ID == id {
			return &cfg.Stages[i]
		}
	}
	return nil
}

// NextStageID returns the ID of the stage after the given one, or "" if it's the last.
func (cfg *TriageConfig) NextStageID(currentID string) string {
	for i, s := range cfg.Stages {
		if s.ID == currentID && i+1 < len(cfg.Stages) {
			return cfg.Stages[i+1].ID
		}
	}
	return ""
}

func applyDefaults(cfg *TriageConfig) {
	for i := range cfg.Stages {
		if cfg.Stages[i].Timeout == "" {
			cfg.Stages[i].Timeout = "15m"
		}
	}
}
```

**Step 4: Run tests**

```bash
cd /Users/lucas/Documents/taintfactory && go test ./internal/triage/... -run TestLoad -v
```
Expected: all PASS.

**Step 5: Commit**

```bash
git add internal/triage/config.go internal/triage/config_test.go
git commit -m "feat(triage): add config types and YAML loading"
```

---

### Task 2: `internal/triage/state.go` — State types and Store

**Files:**
- Create: `internal/triage/state.go`
- Create: `internal/triage/state_test.go`

**Step 1: Write the failing tests**

```go
// internal/triage/state_test.go
package triage

import (
	"testing"
	"time"
)

func TestStore_SaveAndGet(t *testing.T) {
	store := NewStore(t.TempDir())
	state := &TriageState{
		Issue:        42,
		Repo:         "owner/repo",
		CurrentStage: "stale_context",
		Status:       "in_progress",
		StageHistory: []TriageStageHistoryEntry{},
	}
	if err := store.Save(state); err != nil {
		t.Fatalf("Save() error: %v", err)
	}
	got, err := store.Get(42)
	if err != nil {
		t.Fatalf("Get() error: %v", err)
	}
	if got.Issue != 42 {
		t.Errorf("Issue = %d, want 42", got.Issue)
	}
	if got.CurrentStage != "stale_context" {
		t.Errorf("CurrentStage = %q, want %q", got.CurrentStage, "stale_context")
	}
	if got.Status != "in_progress" {
		t.Errorf("Status = %q, want %q", got.Status, "in_progress")
	}
}

func TestStore_Get_NotFound(t *testing.T) {
	store := NewStore(t.TempDir())
	_, err := store.Get(999)
	if err == nil {
		t.Fatal("expected error for missing issue, got nil")
	}
}

func TestStore_List(t *testing.T) {
	store := NewStore(t.TempDir())

	for _, s := range []struct {
		issue  int
		status string
	}{
		{1, "in_progress"},
		{2, "completed"},
		{3, "in_progress"},
	} {
		if err := store.Save(&TriageState{Issue: s.issue, Status: s.status, StageHistory: []TriageStageHistoryEntry{}}); err != nil {
			t.Fatal(err)
		}
	}

	all, err := store.List("")
	if err != nil {
		t.Fatalf("List() error: %v", err)
	}
	if len(all) != 3 {
		t.Errorf("List('') = %d items, want 3", len(all))
	}

	active, err := store.List("in_progress")
	if err != nil {
		t.Fatalf("List(in_progress) error: %v", err)
	}
	if len(active) != 2 {
		t.Errorf("List(in_progress) = %d items, want 2", len(active))
	}
}

func TestStore_Update(t *testing.T) {
	store := NewStore(t.TempDir())
	state := &TriageState{Issue: 10, Status: "pending", StageHistory: []TriageStageHistoryEntry{}}
	if err := store.Save(state); err != nil {
		t.Fatal(err)
	}

	before := time.Now()
	if err := store.Update(10, func(s *TriageState) {
		s.Status = "in_progress"
		s.CurrentStage = "stale_context"
	}); err != nil {
		t.Fatalf("Update() error: %v", err)
	}

	got, err := store.Get(10)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "in_progress" {
		t.Errorf("Status = %q, want in_progress", got.Status)
	}
	if got.UpdatedAt == "" {
		t.Error("UpdatedAt should be set after Update()")
	}
	// UpdatedAt should be after the before timestamp
	ts, err := time.Parse(time.RFC3339, got.UpdatedAt)
	if err != nil {
		t.Errorf("UpdatedAt parse error: %v", err)
	}
	if ts.Before(before) {
		t.Errorf("UpdatedAt %v is before test start %v", ts, before)
	}
}

func TestStore_OutcomePath(t *testing.T) {
	store := NewStore("/tmp/triage-test")
	path := store.OutcomePath(42, "stale_context")
	want := "/tmp/triage-test/42/stale_context.outcome.json"
	if path != want {
		t.Errorf("OutcomePath = %q, want %q", path, want)
	}
}
```

**Step 2: Run test to verify it fails**

```bash
cd /Users/lucas/Documents/taintfactory && go test ./internal/triage/... -run TestStore -v 2>&1 | head -10
```
Expected: compile error — types not defined yet.

**Step 3: Implement `state.go`**

```go
// internal/triage/state.go
package triage

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"
)

// TriageState is the persisted state for a single issue's triage pipeline.
type TriageState struct {
	Issue          int                       `json:"issue"`
	Repo           string                    `json:"repo"`
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

// DefaultStore returns a Store at ~/.factory/triage/{repoSlug}, creating the directory if needed.
// repoSlug should be e.g. "owner-repo" (slashes replaced with dashes).
func DefaultStore(repoSlug string) (*Store, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("get home dir: %w", err)
	}
	dir := filepath.Join(home, ".factory", "triage", repoSlug)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir %s: %w", dir, err)
	}
	return &Store{baseDir: dir}, nil
}

func (s *Store) statePath(issue int) string {
	return filepath.Join(s.baseDir, strconv.Itoa(issue)+".json")
}

// OutcomePath returns the path where the agent writes its outcome JSON.
func (s *Store) OutcomePath(issue int, stageID string) string {
	return filepath.Join(s.baseDir, strconv.Itoa(issue), stageID+".outcome.json")
}

// Save writes the state to disk (creates or overwrites).
func (s *Store) Save(state *TriageState) error {
	if err := os.MkdirAll(s.baseDir, 0o755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal state: %w", err)
	}
	return os.WriteFile(s.statePath(state.Issue), data, 0o644)
}

// Get reads the triage state for an issue.
func (s *Store) Get(issue int) (*TriageState, error) {
	data, err := os.ReadFile(s.statePath(issue))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("triage state for issue %d not found", issue)
		}
		return nil, err
	}
	var state TriageState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("parse state: %w", err)
	}
	return &state, nil
}

// Update performs a read-modify-write of the triage state, setting UpdatedAt automatically.
func (s *Store) Update(issue int, fn func(*TriageState)) error {
	state, err := s.Get(issue)
	if err != nil {
		return err
	}
	fn(state)
	state.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	return s.Save(state)
}

// List returns all triage states, optionally filtered by status. Pass "" for all.
func (s *Store) List(statusFilter string) ([]TriageState, error) {
	entries, err := os.ReadDir(s.baseDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read dir: %w", err)
	}
	var states []TriageState
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if filepath.Ext(name) != ".json" {
			continue
		}
		numStr := name[:len(name)-5] // strip .json
		issue, err := strconv.Atoi(numStr)
		if err != nil {
			continue
		}
		st, err := s.Get(issue)
		if err != nil {
			continue
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

// ReadOutcome reads the outcome file the agent wrote.
func (s *Store) ReadOutcome(issue int, stageID string) (*TriageOutcome, error) {
	data, err := os.ReadFile(s.OutcomePath(issue, stageID))
	if err != nil {
		return nil, fmt.Errorf("read outcome file: %w", err)
	}
	var out TriageOutcome
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("parse outcome: %w", err)
	}
	return &out, nil
}

// EnsureOutcomeDir creates the per-issue outcome directory.
func (s *Store) EnsureOutcomeDir(issue int) error {
	dir := filepath.Join(s.baseDir, strconv.Itoa(issue))
	return os.MkdirAll(dir, 0o755)
}
```

**Step 4: Run tests**

```bash
cd /Users/lucas/Documents/taintfactory && go test ./internal/triage/... -run TestStore -v
```
Expected: all PASS.

**Step 5: Commit**

```bash
git add internal/triage/state.go internal/triage/state_test.go
git commit -m "feat(triage): add state types and Store"
```

---

### Task 3: `internal/triage/templates/` — Embedded default prompt templates

**Files:**
- Create: `internal/triage/templates/stale-context.md`
- Create: `internal/triage/templates/already-implemented.md`

No tests needed — these are content files.

**Step 1: Write `stale-context.md`**

```markdown
<!-- internal/triage/templates/stale-context.md -->
You are triaging GitHub issue #{{.issue_number}}: "{{.issue_title}}".

Your task: determine whether the issue's context is **stale** (references code that no longer exists) or **clean** (still valid).

## Issue body

{{.issue_body}}

## Instructions

1. Extract every concrete reference from the issue body: file paths, function names, type names, CLI commands, package names, error messages that mention specific code.
2. For each reference, verify it still exists in the codebase using tools like `ls`, `grep`, or reading relevant files.
3. If **any** reference is stale (no longer exists or has moved):
   - Post a comment on the issue explaining what's gone: `gh issue comment {{.issue_number}} --body "..."`
   - Add the `stale` label: `gh issue edit {{.issue_number}} --add-label stale`
   - Write `{"outcome":"stale","summary":"<one sentence>"}` to `{{.outcome_file}}`
4. If all references check out (or the issue has no concrete references):
   - Write `{"outcome":"clean","summary":"All referenced symbols found"}` to `{{.outcome_file}}`

Write the outcome file as your **final act**. Do nothing else after writing it.
```

**Step 2: Write `already-implemented.md`**

```markdown
<!-- internal/triage/templates/already-implemented.md -->
You are triaging GitHub issue #{{.issue_number}}: "{{.issue_title}}".

Your task: determine whether the behavior described in this issue **already exists** in the codebase.

## Issue body

{{.issue_body}}

## Instructions

1. Read the issue carefully and identify the specific behavior or feature it requests.
2. Search the codebase for evidence that this behavior is already implemented (grep, read files, check tests).
3. If the behavior **is already implemented**:
   - Post a comment with evidence: `gh issue comment {{.issue_number}} --body "Already implemented in <file>:<line> — <brief explanation>"`
   - Close the issue: `gh issue close {{.issue_number}} --reason completed`
   - Write `{"outcome":"implemented","summary":"<where it was found>"}` to `{{.outcome_file}}`
4. If the behavior is **not implemented**:
   - Write `{"outcome":"not_implemented","summary":"No existing implementation found"}` to `{{.outcome_file}}`

Write the outcome file as your **final act**. Do nothing else after writing it.
```

**Step 3: Commit**

```bash
git add internal/triage/templates/
git commit -m "feat(triage): add default prompt templates for stale-context and already-implemented"
```

---

### Task 4: `internal/triage/runner.go` — Runner with Enqueue and Advance

**Files:**
- Create: `internal/triage/runner.go`
- Create: `internal/triage/runner_test.go`

**Step 1: Write the failing tests**

The runner tests use an in-memory DB and a mock session manager. Look at how `internal/session/session_test.go` mocks `TmuxRunner` for reference.

```go
// internal/triage/runner_test.go
package triage

import (
	"fmt"
	"testing"
	"time"

	"github.com/lucasnoah/taintfactory/internal/db"
	"github.com/lucasnoah/taintfactory/internal/session"
)

// mockTmux records calls and simulates tmux behavior for tests.
type mockTmux struct {
	sessions     map[string]bool
	sentKeys     []string
	captureLines string
}

func (m *mockTmux) NewSession(name, workdir, cmd string) error {
	if m.sessions == nil {
		m.sessions = make(map[string]bool)
	}
	m.sessions[name] = true
	return nil
}
func (m *mockTmux) HasSession(name string) (bool, error) {
	return m.sessions != nil && m.sessions[name], nil
}
func (m *mockTmux) SendKeys(name, keys string) error {
	m.sentKeys = append(m.sentKeys, keys)
	return nil
}
func (m *mockTmux) ListSessions() ([]string, error) {
	var names []string
	for n := range m.sessions {
		names = append(names, n)
	}
	return names, nil
}
func (m *mockTmux) KillSession(name string) error {
	delete(m.sessions, name)
	return nil
}
func (m *mockTmux) CapturePaneLines(name string, n int) (string, error) {
	return m.captureLines, nil
}

// mockGH simulates gh CLI responses.
type mockGH struct {
	responses map[string]string
}

func (m *mockGH) Run(args ...string) (string, error) {
	key := fmt.Sprintf("%v", args)
	if resp, ok := m.responses[key]; ok {
		return resp, nil
	}
	return `{"number":42,"title":"Test issue","body":"Test body","state":"open","labels":[]}`, nil
}

func newTestDB(t *testing.T) *db.DB {
	t.Helper()
	d, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	if err := d.Migrate(); err != nil {
		t.Fatalf("db.Migrate: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func TestRunner_Enqueue(t *testing.T) {
	store := NewStore(t.TempDir())
	tmux := &mockTmux{}
	database := newTestDB(t)
	sessions := session.NewManager(tmux, database, nil)
	gh := &mockGHClient{}

	cfg := &TriageConfig{
		Triage: TriageMeta{Name: "Test", Repo: "owner/test"},
		Stages: []TriageStage{
			{ID: "stale_context", Timeout: "10m", Outcomes: map[string]string{"stale": "done", "clean": "already_implemented"}},
			{ID: "already_implemented", Timeout: "10m", Outcomes: map[string]string{"implemented": "done", "not_implemented": "done"}},
		},
	}

	runner := NewRunner(cfg, store, database, sessions, gh, t.TempDir())

	if err := runner.Enqueue(42, "Test issue", "Test body"); err != nil {
		t.Fatalf("Enqueue() error: %v", err)
	}

	state, err := store.Get(42)
	if err != nil {
		t.Fatalf("Get() after Enqueue: %v", err)
	}
	if state.CurrentStage != "stale_context" {
		t.Errorf("CurrentStage = %q, want stale_context", state.CurrentStage)
	}
	if state.Status != "in_progress" {
		t.Errorf("Status = %q, want in_progress", state.Status)
	}
	if state.CurrentSession == "" {
		t.Error("CurrentSession should be set after Enqueue")
	}
	// A tmux session should have been created
	if !tmux.sessions[state.CurrentSession] {
		t.Errorf("tmux session %q not created", state.CurrentSession)
	}
}

func TestRunner_Advance_SkipsActiveSession(t *testing.T) {
	store := NewStore(t.TempDir())
	tmux := &mockTmux{sessions: map[string]bool{"triage-42-stale_context": true}}
	database := newTestDB(t)
	sessions := session.NewManager(tmux, database, nil)

	// Log a "busy" state for the session (not idle)
	if err := database.LogSessionEvent("triage-42-stale_context", "busy", 0); err != nil {
		t.Fatal(err)
	}

	cfg := &TriageConfig{
		Triage: TriageMeta{Repo: "owner/test"},
		Stages: []TriageStage{
			{ID: "stale_context", Timeout: "10m", Outcomes: map[string]string{"stale": "done"}},
		},
	}

	if err := store.Save(&TriageState{
		Issue:          42,
		Status:         "in_progress",
		CurrentStage:   "stale_context",
		CurrentSession: "triage-42-stale_context",
		StageHistory:   []TriageStageHistoryEntry{},
	}); err != nil {
		t.Fatal(err)
	}

	runner := NewRunner(cfg, store, database, sessions, nil, t.TempDir())
	actions, err := runner.Advance()
	if err != nil {
		t.Fatalf("Advance() error: %v", err)
	}
	if len(actions) != 1 || actions[0].Action != "skip" {
		t.Errorf("expected skip action, got %+v", actions)
	}
}

func TestRunner_Advance_IdleSession_RoutesToNextStage(t *testing.T) {
	repoRoot := t.TempDir()
	store := NewStore(t.TempDir())
	tmux := &mockTmux{sessions: map[string]bool{"triage-42-stale_context": true}}
	database := newTestDB(t)
	sessions := session.NewManager(tmux, database, nil)

	// Simulate idle session
	if err := database.LogSessionEvent("triage-42-stale_context", "idle", 0); err != nil {
		t.Fatal(err)
	}

	// Write outcome file
	if err := store.EnsureOutcomeDir(42); err != nil {
		t.Fatal(err)
	}
	outcomeData := []byte(`{"outcome":"clean","summary":"All good"}`)
	if err := os.WriteFile(store.OutcomePath(42, "stale_context"), outcomeData, 0644); err != nil {
		t.Fatal(err)
	}

	cfg := &TriageConfig{
		Triage: TriageMeta{Repo: "owner/test"},
		Stages: []TriageStage{
			{ID: "stale_context", Timeout: "10m", Outcomes: map[string]string{"stale": "done", "clean": "already_implemented"}},
			{ID: "already_implemented", Timeout: "10m", Outcomes: map[string]string{"implemented": "done", "not_implemented": "done"}},
		},
	}

	if err := store.Save(&TriageState{
		Issue:          42,
		Status:         "in_progress",
		CurrentStage:   "stale_context",
		CurrentSession: "triage-42-stale_context",
		StageHistory:   []TriageStageHistoryEntry{},
	}); err != nil {
		t.Fatal(err)
	}

	runner := NewRunner(cfg, store, database, sessions, nil, repoRoot)
	actions, err := runner.Advance()
	if err != nil {
		t.Fatalf("Advance() error: %v", err)
	}
	if len(actions) != 1 || actions[0].Action != "advance" {
		t.Errorf("expected advance action, got %+v", actions)
	}

	state, _ := store.Get(42)
	if state.CurrentStage != "already_implemented" {
		t.Errorf("CurrentStage = %q, want already_implemented", state.CurrentStage)
	}
	if len(state.StageHistory) != 1 || state.StageHistory[0].Outcome != "clean" {
		t.Errorf("StageHistory = %+v, want one entry with outcome=clean", state.StageHistory)
	}
}

func TestRunner_Advance_DoneOutcome_MarksCompleted(t *testing.T) {
	repoRoot := t.TempDir()
	store := NewStore(t.TempDir())
	tmux := &mockTmux{sessions: map[string]bool{"triage-42-stale_context": true}}
	database := newTestDB(t)
	sessions := session.NewManager(tmux, database, nil)

	if err := database.LogSessionEvent("triage-42-stale_context", "idle", 0); err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureOutcomeDir(42); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store.OutcomePath(42, "stale_context"), []byte(`{"outcome":"stale","summary":"File gone"}`), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := &TriageConfig{
		Triage: TriageMeta{Repo: "owner/test"},
		Stages: []TriageStage{
			{ID: "stale_context", Timeout: "10m", Outcomes: map[string]string{"stale": "done"}},
		},
	}
	if err := store.Save(&TriageState{
		Issue:          42,
		Status:         "in_progress",
		CurrentStage:   "stale_context",
		CurrentSession: "triage-42-stale_context",
		StageHistory:   []TriageStageHistoryEntry{},
	}); err != nil {
		t.Fatal(err)
	}

	runner := NewRunner(cfg, store, database, sessions, nil, repoRoot)
	actions, err := runner.Advance()
	if err != nil {
		t.Fatalf("Advance() error: %v", err)
	}
	if len(actions) != 1 || actions[0].Action != "completed" {
		t.Errorf("expected completed action, got %+v", actions)
	}

	state, _ := store.Get(42)
	if state.Status != "completed" {
		t.Errorf("Status = %q, want completed", state.Status)
	}
}
```

**Note:** The test file uses `os` — add `"os"` to the import block.

**Step 2: Run test to verify it fails**

```bash
cd /Users/lucas/Documents/taintfactory && go test ./internal/triage/... -run TestRunner -v 2>&1 | head -20
```
Expected: compile error — Runner not defined.

**Step 3: Implement `runner.go`**

```go
// internal/triage/runner.go
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

// GHClient is the interface the runner needs from the github package.
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
}

// NewRunner creates a Runner.
func NewRunner(cfg *TriageConfig, store *Store, database *db.DB, sessions *session.Manager, gh GHClient, repoRoot string) *Runner {
	return &Runner{
		cfg:      cfg,
		store:    store,
		db:       database,
		sessions: sessions,
		gh:       gh,
		repoRoot: repoRoot,
	}
}

// SetProgress configures the writer for live progress output.
func (r *Runner) SetProgress(w io.Writer) {
	r.progress = w
}

func (r *Runner) logf(format string, args ...any) {
	if r.progress != nil {
		fmt.Fprintf(r.progress, "  → triage #%s\n", fmt.Sprintf(format, args...))
	}
}

// Enqueue initialises a new triage pipeline for an issue and starts the first stage.
// issueTitle and issueBody are passed in directly (already fetched by the CLI).
func (r *Runner) Enqueue(issue int, issueTitle, issueBody string) error {
	firstStage := r.cfg.Stages[0].ID
	state := &TriageState{
		Issue:        issue,
		Repo:         r.cfg.Triage.Repo,
		CurrentStage: firstStage,
		Status:       "pending",
		StageHistory: []TriageStageHistoryEntry{},
	}
	if err := r.store.Save(state); err != nil {
		return fmt.Errorf("save state: %w", err)
	}
	return r.startStage(issue, firstStage, issueTitle, issueBody)
}

// Advance checks all in_progress triage pipelines and takes the next action.
// Called by the orchestrator check-in loop.
func (r *Runner) Advance() ([]TriageAction, error) {
	states, err := r.store.List("in_progress")
	if err != nil {
		return nil, fmt.Errorf("list triage states: %w", err)
	}

	var actions []TriageAction
	for _, st := range states {
		action := r.advanceOne(&st)
		actions = append(actions, action)
	}
	return actions, nil
}

func (r *Runner) advanceOne(st *TriageState) TriageAction {
	sessionName := st.CurrentSession
	if sessionName == "" {
		return TriageAction{Issue: st.Issue, Stage: st.CurrentStage, Action: "skip", Message: "no active session"}
	}

	dbState, err := r.db.GetSessionState(sessionName)
	if err != nil || dbState == nil {
		return TriageAction{Issue: st.Issue, Stage: st.CurrentStage, Action: "skip", Message: "session state unknown"}
	}

	if dbState.Event != "idle" && dbState.Event != "exited" {
		r.logf("issue #%d: session %s still active (%s), skipping", st.Issue, sessionName, dbState.Event)
		return TriageAction{Issue: st.Issue, Stage: st.CurrentStage, Action: "skip", Message: fmt.Sprintf("session %s: %s", sessionName, dbState.Event)}
	}

	// Session is idle — read the outcome file
	r.logf("issue #%d: session %s idle, reading outcome", st.Issue, sessionName)
	outcome, err := r.store.ReadOutcome(st.Issue, st.CurrentStage)
	if err != nil {
		return TriageAction{Issue: st.Issue, Stage: st.CurrentStage, Action: "error", Message: fmt.Sprintf("read outcome: %v", err)}
	}

	// Record stage in history
	entry := TriageStageHistoryEntry{
		Stage:   st.CurrentStage,
		Outcome: outcome.Outcome,
		Summary: outcome.Summary,
	}
	if err := r.store.Update(st.Issue, func(s *TriageState) {
		s.StageHistory = append(s.StageHistory, entry)
	}); err != nil {
		return TriageAction{Issue: st.Issue, Stage: st.CurrentStage, Action: "error", Message: fmt.Sprintf("update history: %v", err)}
	}

	// Route to next stage or complete
	stageCfg := r.cfg.StageByID(st.CurrentStage)
	if stageCfg == nil {
		return TriageAction{Issue: st.Issue, Stage: st.CurrentStage, Action: "error", Message: fmt.Sprintf("stage %q not in config", st.CurrentStage)}
	}

	nextStageID := stageCfg.Outcomes[outcome.Outcome]
	if nextStageID == "" || nextStageID == "done" {
		r.logf("issue #%d: triage complete (outcome: %s)", st.Issue, outcome.Outcome)
		if err := r.store.Update(st.Issue, func(s *TriageState) {
			s.Status = "completed"
			s.CurrentSession = ""
		}); err != nil {
			return TriageAction{Issue: st.Issue, Stage: st.CurrentStage, Action: "error", Message: fmt.Sprintf("mark completed: %v", err)}
		}
		return TriageAction{Issue: st.Issue, Stage: st.CurrentStage, Action: "completed", Message: fmt.Sprintf("outcome: %s", outcome.Outcome)}
	}

	// Start next stage — we need the issue body for the prompt.
	// Fetch it fresh from GitHub.
	issueData, err := r.gh.GetIssue(st.Issue)
	if err != nil {
		return TriageAction{Issue: st.Issue, Stage: st.CurrentStage, Action: "error", Message: fmt.Sprintf("fetch issue: %v", err)}
	}

	r.logf("issue #%d: advancing to stage %s", st.Issue, nextStageID)
	if err := r.startStage(st.Issue, nextStageID, issueData.Title, issueData.Body); err != nil {
		return TriageAction{Issue: st.Issue, Stage: nextStageID, Action: "error", Message: fmt.Sprintf("start stage: %v", err)}
	}

	return TriageAction{Issue: st.Issue, Stage: nextStageID, Action: "advance", Message: fmt.Sprintf("outcome %s → %s", outcome.Outcome, nextStageID)}
}

// startStage creates a session for the given stage and sends the rendered prompt.
func (r *Runner) startStage(issue int, stageID, issueTitle, issueBody string) error {
	sessionName := fmt.Sprintf("triage-%d-%s", issue, stageID)

	// Ensure outcome dir exists so agent has a place to write
	if err := r.store.EnsureOutcomeDir(issue); err != nil {
		return fmt.Errorf("ensure outcome dir: %w", err)
	}

	outcomePath := r.store.OutcomePath(issue, stageID)

	prompt, err := r.renderPrompt(issue, stageID, issueTitle, issueBody, outcomePath)
	if err != nil {
		return fmt.Errorf("render prompt: %w", err)
	}

	stageCfg := r.cfg.StageByID(stageID)
	timeout := 15 * time.Minute
	if stageCfg != nil && stageCfg.Timeout != "" {
		if d, err := time.ParseDuration(stageCfg.Timeout); err == nil {
			timeout = d
		}
	}
	_ = timeout // stored in session config; WaitIdle not called here (async)

	if err := r.sessions.Create(session.CreateOpts{
		Name:    sessionName,
		Workdir: r.repoRoot,
		Flags:   "--dangerously-skip-permissions",
		Issue:   issue,
		Stage:   stageID,
	}); err != nil {
		return fmt.Errorf("create session: %w", err)
	}

	// Wait for Claude to boot before sending prompt
	time.Sleep(15 * time.Second)

	if err := r.sessions.Send(sessionName, prompt); err != nil {
		return fmt.Errorf("send prompt: %w", err)
	}

	return r.store.Update(issue, func(s *TriageState) {
		s.Status = "in_progress"
		s.CurrentStage = stageID
		s.CurrentSession = sessionName
	})
}

// renderPrompt renders the stage's prompt template with issue data.
func (r *Runner) renderPrompt(issue int, stageID, issueTitle, issueBody, outcomePath string) (string, error) {
	// Look for an override in the repo root first
	overridePath := filepath.Join(r.repoRoot, "triage", stageID+".md")
	var tmplSrc string
	if data, err := os.ReadFile(overridePath); err == nil {
		tmplSrc = string(data)
	} else if def, ok := defaultTemplates[stageID]; ok {
		tmplSrc = def
	} else {
		return "", fmt.Errorf("no template found for stage %q", stageID)
	}

	t, err := template.New(stageID).Parse(tmplSrc)
	if err != nil {
		return "", fmt.Errorf("parse template: %w", err)
	}

	vars := map[string]any{
		"issue_number": issue,
		"issue_title":  issueTitle,
		"issue_body":   issueBody,
		"repo_root":    r.repoRoot,
		"outcome_file": outcomePath,
		"stage_id":     stageID,
	}

	var buf bytes.Buffer
	if err := t.Execute(&buf, vars); err != nil {
		return "", fmt.Errorf("execute template: %w", err)
	}
	return buf.String(), nil
}
```

**Note:** The template vars use `.issue_number` etc. but Go templates access map keys with `{{.issue_number}}`. Since vars is `map[string]any`, this works fine.

**Step 4: Run tests**

```bash
cd /Users/lucas/Documents/taintfactory && go test ./internal/triage/... -v
```
Expected: all PASS (the boot sleep is mocked away by the test not calling startStage directly in Advance tests).

**Important fix:** The `TestRunner_Advance_IdleSession_RoutesToNextStage` test calls `Advance()`, which calls `startStage()`, which calls `time.Sleep(15 * time.Second)`. To avoid slow tests, extract the boot wait into a field:

```go
type Runner struct {
    // ... existing fields ...
    bootWait time.Duration // defaults to 15s; overridable in tests
}

// In NewRunner:
func NewRunner(...) *Runner {
    return &Runner{
        // ...
        bootWait: 15 * time.Second,
    }
}

// In startStage:
time.Sleep(r.bootWait)
```

Tests that call `startStage` indirectly set `runner.bootWait = 0` before calling `Advance()`.

**Step 5: Commit**

```bash
git add internal/triage/runner.go internal/triage/runner_test.go
git commit -m "feat(triage): add Runner with Enqueue and Advance"
```

---

### Task 5: `internal/cli/triage.go` — CLI commands

**Files:**
- Create: `internal/cli/triage.go`

No unit tests — CLI command wiring is integration-level. Verify manually.

**Step 1: Implement the CLI**

```go
// internal/cli/triage.go
package cli

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/lucasnoah/taintfactory/internal/db"
	"github.com/lucasnoah/taintfactory/internal/github"
	"github.com/lucasnoah/taintfactory/internal/session"
	"github.com/lucasnoah/taintfactory/internal/triage"
	"github.com/spf13/cobra"
	"strings"
	"strconv"
)

var triageCmd = &cobra.Command{
	Use:   "triage",
	Short: "Triage GitHub issues using AI agents",
}

var triageRunCmd = &cobra.Command{
	Use:   "run <issue>",
	Short: "Enqueue and start a triage pipeline for a GitHub issue",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		issue, err := strconv.Atoi(args[0])
		if err != nil || issue <= 0 {
			return fmt.Errorf("invalid issue number %q", args[0])
		}

		runner, cleanup, err := newTriageRunner()
		if err != nil {
			return err
		}
		defer cleanup()

		ghClient := github.NewClient(&github.ExecRunner{})
		issueData, err := ghClient.GetIssue(issue)
		if err != nil {
			return fmt.Errorf("fetch issue #%d: %w", issue, err)
		}

		fmt.Fprintf(cmd.OutOrStdout(), "  → triage #%d: starting %q\n", issue, runner.FirstStageID())
		return runner.Enqueue(issue, issueData.Title, issueData.Body)
	},
}

var triageStatusCmd = &cobra.Command{
	Use:   "status <issue>",
	Short: "Show triage pipeline status for an issue",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		issue, err := strconv.Atoi(args[0])
		if err != nil || issue <= 0 {
			return fmt.Errorf("invalid issue number %q", args[0])
		}

		_, store, cleanup, err := newTriageStore()
		if err != nil {
			return err
		}
		defer cleanup()

		state, err := store.Get(issue)
		if err != nil {
			return fmt.Errorf("issue #%d: %w", issue, err)
		}

		fmt.Fprintf(cmd.OutOrStdout(), "Issue:    #%d\n", state.Issue)
		fmt.Fprintf(cmd.OutOrStdout(), "Status:   %s\n", state.Status)
		fmt.Fprintf(cmd.OutOrStdout(), "Stage:    %s\n", state.CurrentStage)
		if state.CurrentSession != "" {
			fmt.Fprintf(cmd.OutOrStdout(), "Session:  %s\n", state.CurrentSession)
		}
		if len(state.StageHistory) > 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "\nHistory:")
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
			fmt.Fprintln(w, "  STAGE\tOUTCOME\tSUMMARY")
			for _, h := range state.StageHistory {
				summary := h.Summary
				if len(summary) > 60 {
					summary = summary[:57] + "..."
				}
				fmt.Fprintf(w, "  %s\t%s\t%s\n", h.Stage, h.Outcome, summary)
			}
			w.Flush()
		}
		return nil
	},
}

var triageListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all triage pipelines for this repo",
	RunE: func(cmd *cobra.Command, args []string) error {
		_, store, cleanup, err := newTriageStore()
		if err != nil {
			return err
		}
		defer cleanup()

		states, err := store.List("")
		if err != nil {
			return err
		}
		if len(states) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "No triage pipelines found.")
			return nil
		}

		w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "ISSUE\tSTATUS\tSTAGE\tSESSION")
		for _, s := range states {
			fmt.Fprintf(w, "%d\t%s\t%s\t%s\n", s.Issue, s.Status, s.CurrentStage, s.CurrentSession)
		}
		return w.Flush()
	},
}

// newTriageStore opens just the config + store without a full runner (for read-only commands).
func newTriageStore() (*triage.TriageConfig, *triage.Store, func(), error) {
	repoRoot, err := findRepoRoot()
	if err != nil {
		return nil, nil, nil, err
	}
	cfg, err := triage.LoadDefault(repoRoot)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("load triage config: %w", err)
	}
	slug := repoSlug(cfg.Triage.Repo)
	store, err := triage.DefaultStore(slug)
	if err != nil {
		return nil, nil, nil, err
	}
	return cfg, store, func() {}, nil
}

// newTriageRunner builds a fully-wired Runner.
func newTriageRunner() (*triage.Runner, func(), error) {
	repoRoot, err := findRepoRoot()
	if err != nil {
		return nil, nil, err
	}
	cfg, err := triage.LoadDefault(repoRoot)
	if err != nil {
		return nil, nil, fmt.Errorf("load triage config: %w", err)
	}

	slug := repoSlug(cfg.Triage.Repo)
	store, err := triage.DefaultStore(slug)
	if err != nil {
		return nil, nil, err
	}

	dbPath, err := db.DefaultDBPath()
	if err != nil {
		return nil, nil, err
	}
	database, err := db.Open(dbPath)
	if err != nil {
		return nil, nil, err
	}
	if err := database.Migrate(); err != nil {
		database.Close()
		return nil, nil, err
	}

	tmux := session.NewExecTmux()
	sessions := session.NewManager(tmux, database, nil)

	ghRunner := &github.ExecRunner{}
	ghClient := github.NewClient(ghRunner)

	runner := triage.NewRunner(cfg, store, database, sessions, ghClient, repoRoot)
	runner.SetProgress(os.Stderr)

	return runner, func() { database.Close() }, nil
}

// repoSlug converts "owner/repo" to "owner-repo" for use as a directory name.
func repoSlug(repo string) string {
	return strings.ReplaceAll(repo, "/", "-")
}

func init() {
	triageCmd.AddCommand(triageRunCmd)
	triageCmd.AddCommand(triageStatusCmd)
	triageCmd.AddCommand(triageListCmd)
}
```

**Note:** `Runner` needs a `FirstStageID()` method added to `runner.go`:

```go
// FirstStageID returns the ID of the first configured stage.
func (r *Runner) FirstStageID() string {
	if len(r.cfg.Stages) == 0 {
		return ""
	}
	return r.cfg.Stages[0].ID
}
```

**Step 2: Verify compilation**

```bash
cd /Users/lucas/Documents/taintfactory && go build ./internal/cli/...
```
Expected: compiles cleanly.

**Step 3: Commit**

```bash
git add internal/cli/triage.go
git commit -m "feat(triage): add CLI commands (run/status/list)"
```

---

### Task 6: Wire triage into orchestrator check-in

**Files:**
- Modify: `internal/orchestrator/orchestrator.go`
- Modify: `internal/cli/orchestrator.go` (already has `newOrchestrator`)
- Modify: `internal/cli/pipeline.go` (`newOrchestrator` factory)

**Step 1: Add triage runner field to Orchestrator**

In `internal/orchestrator/orchestrator.go`, add a triage runner field and method:

```go
// Add to imports:
"github.com/lucasnoah/taintfactory/internal/triage"

// Add field to Orchestrator struct:
type Orchestrator struct {
    // ... existing fields ...
    triageRunner *triage.Runner // optional; nil if no triage.yaml
}

// Add setter:
func (o *Orchestrator) SetTriageRunner(r *triage.Runner) {
    o.triageRunner = r
}
```

**Step 2: Call `triageRunner.Advance()` in `CheckIn()`**

Find the `CheckIn()` method (line ~657) and add triage advancement **after** the existing pipeline loop:

```go
func (o *Orchestrator) CheckIn() (*CheckInResult, error) {
    // ... existing pipeline logic unchanged ...

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
```

**Step 3: Wire triage runner in `newOrchestrator` (pipeline.go)**

In `newOrchestrator()` in `internal/cli/pipeline.go`, after building the orchestrator, add:

```go
// Attach triage runner if triage.yaml exists in the repo root
if triageCfg, err := triage.LoadDefault(repoDir); err == nil {
    slug := strings.ReplaceAll(triageCfg.Triage.Repo, "/", "-")
    if triageStore, err := triage.DefaultStore(slug); err == nil {
        triageRunner := triage.NewRunner(triageCfg, triageStore, database, sessions, ghClient, repoDir)
        triageRunner.SetProgress(os.Stderr)
        orch.SetTriageRunner(triageRunner)
    }
}
```

Add to imports in `pipeline.go`:
```go
"github.com/lucasnoah/taintfactory/internal/triage"
"strings"
```

**Step 4: Verify compilation and tests**

```bash
cd /Users/lucas/Documents/taintfactory && go build ./... && go test ./...
```
Expected: all pass. The triage runner is nil in repos without `triage.yaml` (silently no-ops).

**Step 5: Commit**

```bash
git add internal/orchestrator/orchestrator.go internal/cli/pipeline.go
git commit -m "feat(triage): wire triage runner into orchestrator check-in loop"
```

---

### Task 7: Register `triage` command in root

**Files:**
- Modify: `internal/cli/root.go`

**Step 1: Read the current root.go**

```bash
grep -n "AddCommand\|rootCmd" /Users/lucas/Documents/taintfactory/internal/cli/root.go | head -20
```

**Step 2: Add triage command**

Find the block where other commands are registered (look for `rootCmd.AddCommand(queueCmd)` or similar) and add:

```go
rootCmd.AddCommand(triageCmd)
```

**Step 3: Verify build**

```bash
cd /Users/lucas/Documents/taintfactory && go build -o /tmp/factory ./cmd/factory/ && /tmp/factory triage --help
```
Expected: shows `run`, `status`, `list` subcommands.

**Step 4: Commit**

```bash
git add internal/cli/root.go
git commit -m "feat(triage): register triage command in CLI root"
```

---

### Task 8: Smoke test end-to-end

**No code — manual verification.**

**Step 1: Add a `triage.yaml` to the deathcookies repo**

```bash
cat > /Users/lucas/Documents/deathcookies/triage.yaml << 'EOF'
triage:
  name: "DeathCookies"
  repo: "lucasnoah/deathcookies"

stages:
  - id: stale_context
    timeout: 15m
    outcomes:
      stale: done
      clean: already_implemented

  - id: already_implemented
    timeout: 15m
    outcomes:
      implemented: done
      not_implemented: done
EOF
```

**Step 2: Build the binary**

```bash
cd /Users/lucas/Documents/taintfactory && go build -o /tmp/factory ./cmd/factory/
```

**Step 3: Run triage on a real issue**

```bash
cd /Users/lucas/Documents/deathcookies && unset CLAUDECODE && /tmp/factory triage run <some-issue-number>
```
Expected: prints `→ triage #N: starting stale_context`, creates a tmux session named `triage-N-stale_context`.

**Step 4: Check status**

```bash
cd /Users/lucas/Documents/deathcookies && /tmp/factory triage status <issue>
```
Expected: shows `in_progress`, current stage, current session.

**Step 5: Verify next check-in advances it**

```bash
cd /Users/lucas/Documents/deathcookies && /tmp/factory orchestrator check-in
```
Expected: when the triage session goes idle, the check-in picks it up and prints `triage:advance` or `triage:completed` actions.

**Step 6: Rebuild factory-runner binary and restart loop**

```bash
cd /Users/lucas/Documents/taintfactory && go build -o /tmp/factory ./cmd/factory/
tmux send-keys -t factory-runner C-c Enter
tmux send-keys -t factory-runner "while true; do /tmp/factory orchestrator check-in; sleep 10; done" Enter
```

---

## Summary

| Task | Files | Tests |
|---|---|---|
| 1 | `internal/triage/config.go` | `config_test.go` |
| 2 | `internal/triage/state.go` | `state_test.go` |
| 3 | `internal/triage/templates/*.md` | none (content) |
| 4 | `internal/triage/runner.go` | `runner_test.go` |
| 5 | `internal/cli/triage.go` | manual |
| 6 | orchestrator + pipeline.go | existing tests |
| 7 | `internal/cli/root.go` | build check |
| 8 | deathcookies/triage.yaml | smoke test |
