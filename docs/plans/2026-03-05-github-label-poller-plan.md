# GitHub Label Poller + Repo Registry Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Auto-detect GitHub issues with a configured label and enqueue them into the factory pipeline.

**Architecture:** A `repos` table in PostgreSQL stores registered repos with poll config. The orchestrator's CheckIn loop polls GitHub every ~2 min for labeled issues, deduplicates against the queue/pipeline store, derives intent via LLM, and auto-enqueues. CLI and web UI provide CRUD for repo management.

**Tech Stack:** Go, PostgreSQL, Cobra CLI, `gh` CLI, html/template

---

### Task 1: DB migration — `repos` table

**Files:**
- Modify: `internal/db/db.go`
- Test: `internal/db/db_test.go` (if exists, otherwise manual verify)

**Step 1: Add the `repos` table to the schema constant**

In `internal/db/db.go`, add the following SQL after the `issue_queue` table definition (after the `CREATE INDEX ... idx_queue_ns_issue` line, before the closing backtick of the `schema` const):

```sql
CREATE TABLE IF NOT EXISTS repos (
    id             SERIAL PRIMARY KEY,
    namespace      TEXT UNIQUE NOT NULL,
    repo_url       TEXT NOT NULL,
    local_path     TEXT NOT NULL,
    config_path    TEXT NOT NULL,
    poll_label     TEXT,
    poll_interval  INTEGER NOT NULL DEFAULT 120,
    active         BOOLEAN NOT NULL DEFAULT true,
    added_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_repos_active ON repos(active);
```

**Step 2: Verify migration applies cleanly**

Run:
```bash
go build ./... && echo "build ok"
```
Expected: `build ok`

**Step 3: Commit**

```bash
git add internal/db/db.go
git commit -m "feat(db): add repos table for repo registry"
```

---

### Task 2: DB queries for repo CRUD

**Files:**
- Modify: `internal/db/queries.go`

**Step 1: Write the failing test**

Create `internal/db/repo_queries_test.go`:

```go
package db

import (
	"testing"
)

func TestRepoAdd(t *testing.T) {
	d := openTestDB(t)

	err := d.RepoAdd(RepoRecord{
		Namespace:    "mbrucker/deathcookies",
		RepoURL:      "github.com/mbrucker/deathcookies",
		LocalPath:    "/data/repos/deathcookies",
		ConfigPath:   "/data/repos/deathcookies/pipeline.yaml",
		PollLabel:    "implementation",
		PollInterval: 120,
	})
	if err != nil {
		t.Fatalf("RepoAdd: %v", err)
	}

	repos, err := d.RepoList()
	if err != nil {
		t.Fatalf("RepoList: %v", err)
	}
	if len(repos) != 1 {
		t.Fatalf("expected 1 repo, got %d", len(repos))
	}
	if repos[0].Namespace != "mbrucker/deathcookies" {
		t.Errorf("Namespace = %q, want mbrucker/deathcookies", repos[0].Namespace)
	}
	if repos[0].PollLabel != "implementation" {
		t.Errorf("PollLabel = %q, want implementation", repos[0].PollLabel)
	}
	if !repos[0].Active {
		t.Error("expected Active = true")
	}
}

func TestRepoAddDuplicate(t *testing.T) {
	d := openTestDB(t)

	rec := RepoRecord{
		Namespace:  "org/repo",
		RepoURL:    "github.com/org/repo",
		LocalPath:  "/data/repos/repo",
		ConfigPath: "/data/repos/repo/pipeline.yaml",
	}
	if err := d.RepoAdd(rec); err != nil {
		t.Fatalf("first add: %v", err)
	}
	err := d.RepoAdd(rec)
	if err == nil {
		t.Fatal("expected error on duplicate, got nil")
	}
}

func TestRepoRemove(t *testing.T) {
	d := openTestDB(t)

	_ = d.RepoAdd(RepoRecord{
		Namespace:  "org/repo",
		RepoURL:    "github.com/org/repo",
		LocalPath:  "/data/repos/repo",
		ConfigPath: "/data/repos/repo/pipeline.yaml",
	})

	if err := d.RepoRemove("org/repo"); err != nil {
		t.Fatalf("RepoRemove: %v", err)
	}

	repos, _ := d.RepoList()
	if len(repos) != 0 {
		t.Errorf("expected 0 repos after remove, got %d", len(repos))
	}
}

func TestRepoUpdate(t *testing.T) {
	d := openTestDB(t)

	_ = d.RepoAdd(RepoRecord{
		Namespace:    "org/repo",
		RepoURL:      "github.com/org/repo",
		LocalPath:    "/data/repos/repo",
		ConfigPath:   "/data/repos/repo/pipeline.yaml",
		PollLabel:    "old-label",
		PollInterval: 60,
		Active:       true,
	})

	err := d.RepoUpdate("org/repo", RepoUpdateOpts{
		PollLabel:    strPtr("new-label"),
		PollInterval: intPtr(300),
		Active:       boolPtr(false),
	})
	if err != nil {
		t.Fatalf("RepoUpdate: %v", err)
	}

	repos, _ := d.RepoList()
	if repos[0].PollLabel != "new-label" {
		t.Errorf("PollLabel = %q, want new-label", repos[0].PollLabel)
	}
	if repos[0].PollInterval != 300 {
		t.Errorf("PollInterval = %d, want 300", repos[0].PollInterval)
	}
	if repos[0].Active {
		t.Error("expected Active = false")
	}
}

func TestRepoGetPollable(t *testing.T) {
	d := openTestDB(t)

	_ = d.RepoAdd(RepoRecord{
		Namespace: "active/with-label", RepoURL: "github.com/a/b",
		LocalPath: "/a", ConfigPath: "/a/p.yaml",
		PollLabel: "impl", PollInterval: 120, Active: true,
	})
	_ = d.RepoAdd(RepoRecord{
		Namespace: "active/no-label", RepoURL: "github.com/c/d",
		LocalPath: "/b", ConfigPath: "/b/p.yaml",
		PollLabel: "", PollInterval: 120, Active: true,
	})
	_ = d.RepoAdd(RepoRecord{
		Namespace: "inactive/with-label", RepoURL: "github.com/e/f",
		LocalPath: "/c", ConfigPath: "/c/p.yaml",
		PollLabel: "impl", PollInterval: 120, Active: false,
	})

	repos, err := d.RepoGetPollable()
	if err != nil {
		t.Fatalf("RepoGetPollable: %v", err)
	}
	if len(repos) != 1 {
		t.Fatalf("expected 1 pollable repo, got %d", len(repos))
	}
	if repos[0].Namespace != "active/with-label" {
		t.Errorf("got %q", repos[0].Namespace)
	}
}

// helpers
func strPtr(s string) *string  { return &s }
func intPtr(i int) *int        { return &i }
func boolPtr(b bool) *bool     { return &b }
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/db/ -run TestRepo -v -count=1`
Expected: FAIL — `RepoAdd`, `RepoRecord` etc. not defined

