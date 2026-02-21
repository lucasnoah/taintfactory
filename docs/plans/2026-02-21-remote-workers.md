# Remote Worker Infrastructure Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add `factory worker` subcommands that let the operator dispatch work to, monitor, and attach to persistent k8s worker pods running the factory orchestrator remotely.

**Architecture:** Each worker pod is a persistent k8s Deployment (privileged, for DinD) running the existing factory binary, tmux, docker, and gh. The operator's laptop controls workers exclusively via `kubectl exec` — no new services or ingress required. Worker metadata lives in `~/.factory/workers.yaml` on the operator's machine.

**Tech Stack:** Go + Cobra (existing), `gopkg.in/yaml.v3` (already in go.mod), `kubectl` (shelled out), Docker (DinD in pod), GitHub CLI, k8s Deployments + PersistentVolumeClaims + Secrets.

---

## Task 1: Worker config types and loader

**Files:**
- Create: `internal/worker/types.go`
- Create: `internal/worker/config.go`
- Create: `internal/worker/config_test.go`

### Step 1: Write the failing tests

```go
// internal/worker/config_test.go
package worker_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lucasnoah/taintfactory/internal/worker"
)

func TestLoad_EmptyWhenMissing(t *testing.T) {
	cfg, err := worker.Load("/nonexistent/workers.yaml")
	if err != nil {
		t.Fatalf("expected no error for missing file, got: %v", err)
	}
	if len(cfg.Workers) != 0 {
		t.Errorf("expected 0 workers, got %d", len(cfg.Workers))
	}
}

func TestLoad_ParsesWorkers(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "workers.yaml")
	content := `workers:
  - name: worker-1
    pod: factory-worker-1
    namespace: factory
    context: do-nyc1-factory
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := worker.Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Workers) != 1 {
		t.Fatalf("expected 1 worker, got %d", len(cfg.Workers))
	}
	w := cfg.Workers[0]
	if w.Name != "worker-1" {
		t.Errorf("name: got %q, want %q", w.Name, "worker-1")
	}
	if w.Pod != "factory-worker-1" {
		t.Errorf("pod: got %q, want %q", w.Pod, "factory-worker-1")
	}
	if w.Namespace != "factory" {
		t.Errorf("namespace: got %q, want %q", w.Namespace, "factory")
	}
	if w.Context != "do-nyc1-factory" {
		t.Errorf("context: got %q, want %q", w.Context, "do-nyc1-factory")
	}
}

func TestFind_ReturnsWorker(t *testing.T) {
	cfg := &worker.Config{
		Workers: []worker.Worker{
			{Name: "worker-1", Pod: "factory-worker-1", Namespace: "factory"},
		},
	}
	w, err := cfg.Find("worker-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if w.Name != "worker-1" {
		t.Errorf("got %q, want %q", w.Name, "worker-1")
	}
}

func TestFind_ErrorOnMissing(t *testing.T) {
	cfg := &worker.Config{}
	_, err := cfg.Find("nonexistent")
	if err == nil {
		t.Error("expected error for missing worker, got nil")
	}
}
```

### Step 2: Run tests to confirm they fail

```
go test ./internal/worker/... -v
```

Expected: FAIL — package does not exist yet.

### Step 3: Write the types

```go
// internal/worker/types.go
package worker

// Worker represents a remote factory worker pod.
type Worker struct {
	Name      string `yaml:"name"`
	Pod       string `yaml:"pod"`
	Namespace string `yaml:"namespace"`
	Context   string `yaml:"context"` // kubectl context name; empty uses current context
}

// Config holds the list of registered workers.
type Config struct {
	Workers []Worker `yaml:"workers"`
}

// Find returns the worker with the given name, or an error if not found.
func (c *Config) Find(name string) (*Worker, error) {
	for i := range c.Workers {
		if c.Workers[i].Name == name {
			return &c.Workers[i], nil
		}
	}
	return nil, fmt.Errorf("worker %q not found in ~/.factory/workers.yaml", name)
}
```

Add the missing import — the file needs `"fmt"`. Add it to the import block.

### Step 4: Write the config loader

```go
// internal/worker/config.go
package worker

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// DefaultConfigPath returns ~/.factory/workers.yaml.
func DefaultConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	return filepath.Join(home, ".factory", "workers.yaml"), nil
}

