# ADR 0010: Discord Poller Baked Into Orchestrator Check-In
## Status
Accepted
## Date
2026-03-03
## Context
The Discord notification poller needs to run periodically to drain pipeline events and post embeds. The orchestrator already runs `factory orchestrator check-in` on a tight loop (every 10s).
## Decision
Call `discordPollTick()` at the end of every `check-in` invocation rather than running a separate long-lived `factory discord run` process. The tick is non-fatal — Discord errors log to stderr and do not block the orchestrator.
## Consequences
No extra process to manage or restart. Notifications fire on the same cadence as pipeline advances. The standalone `factory discord run` command is retained for ad-hoc use.