**Step 3: Write the implementation**

Add to `internal/db/queries.go`:

```go
// RepoRecord represents a registered repository.
type RepoRecord struct {
	ID           int
	Namespace    string
	RepoURL      string
	LocalPath    string
	ConfigPath   string
	PollLabel    string
	PollInterval int
	Active       bool
	AddedAt      string
}

// RepoUpdateOpts holds optional fields for updating a repo. Nil means "don't change".
type RepoUpdateOpts struct {
	PollLabel    *string
	PollInterval *int
	Active       *bool
}

// RepoAdd inserts a new repo into the registry.
func (d *DB) RepoAdd(r RepoRecord) error {
	_, err := d.conn.Exec(
		`INSERT INTO repos (namespace, repo_url, local_path, config_path, poll_label, poll_interval, active)
		 VALUES ($1, $2, $3, $4, NULLIF($5, ''), $6, $7)`,
		r.Namespace, r.RepoURL, r.LocalPath, r.ConfigPath, r.PollLabel, r.PollInterval, r.Active,
	)
	if err != nil {
		if strings.Contains(err.Error(), "duplicate key") || strings.Contains(err.Error(), "unique constraint") {
			return fmt.Errorf("repo %q already registered", r.Namespace)
		}
		return fmt.Errorf("insert repo: %w", err)
	}
	return nil
}

// RepoList returns all registered repos.
func (d *DB) RepoList() ([]RepoRecord, error) {
	rows, err := d.conn.Query(
		`SELECT id, namespace, repo_url, local_path, config_path, COALESCE(poll_label, ''), poll_interval, active, added_at
		 FROM repos ORDER BY namespace`)
	if err != nil {
		return nil, fmt.Errorf("list repos: %w", err)
	}
	defer rows.Close()

	var repos []RepoRecord
	for rows.Next() {
		var r RepoRecord
		if err := rows.Scan(&r.ID, &r.Namespace, &r.RepoURL, &r.LocalPath, &r.ConfigPath, &r.PollLabel, &r.PollInterval, &r.Active, &r.AddedAt); err != nil {
			return nil, fmt.Errorf("scan repo: %w", err)
		}
		repos = append(repos, r)
	}
	return repos, rows.Err()
}

// RepoRemove deletes a repo by namespace.
func (d *DB) RepoRemove(namespace string) error {
	result, err := d.conn.Exec(`DELETE FROM repos WHERE namespace = $1`, namespace)
	if err != nil {
		return fmt.Errorf("delete repo: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("repo %q not found", namespace)
	}
	return nil
}

// RepoUpdate updates optional fields on a repo.
func (d *DB) RepoUpdate(namespace string, opts RepoUpdateOpts) error {
	sets := []string{}
	args := []interface{}{}
	i := 1

	if opts.PollLabel != nil {
		sets = append(sets, fmt.Sprintf("poll_label = NULLIF($%d, '')", i))
		args = append(args, *opts.PollLabel)
		i++
	}
	if opts.PollInterval != nil {
		sets = append(sets, fmt.Sprintf("poll_interval = $%d", i))
		args = append(args, *opts.PollInterval)
		i++
	}
	if opts.Active != nil {
		sets = append(sets, fmt.Sprintf("active = $%d", i))
		args = append(args, *opts.Active)
		i++
	}

	if len(sets) == 0 {
		return nil
	}

	query := fmt.Sprintf("UPDATE repos SET %s WHERE namespace = $%d", strings.Join(sets, ", "), i)
	args = append(args, namespace)

	result, err := d.conn.Exec(query, args...)
	if err != nil {
		return fmt.Errorf("update repo: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("repo %q not found", namespace)
	}
	return nil
}

// RepoGetPollable returns repos that are active and have a poll_label set.
func (d *DB) RepoGetPollable() ([]RepoRecord, error) {
	rows, err := d.conn.Query(
		`SELECT id, namespace, repo_url, local_path, config_path, poll_label, poll_interval, active, added_at
		 FROM repos WHERE active = true AND poll_label IS NOT NULL ORDER BY namespace`)
	if err != nil {
		return nil, fmt.Errorf("get pollable repos: %w", err)
	}
	defer rows.Close()

	var repos []RepoRecord
	for rows.Next() {
		var r RepoRecord
		if err := rows.Scan(&r.ID, &r.Namespace, &r.RepoURL, &r.LocalPath, &r.ConfigPath, &r.PollLabel, &r.PollInterval, &r.Active, &r.AddedAt); err != nil {
			return nil, fmt.Errorf("scan repo: %w", err)
		}
		repos = append(repos, r)
	}
	return repos, rows.Err()
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./internal/db/ -run TestRepo -v -count=1`
Expected: PASS (all 5 tests)

