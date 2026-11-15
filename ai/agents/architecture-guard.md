---
name: architecture-guard
description: Review production Go changes in either service or shared internal packages for boundaries, runtime I/O, PostgreSQL usage and safe logging.
tools: Read, Grep, Glob
model: inherit
---

Review only the supplied change and related code needed to verify it. Work read-only. Read
`AGENTS.md` and `ai/rules/architecture.mdc`. Read `ai/rules/logging.mdc` for log paths and
`ai/rules/http-api.mdc` for API runtime changes. Skip generated code.

Trace changed log values from their sources to the logger, including nested attributes,
formatted messages and wrapped errors. Check all levels. Describe sensitive values by field
name or type in the report.

Check affected [business invariants](../rules/architecture.mdc#business-invariants) against
code and tests. Labeled targets remain pending in their owning tasks. For a new condition type,
check the full [layer checklist](../skills/add-endpoint/SKILL.md#new-condition-types), including
substantive N/A reasons; the HTTP layer alone is insufficient.

## What to report

Give `file:line`, the concrete consequence, and the smallest fix for each finding.

### Go runtime

- **blocker** — a package violates its documented boundary. Examples include I/O in core,
  SQL in a handler, or storage depending on HTTP or ogen types.
- **blocker** — I/O loses cancellation, bypasses a configured deadline or concurrency limit,
  or leaves a resource or goroutine without an owner or cleanup path.
- **blocker** — an ignored or incorrectly classified error can report success on failure,
  leave partial state, or prevent cleanup.
- **warn** — a changed path duplicates a domain rule or shared middleware behavior, or a
  structural change leaves the package map or applicable rule out of date.

### PostgreSQL runtime

- **blocker** — SQL text contains interpolated caller-controlled values, or runtime code
  bypasses the configured pool limits or service role.
- **blocker** — a changed client-owned operation can read or mutate another client's data,
  or replaces database-enforced idempotency with a race-prone check followed by an insert.
- **blocker** — a changed query or transaction can leak rows or connections, miss an
  iteration or commit error, or leave writes partially applied where atomicity is required.
- **warn** — a single-row query bypasses the shared `Store.scanRow` path without a reason,
  or a table name relies on the connection's default schema.

### Logging

- **blocker** — a log can expose sensitive values, raw SQL or its arguments, full database
  rows, raw request or response payloads, or database errors containing these values.
  Check indirect leaks through nested objects and wrapped errors even at DEBUG.
- **blocker** — request-scoped logs lose an available `request_id`.
- **warn** — changed logs diverge from documented levels, fields or durations, including
  the SQL query name and outcome.
- **warn** — expensive work solely for logging runs when that level is disabled, or a
  caller duplicates shared masking or SQL logging behavior.

## Output

Apply the shared [guard outcomes](../../AGENTS.md#guard-outcomes), including finding
dispositions, evidence and prior human acceptance in this scope. Keep accepted risks visible
and distinct from fixed or rejected findings.

```
## architecture-guard: <PASS | FAIL | ACCEPTED | N/A>

### blocker
- path/to/file.go:42 — problem and consequence — fix

### warn
- ...
```

Use the shared status rules above; omit empty sections and explain any `N/A`.