// Load reads a workers config from path.
// Returns an empty config if the file does not exist.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &Config{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read workers config: %w", err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse workers config: %w", err)
	}
	return &cfg, nil
}
```

Note: move the `Find` method and its `fmt` import to `types.go`, or keep it in `config.go` — just be consistent. Keeping `Find` in `types.go` is cleaner; put the `fmt` import there.

### Step 5: Run tests to confirm they pass

```
go test ./internal/worker/... -v
```

Expected: PASS all 4 tests.

### Step 6: Commit

```bash
git add internal/worker/types.go internal/worker/config.go internal/worker/config_test.go
git commit -m "feat(worker): add worker config types and loader"
```

---

## Task 2: KubectlRunner — exec commands on a remote pod

**Files:**
- Create: `internal/worker/kubectl.go`
- Create: `internal/worker/kubectl_test.go`

### Step 1: Write the failing tests

The runner uses a `CmdRunner` interface so tests can inject a fake without shelling out to `kubectl`.

```go
// internal/worker/kubectl_test.go
package worker_test

import (
	"testing"

	"github.com/lucasnoah/taintfactory/internal/worker"
)

// fakeCmd records calls and returns configured output/err.
type fakeCmd struct {
	calls  [][]string
	output string
	err    error
}

func (f *fakeCmd) Run(args ...string) (string, error) {
	f.calls = append(f.calls, args)
	return f.output, f.err
}

func TestKubectlRunner_RunBuildsArgs(t *testing.T) {
	fake := &fakeCmd{output: "ok\n"}
	w := &worker.Worker{
		Name:      "worker-1",
		Pod:       "factory-worker-1",
		Namespace: "factory",
		Context:   "do-nyc1-factory",
	}
	runner := worker.NewKubectlRunnerWithCmd(w, fake)
	out, err := runner.Run("factory", "status")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "ok\n" {
		t.Errorf("got %q, want %q", out, "ok\n")
	}
	if len(fake.calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(fake.calls))
	}
	args := fake.calls[0]
	// Expect: exec --context do-nyc1-factory -n factory factory-worker-1 -- factory status
	wantArgs := []string{"exec", "--context", "do-nyc1-factory", "-n", "factory", "factory-worker-1", "--", "factory", "status"}
	if len(args) != len(wantArgs) {
		t.Fatalf("args length: got %d, want %d\ngot: %v\nwant: %v", len(args), len(wantArgs), args, wantArgs)
	}
	for i, a := range args {
		if a != wantArgs[i] {
			t.Errorf("arg[%d]: got %q, want %q", i, a, wantArgs[i])
		}
	}
}

func TestKubectlRunner_SessionsParsesOutput(t *testing.T) {
	fake := &fakeCmd{output: "orchestrator\ndev\n"}
	w := &worker.Worker{Pod: "factory-worker-1", Namespace: "factory"}
	runner := worker.NewKubectlRunnerWithCmd(w, fake)
	sessions, err := runner.Sessions()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(sessions) != 2 {
		t.Fatalf("expected 2 sessions, got %d: %v", len(sessions), sessions)
	}
	if sessions[0] != "orchestrator" || sessions[1] != "dev" {
		t.Errorf("got %v, want [orchestrator dev]", sessions)
	}
}

func TestKubectlRunner_SessionsEmptyWhenNoServer(t *testing.T) {
	fake := &fakeCmd{output: "no server running on /tmp/tmux", err: fmt.Errorf("exit status 1")}
	w := &worker.Worker{Pod: "factory-worker-1", Namespace: "factory"}
	runner := worker.NewKubectlRunnerWithCmd(w, fake)
	sessions, err := runner.Sessions()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(sessions) != 0 {
		t.Errorf("expected 0 sessions, got %v", sessions)
	}
}
```

Note: `kubectl_test.go` needs `"fmt"` imported for `fmt.Errorf`.

### Step 2: Run tests to confirm they fail

```
go test ./internal/worker/... -run TestKubectl -v
```

Expected: FAIL — `NewKubectlRunnerWithCmd` does not exist.

### Step 3: Write the runner

```go
// internal/worker/kubectl.go
package worker

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
)

