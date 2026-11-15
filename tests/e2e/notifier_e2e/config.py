import subprocess
from dataclasses import dataclass
from pathlib import Path


CALLBACK_URL = "http://emulator:8081/callback"

TIMEOUT_SECONDS = 0.5
DATABASE_CONNECT_ARGS = {
    "connect_timeout": 2,  # Psycopg enforces a two-second minimum for connecting.
    "options": f"-c statement_timeout={int(TIMEOUT_SECONDS * 1000)}",
}


@dataclass(frozen=True)
class ComposeEndpoints:
    api_url: str
    emulator_url: str
    database_url: str


def compose_endpoints(checkout: Path) -> ComposeEndpoints:
    api = _published_address(checkout, "api", 8080)
    emulator = _published_address(checkout, "emulator", 8081)
    postgres = _published_address(checkout, "postgres", 5432)
    return ComposeEndpoints(
        api_url=f"http://{api}",
        emulator_url=f"http://{emulator}",
        database_url=f"postgresql+psycopg://notifier_e2e:notifier_e2e@{postgres}/notifier?sslmode=disable",
    )


def _published_address(checkout: Path, service: str, port: int) -> str:
    message = f"Cannot resolve {service}:{port} in {checkout}. Run make up in that checkout."
    try:
        address = subprocess.check_output(
            ["docker", "compose", "port", service, str(port)],
            cwd=checkout,
            text=True,
            stderr=subprocess.PIPE,
            timeout=5,
        ).strip()
    except (OSError, subprocess.SubprocessError) as error:
        raise RuntimeError(message) from error

    host, _, published_port = address.rpartition(":")
    if host != "127.0.0.1" or not published_port.isdecimal() or not 1 <= int(published_port) <= 65535:
        raise RuntimeError(message)
    return address
