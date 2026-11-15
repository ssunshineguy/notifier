---
name: add-endpoint
description: Add or change a Notifier HTTP endpoint across the split OpenAPI spec, ogen server, Go handler, and PostgreSQL when needed.
---

# Add or change an API endpoint

Read `AGENTS.md`, `ai/rules/openapi.mdc`, and `ai/rules/http-api.mdc`. Use the closest existing operation as a reference. For POST notifications, start at `notifier/api/spec/paths/notifications.yaml` and follow the handler in `notifier/internal/server/handlers/create_notification.go` into `notifier/internal/core/notification.go` and `notifier/internal/storage/postgres/notification.go`.

1. **Define the contract.** Register a new URL in `notifier/api/spec/openapi.yaml` and put its operations in a file under `notifier/api/spec/paths/`. Add a method for an existing URL to that URL's file. Define inputs and responses in `notifier/api/spec/components/` and reuse shared parameters, headers, and errors. Include outcomes from authentication, admission, and request timeouts. If the operation is already specified, check its existing contract before writing code.
2. **Generate the server.** After changing the spec, follow [generate-api](../generate-api/SKILL.md) to run `make gen`. It writes `notifier/api/bundle/openapi.yaml` and `notifier/internal/server/codegen/`. Find the operation's signature on `codegen.Handler`, and use its generated request and response variants. Use the existing generated types when implementing an unchanged operation.
3. **Implement domain and storage behavior.** Put domain types and rules in `notifier/internal/core/`. Put SQL and row mapping in `notifier/internal/storage/postgres/`. Inspect `notifier/db/schema/` and change `notifier/db/migrations/` only if the schema needs to change. Use the request context for I/O and scope client-owned SQL by the resolved client ID. Translate storage outcomes into domain errors or values.
4. **Connect the handler.** Implement the operation on `*Handler` in `notifier/internal/server/handlers/`. Get the client through `middleware.ClientFromContext` for client-scoped operations, map the generated inputs to domain values, and return the generated response variant. Extend the handler's `Store` interface and its test stubs when it needs a new storage method. `notifier/internal/application/api.go` supplies the store and middleware to the ogen server. Check that the operation reaches your handler instead of the embedded `UnimplementedHandler`.
5. **Verify the implemented behavior.** Test status, body and relevant headers through the generated HTTP router with `httptest`. Cover changed SQL behavior with PostgreSQL integration tests as described in [the test rules](../../rules/tests.mdc#postgresql-integration-tests). Use [verify-local-api](../verify-local-api/SKILL.md) for HTTP smoke checks on a running stack. For client-owned resources, cover the owning client and another client. Follow [review-changes](../review-changes/SKILL.md) for checks and guards.

## New condition types

For each new type, trace the complete path below. Report the changed paths or a substantive
**N/A with a reason** for every row; a layer that already handles the variant generically
still needs that explanation. Keep `and`/`or` generic and preserve stored-data compatibility.

| Layer | Required review |
| --- | --- |
| Contract | Add the variant, discriminator, validation, operators and examples in `notifier/api/spec/`; check affected responses and the Information Service contract. |
| Generation | Run [generate-api](../generate-api/SKILL.md); inspect bundles, both servers and the selected Information Service client. Never edit generated Go by hand. |
| Domain | Implement parsing, validation, evaluation and API/domain mapping in `notifier/internal/core/` and handlers. |
| Storage | Check serialization, decoding and existing persisted conditions in `notifier/internal/storage/postgres/`; change schema/migrations only when necessary. |
| Worker/provider | Connect evaluation, required source collection and provider I/O in `notifier/internal/worker/` and `notifier/internal/interactions/`; preserve error and short-circuit semantics. |
| Emulator | Supply the source and controllable test data in `emulator/api/spec/` and `emulator/internal/`, or explain why the type needs no emulator change. |
| Tests | Cover the new behavior and failures at the relevant layers; add/update an e2e scenario and run the full e2e suite. |
| Documentation | Update contract examples and affected AI rules; run README guard and record its edit/N/A rationale. README edits still require the author's scoped approval. |

When the user asks for an incremental change, finish the requested step, explain why it is structured that way, and give a concrete verification command or scenario for that step.
