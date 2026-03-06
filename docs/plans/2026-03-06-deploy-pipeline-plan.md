# Deploy Pipeline Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add a manually-triggered deploy pipeline system that builds, pushes, smoke-tests, and can debug/rollback deployments via Claude agent sessions.

**Architecture:** Deploy pipelines are a second pipeline type alongside implementation pipelines. They're keyed by commit SHA (not issue number), stored at `~/.factory/deploys/{sha}/`, and tracked in a `deploys` DB table. The orchestrator's check-in loop picks up pending deploys and advances them through stages using the existing stage engine. The `deploy:` section in `pipeline.yaml` defines stages that reuse the same `config.Stage` struct.

**Tech Stack:** Go, Cobra CLI, PostgreSQL, existing stage engine / tmux session manager, HTML templates for web UI.

---

### Task 1: Add `Deploy` config section to types and loader

**Files:**
- Modify: `internal/config/types.go`
- Modify: `internal/config/loader.go`
- Create: `internal/config/loader_test.go`

**Step 1: Write the failing test**

Create `internal/config/loader_test.go`:

```go
package config

import (
	"testing"
)

func TestLoadWithDeploySection(t *testing.T) {
	yaml := []byte(`
pipeline:
  name: test-app
  stages:
    - id: implement
      type: agent

deploy:
  name: test-deploy
  stages:
    - id: deploy
      type: agent
      prompt_template: "templates/deploy.md"
      timeout: "10m"
      on_fail: rollback
    - id: smoke-test
      type: agent
      prompt_template: "templates/smoke.md"
      timeout: "5m"
    - id: rollback
      type: agent
      prompt_template: "templates/rollback.md"
      timeout: "5m"
`)
	cfg, err := LoadFromBytes(yaml)
	if err != nil {
		t.Fatalf("LoadFromBytes: %v", err)
	}
	if cfg.Deploy == nil {
		t.Fatal("Deploy section should not be nil")
	}
	if cfg.Deploy.Name != "test-deploy" {
		t.Errorf("Deploy.Name = %q, want %q", cfg.Deploy.Name, "test-deploy")
	}
	if len(cfg.Deploy.Stages) != 3 {
		t.Fatalf("Deploy.Stages has %d entries, want 3", len(cfg.Deploy.Stages))
	}
	if cfg.Deploy.Stages[0].ID != "deploy" {
		t.Errorf("first deploy stage ID = %q, want %q", cfg.Deploy.Stages[0].ID, "deploy")
	}
}

func TestLoadWithoutDeploySection(t *testing.T) {
	yaml := []byte(`
pipeline:
  name: test-app
  stages:
    - id: implement
      type: agent
`)
	cfg, err := LoadFromBytes(yaml)
	if err != nil {
		t.Fatalf("LoadFromBytes: %v", err)
	}
	if cfg.Deploy != nil {
		t.Error("Deploy should be nil when not present in YAML")
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/config/ -run TestLoadWith -v`
Expected: FAIL — `cfg.Deploy` field doesn't exist

**Step 3: Write minimal implementation**

Add to `internal/config/types.go`:

```go
// DeployPipeline defines the deploy pipeline configuration.
type DeployPipeline struct {
	Name   string  `yaml:"name"`
	Stages []Stage `yaml:"stages"`
}
```

Add `Deploy` field to `PipelineConfig`:

```go
type PipelineConfig struct {
	Pipeline Pipeline        `yaml:"pipeline"`
	Deploy   *DeployPipeline `yaml:"deploy"`
}
```

No changes needed to `loader.go` — `yaml.Unmarshal` will automatically populate the new field. But update `applyDefaults` to also apply defaults to deploy stages:

In `internal/config/loader.go`, add to `applyDefaults`:

```go
if cfg.Deploy != nil {
	for i := range cfg.Deploy.Stages {
		s := &cfg.Deploy.Stages[i]
		if s.Model == "" && cfg.Pipeline.Defaults.Model != "" {
			s.Model = cfg.Pipeline.Defaults.Model
		}
		if s.Flags == "" && cfg.Pipeline.Defaults.Flags != "" {
			s.Flags = cfg.Pipeline.Defaults.Flags
		}
	}
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./internal/config/ -run TestLoadWith -v`
Expected: PASS

**Step 5: Run full test suite to check for regressions**

Run: `go test ./internal/config/ -v`
Expected: All pass (existing tests unaffected since Deploy is a pointer, nil by default)

**Step 6: Commit**

```bash
git add internal/config/types.go internal/config/loader.go internal/config/loader_test.go
git commit -m "feat(config): add deploy section to pipeline config"
```

---

### Task 2: Add `DeployState` type and `DeployStore`

**Files:**
- Modify: `internal/pipeline/types.go`
- Create: `internal/pipeline/deploy_store.go`
- Create: `internal/pipeline/deploy_store_test.go`