// CmdRunner abstracts shelling out to kubectl for testability.
type CmdRunner interface {
	Run(args ...string) (string, error)
}

// ExecCmdRunner shells out to kubectl.
type ExecCmdRunner struct{}

func (e *ExecCmdRunner) Run(args ...string) (string, error) {
	cmd := exec.Command("kubectl", args...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return out.String(), fmt.Errorf("kubectl: %w\n%s", err, out.String())
	}
	return out.String(), nil
}

// KubectlRunner executes commands on a remote pod via kubectl exec.
type KubectlRunner struct {
	w   *Worker
	cmd CmdRunner
}

// NewKubectlRunner creates a runner using the real kubectl binary.
func NewKubectlRunner(w *Worker) *KubectlRunner {
	return &KubectlRunner{w: w, cmd: &ExecCmdRunner{}}
}

// NewKubectlRunnerWithCmd creates a runner with an injected CmdRunner (for tests).
func NewKubectlRunnerWithCmd(w *Worker, cmd CmdRunner) *KubectlRunner {
	return &KubectlRunner{w: w, cmd: cmd}
}

// Run executes a command on the pod and returns combined stdout+stderr.
func (r *KubectlRunner) Run(args ...string) (string, error) {
	kubectlArgs := r.baseArgs()
	kubectlArgs = append(kubectlArgs, "--")
	kubectlArgs = append(kubectlArgs, args...)
	return r.cmd.Run(kubectlArgs...)
}

// Sessions returns the list of tmux session names on the pod.
// Returns nil (not an error) when tmux has no server running.
func (r *KubectlRunner) Sessions() ([]string, error) {
	out, err := r.Run("tmux", "list-sessions", "-F", "#{session_name}")
	if err != nil {
		if strings.Contains(out, "no server running") || strings.Contains(err.Error(), "exit status 1") {
			return nil, nil
		}
		return nil, err
	}
	raw := strings.TrimSpace(out)
	if raw == "" {
		return nil, nil
	}
	return strings.Split(raw, "\n"), nil
}

// AttachArgs returns the full kubectl exec argument list for an interactive tmux attach.
// The caller is responsible for exec.Command("kubectl", AttachArgs()...) with Stdin/Stdout/Stderr set.
func (r *KubectlRunner) AttachArgs(session string) []string {
	args := []string{"exec", "-it"}
	if r.w.Context != "" {
		args = append(args, "--context", r.w.Context)
	}
	args = append(args, "-n", r.w.Namespace, r.w.Pod, "--", "tmux", "attach", "-t", session)
	return args
}

// baseArgs returns the kubectl exec preamble (without the command or "--").
func (r *KubectlRunner) baseArgs() []string {
	args := []string{"exec"}
	if r.w.Context != "" {
		args = append(args, "--context", r.w.Context)
	}
	args = append(args, "-n", r.w.Namespace, r.w.Pod)
	return args
}
```

### Step 4: Run tests to confirm they pass

```
go test ./internal/worker/... -v
```

Expected: PASS all tests.

### Step 5: Commit

```bash
git add internal/worker/kubectl.go internal/worker/kubectl_test.go
git commit -m "feat(worker): add KubectlRunner for remote pod exec"
```

---

## Task 3: `factory worker` CLI commands

**Files:**
- Create: `internal/cli/worker.go`
- Modify: `internal/cli/root.go`

### Step 1: Write the implementation

There is no meaningful unit test for the CLI layer here (it shells out to kubectl and uses os.Stdin/Stdout for attach) — integration is tested manually. This follows the pattern of existing CLI files like `queue.go` and `session.go`.

```go
// internal/cli/worker.go
package cli

import (
	"fmt"
	"os"
	"os/exec"
	"text/tabwriter"

	"github.com/lucasnoah/taintfactory/internal/worker"
	"github.com/spf13/cobra"
)

