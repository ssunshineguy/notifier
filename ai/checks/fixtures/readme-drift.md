# Unchanged README with a removed command

## Context

Synthetic case, fixture base R1. The user explicitly authorized renaming the `smoke` Make target to
`check`, preserving its script. No other edits or risk acceptance. The author has not yet approved a
README edit. At base R1, `README.md:40` says: `Run make smoke to check the local setup.` The README
is unchanged and there is no fallback alias. The script and other commands are unchanged.

## Diff

```diff
--- a/Makefile
+++ b/Makefile
@@ -1,2 +1,2 @@
-.PHONY: smoke
-smoke:
+.PHONY: check
+check:
```

## Expected review

- Scope: PASS, the target rename is explicitly requested.
- README: FAIL, open blocker at `README.md:40`: the advertised command no longer exists.
- Propose the exact replacement `Run make check to check the local setup.` and obtain the author's
  approval before editing. A read-only reviewer must not silently make the change.
- Unchanged README does not make this guard N/A. Merely listing the stale command does not fix it.
