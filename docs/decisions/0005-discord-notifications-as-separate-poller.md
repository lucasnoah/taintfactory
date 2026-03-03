# ADR 0005: Discord Notifications as Separate Poller Process

## Status
Accepted

## Date
2026-03-03

## Context
Discord stage notifications need to post after each pipeline stage completes. Two approaches were considered: integrating posting directly into the orchestrator, or running a separate process that polls pipeline_events.

## Decision
Implement Discord notifications as a standalone poller process (`factory discord`). It polls the pipeline_events table for new stage_advanced/completed/failed rows and posts independently of the orchestrator.

## Consequences
Pipeline functionality has zero dependency on Discord availability — notifications are best-effort. The poller is restartable and deployable independently, but introduces a polling interval lag between stage completion and notification.
