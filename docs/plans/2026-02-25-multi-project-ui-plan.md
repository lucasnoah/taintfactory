# Multi-Project UI Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add a persistent sidebar to the web UI that groups pipelines by project (namespace), lets the user scope every page to a single project via `?project=org/repo`, and shows a cross-project summary strip on the All view.

**Architecture:** The base layout gains a sidebar + content grid. Every handler calls `currentProject(r)` and `sidebarData(proj)` to produce a `SidebarData` value injected into each view model. Server-side filtering on `ps.Namespace` (pipelines) and `namespaceFromConfigPath` (pending queue items) narrows results when a project is selected.

**Tech Stack:** Go (`internal/web`), Go `html/template` (embedded), plain CSS (in `base.html`).

---

### Task 1: Types + server helpers (TDD)

**Files:**
- Modify: `internal/web/handlers.go` (add types + fields)
- Modify: `internal/web/server.go` (add helpers)
- Modify: `internal/web/server_test.go` (add tests)

**Context:** All view model types live in `handlers.go`. Server helpers (`configFor`, `allRepoConfigs`, etc.) live in `server.go`. Tests live in `server_test.go` (already created, package `web`).

---

**Step 1: Write the failing tests**

Add to `internal/web/server_test.go`:

```go
import (
    // add to existing imports:
    "github.com/lucasnoah/taintfactory/internal/pipeline"
)

func TestSidebarData_GroupsByNamespace(t *testing.T) {
    dir := t.TempDir()
    store := pipeline.NewStore(dir)
    store.Create(pipeline.CreateOpts{Issue: 1, Title: "A", Branch: "b", Worktree: "w", FirstStage: "impl", Namespace: "org/repo-a", ConfigPath: "/x/pipeline.yaml", RepoDir: "/x"})
    store.Create(pipeline.CreateOpts{Issue: 2, Title: "B", Branch: "b", Worktree: "w", FirstStage: "impl", Namespace: "org/repo-a", ConfigPath: "/x/pipeline.yaml", RepoDir: "/x"})
    store.Create(pipeline.CreateOpts{Issue: 3, Title: "C", Branch: "b", Worktree: "w", FirstStage: "impl", Namespace: "org/repo-b", ConfigPath: "/y/pipeline.yaml", RepoDir: "/y"})

    s := NewServer(store, nil, 0, "")
    sd := s.sidebarData("")

    if len(sd.Projects) != 2 {
        t.Fatalf("expected 2 projects, got %d", len(sd.Projects))
    }
    // Projects are sorted alphabetically
    if sd.Projects[0].Namespace != "org/repo-a" {
        t.Errorf("expected org/repo-a first, got %q", sd.Projects[0].Namespace)
    }
}

func TestSidebarData_CountsOnlyActive(t *testing.T) {
    dir := t.TempDir()
    store := pipeline.NewStore(dir)
    ps, _ := store.Create(pipeline.CreateOpts{Issue: 1, Title: "A", Branch: "b", Worktree: "w", FirstStage: "impl", Namespace: "org/app", ConfigPath: "/x/pipeline.yaml", RepoDir: "/x"})
    ps.Status = "in_progress"
    store.Update(1, func(p *pipeline.PipelineState) { p.Status = "in_progress" })
    store.Create(pipeline.CreateOpts{Issue: 2, Title: "B", Branch: "b", Worktree: "w", FirstStage: "impl", Namespace: "org/app", ConfigPath: "/x/pipeline.yaml", RepoDir: "/x"})
    // issue 2 stays pending

    s := NewServer(store, nil, 0, "")
    sd := s.sidebarData("")

    if len(sd.Projects) != 1 {
        t.Fatalf("expected 1 project, got %d", len(sd.Projects))
    }
    if sd.Projects[0].ActiveCount != 1 {
        t.Errorf("expected ActiveCount=1, got %d", sd.Projects[0].ActiveCount)
    }
}

func TestSidebarData_MarksSelected(t *testing.T) {
    dir := t.TempDir()
    store := pipeline.NewStore(dir)
    store.Create(pipeline.CreateOpts{Issue: 1, Title: "A", Branch: "b", Worktree: "w", FirstStage: "impl", Namespace: "org/app", ConfigPath: "/x/pipeline.yaml", RepoDir: "/x"})
    store.Create(pipeline.CreateOpts{Issue: 2, Title: "B", Branch: "b", Worktree: "w", FirstStage: "impl", Namespace: "org/other", ConfigPath: "/y/pipeline.yaml", RepoDir: "/y"})

    s := NewServer(store, nil, 0, "")
    sd := s.sidebarData("org/app")

    var found bool
    for _, p := range sd.Projects {
        if p.Namespace == "org/app" && p.IsSelected {
            found = true
        }
        if p.Namespace == "org/other" && p.IsSelected {
            t.Error("org/other should not be selected")
        }
    }
    if !found {
        t.Error("org/app should be marked IsSelected")
    }
    if sd.CurrentProject != "org/app" {
        t.Errorf("CurrentProject = %q, want org/app", sd.CurrentProject)
    }
}

func TestSidebarData_ExcludesLegacyPipelines(t *testing.T) {
    dir := t.TempDir()
    store := pipeline.NewStore(dir)
    // Legacy pipeline: no Namespace
    store.Create(pipeline.CreateOpts{Issue: 1, Title: "Legacy", Branch: "b", Worktree: "/some/worktree", FirstStage: "impl"})

    s := NewServer(store, nil, 0, "")
    sd := s.sidebarData("")

    if len(sd.Projects) != 0 {
        t.Errorf("expected 0 projects (legacy has no namespace), got %d", len(sd.Projects))
    }
}

func TestRepoToNamespace(t *testing.T) {
    cases := []struct {
        repo string
        want string
    }{
        {"github.com/myorg/myapp", "myorg/myapp"},
        {"https://github.com/myorg/myapp", "myorg/myapp"},
        {"http://github.com/myorg/myapp", "myorg/myapp"},
        {"myorg/myapp", "myapp"},  // no hostname: returns last segment
        {"", ""},
    }
    for _, c := range cases {
        got := repoToNamespace(c.repo)
        if got != c.want {
            t.Errorf("repoToNamespace(%q) = %q, want %q", c.repo, got, c.want)
        }
    }
}
```

