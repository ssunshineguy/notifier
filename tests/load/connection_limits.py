"""Measure API connection limits on the isolated notifier_5 Compose stack."""

import argparse
from collections import Counter
from concurrent.futures import ThreadPoolExecutor
from datetime import datetime, timezone
from http.client import HTTPConnection, HTTPException
import json
import math
import os
from pathlib import Path
import re
import resource
import subprocess
from threading import Barrier
from time import monotonic, sleep
from urllib.parse import urlsplit
from uuid import uuid4

from requests import Session

from notifier_e2e import config
from notifier_e2e.api.emulator import EmulatorClient
from notifier_e2e.api.notifier_api import NotifierApiClient
from notifier_e2e.db.client import Client
from notifier_e2e.db.connection import create_test_engine
from notifier_e2e.db.notification import Notification

ROOT = Path(__file__).resolve().parents[2]
CLIENTS = 32
HTTP_TIMEOUT = 2.0


def compose(*arguments: str, override: Path | None = None) -> str:
    command = ["docker", "compose", "-f", str(ROOT / "docker-compose.yaml")]
    if override is not None:
        command += ["-f", str(override)]
    result = subprocess.run(
        command + list(arguments), cwd=ROOT, text=True, capture_output=True,
        timeout=90, check=False,
    )
    if result.returncode:
        raise RuntimeError("Compose command failed; inspect the isolated stack separately")
    return result.stdout.strip()


def metrics() -> dict[str, int]:
    values = compose("exec", "-T", "api", "sh", "-c", (
        "awk '/^VmRSS:/ {print $2}' /proc/1/status; "
        "set -- /proc/1/fd/*; printf '%s\\n' \"$#\"; "
        "cat /sys/fs/cgroup/memory.current"
    )).splitlines()
    return dict(zip(("process_rss_kib", "process_fds", "cgroup_memory_bytes"), map(int, values), strict=True))


def probes() -> dict[str, int]:
    return {
        name: int(compose(
            "exec", "-T", "api", "curl", "--silent", "--noproxy", "*",
            "--max-time", "2", "--output", "/dev/null", "--write-out", "%{http_code}",
            f"http://127.0.0.1:9080/health/{name}",
        )) for name in ("live", "ready")
    }


def connection() -> HTTPConnection:
    address = urlsplit(config.compose_endpoints(ROOT).api_url)
    return HTTPConnection(address.hostname, address.port, timeout=HTTP_TIMEOUT)


def get(conn: HTTPConnection, path: str, headers: dict[str, str]) -> int:
    conn.request("GET", path, headers=headers)
    response = conn.getresponse()
    response.read()
    return response.status


def quantiles(samples: list[float]) -> dict[str, float | None]:
    ordered = sorted(samples)
    return {
        name: round(ordered[min(len(ordered) - 1, math.ceil(len(ordered) * fraction) - 1)] * 1000, 3) if ordered else None
        for name, fraction in (("p50_ms", 0.5), ("p95_ms", 0.95), ("p99_ms", 0.99), ("max_ms", 1.0))
    }


def workload(path: str, headers: dict[str, str], duration: float, rps: float) -> dict[str, object]:
    address = urlsplit(config.compose_endpoints(ROOT).api_url)
    connections = [HTTPConnection(address.hostname, address.port, timeout=HTTP_TIMEOUT) for _ in range(CLIENTS)]
    barrier = Barrier(CLIENTS + 1)
    timing = {}

    def worker(conn: HTTPConnection) -> dict[str, object]:
        statuses, errors, latencies = Counter(), Counter(), []
        barrier.wait(timeout=10)
        due = timing["start"]
        while monotonic() < timing["end"]:
            started = monotonic()
            try:
                status = get(conn, path, headers)
                statuses[str(status)] += 1
                if status == 200:
                    latencies.append(monotonic() - started)
            except (OSError, HTTPException) as error:
                errors[type(error).__name__] += 1
                break
            due += CLIENTS / rps
            sleep(max(0, min(due, timing["end"]) - monotonic()))
        return {"statuses": statuses, "errors": errors, "latencies": latencies}

    try:
        with ThreadPoolExecutor(max_workers=CLIENTS) as executor:
            futures = [executor.submit(worker, conn) for conn in connections]
            timing["start"] = monotonic()
            timing["end"] = timing["start"] + duration
            barrier.wait(timeout=10)
            results = [future.result(timeout=duration + HTTP_TIMEOUT + 5) for future in futures]
        elapsed = monotonic() - timing["start"]
        statuses, errors, latencies = Counter(), Counter(), []
        for result in results:
            statuses.update(result["statuses"])
            errors.update(result["errors"])
            latencies.extend(result["latencies"])
        return {
            "elapsed_seconds": round(elapsed, 3), "statuses": dict(statuses),
            "transport_errors": dict(errors), "successful_clients": sum(bool(result["latencies"]) for result in results),
            "successful_requests_per_second": round(statuses["200"] / elapsed, 3),
            "success_latency": quantiles(latencies), "resources_with_clients_open": metrics(),
            "health_with_clients_open": probes(),
        }
    finally:
        for conn in connections:
            conn.close()


def write_config(source: str, destination: Path, limit: int) -> None:
    section = re.search(r"(?ms)^api:\n.*?(?=^[^\s#]|\Z)", source)
    if section is None:
        raise ValueError("Missing API configuration")
    changed, count = re.subn(r"(?m)^  max_connections: *\d+ *$", f"  max_connections: {limit}", section.group())
    if count != 1:
        raise ValueError("Expected one API max_connections setting")
    destination.write_text(source[:section.start()] + changed + source[section.end():])


def restart_api(override: Path | None) -> None:
    compose("up", "-d", "--no-deps", "--force-recreate", "--wait", "api", override=override)


