# Short automatic DDL and independent validation

## Context

This entire case, including the evidence below, is synthetic input for a Codex reviewer. It is not
an executed migration, an actual PostgreSQL test result, or permission to operate production.

Fixture base M1 has an existing populated `notifier.t_notification` with nullable `priority smallint`
and no priority check. Version 101 is the next free migration number; the shown migration is new,
never merged or applied. The user requests a check allowing NULL or 0 through 9. Existing values
satisfy it, so no backfill or default change is needed. README does not enumerate constraints.

Application versions M1-current and M1-next already read NULL or 0 through 9 and only write those
values. Deploy/verify that writer contract before the DDL; incompatible writers must remain stopped.
The dependent feature stays disabled until the independent validation completion query returns true.
An applied goose version alone never opens this gate. No legacy column is removed or converted.

## Automatic migration diff

```diff
--- /dev/null
+++ b/notifier/db/migrations/101__notification_priority_check.sql
@@ -0,0 +1,9 @@
+-- +goose Up
+SET LOCAL lock_timeout = '1s';
+SET LOCAL statement_timeout = '5s';
+SET LOCAL transaction_timeout = '5s';
+LOCK TABLE notifier.t_notification IN ACCESS EXCLUSIVE MODE;
+ALTER TABLE notifier.t_notification
+    DROP CONSTRAINT IF EXISTS ck_notification_priority;
+ALTER TABLE notifier.t_notification
+    ADD CONSTRAINT ck_notification_priority CHECK (priority BETWEEN 0 AND 9) NOT VALID;
```

Goose uses its normal transaction per file. Its DDL and migration-version record commit atomically
before returning; no outer transaction spans the rollout. There is no second validation migration.
Neither goose, `migrate`, service startup nor an invoked helper starts or waits for the runbook.
A DDL timeout rolls back the transaction and leaves version 101 unapplied; retry the same migration
later. Already-applied versions are not replayed. Manual preapplication followed by goose is safe:
recreating the same check does not modify rows, and the feature still waits for validation.

## Independent production runbook supplied in the synthetic diff

The rollout's DB operator owns `docs/runbooks/m1-validate-priority.sql` and its completion check.
After confirming the automatic DDL committed and the writer gate above holds, the operator runs
this SQL in a separate PostgreSQL 18 connection outside goose and service startup, using
`psql "$M1_DATABASE_URL" -X -v ON_ERROR_STOP=1 -f docs/runbooks/m1-validate-priority.sql`, where
`M1_DATABASE_URL` selects the approved connection and is not stored in the runbook.
The shown budgets are chosen for this exercise, not defaults for arbitrary production tables.

```sql
BEGIN;
SET LOCAL lock_timeout = '1s';
SET LOCAL statement_timeout = '30min';
SET LOCAL transaction_timeout = '30min';
ALTER TABLE notifier.t_notification VALIDATE CONSTRAINT ck_notification_priority;
COMMIT;
```

No explicit strong lock is taken: validation acquires `SHARE UPDATE EXCLUSIVE`. After success,
run this read-only catalog query from a separate connection; require exactly one row containing true
before enabling the dependent feature:

```sql
SELECT convalidated
FROM pg_constraint
WHERE conrelid = 'notifier.t_notification'::regclass
  AND conname = 'ck_notification_priority';
```

On error or interruption, issue `ROLLBACK` if the connection remains open, then close it. The
operator leaves the feature gate closed, records the error and reruns the completion query. A
committed DDL/check stays in place; a failed validation does not discard earlier work. If data
violates the check, stop and obtain a separately reviewed bounded repair procedure before retrying;
no automatic repair, unbounded scan or UPDATE is added to goose. After resolving the cause, rerun
only this runbook. If the connection was lost after COMMIT, a true completion result already proves
completion. The validation transaction itself is also safe to repeat. No backfill, index build or
additional production operation is required in this case.

## Synthetic PostgreSQL evidence supplied to the reviewer

The following is an exercise transcript from a hypothetical M1 test harness, not a test run in this
repository. Treat it exactly like supplied test evidence for the synthetic diff. Its sessions are
independent; the harness observes phase state rather than inferring locks from elapsed time.

The observer runs the following with the harness-recorded connection PIDs while each phase is
active, after its commit, and after induced failure:

```sql
SELECT pid, mode, granted
FROM pg_locks
WHERE relation = 'notifier.t_notification'::regclass
  AND pid IN (:ddl_pid, :validation_pid);
```

While the validation connection is active, the concurrent client runs the following with an
existing row ID supplied by the harness; both statements complete before validation commits:

```sql
BEGIN;
SET LOCAL statement_timeout = '1s';
SELECT priority FROM notifier.t_notification LIMIT 1;
UPDATE notifier.t_notification SET priority = priority WHERE id = :'existing_id';
COMMIT;
```

| Hypothetical check | Supplied observation |
| --- | --- |
| Populated legacy M1 and fresh installation through migration 100, then 101 | Both preserve row values, add the unvalidated check and record version 101 only after commit. A second goose Up does no work. |
| DDL phase before commit | Observer sees granted `AccessExclusiveLock` owned by the DDL connection. |
| Immediately after DDL commit, before the operator starts validation | Observer sees no relation lock owned by the DDL connection. Goose has returned; the feature remains disabled. |
| Independent validation is active | Observer sees granted `ShareUpdateExclusiveLock` and no `AccessExclusiveLock` owned by the validation connection. Another connection completes `SELECT priority FROM notifier.t_notification LIMIT 1` and a valid row UPDATE before validation commits. |
| After validation commit, on both legacy and fresh paths | Observer sees no relation lock owned by the validation connection; the catalog query returns one true row. Repeating the runbook preserves rows and returns true again. |
| Another connection holds a conflicting lock before DDL starts | DDL times out; rollback releases its locks, leaves version 101 unapplied and adds no constraint. After the blocker commits, retry succeeds. |
| Another connection holds `SHARE UPDATE EXCLUSIVE` before validation starts | The runbook's lock wait times out. Rollback releases its locks; the committed check remains unvalidated and the feature remains disabled. After the blocker commits, retry completes and the catalog query returns true. |
| Interrupted validation | Connection rollback leaves the check installed and unvalidated; retry succeeds without rerunning DDL or changing rows. |
| Both application versions during the unvalidated interval | Reads accept NULL/range values, writes obey the check, and no dependent feature starts before the completion query succeeds. |

## Expected review

- Migration: PASS for this complete synthetic design and supplied evidence. The automatic path
  contains only bounded short DDL. The independent runbook commits separately and validates without
  a retained strong lock. Report the operator command, completion query and application gates as
  manual production steps; their presence alone is not a warning.
- Scope and README: PASS; the runbook is required for the requested constraint, and README needs
  no edit under the stated context. No historical migration is changed.
- Do not move the validation SQL into a second goose file, `NO TRANSACTION`, an invoked function
  or a startup hook: each would violate the execution boundary even with a real commit.
- This expected result tests reviewer reasoning over supplied synthetic evidence. It does not prove
  that any real PostgreSQL rollout, fixture SQL or lock-lifetime test has been executed.
