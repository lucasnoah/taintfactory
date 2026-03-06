# CLI Contract: factory deploy

**Feature**: 002-deploy-pipeline | **Date**: 2026-03-06

## Commands

### factory deploy create \<commit-sha\>

Creates a new deploy pipeline for the given commit SHA.

**Arguments**: `<commit-sha>` (required) -- the git commit SHA to deploy.

**Flags**:
- `--namespace <string>` -- project namespace (e.g., "myorg/myapp"). Default: "".

**Output (stdout)**:
```
Deploy pipeline created for abc1234
  Previous SHA: def5678
  First stage:  deploy
  Status:       pending
```

**Errors (stderr)**:
- "no deploy: section found in pipeline config" -- when pipeline.yaml lacks deploy section
- "deploy pipeline has no stages" -- when deploy section has empty stages list
- "deploy abc1234 already exists" -- when duplicate SHA

**Exit codes**: 0 success, 1 error.

---

### factory deploy list

Lists recent deploys in tabular format.

**Flags**:
- `--limit <int>` -- max deploys to show. Default: 20.
- `--format <string>` -- output format: "table" (default) or "json".

**Output (stdout)**:
```
SHA      STATUS       STAGE        NAMESPACE     CREATED
abc1234  completed    smoke-test   myorg/myapp   2026-03-06T10:00:00Z
def5678  in_progress  deploy       myorg/myapp   2026-03-06T09:30:00Z
```

**Output when empty**: "No deploys found."

**Exit codes**: 0 success, 1 error.

---

### factory deploy status \[sha\]

Shows detailed deploy status. If no SHA given, shows the most recent deploy.

**Arguments**: `[sha]` (optional) -- commit SHA to inspect.

**Flags**:
- `--format <string>` -- output format: "table" (default) or "json".

**Output (stdout)**:
```
Deploy: abc1234567890
  Status:       completed
  Stage:        smoke-test
  Attempt:      1
  Previous SHA: def456
  Created:      2026-03-06T10:00:00Z
  Updated:      2026-03-06T10:15:00Z
  History:
    deploy: success (attempt 1, 5m30s)
    smoke-test: success (attempt 1, 2m15s)
```

**Errors (stderr)**:
- "no deploys found" -- when no deploys exist and no SHA given
- "deploy abc1234 not found" -- when specified SHA doesn't exist

**Exit codes**: 0 success, 1 error.

## Web UI Route

### GET /deploys

Renders HTML table of recent deploys with status badges. Read-only, no mutation. Data sourced from `deploys` DB table. Sidebar link added under "Views" section.
