# Python-only change

## Context

Synthetic case; nothing below is evidence of a real repository test run. The user asks to reject
blank environment names in an existing local helper. This is the entire task diff against fixture
base P1; no pre-existing edits. No refactoring is authorized. The unchanged README describes the
public service commands and does not document this private helper.

## Diff

```diff
--- a/scripts/environment.py
+++ b/scripts/environment.py
@@ -8,2 +8,4 @@
 def environment_name(value):
+    if not value.strip():
+        raise ValueError("environment name must not be blank")
     return value
```

## Supplied evidence

A new isolated unit scenario passes for empty, whitespace-only and nonempty inputs. No API e2e run
was performed. The implementer claims that changing only Python tooling exempts the task from e2e.
There are no human risk acceptances.

## Expected review

- Scope: PASS, narrowly implements the request; README: PASS with a concrete no-change reason.
- Test coverage: FAIL, open blocker at `scripts/environment.py:9`: every code change requires the
  full API e2e suite. Request an actual run for this diff; the new unit scenario does not replace it.
- Explain Python/tooling routing explicitly. Do not invent an exception for a private script.
