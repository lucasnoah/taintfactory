# Multi-Project + Distributed Coordinator Design

**Date:** 2026-02-25
**Status:** Approved
**Supersedes:** `2026-02-21-remote-workers-design.md` (kubectl exec / push dispatch model)

---

## Overview

Two related evolutions:

1. **Multi-project support** — a single factory instance can manage issues across multiple GitHub repos, each with its own `pipeline.yaml`.
2. **Coordinator/worker split** — factory becomes a three-tier system: CLI clients → a central coordinator → stateless workers. Both coordinator and workers can run on k8s.

These are designed as sequential phases. Phase 1 (multi-project) ships first and is independently useful. Phases 2–3 build on it without breaking it.

---

## Architecture

```
Developer laptop (or CI)
  factory queue add --config ~/projects/myapp/pipeline.yaml 42
  factory pipeline status
  factory web
      │
      │  HTTP REST + SSE
      ▼
┌─────────────────────────────────────────────────────────────┐
│              COORDINATOR  (single leader)                    │
│                                                             │
│  ┌───────────────┐  ┌────────────────┐  ┌───────────────┐  │
│  │  Orchestrator │  │  gRPC Server   │  │  HTTP API     │  │
│  │  (check-in)   │─►│  (job router)  │  │  + SSE        │  │
│  └───────────────┘  └────────────────┘  └───────────────┘  │
│                              │                   │           │
│  ┌───────────────────────────┴───────────────────┴───────┐  │
│  │   State: SQLite + ~/.factory/pipelines/{org}/{repo}/  │  │
│  └───────────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────────┘
                              │  gRPC (outbound from workers)
         ┌────────────────────┼────────────────────┐
         ▼                    ▼                    ▼
┌──────────────┐   ┌──────────────┐   ┌──────────────────┐
│  WORKER      │   │  WORKER      │   │  WORKER          │
│  (local)     │   │  (Docker)    │   │  (k8s pod)       │
│              │   │              │   │                  │
│  clone repo  │   │  clone repo  │   │  clone repo      │
│  run Claude  │   │  run Claude  │   │  run Claude      │
│  stream logs │   │  stream logs │   │  stream logs     │
└──────────────┘   └──────────────┘   └──────────────────┘
```

**Key principles:**
- Coordinator is the single source of truth. State never lives on workers.
- Workers only initiate outbound connections to the coordinator. No inbound ports needed (NAT/firewall friendly, clean k8s pattern).
- Coordinator sends fully-rendered prompts to workers. Workers have zero understanding of pipeline config or template logic.
- CLI works in two modes: **embedded** (coordinator runs in-process, current behavior) and **client** (points to remote coordinator via `FACTORY_COORDINATOR_URL`).

---

## Phase 1: Multi-Project Support

### What changes

**Pipeline state** gains two new fields:

```go
type PipelineState struct {
    // existing fields...
    ConfigPath string  // absolute path to pipeline.yaml for this issue
    RepoDir    string  // absolute path to git repo root (parent of pipeline.yaml)
}
```

**Storage namespace** moves from flat to repo-scoped:

```
before: ~/.factory/pipelines/{issue}/pipeline.json
after:  ~/.factory/pipelines/{org}/{repo}/{issue}/pipeline.json
```

The `{org}/{repo}` is derived from `pipeline.repo` in the config (e.g. `github.com/myorg/myapp` → `myorg/myapp`).

**Repo root** is derived from the config file's parent directory. A `pipeline.yaml` at `/home/user/projects/myapp/pipeline.yaml` means the repo root is `/home/user/projects/myapp/`.

**Queue** gains a `config_path` column so each enqueued issue carries its config reference:

```sql
ALTER TABLE issue_queue ADD COLUMN config_path TEXT NOT NULL DEFAULT '';
```

**CLI**: `factory queue add` gains a `--config` flag. Defaults to current behavior (searches `./pipeline.yaml` then `~/.factory/config.yaml`) if omitted.

```
factory queue add --config ~/projects/myapp/pipeline.yaml 42 43
factory queue add --config ~/projects/backend/pipeline.yaml 101
```

**Orchestrator**: loads config per pipeline on each check-in instead of once at startup. The orchestrator scan globs `~/.factory/pipelines/**/{issue}/pipeline.json`.

Backward compat: also scan the legacy flat path `~/.factory/pipelines/{issue}/pipeline.json` for existing pipelines.

### What does not change

- Stage logic, check runners, prompt rendering
- tmux session management
- Web dashboard
- All other CLI commands

---

## Phase 2: Coordinator as a Service

### What changes

`factory coordinator serve` starts an HTTP + gRPC server. All existing CLI commands gain an HTTP client path:

```go
// Before
func runQueueAdd(issue int) {
    db := openDB()
    db.QueueAdd(issue)
}

// After
func runQueueAdd(issue int) {
    if coordinatorURL := os.Getenv("FACTORY_COORDINATOR_URL"); coordinatorURL != "" {
        httpClient.Post(coordinatorURL + "/api/queue", ...)
    } else {
        db := openDB()   // embedded mode, same as before
        db.QueueAdd(issue)
    }
}
```

**HTTP API** exposed by coordinator:

```
POST   /api/queue              add issue to queue
GET    /api/queue              list queue
DELETE /api/queue/{issue}      remove from queue
GET    /api/pipelines          list all pipelines
GET    /api/pipelines/{id}     pipeline status
POST   /api/pipelines/{id}/retry
GET    /api/logs/{job_id}      SSE stream of log lines for a job
GET    /api/dashboard          web UI data
```

