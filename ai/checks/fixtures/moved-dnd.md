# Move a DND comment without changing its meaning

## Context

Synthetic case, fixture base D2. The row token changes on each new reservation; `claim` is this worker's token. A mismatch returns before any write. The user requests
moving the existing comment to the comparison it explains. The full diff below is the entire
change; no protected code or decision changes. No separate approval was requested. README's lease
explanation is still accurate.

## Diff

```diff
--- a/notifier/internal/storage/postgres/lease_example.go
+++ b/notifier/internal/storage/postgres/lease_example.go
@@ -9,8 +9,8 @@
-// DND: Match the claimed lease so a stale worker cannot overwrite a newer reservation.
 func finish(row *state, claim string, result string) error {
+    // DND: Match the claimed lease so a stale worker cannot overwrite a newer reservation.
     if row.lease != claim {
         return errLeaseLost
     }
     row.result = result
     return nil
 }
```

## Expected review

- Selected guards: PASS. Marker, explanation, associated comparison and write are preserved.
- Inspect both the deletion and addition; do not classify the deleted line alone as unapproved
  removal. A move with unchanged protected meaning needs no new approval.
- Scope is the requested move; README needs no change. This is not authorization for moving the
  comment away from the protected code or altering the protected decision.
