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
4. If the behavior is **not implemented**:
   - Write `{"outcome":"not_implemented","summary":"No existing implementation found"}` to `{{.outcome_file}}`

Write the outcome file as your **final act**. Do nothing else after writing it.