**Step 1: Write the failing test**

Create `internal/pipeline/deploy_store_test.go`:

```go
package pipeline

import (
	"testing"
)

func TestDeployStoreCreateAndGet(t *testing.T) {
	s := NewDeployStore(t.TempDir())

	ds, err := s.Create(DeployCreateOpts{
		CommitSHA:   "abc123",
		Namespace:   "myorg/myapp",
		FirstStage:  "deploy",
		PreviousSHA: "def456",
		ConfigPath:  "/data/repos/myapp/pipeline.yaml",
		RepoDir:     "/data/repos/myapp",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if ds.CommitSHA != "abc123" {
		t.Errorf("CommitSHA = %q, want %q", ds.CommitSHA, "abc123")
	}
	if ds.PreviousSHA != "def456" {
		t.Errorf("PreviousSHA = %q, want %q", ds.PreviousSHA, "def456")
	}
	if ds.Status != "pending" {
		t.Errorf("Status = %q, want %q", ds.Status, "pending")
	}
	if ds.CurrentStage != "deploy" {
		t.Errorf("CurrentStage = %q, want %q", ds.CurrentStage, "deploy")
	}

	got, err := s.Get("abc123")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.CommitSHA != "abc123" {
		t.Errorf("Get CommitSHA = %q, want %q", got.CommitSHA, "abc123")
	}
	if got.PreviousSHA != "def456" {
		t.Errorf("Get PreviousSHA = %q, want %q", got.PreviousSHA, "def456")
	}
}

func TestDeployStoreCreateDuplicate(t *testing.T) {
	s := NewDeployStore(t.TempDir())
	_, _ = s.Create(DeployCreateOpts{CommitSHA: "abc123", FirstStage: "deploy"})
	_, err := s.Create(DeployCreateOpts{CommitSHA: "abc123", FirstStage: "deploy"})
	if err == nil {
		t.Fatal("expected error creating duplicate deploy")
	}
}

func TestDeployStoreUpdate(t *testing.T) {
	s := NewDeployStore(t.TempDir())
	_, _ = s.Create(DeployCreateOpts{CommitSHA: "abc123", FirstStage: "deploy"})

	err := s.Update("abc123", func(ds *DeployState) {
		ds.Status = "in_progress"
		ds.CurrentStage = "smoke-test"
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}

	got, _ := s.Get("abc123")
	if got.Status != "in_progress" {
		t.Errorf("Status = %q, want %q", got.Status, "in_progress")
	}
}

func TestDeployStoreList(t *testing.T) {
	s := NewDeployStore(t.TempDir())
	_, _ = s.Create(DeployCreateOpts{CommitSHA: "aaa", FirstStage: "deploy"})
	_, _ = s.Create(DeployCreateOpts{CommitSHA: "bbb", FirstStage: "deploy"})
	_ = s.Update("bbb", func(ds *DeployState) { ds.Status = "completed" })

	all, _ := s.List("")
	if len(all) != 2 {
		t.Fatalf("List all = %d, want 2", len(all))
	}

	completed, _ := s.List("completed")
	if len(completed) != 1 {
		t.Fatalf("List completed = %d, want 1", len(completed))
	}
}

func TestDeployStoreGetNotFound(t *testing.T) {
	s := NewDeployStore(t.TempDir())
	_, err := s.Get("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent deploy")
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/pipeline/ -run TestDeployStore -v`
Expected: FAIL — types/functions don't exist

**Step 3: Add DeployState to types.go**

Add to `internal/pipeline/types.go`:

```go
// DeployState is the persisted state for a deploy pipeline.
type DeployState struct {
	CommitSHA      string              `json:"commit_sha"`
	Namespace      string              `json:"namespace,omitempty"`
	CurrentStage   string              `json:"current_stage"`
	CurrentAttempt int                 `json:"current_attempt"`
	CurrentSession string              `json:"current_session"`
	StageHistory   []StageHistoryEntry `json:"stage_history"`
	Status         string              `json:"status"` // pending, in_progress, completed, failed, rolled_back
	PreviousSHA    string              `json:"previous_sha"`
	CreatedAt      string              `json:"created_at"`
	UpdatedAt      string              `json:"updated_at"`
	ConfigPath     string              `json:"config_path,omitempty"`
	RepoDir        string              `json:"repo_dir,omitempty"`
}
```

**Step 4: Create deploy_store.go**

Create `internal/pipeline/deploy_store.go`:

```go
package pipeline

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// DeployStore manages deploy pipeline state on disk.
type DeployStore struct {
	baseDir string // defaults to ~/.factory/deploys
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

// DeployCreateOpts holds options for creating a new deploy.
type DeployCreateOpts struct {
	CommitSHA   string
	Namespace   string
	FirstStage  string
	PreviousSHA string
	ConfigPath  string
	RepoDir     string
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
		return deploys[i].CreatedAt > deploys[j].CreatedAt // newest first
	})
	return deploys, nil
}
```

