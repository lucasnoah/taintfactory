# ADR 0012: Multi-Repo Database Autoconfiguration

## Status
Accepted

## Date
2026-03-06

## Context
Adding multi-repo support revealed that the shared Postgres sidecar only has the factory database. Repos like wptl need their own databases, but there's no mechanism to provision them, inject DATABASE_URL into sessions, or run setup/migration commands.

## Decision
Each repo declares a `database:` section in pipeline.yaml (name, user, password, migrate). Factory auto-provisions a separate Postgres database per repo on `factory repo add`, re-checks idempotently on pod restart, and exports DATABASE_URL into tmux sessions before launching claude. A general `env:` map supports arbitrary per-repo env vars. Setup commands execute before each issue's first stage.

## Consequences
Repos get isolated databases with zero manual DB admin. Adding a new repo with DB needs is a single `factory repo add` command. The tradeoff is that database credentials live in plaintext in pipeline.yaml — acceptable for dev/CI databases on a private sidecar, but would need secrets management for production use.
