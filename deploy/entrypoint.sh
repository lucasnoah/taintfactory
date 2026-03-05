#!/bin/bash
set -euo pipefail

# Ensure data directories exist
mkdir -p "${FACTORY_DATA_DIR:-/data}/pipelines"
mkdir -p "${FACTORY_DATA_DIR:-/data}/triage"

# Clone repos if not already present (configured via FACTORY_REPOS env var)
# Format: comma-separated list of git URLs
if [ -n "${FACTORY_REPOS:-}" ]; then
  IFS=',' read -ra REPOS <<< "$FACTORY_REPOS"
  for repo in "${REPOS[@]}"; do
    repo_name=$(basename "$repo" .git)
    repo_dir="${FACTORY_DATA_DIR:-/data}/repos/${repo_name}"
    if [ ! -d "$repo_dir/.git" ]; then
      echo "Cloning $repo → $repo_dir"
      git clone "$repo" "$repo_dir"
    else
      echo "Repo $repo_name already cloned, pulling latest"
      git -C "$repo_dir" pull --ff-only || true
    fi
  done
fi

# Start factory with orchestrator
exec factory serve \
  --port "${FACTORY_PORT:-17432}" \
  --with-orchestrator \
  --orchestrator-interval "${ORCHESTRATOR_INTERVAL:-120}"
