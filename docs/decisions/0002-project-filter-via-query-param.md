# ADR 0002: Project Filtering Uses Query Parameter

## Status
Accepted

## Date
2026-02-25

## Context
The sidebar navigation needs a mechanism to scope the dashboard, queue, and config pages to a single project (namespace). Three options were considered: query-param filter, per-project routes, and client-side JS filter.

## Decision
Use a `?project=org/repo` query parameter on existing routes. The sidebar sets this param on navigation; all existing handlers read it to filter their data. No new routes or templates required.

## Consequences
URLs are shareable and bookmarkable per project. Existing handlers need a small filter pass on the namespace field. The sidebar must be passed the current project selection from each handler so it can highlight the active entry.
