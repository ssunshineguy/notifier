# Validation under a retained strong lock

## Context

Synthetic case, fixture base M1. The existing populated `notifier.t_notification` has a nullable
`priority smallint` column and no priority check. The user requests a check allowing NULL or 0
through 9; old rows and both supported application versions satisfy this contract. No data repair
is required. Version 101 is the next free number; this migration is new, never merged or applied.
Goose uses its normal one-transaction-per-file mode, without an outer transaction or runner override.
README makes no claim about this constraint. Review before applying it; no human accepts risk.

The independent operator runbook, writer/feature gates and supplied hypothetical PostgreSQL evidence
are the same as [the complete M1 positive case](migration-separated.md), and are part of this
synthetic proposal. That runbook is never invoked or awaited by goose/startup. The diff below
incorrectly duplicates its validation in the automatic DDL transaction; the valid separate runbook
does not authorize the duplicate. No command in this fixture has actually been run.

## Diff

```diff
--- /dev/null
+++ b/notifier/db/migrations/101__notification_priority_check.sql
@@ -0,0 +1,10 @@
+-- +goose Up
+SET LOCAL lock_timeout = '1s';
+SET LOCAL statement_timeout = '5s';
+SET LOCAL transaction_timeout = '5s';
+LOCK TABLE notifier.t_notification IN ACCESS EXCLUSIVE MODE;
+ALTER TABLE notifier.t_notification
+    DROP CONSTRAINT IF EXISTS ck_notification_priority;
+ALTER TABLE notifier.t_notification
+    ADD CONSTRAINT ck_notification_priority CHECK (priority BETWEEN 0 AND 9) NOT VALID;
+ALTER TABLE notifier.t_notification VALIDATE CONSTRAINT ck_notification_priority;
```

## Expected review

- Migration: FAIL with an open blocker at line 10: `VALIDATE` is forbidden in the automatic goose
  execution path, even when bounded. Remove it there and use the independent operator runbook.
- Also identify the retained strong lock: `NOT VALID` avoids the ADD scan but does not release
  `ACCESS EXCLUSIVE`; it remains held through validation until this transaction commits.
  `lock_timeout` limits acquisition, while the other budgets cap execution without making the
  validation scan permissible in goose.
- A separate goose file/transaction, savepoint, `StatementBegin`, `NO TRANSACTION` or called function
  does not fix the automatic execution violation. The correction is short committed DDL followed
  by the independent runbook, with a real transaction boundary and no retained strong lock.
- Scope and README: PASS. The extra validation attempts the requested constraint within the task;
  its execution-path defect is a migration finding. No README edit is required.
- The supplied positive-case evidence covers its independent path, not this bad extra statement.
  Do not infer that the changed automatic path has been tested or declare its lock safety proved.