Note: If `openTestDB` doesn't exist or uses SQLite, you may need to set `DATABASE_URL` to a test PostgreSQL instance. Check existing test patterns in `internal/db/`.

**Step 5: Commit**

```bash
git add internal/db/queries.go internal/db/repo_queries_test.go
git commit -m "feat(db): add repo registry CRUD queries and tests"
```

---

### Task 3: GitHub `ListLabeledIssues` function

**Files:**
- Modify: `internal/github/github.go`
- Create: `internal/github/github_test.go` (or add to existing)

**Step 1: Write the failing test**

Add to (or create) `internal/github/github_test.go`:

```go
package github

import (
	"encoding/json"
	"fmt"
	"testing"
)

type mockCmd struct {
	output string
	err    error
}

func (m *mockCmd) Run(args ...string) (string, error) {
	return m.output, m.err
}

func TestListLabeledIssues(t *testing.T) {
	issues := []IssueSummary{
		{Number: 100, Title: "Add login page", Body: "Implement login"},
		{Number: 101, Title: "Fix bug", Body: "Something broken"},
	}
	data, _ := json.Marshal(issues)

	client := NewClient(&mockCmd{output: string(data)})
	client = client.WithRepo("mbrucker/deathcookies")

	got, err := client.ListLabeledIssues("implementation")
	if err != nil {
		t.Fatalf("ListLabeledIssues: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 issues, got %d", len(got))
	}
	if got[0].Number != 100 {
		t.Errorf("got[0].Number = %d, want 100", got[0].Number)
	}
}

func TestListLabeledIssues_Empty(t *testing.T) {
	client := NewClient(&mockCmd{output: "[]"})
	client = client.WithRepo("org/repo")

	got, err := client.ListLabeledIssues("no-match")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected 0 issues, got %d", len(got))
	}
}

func TestListLabeledIssues_GhError(t *testing.T) {
	client := NewClient(&mockCmd{output: "", err: fmt.Errorf("gh failed")})
	client = client.WithRepo("org/repo")

	_, err := client.ListLabeledIssues("label")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/github/ -run TestListLabeled -v -count=1`
Expected: FAIL — `IssueSummary` and `ListLabeledIssues` not defined

**Step 3: Write the implementation**

Add to `internal/github/github.go`:

```go
// IssueSummary is a lightweight issue representation returned by list queries.
type IssueSummary struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	Body   string `json:"body"`
}

// ListLabeledIssues returns open issues with the given label.
// Requires the client to be scoped to a repo via WithRepo().
func (c *Client) ListLabeledIssues(label string) ([]IssueSummary, error) {
	args := append([]string{
		"issue", "list",
		"--label", label,
		"--state", "open",
		"--json", "number,title,body",
		"--limit", "100",
	}, c.repoArgs()...)

	out, err := c.cmd.Run(args...)
	if err != nil {
		return nil, fmt.Errorf("list labeled issues: %w", err)
	}

	var issues []IssueSummary
	if err := json.Unmarshal([]byte(out), &issues); err != nil {
		return nil, fmt.Errorf("parse issues JSON: %w", err)
	}
	return issues, nil
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./internal/github/ -run TestListLabeled -v -count=1`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/github/github.go internal/github/github_test.go
git commit -m "feat(github): add ListLabeledIssues for label polling"
```

---

### Task 4: Orchestrator `pollLabeledIssues` method

**Files:**
- Modify: `internal/orchestrator/orchestrator.go`

**Step 1: Write the failing test**

Add to `internal/orchestrator/orchestrator_test.go`:

```go
func TestPollLabeledIssues_EnqueuesNewIssue(t *testing.T) {
	// Setup: DB with one pollable repo, no existing queue items
	d := openTestDB(t)
	store := newTestStore(t)

	// Register a repo
	_ = d.RepoAdd(db.RepoRecord{
		Namespace:    "org/repo",
		RepoURL:      "github.com/org/repo",
		LocalPath:    "/data/repos/repo",
		ConfigPath:   "/data/repos/repo/pipeline.yaml",
		PollLabel:    "implementation",
		PollInterval: 120,
		Active:       true,
	})

	// Mock GitHub client returns one issue
	mockGH := &mockGitHubClient{
		labeledIssues: map[string][]github.IssueSummary{
			"implementation": {
				{Number: 42, Title: "Add feature X", Body: "Build feature X"},
			},
		},
	}

	// Mock LLM returns intent
	mockLLM := func(prompt string) (string, error) {
		return "Users can do X", nil
	}

	o := NewOrchestrator(store, d, nil, nil, nil, nil, nil, nil)
	o.SetClaudeFn(mockLLM)
	o.ghForRepo = func(repo string) labelPoller { return mockGH }

	n, err := o.pollLabeledIssues()
	if err != nil {
		t.Fatalf("pollLabeledIssues: %v", err)
	}
	if n != 1 {
		t.Errorf("enqueued %d, want 1", n)
	}

	// Verify issue is in the queue
	items, _ := d.QueueList()
	if len(items) != 1 {
		t.Fatalf("queue has %d items, want 1", len(items))
	}
	if items[0].Issue != 42 {
		t.Errorf("queued issue = %d, want 42", items[0].Issue)
	}
	if items[0].FeatureIntent != "Users can do X" {
		t.Errorf("intent = %q", items[0].FeatureIntent)
	}
}

