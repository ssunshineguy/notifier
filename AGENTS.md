# Context for AI agents

## Service

Notifier sends an HTTP event to the client when a condition set by the client is met.
Architecture, sequence diagrams and prototype limitations are described in [README.md](README.md).

The service is a single `notifier` binary that runs in different modes:

| Mode | Purpose | Lifecycle |
| --- | --- | --- |
| `api` | CRUD of notifications over HTTP | long-running process |
| `worker` | checks conditions and delivers events to clients | long-running process |
| `collector` | removes stopped notifications in batches | runs once and exits, started on a schedule |
| `migrate` | applies database migrations | one-off run before the services start |

## Language

- English: commit messages, PR titles and descriptions, branch names, code comments,
  OpenAPI descriptions, and everything written for AI agents (`AGENTS.md`, `ai/**`).
- Russian is allowed only in `README.md`.

## Repository structure

Described in [README.md](README.md#структура-репозитория).

## Layer boundaries

- `core` imports nothing from `internal/` and performs no I/O.
- `server/handlers` is a thin layer: parse the request → call the logic → write the response. No SQL or HTTP clients here.
- `storage` and `interactions` do not import `server`, `worker` or `collector`.
- `server/codegen` is generated from `api/bundle/openapi.yaml`; change it only through the spec in `api/spec/`.
- Dependencies are wired only in `application`.

## Keeping docs up to date

After every task update this file, the "Структура репозитория" section of `README.md` and `ai/rules/`
if the folder structure, layer boundaries, run modes or development commands have changed.
