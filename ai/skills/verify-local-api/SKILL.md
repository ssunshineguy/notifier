---
name: verify-local-api
description: Smoke-test Notifier HTTP behavior with curl and real PostgreSQL in Docker Compose. Use for probes, migrations, API persistence, or local startup changes.
---

# Verify the API on a local stack

Check which Compose project and ports are in use. If `db/migrations/` or `db/init/` changed, run `migration-guard` and resolve blockers or record the author's exception before starting a stack that applies the changes.

For a read-only check, use the intended local stack. Start it with `make up` if needed. It builds the image and waits for migrations and API readiness. Use `docker compose ps -a` and `docker compose logs --tail=100 api migrate postgres` to diagnose startup.

```bash
docker compose exec -T postgres psql -U notifier_migrator -d notifier -v ON_ERROR_STOP=1 -c 'SELECT 1;'
curl --noproxy '*' -fsS -i --max-time 5 http://127.0.0.1:8080/health/live
curl --noproxy '*' -fsS -i --max-time 5 http://127.0.0.1:8080/health/ready
```

Expect one row containing `1` and two `200` responses. Use the actual published API port if it differs from the default.

## Disposable stack

Use a disposable Compose project for test writes, migration verification, and failure injection. Copy the reviewed tree, including new files, into a temporary directory. In its Compose file, set both image references to a unique tag and use free loopback host ports while keeping the container ports. Run `make` and `docker compose` from that copy with the same unique `COMPOSE_PROJECT_NAME`. Use `docker compose port api 8080` to find the API address.

- **API operation**. Use a request example from `api/spec/`. Add a test client fixture and send `X-Client-ID` and `X-Request-ID` on business requests. Assert the status, content type and relevant fields. Check the stored row for persistence changes. For duplicate creation, repeat with the same client and external ID and compare the returned ID.
- **Readiness**. Use PostgreSQL statement logging to verify that the application executes `SELECT 1`. Stop PostgreSQL in the disposable project and expect liveness `200` and readiness `503`. Restart it and expect readiness `200` again. Use `docker inspect` to verify Docker health transitions when the probe itself changed.
- **Migration**. From the disposable copy, run `make migrate` twice as specified in `ai/rules/migrations.mdc` and inspect the generated schema. Verify an upgrade from the base schema with representative rows when the change depends on existing data. Use a fresh volume to verify a bootstrap change.

When finished, run `docker compose down -v` from the disposable copy with its project name and remove its temporary files and image tag. Stop a normal local stack with `make down` if you started it for the check and it is no longer needed. Report the commands, outcomes and any material gaps.