func TestPollLabeledIssues_SkipsExistingQueue(t *testing.T) {
	d := openTestDB(t)
	store := newTestStore(t)

	_ = d.RepoAdd(db.RepoRecord{
		Namespace: "org/repo", RepoURL: "github.com/org/repo",
		LocalPath: "/data/repos/repo", ConfigPath: "/data/repos/repo/pipeline.yaml",
		PollLabel: "implementation", PollInterval: 120, Active: true,
	})

	// Pre-add issue 42 to queue
	_ = d.QueueAdd([]db.QueueAddItem{{Namespace: "org/repo", Issue: 42, FeatureIntent: "existing"}})

	mockGH := &mockGitHubClient{
		labeledIssues: map[string][]github.IssueSummary{
			"implementation": {{Number: 42, Title: "Already queued", Body: ""}},
		},
	}

	o := NewOrchestrator(store, d, nil, nil, nil, nil, nil, nil)
	o.ghForRepo = func(repo string) labelPoller { return mockGH }

	n, _ := o.pollLabeledIssues()
	if n != 0 {
		t.Errorf("enqueued %d, want 0 (should skip existing)", n)
	}
}
```

Note: You'll need to define a `labelPoller` interface and `mockGitHubClient` to make these testable. Adapt the test helpers to match whatever mock patterns exist in `orchestrator_test.go`.

**Step 2: Run test to verify it fails**

Run: `go test ./internal/orchestrator/ -run TestPollLabeled -v -count=1`
Expected: FAIL — method not defined

**Step 3: Write the implementation**

Add to `internal/orchestrator/orchestrator.go`:

```go
// labelPoller is the subset of github.Client used by the label poller.
type labelPoller interface {
	ListLabeledIssues(label string) ([]github.IssueSummary, error)
}

// pollLabeledIssues checks all registered repos for new issues with their
// configured poll_label and enqueues them. Returns the number of issues enqueued.
func (o *Orchestrator) pollLabeledIssues() (int, error) {
	repos, err := o.db.RepoGetPollable()
	if err != nil {
		return 0, fmt.Errorf("get pollable repos: %w", err)
	}
	if len(repos) == 0 {
		return 0, nil
	}

	// Build set of already-known issues from queue
	queueItems, err := o.db.QueueList()
	if err != nil {
		return 0, fmt.Errorf("list queue: %w", err)
	}
	knownIssues := make(map[string]bool) // key: "namespace:issue"
	for _, q := range queueItems {
		knownIssues[fmt.Sprintf("%s:%d", q.Namespace, q.Issue)] = true
	}

	enqueued := 0
	for _, repo := range repos {
		gh := o.ghForRepo(repo.RepoURL)
		issues, err := gh.ListLabeledIssues(repo.PollLabel)
		if err != nil {
			o.logf("poll %s: %v", repo.Namespace, err)
			continue
		}

		for _, iss := range issues {
			key := fmt.Sprintf("%s:%d", repo.Namespace, iss.Number)
			if knownIssues[key] {
				continue
			}

			// Check if pipeline already exists on disk
			if _, err := o.store.GetForNamespace(repo.Namespace, iss.Number); err == nil {
				knownIssues[key] = true
				continue
			}

			// Derive intent
			intent := ""
			if o.claudeFn != nil {
				fullIssue := &github.Issue{Number: iss.Number, Title: iss.Title, Body: iss.Body}
				derived, err := github.DeriveFeatureIntent(fullIssue, o.claudeFn)
				if err != nil {
					o.logf("derive intent for #%d: %v", iss.Number, err)
				} else {
					intent = derived
				}
			}

			// Enqueue
			err := o.db.QueueAdd([]db.QueueAddItem{{
				Namespace:     repo.Namespace,
				Issue:         iss.Number,
				FeatureIntent: intent,
				ConfigPath:    repo.ConfigPath,
			}})
			if err != nil {
				o.logf("enqueue #%d: %v", iss.Number, err)
				continue
			}

			o.logf("polled and enqueued #%d (%s) from %s", iss.Number, iss.Title, repo.Namespace)
			knownIssues[key] = true
			enqueued++
		}
	}

	return enqueued, nil
}
```

Add `ghForRepo` field to the Orchestrator struct (default uses the real GitHub client):

```go
// In the Orchestrator struct, add:
ghForRepo func(repoURL string) labelPoller

