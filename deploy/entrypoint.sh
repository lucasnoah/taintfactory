#!/bin/bash
set -euo pipefail

DATA_DIR="${FACTORY_DATA_DIR:-/data}"

# Configure git credentials if GITHUB_TOKEN is set
if [ -n "${GITHUB_TOKEN:-}" ]; then
  git config --global credential.helper '!f() { echo "username=x-access-token"; echo "password=${GITHUB_TOKEN}"; }; f'
  git config --global user.email "factory@taintfactory"
  git config --global user.name "TaintFactory"
fi

# Write env vars to .bashrc so tmux sessions inherit them.
# tmux sessions inherit the tmux SERVER's env (not the calling process's),
# so we write key vars to .bashrc which every new shell sources.
{
  echo "# Factory env vars (written by entrypoint)"
  [ -n "${CLAUDE_CODE_OAUTH_TOKEN:-}" ] && echo "export CLAUDE_CODE_OAUTH_TOKEN='${CLAUDE_CODE_OAUTH_TOKEN}'"
  [ -n "${GITHUB_TOKEN:-}" ] && echo "export GITHUB_TOKEN='${GITHUB_TOKEN}'"
  [ -n "${DOCKER_HOST:-}" ] && echo "export DOCKER_HOST='${DOCKER_HOST}'"
} > "$HOME/.bashrc"

# Persist Claude's config on the PVC so interactive auth survives pod restarts.
# After first deploy, run `claude setup-token` in the pod to complete OAuth.
mkdir -p "$DATA_DIR/.claude"
if [ -e "$HOME/.claude" ] && [ ! -L "$HOME/.claude" ]; then
  # First run: move any existing config to PVC, then symlink
  cp -a "$HOME/.claude/." "$DATA_DIR/.claude/" 2>/dev/null || true
  rm -rf "$HOME/.claude"
fi
ln -sfn "$DATA_DIR/.claude" "$HOME/.claude"
# Also symlink .claude.json (Claude stores settings in home dir root)
if [ -f "$DATA_DIR/.claude.json" ]; then
  ln -sfn "$DATA_DIR/.claude.json" "$HOME/.claude.json"
elif [ -f "$HOME/.claude.json" ]; then
  mv "$HOME/.claude.json" "$DATA_DIR/.claude.json"
  ln -sfn "$DATA_DIR/.claude.json" "$HOME/.claude.json"
else
  echo '{"hasCompletedOnboarding":true,"lastOnboardingVersion":"2.1.70"}' > "$DATA_DIR/.claude.json"
  ln -sfn "$DATA_DIR/.claude.json" "$HOME/.claude.json"
fi

# Ensure data directories exist
mkdir -p "$DATA_DIR/pipelines"
mkdir -p "$DATA_DIR/triage"
mkdir -p "$DATA_DIR/repos"

# Clone/update the primary repo (required for orchestrator)
if [ -n "${FACTORY_PRIMARY_REPO:-}" ]; then
  REPO_NAME=$(basename "$FACTORY_PRIMARY_REPO" .git)
  REPO_DIR="$DATA_DIR/repos/$REPO_NAME"
  if [ ! -d "$REPO_DIR/.git" ]; then
    echo "Cloning primary repo $FACTORY_PRIMARY_REPO → $REPO_DIR"
    git clone "https://x-access-token:${GITHUB_TOKEN}@github.com/${FACTORY_PRIMARY_REPO#*github.com/}" "$REPO_DIR" 2>&1 || \
    git clone "$FACTORY_PRIMARY_REPO" "$REPO_DIR" 2>&1
  else
    echo "Primary repo already cloned, pulling latest"
    git -C "$REPO_DIR" pull --ff-only || true
  fi
  cd "$REPO_DIR"
  echo "Working directory: $(pwd)"
fi

# Clone additional repos
if [ -n "${FACTORY_REPOS:-}" ]; then
  IFS=',' read -ra REPOS <<< "$FACTORY_REPOS"
  for repo in "${REPOS[@]}"; do
    repo_name=$(basename "$repo" .git)
    repo_dir="$DATA_DIR/repos/${repo_name}"
    if [ ! -d "$repo_dir/.git" ]; then
      echo "Cloning $repo → $repo_dir"
      git clone "$repo" "$repo_dir"
    else
      echo "Repo $repo_name already cloned, pulling latest"
      git -C "$repo_dir" pull --ff-only || true
    fi
  done
fi

# Build serve command
SERVE_ARGS="--port ${FACTORY_SERVE_PORT:-17432}"

if [ "${FACTORY_WITH_ORCHESTRATOR:-false}" = "true" ]; then
  SERVE_ARGS="$SERVE_ARGS --with-orchestrator --orchestrator-interval ${ORCHESTRATOR_INTERVAL:-10}"
fi

# Start factory
exec factory serve $SERVE_ARGS
