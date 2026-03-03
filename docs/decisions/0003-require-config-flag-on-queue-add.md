# ADR 0003: Require --config flag on queue add

## Status
Accepted

## Date
2026-02-26

## Context
The factory uses issue number as the pipeline key. With multiple projects (deathcookies, wptl) sharing the same GitHub issue number space, omitting --config caused queue items to be processed against the wrong project's pipeline.yaml.

## Decision
Made --config a required parameter on `factory queue add`. `resolveConfigPath` now returns an error when the flag is absent rather than returning an empty string or auto-discovering.

## Consequences
Callers must always specify --config (e.g. `factory queue add 153 --config /path/to/wptl/pipeline.yaml`). This prevents silent cross-project collisions at the cost of a slightly more verbose command.