**Important:** The `DefaultDeployStore` function imports `config` — but `deploy_store.go` is in the `pipeline` package which already imports `config`. Check: yes, `store.go` line 11 already imports `"github.com/lucasnoah/taintfactory/internal/config"`. So the import is already available.

**Step 5: Run test to verify it passes**

Run: `go test ./internal/pipeline/ -run TestDeployStore -v`
Expected: PASS

**Step 6: Run full pipeline package tests**

Run: `go test ./internal/pipeline/ -v`
Expected: All pass

**Step 7: Commit**

```bash
git add internal/pipeline/types.go internal/pipeline/deploy_store.go internal/pipeline/deploy_store_test.go
git commit -m "feat(pipeline): add DeployState type and DeployStore"
```

---

### Task 3: Add `deploys` DB table and queries

**Files:**
- Modify: `internal/db/db.go` (schema + Migrate + Reset)
- Modify: `internal/db/queries.go` (new query functions)

**Step 1: Add the deploys table to the schema**

In `internal/db/db.go`, add to the `schema` const string, before the closing backtick:

```sql
CREATE TABLE IF NOT EXISTS deploys (
    id             SERIAL PRIMARY KEY,
    namespace      TEXT NOT NULL DEFAULT '',
    commit_sha     TEXT NOT NULL,
    status         TEXT NOT NULL DEFAULT 'pending'
                   CHECK(status IN ('pending','in_progress','completed','failed','rolled_back')),
    previous_sha   TEXT NOT NULL DEFAULT '',
    current_stage  TEXT NOT NULL DEFAULT '',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_deploys_status ON deploys(status);
CREATE INDEX IF NOT EXISTS idx_deploys_sha ON deploys(commit_sha);
```

Also add `"deploys"` to the `tables` slice in `Reset()` and add `"repos"` if it's not already there.

**Step 2: Add deploy query functions to queries.go**

Add to `internal/db/queries.go`:

```go
// DeployRecord represents a row in the deploys table.
type DeployRecord struct {
	ID           int
	Namespace    string
	CommitSHA    string
	Status       string
	PreviousSHA  string
	CurrentStage string
	CreatedAt    string
	UpdatedAt    string
}

// DeployInsert inserts a new deploy record.
func (d *DB) DeployInsert(namespace, commitSHA, previousSHA, currentStage string) error {
	_, err := d.conn.Exec(
		`INSERT INTO deploys (namespace, commit_sha, previous_sha, current_stage)
		 VALUES ($1, $2, $3, $4)`,
		namespace, commitSHA, previousSHA, currentStage,
	)
	if err != nil {
		return fmt.Errorf("insert deploy: %w", err)
	}
	return nil
}

// DeployUpdateStatus updates the status and current_stage of a deploy.
func (d *DB) DeployUpdateStatus(commitSHA, status, currentStage string) error {
	_, err := d.conn.Exec(
		`UPDATE deploys SET status = $1, current_stage = $2, updated_at = NOW()
		 WHERE commit_sha = $3`,
		status, currentStage, commitSHA,
	)
	if err != nil {
		return fmt.Errorf("update deploy status: %w", err)
	}
	return nil
}

// DeployList returns recent deploys, newest first.
func (d *DB) DeployList(limit int) ([]DeployRecord, error) {
	rows, err := d.conn.Query(
		`SELECT id, namespace, commit_sha, status, previous_sha, current_stage, created_at, updated_at
		 FROM deploys ORDER BY created_at DESC LIMIT $1`, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list deploys: %w", err)
	}
	defer rows.Close()

	var deploys []DeployRecord
	for rows.Next() {
		var r DeployRecord
		if err := rows.Scan(&r.ID, &r.Namespace, &r.CommitSHA, &r.Status, &r.PreviousSHA, &r.CurrentStage, &r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan deploy: %w", err)
		}
		deploys = append(deploys, r)
	}
	return deploys, rows.Err()
}

// DeployGetLatestCompleted returns the most recently completed deploy for a namespace.
func (d *DB) DeployGetLatestCompleted(namespace string) (*DeployRecord, error) {
	var r DeployRecord
	err := d.conn.QueryRow(
		`SELECT id, namespace, commit_sha, status, previous_sha, current_stage, created_at, updated_at
		 FROM deploys WHERE namespace = $1 AND status = 'completed'
		 ORDER BY created_at DESC LIMIT 1`, namespace,
	).Scan(&r.ID, &r.Namespace, &r.CommitSHA, &r.Status, &r.PreviousSHA, &r.CurrentStage, &r.CreatedAt, &r.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("get latest completed deploy: %w", err)
	}
	return &r, nil
}
```

**Step 3: Run existing tests to verify no regressions**