**Step 2: Run tests to verify they fail**

```bash
go test ./internal/web/ -run "TestSidebarData|TestRepoToNamespace" -v
```

Expected: compile error — `sidebarData`, `repoToNamespace` undefined.

---

**Step 3: Add types to `handlers.go`**

After the existing type declarations (after line 58, after `QueueRowView`), add:

```go
type ProjectSidebarItem struct {
    Namespace   string
    ActiveCount int
    IsSelected  bool
}

type SidebarData struct {
    Projects       []ProjectSidebarItem
    CurrentProject string // empty = All view
}

type ProjectSummaryCard struct {
    Namespace   string
    ActiveCount int
    TotalCount  int
    FailedCount int
}
```

Add `Namespace string` field to `QueueRowView`:

```go
type QueueRowView struct {
    Issue          int
    Position       int
    Status         string
    DependsOnStr   string
    HasPipeline    bool
    PipelineStatus string
    Namespace      string // project namespace; empty for legacy
}
```

Add `Sidebar SidebarData` to every `*Data` struct:

```go
type DashboardData struct {
    Pipelines      []PipelineRow
    QueueItems     []QueueRowView
    RecentActivity []ActivityRow
    TriageRows     []TriageRow
    ProjectSummary []ProjectSummaryCard
    Sidebar        SidebarData
}

type TriageListData struct {
    TriageRows []TriageRow
    Sidebar    SidebarData
}

type PipelineDetailData struct {
    // ... all existing fields ...
    Sidebar SidebarData
}

type AttemptDetailData struct {
    // ... all existing fields ...
    Sidebar SidebarData
}

type QueueData struct {
    Items   []QueueRowView
    Sidebar SidebarData
}

type ConfigData struct {
    Repos   []RepoConfigView
    Sidebar SidebarData
}
```

---

**Step 4: Add helpers to `server.go`**

Add these after the `allRepoConfigs` function (around line 170):

```go
// currentProject reads the ?project= query parameter from a request.
func currentProject(r *http.Request) string {
    return r.URL.Query().Get("project")
}

// repoToNamespace converts a pipeline.repo value ("github.com/org/repo" or
// "https://github.com/org/repo") to a namespace string "org/repo".
func repoToNamespace(repo string) string {
    if repo == "" {
        return ""
    }
    repo = strings.TrimPrefix(repo, "https://")
    repo = strings.TrimPrefix(repo, "http://")
    parts := strings.SplitN(repo, "/", 2)
    if len(parts) >= 2 {
        return parts[1]
    }
    return repo
}

// namespaceFromConfigPath derives the namespace from a config file path by
// reading the cached pipeline config for that file's directory. Returns ""
// if the config is not in the cache (call sidebarData first to warm the cache).
func (s *Server) namespaceFromConfigPath(configPath string) string {
    if configPath == "" {
        return ""
    }
    repoDir := filepath.Dir(configPath)
    s.cfgMu.RLock()
    cfg := s.cfgCache[repoDir]
    s.cfgMu.RUnlock()
    if cfg == nil {
        return ""
    }
    return repoToNamespace(cfg.Pipeline.Repo)
}

// sidebarData returns sidebar state for all known namespaced projects.
// currentProject should be the ?project= query param value (empty = All).
func (s *Server) sidebarData(currentProject string) SidebarData {
    pipelines, _ := s.store.List("")

    // Pre-warm config cache so namespaceFromConfigPath works for queue items.
    for i := range pipelines {
        s.configForPS(&pipelines[i])
    }

    type entry struct {
        active int
        total  int
    }
    counts := make(map[string]*entry)
    for _, ps := range pipelines {
        if ps.Namespace == "" {
            continue
        }
        e := counts[ps.Namespace]
        if e == nil {
            e = &entry{}
            counts[ps.Namespace] = e
        }
        e.total++
        if ps.Status == "in_progress" {
            e.active++
        }
    }

    var projects []ProjectSidebarItem
    for ns, e := range counts {
        projects = append(projects, ProjectSidebarItem{
            Namespace:   ns,
            ActiveCount: e.active,
            IsSelected:  ns == currentProject,
        })
    }
    sort.Slice(projects, func(i, j int) bool {
        return projects[i].Namespace < projects[j].Namespace
    })

    return SidebarData{
        Projects:       projects,
        CurrentProject: currentProject,
    }
}
```

