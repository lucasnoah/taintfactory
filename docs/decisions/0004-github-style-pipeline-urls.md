# ADR 0004: GitHub-Style Pipeline URLs /{owner}/{repo}/{issue}

## Status
Accepted

## Date
2026-02-26

## Context
After adding multi-repo support with namespace scoping, the previous `/pipeline/{issue}` URL structure was ambiguous — issue numbers are only unique per repo, so two repos could each have issue #153 and the URL would be non-deterministic.

## Decision
Pipeline URLs now follow the GitHub-style path `/pipeline/{owner}/{repo}/{issue}` (e.g. `/pipeline/lucasnoah/wptl/171`), matching the namespace format already used for pipeline state storage and the DB unique constraint `(namespace, issue)`.

## Consequences
All internal links in the web UI (dashboard, queue, attempt pages, SSE stream endpoint) use the new URL structure. Legacy `/pipeline/{issue}` URLs return 404. The pipeline store gained `GetForNamespace(namespace, issue)` for direct O(1) lookup without a filesystem walk.