Run: `go test ./internal/db/ -v`
Expected: All pass (schema change is additive, `CREATE TABLE IF NOT EXISTS`)

**Step 4: Commit**

```bash
git add internal/db/db.go internal/db/queries.go
git commit -m "feat(db): add deploys table and query functions"
```

---

### Task 4: Add `factory deploy` CLI commands

**Files:**
- Create: `internal/cli/deploy.go`
- Modify: `internal/cli/root.go` (register command)

**Step 1: Create the deploy CLI commands**

Create `internal/cli/deploy.go`:

```go
package cli

import (
	"fmt"
	"os"
	"strconv"
	"text/tabwriter"

	"github.com/lucasnoah/taintfactory/internal/config"
	"github.com/lucasnoah/taintfactory/internal/db"
	"github.com/lucasnoah/taintfactory/internal/pipeline"
	"github.com/spf13/cobra"
)

var deployCmd = &cobra.Command{
	Use:   "deploy",
	Short: "Manage deploy pipelines",
}

var deployCreateCmd = &cobra.Command{
	Use:   "create <commit-sha>",
	Short: "Start a deploy pipeline for a commit SHA",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		sha := args[0]

		connStr, err := db.DefaultConnStr()
		if err != nil {
			return err
		}
		database, err := db.Open(connStr)
		if err != nil {
			return err
		}
		defer database.Close()
		if err := database.Migrate(); err != nil {
			return err
		}

		cfg, err := config.LoadDefault()
		if err != nil {
			return fmt.Errorf("load config: %w", err)
		}
		if cfg.Deploy == nil {
			return fmt.Errorf("no deploy: section found in pipeline config")
		}
		if len(cfg.Deploy.Stages) == 0 {
			return fmt.Errorf("deploy pipeline has no stages")
		}

		deployStore, err := pipeline.DefaultDeployStore()
		if err != nil {
			return err
		}

		// Find previous SHA for rollback
		namespace := cmd.Flag("namespace").Value.String()
		previousSHA := ""
		if prev, err := database.DeployGetLatestCompleted(namespace); err == nil {
			previousSHA = prev.CommitSHA
		}

		repoDir, err := findRepoRoot()
		if err != nil {
			return err
		}

		configPath := ""
		// Try to find pipeline.yaml
		candidates := []string{
			"pipeline.yaml",
			repoDir + "/pipeline.yaml",
		}
		for _, c := range candidates {
			if _, err := os.Stat(c); err == nil {
				configPath = c
				break
			}
		}

		ds, err := deployStore.Create(pipeline.DeployCreateOpts{
			CommitSHA:   sha,
			Namespace:   namespace,
			FirstStage:  cfg.Deploy.Stages[0].ID,
			PreviousSHA: previousSHA,
			ConfigPath:  configPath,
			RepoDir:     repoDir,
		})
		if err != nil {
			return err
		}

		// Record in DB for web UI / listing
		_ = database.DeployInsert(namespace, sha, previousSHA, ds.CurrentStage)

		fmt.Printf("Deploy pipeline created for %s\n", sha)
		fmt.Printf("  Previous SHA: %s\n", previousSHA)
		fmt.Printf("  First stage:  %s\n", ds.CurrentStage)
		fmt.Printf("  Status:       %s\n", ds.Status)
		return nil
	},
}

var deployListCmd = &cobra.Command{
	Use:   "list",
	Short: "List recent deploys",
	RunE: func(cmd *cobra.Command, args []string) error {
		connStr, err := db.DefaultConnStr()
		if err != nil {
			return err
		}
		database, err := db.Open(connStr)
		if err != nil {
			return err
		}
		defer database.Close()

		limit, _ := strconv.Atoi(cmd.Flag("limit").Value.String())
		if limit == 0 {
			limit = 20
		}

		deploys, err := database.DeployList(limit)
		if err != nil {
			return err
		}

		if len(deploys) == 0 {
			fmt.Println("No deploys found.")
			return nil
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "SHA\tSTATUS\tSTAGE\tNAMESPACE\tCREATED")
		for _, d := range deploys {
			sha := d.CommitSHA
			if len(sha) > 7 {
				sha = sha[:7]
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", sha, d.Status, d.CurrentStage, d.Namespace, d.CreatedAt)
		}
		w.Flush()
		return nil
	},
}

var deployStatusCmd = &cobra.Command{
	Use:   "status [sha]",
	Short: "Show deploy status (latest if no SHA given)",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		deployStore, err := pipeline.DefaultDeployStore()
		if err != nil {
			return err
		}

		var ds *pipeline.DeployState
		if len(args) == 1 {
			ds, err = deployStore.Get(args[0])
		} else {
			all, listErr := deployStore.List("")
			if listErr != nil || len(all) == 0 {
				return fmt.Errorf("no deploys found")
			}
			ds = &all[0]
			err = nil
		}
		if err != nil {
			return err
		}

		fmt.Printf("Deploy: %s\n", ds.CommitSHA)
		fmt.Printf("  Status:       %s\n", ds.Status)
		fmt.Printf("  Stage:        %s\n", ds.CurrentStage)
		fmt.Printf("  Attempt:      %d\n", ds.CurrentAttempt)
		fmt.Printf("  Previous SHA: %s\n", ds.PreviousSHA)
		fmt.Printf("  Created:      %s\n", ds.CreatedAt)
		fmt.Printf("  Updated:      %s\n", ds.UpdatedAt)
		if len(ds.StageHistory) > 0 {
			fmt.Println("  History:")
			for _, h := range ds.StageHistory {
				fmt.Printf("    %s: %s (attempt %d, %s)\n", h.Stage, h.Outcome, h.Attempt, h.Duration)
			}
		}
		return nil
	},
}

func init() {
	deployCmd.AddCommand(deployCreateCmd)
	deployCmd.AddCommand(deployListCmd)
	deployCmd.AddCommand(deployStatusCmd)

	deployCreateCmd.Flags().String("namespace", "", "project namespace (e.g. myorg/myapp)")
	deployListCmd.Flags().String("limit", "20", "max deploys to show")
}
```