var workerCmd = &cobra.Command{
	Use:   "worker",
	Short: "Manage remote worker pods",
}

var workerListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all registered workers and their tmux sessions",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadWorkerConfig()
		if err != nil {
			return err
		}
		if len(cfg.Workers) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "No workers configured. Add entries to ~/.factory/workers.yaml")
			return nil
		}
		w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "NAME\tPOD\tNAMESPACE\tSESSIONS")
		for _, wk := range cfg.Workers {
			runner := worker.NewKubectlRunner(&wk)
			sessions, err := runner.Sessions()
			sessionStr := joinSessions(sessions)
			if err != nil {
				sessionStr = "(unreachable)"
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", wk.Name, wk.Pod, wk.Namespace, sessionStr)
		}
		return w.Flush()
	},
}

var workerStatusCmd = &cobra.Command{
	Use:   "status <worker>",
	Short: "Show pipeline and queue status for a worker",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		runner, err := resolveWorker(args[0])
		if err != nil {
			return err
		}
		out, err := runner.Run("factory", "status")
		fmt.Print(out)
		return err
	},
}

var workerDispatchCmd = &cobra.Command{
	Use:   "dispatch <worker>",
	Short: "Push an issue to a worker's queue",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		project, _ := cmd.Flags().GetString("project")
		issue, _ := cmd.Flags().GetInt("issue")
		intent, _ := cmd.Flags().GetString("intent")

		if project == "" {
			return fmt.Errorf("--project is required")
		}
		if issue <= 0 {
			return fmt.Errorf("--issue must be a positive integer")
		}

		runner, err := resolveWorker(args[0])
		if err != nil {
			return err
		}

		remoteArgs := []string{"factory", "queue", "add", fmt.Sprintf("%d", issue)}
		if intent != "" {
			remoteArgs = append(remoteArgs, "--intent", intent)
		}

		out, err := runner.Run(remoteArgs...)
		fmt.Print(out)
		return err
	},
}

var workerPeekCmd = &cobra.Command{
	Use:   "peek <worker>",
	Short: "Show recent tmux output from a worker session",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		sessionName, _ := cmd.Flags().GetString("session")
		lines, _ := cmd.Flags().GetInt("lines")

		runner, err := resolveWorker(args[0])
		if err != nil {
			return err
		}

		if sessionName == "" {
			sessionName, err = pickSession(runner)
			if err != nil {
				return err
			}
		}

		out, err := runner.Run(
			"tmux", "capture-pane", "-p", "-t", sessionName,
			"-S", fmt.Sprintf("-%d", lines),
		)
		fmt.Print(out)
		return err
	},
}

var workerAttachCmd = &cobra.Command{
	Use:   "attach <worker>",
	Short: "Attach interactively to a tmux session on a worker pod",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		sessionName, _ := cmd.Flags().GetString("session")

		cfg, err := loadWorkerConfig()
		if err != nil {
			return err
		}
		wk, err := cfg.Find(args[0])
		if err != nil {
			return err
		}

		runner := worker.NewKubectlRunner(wk)

		if sessionName == "" {
			sessionName, err = pickSession(runner)
			if err != nil {
				return err
			}
		}

		kubectlArgs := runner.AttachArgs(sessionName)
		c := exec.Command("kubectl", kubectlArgs...)
		c.Stdin = os.Stdin
		c.Stdout = os.Stdout
		c.Stderr = os.Stderr
		return c.Run()
	},
}

// loadWorkerConfig loads ~/.factory/workers.yaml.
func loadWorkerConfig() (*worker.Config, error) {
	path, err := worker.DefaultConfigPath()
	if err != nil {
		return nil, err
	}
	return worker.Load(path)
}

// resolveWorker looks up a worker by name and returns a KubectlRunner.
func resolveWorker(name string) (*worker.KubectlRunner, error) {
	cfg, err := loadWorkerConfig()
	if err != nil {
		return nil, err
	}
	wk, err := cfg.Find(name)
	if err != nil {
		return nil, err
	}
	return worker.NewKubectlRunner(wk), nil
}

