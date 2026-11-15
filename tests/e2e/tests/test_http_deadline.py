import signal
import threading
import time
from collections.abc import Iterator
from contextlib import contextmanager
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from types import FrameType

import pytest
from requests import RequestException, Session

from notifier_e2e.api.http_client import HttpClient
from notifier_e2e.support.deadline import request_deadline


@contextmanager
def slow_server() -> Iterator[tuple[str, list[str]]]:
    stopped = threading.Event()
    requests: list[str] = []

    class Handler(BaseHTTPRequestHandler):
        def do_PUT(self) -> None:
            requests.append(self.path)
            if self.path == "/body":
                self.send_response(200)
                self.send_header("Content-Length", "1000")
                self.end_headers()
                data = b"x" * 1000
            else:
                data = b"HTTP/1.1 200 OK\r\nContent-Length: 0\r\n\r\n"
            try:
                for byte in data:
                    self.wfile.write(bytes([byte]))
                    self.wfile.flush()
                    if stopped.wait(0.02):
                        break
            except OSError:
                pass

        def log_message(self, _format: str, *args: object) -> None:
            pass

    server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    thread = threading.Thread(
        target=server.serve_forever, kwargs={"poll_interval": 0.01}, daemon=True
    )
    thread.start()
    try:
        yield f"http://127.0.0.1:{server.server_port}", requests
    finally:
        stopped.set()
        server.shutdown()
        server.server_close()
        thread.join(timeout=1)
        assert not thread.is_alive()


@pytest.mark.skipif(not hasattr(signal, "setitimer"), reason="POSIX timer required")
@pytest.mark.parametrize("path", ["/headers", "/body"])
def test_http_deadline_bounds_slow_response_without_retry(path: str) -> None:
    with slow_server() as (base_url, requests), Session() as session:
        session.trust_env = False
        client = HttpClient(session, base_url)
        previous_handler = signal.getsignal(signal.SIGALRM)
        started = time.monotonic()
        with pytest.raises(RequestException):
            client.request("PUT", path, timeout_seconds=0.12)
        elapsed = time.monotonic() - started
        assert 0.08 <= elapsed < 0.4
        assert requests == [path]
        assert signal.getitimer(signal.ITIMER_REAL) == (0.0, 0.0)
        assert signal.getsignal(signal.SIGALRM) is previous_handler


@pytest.mark.skipif(not hasattr(signal, "setitimer"), reason="POSIX timer required")
@pytest.mark.parametrize("fail", [False, True])
def test_request_deadline_restores_handler_and_disarms_timer(fail: bool) -> None:
    def previous_handler(_signum: int, _frame: FrameType | None) -> None:
        pass

    original_handler = signal.signal(signal.SIGALRM, previous_handler)
    try:
        try:
            with request_deadline(1):
                assert signal.getitimer(signal.ITIMER_REAL)[0] > 0
                if fail:
                    raise LookupError("test failure")
        except LookupError:
            assert fail
        assert signal.getitimer(signal.ITIMER_REAL) == (0.0, 0.0)
        assert signal.getsignal(signal.SIGALRM) is previous_handler
    finally:
        signal.signal(signal.SIGALRM, original_handler)


@pytest.mark.skipif(not hasattr(signal, "setitimer"), reason="POSIX timer required")
def test_request_deadline_does_not_replace_active_timer() -> None:
    previous_handler = signal.getsignal(signal.SIGALRM)
    signal.setitimer(signal.ITIMER_REAL, 10, 0.4)
    try:
        with pytest.raises(RuntimeError, match="active interval timer"):
            with request_deadline(1):
                pytest.fail("The protected request must not run")
        remaining, interval = signal.getitimer(signal.ITIMER_REAL)
        assert 9 < remaining <= 10
        assert interval == 0.4
        assert signal.getsignal(signal.SIGALRM) is previous_handler
    finally:
        signal.setitimer(signal.ITIMER_REAL, 0)


@pytest.mark.parametrize("timeout_seconds", [0, -1, float("inf"), float("nan")])
def test_request_deadline_rejects_invalid_budget(timeout_seconds: float) -> None:
    with pytest.raises(ValueError, match="finite and positive"):
        with request_deadline(timeout_seconds):
            pytest.fail("The invalid request must not run")


def test_request_deadline_requires_main_thread() -> None:
    errors: list[Exception] = []

    def request() -> None:
        try:
            with request_deadline(1):
                pass
        except Exception as error:
            errors.append(error)

    thread = threading.Thread(target=request)
    thread.start()
    thread.join(timeout=1)
    assert not thread.is_alive()
    assert len(errors) == 1
    assert isinstance(errors[0], RuntimeError)
    assert "main thread" in str(errors[0])


def test_request_deadline_requires_posix_timer(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.delattr(signal, "setitimer", raising=False)
    with pytest.raises(RuntimeError, match="POSIX interval timers"):
        with request_deadline(1):
            pytest.fail("The unsupported request must not run")


def test_unbounded_request_does_not_require_timer(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.delattr(signal, "setitimer", raising=False)
    with request_deadline(None):
        pass
