You are triaging GitHub issue #{{.issue_number}}: "{{.issue_title}}".

Your task: determine whether the behavior described in this issue **already exists** in the codebase.

## Issue body

{{.issue_body}}

## Instructions

1. Read the issue carefully and identify the specific behavior or feature it requests.
2. Search the codebase for evidence that this behavior is already implemented (grep, read files, check tests). The repo root is `{{.repo_root}}`.
3. If the behavior **is already implemented**:
   - Post a comment with evidence: `gh issue comment {{.issue_number}} --body "Already implemented in <file>:<line> — <brief explanation>"`
   - Close the issue: `gh issue close {{.issue_number}} --reason completed`
   - Write `{"outcome":"implemented","summary":"<where it was found>"}` to `{{.outcome_file}}`
4. If the behavior is **partially implemented** (some parts exist, some don't):
   - Post a comment listing what exists and what's missing: `gh issue comment {{.issue_number}} --body "Partially implemented:\n\n**Already exists:**\n- <list>\n\n**Not yet implemented:**\n- <list>"`
   - Create the label if it doesn't exist: `gh label create partial --color "#FFA500" --description "Partially implemented" --force 2>/dev/null; true`
   - Add the `partial` label: `gh issue edit {{.issue_number}} --add-label partial`
   - Write `{"outcome":"partial","summary":"<brief summary of what exists vs what's missing>"}` to `{{.outcome_file}}`
5. If the behavior is **not implemented**:
   - Write `{"outcome":"not_implemented","summary":"No existing implementation found"}` to `{{.outcome_file}}`

Write the outcome file as your **final act**. Do nothing else after writing it.
