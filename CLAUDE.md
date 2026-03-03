# taintfactory

## Decision Tracking

Engineering decisions are automatically tracked via two hooks:
1. A PostToolUse hook logs all AskUserQuestion exchanges to `docs/decisions/raw-log.jsonl`
2. A Stop hook classifies whether a significant decision was made each turn

When you receive additionalContext containing `DECISION_DETECTED:`, you MUST create
an ADR file in `docs/decisions/` using the next sequential number (NNNN format, e.g. 0001).
To find the next number: `ls docs/decisions/[0-9]*.md | tail -1` (or start at 0001).

Use Nygard ADR format:
```
# ADR NNNN: Title
## Status
Accepted
## Date
YYYY-MM-DD
## Context
[1-3 sentences: what prompted this decision]
## Decision
[1-3 sentences: what was decided and why]
## Consequences
[1-3 sentences: what becomes easier or harder]
```

Keep ADRs concise (5-15 lines of content). One decision per file.
File naming: `docs/decisions/NNNN-kebab-case-title.md`
Commit ADR files alongside the code they relate to, not in separate commits.

Do NOT create ADRs for: clarifying questions, simple confirmations, exploration,
or process meta-questions.