**Step 2: Register the command in root.go**

In `internal/cli/root.go`, add to `init()`:

```go
rootCmd.AddCommand(deployCmd)
```

**Step 3: Verify it compiles**

Run: `go build ./cmd/factory/`
Expected: Compiles successfully

**Step 4: Commit**

```bash
git add internal/cli/deploy.go internal/cli/root.go
git commit -m "feat(cli): add factory deploy create/list/status commands"
```

---

### Task 5: Add deploy pipeline advancement to orchestrator

**Files:**
- Modify: `internal/orchestrator/orchestrator.go`

This is the core integration. The orchestrator needs to:
1. Hold a reference to a `DeployStore`
2. Check for pending/in-progress deploys during `CheckIn()`
3. Advance deploy pipelines through stages using the existing stage engine

**Step 1: Add DeployStore field to Orchestrator**

In `internal/orchestrator/orchestrator.go`, add field to `Orchestrator` struct:

```go
deployStore *pipeline.DeployStore
```

Add setter:

```go
// SetDeployStore configures the deploy pipeline store.
func (o *Orchestrator) SetDeployStore(ds *pipeline.DeployStore) {
	o.deployStore = ds
}
```

**Step 2: Add `checkInDeploy` method**

Add a method that finds the active deploy and advances it, similar to `checkInPipeline` but for deploys:

```go
// checkInDeploy checks for pending/in-progress deploys and advances them.
func (o *Orchestrator) checkInDeploy() *CheckInAction {
	if o.deployStore == nil {
		return nil
	}
	deploys, err := o.deployStore.List("")
	if err != nil {
		return nil
	}
	for i := range deploys {
		ds := &deploys[i]
		if ds.Status == "completed" || ds.Status == "failed" || ds.Status == "rolled_back" {
			continue
		}
		action := o.advanceDeploy(ds)
		return &action
	}
	return nil
}
```

**Step 3: Add `advanceDeploy` method**

This method advances a deploy pipeline through its stages, reusing the stage engine:

