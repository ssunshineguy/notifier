# Configuration reference

The shipped non-secret values live in [config.yaml](../notifier/configs/config.yaml).
The [loader](../notifier/internal/config/config.go) defines fallback values and validates
configuration. Do not assume that deleting a key preserves the shipped value: for example,
`database.min_conns` has a different loader fallback, and `notification.scheduled_time_sec`
is required to be positive even when omitted. Unknown YAML keys are rejected. The loader
validates the whole shared configuration for every mode, but reads only the selected mode's
DSN file.

YAML, CLI and environment settings are read at process startup; there is no hot reload.
Restart the affected mode after changing them. The stock Compose images contain the YAML,
so use `make up` to rebuild and recreate services after changing it. Client settings in
`t_client` are read from PostgreSQL for each API request or callback target lookup instead.
The source column below points to canonical values and their implementation, rather than
copying numeric defaults into another document.

| Parameter | Purpose | Unit | Mode | Constraints | When applied | Value source / implementation |
| --- | --- | --- | --- | --- | --- | --- |
| Positional mode | Select the process lifecycle | Name | All Notifier modes | Exactly one of `api`, `worker`, `collector`, `migrate` | Startup | [Notifier CLI](../notifier/cmd/notifier/main.go) |
| `-config` | Select the shared YAML file | Path | All Notifier modes | Readable YAML with known keys; flags precede the mode | Startup | [Notifier CLI](../notifier/cmd/notifier/main.go), [loader](../notifier/internal/config/config.go) |
| `-database-dsn-file` / `<mode>.dsn` | Select database credentials for this mode only | Path / connection string | All Notifier modes | Nonempty, parseable pgx DSN; default file is beside the selected YAML; another mode's file is not a fallback | Startup | [Loader](../notifier/internal/config/config.go); [Compose secret mounts](../docker-compose.yaml) |
| `-listen-addr` | Override `api.listen_addr` | Host:port | API | Nonempty override; socket must be bindable; ignored by other Notifier modes | Startup, after YAML validation | [Notifier CLI](../notifier/cmd/notifier/main.go) |
| `NOTIFIER_LOG_LEVEL` | Set the minimum structured log level | slog level | All Notifier modes | `DEBUG`, `INFO`, `WARN`, `ERROR` and offsets accepted by `slog.Level.UnmarshalText`; invalid values fail startup | Startup | [Notifier CLI](../notifier/cmd/notifier/main.go); Compose forwards it to API/worker |
| `api.listen_addr` | Bind the business HTTP listener | Host:port | API | Nonempty; valid socket address required at bind time | Startup | [YAML](../notifier/configs/config.yaml), [API wiring](../notifier/internal/application/api.go); Compose supplies a CLI override |
| `api.max_connections` | Cap accepted business TCP connections, including idle connections | Connections | API | Positive integer; separate probe listener is outside this cap | Startup | [YAML](../notifier/configs/config.yaml), [listener](../internal/health/server.go) |
| `api.max_in_flight` | Bound concurrently admitted business requests | Requests | API | Positive integer | Startup | [YAML](../notifier/configs/config.yaml), [admission](../notifier/internal/server/middleware/in_flight.go) |
| `api.max_queued` | Bound requests waiting for an execution slot | Requests | API | Nonnegative integer; zero disables waiting capacity | Startup | [YAML](../notifier/configs/config.yaml), [admission](../notifier/internal/server/middleware/in_flight.go) |
| `api.slot_wait_timeout_ms` | Limit waiting for an admission slot | Milliseconds | API | Nonnegative int32; zero rejects immediately when all execution slots are busy; inherited request deadline can expire earlier | Startup | [YAML](../notifier/configs/config.yaml), [admission](../notifier/internal/server/middleware/in_flight.go) |
| `api.request_timeout_ms` | Bound queue wait, body decoding, security checks and handler work together | Milliseconds | API | Positive int32; expiration produces the `503` request-timeout Problem; not a complete network round-trip guarantee | Startup | [YAML](../notifier/configs/config.yaml), [request timeout](../notifier/internal/server/middleware/request_timeout.go) |
| `api.write_timeout_ms` | Bound response writing after processing, including a timeout response | Milliseconds | API | Positive int32; independent of the processing budget | Startup | [YAML](../notifier/configs/config.yaml), [request timeout](../notifier/internal/server/middleware/request_timeout.go), [API wiring](../notifier/internal/application/api.go) |
| `api.idle_timeout_ms` | Bound waiting for the next request on a keep-alive connection | Milliseconds | API | Positive int32 | Startup | [YAML](../notifier/configs/config.yaml), [API wiring](../notifier/internal/application/api.go) |
| API header read timeout | Bound reading request headers before handler processing | Duration | API | Code constant; not a YAML key | Server creation | [API wiring](../notifier/internal/application/api.go) |
| `worker.workers` | Set the number of independent polling loops | Loops | Worker | Positive integer | Startup | [YAML](../notifier/configs/config.yaml), [worker loop](../notifier/internal/worker/worker.go) |
| `worker.batch_size` | Limit outstanding handlers per polling loop | Notifications per loop | Worker | Positive integer; maximum outstanding handlers is `workers × batch_size`; claims use free slots | Startup | [YAML](../notifier/configs/config.yaml), [worker loop](../notifier/internal/worker/worker.go) |
| `worker.cycle_timeout_ms` | Bound one claim-and-processing cycle | Milliseconds | Worker | Positive int32; shared parent deadline for that cycle's handlers; must not exceed the lease | Startup | [YAML](../notifier/configs/config.yaml), [worker loop](../notifier/internal/worker/worker.go) |
| `collector.batch_size` | Limit rows removed in one cleanup transaction | Rows | Collector | Positive whole integer | Startup; each batch in this invocation | [YAML](../notifier/configs/config.yaml), [loader](../notifier/internal/config/config.go), [cleanup query](../notifier/internal/storage/postgres/notification_collection.go) |
| `collector.retention_days` | Retain ordinary stopped notifications for the configured period | Whole 24-hour days | Collector | Integer from 1 through 106751; measured from `stop_dt`; only rows strictly older than the cutoff are eligible; DLQ is retained indefinitely | Startup; cutoff is fixed once for the invocation | [YAML](../notifier/configs/config.yaml), [loader](../notifier/internal/config/config.go), [Collector wiring](../notifier/internal/application/collector.go), [cleanup query](../notifier/internal/storage/postgres/notification_collection.go) |
| `notification.lock_duration_ms` | Reserve claimed rows until the persisted lease expires | Milliseconds | Worker | Positive int32 and at least `worker.cycle_timeout_ms`; expired or replaced claims cannot save results | Startup; each new claim stores its lease | [YAML](../notifier/configs/config.yaml), [claim query](../notifier/internal/storage/postgres/queries.go), [result write](../notifier/internal/storage/postgres/worker.go) |
| `notification.initial_check_delay_sec` | Schedule the first check after notification creation | Whole seconds | API | Integer from zero through `MaxInt64 / time.Second`; no fractional values; hours are allowed; first date is not clamped to deadline | Startup; only notifications subsequently created | [YAML](../notifier/configs/config.yaml), [loader](../notifier/internal/config/config.go), [creation](../notifier/internal/core/notification.go) |
| `notification.scheduled_time_sec` | Schedule another condition check while the notification is active | Seconds | Worker | Positive integer; next recheck is capped at the notification deadline | Startup; each subsequently saved recheck | [YAML](../notifier/configs/config.yaml), [processor](../notifier/internal/worker/notification_processor.go) |
| Technical retry policy | Retry processing failures before a callback | Policy / stored scheduling time | Worker | No independent setting in this baseline: load/target failures leave the claim to expire; an indeterminate Information Service check uses the ordinary recheck path; callback attempt count is unchanged | Each processing attempt; task 04 must replace this row with its implemented settings | [Processor](../notifier/internal/worker/notification_processor.go), [condition processor](../notifier/internal/worker/condition_processor.go) |
| Callback retry/backoff policy | Schedule failed deliveries and stop after the attempt budget | Failed attempts / duration | Worker | Code-defined schedule; distinct from first check and condition recheck; exhausted attempts produce `delivery_failed`, not DLQ | After each attempted callback; stored retry dates are not recalculated automatically | [AfterDelivery](../notifier/internal/core/notification_delivery.go) |
| Background shutdown budget | Bound process exit and pool closure after stop/failure | Duration | Worker, Collector | Fixed 10-second exit deadline; no YAML key; worker stops claims and drains within existing cycle deadlines; Collector cancels its current operation | Each shutdown | [CLI watchdog](../notifier/cmd/notifier/main.go), [worker lifecycle](../notifier/internal/application/worker.go), [Collector lifecycle](../notifier/internal/application/collector.go); Compose stop grace is in [Compose](../docker-compose.yaml) |
| `database.min_conns` | Set the pool minimum for this process | Connections | API, worker, Collector | int32 from zero through `database.max_conns` | Startup; separate pool per process | [YAML](../notifier/configs/config.yaml), [pool wiring](../notifier/internal/application/database.go) |
| `database.max_conns` | Set the pool maximum for this process | Connections | API, worker, Collector | Positive int32; YAML values override pool options from the DSN | Startup; separate pool per process | [YAML](../notifier/configs/config.yaml), [pool wiring](../notifier/internal/application/database.go) |
| `database.operation_timeout_sec` | Bound one runtime SQL operation, including pool acquisition | Seconds | API, worker, Collector | Positive int32; an earlier parent deadline wins; also supplies the worker's desired result-write reserve | Startup | [YAML](../notifier/configs/config.yaml), [store](../notifier/internal/storage/postgres/store.go), [worker wiring](../notifier/internal/application/worker.go), [Collector wiring](../notifier/internal/application/collector.go) |
| `infoservice.base_url` | Select the source of time and price data | Absolute URL | Worker | HTTP or HTTPS with a host; redirects are not followed | Startup | [YAML](../notifier/configs/config.yaml), [loader](../notifier/internal/config/config.go), [IS client](../notifier/internal/interactions/infoservice/client.go) |
| `infoservice.time_timeout_ms` | Bound each time request | Milliseconds | Worker | Positive int32; cycle deadline can expire earlier | Startup; each outbound time call | [YAML](../notifier/configs/config.yaml), [IS client](../notifier/internal/interactions/infoservice/client.go) |
| `infoservice.price_timeout_ms` | Bound each price request | Milliseconds | Worker | Positive int32; cycle deadline can expire earlier | Startup; each outbound price call | [YAML](../notifier/configs/config.yaml), [IS client](../notifier/internal/interactions/infoservice/client.go) |
| `t_client.callback_url` | Select the client's callback destination | URL | Worker | Database requires text length 1–128; use a reachable HTTP(S) endpoint; invalid destinations fail delivery; redirects count as failed attempts | Read afresh before each attempted delivery | [Client schema](../notifier/db/migrations/001__t_client.sql), [target lookup](../notifier/internal/storage/postgres/worker.go), [callback sender](../notifier/internal/interactions/callback/client.go) |
| `t_client.callback_timeout_ms` | Override the default response budget for this client | Milliseconds | Worker | Nullable positive PostgreSQL integer; NULL selects the code default; remaining cycle budget can shorten it to reserve time for saving the result | Read afresh before each attempted delivery | [Client schema](../notifier/db/migrations/001__t_client.sql), [deliver](../notifier/internal/worker/notification_processor.go) |
| `t_client.max_lifetime_sec` | Limit the permitted deadline and supply one when omitted | Seconds | API | Positive PostgreSQL integer; requested deadline must be after creation and within the client's maximum lifetime | Read during API client lookup; affects new notification validation, not existing deadlines | [Client schema](../notifier/db/migrations/001__t_client.sql), [creation](../notifier/internal/core/notification.go) |
| HTTP JSON body limit | Bound API/emulator request decoding and IS response decoding | Bytes | API, worker, emulator | Fixed 32 KiB cap, including whitespace and escaping; oversized requests receive `413`; not a YAML key | Every relevant HTTP operation | [Shared limit](../internal/bodylimit/bodylimit.go), [IS transport](../notifier/internal/interactions/infoservice/response_limit.go) |
| Probe addresses and paths | Separate liveness/readiness from business traffic | Loopback port / path | API, worker, emulator | Fixed ports in application wiring; GET `/health/live` and `/health/ready`; no public port forwarding | Startup; each readiness call checks SQL for API/worker; emulator has no DB check | [API](../notifier/internal/application/api.go), [worker](../notifier/internal/application/worker.go), [emulator](../emulator/internal/application/emulator.go), [probe handler](../internal/health/handler.go) |
| Probe request budgets | Bound internal probe work | Duration | API, worker, emulator | API uses its request/write budgets; worker SQL uses the store timeout; every probe listener has a code-defined header-read timeout | Server creation; each probe call | [API](../notifier/internal/application/api.go), [worker](../notifier/internal/application/worker.go), [probe server](../internal/health/server.go) |
| Emulator `-listen-addr` | Bind the emulator business listener | Host:port | Emulator | Separate binary and CLI; no Notifier YAML is loaded | Startup | [Emulator CLI](../emulator/cmd/emulator/main.go); [Compose](../docker-compose.yaml) supplies the network bind address |
| Emulator read/write/idle budgets | Bound HTTP reads and response writes while allowing deliberate callback delay | Duration | Emulator | Code-defined; write budget starts at first response write and allows the full callback-delay contract before then | Server creation; each response | [Emulator server](../emulator/internal/application/emulator.go), [response timeout](../emulator/internal/application/response_timeout.go) |

