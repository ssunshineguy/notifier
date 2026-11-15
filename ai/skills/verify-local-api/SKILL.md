---
name: verify-local-api
description: Smoke-test Notifier HTTP behavior with curl and real PostgreSQL in Docker Compose. Use for probes, migrations, API persistence, or local startup changes.
---

# Verify the API on a local stack

Use the shared commands and prerequisites in the [development guide](../../../docs/development.md).
Run `make setup-e2e` explicitly to install pinned dependencies and `make test-e2e`
against the same project for every code change; the smoke checks below do not replace it.

Check which Compose project and ports are in use. If `notifier/db/migrations/` or `notifier/db/init/` changed, run `migration-guard` and resolve confirmed blockers and obtain or reuse explicit human acceptance of remaining warnings under [guard outcomes](../../../AGENTS.md#guard-outcomes) before starting a stack that applies the changes.

For a read-only check, use the intended local stack. Start it with `make up` if needed. It builds the images and waits for migrations, API readiness, and emulator readiness. Use `docker compose ps -a` and `docker compose logs --tail=100 api emulator migrate postgres` to diagnose startup.

```bash
docker compose exec -T postgres psql -U notifier_migrator -d notifier -v ON_ERROR_STOP=1 -c 'SELECT 1;'
docker compose exec -T api curl --noproxy '*' -fsS -i --max-time 5 http://127.0.0.1:9080/health/live
docker compose exec -T api curl --noproxy '*' -fsS -i --max-time 5 http://127.0.0.1:9080/health/ready
docker compose exec -T emulator curl --noproxy '*' -fsS -i --max-time 5 http://127.0.0.1:9081/health/live
docker compose exec -T emulator curl --noproxy '*' -fsS -i --max-time 5 http://127.0.0.1:9081/health/ready
```

Expect one row containing `1` and four `200` responses. Verify that both probe paths return `404` on each published HTTP port when checking probe isolation.

## Disposable stack

Use a disposable Compose project for test writes, migration verification, and failure injection. Copy the reviewed tree, including new files, into a temporary directory with a unique name. Run `make` and `docker compose` from that copy. If an inherited `COMPOSE_PROJECT_NAME` would select another project, unset it or set a unique value for all commands in the disposable copy. `make up` prints the assigned loopback addresses. Use `docker compose port` to get the current addresses.

- **API operation**. Use a request example from `notifier/api/spec/`. Add a test client fixture and send `X-Client-ID` and `X-Request-ID` on business requests. Assert the status, content type and relevant fields. Check the stored row for persistence changes. For duplicate creation, repeat with the same client and external ID and compare the returned ID.
- **Readiness**. Use PostgreSQL statement logging to verify that the application executes `SELECT 1`. Stop PostgreSQL in the disposable project. Expect Notifier API liveness `200` and readiness `503`, and both emulator probes to return `200`. Restart PostgreSQL and expect API readiness `200` again. Use `docker inspect` to verify Docker health transitions when the probe itself changed.
- **Migration**. From the disposable copy, run `make migrate` twice as specified in `ai/rules/migrations.mdc` and inspect the generated schema. Verify an upgrade from the base schema with representative rows when the change depends on existing data. Use a fresh volume to verify a bootstrap change.

When finished, run `docker compose down -v` from the disposable copy with its project name and remove its temporary files and image tags. Stop a normal local stack with `make down` if you started it for the check and it is no longer needed. Report the commands, outcomes and any material gaps.
