# taintfactory

A CLI-driven software factory that orchestrates Claude Code sessions through configurable pipelines with automated checks, persistent sessions, and browser-based QA.

## Architecture

- **Orchestrator**: Long-running agent that checks in via cron, queries pipeline state, and advances work
- **factory CLI**: Stateless CLI that manages pipelines, sessions, checks, and context — the orchestrator's primary tool
- **Claude Code sessions**: Run in tmux, report state via hooks to SQLite, human-attachable
- **Check system**: Deterministic gates (lint, format, typecheck, test) with auto-fix and structured records
- **SQLite event store**: Session state transitions, check results, pipeline lifecycle — queryable by orchestrator and humans

## Status

Under development.