// In NewOrchestrator, after setting other fields, add:
o := &Orchestrator{...}
o.ghForRepo = func(repoURL string) labelPoller {
	cmd := github.NewExecRunner()
	client := github.NewClient(cmd)
	// Extract "owner/repo" from URL like "github.com/owner/repo"
	parts := strings.SplitN(strings.TrimPrefix(repoURL, "github.com/"), "/", 2)
	if len(parts) == 2 {
		client = client.WithRepo(parts[0] + "/" + parts[1])
	}
	return client
}
return o
```

**Step 4: Integrate into CheckIn**

Add a poll tick counter to the Orchestrator struct and call `pollLabeledIssues` from `CheckIn()`:

```go
// Add to Orchestrator struct:
pollTick     int
pollInterval int // in number of check-ins; default 12 (12 * 10s = 120s)

// In NewOrchestrator, set default:
o.pollInterval = 12

// In CheckIn(), add BEFORE the return:
o.pollTick++
if o.pollInterval > 0 && o.pollTick >= o.pollInterval {
	o.pollTick = 0
	if n, err := o.pollLabeledIssues(); err != nil {
		o.logf("label poll error: %v", err)
	} else if n > 0 {
		o.logf("label poll: enqueued %d new issues", n)
	}
}
```

**Step 5: Run test to verify it passes**

Run: `go test ./internal/orchestrator/ -run TestPollLabeled -v -count=1`
Expected: PASS

**Step 6: Build check**

Run: `go build ./...`
Expected: Success

**Step 7: Commit**

```bash
git add internal/orchestrator/orchestrator.go internal/orchestrator/orchestrator_test.go
git commit -m "feat(orchestrator): add GitHub label poller for auto-enqueue"
```

---

### Task 5: CLI — `factory repo` commands

**Files:**
- Create: `internal/cli/repo.go`

**Step 1: Create the CLI commands**

Create `internal/cli/repo.go` following the patterns in `internal/cli/queue.go`:

```go
package cli

import (
	"fmt"
	"os"
	"strconv"
	"text/tabwriter"

	"github.com/lucasnoah/taintfactory/internal/db"
	"github.com/spf13/cobra"
)

var repoCmd = &cobra.Command{
	Use:   "repo",
	Short: "Manage registered repositories",
}

var repoAddCmd = &cobra.Command{
	Use:   "repo add <repo_url>",
	Short: "Register a repository",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		repoURL := args[0]
		localPath, _ := cmd.Flags().GetString("local-path")
		configPath, _ := cmd.Flags().GetString("config")
		pollLabel, _ := cmd.Flags().GetString("label")
		pollInterval, _ := cmd.Flags().GetInt("poll-interval")
		namespace, _ := cmd.Flags().GetString("namespace")

		if namespace == "" {
			// Derive from repo URL: "github.com/owner/repo" -> "owner/repo"
			namespace = repoURLToNamespace(repoURL)
		}

		d, err := openDB()
		if err != nil {
			return err
		}
		defer d.Close()
		if err := d.Migrate(); err != nil {
			return err
		}

		active := true
		return d.RepoAdd(db.RepoRecord{
			Namespace:    namespace,
			RepoURL:      repoURL,
			LocalPath:    localPath,
			ConfigPath:   configPath,
			PollLabel:    pollLabel,
			PollInterval: pollInterval,
			Active:       active,
		})
	},
}

var repoListCmd = &cobra.Command{
	Use:   "repo list",
	Short: "List registered repositories",
	RunE: func(cmd *cobra.Command, args []string) error {
		d, err := openDB()
		if err != nil {
			return err
		}
		defer d.Close()
		if err := d.Migrate(); err != nil {
			return err
		}

		repos, err := d.RepoList()
		if err != nil {
			return err
		}

		format, _ := cmd.Flags().GetString("format")
		if format == "json" {
			return printJSON(repos)
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "NAMESPACE\tREPO URL\tLABEL\tINTERVAL\tACTIVE")
		for _, r := range repos {
			label := r.PollLabel
			if label == "" {
				label = "-"
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%ds\t%v\n", r.Namespace, r.RepoURL, label, r.PollInterval, r.Active)
		}
		return w.Flush()
	},
}

var repoRemoveCmd = &cobra.Command{
	Use:   "repo remove <namespace>",
	Short: "Remove a registered repository",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		d, err := openDB()
		if err != nil {
			return err
		}
		defer d.Close()
		if err := d.Migrate(); err != nil {
			return err
		}

		return d.RepoRemove(args[0])
	},
}

