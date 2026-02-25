# ADR 0001: Multi-Project UI Uses Sidebar Navigation

## Status
Accepted

## Date
2026-02-25

## Context
With multi-project support added, the dashboard shows pipelines from multiple repos in a flat list with no visual grouping. A navigation model was needed to let users scope the UI to a single project.

## Decision
Use a sidebar navigation model where each project (namespace) is listed in a persistent left rail. Clicking a project narrows the entire UI to that project's pipelines, queue, and activity.

## Consequences
The layout shifts from a single-column main area to a sidebar + content split. The sidebar adds persistent project-switching without requiring separate URLs per project. Legacy single-project pipelines (empty Namespace) appear under an "All" or default group.
