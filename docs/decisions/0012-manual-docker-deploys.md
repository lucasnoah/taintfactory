# ADR 0012: Manual Docker Builds Instead of GitHub Actions CI

## Status
Accepted

## Date
2026-03-05

## Context
The GitHub Actions deploy workflow required `DIGITALOCEAN_ACCESS_TOKEN` and `DOCR_REGISTRY` secrets configured in the repo. These were not set up, causing CI failures on every push to main. The deploy target (DigitalOcean k8s) also requires amd64 images built via `docker buildx` from an ARM Mac.

## Decision
Remove the GitHub Actions workflow and deploy manually using local `docker buildx build --platform linux/amd64 --push` followed by `kubectl delete pod` to roll the new image. This keeps the deploy process simple and avoids managing CI secrets.

## Consequences
Deploys require a developer machine with `docker`, `doctl`, and `kubectl` configured. No automated deploys on push — this is acceptable for the current single-operator setup but would need revisiting if the team grows.
