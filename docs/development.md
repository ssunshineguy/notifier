# Local development

Run the commands below from the checkout root. [Makefile](../Makefile) and its scripts
are the executable source of truth; AI workflows use the same commands.
See [configuration](configuration.md) for settings, units, constraints and when changes apply.

## Prerequisites

- Docker Engine with Docker Compose v2 or newer, running and accessible from the shell.
- `make`, Bash, Git and `curl`; the documented shell commands target macOS/Linux.
- The Go version declared in [go.mod](../go.mod).
- `golangci-lint` v2 with the formatters in [.golangci.yaml](../.golangci.yaml).
- The exact Python patch in [.python-version](../.python-version), available as
  `python3.14`, with `venv` and pip. The e2e helpers use POSIX timers.
- For API generation only: Node.js and a compatible Redocly CLI on `PATH` as
  `redocly`. ogen is a Go tool dependency in [go.mod](../go.mod).

Python dependencies are installed explicitly by `make setup-e2e`. The normal test
command does not install or update packages. Go module and container downloads may
require network access on the first run.

## Choose an isolated stack

Compose uses the checkout directory name unless `COMPOSE_PROJECT_NAME` overrides it.
Use a distinct name when working in parallel or when an earlier checkout used the same name:

```bash
export COMPOSE_PROJECT_NAME=notifier_4_wave3
make up
```

Keep this environment variable in the same shell for every Compose and e2e command.
A local `.env` can hold the project name for later shells. Do not reuse somebody
else's project. `make up` builds the service images, initializes PostgreSQL, applies
migrations, provisions the local e2e role and waits for API, worker and emulator health.

PostgreSQL is pinned by a readable version tag and a digest in
[docker-compose.yaml](../docker-compose.yaml). The local migration baseline was
changed in PR #37: new databases create the split notification/delivery status schema
in the initial table migration. An old volume's migration history is not upgraded by
editing those initial migrations. Use a fresh project/volume for this walkthrough;
keep an existing volume until its owner has chosen a migration or disposal procedure.

Host ports are allocated dynamically. `make up` prints them; resolve them again after
recreating containers:

```bash
API_URL="http://$(docker compose port api 8080)"
EMULATOR_URL="http://$(docker compose port emulator 8081)"
POSTGRES_ADDRESS="$(docker compose port postgres 5432)"
printf 'API: %s\nEmulator: %s\nPostgreSQL: %s\n' \
  "$API_URL" "$EMULATOR_URL" "$POSTGRES_ADDRESS"
```

Inside the Compose network, the worker uses `http://emulator:8081`. A callback URL
must be reachable from that network; a published host address is not the emulator's
container address. Internal health probes are not published business endpoints.
See [verify-local-api](../ai/skills/verify-local-api/SKILL.md) for in-container checks.

`make down` stops the selected stack while retaining its data. `make psql` opens its
local database. `make reset` deletes that project's data volume: it is an explicit
reset operation, not a prerequisite or a routine cleanup command.

## Run Collector

After `make up`, use the same `COMPOSE_PROJECT_NAME` and run from the checkout root:

```bash
docker compose run --rm --no-deps -T collector
```

This permanently deletes ordinary stopped notifications older than the configured retention.
Active notifications and DLQ are retained. See [configuration](configuration.md) for the
retention and batch settings. The stack must be running with its migrations applied and
Notifier image built; `--no-deps` does not start them. Collector runs once and exits. Its
`maintenance` profile excludes it from normal stack startup. Run only one instance at a time.

## Checks and suites

| Command | What it verifies | Required services / side effects |
| --- | --- | --- |
| `make check` | Go unit tests, vet, lint, formatting and `git diff --check` | No Compose stack; checks do not format or fix source files. |
| `make test-integration` | PostgreSQL adapter integration suite | Docker; the Go tests create and remove their own isolated databases. |
| `make test-e2e` | Python helper tests and HTTP/database scenarios | Run `make setup-e2e` and `make up` first; uses the selected Compose project and per-test clients. |
| `make ai-check` | Canonical AI metadata, local setup links, generated TOML and repeat generation | Run `make ai-setup` for initial installation; the check reports drift without repairing it. |

Each check exits nonzero on failure; stop and resolve the failure before treating that
suite as passed. A successful `make check` does not imply that integration or e2e ran.
Run e2e for every code change, including Python, tests, scripts, tooling and generated
code. Prose-only changes still need scope and README review. See the
[test rules](../ai/rules/tests.mdc) and [review workflow](../ai/skills/review-changes/SKILL.md).

For concurrency or process lifecycle changes, also run `go test -race ./...`.
For PostgreSQL changes, run the integration suite and any migration-specific checks.
For OpenAPI or generator changes, regenerate and inspect all affected outputs as
explained below. These checks are selected by the changed area, not silently skipped
because another suite passed. The integration fixture selects its own PostgreSQL
image; the Compose digest pin applies to the e2e and walkthrough stack.

### Python installation and updates

```bash
make setup-e2e
make test-e2e
```

The default environment is `tests/e2e/.venv`; `PYTHON=/path/to/python` selects the
interpreter, whose patch must match `.python-version`. The test command checks the
installed versions and editable checkout before running pytest; a mismatch fails with
setup instructions. Select a separate clean environment with
`E2E_VENV`, passing the same path to setup and tests. This permits two independent
installations without deleting an existing environment:

