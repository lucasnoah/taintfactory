# Implementation Issue Template

Sub-task issues that implement a slice of a feature. Created by the planning
agent when decomposing a feature issue into shippable units. Every implementation
issue MUST follow this template and carry the `implementation` GitHub label.
Do not skip sections — if a section doesn't apply, write "N/A" with a brief reason.

## Principles

- **Read the parent first.** Before writing a single line of code, read the
  parent feature issue. This issue defines what to build; the parent defines
  who it's for and why it matters.
- **Scope is narrow by design.** This issue covers one slice. If you discover
  work outside this scope during implementation, open a new issue — do not
  expand this one.
- **Acceptance criteria are developer-verifiable.** Tests passing, queries
  returning expected results, packages compiling — not "user can see X in
  browser." Browser verification belongs to the parent feature issue.
- **Scope is package-level.** Exact file names are implementation decisions.
  Package-level scope is the contract.
- **Data contracts are precise.** The planning agent has full codebase context.
  Column types, query signatures, struct fields, and API shapes should be written
  out exactly — not summarized. Precision here prevents the implementer from
  re-deriving decisions that were already made.

## Template

### Parent

**Feature:** #NNN — [title]

Read the parent before starting. It contains the user intent, user stories,
and external acceptance criteria this issue contributes to.

### Intent

What this slice accomplishes within the larger feature, and why it exists as
a separate issue. One short paragraph. Answer: what does this build, and what
becomes possible after this lands that wasn't possible before?

### Technical Scope

| Package | Action | Purpose |
|---|---|---|
| `internal/...` | Create / Modify | What it does |

### Data Contracts

Precise shapes for every data boundary this issue introduces or modifies.
Include only the subsections that apply — omit the rest.

#### Schema — `table_name`

| Column | Type | Constraints |
|---|---|---|
| `column` | `TYPE` | NOT NULL / DEFAULT / etc. |

Indexes, unique keys, and foreign keys noted below the table.

#### sqlc Query Signatures

```go
FunctionName(ctx context.Context, params ParamType) (ReturnType, error)
```

#### Go Types

Domain types introduced by this issue that cross package boundaries.

```go
type TypeName struct {
    Field Type
}
```

#### HTTP Shapes

Request and response JSON for any new or modified API endpoints.

```
POST /api/v1/...
Request:  { "field": type }
Response: { "field": type }
```

### Approach

The chosen technical strategy and WHY. Not just "use a River job" but "use a
River periodic job because we need retry semantics and it integrates with the
existing job infrastructure." This preserves the planning agent's architectural
rationale so the implementer and future reviewers understand the decision, not
just the outcome.

### Acceptance Criteria

Verifiable by code inspection or running tests.

- [ ] ...
- [ ] ...

### Dependencies

- **Blocked by:** #NNN (reason), or N/A
- **Blocks:** #NNN (reason), or N/A

### Out of Scope

What this issue explicitly does NOT touch. Prevents scope creep and tells
reviewers what not to check.

---

## Example: Weather DB Layer (#NNN)

Below is a complete implementation issue following this template.

---

### Parent

**Feature:** #154 — Weather data pipeline: collect historical and forecast weather for Healdsburg

Read the parent before starting. It contains the user intent, user stories,
and external acceptance criteria this issue contributes to.

### Intent

Adds the persistence layer for the weather pipeline. Creates the `weather_daily`
table and the sqlc queries that all downstream components (API client, River job,
backfill) will use. Nothing else in the weather feature can be wired up until
this lands, because every other slice depends on these repository functions.

### Technical Scope

| Package | Action | Purpose |
|---|---|---|
| `migrations/` | Create | `weather_daily` table migration (up + down) |
| `internal/repository/queries/` | Create | Upsert, get-by-date, get-forecast-range queries |
| `internal/repository/` | Generated | sqlc regeneration for new query file |

### Data Contracts

#### Schema — `weather_daily`

| Column | Type | Constraints |
|---|---|---|
| `date` | `DATE` | NOT NULL |
| `is_forecast` | `BOOLEAN` | NOT NULL |
| `high_temp_f` | `REAL` | NOT NULL |
| `low_temp_f` | `REAL` | NOT NULL |
| `precip_in` | `REAL` | NOT NULL |
| `conditions` | `TEXT` | NOT NULL |
| `fetched_at` | `TIMESTAMPTZ` | NOT NULL DEFAULT now() |

Unique key: `(date, is_forecast)`

#### sqlc Query Signatures

```go
UpsertWeatherDaily(ctx context.Context, arg UpsertWeatherDailyParams) error
GetWeatherForDate(ctx context.Context, date time.Time) (WeatherDaily, error)
GetWeatherForecastRange(ctx context.Context, start, end time.Time) ([]WeatherDaily, error)
```

### Approach

Single table with a composite unique key on `(date, is_forecast)`. Both actual
and forecast records coexist for a given date; the query layer returns actuals
over forecasts when both exist, so callers never need to think about precedence.
Upsert semantics on insert — re-running the sync for the same date is safe.
The `is_forecast` boolean is flipped to false when an actual record is stored
for a previously forecast date; the forecast record is not deleted.

### Acceptance Criteria

- [ ] Migration runs on a fresh database without error
- [ ] Migration rolls back cleanly
- [ ] sqlc generates a `WeatherDaily` type with all expected fields (date,
      is_forecast, high_temp_f, low_temp_f, precip_in, conditions, fetched_at)
- [ ] `GetWeatherForDate` returns the actual record when both actual and forecast
      exist for the same date
- [ ] Upsert on an existing record for the same (date, is_forecast) updates
      rather than errors

### Dependencies

- **Blocked by:** N/A
- **Blocks:** Open-Meteo client issue, River job issue (both depend on these
  repository functions)

### Out of Scope

- Open-Meteo HTTP client — separate issue
- River job wiring — separate issue
- Backfill command — part of the job issue
- Any UI or API endpoint for querying weather data
