# Emulator receiver response 201

## Context

Synthetic case, fixture base E1. The user requests an additional receiver-response test fixture.
The emulator's existing callback status setting accepts HTTP status codes and its handler writes
that configured value. Its contract already permits simulated external receiver responses. No
production Notifier endpoint, OpenAPI source, generated output or handler changes. The unchanged
README describes the emulator generically and does not enumerate status presets. No other edits.

## Diff

```diff
--- /dev/null
+++ b/tests/fixtures/receiver-created.json
@@ -0,0 +1 @@
+{"callback_status": 201}
```

## Supplied evidence

Within this fictional case, the fixture is exercised by an existing receiver-status scenario:
configured status 201 is observed by the HTTP client. Full e2e passes for E1 plus this diff. These
statements are inputs to the reviewer exercise, not actual task verification. Regeneration is not
required because neither the contract nor generated code changes.

## Expected review

- All selected guards: PASS. Scope is limited to the requested fixture; README needs no change.
- API contract: the production-success-200 rule permits emulator simulation of an external
  receiver's 201. Do not demand normalization to 200 or claim every non-200 response is an error.
- This targeted status-policy case does not certify arbitrary emulator contracts or code.
