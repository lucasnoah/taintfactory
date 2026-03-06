#!/usr/bin/env bash
# PostToolUse hook for AskUserQuestion — captures Q&A exchanges to JSONL log.
#
# Input: JSON on stdin from Claude Code PostToolUse event
# Output: Appends one JSONL line to docs/decisions/raw-log.jsonl

set -euo pipefail

INPUT=$(cat)
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
LOG_FILE="$PROJECT_ROOT/docs/decisions/raw-log.jsonl"

mkdir -p "$(dirname "$LOG_FILE")"

echo "$INPUT" | jq -c '{
  timestamp: (now | strftime("%Y-%m-%dT%H:%M:%SZ")),
  session_id: .session_id,
  questions: (.tool_input.questions // []),
  answers: (.tool_response // {})
}' >> "$LOG_FILE"

exit 0
