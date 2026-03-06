# Config Contract: deploy section

**Feature**: 002-deploy-pipeline | **Date**: 2026-03-06

## YAML Schema

The `deploy:` section is a top-level key in `pipeline.yaml`, sibling to `pipeline:`.

```yaml
# pipeline.yaml

pipeline:
  name: my-app
  # ... existing pipeline config ...

deploy:
  name: my-app-deploy          # Required: human-readable name
  stages:                      # Required: ordered list of deploy stages
    - id: deploy               # Required: unique stage identifier
      type: agent              # Stage type (default: agent)
      prompt_template: "templates/deploy.md"  # Path to prompt template
      timeout: "10m"           # Stage timeout (Go duration format)
      on_fail: rollback        # Stage ID to route to on failure (optional)

    - id: smoke-test
      type: agent
      prompt_template: "templates/smoke-test.md"
      timeout: "5m"
      on_fail: debug

    - id: debug
      type: agent
      prompt_template: "templates/debug-deploy.md"
      timeout: "5m"
      on_fail: rollback

    - id: rollback
      type: agent
      prompt_template: "templates/rollback.md"
      timeout: "5m"
```

## Stage Fields

Deploy stages reuse the existing `config.Stage` struct. All fields available for implementation pipeline stages are also available for deploy stages.

Key fields for deploy use:
- `id` (string, required): Unique stage identifier
- `type` (string): Stage type. Default "agent" for Claude sessions
- `prompt_template` (string): Path to prompt template file
- `timeout` (string): Go duration string (e.g., "10m", "1h")
- `on_fail` (string|object): Stage ID to route to on failure
- `model` (string): Claude model override
- `flags` (string): Claude Code flags override

## Defaults Inheritance

Deploy stages inherit `model` and `flags` from `pipeline.defaults` if not explicitly set on the stage. This matches the behavior of implementation pipeline stages.

## Absence Handling

When no `deploy:` section exists in `pipeline.yaml`, `PipelineConfig.Deploy` is nil. Running `factory deploy create` returns an error. No other behavior is affected.

## Template Variables

Deploy stage prompt templates have access to these additional variables:

| Variable      | Type   | Description                                  |
|---------------|--------|----------------------------------------------|
| CommitSHA     | string | The commit SHA being deployed                |
| PreviousSHA   | string | The last successfully deployed commit SHA    |
| Namespace     | string | Project namespace (e.g., "myorg/myapp")      |
| RepoDir       | string | Absolute path to the repository root         |
