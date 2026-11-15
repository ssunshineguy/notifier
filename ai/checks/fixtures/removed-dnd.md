# Unapproved DND removal

## Context

Synthetic case, fixture base D2. The user requests changing only whitespace around `finish`; there
is no permission to remove comments or alter the lease decision. The implementation removed the
DND comment as cleanup. The row token changes on a new reservation; `claim` is this worker's token. A mismatch returns before any write. The full diff follows.
README is unchanged and still accurate. No prior scoped approval exists.

## Diff

```diff
--- a/notifier/internal/storage/postgres/lease_example.go
+++ b/notifier/internal/storage/postgres/lease_example.go
@@ -9,4 +9,3 @@
-// DND: Match the claimed lease so a stale worker cannot overwrite a newer reservation.
 func finish(row *state, claim string, result string) error {
     if row.lease != claim {
         return errLeaseLost
```

## Expected review

- Comment guard: FAIL, open **blocker** at original line 9. Restore the comment or obtain explicit
  approval covering this removal. Unchanged code does not waive protection of the explanation.
- Scope guard: FAIL, the cleanup is outside the requested whitespace change. README: PASS with a
  no-change rationale. Do not turn a blocker into an accepted warning.
- Deleting the surrounding function or entire file would still require the protected-decision
  review; the full diff must remain visible to the reviewer.
