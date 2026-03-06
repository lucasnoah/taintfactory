# Multi-Repo Database Autoconfiguration Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Auto-provision per-repo PostgreSQL databases on the shared sidecar and inject DATABASE_URL + env vars into Claude sessions.

**Architecture:** Add `Database` and `Env` fields to pipeline config. A new `dbprov` package connects to the factory Postgres and runs CREATE DATABASE/USER. The session manager exports env vars in tmux before launching Claude. The stage engine runs setup commands in the worktree before the first session. The entrypoint re-checks DBs on pod restart.

**Tech Stack:** Go, PostgreSQL (lib/pq), tmux, YAML config, bash entrypoint

---

### Task 1: Add Database and Env fields to pipeline config

**Files:**
- Modify: `internal/config/types.go:9-21`
- Test: `internal/config/config_test.go`

**Step 1: Write the failing test**

Add to `internal/config/config_test.go`:

```go
func TestDatabaseConfig(t *testing.T) {
	yaml := `
pipeline:
  name: test
  repo: github.com/test/test
  database:
    name: test_dev
    user: testuser
    password: testpass
    migrate: "make migrate"
  env:
    API_KEY: "secret123"
    DEBUG: "true"
  stages:
    - id: s1
      type: agent
`
	cfg, err := LoadFromBytes([]byte(yaml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Pipeline.Database == nil {
		t.Fatal("expected database config to be parsed")
	}
	if cfg.Pipeline.Database.Name != "test_dev" {
		t.Errorf("Database.Name = %q, want %q", cfg.Pipeline.Database.Name, "test_dev")
	}
	if cfg.Pipeline.Database.User != "testuser" {
		t.Errorf("Database.User = %q, want %q", cfg.Pipeline.Database.User, "testuser")
	}
	if cfg.Pipeline.Database.Password != "testpass" {
		t.Errorf("Database.Password = %q, want %q", cfg.Pipeline.Database.Password, "testpass")
	}
	if cfg.Pipeline.Database.Migrate != "make migrate" {
		t.Errorf("Database.Migrate = %q, want %q", cfg.Pipeline.Database.Migrate, "make migrate")
	}
	if cfg.Pipeline.Env["API_KEY"] != "secret123" {
		t.Errorf("Env[API_KEY] = %q, want %q", cfg.Pipeline.Env["API_KEY"], "secret123")
	}
	if cfg.Pipeline.Env["DEBUG"] != "true" {
		t.Errorf("Env[DEBUG] = %q, want %q", cfg.Pipeline.Env["DEBUG"], "true")
	}
}

func TestDatabaseConfigEmpty(t *testing.T) {
	yaml := `
pipeline:
  name: test
  repo: github.com/test/test
  stages:
    - id: s1
      type: agent
`
	cfg, err := LoadFromBytes([]byte(yaml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Pipeline.Database != nil {
		t.Error("expected nil database config when not specified")
	}
	if len(cfg.Pipeline.Env) != 0 {
		t.Errorf("expected empty env map, got %v", cfg.Pipeline.Env)
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/config/ -run TestDatabaseConfig -v`
Expected: FAIL — `cfg.Pipeline.Database` field doesn't exist

**Step 3: Write minimal implementation**

In `internal/config/types.go`, add the `DatabaseConfig` struct and new fields to `Pipeline`:

```go
// DatabaseConfig declares per-repo PostgreSQL database needs.
type DatabaseConfig struct {
	Name     string `yaml:"name"`
	User     string `yaml:"user"`
	Password string `yaml:"password"`
	Migrate  string `yaml:"migrate"`
}

// Pipeline defines the full pipeline: metadata, defaults, checks, and stages.
type Pipeline struct {
	Name              string              `yaml:"name"`
	Repo              string              `yaml:"repo"`
	MaxFixRounds      int                 `yaml:"max_fix_rounds"`
	FreshSessionAfter int                 `yaml:"fresh_session_after"`
	Setup             []string            `yaml:"setup"`
	Database          *DatabaseConfig     `yaml:"database"`
	Env               map[string]string   `yaml:"env"`
	Defaults          StageDefaults       `yaml:"defaults"`
	DefaultChecks     []string            `yaml:"default_checks"`
	Checks            map[string]Check    `yaml:"checks"`
	Stages            []Stage             `yaml:"stages"`
	Vars              map[string]string   `yaml:"vars"`
	Notifications     NotificationsConfig `yaml:"notifications"`
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./internal/config/ -run TestDatabaseConfig -v`
Expected: PASS