```go
// advanceDeploy advances a single deploy pipeline.
func (o *Orchestrator) advanceDeploy(ds *pipeline.DeployState) CheckInAction {
	cfg, err := o.deployConfig(ds)
	if err != nil || cfg.Deploy == nil {
		return CheckInAction{Action: "skip", Message: fmt.Sprintf("deploy %s: no deploy config", ds.CommitSHA)}
	}

	// If there's an active session, check its status (same as issue pipeline)
	if ds.CurrentSession != "" {
		status := o.sessions.Status(ds.CurrentSession)
		switch status {
		case "active":
			return CheckInAction{Action: "skip", Message: fmt.Sprintf("deploy %s: session active", ds.CommitSHA)}
		case "idle", "exited":
			// Clean up and re-advance
			_, _ = o.sessions.Kill(ds.CurrentSession)
			_ = o.deployStore.Update(ds.CommitSHA, func(d *pipeline.DeployState) {
				d.CurrentSession = ""
			})
		}
	}

	// Find stage config
	stageCfg := o.findDeployStage(ds.CurrentStage, cfg)
	if stageCfg == nil {
		return CheckInAction{Action: "skip", Message: fmt.Sprintf("deploy %s: stage %q not found", ds.CommitSHA, ds.CurrentStage)}
	}

	// Update status to in_progress
	_ = o.deployStore.Update(ds.CommitSHA, func(d *pipeline.DeployState) {
		d.Status = "in_progress"
	})
	_ = o.db.DeployUpdateStatus(ds.CommitSHA, "in_progress", ds.CurrentStage)

	o.logf("deploy %s: running stage %q (attempt %d)", ds.CommitSHA, ds.CurrentStage, ds.CurrentAttempt)

	// Determine timeout
	timeout := 10 * time.Minute
	if stageCfg.Timeout != "" {
		if d, parseErr := time.ParseDuration(stageCfg.Timeout); parseErr == nil {
			timeout = d
		}
	}

	// Run the stage via stage engine
	// Note: we use a synthetic "issue" number of 0 for deploys and override the working directory
	runResult, err := o.engine.Run(stage.RunOpts{
		Issue:   0, // deploys aren't tied to issues
		Stage:   ds.CurrentStage,
		Timeout: timeout,
		Config:  cfg,
	})
	if err != nil {
		_ = o.deployStore.Update(ds.CommitSHA, func(d *pipeline.DeployState) {
			d.Status = "pending" // reset so next check-in retries
		})
		return CheckInAction{Action: "escalate", Message: fmt.Sprintf("deploy %s: stage engine error: %v", ds.CommitSHA, err)}
	}

	// Record stage history
	_ = o.deployStore.Update(ds.CommitSHA, func(d *pipeline.DeployState) {
		d.StageHistory = append(d.StageHistory, pipeline.StageHistoryEntry{
			Stage:    runResult.Stage,
			Attempt:  runResult.Attempt,
			Outcome:  runResult.Outcome,
			Duration: runResult.TotalDuration.String(),
		})
	})

	if runResult.Outcome == "success" {
		return o.advanceDeployToNext(ds, cfg)
	}

	// Stage failed — route via on_fail
	return o.handleDeployFailure(ds, stageCfg, cfg)
}
```

**Step 4: Add helper methods**

```go
// deployConfig loads the config for a deploy pipeline.
func (o *Orchestrator) deployConfig(ds *pipeline.DeployState) (*config.PipelineConfig, error) {
	if ds.ConfigPath != "" {
		return config.Load(ds.ConfigPath)
	}
	return o.cfg, nil
}

// findDeployStage finds a stage in the deploy config by ID.
func (o *Orchestrator) findDeployStage(stageID string, cfg *config.PipelineConfig) *config.Stage {
	if cfg.Deploy == nil {
		return nil
	}
	for i := range cfg.Deploy.Stages {
		if cfg.Deploy.Stages[i].ID == stageID {
			return &cfg.Deploy.Stages[i]
		}
	}
	return nil
}

// advanceDeployToNext moves the deploy to the next stage or marks it completed.
func (o *Orchestrator) advanceDeployToNext(ds *pipeline.DeployState, cfg *config.PipelineConfig) CheckInAction {
	nextStage := ""
	for i, s := range cfg.Deploy.Stages {
		if s.ID == ds.CurrentStage && i+1 < len(cfg.Deploy.Stages) {
			nextStage = cfg.Deploy.Stages[i+1].ID
			break
		}
	}

	if nextStage == "" {
		// Pipeline complete
		_ = o.deployStore.Update(ds.CommitSHA, func(d *pipeline.DeployState) {
			d.Status = "completed"
		})
		_ = o.db.DeployUpdateStatus(ds.CommitSHA, "completed", ds.CurrentStage)
		o.logf("deploy %s: completed successfully", ds.CommitSHA)
		return CheckInAction{Action: "completed", Message: fmt.Sprintf("deploy %s completed", ds.CommitSHA)}
	}

	_ = o.deployStore.Update(ds.CommitSHA, func(d *pipeline.DeployState) {
		d.CurrentStage = nextStage
		d.CurrentAttempt = 1
		d.CurrentSession = ""
	})
	_ = o.db.DeployUpdateStatus(ds.CommitSHA, "in_progress", nextStage)
	o.logf("deploy %s: advancing to stage %q", ds.CommitSHA, nextStage)
	return CheckInAction{Action: "advance", Message: fmt.Sprintf("deploy %s → %s", ds.CommitSHA, nextStage)}
}

// handleDeployFailure routes deploy stage failures via on_fail.
func (o *Orchestrator) handleDeployFailure(ds *pipeline.DeployState, stageCfg *config.Stage, cfg *config.PipelineConfig) CheckInAction {
	target := resolveOnFail(stageCfg.OnFail)
	if target == "" {
		// No on_fail defined — deploy fails
		_ = o.deployStore.Update(ds.CommitSHA, func(d *pipeline.DeployState) {
			d.Status = "failed"
		})
		_ = o.db.DeployUpdateStatus(ds.CommitSHA, "failed", ds.CurrentStage)
		return CheckInAction{Action: "fail", Message: fmt.Sprintf("deploy %s: stage %q failed, no on_fail", ds.CommitSHA, ds.CurrentStage)}
	}

	// Check if the on_fail target is "rollback" — if rollback itself fails, mark as failed
	if ds.CurrentStage == "rollback" || target == ds.CurrentStage {
		_ = o.deployStore.Update(ds.CommitSHA, func(d *pipeline.DeployState) {
			d.Status = "failed"
		})
		_ = o.db.DeployUpdateStatus(ds.CommitSHA, "failed", ds.CurrentStage)
		return CheckInAction{Action: "fail", Message: fmt.Sprintf("deploy %s: %s failed, cannot recover", ds.CommitSHA, ds.CurrentStage)}
	}

	// If the target is "rollback", mark as rolled_back when it succeeds (handled in advanceDeployToNext)
	_ = o.deployStore.Update(ds.CommitSHA, func(d *pipeline.DeployState) {
		d.CurrentStage = target
		d.CurrentAttempt = 1
		d.CurrentSession = ""
	})
	_ = o.db.DeployUpdateStatus(ds.CommitSHA, "in_progress", target)
	o.logf("deploy %s: stage %q failed, routing to %q", ds.CommitSHA, ds.CurrentStage, target)
	return CheckInAction{Action: "retry", Message: fmt.Sprintf("deploy %s: %s → %s", ds.CommitSHA, ds.CurrentStage, target)}
}
```