## Budget and scheduling relationships

The API processing budget includes admission waiting and database work. A database operation
inherits that deadline even when its own configured timeout is longer. Response writing has
its own budget, so a timeout response still has a bounded chance to reach the caller. Header
reading and keep-alive waiting are separate phases. The API budget does not govern a worker
cycle or a client's callback timeout.

Each worker loop can process a bounded number of notifications concurrently. A successful
batch can be followed immediately by another claim; the cycle timeout is not a fixed polling
sleep. Empty or failed claims wait for the current cycle deadline. The lease must cover the
cycle, but it does not renew itself or prove that an already-started HTTP request stopped.
Result writes check both the claim identity and its unexpired lease.

First-check delay, condition recheck and callback backoff have different effects. Changing the
first-check delay affects only future creation; it does not move stored `next_scheduled_dt`
values, pause API writes or stop the worker from consuming existing due rows. Callback target
and timeout are read for each delivery, so client configuration changes can affect a retry.
Callback delivery and its result write are not atomic; a received callback can be repeated
when saving the result fails.

## Database configuration and baseline

Use the selected mode's DSN file; never put a DSN in the shared YAML or substitute another
mode's credentials. Compose mounts only the required file into each service, and the build
excludes DSN files. The local role definitions are in `notifier/db/init`; this document does
not reproduce credentials.

Runtime pool and SQL timeout fields apply to API, worker and Collector. Each Collector
invocation opens its own pool with its selected Collector DSN. The `migrate` mode uses its own selected
DSN through `database/sql` and goose; the runtime pgx pool settings and store operation timeout
do not configure goose.

The current local migration baseline creates the final notification schema directly in
[002__t_notification.sql](../notifier/db/migrations/002__t_notification.sql). Existing volumes
that recorded an older meaning of the same migration numbers are not upgraded by editing
those files. Use a new isolated Compose project for a fresh local baseline; do not infer
permission to reset an existing volume. Follow the [migration rules](../ai/rules/migrations.mdc)
for changes to an already-applied environment.

## Pending task 04 reconciliation

The technical-retry row still requires reconciliation with task 04's implementation:

- Replace it with the actual independent settings, validation and application time.
- Verify the configuration against that runtime; do not infer parameter names or DLQ fields
  from the plan alone.
