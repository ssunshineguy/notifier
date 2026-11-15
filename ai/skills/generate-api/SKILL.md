---
name: generate-api
description: Regenerate and verify the bundled OpenAPI specs and ogen servers and clients after a contract or generator change.
---

# Generate the API

Read `ai/rules/openapi.mdc` and the [generated boundaries](../../../docs/development.md#generation-boundaries). From the repository root, with the Go version in `go.mod` and Redocly CLI available, run:

```bash
make gen
git diff -- notifier/api/bundle notifier/internal/server/codegen emulator/api/bundle emulator/internal/server/codegen notifier/internal/interactions/infoservice/codegen
git status --short -- notifier/api/bundle notifier/internal/server/codegen emulator/api/bundle emulator/internal/server/codegen notifier/internal/interactions/infoservice/codegen
```

`make gen` runs Redocly lint and bundle for both services, regenerates the ogen servers and the Information Service client with the version from `go.mod`, and builds the module. Inspect the generated diff, including new or deleted files. Trace each changed path and response back to the source or generator change. Fix generation problems at the source. Pass the result to `api-contract-guard`.

For a regeneration drift check during review, run `make gen` in a temporary copy of the reviewed tree and compare the generated files to the originals.