Note: `sidebarData` needs `sort` import — already present in `handlers.go` but not `server.go`. Add to `server.go` imports: `"sort"`.

---

**Step 5: Run tests to verify they pass**

```bash
go test ./internal/web/ -run "TestSidebarData|TestRepoToNamespace" -v
```

Expected: all PASS.

**Step 6: Run all web tests**

```bash
go test ./internal/web/ -v
```

Expected: all existing tests still PASS (new fields are zero-valued, templates render empty sidebar gracefully).

**Step 7: Commit**

```bash
git add internal/web/handlers.go internal/web/server.go internal/web/server_test.go
git commit -m "feat(web): sidebar types + server helpers for multi-project UI"
```

---

### Task 2: base.html — sidebar layout

**Files:**
- Modify: `internal/web/templates/base.html`

**Context:** Currently `base.html` has a `<nav>` bar across the top and `<main>` as a direct child of `<body>`. We replace this with a two-column flex layout: fixed-width sidebar on the left, scrollable content on the right. The sidebar links use `?project=` query params to filter. The active project is highlighted using the `IsSelected` field. No Go tests for templates — verify visually by running the server.

---

**Step 1: Replace `base.html` entirely**

```html
{{define "base"}}<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
{{block "refresh" .}}{{end}}
<title>TaintFactory — {{block "title" .}}UI{{end}}</title>
<style>
:root {
  --bg: #f8f9fa; --border: #dee2e6; --text: #212529;
  --sidebar-bg: #1a1d23; --sidebar-text: #8b949e; --sidebar-active: #e6edf3;
  --muted: #6c757d;
}
* { box-sizing: border-box; margin: 0; padding: 0; }
body { font-family: system-ui, -apple-system, sans-serif; background: var(--bg); color: var(--text); font-size: 14px; display: flex; min-height: 100vh; }

/* ---- Sidebar ---- */
.sidebar { width: 200px; min-width: 200px; background: var(--sidebar-bg); display: flex; flex-direction: column; position: sticky; top: 0; height: 100vh; overflow-y: auto; flex-shrink: 0; }
.sidebar-brand { color: #fff; font-weight: 700; font-size: .9rem; letter-spacing: -.02em; padding: 1rem 1rem .75rem; border-bottom: 1px solid #2d333b; margin-bottom: .5rem; }
.sidebar-section { font-size: .65rem; font-weight: 600; color: #484f58; text-transform: uppercase; letter-spacing: .08em; padding: .75rem 1rem .25rem; }
.sidebar-link { display: flex; align-items: center; justify-content: space-between; padding: .3rem 1rem; color: var(--sidebar-text); text-decoration: none; font-size: .8rem; border-radius: 4px; margin: 0 .35rem; }
.sidebar-link:hover { color: var(--sidebar-active); background: #2d333b; }
.sidebar-link.active { color: var(--sidebar-active); background: #2d333b; }
.sidebar-count { background: #2d333b; color: #8b949e; font-size: .65rem; font-weight: 600; padding: .1em .45em; border-radius: 10px; min-width: 1.4em; text-align: center; }
.sidebar-link.active .sidebar-count { background: #0d6efd; color: #fff; }
.sidebar-divider { border-top: 1px solid #2d333b; margin: .5rem .75rem; }

/* ---- Content area ---- */
.content { flex: 1; min-width: 0; display: flex; flex-direction: column; }
main { max-width: 1200px; margin: 0 auto; padding: 1.5rem; width: 100%; }

h2 { font-size: 0.75rem; font-weight: 600; color: var(--muted); text-transform: uppercase; letter-spacing: .08em; margin: 1.5rem 0 0.75rem; }
h2:first-child { margin-top: 0; }
table { width: 100%; border-collapse: collapse; margin-bottom: 1.5rem; background: #fff; border: 1px solid var(--border); border-radius: 6px; overflow: hidden; }
thead { background: var(--bg); }
th { text-align: left; padding: 0.5rem 0.75rem; font-size: 0.75rem; font-weight: 600; color: var(--muted); border-bottom: 1px solid var(--border); white-space: nowrap; }
td { padding: 0.5rem 0.75rem; border-bottom: 1px solid var(--border); vertical-align: middle; }
tr:last-child td { border-bottom: none; }
tr:hover td { background: #f1f3f5; }
a { color: #0d6efd; text-decoration: none; }
a:hover { text-decoration: underline; }
.muted { color: var(--muted); }
.badge { display: inline-block; padding: .2em .55em; border-radius: 4px; font-size: .72rem; font-weight: 600; white-space: nowrap; }
.badge-pending    { background: #e9ecef; color: #495057; }
.badge-in-progress { background: #cfe2ff; color: #084298; }
.badge-active     { background: #cfe2ff; color: #084298; }
.badge-completed  { background: #d1e7dd; color: #0a3622; }
.badge-success    { background: #d1e7dd; color: #0a3622; }
.badge-failed     { background: #f8d7da; color: #842029; }
.badge-fail       { background: #f8d7da; color: #842029; }
.badge-blocked      { background: #fff3cd; color: #664d03; }
.badge-escalate     { background: #fff3cd; color: #664d03; }
.badge-rate-limited { background: #e2d9f3; color: #432874; }
.dot { display: inline-block; width: 8px; height: 8px; border-radius: 50%; }
.dot-green  { background: #198754; box-shadow: 0 0 4px #19875488; }
.dot-yellow { background: #ffc107; }
.dot-grey   { background: #adb5bd; }
@keyframes live-pulse { 0%,100% { opacity:1; box-shadow: 0 0 0 0 #19875466; } 50% { opacity:.85; box-shadow: 0 0 0 5px #19875400; } }
.badge-live { background: #d1e7dd; color: #0a3622; animation: live-pulse 1.6s ease-in-out infinite; }
.badge-ns { background: #e9ecef; color: #495057; font-size: .65rem; font-family: monospace; }
.progress-bar { display: flex; gap: 3px; height: 12px; margin: 0.5rem 0 0.25rem; }
.seg { flex: 1; border-radius: 6px; }
.seg-done     { background: #198754; }
.seg-active   { background: #0d6efd; }
.seg-upcoming { background: #dee2e6; }
.seg-labels { display: flex; gap: 3px; margin-bottom: 1.5rem; }
.seg-label { flex: 1; font-size: .65rem; text-align: center; overflow: hidden; padding: 0.1rem; line-height: 1.3; }
pre { background: #1a1d23; color: #e8eaf0; padding: 1rem; border-radius: 6px; overflow-x: auto; font-size: .78rem; line-height: 1.55; white-space: pre-wrap; word-break: break-all; margin-bottom: 1.5rem; }
pre.prompt { background: #fff; color: var(--text); border: 1px solid var(--border); }
.grid-2 { display: grid; grid-template-columns: 1fr 280px; gap: 1.5rem; }
.card { background: #fff; border: 1px solid var(--border); border-radius: 6px; padding: 0.875rem 1rem; margin-bottom: 1rem; font-size: 0.875rem; }
.queue-list { list-style: none; background: #fff; border: 1px solid var(--border); border-radius: 6px; overflow: hidden; }
.queue-list li { padding: 0.5rem 0.75rem; border-bottom: 1px solid var(--border); display: flex; align-items: center; gap: 0.5rem; flex-wrap: wrap; }
.queue-list li:last-child { border-bottom: none; }
.result-pass { color: #198754; font-weight: 600; }
.result-fail { color: #842029; font-weight: 600; }
code { background: #e9ecef; padding: .15em .35em; border-radius: 3px; font-size: .85em; word-break: break-all; }
/* Project summary cards */
.project-cards { display: flex; flex-wrap: wrap; gap: .75rem; margin-bottom: 1.5rem; }
.project-card { background: #fff; border: 1px solid var(--border); border-radius: 6px; padding: .75rem 1rem; min-width: 180px; text-decoration: none; color: var(--text); display: block; }
.project-card:hover { border-color: #0d6efd; text-decoration: none; }
.project-card-name { font-size: .8rem; font-weight: 600; font-family: monospace; margin-bottom: .35rem; color: var(--text); }
.project-card-stats { display: flex; gap: .4rem; flex-wrap: wrap; align-items: center; }
</style>
</head>
<body>

<aside class="sidebar">
  <div class="sidebar-brand">TaintFactory</div>

  <div class="sidebar-section">Projects</div>
  <a href="/" class="sidebar-link{{if not .Sidebar.CurrentProject}} active{{end}}">All</a>
  {{range .Sidebar.Projects}}
  <a href="/?project={{.Namespace}}" class="sidebar-link{{if .IsSelected}} active{{end}}">
    <span style="overflow:hidden;text-overflow:ellipsis;white-space:nowrap;min-width:0">{{.Namespace}}</span>
    {{if .ActiveCount}}<span class="sidebar-count">{{.ActiveCount}}</span>{{end}}
  </a>
  {{end}}

  <div class="sidebar-divider"></div>
  <div class="sidebar-section">Views</div>
  <a href="/triage" class="sidebar-link">Triage</a>
  <a href="/queue" class="sidebar-link">Queue</a>
  <a href="/config" class="sidebar-link">Config</a>
</aside>

<div class="content">
<main>
{{block "content" .}}{{end}}
</main>
</div>

</body>
</html>{{end}}
```

