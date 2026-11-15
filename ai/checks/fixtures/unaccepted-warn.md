# Warning without applicable human acceptance

## Context

Synthetic base W1. The user requests a teaching example in `scripts/example.py`; this exact small
addition is in scope. The script summary and surrounding code are unchanged. README does not
mention the example. In this fictional case, full e2e and the relevant unit scenario passed for the
diff; this is not evidence of real task verification. There is no W1-approval for this case. An AI reviewer says the comment is harmless, and the human
has not responded. An older human approval covers a different comment in `scripts/other.py` only.

## Diff

```diff
--- a/scripts/example.py
+++ b/scripts/example.py
@@ -7,2 +7,3 @@
 def identity(value):
+    # Return the value.
     return value
```

## Expected review

- Scope and README: PASS with the same no-change rationale as `accepted-warn`.
- Comment guard: FAIL, original **warn** at `scripts/example.py:8`, disposition `open`, consequence
  unnecessary noise. Require removal and recheck or explicit human acceptance of this exact risk.
- AI acceptance, silence, listing the warning and the unrelated prior decision do not satisfy the
  acceptance policy. Do not downgrade to info to manufacture PASS.
