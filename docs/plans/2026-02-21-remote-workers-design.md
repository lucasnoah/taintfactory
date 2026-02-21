# Remote Worker Infrastructure Design

**Date:** 2026-02-21
**Status:** Approved

## Overview

Move TaintFactory from a local development process to a managed remote development system. Persistent worker pods run on a DigitalOcean Kubernetes cluster, each operating as a fully autonomous dev environment with its own tmux sessions, SQLite state, and credentials. A new `factory worker` subcommand group on the operator's laptop provides dispatch, monitoring, and drop-in access — all over `kubectl exec`, with no new services required.

The MVP proves out the workflow with a single worker pod before scaling.

---

## Architecture

```
laptop
  factory worker dispatch worker-1 --project deathcookies --issue 23
       │
       └─► kubectl exec factory-worker-1 ── factory queue add ...
                    │
              [orchestrator tmux session]  — always running, manages pipeline
                    │
              [dev / qa / ... tmux sessions]  — created/killed by orchestrator
                       ▲
  factory worker attach worker-1  ──────────────────────────────┘
```

**Key decisions:**

- **Persistent pods** — worker pods stay running continuously; tmux sessions survive across work items
- **kubectl exec only** — no ingress, no SSH, no new services; all remote ops go through the k8s API
- **SQLite per pod** — each worker keeps its own local SQLite under `~/.factory/`; no shared DB needed for MVP with push dispatch
- **Push dispatch** — the operator explicitly assigns work to a named worker; no claiming/locking logic required
- **DinD (Docker-in-Docker)** — pods run with `privileged: true` to support `docker compose` for project test stacks (postgres, backend, frontend)

---

## New CLI Commands

Worker metadata lives in `~/.factory/workers.yaml` on the operator's laptop.

```yaml
workers:
  - name: worker-1
    pod: factory-worker-1
    namespace: factory
    context: do-nyc1-factory  # kubectl context name
```

### Commands

```
factory worker list
  # Show all workers with current status and active sessions
  # NAME        POD                   STATUS    SESSIONS              QUEUE
  # worker-1    factory-worker-1      running   orchestrator, qa      deathcookies#23

factory worker dispatch <worker> --project <repo> --issue <n>
  # Execs: factory queue add --project <repo> --issue <n> on the pod

factory worker status <worker>
  # Execs: factory status on the pod; prints pipeline/queue state locally

factory worker peek <worker> [--session <name>]
  # Execs: tmux capture-pane on the pod; shows recent output without attaching
  # Omitting --session lists available sessions

factory worker attach <worker> [--session <name>]
  # Execs: tmux attach -t <session> on the pod
  # Omitting --session lists available sessions and prompts for selection
```

All commands resolve the pod and namespace from `~/.factory/workers.yaml` and shell out to `kubectl exec`.

---

## Dynamic Session Handling

The existing orchestrator manages tmux sessions inside the pod exactly as it does locally — creating sessions for each pipeline stage and destroying them on completion. Nothing changes in the orchestrator.

The CLI adapts to dynamic sessions by introspecting via `tmux ls` before attaching or peeking:

```
$ factory worker attach worker-1
  Available sessions:
  1. orchestrator  (idle)
  2. qa-review     (active - last output 30s ago)
> attach to: 2
```

`factory worker status` includes live session list in its output.

---

## Pod Setup

### k8s Resources (per worker)

| Resource | Name | Purpose |
|---|---|---|
| Namespace | `factory` | Isolates all worker resources |
| Deployment | `factory-worker-1` | Single-replica persistent pod |
| Secret | `factory-worker-1-secrets` | Claude OAuth token, GH token |
| PersistentVolumeClaim | `factory-worker-1-data` | Mounts to `/root/.factory/`; persists SQLite, pipeline state, session output |

### Secret contents

```
CLAUDE_CODE_OAUTH_TOKEN=<per-worker token>
GH_TOKEN=<taintfactory-bot PAT>
GH_USER=taintfactory-bot
```

### Security context

```yaml
securityContext:
  privileged: true  # Required for DinD
```

### Resource sizing

```yaml
resources:
  requests:
    cpu: "2"
    memory: "4Gi"
  limits:
    cpu: "4"
    memory: "8Gi"
```

Sized to support the factory orchestrator plus a typical project stack (postgres + backend + frontend) running concurrently via `docker compose`.

---

## Docker Image

Base: `golang:1.23-bookworm`

Installed toolchain:
- `docker` — Docker daemon + CLI (for DinD)
- `docker compose` — project test stack management
- `gh` — GitHub CLI (authenticated at startup via `GH_TOKEN`)
- `tmux` — session management
- `git` — source control
- `factory` binary — copied in at image build time

### Entrypoint sequence

1. Start Docker daemon (`dockerd &`), wait for socket ready
2. Authenticate `gh` with `$GH_TOKEN`
3. Start tmux server
4. Launch `factory orchestrator check-in` in a new `orchestrator` session
5. `sleep infinity` to keep pod alive

### Image updates

Rebuild locally → push to DO Container Registry → `kubectl rollout restart deployment/factory-worker-1`.

---

## GitHub Bot Account

A dedicated GitHub account (`taintfactory-bot`) is created once and provisioned with:
- A Personal Access Token with `repo` and `workflow` scopes
- SSH key for git operations
- Added as a collaborator to target project repos as needed

The PAT is stored in the per-worker k8s Secret and injected at pod startup.

---

## MVP Scope

Prove out with one worker pod and one project. Explicitly out of scope for MVP:

- GUI / web dashboard (CLI first)
- Pull-based dispatch / auto-assignment
- Worker auto-scaling
- Homebrew or package manager distribution
- Multi-worker orchestration in a single check-in loop

---

## What Does Not Change

- Existing orchestrator check-in loop
- Pipeline stage logic and tmux session lifecycle
- SQLite schema (per-worker; no shared DB)
- `factory queue`, `factory status`, and all existing commands (run unchanged inside the pod)
