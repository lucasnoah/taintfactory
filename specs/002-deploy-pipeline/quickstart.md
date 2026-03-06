# Quickstart: Deploy Pipeline

**Feature**: 002-deploy-pipeline | **Date**: 2026-03-06

## Prerequisites

- Go 1.21+
- PostgreSQL running with `DATABASE_URL` set
- Existing taintfactory project with `pipeline.yaml`

## 1. Run database migration

```bash
factory db migrate
```

## 2. Add deploy section to pipeline.yaml

```yaml
deploy:
  name: my-app-deploy
  stages:
    - id: deploy
      type: agent
      prompt_template: "templates/deploy.md"
      timeout: "10m"
      on_fail: rollback
    - id: smoke-test
      type: agent
      prompt_template: "templates/smoke-test.md"
      timeout: "5m"
    - id: rollback
      type: agent
      prompt_template: "templates/rollback.md"
      timeout: "5m"
```

## 3. Create prompt templates

Create `templates/deploy.md` in the project root (next to `pipeline.yaml`):
```
Deploy commit {{.CommitSHA}} for {{.Namespace}}.
Previous deploy: {{.PreviousSHA}}.
Repository: {{.RepoDir}}.
```

Create `templates/rollback.md`:
```
Rollback to {{.PreviousSHA}} for {{.Namespace}}.
Failed deploy was {{.CommitSHA}}.
```

> **Note**: On the first deploy, `PreviousSHA` will be empty since there is no prior successful deployment.

## 4. Trigger a deploy

```bash
factory deploy create abc1234 --namespace myorg/myapp
```

## 5. Monitor

```bash
# Check status
factory deploy status abc1234

# List all deploys
factory deploy list

# Web UI
factory serve  # then visit /deploys
```

## 6. Orchestrator picks it up

The existing `factory orchestrator check-in` loop will automatically advance the deploy through its stages. No additional setup needed.

## Build & Test

```bash
make build    # Compile
make test     # Run all tests
```