**Step 5: Run all config tests**

Run: `go test ./internal/config/ -v`
Expected: All PASS (existing tests unaffected)

**Step 6: Commit**

```bash
git add internal/config/types.go internal/config/config_test.go
git commit -m "feat(config): add Database and Env fields to pipeline config"
```

---

### Task 2: Add DatabaseURL helper to DatabaseConfig

**Files:**
- Modify: `internal/config/types.go`
- Test: `internal/config/config_test.go`

**Step 1: Write the failing test**

```go
func TestDatabaseURL(t *testing.T) {
	db := &DatabaseConfig{
		Name:     "wptl_dev",
		User:     "wptl",
		Password: "wptl_dev",
	}
	want := "postgres://wptl:wptl_dev@localhost:5432/wptl_dev?sslmode=disable"
	got := db.URL()
	if got != want {
		t.Errorf("URL() = %q, want %q", got, want)
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/config/ -run TestDatabaseURL -v`
Expected: FAIL — `URL()` method doesn't exist

**Step 3: Write minimal implementation**

Add to `internal/config/types.go`:

```go
// URL returns a PostgreSQL connection string for this database config.
func (d *DatabaseConfig) URL() string {
	return fmt.Sprintf("postgres://%s:%s@localhost:5432/%s?sslmode=disable", d.User, d.Password, d.Name)
}
```

Add `"fmt"` to the imports.

**Step 4: Run tests**

Run: `go test ./internal/config/ -run TestDatabaseURL -v`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/config/types.go internal/config/config_test.go
git commit -m "feat(config): add DatabaseURL helper method"
```

---

### Task 3: Create database provisioner package

**Files:**
- Create: `internal/dbprov/provision.go`
- Create: `internal/dbprov/provision_test.go`

**Step 1: Write the failing test**

Create `internal/dbprov/provision_test.go`:

```go
package dbprov

import (
	"testing"

	"github.com/lucasnoah/taintfactory/internal/config"
)