**Step 2: Build to verify no template errors**

```bash
go build ./... 2>&1
```

Expected: clean build (template errors surface at startup, not compile time, but syntax errors in embedded templates do cause build failures with `template.Must`).

**Step 3: Run server and verify layout**

```bash
go build -o /tmp/factory ./cmd/factory/ && /tmp/factory serve &
```

Open http://localhost:17432 — should see the sidebar on the left. It will be mostly empty (no Sidebar data yet because handlers don't populate it). That's fine, it confirms the layout works.

Kill the test server: `pkill -f "/tmp/factory serve"` (or just use the existing running instance — it will hot-reload on next build).

**Step 4: Commit**

```bash
git add internal/web/templates/base.html
git commit -m "feat(web): sidebar layout — two-column shell with project nav"
```

---

### Task 3: Wire handlers to populate Sidebar + apply project filter

**Files:**
- Modify: `internal/web/handlers.go` (all handlers)

**Context:** Every handler needs to: (1) read `?project=` param, (2) call `s.sidebarData(proj)`, (3) inject `Sidebar` into its data struct, (4) filter its data by namespace when `proj != ""`. This task wires all the handlers. Template changes come in the next tasks.

No unit tests for handler wiring — the running server is the test.

---

**Step 1: Update `handleDashboard`**