def phase(path: str, headers: dict[str, str], idle_count: int, args: argparse.Namespace) -> dict[str, object]:
    sockets = []
    try:
        warm = connection()
        try:
            if get(warm, path, headers) != 200:
                raise RuntimeError("Fixture GET failed")
        finally:
            warm.close()
        before = metrics()
        address = urlsplit(config.compose_endpoints(ROOT).api_url)
        opening = monotonic()
        for _ in range(idle_count):
            conn = HTTPConnection(address.hostname, address.port, timeout=HTTP_TIMEOUT)
            sockets.append(conn)
            if get(conn, path, headers) != 200:
                raise RuntimeError("Idle connection did not complete its fixture GET")
        idle_seconds = monotonic() - opening
        if idle_seconds + args.duration + HTTP_TIMEOUT >= 55:
            raise RuntimeError("Phase could exceed the configured keep-alive idle budget")
        return {
            "idle_connections_confirmed_by_response": idle_count,
            "idle_open_seconds": round(idle_seconds, 3), "resources_before_idle": before,
            "resources_after_idle": metrics(), "load": workload(path, headers, args.duration, args.rps),
        }
    finally:
        for conn in sockets:
            conn.close()


def run(args: argparse.Namespace) -> None:
    tag = uuid4().hex
    generated = ROOT / f".connection-limit-{tag}.local.yaml"
    override = ROOT / f".connection-limit-compose-{tag}.local.yaml"
    source = (ROOT / "notifier/configs/config.yaml").read_text()
    endpoints = config.compose_endpoints(ROOT)
    database = create_test_engine(endpoints.database_url)
    client = Client(f"load-{tag}", config.CALLBACK_URL, 3600)
    result = {
        "started_utc": datetime.now(timezone.utc).isoformat(), "compose_project": "notifier_5",
        "api_memory_limit_mib": args.memory_mib, "api_cpu_limit": args.cpus,
        "clients": CLIENTS, "target_requests_per_second": args.rps,
        "duration_seconds": args.duration, "http_timeout_seconds": HTTP_TIMEOUT,
        "profile": "HTTP/1.1 GET 200 with client lookup and notification query; idle sockets complete a GET first",
        "candidates": [],
    }
    restore_needed = False
    with Session() as session:
        emulator = EmulatorClient(session, base_url=endpoints.emulator_url)
        try:
            with database.begin() as db:
                client.insert(db)
            emulator.set_client_override(client.client_id, "price", 100).raise_for_status()
            api = NotifierApiClient(session, client.client_id, base_url=endpoints.api_url)
            created = api.create_notification(f"load-{tag}", [{"type": "price", "operation": "greater_than", "value": 100}])
            path = "/v1/notifications/" + created.json()["notification_id"]
            headers = {"X-Client-ID": client.client_id, "X-Request-ID": "connection-limit-load"}
            override.write_text(json.dumps({"services": {"api": {
                "volumes": [{"type": "bind", "source": str(generated), "target": "/app/run/notifier/configs/config.yaml", "read_only": True}],
                "mem_limit": f"{args.memory_mib}m", "cpus": args.cpus,
            }}}))
            for limit in args.candidates:
                write_config(source, generated, limit)
                restore_needed = True
                restart_api(override)
                baseline = phase(path, headers, 0, args)
                restart_api(override)
                occupied = phase(path, headers, int(limit * 0.9), args)
                result["candidates"].append({"max_connections": limit, "baseline": baseline, "ninety_percent_idle": occupied})
                args.output.parent.mkdir(parents=True, exist_ok=True)
                args.output.write_text(json.dumps(result, indent=2) + "\n")
                print(json.dumps({"candidate_complete": limit}), flush=True)
        finally:
            try:
                if restore_needed:
                    restart_api(None)
            finally:
                try:
                    emulator.reset_client_overrides(client.client_id).raise_for_status()
                finally:
                    try:
                        with database.begin() as db:
                            Notification.delete_for_client(db, client.client_id)
                            client.delete(db)
                    finally:
                        database.dispose()
                        generated.unlink(missing_ok=True)
                        override.unlink(missing_ok=True)


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--candidates", type=int, nargs="+", default=[64, 256, 512, 1024])
    parser.add_argument("--duration", type=float, default=10)
    parser.add_argument("--rps", type=float, default=100)
    parser.add_argument("--memory-mib", type=int, default=256)
    parser.add_argument("--cpus", type=float, default=1)
    args = parser.parse_args()
    if Path.cwd().resolve() != ROOT or os.environ.get("COMPOSE_PROJECT_NAME") != "notifier_5":
        parser.error("Run from the checkout root with COMPOSE_PROJECT_NAME=notifier_5")
    if not all(32 <= value <= 4096 for value in args.candidates) or not 1 <= args.duration <= 30 or not 1 <= args.rps <= 500:
        parser.error("Candidates must be 32..4096, duration 1..30s, and rate 1..500rps")
    if not 64 <= args.memory_mib <= 1024 or not 0.5 <= args.cpus <= 2:
        parser.error("API resource envelope must be 64..1024MiB and 0.5..2 CPUs")
    soft, hard = resource.getrlimit(resource.RLIMIT_NOFILE)
    needed = max(args.candidates) + CLIENTS + 64
    if soft < needed:
        if hard != resource.RLIM_INFINITY and hard < needed:
            parser.error("Insufficient descriptor limit for the selected candidates")
        resource.setrlimit(resource.RLIMIT_NOFILE, (min(8192, needed), hard))
    try:
        run(args)
    except Exception as error:
        raise SystemExit(f"Connection load measurement failed ({type(error).__name__}); no sensitive error details printed") from None


if __name__ == "__main__":
    main()