func TestBuildProvisionSQL(t *testing.T) {
	dbCfg := &config.DatabaseConfig{
		Name:     "wptl_dev",
		User:     "wptl",
		Password: "wptl_pass",
	}

	stmts := BuildProvisionSQL(dbCfg)
	if len(stmts) != 3 {
		t.Fatalf("expected 3 SQL statements, got %d", len(stmts))
	}

	// Check CREATE USER
	if stmts[0] != `CREATE USER "wptl" WITH PASSWORD 'wptl_pass'` {
		t.Errorf("stmt[0] = %q", stmts[0])
	}
	// Check CREATE DATABASE
	if stmts[1] != `CREATE DATABASE "wptl_dev" OWNER "wptl"` {
		t.Errorf("stmt[1] = %q", stmts[1])
	}
	// Check GRANT
	if stmts[2] != `GRANT ALL PRIVILEGES ON DATABASE "wptl_dev" TO "wptl"` {
		t.Errorf("stmt[2] = %q", stmts[2])
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/dbprov/ -run TestBuildProvisionSQL -v`
Expected: FAIL — package doesn't exist

**Step 3: Write minimal implementation**

Create `internal/dbprov/provision.go`:

```go
package dbprov

import (
	"database/sql"
	"fmt"
	"strings"

	"github.com/lucasnoah/taintfactory/internal/config"
)

// BuildProvisionSQL returns the SQL statements needed to provision a database.
// These must be run against the postgres (admin) database, not the target database.
func BuildProvisionSQL(cfg *config.DatabaseConfig) []string {
	return []string{
		fmt.Sprintf(`CREATE USER "%s" WITH PASSWORD '%s'`, cfg.User, strings.ReplaceAll(cfg.Password, "'", "''")),
		fmt.Sprintf(`CREATE DATABASE "%s" OWNER "%s"`, cfg.Name, cfg.User),
		fmt.Sprintf(`GRANT ALL PRIVILEGES ON DATABASE "%s" TO "%s"`, cfg.Name, cfg.User),
	}
}

// Provision creates the database and user on the given admin connection.
// Idempotent: silently ignores "already exists" errors.
func Provision(adminConn *sql.DB, cfg *config.DatabaseConfig) error {
	for _, stmt := range BuildProvisionSQL(cfg) {
		_, err := adminConn.Exec(stmt)
		if err != nil {
			// Ignore "already exists" errors (42710 = duplicate_object, 42P04 = duplicate_database)
			errStr := err.Error()
			if strings.Contains(errStr, "already exists") ||
				strings.Contains(errStr, "42710") ||
				strings.Contains(errStr, "42P04") {
				continue
			}
			return fmt.Errorf("provision %q: %w", stmt, err)
		}
	}
	return nil
}
```

**Step 4: Run tests**

Run: `go test ./internal/dbprov/ -v`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/dbprov/provision.go internal/dbprov/provision_test.go
git commit -m "feat(dbprov): add database provisioner package"
```

---

### Task 4: Add env var export to session creation

This is the core change — make `session.Create` export env vars in the tmux session before launching Claude.

**Files:**
- Modify: `internal/session/session.go:17-27` (CreateOpts)
- Modify: `internal/session/session.go:68-137` (Create method)
- Test: `internal/session/session_test.go`

**Step 1: Write the failing test**

Add to `internal/session/session_test.go`:

```go
func TestCreate_WithEnvVars(t *testing.T) {
	tmux := newMockTmux()
	d := testDB(t)
	mgr := NewManager(tmux, d, nil)

	err := mgr.Create(CreateOpts{
		Name:    "test-env",
		Workdir: "/tmp/myproject",
		Issue:   42,
		Stage:   "impl",
		Env: map[string]string{
			"DATABASE_URL": "postgres://wptl:wptl_dev@localhost:5432/wptl_dev",
			"API_KEY":      "secret123",
		},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Verify export commands were sent before the claude command
	var exportCalls []string
	for _, c := range tmux.calls {
		if strings.Contains(c, "export ") {
			exportCalls = append(exportCalls, c)
		}
	}
	if len(exportCalls) != 2 {
		t.Fatalf("expected 2 export calls, got %d: %v", len(exportCalls), tmux.calls)
	}

	// Verify both vars were exported
	allCalls := strings.Join(tmux.calls, "\n")
	if !strings.Contains(allCalls, "DATABASE_URL") {
		t.Error("missing DATABASE_URL export")
	}
	if !strings.Contains(allCalls, "API_KEY") {
		t.Error("missing API_KEY export")
	}

	// Verify exports came before the claude command
	lastExportIdx := -1
	claudeIdx := -1
	for i, c := range tmux.calls {
		if strings.Contains(c, "export ") {
			lastExportIdx = i
		}
		if strings.Contains(c, "claude") {
			claudeIdx = i
		}
	}
	if lastExportIdx >= claudeIdx {
		t.Errorf("exports should come before claude command: exports at %d, claude at %d", lastExportIdx, claudeIdx)
	}
}

func TestCreate_EmptyEnvVars(t *testing.T) {
	tmux := newMockTmux()
	d := testDB(t)
	mgr := NewManager(tmux, d, nil)

	err := mgr.Create(CreateOpts{
		Name:  "test-noenv",
		Issue: 1,
		Stage: "plan",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Verify no export calls when Env is nil
	for _, c := range tmux.calls {
		if strings.Contains(c, "export ") {
			t.Errorf("unexpected export call: %q", c)
		}
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/session/ -run TestCreate_WithEnvVars -v`
Expected: FAIL — `CreateOpts` has no `Env` field

**Step 3: Write minimal implementation**

In `internal/session/session.go`, add `Env` field to `CreateOpts`:

```go
type CreateOpts struct {
	Name        string
	Workdir     string
	Flags       string
	Model       string
	Issue       int
	Stage       string
	Interactive bool
	Env         map[string]string // env vars to export in tmux before launching claude
}
```

In the `Create` method, after the `unset CLAUDECODE` block (line ~114) and before `buildClaudeCommand`, add env var export:

```go
	// Export env vars into the tmux session
	if len(opts.Env) > 0 {
		// Sort keys for deterministic order
		keys := make([]string, 0, len(opts.Env))
		for k := range opts.Env {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			exportCmd := fmt.Sprintf("export %s=%s", k, shellQuote(opts.Env[k]))
			if err := m.tmux.SendKeys(opts.Name, exportCmd); err != nil {
				return fmt.Errorf("export %s: %w", k, err)
			}
		}
	}
```

Add `"sort"` to the imports.

**Step 4: Run tests**

Run: `go test ./internal/session/ -v`
Expected: All PASS (existing tests still pass — they use nil/empty Env)

**Step 5: Commit**

```bash
git add internal/session/session.go internal/session/session_test.go
git commit -m "feat(session): export env vars in tmux before launching claude"
```

---

### Task 5: Wire env vars through the stage engine

The stage engine creates sessions. It needs to read the pipeline config's `Database` and `Env` fields and pass them as `CreateOpts.Env`.

**Files:**
- Modify: `internal/stage/engine.go:442-467` (createAndRunSession)
- Test: `internal/stage/engine_test.go`

**Step 1: Write the failing test**

Add to `internal/stage/engine_test.go`:

```go
func TestRunAgent_EnvVarsPassedToSession(t *testing.T) {
	checkCmd := &mockCheckCmd{}

	cfg := testConfig(
		[]config.Stage{{ID: "impl", Type: "agent", SkipChecks: true, PromptTemplate: "impl.md"}},
		nil,
	)
	cfg.Pipeline.Database = &config.DatabaseConfig{
		Name:     "test_dev",
		User:     "testuser",
		Password: "testpass",
	}
	cfg.Pipeline.Env = map[string]string{
		"CUSTOM_VAR": "hello",
	}

	engine, tmuxMock, database, store := setupEngine(t, cfg, checkCmd)
	createTestPipeline(t, store, 1)
	installTemplate(t, store, 1, "impl.md", "Implement {{issue_number}}")

	go func() {
		time.Sleep(50 * time.Millisecond)
		simulateWorkIdle(t, database, "1-impl-1", 1, "impl")
	}()

	result, err := engine.Run(RunOpts{Issue: 1, Stage: "impl", Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Outcome != "success" {
		t.Errorf("expected success, got %q", result.Outcome)
	}

	// Verify DATABASE_URL and CUSTOM_VAR were exported in tmux
	var exports []string
	for _, s := range tmuxMock.sent {
		if strings.Contains(s.Keys, "export ") {
			exports = append(exports, s.Keys)
		}
	}

	foundDB := false
	foundCustom := false
	for _, e := range exports {
		if strings.Contains(e, "DATABASE_URL") && strings.Contains(e, "test_dev") {
			foundDB = true
		}
		if strings.Contains(e, "CUSTOM_VAR") && strings.Contains(e, "hello") {
			foundCustom = true
		}
	}
	if !foundDB {
		t.Errorf("DATABASE_URL not found in exports: %v", exports)
	}
	if !foundCustom {
		t.Errorf("CUSTOM_VAR not found in exports: %v", exports)
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/stage/ -run TestRunAgent_EnvVarsPassedToSession -v`
Expected: FAIL — `cfg.Pipeline.Database` field doesn't exist yet in this package's test config helper (it will compile since Task 1 added the field, but no env vars will be exported)

**Step 3: Write minimal implementation**

In `internal/stage/engine.go`, modify `createAndRunSession` to build the env map and pass it to session creation. After the model resolution (around line 454), add:

```go
	// Build env vars map from pipeline config
	envVars := make(map[string]string)
	if cfg.Pipeline.Database != nil {
		envVars["DATABASE_URL"] = cfg.Pipeline.Database.URL()
	}
	for k, v := range cfg.Pipeline.Env {
		envVars[k] = v
	}
```

Then in the `e.sessions.Create(session.CreateOpts{...})` call, add the Env field:

```go
	if err := e.sessions.Create(session.CreateOpts{
		Name:        name,
		Workdir:     ps.Worktree,
		Flags:       flags,
		Model:       model,
		Issue:       opts.Issue,
		Stage:       opts.Stage,
		Interactive: true,
		Env:         envVars,
	}); err != nil {
```

**Step 4: Run tests**

Run: `go test ./internal/stage/ -v`
Expected: All PASS

**Step 5: Commit**

```bash
git add internal/stage/engine.go internal/stage/engine_test.go
git commit -m "feat(stage): wire database URL and env vars into session creation"
```

---

### Task 6: Execute setup commands before first stage

The `setup` field in pipeline.yaml lists commands to run in the worktree before the first session. Currently parsed but never executed.

**Files:**
- Modify: `internal/stage/engine.go`
- Test: `internal/stage/engine_test.go`

**Step 1: Write the failing test**

Add to `internal/stage/engine_test.go`:

```go
func TestRunAgent_SetupCommandsExecuted(t *testing.T) {
	checkCmd := &mockCheckCmd{}

	cfg := testConfig(
		[]config.Stage{{ID: "impl", Type: "agent", SkipChecks: true, PromptTemplate: "impl.md"}},
		nil,
	)
	cfg.Pipeline.Setup = []string{"echo setup1", "echo setup2"}

	engine, _, database, store := setupEngine(t, cfg, checkCmd)
	createTestPipeline(t, store, 1)
	installTemplate(t, store, 1, "impl.md", "Implement {{issue_number}}")

	go func() {
		time.Sleep(50 * time.Millisecond)
		simulateWorkIdle(t, database, "1-impl-1", 1, "impl")
	}()

	result, err := engine.Run(RunOpts{Issue: 1, Stage: "impl", Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Outcome != "success" {
		t.Errorf("expected success, got %q", result.Outcome)
	}
}
```

**Step 2: Run test — this should already pass since setup is optional**

Run: `go test ./internal/stage/ -run TestRunAgent_SetupCommandsExecuted -v`
Expected: PASS (setup commands are not blocking yet)

**Step 3: Write the implementation**

Add a `runSetup` method to the engine in `internal/stage/engine.go`:

```go
// runSetup executes pipeline setup commands in the worktree directory.
// Commands run with the pipeline's env vars (including DATABASE_URL) available.
func (e *Engine) runSetup(worktree string, cfg *config.PipelineConfig) error {
	if len(cfg.Pipeline.Setup) == 0 {
		return nil
	}

	// Build env vars for setup commands
	env := os.Environ()
	if cfg.Pipeline.Database != nil {
		env = append(env, "DATABASE_URL="+cfg.Pipeline.Database.URL())
	}
	for k, v := range cfg.Pipeline.Env {
		env = append(env, k+"="+v)
	}

	for _, cmdStr := range cfg.Pipeline.Setup {
		e.logf("setup: %s", cmdStr)
		cmd := exec.Command("sh", "-c", cmdStr)
		cmd.Dir = worktree
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("setup command %q failed: %w\nOutput: %s", cmdStr, err, string(out))
		}
	}

	// Run database migrations if configured
	if cfg.Pipeline.Database != nil && cfg.Pipeline.Database.Migrate != "" {
		e.logf("setup: running migrations: %s", cfg.Pipeline.Database.Migrate)
		cmd := exec.Command("sh", "-c", cfg.Pipeline.Database.Migrate)
		cmd.Dir = worktree
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("migration %q failed: %w\nOutput: %s", cfg.Pipeline.Database.Migrate, err, string(out))
		}
	}

	return nil
}
```

Add `"os"` to the imports in engine.go (it's already imported for exec).

Call `runSetup` in the `Run` method, after getting the pipeline state and before building the prompt. In `Run()`, after `findStageConfig` (around line 127) and the checks_only early return, add:

```go
	// Run setup commands in worktree (deps install, migrations)
	e.logf("running setup commands...")
	if err := e.runSetup(ps.Worktree, cfg); err != nil {
		return nil, fmt.Errorf("setup: %w", err)
	}
```

**Step 4: Run tests**

Run: `go test ./internal/stage/ -v`
Expected: All PASS

**Step 5: Commit**

```bash
git add internal/stage/engine.go internal/stage/engine_test.go
git commit -m "feat(stage): execute setup commands and migrations before first session"
```

---

### Task 7: Wire provisioner into `factory repo add`

**Files:**
- Modify: `internal/cli/repo.go:20-57`
- No unit test (integration-level — calls real Postgres). Test manually.

**Step 1: Modify `repoAddCmd` to provision database**

In `internal/cli/repo.go`, after the repo is registered in the DB (after `d.RepoAdd(...)` succeeds), add database provisioning:

```go
		// Provision database if pipeline config has database section
		if configPath != "" {
			pCfg, cfgErr := config.Load(configPath)
			if cfgErr != nil {
				fmt.Fprintf(os.Stderr, "warning: could not load pipeline config: %v\n", cfgErr)
			} else if pCfg.Pipeline.Database != nil {
				adminConnStr := os.Getenv("DATABASE_URL")
				if adminConnStr == "" {
					fmt.Fprintf(os.Stderr, "warning: DATABASE_URL not set, skipping database provisioning\n")
				} else {
					adminDB, dbErr := sql.Open("postgres", adminConnStr)
					if dbErr != nil {
						return fmt.Errorf("connect to admin db: %w", dbErr)
					}
					defer adminDB.Close()
					if provErr := dbprov.Provision(adminDB, pCfg.Pipeline.Database); provErr != nil {
						return fmt.Errorf("provision database: %w", provErr)
					}
					fmt.Printf("Provisioned database %q (user: %s)\n", pCfg.Pipeline.Database.Name, pCfg.Pipeline.Database.User)
				}
			}
		}
```

Add imports:
```go
	"database/sql"
	"github.com/lucasnoah/taintfactory/internal/config"
	"github.com/lucasnoah/taintfactory/internal/dbprov"
```

**Step 2: Run build to verify it compiles**

Run: `go build ./cmd/factory/`
Expected: Compiles cleanly

**Step 3: Run all tests**

Run: `go test ./...`
Expected: All PASS

**Step 4: Commit**

```bash
git add internal/cli/repo.go
git commit -m "feat(cli): provision database on repo add"
```

---

### Task 8: Add `factory repo provision-db` command

A standalone command to re-provision databases for already-registered repos. Useful for manual recovery and entrypoint re-checks.

**Files:**
- Modify: `internal/cli/repo.go`

**Step 1: Add the command**

```go
var repoProvisionDBCmd = &cobra.Command{
	Use:   "provision-db [namespace]",
	Short: "Provision databases for registered repos",
	Long:  "Creates databases and users for repos with a database config. If namespace is given, provisions only that repo. Otherwise provisions all.",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		d, cleanup, err := openDB()
		if err != nil {
			return err
		}
		defer cleanup()

		adminConnStr := os.Getenv("DATABASE_URL")
		if adminConnStr == "" {
			return fmt.Errorf("DATABASE_URL not set")
		}
		adminDB, err := sql.Open("postgres", adminConnStr)
		if err != nil {
			return fmt.Errorf("connect to admin db: %w", err)
		}
		defer adminDB.Close()

		var repos []db.RepoRecord
		if len(args) > 0 {
			r, err := d.RepoGetByNamespace(args[0])
			if err != nil {
				return err
			}
			repos = []db.RepoRecord{*r}
		} else {
			repos, err = d.RepoList()
			if err != nil {
				return err
			}
		}

		for _, r := range repos {
			if r.ConfigPath == "" {
				continue
			}
			pCfg, err := config.Load(r.ConfigPath)
			if err != nil {
				fmt.Fprintf(os.Stderr, "warning: %s: %v\n", r.Namespace, err)
				continue
			}
			if pCfg.Pipeline.Database == nil {
				continue
			}
			if err := dbprov.Provision(adminDB, pCfg.Pipeline.Database); err != nil {
				fmt.Fprintf(os.Stderr, "error: %s: %v\n", r.Namespace, err)
				continue
			}
			fmt.Printf("Provisioned %s → database %q\n", r.Namespace, pCfg.Pipeline.Database.Name)
		}
		return nil
	},
}
```

Register in `init()`:
```go
repoCmd.AddCommand(repoProvisionDBCmd)
```

**Step 2: Run build**

Run: `go build ./cmd/factory/`
Expected: Compiles

**Step 3: Commit**

```bash
git add internal/cli/repo.go
git commit -m "feat(cli): add 'repo provision-db' command for re-provisioning"
```

---

### Task 9: Update entrypoint for DB re-provisioning on restart

**Files:**
- Modify: `deploy/entrypoint.sh`

**Step 1: Add DB provisioning after repo cloning**

After the repo cloning sections (after line 78), before the serve command (before line 80), add:

```bash
# Provision databases for repos that declare them.
# Idempotent — CREATE IF NOT EXISTS semantics.
echo "Provisioning databases for registered repos..."
factory repo provision-db 2>&1 || echo "warning: database provisioning failed (may be first boot)"
```

**Step 2: Verify the script is syntactically valid**

Run: `bash -n deploy/entrypoint.sh`
Expected: No errors

**Step 3: Commit**

```bash
git add deploy/entrypoint.sh
git commit -m "feat(deploy): provision repo databases on pod startup"
```

---

### Task 10: Integration test — verify full flow

**Files:**
- Test: manual verification

**Step 1: Run all unit tests**

Run: `go test ./...`
Expected: All PASS

**Step 2: Build the binary**

Run: `go build -o /tmp/factory ./cmd/factory/`
Expected: Compiles cleanly

**Step 3: Verify config parsing with wptl pipeline.yaml**

The wptl pipeline.yaml will need its `database:` section added. Update `/Users/lucas/Documents/wptl/pipeline.yaml` to add:

```yaml
pipeline:
  name: wptl
  repo: lucasnoah/wptl

  database:
    name: wptl_dev
    user: wptl
    password: wptl_dev
    migrate: "make migrate"

  # ... rest unchanged
```

**Step 4: Commit everything and prepare for deploy**

```bash
git add -A
git commit -m "feat: multi-repo database autoconfiguration

Each repo can declare a database: section in pipeline.yaml.
Factory auto-provisions PostgreSQL databases on the shared sidecar,
injects DATABASE_URL into tmux sessions, and runs setup commands
before the first stage of each issue."
```

**Step 5: Deploy**

Build and push Docker image, roll the pod:

```bash
doctl registry login
docker buildx build --platform linux/amd64 -t registry.digitalocean.com/personalcluster/taintfactory:<commit> -t registry.digitalocean.com/personalcluster/taintfactory:latest --push .
kubectl set image statefulset/factory factory=registry.digitalocean.com/personalcluster/taintfactory:<commit> -n taintfactory
kubectl delete pod factory-0 -n taintfactory
```

Then re-register wptl with its updated pipeline.yaml (which now has the database section), or run `factory repo provision-db` to provision the DB for the already-registered repo.