// pickSession lists sessions on the runner and prompts the user to choose.
// If exactly one session exists, it is returned without prompting.
func pickSession(runner *worker.KubectlRunner) (string, error) {
	sessions, err := runner.Sessions()
	if err != nil {
		return "", fmt.Errorf("list sessions: %w", err)
	}
	if len(sessions) == 0 {
		return "", fmt.Errorf("no active tmux sessions on this worker")
	}
	if len(sessions) == 1 {
		return sessions[0], nil
	}
	fmt.Println("Available sessions:")
	for i, s := range sessions {
		fmt.Printf("  %d. %s\n", i+1, s)
	}
	fmt.Print("Attach to (number): ")
	var choice int
	if _, err := fmt.Scan(&choice); err != nil || choice < 1 || choice > len(sessions) {
		return "", fmt.Errorf("invalid choice")
	}
	return sessions[choice-1], nil
}

// joinSessions formats a session list for display.
func joinSessions(sessions []string) string {
	if len(sessions) == 0 {
		return "(none)"
	}
	result := ""
	for i, s := range sessions {
		if i > 0 {
			result += ", "
		}
		result += s
	}
	return result
}

func init() {
	workerDispatchCmd.Flags().String("project", "", "GitHub repo name (required)")
	workerDispatchCmd.Flags().Int("issue", 0, "Issue number (required)")
	workerDispatchCmd.Flags().String("intent", "", "Feature intent (auto-derived if omitted)")

	workerPeekCmd.Flags().String("session", "", "Session name (prompted if omitted)")
	workerPeekCmd.Flags().Int("lines", 50, "Number of lines to capture")

	workerAttachCmd.Flags().String("session", "", "Session name (prompted if omitted)")

	workerCmd.AddCommand(workerListCmd)
	workerCmd.AddCommand(workerStatusCmd)
	workerCmd.AddCommand(workerDispatchCmd)
	workerCmd.AddCommand(workerPeekCmd)
	workerCmd.AddCommand(workerAttachCmd)
}
```

### Step 2: Register `workerCmd` in root.go

In `internal/cli/root.go`, add to the `init()` function:

```go
rootCmd.AddCommand(workerCmd)
```

Place it after `rootCmd.AddCommand(queueCmd)`.

### Step 3: Build to confirm no compile errors

```
go build ./...
```

Expected: no output (success).

### Step 4: Smoke test the commands compile correctly

```
go run ./cmd/factory worker --help
```

Expected output includes: `list`, `status`, `dispatch`, `peek`, `attach`.

### Step 5: Commit

```bash
git add internal/cli/worker.go internal/cli/root.go
git commit -m "feat(cli): add factory worker subcommands (list, status, dispatch, peek, attach)"
```

---

## Task 4: Dockerfile and entrypoint

**Files:**
- Create: `deploy/Dockerfile`
- Create: `deploy/entrypoint.sh`

### Step 1: Write the Dockerfile

```dockerfile
# deploy/Dockerfile
FROM golang:1.23-bookworm

