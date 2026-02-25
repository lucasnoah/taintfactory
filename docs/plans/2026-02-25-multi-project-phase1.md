# Multi-Project Support (Phase 1) Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Allow factory to manage issues across multiple GitHub repos by storing a `config_path` per issue, loading the right config per pipeline, and scoping worktrees to each project's repo directory.

**Architecture:** Each queue item and pipeline state now carries its `config_path` (absolute path to the project's `pipeline.yaml`). The orchestrator's `Advance`, `Create`, and stage engine calls all use the pipeline-local config rather than the single globally-loaded one. Worktree managers are derived per-pipeline from `ps.RepoDir`. Storage gains an optional namespace layer (`{org}/{repo}/{issue}/`) to prevent collisions when two repos share issue numbers.

**Tech Stack:** Go, Cobra CLI, SQLite (`go-sqlite3`), JSON state files, YAML config

---

## Orientation

Key files you'll be editing:

| File | Role |
|------|------|
| `internal/pipeline/types.go` | `PipelineState` struct |
| `internal/pipeline/store.go` | JSON file read/write, path resolution |
| `internal/db/db.go` | SQLite migrations |
| `internal/db/queries.go` | Queue queries (`QueueItem`, `QueueAdd`, etc.) |
| `internal/cli/queue.go` | `factory queue add` command |
| `internal/orchestrator/orchestrator.go` | Main pipeline logic |
| `internal/stage/engine.go` | Stage runner |

Run tests with: `go test ./...`
Build with: `go build ./cmd/factory/`

---

## Task 1: Add `ConfigPath`, `RepoDir`, `Namespace` to `PipelineState`

**Files:**
- Modify: `internal/pipeline/types.go`
- Test: `internal/pipeline/store_test.go`

**Step 1: Write the failing test**

Add to `internal/pipeline/store_test.go`:

```go
func TestCreatePreservesConfigFields(t *testing.T) {
	s := newTestStore(t)

	ps, err := s.Create(pipeline.CreateOpts{
		Issue:      55,
		Title:      "Multi-project test",
		Branch:     "feature/55",
		Worktree:   "/tmp/wt-55",
		FirstStage: "implement",
		GoalGates:  map[string]string{},
		ConfigPath: "/projects/myapp/pipeline.yaml",
		RepoDir:    "/projects/myapp",
		Namespace:  "myorg/myapp",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if ps.ConfigPath != "/projects/myapp/pipeline.yaml" {
		t.Errorf("ConfigPath = %q, want %q", ps.ConfigPath, "/projects/myapp/pipeline.yaml")
	}
	if ps.RepoDir != "/projects/myapp" {
		t.Errorf("RepoDir = %q, want %q", ps.RepoDir, "/projects/myapp")
	}
	if ps.Namespace != "myorg/myapp" {
		t.Errorf("Namespace = %q, want %q", ps.Namespace, "myorg/myapp")
	}

	// Round-trip through disk
	got, err := s.Get(55)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ConfigPath != "/projects/myapp/pipeline.yaml" {
		t.Errorf("after Get: ConfigPath = %q", got.ConfigPath)
	}
}
```

**Step 2: Run test to verify it fails**

```
go test ./internal/pipeline/... -run TestCreatePreservesConfigFields -v
```
Expected: compile error — `pipeline.CreateOpts` doesn't exist yet.

**Step 3: Add fields to `PipelineState` and introduce `CreateOpts`**

In `internal/pipeline/types.go`, add to `PipelineState`:

```go
// Multi-project fields (optional; empty for legacy single-project pipelines)
ConfigPath string `json:"config_path,omitempty"` // abs path to pipeline.yaml
RepoDir    string `json:"repo_dir,omitempty"`     // abs path to git repo root
Namespace  string `json:"namespace,omitempty"`    // "{org}/{repo}", e.g. "myorg/myapp"
```

In `internal/pipeline/store.go`, replace the `Create` signature. Currently it takes 6 positional args; replace with a struct. Add at the top of `store.go`:

```go
// CreateOpts holds options for creating a new pipeline on disk.
type CreateOpts struct {
	Issue      int
	Title      string
	Branch     string
	Worktree   string
	FirstStage string
	GoalGates  map[string]string
	// Multi-project fields (optional)
	ConfigPath string
	RepoDir    string
	Namespace  string
}
```

Update the `Create` method signature and body:

```go
func (s *Store) Create(opts CreateOpts) (*PipelineState, error) {
	if _, err := os.Stat(s.issueDir(opts.Namespace, opts.Issue)); err == nil {
		return nil, fmt.Errorf("pipeline %d already exists", opts.Issue)
	}

	dir := s.issueDir(opts.Namespace, opts.Issue)
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
```

Also add these two helpers to `store.go` (the old `issueDir` stays as a fallback):

```go
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
```

**Step 4: Fix `store.go` methods that use the old `issueDir` signature**

The old `issueDir(issue int)` is now `issueDir(namespace, issue)`. Update all internal usages in `store.go`:

- `pipelinePath(issue int)` — replace with `pipelinePathFor(ps)` where possible; for `Get`, derive from reading the file
- `stageAttemptDir(issue, stage, attempt)` — add `namespace string` param

Update `stageAttemptDir` and all methods that call it:

```go
func (s *Store) stageAttemptDir(namespace string, issue int, stage string, attempt int) string {
	return filepath.Join(s.issueDir(namespace, issue), "stages", stage, fmt.Sprintf("attempt-%d", attempt))
}
```

Methods to update (add `namespace string` as first param):
- `CheckOutputDir(namespace string, issue int, ...)`
- `GateResultDir(namespace string, issue int, ...)`
- `InitStageAttempt(namespace string, issue int, ...)`
- `SaveStageOutcome(namespace string, issue int, ...)`
- `GetStageOutcome(namespace string, issue int, ...)`
- `SaveStageSummary(namespace string, issue int, ...)`
- `GetStageSummary(namespace string, issue int, ...)`
- `SavePrompt(namespace string, issue int, ...)`
- `SaveSessionLog(namespace string, issue int, ...)`
- `GetPrompt(namespace string, issue int, ...)`
- `GetSessionLog(namespace string, issue int, ...)`

**Step 5: Update `Store.Get` to find namespaced files**

Replace the current `Get` with a version that tries the flat path first (legacy), then walks for namespaced:

```go
func (s *Store) Get(issue int) (*PipelineState, error) {
	// Try legacy flat path first
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
```

**Step 6: Update `Store.Update` to write to the correct namespaced path**

```go
func (s *Store) Update(issue int, fn func(*PipelineState)) error {
	ps, err := s.Get(issue)
	if err != nil {
		return err
	}
	fn(ps)
	ps.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	return WriteJSON(s.pipelinePathFor(ps), ps)
}
```

**Step 7: Update `Store.List` to walk recursively**

```go
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
```

**Step 8: Update `Store.Delete`**

```go
func (s *Store) Delete(issue int) error {
	ps, err := s.Get(issue)
	if err != nil {
		return fmt.Errorf("pipeline %d not found", issue)
	}
	return os.RemoveAll(s.issueDir(ps.Namespace, ps.Issue))
}
```

**Step 9: Run tests**

```
go test ./internal/pipeline/... -v
```
Expected: most existing tests pass; `TestCreatePreservesConfigFields` passes.

Fix compilation errors in any tests that call `store.Create` with old positional signature (update to use `CreateOpts{}`).

**Step 10: Commit**

```bash
git add internal/pipeline/types.go internal/pipeline/store.go internal/pipeline/store_test.go
git commit -m "feat(pipeline): add ConfigPath/RepoDir/Namespace to PipelineState with namespaced storage"
```

---

## Task 2: DB schema v5 — `config_path` in `issue_queue`

**Files:**
- Modify: `internal/db/db.go`
- Modify: `internal/db/queries.go`
- Test: `internal/db/db_test.go`

**Step 1: Write the failing test**

Add to `internal/db/db_test.go`:

```go
func TestQueueConfigPath(t *testing.T) {
	d := testDB(t)

	err := d.QueueAdd([]QueueAddItem{
		{Issue: 42, FeatureIntent: "test", ConfigPath: "/projects/myapp/pipeline.yaml"},
	})
	if err != nil {
		t.Fatalf("QueueAdd: %v", err)
	}

	item, err := d.QueueNext()
	if err != nil || item == nil {
		t.Fatalf("QueueNext: err=%v, item=%v", err, item)
	}
	if item.ConfigPath != "/projects/myapp/pipeline.yaml" {
		t.Errorf("ConfigPath = %q, want %q", item.ConfigPath, "/projects/myapp/pipeline.yaml")
	}

	items, err := d.QueueList()
	if err != nil {
		t.Fatalf("QueueList: %v", err)
	}
	if items[0].ConfigPath != "/projects/myapp/pipeline.yaml" {
		t.Errorf("List ConfigPath = %q", items[0].ConfigPath)
	}
}
```

**Step 2: Run test to verify it fails**

```
go test ./internal/db/... -run TestQueueConfigPath -v
```
Expected: FAIL — `QueueAddItem` has no `ConfigPath` field.

**Step 3: Add schema v5 migration**

In `internal/db/db.go`, after `schemaV4`:

```go
const schemaV5 = `
ALTER TABLE issue_queue ADD COLUMN config_path TEXT NOT NULL DEFAULT '';
`
```

Add `schemaV5` application to `Migrate()`, following the same pattern as v1–v4:

```go
// Apply v5 if needed
var v5Count int
err = d.conn.QueryRow("SELECT COUNT(*) FROM schema_version WHERE version = 5").Scan(&v5Count)
if err != nil || v5Count == 0 {
	tx, err := d.conn.Begin()
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.Exec(schemaV5); err != nil {
		return fmt.Errorf("apply schema v5: %w", err)
	}
	if _, err := tx.Exec("INSERT INTO schema_version (version) VALUES (5)"); err != nil {
		return fmt.Errorf("record schema version v5: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit v5: %w", err)
	}
}
```

**Step 4: Update `QueueItem` and `QueueAddItem`**

In `internal/db/queries.go`, add `ConfigPath string` to both structs:

```go
type QueueItem struct {
	ID            int
	Issue         int
	Status        string
	Position      int
	FeatureIntent string
	ConfigPath    string  // NEW
	DependsOn     []int
	AddedAt       string
	StartedAt     string
	FinishedAt    string
}

type QueueAddItem struct {
	Issue         int
	FeatureIntent string
	ConfigPath    string  // NEW
	DependsOn     []int
}
```

**Step 5: Update `QueueAdd`**

Change the INSERT and param to include `config_path`:

```go
stmt, err := tx.Prepare("INSERT INTO issue_queue (issue, position, feature_intent, depends_on, config_path) VALUES (?, ?, ?, ?, ?)")
// ...
if _, err := stmt.Exec(item.Issue, nextPos, item.FeatureIntent, depsJSON, item.ConfigPath); err != nil {
```

**Step 6: Update `QueueList` and `QueueNext`**

In both queries, add `config_path` to the SELECT and Scan calls:

```go
// QueueList SELECT:
`SELECT id, issue, status, position, feature_intent, depends_on, config_path, added_at, started_at, finished_at
 FROM issue_queue ORDER BY position`
// Scan (add after dependsOnJSON):
var configPath string
if err := rows.Scan(..., &dependsOnJSON, &configPath, &item.AddedAt, ...); err != nil { ... }
item.ConfigPath = configPath
```

Apply the same change to `QueueNext`.

**Step 7: Run tests**

```
go test ./internal/db/... -v
```
Expected: all pass including `TestQueueConfigPath`.

**Step 8: Commit**

```bash
git add internal/db/db.go internal/db/queries.go internal/db/db_test.go
git commit -m "feat(db): schema v5 — add config_path to issue_queue"
```

---

## Task 3: `factory queue add --config` flag

**Files:**
- Modify: `internal/cli/queue.go`
- Test: manual smoke test (no unit test needed here — tested via DB layer)

**Step 1: Add flag and resolve config path**

In `internal/cli/queue.go`, in `queueAddCmd.RunE`:

After the block that parses `--depends-on`, read the new flag:

```go
configFlag, _ := cmd.Flags().GetString("config")

// Resolve config path: if --config given, use it; otherwise find default
var resolvedConfigPath string
if configFlag != "" {
	abs, err := filepath.Abs(configFlag)
	if err != nil {
		return fmt.Errorf("resolve --config path: %w", err)
	}
	if _, err := os.Stat(abs); err != nil {
		return fmt.Errorf("config file %q not found", abs)
	}
	resolvedConfigPath = abs
} else {
	// Find the default config so we can store its path too
	if cfg, err := config.LoadDefault(); err == nil {
		// config.LoadDefault doesn't return the path; find it manually
		for _, candidate := range []string{"pipeline.yaml"} {
			if abs, err2 := filepath.Abs(candidate); err2 == nil {
				if _, err3 := os.Stat(abs); err3 == nil {
					resolvedConfigPath = abs
					_ = cfg
					break
				}
			}
		}
	}
}
```

Update the `items` construction to include `ConfigPath`:

```go
items = append(items, db.QueueAddItem{
	Issue:         n,
	FeatureIntent: itemIntent,
	DependsOn:     dependsOn,
	ConfigPath:    resolvedConfigPath,
})
```

You need to add the `"os"`, `"path/filepath"`, and `config` imports:
```go
import (
	...
	"os"
	"path/filepath"
	"github.com/lucasnoah/taintfactory/internal/config"
)
```

**Step 2: Register the flag**

In the `init()` function:

```go
queueAddCmd.Flags().String("config", "", "Path to pipeline.yaml for this project (default: searches ./pipeline.yaml then ~/.factory/config.yaml)")
```

**Step 3: Run tests and build**

```
go test ./internal/cli/... -v
go build ./cmd/factory/
```
Expected: compiles and tests pass.

**Step 4: Smoke test**

```
./factory queue add --config /path/to/your/pipeline.yaml 99
./factory queue list
```
Expected: issue 99 appears in the queue. Verify config_path is stored:
```
sqlite3 ~/.factory/factory.db "SELECT issue, config_path FROM issue_queue WHERE issue=99"
```

**Step 5: Commit**

```bash
git add internal/cli/queue.go
git commit -m "feat(cli): add --config flag to queue add for multi-project support"
```

---

## Task 4: Stage engine accepts per-run config

**Files:**
- Modify: `internal/stage/engine.go`
- Test: `internal/stage/engine_test.go`

The stage engine is initialized with a single `cfg`. For multi-project, `Run()` needs to use the pipeline-specific config if provided.

**Step 1: Write the failing test**

In `internal/stage/engine_test.go`, find an existing test that verifies `maxFixRounds` (e.g., `TestMaxFixRounds`). Add a new test that passes a different config via `RunOpts`:

```go
func TestRunOptsConfigOverride(t *testing.T) {
	// Build engine with default config (max_fix_rounds=1)
	defaultCfg := minimalConfig()
	defaultCfg.Pipeline.MaxFixRounds = 1

	// Override config in RunOpts (max_fix_rounds=5)
	overrideCfg := minimalConfig()
	overrideCfg.Pipeline.MaxFixRounds = 5

	// ... use a mock that records which maxFixRounds was used
	// This test verifies engine.Run respects RunOpts.Config over e.cfg
	e := newTestEngine(t, defaultCfg)
	opts := RunOpts{
		Issue:   42,
		Stage:   "implement",
		Timeout: 30 * time.Minute,
		Config:  overrideCfg,
	}
	// Verify opts.Config is used: check the engine selects maxFixRounds=5
	cfg := e.cfgFor(opts)
	if cfg.Pipeline.MaxFixRounds != 5 {
		t.Errorf("cfgFor = %d max fix rounds, want 5", cfg.Pipeline.MaxFixRounds)
	}
}
```

**Step 2: Run test to verify it fails**

```
go test ./internal/stage/... -run TestRunOptsConfigOverride -v
```
Expected: FAIL — `RunOpts` has no `Config` field; `e.cfgFor` doesn't exist.

**Step 3: Add `Config` to `RunOpts` and `cfgFor` helper**

In `internal/stage/engine.go`, add to `RunOpts`:

```go
type RunOpts struct {
	Issue   int
	Stage   string
	Timeout time.Duration
	Config  *config.PipelineConfig // optional: overrides engine's default config
}
```

Add `cfgFor` method to `Engine`:

```go
// cfgFor returns the effective config for a run: RunOpts.Config if set, else e.cfg.
func (e *Engine) cfgFor(opts RunOpts) *config.PipelineConfig {
	if opts.Config != nil {
		return opts.Config
	}
	return e.cfg
}
```

Update `engine.Run()` to call `e.cfgFor(opts)` at the top and use the result for all config access:

```go
func (e *Engine) Run(opts RunOpts) (*RunResult, error) {
	cfg := e.cfgFor(opts)  // ADD THIS LINE
	// replace all e.cfg references in this function with cfg
	...
}
```

Also update all private helpers that currently use `e.cfg` and are called from within `Run`:
- `findStageCfg(stageID, cfg)` — pass cfg as param instead of using e.cfg
- Any other per-run config access

**Step 4: Run tests**

```
go test ./internal/stage/... -v
```
Expected: all pass.

**Step 5: Commit**

```bash
git add internal/stage/engine.go internal/stage/engine_test.go
git commit -m "feat(stage): RunOpts.Config allows per-run config override"
```

---

## Task 5: Orchestrator — `configFor` and `worktreeFor` helpers

**Files:**
- Modify: `internal/orchestrator/orchestrator.go`
- Test: `internal/orchestrator/orchestrator_test.go`

**Step 1: Write the failing test**

In `internal/orchestrator/orchestrator_test.go` (find or create), add:

```go
func TestConfigForFallsBackToDefault(t *testing.T) {
	defaultCfg := &config.PipelineConfig{}
	defaultCfg.Pipeline.Name = "default"

	o := &Orchestrator{cfg: defaultCfg}

	// Pipeline with no ConfigPath → should return o.cfg
	ps := &pipeline.PipelineState{Issue: 1}
	cfg, err := o.configFor(ps)
	if err != nil {
		t.Fatalf("configFor: %v", err)
	}
	if cfg.Pipeline.Name != "default" {
		t.Errorf("got name %q, want default", cfg.Pipeline.Name)
	}
}
```

**Step 2: Run test to verify it fails**

```
go test ./internal/orchestrator/... -run TestConfigForFallsBackToDefault -v
```
Expected: FAIL — `configFor` doesn't exist.

**Step 3: Add `configFor` to Orchestrator**

In `internal/orchestrator/orchestrator.go`, add after the `logf` method:

```go
// configFor returns the effective config for a pipeline.
// Uses ps.ConfigPath if set (loads from disk); falls back to o.cfg.
func (o *Orchestrator) configFor(ps *pipeline.PipelineState) (*config.PipelineConfig, error) {
	if ps.ConfigPath != "" {
		return config.Load(ps.ConfigPath)
	}
	if o.cfg == nil {
		return nil, fmt.Errorf("no config available for pipeline #%d (no ConfigPath and no default config)", ps.Issue)
	}
	return o.cfg, nil
}

// worktreeFor returns a worktree.Manager scoped to the pipeline's repo directory.
// Falls back to o.wt (the default manager) if ps.RepoDir is not set.
func (o *Orchestrator) worktreeFor(ps *pipeline.PipelineState) *worktree.Manager {
	if ps.RepoDir == "" {
		return o.wt
	}
	wtDir := filepath.Join(ps.RepoDir, "worktrees")
	return worktree.NewManager(&worktree.ExecGit{}, ps.RepoDir, wtDir)
}
```

**Step 4: Add `namespaceFromRepo` helper**

This derives `"myorg/myapp"` from `"github.com/myorg/myapp"`:

```go
// namespaceFromRepo derives a filesystem-safe namespace from a repo URL.
// "github.com/myorg/myapp" → "myorg/myapp"
// Returns "" if the repo string doesn't contain at least two path segments.
func namespaceFromRepo(repo string) string {
	// Strip protocol prefix if present (https://, git://, etc.)
	if idx := strings.Index(repo, "://"); idx >= 0 {
		repo = repo[idx+3:]
	}
	// Strip host (first segment)
	parts := strings.SplitN(repo, "/", 3)
	if len(parts) < 3 {
		return ""
	}
	return parts[1] + "/" + parts[2]
}
```

**Step 5: Run tests**

```
go test ./internal/orchestrator/... -v
```
Expected: all pass.

**Step 6: Commit**

```bash
git add internal/orchestrator/orchestrator.go internal/orchestrator/orchestrator_test.go
git commit -m "feat(orchestrator): add configFor/worktreeFor/namespaceFromRepo helpers"
```

---

## Task 6: Update `Orchestrator.Create` to use per-pipeline config

**Files:**
- Modify: `internal/orchestrator/orchestrator.go`

**Step 1: Extend `CreateOpts`**

Add `ConfigPath string` to `CreateOpts`:

```go
type CreateOpts struct {
	Issue         int
	FeatureIntent string
	ConfigPath    string // optional: abs path to pipeline.yaml for this project
}
```

**Step 2: Update `Create` method**

Replace the start of `Create` to load the right config and derive RepoDir + Namespace:

```go
func (o *Orchestrator) Create(opts CreateOpts) (*pipeline.PipelineState, error) {
	if opts.Issue <= 0 {
		return nil, fmt.Errorf("invalid issue number %d: must be positive", opts.Issue)
	}

	// Load the config for this specific pipeline
	var cfg *config.PipelineConfig
	var repoDir string
	var namespace string

	if opts.ConfigPath != "" {
		var err error
		cfg, err = config.Load(opts.ConfigPath)
		if err != nil {
			return nil, fmt.Errorf("load config %q: %w", opts.ConfigPath, err)
		}
		repoDir = filepath.Dir(opts.ConfigPath)
		namespace = namespaceFromRepo(cfg.Pipeline.Repo)
	} else {
		cfg = o.cfg
		// repoDir and namespace remain empty (legacy behavior)
	}

	// ... rest of Create uses cfg (not o.cfg) ...
```

Change all `o.cfg.` references within `Create` to `cfg.`:
- `o.cfg.Pipeline.Stages[0].ID` → `cfg.Pipeline.Stages[0].ID`
- `o.cfg.Pipeline.Stages` loops → `cfg.Pipeline.Stages`

Update the `store.Create` call to use `pipeline.CreateOpts`:

```go
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
```

Update the worktree creation to use the config-derived repoDir when available:

```go
// Use repoDir from config if provided; otherwise use the manager's built-in repoDir
var wt *worktree.Manager
if repoDir != "" {
	wtDir := filepath.Join(repoDir, "worktrees")
	wt = worktree.NewManager(&worktree.ExecGit{}, repoDir, wtDir)
} else {
	wt = o.wt
}
wtResult, err := wt.Create(worktree.CreateOpts{
	Issue: opts.Issue,
	Title: issue.Title,
})
```

Update `runSetup` to accept cfg:

```go
func (o *Orchestrator) runSetup(worktreePath string, cfg *config.PipelineConfig) error {
	for _, cmdStr := range cfg.Pipeline.Setup {
		...
	}
	return nil
}
// Call as: o.runSetup(wtResult.Path, cfg)
```

Update the `pipelineDir` for issue JSON caching to be namespace-aware:

```go
pipelineDir := filepath.Join(o.store.BaseDir(), namespace, strconv.Itoa(opts.Issue))
if namespace == "" {
	pipelineDir = filepath.Join(o.store.BaseDir(), strconv.Itoa(opts.Issue))
}
_, _ = o.gh.CacheIssue(opts.Issue, pipelineDir)
```

**Step 3: Run tests**

```
go test ./... -v 2>&1 | tail -30
```
Fix any compilation errors.

**Step 4: Commit**

```bash
git add internal/orchestrator/orchestrator.go
git commit -m "feat(orchestrator): Create loads per-pipeline config from ConfigPath"
```

---

## Task 7: Update `Orchestrator.Advance` to use per-pipeline config

**Files:**
- Modify: `internal/orchestrator/orchestrator.go`

**Step 1: Update `Advance`**

At the top of `Advance(issue int)`, after `store.Get(issue)`, load the pipeline-specific config:

```go
func (o *Orchestrator) Advance(issue int) (*AdvanceResult, error) {
	ps, err := o.store.Get(issue)
	if err != nil {
		return nil, fmt.Errorf("get pipeline: %w", err)
	}

	cfg, err := o.configFor(ps)  // ADD THIS
	if err != nil {
		return nil, fmt.Errorf("load config for pipeline #%d: %w", issue, err)
	}
	// ... rest of Advance ...
```

**Step 2: Replace `o.cfg` with `cfg` throughout `Advance`**

- Line ~196: `o.cfg.Pipeline.Defaults.Timeout` → `cfg.Pipeline.Defaults.Timeout`
- The `findStage(currentStage)` call → needs `cfg` param (see step 3)

**Step 3: Update private helpers to accept `cfg` param**

Update these helpers to accept `cfg *config.PipelineConfig` instead of using `o.cfg`:

```go
func (o *Orchestrator) findStage(stageID string, cfg *config.PipelineConfig) *config.Stage {
	for i := range cfg.Pipeline.Stages {
		if cfg.Pipeline.Stages[i].ID == stageID {
			return &cfg.Pipeline.Stages[i]
		}
	}
	return nil
}

func (o *Orchestrator) nextStageID(currentID string, cfg *config.PipelineConfig) string {
	for i, s := range cfg.Pipeline.Stages {
		if s.ID == currentID && i+1 < len(cfg.Pipeline.Stages) {
			return cfg.Pipeline.Stages[i+1].ID
		}
	}
	return ""
}

func (o *Orchestrator) findMergeOnFailTarget(cfg *config.PipelineConfig) string {
	for _, s := range cfg.Pipeline.Stages {
		if s.Type == "merge" {
			return resolveOnFail(s.OnFail)
		}
	}
	return ""
}
```

Update all call sites of these helpers within `orchestrator.go` to pass `cfg`.

**Step 4: Pass cfg to `engine.Run`**

```go
runResult, err = o.engine.Run(stage.RunOpts{
	Issue:   issue,
	Stage:   currentStage,
	Timeout: timeout,
	Config:  cfg,   // ADD THIS
})
```

**Step 5: Update `runMerge` to use per-pipeline worktree manager**

In `runMerge(issue, ps, stageCfg)`, replace `o.wt` with `o.worktreeFor(ps)`:

```go
wt := o.worktreeFor(ps)
// replace all o.wt.Remove(...) calls with wt.Remove(...)
// replace all o.wt.Create(...) calls with wt.Create(...)
```

**Step 6: Update `checkInPipeline` to use per-pipeline config for timeout**

In `checkInPipeline(ps)`, load the config when accessing timeout:

```go
cfg, err := o.configFor(ps)
if err != nil {
	// log and skip gracefully
	return CheckInAction{Issue: ps.Issue, Action: "skip", Message: fmt.Sprintf("config load error: %v", err)}
}
timeout := 30 * time.Minute
if cfg.Pipeline.Defaults.Timeout != "" {
	if d, err := time.ParseDuration(cfg.Pipeline.Defaults.Timeout); err == nil {
		timeout = d
	}
}
```

**Step 7: Run all tests**

```
go test ./... 2>&1 | grep -E "^(ok|FAIL|---)"
```
Expected: all packages pass.

**Step 8: Commit**

```bash
git add internal/orchestrator/orchestrator.go
git commit -m "feat(orchestrator): Advance uses per-pipeline config and worktree manager"
```

---

## Task 8: Update `processQueue` to pass `ConfigPath` to `Create`

**Files:**
- Modify: `internal/orchestrator/orchestrator.go`

**Step 1: Update `processQueue`**

In `processQueue()`, pass `item.ConfigPath` to `Create`:

```go
_, err = o.Create(CreateOpts{
	Issue:         item.Issue,
	FeatureIntent: item.FeatureIntent,
	ConfigPath:    item.ConfigPath,  // ADD THIS
})
```

**Step 2: Run tests**

```
go test ./... 2>&1 | grep -E "^(ok|FAIL|---)"
```

**Step 3: Commit**

```bash
git add internal/orchestrator/orchestrator.go
git commit -m "feat(orchestrator): processQueue passes config_path to pipeline Create"
```

---

## Task 9: Fix all Store callers that use namespace-aware methods

**Files:**
- Search all callers of `store.InitStageAttempt`, `store.SaveStageOutcome`, etc.

**Step 1: Find all callers**

```
grep -rn "store\.InitStageAttempt\|store\.SaveStageOutcome\|store\.GetStageOutcome\|store\.SaveStageSummary\|store\.GetStageSummary\|store\.SavePrompt\|store\.SaveSessionLog\|store\.GetPrompt\|store\.GetSessionLog\|store\.CheckOutputDir\|store\.GateResultDir" internal/
```

These callers are primarily in:
- `internal/stage/engine.go`
- `internal/context/builder.go`

**Step 2: For each caller, pass `ps.Namespace`**

In `internal/stage/engine.go`, the engine calls stage store methods on the pipeline. The engine receives `ps` (from `store.Get`), so it has `ps.Namespace`. Update all calls:

```go
// Before:
store.InitStageAttempt(issue, stage, attempt)
// After:
store.InitStageAttempt(ps.Namespace, issue, stage, attempt)
```

In `internal/context/builder.go`, the builder calls `store.GetStageOutcome(ps.Issue, ...)`. Since `ps` is available in the builder calls, update to:

```go
store.GetStageOutcome(ps.Namespace, ps.Issue, stage, attempt)
```

**Step 3: Fix `session/session.go` and `cli/worktree.go`**

In `internal/session/session.go:132`: the session manager calls `store.Update`. Since it only calls `Update(issue, fn)`, and `Update` now handles namespacing internally via `Get`, no change is needed.

In `internal/cli/worktree.go:48,99`: these call `store.Update(issue, ...)` and `store.Get(issue)`. The updated versions handle namespacing internally, so no change needed.

**Step 4: Run all tests and fix compilation errors**

```
go test ./... 2>&1 | grep -E "^(ok|FAIL|---)"
```

**Step 5: Commit**

```bash
git add internal/stage/engine.go internal/context/builder.go
git commit -m "fix: pass Namespace to Store artifact methods throughout"
```

---

## Task 10: End-to-end smoke test and cleanup

**Step 1: Build**

```
go build -o /tmp/factory ./cmd/factory/
```
Expected: no errors.

**Step 2: Smoke test — single project (regression)**

From the factory repo:
```bash
/tmp/factory config validate
/tmp/factory queue list
```
Expected: works exactly as before.

**Step 3: Smoke test — multi-project queue**

```bash
# Add issue from a different project
/tmp/factory queue add --config /path/to/other-project/pipeline.yaml 77

# Verify config_path stored
sqlite3 ~/.factory/factory.db "SELECT issue, config_path FROM issue_queue WHERE issue=77"

# Verify pipeline list still works
/tmp/factory pipeline list
```

**Step 4: Verify namespaced storage**

After creating a pipeline with a config path:
```bash
/tmp/factory pipeline create 77  # with --config flag if needed, or via queue+check-in
ls ~/.factory/pipelines/
# Should show: 77/ (legacy) OR myorg/myapp/ (namespaced)
```

**Step 5: Run full test suite**

```
go test ./... -count=1
```
Expected: all green.

**Step 6: Final commit if any cleanup was needed**

```bash
git add -A
git commit -m "chore: fix any remaining issues from multi-project Phase 1"
```

---

## Summary of Changes

| File | Change |
|------|--------|
| `internal/pipeline/types.go` | Add `ConfigPath`, `RepoDir`, `Namespace` fields |
| `internal/pipeline/store.go` | `CreateOpts` struct; namespace-aware path resolution; recursive `List/Get` |
| `internal/db/db.go` | Schema v5: `config_path` column on `issue_queue` |
| `internal/db/queries.go` | `ConfigPath` on `QueueItem`/`QueueAddItem`; update queries |
| `internal/cli/queue.go` | `--config` flag on `queue add` |
| `internal/stage/engine.go` | `RunOpts.Config` override; `cfgFor()` helper |
| `internal/orchestrator/orchestrator.go` | `configFor()`, `worktreeFor()`, `namespaceFromRepo()`; `Advance`/`Create`/`processQueue` use per-pipeline config |
| `internal/context/builder.go` | Pass `Namespace` to Store artifact methods |
