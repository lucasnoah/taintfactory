# ADR 0008: Discord Notifications Use Rich Embeds

## Status
Accepted

## Date
2026-03-03

## Context
Discord notifications can be plain text or rich embeds with labeled fields. The goal is to surface stage duration, fix rounds, stage-specific summaries (implement: what was built; review/qa/contract-check: what was flagged, what changed, open questions remaining).

## Decision
Use Discord rich embeds with labeled fields: Stage, Duration, Fix Rounds, Summary, Changes, and Open Questions. Stage-type-specific prompts drive the Claude-generated content — implement stages summarize what was built; review/qa/contract-check stages highlight flags, diffs, and unresolved questions.

## Consequences
Notifications are scannable and information-dense. Requires constructing Discord embed payloads (JSON) rather than plain webhook messages. Open Questions field surfaces follow-up work that might otherwise get lost in session logs.
