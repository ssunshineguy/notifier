# Validation in a second goose file

## Context

This case is entirely synthetic. Base M1 has an existing populated `notifier.t_notification` with
nullable `priority smallint` and no priority check. The requested constraint allows NULL or 0
through 9; existing rows and both supported application versions already satisfy it. No backfill,
default change or new application contract is needed. Versions 101 and 102 are the next free
numbers; both files are new. README does not enumerate constraints. No human accepts risk.

Goose commits each file before starting the next; there is no outer transaction. Thus the strong
lock from 101 is genuinely released before 102 begins. The operator runbook, compatibility/gates
and supplied hypothetical evidence from [the complete M1 positive case](migration-separated.md)
are also provided unchanged. That runbook is independent of goose and startup. This diff nonetheless
duplicates validation in a second automatic migration. No real migration or test is executed here.

## Diff

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
--- /dev/null
+++ b/notifier/db/migrations/102__validate_notification_priority.sql
@@ -0,0 +1,5 @@
+-- +goose Up
+SET LOCAL lock_timeout = '1s';
+SET LOCAL statement_timeout = '30min';
+SET LOCAL transaction_timeout = '30min';
+ALTER TABLE notifier.t_notification VALIDATE CONSTRAINT ck_notification_priority;
```

## Expected review

- Migration: FAIL with an open blocker at file 102, line 5: the validation scan is in the automatic
  goose path. Real transaction separation and bounded execution do not permit it. Remove file 102;
  validation belongs only to the separately scheduled operator runbook, after committed DDL.
- Do not incorrectly claim that the `ACCESS EXCLUSIVE` lock from file 101 survives its commit or
  require another strong lock for validation. File 102 takes `SHARE UPDATE EXCLUSIVE`; the defect
  is the automatic execution path, not retained `ACCESS EXCLUSIVE`.
- Scope and README: PASS under this request. The operation serves the requested constraint but
  violates migration policy; README needs no edit. The supplied independent runbook prevents an
  unrelated missing-runbook finding but cannot make the duplicate automatic scan acceptable.
- No declared check of the positive independent path proves this automatic variant safe. This is
  a semantic reviewer probe, not an SQL keyword scanner or evidence of an actual PostgreSQL run.
