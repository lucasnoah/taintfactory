# Design: Print-Mode Triage Stages

## Problem

The 8 classifier stages added to the deathcookies pipeline are fundamentally different
from `stale_context` and `already_implemented`. The first two stages are **agentic**:
they search the codebase, post GitHub comments, close issues. They need tool access,
persistent sessions, and idle detection.

The classifiers are **stateless reasoning tasks**. Each one reads the issue body,
maybe checks one file, and outputs a binary yes/no. Running them as full interactive
Claude Code sessions costs:

- 8 × 15s boot wait = 2+ minutes of pure overhead
- 8 × issue body tokenized as a fresh prompt
- Duplicate file reads (e.g. `api/openapi.yaml` across multiple classifiers)
- 8 tmux sessions, 8 DB rows, 8 hooks configs written/cleaned up

For 10 stages total per issue, the tail of classifiers dominates the total runtime.

## Solution: `mode: print`

Add a `mode` field to `TriageStage`. When `mode: print`, the runner executes the stage
synchronously using `claude --print` rather than creating a tmux session. This means:

- No tmux session created
- No boot wait
- No idle detection loop
- Runner captures stdout directly, parses it as the outcome JSON
- Stage completes in the same `Advance()` call it starts in

## Label Side Effects

The existing classifier templates tell the Claude session to run `gh issue edit --add-label`.
In print mode the agent still has tool access and CAN do this — but relying on the agent
to correctly emit both a side-effect command and clean stdout JSON is fragile.

**Decision**: move label application to the runner.

Add a `label` field to `TriageStage`. When a print-mode stage resolves with outcome `yes`,
the runner calls `gh issue edit <issue> --add-label <label>` itself via `exec.Command`.
This keeps templates purely declarative (just output the JSON) and makes label logic
explicit in config, not buried in a prompt.

The same `label` field can eventually be backfilled onto non-print stages to replace
hardcoded `gh issue edit` calls in their templates — but that's out of scope here.

## Template Changes

Print-mode templates drop the "Actions" section entirely. They just:
1. Present the classifier question and rules
2. Present the issue content
3. End with: "Output JSON only: `{\"outcome\":\"yes\",\"summary\":\"...\"}` or
   `{\"outcome\":\"no\",\"summary\":\"...\"}`"

This matches what the original classifier prompts already said — we're restoring their
original intent rather than bolting on instructions for file writes and gh commands.

## Parsing Strategy

`claude --print` stdout may contain conversational preamble if the model doesn't comply
perfectly with "JSON only". The runner should:
1. First try `json.Unmarshal` on the trimmed full output
2. If that fails, scan lines in reverse for the last line that parses as valid JSON
3. If that also fails, return an error (not silently skip)

## Changes Required

### `internal/triage/config.go`
- Add `Mode string \`yaml:"mode"\`` to `TriageStage` (valid values: `""`, `"print"`)
- Add `Label string \`yaml:"label"\`` to `TriageStage` (GitHub label to add on `yes`)

### `internal/triage/runner.go`
- `runPrintStage(issue int, stageID, title, body string) TriageAction`
  - Renders prompt via existing `renderPrompt`
  - Executes `claude --print [-m <model>] --dangerously-skip-permissions <prompt>`
    via `exec.CommandContext` with a stage-level timeout
  - Parses stdout for `TriageOutcome` JSON
  - Writes outcome file via `store.EnsureOutcomeDir` + atomic write
  - If outcome is `yes` and stage has a `label`, runs `gh issue edit` to apply it
  - Updates triage state: advances to next stage or marks completed
  - Returns a `TriageAction` with `action: "advance"` or `"completed"`
- `Advance()`: when advancing to a print-mode stage (or when a pending pipeline's
  first stage is print-mode), call `runPrintStage` instead of `startStage`
- `advanceOne()`: if in_progress stage is print-mode with empty `CurrentSession`,
  call `runPrintStage` directly (handles the case where we landed in print mode
  mid-pipeline after an async stage)

### `deathcookies/triage.yaml`
- Add `mode: print` and `label: <name>` to each of the 8 classifier stages
- Remove `prompt_template` overrides (not needed; runner still finds `triage/<id>.md`)

### `deathcookies/triage/*.md` (classifier templates)
- Remove "Actions" section (label application, outcome file writing)
- Add clean JSON-only output instruction at the end

## What We're NOT Doing

- Batching all 8 classifiers into a single `--print` call. This would be faster but
  breaks the per-stage history tracking and outcome routing. The gains from `mode: print`
  already eliminate the dominant cost (boot wait × 8). Batching is a further optimization
  we can revisit if the sequential print calls are still too slow.

- Pre-fetching files into a shared context document. Unnecessary once boot overhead is
  removed — the remaining cost is one file read per stage, which is cheap.

- Changing `stale_context` or `already_implemented` to print mode. They need real
  agentic tool use (codebase search, GitHub comments, closing issues). They stay as
  interactive sessions.

## Open Question

`claude --print` with `--dangerously-skip-permissions` will still have tool access.
For classifiers that need to check a file (e.g. `display_only` reading `api/openapi.yaml`),
this is fine — the tool call happens silently and the text response is captured on stdout.
We need to verify in practice that tool use doesn't pollute stdout with non-JSON content.
If it does, inject the file content directly into the template via a new template variable
rather than letting the agent read it.