var repoUpdateCmd = &cobra.Command{
	Use:   "repo update <namespace>",
	Short: "Update a registered repository",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		d, err := openDB()
		if err != nil {
			return err
		}
		defer d.Close()
		if err := d.Migrate(); err != nil {
			return err
		}

		opts := db.RepoUpdateOpts{}
		if cmd.Flags().Changed("label") {
			v, _ := cmd.Flags().GetString("label")
			opts.PollLabel = &v
		}
		if cmd.Flags().Changed("poll-interval") {
			v, _ := cmd.Flags().GetInt("poll-interval")
			opts.PollInterval = &v
		}
		if cmd.Flags().Changed("active") {
			v, _ := cmd.Flags().GetString("active")
			b, _ := strconv.ParseBool(v)
			opts.Active = &b
		}

		return d.RepoUpdate(args[0], opts)
	},
}

// repoURLToNamespace extracts "owner/repo" from a GitHub URL.
func repoURLToNamespace(url string) string {
	// Handle "github.com/owner/repo", "https://github.com/owner/repo", "owner/repo"
	for _, prefix := range []string{"https://github.com/", "github.com/"} {
		if len(url) > len(prefix) && url[:len(prefix)] == prefix {
			return url[len(prefix):]
		}
	}
	return url
}

func init() {
	rootCmd.AddCommand(repoCmd)
	repoCmd.AddCommand(repoAddCmd)
	repoCmd.AddCommand(repoListCmd)
	repoCmd.AddCommand(repoRemoveCmd)
	repoCmd.AddCommand(repoUpdateCmd)

	repoAddCmd.Flags().String("local-path", "", "Local path to cloned repo")
	repoAddCmd.Flags().String("config", "", "Path to pipeline.yaml")
	repoAddCmd.Flags().String("label", "", "GitHub label to poll for")
	repoAddCmd.Flags().Int("poll-interval", 120, "Poll interval in seconds")
	repoAddCmd.Flags().String("namespace", "", "Namespace (default: derived from repo URL)")
	_ = repoAddCmd.MarkFlagRequired("local-path")
	_ = repoAddCmd.MarkFlagRequired("config")

	repoListCmd.Flags().String("format", "table", "Output format: table or json")

	repoUpdateCmd.Flags().String("label", "", "GitHub label to poll for")
	repoUpdateCmd.Flags().Int("poll-interval", 120, "Poll interval in seconds")
	repoUpdateCmd.Flags().String("active", "", "Enable/disable repo (true/false)")
}
```

Note: Check how `openDB()` and `printJSON()` work in the existing CLI code (likely in `internal/cli/queue.go` or a shared helper). Reuse those patterns. If `openDB` doesn't exist as a shared function, extract it.

**Step 2: Build check**

Run: `go build ./...`
Expected: Success

**Step 3: Verify it runs**

Run: `go run ./cmd/factory/ repo --help`
Expected: Shows subcommands (add, list, remove, update)

**Step 4: Commit**

```bash
git add internal/cli/repo.go
git commit -m "feat(cli): add factory repo CRUD commands"
```

---

### Task 6: Auto-register repos on startup

**Files:**
- Modify: `internal/orchestrator/orchestrator.go` (or `internal/cli/serve.go`)

**Step 1: Add auto-registration helper**

Add to `internal/orchestrator/orchestrator.go`:

```go
// AutoRegisterRepos scans a directory for repos with pipeline.yaml and registers
// any that aren't already in the DB. Called once on startup.
func (o *Orchestrator) AutoRegisterRepos(repoBaseDir string) error {
	existing, err := o.db.RepoList()
	if err != nil {
		return fmt.Errorf("list repos: %w", err)
	}

	// Only auto-register if table is empty
	if len(existing) > 0 {
		return nil
	}

	entries, err := os.ReadDir(repoBaseDir)
	if err != nil {
		return fmt.Errorf("read repo dir: %w", err)
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		repoDir := filepath.Join(repoBaseDir, entry.Name())
		configPath := filepath.Join(repoDir, "pipeline.yaml")
		if _, err := os.Stat(configPath); err != nil {
			continue
		}

		cfg, err := config.Load(configPath)
		if err != nil {
			o.logf("skip %s: %v", repoDir, err)
			continue
		}

		namespace := repoToNamespace(cfg.Pipeline.Repo)
		if namespace == "" {
			namespace = entry.Name()
		}

		if err := o.db.RepoAdd(db.RepoRecord{
			Namespace:    namespace,
			RepoURL:      cfg.Pipeline.Repo,
			LocalPath:    repoDir,
			ConfigPath:   configPath,
			PollInterval: 120,
			Active:       true,
		}); err != nil {
			o.logf("auto-register %s: %v", namespace, err)
			continue
		}
		o.logf("auto-registered repo: %s", namespace)
	}
	return nil
}

