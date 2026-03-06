# ADR 0011: K8s Deployment Architecture

## Status
Accepted

## Date
2026-03-05

## Context
Factory needs to run 24/7 without a developer laptop and expose the dashboard to the team. The user has a personal k8s cluster on DigitalOcean with ingress and PVC support.

## Decision
Deploy as a single StatefulSet with a Docker-in-Docker sidecar for build containers, replace SQLite with a single-replica PostgreSQL StatefulSet, and expose the web UI via Ingress with basic-auth. Use `FACTORY_DATA_DIR` env var and `--with-orchestrator` serve flag to run everything in one process.

## Consequences
Simpler than multi-pod but single point of failure (acceptable for single-instance workload). DinD sidecar requires privileged mode. Dropping SQLite for Postgres removes CGO dependency and enables future multi-pod evolution.
