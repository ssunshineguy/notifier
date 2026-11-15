# A marker without a reason

## Context

Synthetic case, fixture base D1. The task author requests explaining the lease-token comparison,
The row token changes on a new reservation;
`claim` is this worker's token, and a mismatch returns before any write. No prior AI rationale was given and there is no human acceptance.
README already describes lease ownership and needs no update. The full diff follows.

## Diff

```diff
--- a/notifier/internal/storage/postgres/lease_example.go
+++ b/notifier/internal/storage/postgres/lease_example.go
@@ -10,3 +10,4 @@
 func finish(row *state, claim string, result string) error {
+    // DND: Do not change.
     if row.lease != claim {
         return errLeaseLost
```

## Expected review

- Comment guard: FAIL with an open **warn** at line 11: the tag gives no concrete reason and the
  AI did not explain what mistake it prevents. Supply that explanation and a useful comment,
  then recheck; there is no human acceptance to reuse.
- Scope and README: PASS; the attempt is within the requested comment scope and README needs no
  change. DND is not a correctness certificate and does not excuse a useless comment.
