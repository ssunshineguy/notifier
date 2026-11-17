# DLQ contract (task 04)

## Storage and cleanup

`t_notification` keeps the original notification, ownership and external-ID
reservation. Its existing lifecycle columns mark the row `dlq` and stopped;
there are no additional DLQ columns in this table.

`notifier.t_dlq` contains four columns:

- `notification_id`: primary key and foreign key to `t_notification.id`, ON DELETE CASCADE.
- `notification`: JSONB snapshot of the entire original row before the transition,
  including the damaged `data`, original schedule, lease and attempts.
- `reason`: safe stable code `invalid_stored_conditions`.
- `created_dt`: transition time, equal to the main row's `stop_dt`.

One SQL statement locks and checks the current active lease, captures the original
row, stops processing and inserts its DLQ record. Failure to insert the archive
rolls back the main-row change too. A repeated or stale transition inserts nothing
and reports an unavailable claim. The primary key prevents duplicate DLQ entries.
The original conditions in the main row are preserved; the separate snapshot also
retains lifecycle values replaced by the stop transition. Raw data and parser
errors are not exposed by GET or logged.

The owner's DELETE removes the main row and its DLQ record through the foreign key
in the same transaction. Ownership and the unique `(client_id, external_id)` remain
until deletion. Collector retains DLQ indefinitely; its ordinary-notification retention
does not apply to DLQ rows or their snapshots. No replay/requeue.

## API

GET 200 is a union of the existing ordinary Notification and a DLQ notification.
The ordinary response still requires `notification_params`. The DLQ response
requires `notification_id`, `external_id`, `notification_status: dlq`, `deadline_dt`,
`start_dt`, `stop_dt`, `dlq_reason`, and `dlq_dt`; it omits `notification_params`
and `delivery_status`. PUT on a DLQ row returns the existing stopped-row 400;
DELETE remains 200 for its owner and 404 for a foreign/missing row.
Callback EventStatus remains only `success` / `deadline_exceeded`.

## Processing and transient failures

Persisted event retries and active rows already past deadline skip condition
decoding when loaded. Once loading returns `ErrInvalidStoredConditions`, the
processor attempts DLQ without checking the deadline again, even if it elapsed
during loading. Callback exhaustion retains the selected event status and becomes
`delivery_failed`. DLQ never sends a callback or increments attempts. If the DLQ
write fails, the processor logs the error and exits without rescheduling; the
unchanged due row becomes available again after its lease expires.

`notification.technical_retry_delay_sec` is an independent positive integer setting,
with a default of 10 seconds. This deliberately modest starting interval avoids a
hot loop while allowing prompt recovery of a missing client; 30 seconds and five
minutes were not approved defaults. The setting is independent of first checks,
condition rechecks, callback backoff and deadline. A transient load/target failure
reschedules from current time and releases only its own valid claim, preserving
statuses, stop time and attempts. Unavailable DB, expired context or lost lease
leaves recovery to lease expiry. Source-value errors keep existing recheck semantics.

## Local database bootstrap

These migrations create the local development schema. Per the user's explicit
2026-11-15 instruction, all final notification constraints are declared directly
in `002__t_notification.sql`. Migration `003__notification_dlq.sql` only creates
`t_dlq`, including its primary key, checks and cascading foreign key. There is no
ALTER of the main table, NOT VALID stage or separate validation procedure.

This is an explicitly approved revision of the local migration baseline, not an
upgrade procedure for databases that already ran older 002/003/004 variants.
Goose will not rerun an applied version. Start a new empty local database to use
this baseline; no existing volume is reset or removed by this task.

Fresh installation order is 001, 002, 003. The historical split-statuses version
003 was removed by PR37; the user explicitly selected 003 for DLQ in this local
baseline. Main-row ownership and external-ID constraints remain unchanged.

The earlier test volumes `notifier_1_wave3_postgres-data` and
`notifier_1_wave3_dlq_table_postgres-data` are preserved and stopped. Verification
of this revision uses `notifier_1_wave3_simple_migrations_postgres-data` with the
same isolated Compose project `notifier_1_wave3`.

## Verification and integration handoff

The DLQ storage/API/worker design is unchanged by this migration simplification.
Checks repeated against this local baseline on 2026-11-15:

- Fresh Compose bootstrap and two `make migrate` runs: PASS; schemas regenerated.
- Combined unit and PostgreSQL integration coverage run: PASS, 93.0% statements
  across all non-generated packages. Migration integration passed in 1.877 s;
  storage integration passed in 39.243 s. All five status checks are validated at
  creation in 002; fresh and staged bootstrap plus direct DDL reruns preserve
  existing main rows and DLQ snapshots. Storage checks cover atomic snapshot/stop,
  rollback, cascade deletion, constraints, ownership and lease loss.
- Full Python regression suite: 118 passed (3.81 s) on rerun. The preceding run
  had 117 passed and one existing lifecycle clock assertion failure: the Docker
  timestamp preceded the host timestamp by 318 microseconds. This test was not
  changed; the successful rerun does not fix its timing sensitivity.
- `go test ./...`, `go test -race ./...`, `go vet ./...`, default lint,
  tagged migration lint and `make ai-check`: PASS.
- `make gen` in a temporary copy: PASS with no generated drift; `gofmt`: PASS.
- The staged `git diff --check` reports only the schema exporter's standard
  trailing blank line in the new `t_dlq.sql`. All other whitespace checks pass;
  the generated file also passes with a command-local `blank-at-eof` exclusion.
  The generator and repository configuration were not changed.
- Additional lint with all integration files reports two pre-existing findings:
  gofumpt in `worker_result_integration_test.go:45` and context-argument
  order in `notification_mutation_integration_test.go:120`. The same tagged lint
  restricted to changes against `origin/main` reports no new issues. These
  unrelated findings remain unfixed.

Existing volumes were preserved. On 2026-11-15 the user explicitly requested
publication of task 04 independently so task 10 can integrate its contract.
The live DLQ journal scenario and README integration remain outside this task's
ownership; this verification does not claim the combined 04/10/15 result.

| Owner | Required remaining work |
| --- | --- |
| 10, callback journal / e2e | Integrate the agreed 04 contract, adapt ordinary-versus-DLQ GET assertions, add the live damaged-condition scenario that observes DLQ in API/DB and proves no callback in the journal, and rerun the combined full suite. |
| 15, README API / worker | Use the final GET variant and show DLQ without callback plus independent technical rescheduling in the worker diagram. |
| 05, Collector | DLQ retention is indefinite: Collector excludes DLQ rows and their snapshots. Owner DELETE still removes the main row and its snapshot through ON DELETE CASCADE. |
| 12, configuration docs | Document `notification.technical_retry_delay_sec`: positive int32 seconds, default 10, worker only, read at startup; used on transient storage/target failure, including failure to save a result, and never capped at deadline. |

Do not copy unfinished neighboring implementations or independently merge their
branches to manufacture a combined PASS. Recheck dependent behavior once the agreed
integration is available. Replay/requeue, Collector implementation and callback
journal implementation remain outside task 04.