**Step 5: Wire into CheckIn()**

In `CheckIn()`, add deploy check after issue pipelines and queue, but before triage:

```go
// Check for active deploys
if action := o.checkInDeploy(); action != nil {
	result.Actions = append(result.Actions, *action)
}
```

Add this right before the `// Advance triage pipelines` comment block (around line 769).

**Step 6: Verify it compiles**

Run: `go build ./cmd/factory/`
Expected: Compiles successfully

**Step 7: Commit**

```bash
git add internal/orchestrator/orchestrator.go
git commit -m "feat(orchestrator): add deploy pipeline advancement to check-in loop"
```

---

### Task 6: Wire DeployStore into serve command and CLI orchestrator creation

**Files:**
- Modify: `internal/cli/pipeline.go` (the `newOrchestrator` helper)
- Modify: `internal/cli/serve.go` (if it exists, wire deploy store)

**Step 1: Find and read serve.go**

Look for: `internal/cli/serve.go` — this is where the serve command creates the orchestrator and web server.

**Step 2: Add DeployStore creation to newOrchestrator**

In `internal/cli/pipeline.go`, inside `newOrchestrator()`, after creating the pipeline store, add:

```go
deployStore, err := pipeline.DefaultDeployStore()
if err != nil {
	database.Close()
	return nil, nil, fmt.Errorf("open deploy store: %w", err)
}
```

After creating the orchestrator, wire it:

```go
orch.SetDeployStore(deployStore)
```

**Step 3: Verify it compiles and tests pass**

Run: `go build ./cmd/factory/ && go test ./... 2>&1 | tail -20`
Expected: Compiles, tests pass

**Step 4: Commit**

```bash
git add internal/cli/pipeline.go
git commit -m "feat(cli): wire DeployStore into orchestrator initialization"
```

---

### Task 7: Add deploy template variables

**Files:**
- Modify: `internal/context/builder.go` (or wherever template vars are assembled)

The stage engine needs to inject deploy-specific variables (`CommitSHA`, `PreviousSHA`, `Namespace`, `RepoDir`) into the prompt template context when running deploy stages.

**Step 1: Find how template vars are injected**

Search for how `RuntimeVars` or template variables are passed to the stage engine. This is likely in `internal/context/builder.go` or `internal/stage/engine.go`.

**Step 2: Add deploy vars injection**

The deploy advancement code needs to set `RuntimeVars` on the deploy state (or pass vars directly to the stage engine). Since deploy pipelines don't have a `PipelineState`, the simplest approach is to pass the vars through the `stage.RunOpts`:

Add a `Vars` field to `stage.RunOpts` (if not already present):

```go
type RunOpts struct {
	Issue   int
	Stage   string
	Timeout time.Duration
	Config  *config.PipelineConfig
	Vars    map[string]string // extra template vars
}
```

In the deploy advancement code (`advanceDeploy`), populate the vars:

```go
runResult, err := o.engine.Run(stage.RunOpts{
	Issue:   0,
	Stage:   ds.CurrentStage,
	Timeout: timeout,
	Config:  cfg,
	Vars: map[string]string{
		"CommitSHA":   ds.CommitSHA,
		"PreviousSHA": ds.PreviousSHA,
		"Namespace":   ds.Namespace,
		"RepoDir":     ds.RepoDir,
	},
})
```

Then in the stage engine's template rendering, merge these vars into the template context.

**Step 3: Verify it compiles**

Run: `go build ./cmd/factory/`
Expected: Compiles

**Step 4: Commit**

```bash
git add internal/stage/engine.go internal/orchestrator/orchestrator.go
git commit -m "feat(stage): add extra vars support for deploy template rendering"
```

---

