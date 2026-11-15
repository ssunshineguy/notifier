from collections.abc import Iterator
from uuid import uuid4

from requests import RequestException, Response, Session
from urllib3.util import Timeout

from notifier_e2e import config
from notifier_e2e.support.deadline import request_deadline
from notifier_e2e.support.diagnostics import transport_details


class HttpRequestError(RequestException):
    pass


class HttpClient:
    def __init__(self, session: Session, base_url: str) -> None:
        self._session = session
        self._base_url = base_url.rstrip("/")

    def request(
        self,
        method: str,
        path: str,
        *,
        client_id: str | None = None,
        payload: dict[str, object] | None = None,
        raw_body: bytes | Iterator[bytes] | None = None,
        timeout_seconds: float | None = None,
    ) -> Response:
        if payload is not None and raw_body is not None:
            raise ValueError("Choose either a JSON payload or a raw body")
        headers = {"X-Request-ID": f"e2e-{uuid4().hex}"}
        if raw_body is not None:
            headers["Content-Type"] = "application/json"
        if client_id is not None:
            headers["X-Client-ID"] = client_id
        timeout: float | Timeout = config.TIMEOUT_SECONDS
        if timeout_seconds is not None:
            if timeout_seconds <= 0:
                raise ValueError("HTTP request budget must be positive")
            timeout = Timeout(
                total=timeout_seconds,
                connect=min(config.TIMEOUT_SECONDS, timeout_seconds),
                read=min(config.TIMEOUT_SECONDS, timeout_seconds),
            )
        try:
            with request_deadline(timeout_seconds):
                return self._session.request(
                    method,
                    self._base_url + path,
                    headers=headers,
                    json=payload,
                    data=raw_body,
                    timeout=timeout,
                    allow_redirects=False,
                )
        except RequestException as error:
            raise HttpRequestError(transport_details(
                method, path, headers["X-Request-ID"], type(error).__name__
            )) from None
