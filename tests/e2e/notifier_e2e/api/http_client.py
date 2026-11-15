from uuid import uuid4

from requests import Response, Session

from notifier_e2e import config


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
    ) -> Response:
        headers = {"X-Request-ID": f"e2e-{uuid4().hex}"}
        if client_id is not None:
            headers["X-Client-ID"] = client_id
        return self._session.request(
            method,
            self._base_url + path,
            headers=headers,
            json=payload,
            timeout=config.TIMEOUT_SECONDS,
            allow_redirects=False,
        )
