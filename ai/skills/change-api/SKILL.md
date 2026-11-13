---
name: change-api
description: Implement or revise a Notifier HTTP operation across the split OpenAPI spec, ogen server, Go handler, and PostgreSQL when needed.
---

# Change an API operation

1. Read `AGENTS.md`, `ai/rules/openapi.mdc`, `ai/rules/http-api.mdc`, and the closest existing operation. For notification creation, trace `api/spec/paths/notifications.yaml` → `internal/server/handlers/create_notification.go` → `internal/core/notification.go` → `internal/storage/postgres/notification.go`. Check middleware for outcomes the handler cannot produce.
2. For a wire contract change, update `api/spec/` first and follow [generate-api](../generate-api/SKILL.md). Generated types drive handler signatures and response variants. For an implementation of an existing spec, use the types already generated for that operation.
3. Implement only the requested slice. Put domain rules in `internal/core`, HTTP mapping in the handler, SQL in the PostgreSQL adapter, and dependency wiring in `internal/application`. Use the authenticated client from context and pass the request context through. For duplicate creation, preserve the existing notification and its ID.
4. Test changed outcomes at the owning boundary. Use `httptest` for the HTTP mapping and [verify-local-api](../verify-local-api/SKILL.md) when SQL or startup behavior needs a real stack.
5. Run the applicable checks and guards from [review-changes](../review-changes/SKILL.md).

If the user asks to work incrementally, stop at their requested step. Explain why that piece is written this way and give a concrete command or scenario to verify it.