# System tools
RUN apt-get update && apt-get install -y --no-install-recommends \
    git \
    tmux \
    curl \
    ca-certificates \
    iptables \
    && rm -rf /var/lib/apt/lists/*

# Docker (for DinD)
RUN curl -fsSL https://get.docker.com | sh

# GitHub CLI
RUN curl -fsSL https://cli.github.com/packages/githubcli-archive-keyring.gpg \
    | dd of=/usr/share/keyrings/githubcli-archive-keyring.gpg \
    && chmod go+r /usr/share/keyrings/githubcli-archive-keyring.gpg \
    && echo "deb [arch=$(dpkg --print-architecture) signed-by=/usr/share/keyrings/githubcli-archive-keyring.gpg] https://cli.github.com/packages stable main" \
    | tee /etc/apt/sources.list.d/github-cli.list > /dev/null \
    && apt-get update \
    && apt-get install -y gh \
    && rm -rf /var/lib/apt/lists/*

# factory binary (built externally and copied in)
COPY factory /usr/local/bin/factory
RUN chmod +x /usr/local/bin/factory

# Entrypoint
COPY deploy/entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

ENTRYPOINT ["/entrypoint.sh"]
```

### Step 2: Write the entrypoint

```bash
#!/usr/bin/env bash
# deploy/entrypoint.sh
set -euo pipefail

# 1. Start Docker daemon (DinD)
dockerd &
DOCKERD_PID=$!

echo "Waiting for Docker daemon..."
for i in $(seq 1 30); do
    if docker info >/dev/null 2>&1; then
        echo "Docker ready."
        break
    fi
    sleep 1
done

# 2. Authenticate GitHub CLI
if [ -n "${GH_TOKEN:-}" ]; then
    echo "$GH_TOKEN" | gh auth login --with-token
    echo "GitHub CLI authenticated as: $(gh api user --jq .login)"
fi

# 3. Configure git identity
GIT_USER="${GH_USER:-taintfactory-bot}"
git config --global user.email "${GIT_USER}@users.noreply.github.com"
git config --global user.name "${GIT_USER}"

# 4. Ensure ~/.factory exists (PVC may be empty on first boot)
mkdir -p /root/.factory

# 5. Start tmux sessions
tmux new-session -d -s orchestrator "factory orchestrator check-in; exec bash"
tmux new-session -d -s dev "exec bash"

echo "Worker ready. Sessions: orchestrator, dev"

# 6. Keep pod alive; clean up dockerd on exit
trap "kill $DOCKERD_PID 2>/dev/null || true" EXIT
wait $DOCKERD_PID
```

### Step 3: Add a .dockerignore

```
# deploy/.dockerignore
.git
docs
*.md
internal
cmd
templates
config
```

Wait — Docker build context is the repo root (factory binary is built separately). Create the `.dockerignore` at repo root instead:

```
# .dockerignore
.git
docs
*.md
internal
cmd
templates
config
go.sum
go.mod
```

### Step 4: Verify the Dockerfile syntax

```bash
docker build --no-cache -f deploy/Dockerfile . --dry-run 2>&1 || echo "dry-run not supported — review manually"
```

If dry-run is not supported, just do a visual review. Do not push yet.

### Step 5: Commit

```bash
git add deploy/Dockerfile deploy/entrypoint.sh .dockerignore
git commit -m "feat(deploy): add Dockerfile and entrypoint for DinD worker pod"
```

---

## Task 5: Kubernetes manifests

**Files:**
- Create: `deploy/k8s/namespace.yaml`
- Create: `deploy/k8s/worker-1/pvc.yaml`
- Create: `deploy/k8s/worker-1/secret.template.yaml`
- Create: `deploy/k8s/worker-1/deployment.yaml`
- Create: `deploy/k8s/README.md`

### Step 1: Namespace

```yaml
# deploy/k8s/namespace.yaml
apiVersion: v1
kind: Namespace
metadata:
  name: factory
```

### Step 2: PersistentVolumeClaim

```yaml
# deploy/k8s/worker-1/pvc.yaml
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: factory-worker-1-data
  namespace: factory
spec:
  accessModes:
    - ReadWriteOnce
  resources:
    requests:
      storage: 20Gi
```

### Step 3: Secret template (not committed with real values — fill in before applying)

```yaml
# deploy/k8s/worker-1/secret.template.yaml
# Copy to secret.yaml, fill in base64-encoded values, and apply.
# Do NOT commit secret.yaml — it is in .gitignore.
apiVersion: v1
kind: Secret
metadata:
  name: factory-worker-1-secrets
  namespace: factory
type: Opaque
stringData:
  CLAUDE_CODE_OAUTH_TOKEN: "<your-claude-oauth-token>"
  GH_TOKEN: "<taintfactory-bot-github-pat>"
  GH_USER: "taintfactory-bot"
```

### Step 4: Add `secret.yaml` to .gitignore

In `.gitignore`, add:
```
deploy/k8s/**/secret.yaml
```

### Step 5: Deployment

```yaml
# deploy/k8s/worker-1/deployment.yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: factory-worker-1
  namespace: factory