func repoToNamespace(repoURL string) string {
	for _, prefix := range []string{"https://github.com/", "github.com/"} {
		if strings.HasPrefix(repoURL, prefix) {
			return strings.TrimPrefix(repoURL, prefix)
		}
	}
	return repoURL
}
```

**Step 2: Call from serve startup**

In the serve command (find in `internal/cli/serve.go` or wherever `factory serve` is defined), after creating the orchestrator, add:

```go
if err := orch.AutoRegisterRepos(filepath.Join(dataDir, "repos")); err != nil {
	log.Printf("auto-register repos: %v", err)
}
```

**Step 3: Build and verify**

Run: `go build ./...`
Expected: Success

**Step 4: Commit**

```bash
git add internal/orchestrator/orchestrator.go internal/cli/serve.go
git commit -m "feat(orchestrator): auto-register repos on first startup"
```

---

### Task 7: Web UI — Repo management page

**Files:**
- Modify: `internal/web/handlers.go` (replace handleConfig or add new handler)
- Create: `internal/web/templates/repos.html`
- Modify: `internal/web/server.go` (add template + route)

**Step 1: Create the template**

Create `internal/web/templates/repos.html`:

```html
{{define "content"}}
<h2>Registered Repositories</h2>

<table class="table">
  <thead>
    <tr>
      <th>Namespace</th>
      <th>Repo URL</th>
      <th>Local Path</th>
      <th>Poll Label</th>
      <th>Interval</th>
      <th>Active</th>
      <th>Actions</th>
    </tr>
  </thead>
  <tbody>
    {{range .Repos}}
    <tr>
      <td>{{.Namespace}}</td>
      <td>{{.RepoURL}}</td>
      <td><code>{{.LocalPath}}</code></td>
      <td>{{if .PollLabel}}<span class="badge">{{.PollLabel}}</span>{{else}}-{{end}}</td>
      <td>{{.PollInterval}}s</td>
      <td>{{if .Active}}<span class="badge badge-live">active</span>{{else}}<span class="badge">inactive</span>{{end}}</td>
      <td>
        <form method="POST" action="/repos/{{.Namespace}}/toggle" style="display:inline">
          <button type="submit" class="btn btn-sm">{{if .Active}}Disable{{else}}Enable{{end}}</button>
        </form>
        <form method="POST" action="/repos/{{.Namespace}}/remove" style="display:inline"
              onsubmit="return confirm('Remove {{.Namespace}}?')">
          <button type="submit" class="btn btn-sm btn-danger">Remove</button>
        </form>
      </td>
    </tr>
    {{else}}
    <tr><td colspan="7">No repos registered. Use <code>factory repo add</code> or the form below.</td></tr>
    {{end}}
  </tbody>
</table>

<h3>Add Repository</h3>
<form method="POST" action="/repos/add" class="form-inline" style="display:flex;gap:.5rem;flex-wrap:wrap;align-items:end">
  <label>Repo URL<br><input type="text" name="repo_url" placeholder="github.com/owner/repo" required></label>
  <label>Local Path<br><input type="text" name="local_path" placeholder="/data/repos/name" required></label>
  <label>Config Path<br><input type="text" name="config_path" placeholder="/data/repos/name/pipeline.yaml" required></label>
  <label>Poll Label<br><input type="text" name="poll_label" placeholder="implementation"></label>
  <label>Interval (s)<br><input type="number" name="poll_interval" value="120" min="30"></label>
  <button type="submit" class="btn">Add</button>