```bash
make setup-e2e E2E_VENV=/tmp/notifier-e2e-a
make setup-e2e E2E_VENV=/tmp/notifier-e2e-b
/tmp/notifier-e2e-a/bin/python -m pip list --format=freeze > /tmp/notifier-e2e-a.txt
/tmp/notifier-e2e-b/bin/python -m pip list --format=freeze > /tmp/notifier-e2e-b.txt
diff -u /tmp/notifier-e2e-a.txt /tmp/notifier-e2e-b.txt
/tmp/notifier-e2e-a/bin/python -m pip check
/tmp/notifier-e2e-b/bin/python -m pip check
make test-e2e E2E_VENV=/tmp/notifier-e2e-a
make test-e2e E2E_VENV=/tmp/notifier-e2e-b
```

Use previously nonexistent paths for a clean-install comparison. Direct dependencies
remain in [pyproject.toml](../tests/e2e/pyproject.toml); the generated
[constraints.txt](../tests/e2e/constraints.txt) pins direct, transitive and build
requirements. The install command applies the constraints to both the target
virtual environment and isolated build environments, using pip
[build constraints](https://pip.pypa.io/en/stable/user_guide/#build-constraints). The pip-tools generator is
pinned with the tooling dependencies. To intentionally update the lock, use
`make lock-e2e`, inspect the dependency diff and repeat clean-install, `pip check`
and e2e verification. Updating the Python patch or PostgreSQL image also requires
an explicit source change and verification.

The reproducibility check compares package versions on the selected Python and
platform. It does not establish identical package artifacts across platforms or
bit-for-bit reproducibility of all container images.

## Generation boundaries

| Editable source | Generated output | Command |
| --- | --- | --- |
| `notifier/api/spec/`, `emulator/api/spec/`, `build/openapi/` | Both `api/bundle/` directories; both `internal/server/codegen/` directories; `notifier/internal/interactions/infoservice/codegen/` | `make gen` |
| `notifier/db/migrations/` and bootstrap definitions | `notifier/db/schema/` | `make migrate` |
| `ai/` | Local `.claude/`, `.agents/skills/`, `.codex/agents/` installation | `make ai-setup` |
| `tests/e2e/pyproject.toml` | `tests/e2e/constraints.txt` | `make lock-e2e` |

Edit the sources and regenerate; do not patch generated API code or schema dumps by
hand. `make gen` lints and bundles both OpenAPI sources, runs ogen and builds Go.
`make migrate` changes the selected database and regenerates its schema snapshot;
it is not a read-only verification command. Preserve applied migration history and
follow the [migration rules](../ai/rules/migrations.mdc), including review before
applying changed migrations.

For generation drift checks, run the generator in a temporary copy of the reviewed
tree and compare the generated paths. This keeps verification from changing the
working checkout. [generate-api](../ai/skills/generate-api/SKILL.md) describes the API
checks. Inspect new and deleted generated files as well as ordinary diffs.

## First notification walkthrough — pending callback journal

The commands below cover the current client registration, emulator setup, creation
and status reads. The callback journal from task 10 is not present in this checkout.
This walkthrough is not complete until its actual journal endpoint and response
contract are available, documented and exercised against this same stack. A final
`delivered` status is useful state evidence but does not replace observing the
received callback in the journal.

Use the fresh stack and shell variables above. A client has no registration API in
this prototype; register a synthetic client in `notifier.t_client`, the same mechanism
used by the e2e fixture. These writes affect only the selected local stack:

```bash
CLIENT_ID="demo-$(python3.14 -c 'import uuid; print(uuid.uuid4().hex)')"
EXTERNAL_ID="first-notification"
docker compose exec -T postgres psql -U notifier_api -d notifier \
  -v ON_ERROR_STOP=1 -v client_id="$CLIENT_ID" <<'SQL'
INSERT INTO notifier.t_client (client_id, callback_url, max_lifetime_sec)
VALUES (:'client_id', 'http://emulator:8081/callback', 3600);
SQL

curl --noproxy '*' --fail-with-body --silent --show-error --max-time 5 \
  -X POST "$EMULATOR_URL/v1/client-overrides/$CLIENT_ID" \
  -H 'Content-Type: application/json' \
  -H "X-Request-ID: $CLIENT_ID-price" \
  -d '{"parameter":"price","value":200}'

NOTIFICATION_ID="$(curl --noproxy '*' --fail-with-body --silent --show-error --max-time 5 \
  -X POST "$API_URL/v1/notifications" \
  -H 'Content-Type: application/json' \
  -H "X-Client-ID: $CLIENT_ID" \
  -H "X-Request-ID: $CLIENT_ID-create" \
  -d '{"external_id":"first-notification","notification_params":[{"type":"price","operation":"greater_than","value":100}]}' \
  | python3.14 -c 'import json, sys; print(json.load(sys.stdin)["notification_id"])')"
printf 'Notification: %s\n' "$NOTIFICATION_ID"
```

The price override makes the condition true. No callback override is needed for the
emulator's normal successful response. The notification deadline is derived from
this demo client's lifetime. Creation returns `200` and a `notification_id`.

Read the current state with:

```bash
curl --noproxy '*' --fail-with-body --silent --show-error --max-time 5 \
  "$API_URL/v1/notifications/$NOTIFICATION_ID" \
  -H "X-Client-ID: $CLIENT_ID" \
  -H "X-Request-ID: $CLIENT_ID-read" \
  | python3.14 -m json.tool
```

Immediately after creation it may still be `active`. With the shipped configuration,
processing should settle to `notification_status: success` and
`delivery_status: delivered`. Initial check delay, worker scheduling and callback
retry settings affect the wait; read their current sources in
[configuration](configuration.md), rather than assuming a synchronous POST callback.

To finish this section after task 10, add a bounded journal read keyed by the created
notification ID, verify the callback's `notification_id`, `external_id`,
`notification_status` and `sent_dt`, then poll GET with a bounded overall deadline for
`success`/`delivered`. Verify the journal's reset/retention semantics for repeat runs.
Do not substitute log scraping or a guessed journal URL for that contract.