spec:
  replicas: 1
  selector:
    matchLabels:
      app: factory-worker-1
  template:
    metadata:
      labels:
        app: factory-worker-1
    spec:
      containers:
        - name: worker
          image: registry.digitalocean.com/<your-registry>/factory-worker:latest
          imagePullPolicy: Always
          securityContext:
            privileged: true  # Required for Docker-in-Docker
          envFrom:
            - secretRef:
                name: factory-worker-1-secrets
          volumeMounts:
            - name: factory-data
              mountPath: /root/.factory
          resources:
            requests:
              cpu: "2"
              memory: "4Gi"
            limits:
              cpu: "4"
              memory: "8Gi"
      volumes:
        - name: factory-data
          persistentVolumeClaim:
            claimName: factory-worker-1-data
```

### Step 6: Write the README

```markdown
# deploy/k8s/README.md

## Deploying a worker pod

### Prerequisites
- `kubectl` configured with your DO cluster context (`do-nyc1-factory`)
- DO Container Registry access
- A GitHub bot account with a PAT (`repo`, `workflow` scopes)
- A Claude Code OAuth token for the worker

### First-time setup

1. Build and push the image:
   ```bash
   make build-linux  # produces ./factory (linux/amd64 binary)
   docker build -f deploy/Dockerfile -t registry.digitalocean.com/<registry>/factory-worker:latest .
   docker push registry.digitalocean.com/<registry>/factory-worker:latest
   ```

2. Apply namespace:
   ```bash
   kubectl apply -f deploy/k8s/namespace.yaml
   ```

3. Create the secret (do not commit this file):
   ```bash
   cp deploy/k8s/worker-1/secret.template.yaml deploy/k8s/worker-1/secret.yaml
   # Edit secret.yaml with real values
   kubectl apply -f deploy/k8s/worker-1/secret.yaml
   ```

4. Apply PVC and Deployment:
   ```bash
   kubectl apply -f deploy/k8s/worker-1/pvc.yaml
   kubectl apply -f deploy/k8s/worker-1/deployment.yaml
   ```

5. Register the worker on your laptop:
   ```yaml
   # ~/.factory/workers.yaml
   workers:
     - name: worker-1
       pod: factory-worker-1
       namespace: factory
       context: do-nyc1-factory
   ```

### Updating the binary

```bash
make build-linux
docker build -f deploy/Dockerfile -t registry.digitalocean.com/<registry>/factory-worker:latest .
docker push registry.digitalocean.com/<registry>/factory-worker:latest
kubectl rollout restart deployment/factory-worker-1 -n factory
```

### Common commands

```bash
factory worker list
factory worker status worker-1
factory worker dispatch worker-1 --project myrepo --issue 42
factory worker peek worker-1
factory worker attach worker-1
```
```

### Step 7: Add a Makefile target for cross-compiling the Linux binary

In `Makefile`, add:
```makefile
build-linux:
	GOOS=linux GOARCH=amd64 go build -o factory ./cmd/factory
```

Check the existing Makefile first to match its style.

### Step 8: Commit

```bash
git add deploy/k8s/ deploy/k8s/README.md
git commit -m "feat(deploy): add k8s manifests for worker-1 pod"
```

---

## Manual Verification Checklist

After all tasks are complete, verify end-to-end on the real cluster:

1. `make build-linux` — produces `./factory` linux binary
2. `docker build -f deploy/Dockerfile .` — image builds cleanly
3. `kubectl apply -f deploy/k8s/namespace.yaml` — namespace created
4. Apply secret, PVC, deployment — pod starts, reaches Running state
5. `kubectl logs -f deployment/factory-worker-1 -n factory` — shows "Worker ready. Sessions: orchestrator, dev"
6. `factory worker list` — shows worker-1 with sessions orchestrator, dev
7. `factory worker status worker-1` — returns factory status output from the pod
8. `factory worker dispatch worker-1 --project <repo> --issue <n>` — issue appears in queue on pod
9. `factory worker peek worker-1` — shows tmux output
10. `factory worker attach worker-1` — drops into interactive tmux session