In `handleDashboard` (starts around line 240 of `handlers.go`), after loading pipelines and before building `rows`:

```go
func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
    proj := currentProject(r)
    sidebar := s.sidebarData(proj)

    pipelines, err := s.store.List("")
    // ... existing error handling ...

    // Filter by project
    if proj != "" {
        var filtered []pipeline.PipelineState
        for _, p := range pipelines {
            if p.Namespace == proj {
                filtered = append(filtered, p)
            }
        }
        pipelines = filtered
    }

    // ... existing sort ...

    // Build project summary cards (only in All view)
    var projectSummary []ProjectSummaryCard
    if proj == "" {
        nsCounts := make(map[string]*ProjectSummaryCard)
        for _, p := range pipelines {  // note: unfiltered here — we already filtered above, but in All view pipelines is unfiltered
            if p.Namespace == "" {
                continue
            }
            c := nsCounts[p.Namespace]
            if c == nil {
                c = &ProjectSummaryCard{Namespace: p.Namespace}
                nsCounts[p.Namespace] = c
            }
            c.TotalCount++
            if p.Status == "in_progress" {
                c.ActiveCount++
            }
            if p.Status == "failed" {
                c.FailedCount++
            }
        }
        for _, c := range nsCounts {
            projectSummary = append(projectSummary, *c)
        }
        sort.Slice(projectSummary, func(i, j int) bool {
            return projectSummary[i].Namespace < projectSummary[j].Namespace
        })
    }

    // ... existing rows/queueRows/activityRows building ...

    // Filter queue items by project
    if proj != "" {
        projectIssues := make(map[int]bool)
        for _, p := range pipelines {
            projectIssues[p.Issue] = true
        }
        var filteredQueue []QueueRowView
        for _, q := range queueRows {
            if projectIssues[q.Issue] || s.namespaceFromConfigPath(/* need config_path from original queue items */) == proj {
                filteredQueue = append(filteredQueue, q)
            }
        }
        queueRows = filteredQueue
    }
```

Wait — to filter queue items by configPath namespace, we need the original `queueItems` (with `ConfigPath`). Restructure: build a `configPathByIssue` map before building `queueRows`, then filter `queueItems` before mapping to `queueRows`:

Replace the queue-row-building block in `handleDashboard` with:

```go
// Build set of project issues (for queue filtering)
projectIssues := make(map[int]bool)
if proj != "" {
    for _, p := range pipelines {
        projectIssues[p.Issue] = true
    }
}

queueRows := make([]QueueRowView, 0, len(queueItems))
for _, q := range queueItems {
    // Derive namespace for this queue item
    ns := ""
    if p, ok := pipelineByIssue[q.Issue]; ok {
        ns = p.Namespace
    } else {
        ns = s.namespaceFromConfigPath(q.ConfigPath)
    }
    // Filter by project
    if proj != "" && ns != proj {
        continue
    }
    var pStatus string
    hasPipeline := false
    if p, ok := pipelineByIssue[q.Issue]; ok {
        hasPipeline = true
        pStatus = p.Status
    }
    queueRows = append(queueRows, QueueRowView{
        Issue:          q.Issue,
        Position:       q.Position,
        Status:         q.Status,
        DependsOnStr:   formatDeps(q.DependsOn),
        HasPipeline:    hasPipeline,
        PipelineStatus: pStatus,
        Namespace:      ns,
    })
}
```

Add `ProjectSummary` and `Sidebar` to the `DashboardData` literal at the end:

```go
data := DashboardData{
    Pipelines:      rows,
    QueueItems:     queueRows,
    RecentActivity: activityRows,
    TriageRows:     triageRows,
    ProjectSummary: projectSummary,
    Sidebar:        sidebar,
}
```

---

**Step 2: Update `handleQueue`**

```go
func (s *Server) handleQueue(w http.ResponseWriter, r *http.Request) {
    proj := currentProject(r)
    sidebar := s.sidebarData(proj)

    queueItems, err := s.db.QueueList()
    // ... existing error handling ...

    pipelines, _ := s.store.List("")
    pipelineByIssue := make(map[int]*pipeline.PipelineState)
    for i := range pipelines {
        p := &pipelines[i]
        pipelineByIssue[p.Issue] = p
    }

    rows := make([]QueueRowView, 0, len(queueItems))
    for _, q := range queueItems {
        ns := ""
        if p, ok := pipelineByIssue[q.Issue]; ok {
            ns = p.Namespace
        } else {
            ns = s.namespaceFromConfigPath(q.ConfigPath)
        }
        if proj != "" && ns != proj {
            continue
        }
        var pStatus string
        hasPipeline := false
        if p, ok := pipelineByIssue[q.Issue]; ok {
            hasPipeline = true
            pStatus = p.Status
        }
        rows = append(rows, QueueRowView{
            Issue:          q.Issue,
            Position:       q.Position,
            Status:         q.Status,
            DependsOnStr:   formatDeps(q.DependsOn),
            HasPipeline:    hasPipeline,
            PipelineStatus: pStatus,
            Namespace:      ns,
        })
    }

    data := QueueData{Items: rows, Sidebar: sidebar}
    // ... existing template execution ...
}
```