</form>
{{end}}
```

**Step 2: Add handlers**

Add to `internal/web/handlers.go`:

```go
func (s *Server) handleRepos(w http.ResponseWriter, r *http.Request) {
	repos, err := s.db.RepoList()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	data := struct {
		Repos   []db.RepoRecord
		Sidebar SidebarData
	}{
		Repos:   repos,
		Sidebar: s.sidebarData(currentProject(r)),
	}

	if err := s.reposTmpl.ExecuteTemplate(w, "base", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) handleRepoAdd(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	interval := 120
	if v := r.FormValue("poll_interval"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 30 {
			interval = n
		}
	}

	repoURL := r.FormValue("repo_url")
	namespace := repoURLToNamespace(repoURL)

	err := s.db.RepoAdd(db.RepoRecord{
		Namespace:    namespace,
		RepoURL:      repoURL,
		LocalPath:    r.FormValue("local_path"),
		ConfigPath:   r.FormValue("config_path"),
		PollLabel:    r.FormValue("poll_label"),
		PollInterval: interval,
		Active:       true,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/repos", http.StatusSeeOther)
}

func (s *Server) handleRepoToggle(w http.ResponseWriter, r *http.Request) {
	// Extract namespace from URL path: /repos/{namespace}/toggle
	namespace := extractRepoNamespace(r.URL.Path, "/toggle")

	repos, _ := s.db.RepoList()
	for _, repo := range repos {
		if repo.Namespace == namespace {
			newActive := !repo.Active
			_ = s.db.RepoUpdate(namespace, db.RepoUpdateOpts{Active: &newActive})
			break
		}
	}
	http.Redirect(w, r, "/repos", http.StatusSeeOther)
}

func (s *Server) handleRepoRemove(w http.ResponseWriter, r *http.Request) {
	namespace := extractRepoNamespace(r.URL.Path, "/remove")
	_ = s.db.RepoRemove(namespace)
	http.Redirect(w, r, "/repos", http.StatusSeeOther)
}

// extractRepoNamespace extracts namespace from paths like /repos/owner/repo/action
func extractRepoNamespace(path, suffix string) string {
	path = strings.TrimPrefix(path, "/repos/")
	path = strings.TrimSuffix(path, suffix)
	return path
}

func repoURLToNamespace(url string) string {
	for _, prefix := range []string{"https://github.com/", "github.com/"} {
		if strings.HasPrefix(url, prefix) {
			return strings.TrimPrefix(url, prefix)
		}
	}
	return url
}
```

**Step 3: Register routes and template**

In `internal/web/server.go`:

1. Add `reposTmpl *template.Template` to the Server struct
2. Parse the template in `NewServer` (follow pattern of existing templates)
3. Add routes in the mux setup:

```go
mux.HandleFunc("/repos", s.handleRepos)
mux.HandleFunc("/repos/add", s.handleRepoAdd)
// Use a prefix handler for toggle/remove since namespace has slashes
mux.HandleFunc("/repos/", func(w http.ResponseWriter, r *http.Request) {
	if strings.HasSuffix(r.URL.Path, "/toggle") {
		s.handleRepoToggle(w, r)
	} else if strings.HasSuffix(r.URL.Path, "/remove") {
		s.handleRepoRemove(w, r)
	} else {
		http.NotFound(w, r)
	}
})
```

**Step 4: Update sidebar**

In `internal/web/templates/base.html`, change the Config sidebar link:

```html
<a href="/repos" class="sidebar-link">Repos</a>
```

(Keep the `/config` route working for backwards compatibility, just change the sidebar link.)

**Step 5: Build and verify**

Run: `go build ./...`
Expected: Success

**Step 6: Commit**

```bash
git add internal/web/handlers.go internal/web/server.go internal/web/templates/repos.html internal/web/templates/base.html
git commit -m "feat(web): add repo management UI page"
```

---

### Task 8: Integration test — end-to-end poll cycle

**Files:**
- Modify: `internal/orchestrator/orchestrator_test.go`

**Step 1: Write end-to-end test**

```go
func TestPollLabeledIssues_FullCycle(t *testing.T) {
	// 1. Setup DB with a registered repo
	d := openTestDB(t)
	store := newTestStore(t)

	_ = d.RepoAdd(db.RepoRecord{
		Namespace: "org/repo", RepoURL: "github.com/org/repo",
		LocalPath: "/data/repos/repo", ConfigPath: "/data/repos/repo/pipeline.yaml",
		PollLabel: "implementation", PollInterval: 120, Active: true,
	})

	// 2. Mock GitHub returns issues 10, 11
	mockGH := &mockGitHubClient{
		labeledIssues: map[string][]github.IssueSummary{
			"implementation": {
				{Number: 10, Title: "Feature A", Body: "Build A"},
				{Number: 11, Title: "Feature B", Body: "Build B"},
			},
		},
	}

	mockLLM := func(prompt string) (string, error) { return "test intent", nil }

	o := NewOrchestrator(store, d, nil, nil, nil, nil, nil, nil)
	o.SetClaudeFn(mockLLM)
	o.ghForRepo = func(repo string) labelPoller { return mockGH }

	// 3. First poll: both enqueued
	n, err := o.pollLabeledIssues()
	if err != nil {
		t.Fatalf("first poll: %v", err)
	}
	if n != 2 {
		t.Errorf("first poll: enqueued %d, want 2", n)
	}

	// 4. Second poll: nothing new (idempotent)
	n, _ = o.pollLabeledIssues()
	if n != 0 {
		t.Errorf("second poll: enqueued %d, want 0", n)
	}

	// 5. Verify queue state
	items, _ := d.QueueList()
	if len(items) != 2 {
		t.Fatalf("queue has %d items, want 2", len(items))
	}
}
```

**Step 2: Run the test**

Run: `go test ./internal/orchestrator/ -run TestPollLabeledIssues_FullCycle -v -count=1`
Expected: PASS

**Step 3: Commit**

```bash
git add internal/orchestrator/orchestrator_test.go
git commit -m "test(orchestrator): add integration test for label poll cycle"
```

---

### Task 9: Deploy and verify

**Step 1: Build binary**

Run: `go build ./...`

**Step 2: Build and push Docker image**

```bash
docker buildx build --platform linux/amd64 -t registry.digitalocean.com/personalcluster/taintfactory:latest .
docker push registry.digitalocean.com/personalcluster/taintfactory:latest
```

**Step 3: Deploy**

```bash
kubectl rollout restart statefulset/factory -n taintfactory
kubectl rollout status statefulset/factory -n taintfactory --timeout=120s
```

**Step 4: Register the repo**

```bash
kubectl exec factory-0 -n taintfactory -c factory -- factory repo add \
  github.com/mbrucker/deathcookies \
  --local-path /data/repos/deathcookies \
  --config /data/repos/deathcookies/pipeline.yaml \
  --label implementation
```

**Step 5: Verify**

```bash
kubectl exec factory-0 -n taintfactory -c factory -- factory repo list
kubectl logs factory-0 -n taintfactory -c factory --tail=20
```

Expected: Repo shows in list. After ~2 minutes, logs should show poll activity.

**Step 6: Commit all remaining changes**

```bash
git add -A
git commit -m "feat: GitHub label poller with repo registry

- Add repos table for managing registered repositories
- Add factory repo add/list/remove/update CLI commands
- Add web UI for repo management (replaces Config page)
- Orchestrator polls GitHub every ~2min for labeled issues
- Auto-enqueue new issues with LLM-derived intent
- Auto-register repos from /data/repos on first boot"
```
