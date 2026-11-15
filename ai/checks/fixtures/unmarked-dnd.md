# Unapproved marker removal

## Context

Synthetic case, fixture base D2. The user requests improving comment wording while preserving its
meaning and DND protection. No approval covers marker removal. The row token changes on a new
reservation; `claim` is this worker's token. A mismatch returns before any write. All code is
unchanged, and the README remains accurate.

## Diff

```diff
--- a/notifier/internal/storage/postgres/lease_example.go
+++ b/notifier/internal/storage/postgres/lease_example.go
@@ -9,4 +9,4 @@
-// DND: Match the claimed lease so a stale worker cannot overwrite a newer reservation.
+// Match the claimed lease so a stale worker cannot overwrite a newer reservation.
 func finish(row *state, claim string, result string) error {
     if row.lease != claim {
         return errLeaseLost
```

## Expected review

- Comment guard: FAIL, open **blocker** at original line 9; removing only the marker requires
  explicit human approval. Restore DND or obtain approval for this exact action.
- Scope guard: FAIL, removal of protection was not the requested wording improvement.
- README: PASS with a no-change rationale. Keeping the words and code does not preserve the
  protection mechanism; neither a generic cleanup approval nor AI acceptance is sufficient.
