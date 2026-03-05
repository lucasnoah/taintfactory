# K8s Deployment Design

## Goals

1. **Always-on automation** — factory runs 24/7 processing issues without a developer laptop
2. **Team access** — dashboard and status visible to the team via a public URL with auth

## Target Environment

- Personal Kubernetes cluster on DigitalOcean
- Ingress controller ready (nginx-ingress or similar)
- PersistentVolumeClaims backed by DO block storage
- Container images pushed to DigitalOcean Container Registry (DOCR)

## Architecture

```
┌─────────────────── k8s Namespace: taintfactory ────────────────────────┐
│                                                                         │
│  ┌─────────── factory pod (StatefulSet, 1r) ────────────┐              │
│  │                                                       │              │
│  │  factory container         docker:dind sidecar        │              │
│  │  ├─ web UI :8080           ├─ Docker daemon           │              │
│  │  ├─ orchestrator loop      └─ (privileged)            │              │
│  │  ├─ tmux sessions                                     │              │
│  │  └─ git worktrees                                     │              │
│  │                                                       │              │
│  │  PVC /data (20Gi): repos, worktrees, pipelines        │              │
│  └───────────────────────┬───────────────────────────────┘              │
│                          │ pgx                                          │
│  ┌───────────────────────┴───────────────────────────────┐              │
│  │  postgres pod (StatefulSet, 1r)                        │              │
│  │  PostgreSQL 16 · PVC 5Gi                               │              │
│  └────────────────────────────────────────────────────────┘              │
│                                                                         │
│  Ingress → factory:8080 (TLS + basic-auth)                             │
│  Secret: CLAUDE_CODE_OAUTH_TOKEN, GITHUB_TOKEN, PG credentials         │
└─────────────────────────────────────────────────────────────────────────┘
```

## Container Image

Multi-stage Dockerfile:

1. **Build stage** (`golang:1.25`): compile factory binary (pure Go, no CGO — pgx driver)
2. **Runtime stage** (`debian:bookworm-slim`): install tmux, git, gh CLI, node, claude CLI, docker CLI (client only)

Image pushed to DOCR. Estimated size ~400-500MB.

## Pod Design

The factory StatefulSet pod has two containers:

| Container | Image | Role |
|-----------|-------|------|
| `factory` | Custom image | Orchestrator loop + web server + tmux sessions |
| `dind` | `docker:dind` | Docker daemon for build/DB containers spawned by Makefiles |

Communication: `DOCKER_HOST=tcp://localhost:2375` (containers in same pod share localhost).

### Process Management

A new `factory serve --with-orchestrator` flag starts both the web server and the orchestrator check-in loop as goroutines in one process. Benefits:
- Single process — no zombie processes or signal forwarding
- Unified health — pod restarts if either component fails
- Simple logging — one stdout stream

## Data Layer: SQLite → PostgreSQL

Replace SQLite with PostgreSQL to be more server-native:

- **Driver**: `jackc/pgx/v5` (pure Go, no CGO)
- **Deployment**: Single-replica Postgres StatefulSet with 5Gi PVC
- **Connection**: via `DATABASE_URL` env var from k8s Secret
- **Migrations**: embedded SQL files, run on startup
- **Schema changes**: `SERIAL` PKs, `TIMESTAMP` columns, `BOOLEAN` types
- **Tests**: `testcontainers-go` for real Postgres in CI

Bonus: dropping `go-sqlite3` removes the CGO dependency entirely.

## K8s Manifests

| Resource | Name | Notes |
|----------|------|-------|
| StatefulSet | `factory` | 1 replica, 2 containers (factory + dind) |
| StatefulSet | `factory-postgres` | 1 replica, PostgreSQL 16 |
| PVC | `factory-data` | 20Gi, mounted at `/data` in factory pod |
| PVC | `factory-pg-data` | 5Gi, mounted at `/var/lib/postgresql/data` |
| Service | `factory` | ClusterIP, port 8080 |
| Service | `factory-postgres` | ClusterIP, port 5432 |
| Ingress | `factory` | TLS + basic-auth, e.g. `factory.yourdomain.com` |
| Secret | `factory-secrets` | OAuth token, GitHub token, PG credentials, Discord webhook |
| ConfigMap | `factory-config` | pipeline.yaml content (optional) |

## Code Changes

1. **`factory serve --with-orchestrator`** — new flag in `internal/cli/serve.go`, starts orchestrator tick loop in a goroutine
2. **`FACTORY_DATA_DIR` env var** — configurable data directory (default `~/.factory`), affects pipeline store and `.env` lookup
3. **`GET /healthz` endpoint** — liveness/readiness probe for k8s
4. **PostgreSQL migration** — replace `go-sqlite3` with `pgx/v5` in `internal/db/`, port all queries, add migration runner
5. **Entrypoint script** — clones target repos into `/data/repos/` on first boot if not present

## Auth

Web dashboard exposed via Ingress with nginx basic-auth annotation. Credentials stored in a k8s Secret. Can upgrade to OAuth2 proxy later if needed.

## CI/CD (Optional)

GitHub Actions workflow: on push to `main`, build Docker image, push to DOCR. Manual `kubectl rollout restart` to deploy, or add auto-deploy later.

## Rejected Alternatives

- **Split web + worker pods**: Adds complexity (shared PVC, ReadWriteMany) with no benefit at single-instance scale
- **Refactor away from tmux**: Large refactor, loses live-attach debugging. Can do later.
- **Podman/Sysbox**: More secure but less compatible with existing Makefiles or harder to set up on DO nodes
- **Keep SQLite**: Works but less natural for server deployment, blocks future multi-pod evolution
- **DO Managed Postgres**: Overkill and adds monthly cost for a single-instance workload
