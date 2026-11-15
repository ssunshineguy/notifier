# Initial API connection cap

Set `api.max_connections` to **1024** in the shipped configuration and loader default.
Both 512 and 1024 served all 32 new HTTP/1.1 clients while 90% of their connection slots
were occupied by idle keep-alive clients. The selected cap provides additional connection
headroom at a higher measured memory cost; it is not the smallest passing candidate or
a measured production capacity guarantee.

The cap applies to accepted public connections through `netutil.LimitListener`.
Active and idle connections both count. The independent health listener is excluded.
At capacity, new connections wait before acceptance instead of receiving an HTTP
`429` or `503`; this wait precedes the API processing deadline of 300 ms.

## Candidate comparison on 2026-11-15

[Raw results](connection-limits-2026-11-15.json) were produced by
[connection_limits.py](connection_limits.py) on the isolated `notifier_5` Compose stack.
This comparison used the working tree based on `cb8059c`, before integrating `main`
at `2170369`. The selected cap was revalidated on the updated base below.
The API image used Go 1.27. The study temporarily set an API resource envelope of
**1 CPU / 256 MiB**; this is a comparison condition, not an approved production sizing.
PostgreSQL, the worker and emulator remained running normally.

Each candidate had two phases, each with a fresh API process:

1. Baseline: 32 new persistent clients, no deliberately held idle connections.
2. Pressure: `floor(0.9 * cap)` idle connections, then 32 new persistent clients.

Each idle connection first completed and fully read a valid GET response, so the
count represents accepted connections rather than successful TCP handshakes in the
kernel backlog. The GET passed through client authentication and read a real fixture
notification from PostgreSQL. A false price condition kept the fixture active.

The workload targeted 100 requests/second for 10 seconds with a two-second client
HTTP timeout. All client connections stayed open through the phase, including clients
whose request timed out. There were no transport retries. The fixed per-client schedule
produced 1,024 successful requests in each passing phase (about 102.3 requests/second).
Only successful responses contribute to latency percentiles; failed clients are shown
separately. Both health endpoints returned 200 in every phase while clients were open.

| Cap | Held idle | Clients served / 32 | Transport timeouts | Pressure GET 200 | Successes/s | Baseline / pressure p95, ms | RSS after idle, MiB | RSS with load clients open, MiB |
| ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 64 | 57 | 7 | 25 | 224 | 22.39 | 12.41 / 8.48 | 19.30 | 20.18 |
| 256 | 230 | 26 | 6 | 832 | 83.15 | 15.34 / 15.02 | 22.62 | 27.09 |
| 512 | 460 | 32 | 0 | 1,024 | 102.34 | 13.67 / 14.46 | 28.95 | 35.33 |
| **1024** | **921** | **32** | **0** | **1,024** | **102.33** | **16.87 / 17.33** | **39.46** | **49.88** |

All baseline requests returned 200. Pressure phases had no HTTP error statuses;
failures at 64 and 256 were client transport timeouts while waiting for acceptance.
For 512, idle RSS rose from 17.11 to 28.95 MiB, and pressure cgroup memory was 36.62 MiB.
Process descriptors were 472 after opening 460 idle clients and 508 with the 32 load
clients open; these totals also include database sockets and other process descriptors.

## Selected cap: 1024

In the candidate comparison, a cap of 512 was the smallest tested passing candidate. The selected value of 1024
leaves 103 slots at the tested 90% idle occupancy, versus 52 for 512. It served all 32
new clients with no transport errors and a pressure p95 of 17.33 ms, close to its
16.87 ms baseline. RSS with load clients open was 49.88 MiB rather than 35.33 MiB for
512. This trades additional memory for connection headroom; the test showed no
throughput improvement from increasing the cap.
The chosen workload deliberately includes a 32-client burst, corresponding to the
current 16 in-flight plus 16 queued requests; connection capacity remains a separate
setting and is not defined as their sum.

The 90% idle occupancy and resource envelope are explicit test assumptions. This was
one short run per phase, without TLS, and does not establish peak RSS, maximum RPS,
long-term stability, or suitability for different client pool sizes and production
memory limits. Cgroup memory includes more than Go heap, and RSS snapshots are not
per-connection allocation guarantees. Revisit the cap with production traffic and
TLS/proxy topology. Even at 1024, idle clients can occupy every slot; the cap protects
resources, not fairness or a complete network round-trip deadline.

## Revalidation after integrating current main

[Raw revalidation results](connection-limits-main-2026-11-15.json) were measured on
2026-11-15 with this change applied to `main` at `2170369`. This was a fresh two-phase
run for 1024 only, using the same workload and 1 CPU / 256 MiB envelope; the comparison
table above retains the original measurements.

With 921 accepted idle connections, all 32 new clients completed their requests:
1,024 GET responses returned 200, with no HTTP or transport errors, at
102.30 successful requests/second. Baseline / pressure p95 was
14.41 / 13.37 ms. RSS was 39.61 MiB after opening the idle connections
and 50.11 MiB with the load clients still open; cgroup memory at that point was
52.88 MiB. Process descriptors were 933 after idle connections and 969 with load
clients open. Both health endpoints returned 200 in both phases.

This confirms the selected cap for this workload on the integrated code. The short-run
and production-sizing limitations above still apply.

## Reproduce

Install the existing e2e project in a Python environment and run from this checkout:

```sh
COMPOSE_PROJECT_NAME=notifier_5 make up
COMPOSE_PROJECT_NAME=notifier_5 python tests/load/connection_limits.py \
  --output tests/load/connection-limits-2026-11-15.json
COMPOSE_PROJECT_NAME=notifier_5 python tests/load/connection_limits.py \
  --candidates 1024 --output tests/load/connection-limits-main-2026-11-15.json
```

The runner requires macOS or Linux, Docker Compose and the existing e2e dependencies.
It accepts only the `notifier_5` project. It changes only that project's API during
the study, restores the ordinary API configuration afterward and removes its own
fixtures and temporary local configurations. It does not stop other projects or
remove volumes. The result path is overwritten when the command is repeated.

The Go TCP tests cover active and idle slot accounting, releasing capacity after close
and idle expiry, and shutdown while acceptance is blocked. The Compose e2e scenario
fills the shipped cap, confirms that both probes remain available, and checks that a
pending request proceeds after one idle connection closes.
