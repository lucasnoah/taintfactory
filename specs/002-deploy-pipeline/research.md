# Research: Deploy Pipeline

**Feature**: 002-deploy-pipeline | **Date**: 2026-03-06

## R1: Deploy State Storage Pattern

**Decision**: Use the same on-disk JSON pattern as the existing pipeline store, with a separate `DeployStore` at `~/.factory/deploys/{sha}/deploy.json`.

**Rationale**: The existing `pipeline.Store` uses `WriteJSON`/`ReadJSON` helpers from `internal/pipeline/fileutil.go` and organizes state by issue number under `~/.factory/pipelines/{issue}/`. Deploy pipelines are keyed by commit SHA instead of issue number, so a parallel store with the same patterns is the simplest approach. The `WriteJSON` and `ReadJSON` helpers (including `WriteAtomic`) are already available in the `pipeline` package.

**Alternatives considered**:
- Database-only storage: Rejected because Constitution Principle I (Crash-Safe Pipeline State) requires on-disk JSON as source of truth. DB is supplementary for querying.
- Extending the existing `pipeline.Store` with a deploy mode: Rejected because deploys are keyed by SHA not issue number, and mixing the two would complicate the existing store interface.

## R2: Orchestrator Integration Point

**Decision**: Add deploy check-in as a separate method `checkInDeploy()` called within `CheckIn()`, after pipeline/queue processing and before triage.

**Rationale**: The existing `CheckIn()` flow (orchestrator.go:770-828) processes: (1) active pipelines, (2) queue, (3) triage, (4) label polling. Deploys are independent from issue pipelines and should be checked in the same loop. Placing deploy advancement after pipelines and queue but before triage respects sequential processing (Constitution III) while keeping deploys from blocking issue work.

**Alternatives considered**:
- Running deploys through the existing queue system: Rejected because deploys aren't tied to GitHub issues and the queue expects issue numbers.
- Separate goroutine for deploy advancement: Rejected because Constitution III mandates sequential processing.

## R3: Stage Engine Reuse vs. Dedicated Deploy Handler

**Decision**: ~~Reuse the existing `stage.Engine.Run()`.~~ **Revised (ADR 0015):** Use a dedicated `runDeployStage()` method in the orchestrator.

**Rationale**: Adversarial review revealed that `Engine.Run()` is deeply coupled to `PipelineState` — it calls `e.store.Get(opts.Issue)`, builds prompts via `appctx.Builder` (which takes `*PipelineState`), uses `ps.Worktree` throughout, and creates session names as `{issue}-{stage}-{attempt}`. Deploys have `DeployState` keyed by SHA, not issue number. Reuse would require either a major engine refactor or constructing synthetic `PipelineState` objects — both worse than a dedicated runner.

The deploy runner handles: session creation (with `deploy-{sha}-{stage}-{attempt}` naming), prompt rendering from `DeployState` fields, wait-idle, and cleanup. It duplicates some session lifecycle code but is self-contained and avoids coupling deploy evolution to the pipeline engine.

**Alternatives considered**:
- Reuse `stage.Engine.Run()` with a `Vars` field extension: Rejected — coupling to `PipelineState`, `pipeline.Store`, and `appctx.Builder` is too deep. Would require passing a synthetic PipelineState or major engine refactoring.
- A new `DeployEngine` struct: Rejected per Constitution VIII (Simplicity) — a method on the orchestrator is simpler than a new struct.

## R4: Deploy-Specific Template Variables

**Decision**: The dedicated deploy runner builds template variables directly from `DeployState` fields and renders prompts using `prompt.LoadTemplate` + `prompt.Render`, bypassing `appctx.Builder` entirely.

**Rationale**: Since R3 was revised to use a dedicated deploy runner (ADR 0015), the runner constructs its own vars map (`CommitSHA`, `PreviousSHA`, `Namespace`, `RepoDir`, plus any stage-level `Vars` from config). It loads and renders templates via the same `prompt` package the engine uses, but without going through `appctx.Builder` (which is coupled to `PipelineState`). Template lookup uses the repo directory instead of a worktree path.

**Alternatives considered**:
- Adding `Vars` to `stage.RunOpts` and routing through `appctx.Builder`: Rejected — builder is coupled to `PipelineState` (R3 revision).
- Creating a synthetic `PipelineState` for deploys: Rejected — misuses the abstraction.

## R5: DB Table Design -- State Table vs. Event Log

**Decision**: Create a `deploys` state table with UPDATE operations for status tracking, plus a dedicated `deploy_events` table for append-only lifecycle logging.

**Rationale**: Constitution Principle V (Append-Only Event Log) applies to event log tables. The `deploys` table is a state table (UPDATE for status changes), following the `issue_queue` precedent. Deploy lifecycle events go to a separate `deploy_events` table (not `pipeline_events`) because `pipeline_events.issue` is `INTEGER NOT NULL` and deploys are keyed by SHA. See ADR 0016.

**Alternatives considered**:
- Logging to existing `pipeline_events` with a new `commit_sha` column: Rejected — would require making `issue` nullable, migrating existing data, and auditing all queries.
- Using `issue=0` as sentinel: Rejected — makes queries ugly and error-prone.
- Pure append-only deploys table with current state derived from event log: Rejected per Constitution VIII (Simplicity).

## R6: Failure Routing Loop Prevention

**Decision**: ~~Self-reference check with 2-hop limit.~~ **Revised (ADR 0017):** Use visited-set cycle detection at runtime, complemented by config-time validation.

**Rationale**: The original approach (self-reference check) was too narrow and conflicted with the config contract's example, which has valid 3-hop chains (smoke-test → debug → rollback → failed). A visited-set is equally simple: `DeployState.FailureVisited` tracks stage IDs entered via `on_fail`. Before routing, check if the target is in the set. If yes, mark failed. Any acyclic chain is valid. Config-time validation additionally warns about obvious cycles at load time.

**Alternatives considered**:
- Hard 2-hop limit: Rejected — conflicts with reasonable configurations and is arbitrary.
- Self-reference check only: Rejected — doesn't catch mutual cycles (A → B → A).
- Config-time validation alone: Insufficient — doesn't prevent runtime loops from unexpected states. Used as complement, not replacement.
