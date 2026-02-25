# Multi-Project UI Design

## Goal

Update the TaintFactory web UI so that users managing pipelines across multiple repos can scope the UI to a single project, see cross-project progress at a glance, and manage the queue in a project-aware way.

## Architecture

A persistent left sidebar replaces the current horizontal nav. Sidebar lists all known projects (namespaces) with a simple name + active count. Project selection passes `?project=org/repo` as a query parameter — no new routes. All existing handlers read this param and filter their data accordingly. A `sidebarProjects()` helper on `Server` is called by every handler and injects `SidebarData` into every view model so `base.html` can render the sidebar consistently.

## Layout

Two-column shell: fixed sidebar (~200px) + scrollable main content area. Top nav bar collapses into the sidebar.

```
┌──────────────┬──────────────────────────────┐
│ TaintFactory │                              │
│              │  [main content area]         │
│ ● All        │                              │
│   myorg/app 3│                              │
│   other/svc 1│                              │
│ ─────────── │                              │
│   Triage     │                              │
│   Queue      │                              │
│   Config     │                              │
└──────────────┴──────────────────────────────┘
```

Active project is highlighted. "All" is selected when `?project=` is absent. Legacy pipelines (empty namespace) are included in "All" but don't appear as a separate sidebar entry.

## Filtering Behavior

**Dashboard (`/?project=org/repo`):** Filters pipelines table and queue panel to the selected namespace. Recent activity filtered similarly. Triage section unfiltered (future work).

**Queue (`/queue?project=org/repo`):** Filters to queue items whose associated pipeline matches the namespace, or whose `config_path` (stored in `issue_queue` since schema v5) maps to that namespace via `namespaceFromConfigPath()`.

**Config (`/config?project=org/repo`):** Shows only the config block for the selected project.

**Pipeline/attempt detail:** No filtering — already scoped to one issue.

## Cross-Project Summary Strip

On the "All" dashboard (no project selected), a summary strip above the pipelines table shows one card per namespace with aggregate active count and a rough progress indicator. Each card links to `/?project=org/repo`. Legacy pipelines excluded from this strip.

## Project-Aware Queue (All View)

When viewing the queue unfiltered, each row gets a project badge derived from `config_path` in the queue item. Implemented via a `namespaceFromConfigPath(path string) string` helper on the server (strips hostname, takes `org/repo` segment from the path).

## Data Model Changes

**New types:**
```go
type ProjectSidebarItem struct {
    Namespace   string
    ActiveCount int
    IsSelected  bool
}

type SidebarData struct {
    Projects       []ProjectSidebarItem
    CurrentProject string // empty = All
}
```

**Every *Data view model** gains a `Sidebar SidebarData` field.

**`QueueRowView`** gains a `Namespace string` field (shown only in All view).

## Server Changes

- `sidebarData(currentProject string) SidebarData` — scans all pipelines, groups by namespace, counts active (status == "in_progress"), returns sorted list
- `namespaceFromConfigPath(path string) string` — derives `org/repo` from an absolute config path (mirrors orchestrator logic)
- `currentProject(r *http.Request) string` — reads `?project=` query param
- All handlers call these and include `Sidebar` in their data structs

## Template Changes

- `base.html` — two-column layout, sidebar rendered from `$.Sidebar`
- `dashboard.html` — summary strip (All view), namespace filter on pipelines + queue
- `queue.html` — namespace badge column in All view
- `config.html` — filter to selected project's block

## Testing

- Unit tests for `sidebarData()`: verifies grouping, active counts, IsSelected flag
- Unit tests for `namespaceFromConfigPath()`: various path patterns
- Existing `configForPS` tests unchanged

## Out of Scope

- Triage namespace filtering (future)
- Per-project pipeline detail pages (not needed with sidebar model)
- Persistent project selection across sessions (query param is sufficient)