---

**Step 3: Update `handleConfig`**

```go
func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
    proj := currentProject(r)
    sidebar := s.sidebarData(proj)

    repos := s.allRepoConfigs()

    // Filter by project
    if proj != "" {
        var filtered []repoConfig
        for _, rc := range repos {
            if repoToNamespace(rc.Cfg.Pipeline.Repo) == proj {
                filtered = append(filtered, rc)
            }
        }
        repos = filtered
    }

    // ... existing view building (unchanged) ...

    data := ConfigData{Repos: views, Sidebar: sidebar}
    // ... existing template execution ...
}
```

---

**Step 4: Update `handlePipelineDetail`**

In `handlePipelineDetail`, after loading `ps`:

```go
// Use the pipeline's own namespace as the sidebar selection
proj := ps.Namespace
sidebar := s.sidebarData(proj)

// ... rest of handler unchanged ...

data := PipelineDetailData{
    // ... all existing fields ...
    Sidebar: sidebar,
}
```

---

**Step 5: Update `handleAttemptDetail`**

After loading `outcome, _ := ...`, load the pipeline state to get its namespace:

```go
// Get namespace for sidebar
var proj string
if ps, err := s.store.Get(issue); err == nil {
    proj = ps.Namespace
}
sidebar := s.sidebarData(proj)

// ... existing data building ...

data := AttemptDetailData{
    // ... existing fields ...
    Sidebar: sidebar,
}
```

---

**Step 6: Update `handleTriageList`**

```go
func (s *Server) handleTriageList(w http.ResponseWriter, r *http.Request) {
    sidebar := s.sidebarData("") // triage not namespaced yet
    // ... existing code ...
    data := TriageListData{TriageRows: rows, Sidebar: sidebar}
    // ...
}
```

---

**Step 7: Build and run**

```bash
go build -o /tmp/factory ./cmd/factory/ && /tmp/factory serve
```

Open http://localhost:17432 — sidebar should now show projects with active counts. Clicking a project should filter the dashboard. Check http://localhost:17432/queue?project=myorg/myapp shows only that project's queue.

Kill the test server when done.

**Step 8: Commit**

```bash
git add internal/web/handlers.go
git commit -m "feat(web): wire handlers with sidebar data and project filtering"
```

---

### Task 4: dashboard.html — project summary strip + filter heading

**Files:**
- Modify: `internal/web/templates/dashboard.html`

**Context:** When `Sidebar.CurrentProject` is empty (All view), show `ProjectSummary` cards above the pipelines table. When a project is selected, show a filter heading. The pipelines are already filtered server-side — the template just renders whatever is in `.Pipelines`.

---

**Step 1: Update `dashboard.html`**

Replace the file with:

