# Useful explanation of a lease invariant

## Context

Synthetic case, fixture base D1. The task is to explain the existing stale-worker protection at the
comparison below. The only change is a comment. The row's lease token changes on a new reservation;
`claim` is the token held by this worker. A mismatch returns before any write. No refactor or
production behavior change is requested. README already explains lease-based ownership and needs
no change. There are no prior findings or risk acceptances.

## Prior AI explanation to the author

A maintainer could mistake the token comparison for redundant validation and remove it. An expired
worker would then overwrite the result of a newer reservation. I will keep that reason next to the
comparison as a DND comment. This rationale needs no separate approval to add the comment.

## Diff

```diff
--- a/notifier/internal/storage/postgres/lease_example.go
+++ b/notifier/internal/storage/postgres/lease_example.go
@@ -10,5 +10,6 @@
 func finish(row *state, claim string, result string) error {
+    // DND: Match the claimed lease so a stale worker cannot overwrite a newer reservation.
     if row.lease != claim {
         return errLeaseLost
     }
     row.result = result
```

## Expected review

- Selected guards: PASS. Scope is exactly the requested explanatory addition; README needs no edit.
- The comment explains a concrete failure prevented by otherwise tempting code removal. It is
  useful, accurate under the supplied context, and supported by the prior AI rationale.
- Do not demand permission for every DND addition or reject all local contract explanations.
- This focused comment case does not prove the repository's lease implementation or concurrency.
