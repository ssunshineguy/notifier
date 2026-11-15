import subprocess
from contextlib import ExitStack
from pathlib import Path

from notifier_e2e import config
from notifier_e2e.api.tcp_connection import APIConnection


def test_connection_limit_keeps_health_available_and_releases_capacity(
    endpoints: config.ComposeEndpoints,
) -> None:
    assert config.MAX_CONNECTIONS > 0
    with ExitStack() as cleanup:
        connections: list[APIConnection] = []
        for _ in range(config.MAX_CONNECTIONS):
            connection = APIConnection(endpoints.api_url)
            cleanup.callback(connection.close)
            connection.send_missing_request()
            connection.read_missing_response()
            connections.append(connection)

        overflow = APIConnection(endpoints.api_url)
        cleanup.callback(overflow.close)
        overflow.send_missing_request()
        overflow.assert_waiting_for_accept(config.API_REQUEST_TIMEOUT_SECONDS + 0.1)

        checkout = Path(__file__).resolve().parents[3]
        for path in ("/health/live", "/health/ready"):
            result = subprocess.run(
                [
                    "docker", "compose", "exec", "-T", "api", "curl",
                    "--noproxy", "*", "--fail", "--silent", "--show-error",
                    "--max-time", "2", "--output", "/dev/null", "--write-out", "%{http_code}",
                    f"http://127.0.0.1:9080{path}",
                ],
                cwd=checkout, capture_output=True, text=True, timeout=5, check=False,
            )
            assert result.returncode == 0 and result.stdout == "200", (
                f"Probe {path} failed at full connection capacity: "
                f"exit={result.returncode} status={result.stdout!r}"
            )

        connections[0].close()
        overflow.read_missing_response()