### Task 8: Add `/deploys` web UI page

**Files:**
- Create: `internal/web/templates/deploys.html`
- Modify: `internal/web/server.go` (add template, route)
- Modify: `internal/web/handlers.go` (add handler)
- Modify: `internal/web/templates/base.html` (add sidebar link)

**Step 1: Create deploys.html template**

Create `internal/web/templates/deploys.html`:

```html
{{define "title"}}Deploys{{end}}

{{define "content"}}
<h1>Deploys</h1>

{{if not .Deploys}}
<p class="empty-state">No deploys yet.</p>
{{else}}
<table class="data-table">
  <thead>
    <tr>
      <th>SHA</th>
      <th>Status</th>
      <th>Stage</th>
      <th>Namespace</th>
      <th>Previous</th>
      <th>Created</th>
    </tr>
  </thead>
  <tbody>
    {{range .Deploys}}
    <tr>
      <td><code>{{.CommitSHA}}</code></td>
      <td><span class="{{badgeClass .Status}}">{{.Status}}</span></td>
      <td>{{.CurrentStage}}</td>
      <td>{{.Namespace}}</td>
      <td><code>{{.PreviousSHA}}</code></td>
      <td>{{relTime .CreatedAt}}</td>
    </tr>
    {{end}}
  </tbody>
</table>
{{end}}
{{end}}
```

**Step 2: Add template and route to server.go**

In `internal/web/server.go`, add to `Server` struct:

```go
deploysTmpl *template.Template
```

In `NewServer()`, add:

```go
deploysTmpl: mustParseTmpl("base.html", "deploys.html"),
```

In `buildMux()`, add route:

```go
mux.HandleFunc("/deploys", s.handleDeploys)
```

**Step 3: Add handler to handlers.go**

In `internal/web/handlers.go`, add:

```go
// DeploysPageData holds data for the deploys page.
type DeploysPageData struct {
	Deploys []db.DeployRecord
	Sidebar SidebarData
}

func (s *Server) handleDeploys(w http.ResponseWriter, r *http.Request) {
	deploys, err := s.db.DeployList(50)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	data := DeploysPageData{
		Deploys: deploys,
		Sidebar: s.sidebarData(currentProject(r)),
	}
	if err := s.deploysTmpl.ExecuteTemplate(w, "base.html", data); err != nil {
		http.Error(w, err.Error(), 500)
	}
}
```

**Step 4: Add sidebar link to base.html**

In `internal/web/templates/base.html`, find the sidebar links section (near where `/repos` was added) and add:

```html
<a href="/deploys" class="sidebar-link">Deploys</a>
```

**Step 5: Verify it compiles**

Run: `go build ./cmd/factory/`
Expected: Compiles

**Step 6: Commit**

```bash
git add internal/web/templates/deploys.html internal/web/templates/base.html internal/web/server.go internal/web/handlers.go
git commit -m "feat(web): add /deploys page to web UI"
```

---

### Task 9: Update pipeline.example.yaml with deploy section

**Files:**
- Modify: `config/pipeline.example.yaml`

**Step 1: Add deploy section to example config**

Append to `config/pipeline.example.yaml`:

```yaml

  # Deploy pipeline (triggered manually via `factory deploy <sha>`)
deploy:
  name: my-web-app-deploy
  stages:
    - id: deploy
      type: agent
      prompt_template: "templates/deploy.md"
      timeout: "10m"
      on_fail: rollback

    - id: smoke-test
      type: agent
      prompt_template: "templates/smoke-test.md"
      timeout: "5m"
      on_fail: debug

    - id: debug
      type: agent
      prompt_template: "templates/debug-deploy.md"
      timeout: "5m"
      on_fail: rollback

    - id: rollback
      type: agent
      prompt_template: "templates/rollback.md"
      timeout: "5m"
```

**Step 2: Verify the YAML parses**

Run: `go test ./internal/config/ -run TestLoad -v`
Expected: All pass

**Step 3: Commit**

```bash
git add config/pipeline.example.yaml
git commit -m "docs: add deploy section to pipeline.example.yaml"
```

---

### Task 10: Rebase onto main and integration test

**Step 1: Rebase the worktree onto main**

The worktree is behind main (missing repos, label poller, multi-repo fixes). Rebase:

```bash
git fetch origin
git rebase origin/main
```

Resolve any conflicts (likely in `internal/db/db.go` schema, `internal/cli/root.go`, `internal/web/server.go`).

**Step 2: Build and run full test suite**

```bash
go build ./cmd/factory/
go test ./...
```

Expected: Everything compiles and tests pass.

**Step 3: Manual smoke test**

```bash
# Verify CLI help
./factory deploy --help
./factory deploy create --help
./factory deploy list --help
./factory deploy status --help
```

**Step 4: Commit rebase resolution if needed**

```bash
git add -A
git rebase --continue
```
