import socket
from http import HTTPStatus
from http.client import HTTPResponse
from urllib.parse import urlsplit

from notifier_e2e import config


MISSING_PATH = "/e2e-connection-limit-missing-route"


class APIConnection:
    def __init__(self, base_url: str) -> None:
        address = urlsplit(base_url)
        assert address.scheme == "http" and address.hostname and address.port
        self._socket = socket.create_connection(
            (address.hostname, address.port), timeout=config.TIMEOUT_SECONDS
        )

    def close(self) -> None:
        self._socket.close()

    def send_missing_request(self) -> None:
        self._socket.sendall(
            f"GET {MISSING_PATH} HTTP/1.1\r\n"
            "Host: localhost\r\n"
            "X-Request-ID: e2e-connection-limit\r\n\r\n".encode("ascii")
        )

    def read_missing_response(self) -> None:
        with HTTPResponse(self._socket) as response:
            response.begin()
            assert response.status == HTTPStatus.NOT_FOUND, (
                f"Missing-route status={response.status}, expected404"
            )
            assert not response.will_close, "Response unexpectedly closed keep-alive"
            response.read()

    def assert_waiting_for_accept(self, timeout_seconds: float) -> None:
        self._socket.settimeout(timeout_seconds)
        try:
            try:
                self._socket.recv(1, socket.MSG_PEEK)
            except TimeoutError:
                return
            raise AssertionError("Overflow connection responded or closed while the API cap was full")
        finally:
            self._socket.settimeout(config.TIMEOUT_SECONDS)
