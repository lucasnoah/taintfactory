# ADR 0014: Use CREATE ROLE with error catching for idempotent provisioning
## Status
Accepted
## Date
2026-03-06
## Context
The initial design specified `CREATE ROLE IF NOT EXISTS` for idempotent database provisioning. This syntax does not exist in any PostgreSQL version — only `CREATE DATABASE IF NOT EXISTS` is valid (PG 9.x+). Local testing against PG 16 confirmed the syntax error (SQLSTATE 42601).
## Decision
Use plain `CREATE ROLE` and catch PG error code 42710 (duplicate_object) in Go, alongside the existing 42P04 (duplicate_database) catch. Both are treated as success for idempotency.
## Consequences
Provisioning is truly idempotent across all PostgreSQL versions. The error-catching approach is simpler than PL/pgSQL `DO $$ BEGIN ... EXCEPTION` blocks and keeps all logic in Go.
