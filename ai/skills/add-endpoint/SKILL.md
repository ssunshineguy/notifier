---
name: add-endpoint
description: Add or change a Notifier HTTP endpoint across the split OpenAPI spec, ogen server, Go handler, and PostgreSQL when needed.
---

# Add or change an API endpoint

Read `AGENTS.md`, `ai/rules/openapi.mdc`, and `ai/rules/http-api.mdc`. Use the closest existing operation as a reference. For POST notifications, start at `api/spec/paths/notifications.yaml` and follow the handler in `internal/server/handlers/create_notification.go` into `internal/core/notification.go` and `internal/storage/postgres/notification.go`.

1. **Define the contract.** Register a new URL in `api/spec/openapi.yaml` and put its operations in a file under `api/spec/paths/`. Add a method for an existing URL to that URL's file. Define inputs and responses in `api/spec/components/` and reuse shared parameters, headers, and errors. Include outcomes from authentication, admission, and request timeouts. If the operation is already specified, check its existing contract before writing code.
2. **Generate the server.** After changing the spec, follow [generate-api](../generate-api/SKILL.md) to run `make gen`. It writes `api/bundle/openapi.yaml` and `internal/server/codegen/`. Find the operation's signature on `codegen.Handler`, and use its generated request and response variants. Use the existing generated types when implementing an unchanged operation.
3. **Implement domain and storage behavior.** Put domain types and rules in `internal/core/`. Put SQL and row mapping in `internal/storage/postgres/`. Inspect `db/schema/` and change `db/migrations/` only if the schema needs to change. Use the request context for I/O and scope client-owned SQL by the resolved client ID. Translate storage outcomes into domain errors or values.
4. **Connect the handler.** Implement the operation on `*Handler` in `internal/server/handlers/`. Get the client through `middleware.ClientFromContext` for client-scoped operations, map the generated inputs to domain values, and return the generated response variant. Extend the handler's `Store` interface and its test stubs when it needs a new storage method. `internal/application/api.go` supplies the store and middleware to the ogen server. Check that the operation reaches your handler instead of the embedded `UnimplementedHandler`.
5. **Verify the implemented behavior.** Test status, body and relevant headers through the generated HTTP router with `httptest`. Test changed SQL behavior with real PostgreSQL using [verify-local-api](../verify-local-api/SKILL.md). For client-owned resources, cover the owning client and another client. Follow [review-changes](../review-changes/SKILL.md) for checks and guards.

When the user asks for an incremental change, finish the requested step, explain why it is structured that way, and give a concrete verification command or scenario for that step.