```html
{{define "title"}}Dashboard{{end}}
{{define "refresh"}}<meta http-equiv="refresh" content="10">{{end}}
{{define "content"}}

{{if not .Sidebar.CurrentProject}}
{{if .ProjectSummary}}
<h2>Projects</h2>
<div class="project-cards">
  {{range .ProjectSummary}}
  <a href="/?project={{.Namespace}}" class="project-card">
    <div class="project-card-name">{{.Namespace}}</div>
    <div class="project-card-stats">
      {{if .ActiveCount}}<span class="badge badge-in-progress">{{.ActiveCount}} active</span>{{end}}
      {{if .FailedCount}}<span class="badge badge-failed">{{.FailedCount}} failed</span>{{end}}
      <span class="muted" style="font-size:.75rem">{{.TotalCount}} total</span>
    </div>
  </a>
  {{end}}
</div>
{{end}}
{{end}}

<div class="grid-2">
  <div>
    <h2>Pipelines{{if .Sidebar.CurrentProject}} — <span style="font-family:monospace;font-weight:400">{{.Sidebar.CurrentProject}}</span>{{end}}</h2>
    {{if .Pipelines}}
    <table>
      <thead><tr><th>Issue</th><th>Title</th><th>Status</th><th>Stage</th><th>Updated</th></tr></thead>
      <tbody>
      {{range .Pipelines}}
      <tr>
        <td><a href="/pipeline/{{.Issue}}">#{{.Issue}}</a></td>
        <td>
          {{if .SessionDot}}<span class="{{dotClass .SessionDot}}" style="margin-right:.35rem"></span>{{end}}
          {{.Title}}
        </td>
        <td>
          {{if .IsLive}}<span class="badge badge-live">● live</span>{{else}}<span class="{{badgeClass .Status}}">{{.Status}}</span>{{end}}
        </td>
        <td class="muted">{{.CurrentStage}}</td>
        <td class="muted">{{.UpdatedAgo}}</td>
      </tr>
      {{end}}
      </tbody>
    </table>
    {{else}}
    <p class="muted">No pipelines found{{if .Sidebar.CurrentProject}} for <code>{{.Sidebar.CurrentProject}}</code>{{end}}.</p>
    {{end}}
  </div>

  <div>
    <h2>Queue</h2>
    {{if .QueueItems}}
    <ul class="queue-list">
      {{range .QueueItems}}
      <li>
        <span class="muted" style="font-size:.75rem;min-width:1.5rem">{{.Position}}</span>
        {{if .HasPipeline}}<a href="/pipeline/{{.Issue}}">#{{.Issue}}</a>{{else}}<strong>#{{.Issue}}</strong>{{end}}
        <span class="{{badgeClass .Status}}">{{.Status}}</span>
        {{if .DependsOnStr}}<span class="muted" style="font-size:.75rem">→ {{.DependsOnStr}}</span>{{end}}
      </li>
      {{end}}
    </ul>
    {{else}}
    <p class="muted">Queue is empty.</p>
    {{end}}
  </div>
</div>

<h2>Triage <a href="/triage" style="font-size:.75rem;font-weight:400;margin-left:.5rem">view all →</a></h2>
{{if .TriageRows}}
<table>
  <thead><tr><th>Repo</th><th>Issue</th><th>Status</th><th>Stage</th><th>Updated</th></tr></thead>
  <tbody>
  {{range .TriageRows}}
  <tr>
    <td class="muted" style="font-size:.8rem">{{.Repo}}</td>
    <td><a href="/triage/{{.Slug}}/{{.Issue}}">#{{.Issue}}</a></td>
    <td>
      {{if .IsLive}}<span class="badge badge-live">● live</span>{{else}}<span class="{{badgeClass .Status}}">{{.Status}}</span>{{end}}
    </td>
    <td class="muted">{{.CurrentStage}}</td>
    <td class="muted">{{.UpdatedAgo}}</td>
  </tr>
  {{end}}
  </tbody>
</table>
{{else}}
<p class="muted">No triage activity yet.</p>
{{end}}

<h2>Recent Activity</h2>
{{if .RecentActivity}}
<table>
  <thead><tr><th>Time</th><th>Issue</th><th>Event</th><th>Stage</th></tr></thead>
  <tbody>
  {{range .RecentActivity}}
  <tr>
    <td class="muted">{{.TimeAgo}}</td>
    <td><a href="/pipeline/{{.Issue}}">#{{.Issue}}</a></td>
    <td>{{.Event}}</td>
    <td class="muted">{{.Stage}}</td>
  </tr>
  {{end}}
  </tbody>
</table>
{{else}}
<p class="muted">No activity yet.</p>
{{end}}
{{end}}
```

**Step 2: Build and verify**

```bash
go build -o /tmp/factory ./cmd/factory/ && /tmp/factory serve
```

Open http://localhost:17432 — project cards should appear above the pipelines table in the All view. Kill server.

**Step 3: Commit**

```bash
git add internal/web/templates/dashboard.html
git commit -m "feat(web): dashboard project summary strip and filter heading"
```

---

### Task 5: queue.html — namespace badge column

**Files:**
- Modify: `internal/web/templates/queue.html`

**Context:** When viewing the queue unfiltered (All view), each row should show which project it belongs to. The `Namespace` field is already populated by the handler. Show it as a small badge only when `Sidebar.CurrentProject` is empty (no point showing it when already filtered to one project).

---

**Step 1: Update `queue.html`**

```html
{{define "title"}}Queue{{end}}
{{define "refresh"}}<meta http-equiv="refresh" content="30">{{end}}
{{define "content"}}
<h1 style="font-size:1.2rem;font-weight:700;margin-bottom:1.25rem">
  Queue{{if .Sidebar.CurrentProject}} — <span style="font-family:monospace;font-weight:400;font-size:1rem">{{.Sidebar.CurrentProject}}</span>{{end}}
</h1>

{{if .Items}}
<table>
  <thead>
    <tr>
      <th>Pos</th>
      <th>Issue</th>
      {{if not .Sidebar.CurrentProject}}<th>Project</th>{{end}}
      <th>Queue Status</th>
      <th>Depends On</th>
      <th>Pipeline</th>
    </tr>
  </thead>
  <tbody>
  {{range .Items}}
  <tr>
    <td class="muted">{{.Position}}</td>
    <td>
      {{if .HasPipeline}}
        <a href="/pipeline/{{.Issue}}">#{{.Issue}}</a>
      {{else}}
        <strong>#{{.Issue}}</strong>
      {{end}}
    </td>
    {{if not $.Sidebar.CurrentProject}}
    <td>
      {{if .Namespace}}<span class="badge badge-ns">{{.Namespace}}</span>{{else}}<span class="muted">—</span>{{end}}
    </td>
    {{end}}
    <td><span class="{{badgeClass .Status}}">{{.Status}}</span></td>
    <td class="muted">{{if .DependsOnStr}}{{.DependsOnStr}}{{else}}—{{end}}</td>
    <td>
      {{if .HasPipeline}}
        <span class="{{badgeClass .PipelineStatus}}">{{.PipelineStatus}}</span>
      {{else}}
        <span class="muted">no pipeline</span>
      {{end}}
    </td>
  </tr>
  {{end}}
  </tbody>
</table>
{{else}}
<p class="muted">Queue is empty{{if .Sidebar.CurrentProject}} for <code>{{.Sidebar.CurrentProject}}</code>{{end}}.</p>
{{end}}
{{end}}
```

