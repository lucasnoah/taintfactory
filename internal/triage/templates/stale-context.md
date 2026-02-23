You are triaging GitHub issue #{{.issue_number}}: "{{.issue_title}}".

Your task: determine whether the issue's context is **stale** (references code that no longer exists) or **clean** (still valid).

## Issue body

{{.issue_body}}

## Instructions

1. Extract every concrete reference from the issue body: file paths, function names, type names, CLI commands, package names, error messages that mention specific code.
2. For each reference, verify it still exists in the codebase using tools like `ls`, `grep`, or reading relevant files. The repo root is `{{.repo_root}}`.
3. If **any** reference is stale (no longer exists or has moved):
   - Post a comment on the issue explaining what's gone: `gh issue comment {{.issue_number}} --body "..."`
   - Create the label if it doesn't exist: `gh label create stale --color "#e4e669" --force 2>/dev/null; true`
   - Add the `stale` label: `gh issue edit {{.issue_number}} --add-label stale`
   - Write `{"outcome":"stale","summary":"<one sentence>"}` to `{{.outcome_file}}`
4. If all references check out (or the issue has no concrete references):
   - Write `{"outcome":"clean","summary":"All referenced symbols found"}` to `{{.outcome_file}}`

Write the outcome file as your **final act**. Do nothing else after writing it.
