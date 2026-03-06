# ADR 0013: Ratify 8-Principle Project Constitution

## Status
Accepted

## Date
2026-03-06

## Context
TaintFactory had no formal governing document for architectural principles and development constraints. Decisions were scattered across CLAUDE.md and tribal knowledge.

## Decision
Ratified a project constitution (v1.0.0) with 8 core principles: crash-safe pipeline state, CLI-first interface, sequential pipeline processing, testability via interfaces, append-only event log, pipeline configuration as data, prompt transparency, and simplicity. PostgreSQL (pgx/v5) is the sole database; pipeline state uses JSON files on disk.

## Consequences
All future PRs and specs must align with these principles. Deviations require documented justification. Constitution amendments follow semver (MAJOR/MINOR/PATCH) with sync impact reports.