Note: `$.Sidebar` (with `$`) is needed inside `{{range}}` to access the top-level data instead of the current iteration item.

**Step 2: Build and verify**

```bash
go build -o /tmp/factory ./cmd/factory/ && /tmp/factory serve
```

Open http://localhost:17432/queue — should see a "Project" column with namespace badges. Open http://localhost:17432/queue?project=myorg/myapp — column should be hidden, heading shows filter.

**Step 3: Commit**

```bash
git add internal/web/templates/queue.html
git commit -m "feat(web): queue namespace badge column in All view"
```

---

### Task 6: config.html — project filter heading

**Files:**
- Modify: `internal/web/templates/config.html`

**Context:** Config filtering is already done server-side. The template just needs to show a heading indicating which project is selected, and update the empty-state message when a project filter is active.

---

**Step 1: Update `config.html`**

Replace the opening `<h1>` and `{{else}}` block:

```html
{{define "title"}}Config{{end}}
{{define "content"}}
<h1 style="font-size:1.2rem;font-weight:700;margin-bottom:1.25rem">
  Pipeline Configs{{if .Sidebar.CurrentProject}} — <span style="font-family:monospace;font-weight:400;font-size:1rem">{{.Sidebar.CurrentProject}}</span>{{end}}
</h1>

{{if .Repos}}
{{range .Repos}}
<div style="margin-bottom:2.5rem">
  <h2 style="font-size:1rem;font-weight:700;text-transform:none;letter-spacing:0;color:var(--text);margin-bottom:.25rem">
    {{.ConfigName}}
  </h2>
  <p class="muted" style="font-size:.8rem;margin-bottom:1rem;font-family:monospace">{{.Dir}}</p>

  <h2>Stages</h2>
  <table>
    <thead><tr><th>ID</th><th>Type</th><th>Model</th><th>Goal Gate</th><th>Checks After</th></tr></thead>
    <tbody>
    {{range .Stages}}
    <tr>
      <td>
        <strong>{{.ID}}</strong>
        {{if .PromptContent}}
        <br><details style="margin-top:.3rem">
          <summary style="font-size:.75rem;cursor:pointer;color:#0d6efd;user-select:none">
            {{.PromptTemplate}}
          </summary>
          <pre class="prompt" style="margin-top:.5rem;font-size:.72rem;line-height:1.5">{{.PromptContent}}</pre>
        </details>
        {{end}}
      </td>
      <td class="muted">{{.Type}}</td>
      <td class="muted">{{.Model}}</td>
      <td class="muted">{{if .GoalGate}}✓{{end}}</td>
      <td class="muted" style="font-size:.8rem">{{.ChecksAfterStr}}</td>
    </tr>
    {{end}}
    </tbody>
  </table>

  {{if .Checks}}
  <h2>Check Definitions</h2>
  <table>
    <thead><tr><th>Name</th><th>Command</th><th>Parser</th><th>Auto-fix</th><th>Timeout</th></tr></thead>
    <tbody>
    {{range .Checks}}
    <tr>
      <td><strong>{{.Name}}</strong></td>
      <td><code>{{.Command}}</code></td>
      <td class="muted">{{.Parser}}</td>
      <td class="muted">{{if .AutoFix}}✓{{end}}</td>
      <td class="muted">{{.Timeout}}</td>
    </tr>
    {{end}}
    </tbody>
  </table>
  {{end}}
</div>
{{end}}

{{else}}
<p class="muted">
  {{if .Sidebar.CurrentProject}}
  No config found for <code>{{.Sidebar.CurrentProject}}</code>.
  {{else}}
  No pipeline configs found. Configs are discovered automatically from each pipeline's
  worktree path — they'll appear here once pipelines have been created.
  {{end}}
</p>
{{end}}
{{end}}
```

**Step 2: Run all tests**

```bash
go test ./internal/web/ -v
go test ./... 2>&1 | grep -E "FAIL|ok"
```

Expected: all web tests pass; only pre-existing `internal/session` failures.

**Step 3: Build and verify**

```bash
go build -o /tmp/factory ./cmd/factory/ && /tmp/factory serve
```

Walk through the full UI:
- `/` — sidebar shows projects with counts; project cards visible in All view
- `/?project=myorg/myapp` — pipelines filtered, project highlighted in sidebar
- `/queue` — namespace column visible
- `/queue?project=myorg/myapp` — column hidden, heading shows project
- `/config?project=myorg/myapp` — only that project's config shown
- `/pipeline/42` — sidebar highlights the pipeline's own project

**Step 4: Final commit**

```bash
git add internal/web/templates/config.html
git commit -m "feat(web): config project filter heading and empty state"
```
