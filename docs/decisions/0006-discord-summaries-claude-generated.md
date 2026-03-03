# ADR 0006: Discord Stage Summaries Are Claude-Generated

## Status
Accepted

## Date
2026-03-03

## Context
Discord notifications need a human-readable summary of what happened in each agent stage. Options considered: raw git commit message, last N lines of session log, or a Claude-generated summary from the session log.

## Decision
Use a Claude call to read the session log and produce a 1-2 sentence summary written to a `notify.md` file in the stage directory. The poller picks this up when posting to Discord.

## Consequences
Summaries are high quality and readable. Adds a small latency between stage completion and notification availability. Requires a claude --print call per agent stage, which costs tokens but runs asynchronously from the pipeline itself.
