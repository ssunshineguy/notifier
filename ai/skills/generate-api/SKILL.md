---
name: generate-api
description: Regenerate and verify Notifier's bundled OpenAPI spec and ogen server after a contract or generator change.
---

# Generate the API

Read `ai/rules/openapi.mdc`. From the repository root, with the Go version in `go.mod` and Redocly CLI available, run:

```bash
make gen
git diff -- api/bundle/openapi.yaml internal/server/codegen
git status --short -- api/bundle/openapi.yaml internal/server/codegen
```

`make gen` runs Redocly lint and bundle, regenerates the ogen package with the version from `go.mod`, and builds the module. Inspect the generated diff, including new or deleted files. Trace each changed path and response back to the source or generator change. Fix generation problems at the source. Pass the result to `api-contract-guard`.

For a regeneration drift check during review, run `make gen` in a temporary copy of the reviewed tree and compare both generated outputs to the originals.
