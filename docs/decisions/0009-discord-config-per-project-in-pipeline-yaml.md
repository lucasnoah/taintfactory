# ADR 0009: Discord Configuration Is Per-Project in pipeline.yaml

## Status
Accepted

## Date
2026-03-03

## Context
Multiple projects run through the same factory instance. Each project should be able to post to its own Discord server and channel. Configuration needs a home — options were global factory config or per-project pipeline.yaml.

## Decision
Discord webhook URL and channel configuration lives in each project's pipeline.yaml under a notifications.discord block. Projects without this block receive no Discord notifications. The poller reads config from the pipeline's stored config_path when processing events.

## Consequences
Each project independently controls its Discord destination. Adding notifications to a project is a one-line pipeline.yaml change. No global factory config needed. The poller must load the correct pipeline.yaml per namespace when processing events.
