# ADR 0007: Discord Poller Owns Summary Generation

## Status
Accepted

## Date
2026-03-03

## Context
Claude-generated summaries need to be produced from session logs before posting to Discord. The question was whether the orchestrator writes notify.md after each stage, or the poller generates summaries on demand when it sees new events.

## Decision
The poller owns summary generation. When it detects a new stage_advanced/completed/failed event, it reads the session log and calls claude --print to generate a 1-2 sentence summary before posting to Discord.

## Consequences
The orchestrator stays unchanged — no new responsibilities added to the core pipeline. The poller is self-contained: it reads events, generates summaries, and posts. Trade-off is the poller needs Claude API access and becomes slightly heavier, but all notification logic stays in one place.
