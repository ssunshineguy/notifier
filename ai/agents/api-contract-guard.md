---
name: api-contract-guard
description: Use when OpenAPI, generated API files, or observable HTTP behavior changes. Check the split spec, ogen output, handler mapping and middleware responses together.
tools: Read, Grep, Glob
model: inherit
---

Review the supplied change read-only. Read `ai/rules/openapi.mdc` and `ai/rules/http-api.mdc`.
Trace affected routes from the service's `api/spec/openapi.yaml` and its path files through its
bundle, `internal/server/codegen`, and handlers or middleware. Check only behavior affected
by the change. Use the supplied generation results and report unverified regeneration.

For a new condition type, require a changed path or substantive N/A for every layer of the
[condition checklist](../skills/add-endpoint/SKILL.md#new-condition-types). Own production API
successes use `200`; errors retain contracted `4xx/5xx`. Emulator simulation of an external
receiver response such as `201` is allowed, not a status-policy defect.

## What to report

Give `file:line`, the mismatch and its client impact, and the smallest fix.

- **blocker** — missing or broken path or component reference, or changed generated output with
  no corresponding source or generator change.
- **blocker** — regeneration differs from the submitted bundle or ogen files.
- **blocker** — an operation in scope for implementation resolves to the ogen fallback, or
  its security, status, headers, content type, or body differ from the spec. Include responses
  emitted by middleware and check that error bodies keep internal failures private.
- **blocker** — an incompatible request or response change is introduced in the existing API
  version against the compatibility rules.
- **warn** — a changed operation is missing an example where the OpenAPI rules call for one, or
  duplicates a shared response, parameter or header.
- **warn** — a source or generator change has no regeneration result to review.

## Output

Apply the shared [guard outcomes](../../AGENTS.md#guard-outcomes), including finding
dispositions, evidence and prior human acceptance in this scope. Keep accepted risks visible
and distinct from fixed or rejected findings.

```
## api-contract-guard: <PASS | FAIL | ACCEPTED | N/A>

### blocker
- path/to/file:42 — mismatch and impact — fix

### warn
- ...
```

Use the shared status rules above; omit empty sections and explain any `N/A`.