**Web UI** SSE endpoint moves from reading local tmux scrollback to reading the coordinator's log buffer. The browser experience is unchanged.

### k8s deployment

```yaml
# Coordinator: single-replica Deployment
apiVersion: apps/v1
kind: Deployment
metadata:
  name: factory-coordinator
spec:
  replicas: 1
  template:
    spec:
      containers:
      - name: coordinator
        command: ["factory", "coordinator", "serve"]
        ports:
        - containerPort: 8080  # HTTP API + SSE
        - containerPort: 9090  # gRPC for workers
        volumeMounts:
        - name: state
          mountPath: /root/.factory
      volumes:
      - name: state
        persistentVolumeClaim:
          claimName: factory-state
```

---

## Phase 3: Remote Workers

### Worker lifecycle

```
1. Start, read FACTORY_COORDINATOR_URL
2. Open persistent gRPC stream to coordinator
3. Send WorkerHello (capabilities, platform)
4. Wait for JobAssignment
5. On job received:
   a. Clone repo (or fetch if cached)
   b. Checkout branch
   c. Run Claude Code subprocess, stream stdout → coordinator
   d. Run checks (lint, test, etc.)
   e. Send StageComplete with result
6. Discard working directory, ready for next job
```

### gRPC protocol

```protobuf
service Factory {
  rpc Connect(stream WorkerEnvelope) returns (stream CoordinatorEnvelope);
}

// Worker → Coordinator
message WorkerEnvelope {
  oneof payload {
    WorkerHello   hello  = 1;  // "I'm worker-abc, ready"
    LogLine       log    = 2;  // raw output line from Claude
    CheckOutput   check  = 3;  // check result
    StageComplete done   = 4;  // stage finished, success/fail
    Heartbeat     ping   = 5;
  }
}

// Coordinator → Worker
message CoordinatorEnvelope {
  oneof payload {
    JobAssignment job    = 1;  // fully rendered: prompt, repo, branch
    SteerMessage  steer  = 2;  // "wrap up" mid-session
    CancelJob     cancel = 3;
    Heartbeat     pong   = 4;
  }
}

message JobAssignment {
  string job_id      = 1;
  int64  issue       = 2;
  string repo_url    = 3;  // https://github.com/org/repo
  string branch      = 4;  // feature/issue-42
  string stage_id    = 5;
  string prompt      = 6;  // fully rendered by coordinator
  repeated Check checks = 7;
  string claude_flags = 8; // e.g. --dangerously-skip-permissions
}
```

### Session abstraction

Workers in containers don't have tmux. A `Runner` interface abstracts execution:

```go
type Runner interface {
    Start(ctx context.Context, job Job) (<-chan string, error)
    Steer(msg string) error
    Wait() (RunResult, error)
}

type TmuxRunner struct{}    // local embedded mode: current behavior
type ProcessRunner struct{} // containerized workers: exec subprocess, capture stdout
```

Coordinator uses `TmuxRunner` in embedded mode. Remote workers use `ProcessRunner`.

### Live session streaming

```
Worker subprocess stdout
  → ProcessRunner captures line
  → gRPC LogLine message → coordinator
  → coordinator writes to in-memory ring buffer (per job)
  → coordinator SSE endpoint fans out to browser clients
```

Browser experience is identical to today. The only change is the log source moves from local tmux scrollback to the coordinator's gRPC-fed buffer.

### k8s worker deployment

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: factory-worker
spec:
  replicas: 3           # scale based on queue depth via HPA
  template:
    spec:
      containers:
      - name: worker
        command: ["factory", "worker", "start"]
        env:
        - name: FACTORY_COORDINATOR_URL
          value: "factory-coordinator:9090"
        - name: CLAUDE_CODE_OAUTH_TOKEN
          valueFrom:
            secretKeyRef:
              name: factory-secrets
              key: claude-token
        - name: GH_TOKEN
          valueFrom:
            secretKeyRef:
              name: factory-secrets
              key: gh-token
        resources:
          requests: { cpu: "2", memory: "4Gi" }
          limits:   { cpu: "4", memory: "8Gi" }
```

Workers need no inbound ports and no PVC — they are fully stateless.

---

## State Evolution

| Phase | State storage |
|-------|--------------|
| Phase 1 | SQLite + JSON files, local (`~/.factory/`) |
| Phase 2 | SQLite + JSON files, on coordinator PVC |
| Future | Postgres (if multi-coordinator or HA needed) |

The state layer is abstracted behind `pipeline.Store` and `db.DB` interfaces. Swapping SQLite for Postgres in Phase 2→future is a storage implementation change, not an API change.

---

## What the Previous Design Got Right

The `2026-02-21-remote-workers-design.md` design (kubectl exec + push dispatch) remains valid for early MVP: it requires no new server code and proves the workflow. This design is the natural next step once multi-project queue management and a proper web UI make the kubectl-exec model too manual.

The Docker image, k8s secret structure, and resource sizing from that design carry forward unchanged.

---

## Out of Scope

- Multi-coordinator / horizontal coordinator scaling (requires Postgres + leader election)
- Worker auto-scaling based on queue depth (HPA config is straightforward once Phase 3 ships)
- Worker authentication / mTLS (assume trusted network for now)
- Homebrew / package distribution
