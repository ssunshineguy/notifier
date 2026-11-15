from collections.abc import Iterator
from http import HTTPStatus
from typing import Literal
from urllib.parse import quote

from requests import Response, Session

from notifier_e2e.support.matchers import assert_problem_response, assert_response

from .http_client import HttpClient


CLIENT_OVERRIDES_PATH = "/v1/client-overrides"


class EmulatorClient:
    def __init__(self, session: Session, *, base_url: str) -> None:
        self._http = HttpClient(session, base_url)

    def get_time(self, client_id: str) -> Response:
        return self._http.request("GET", "/time", client_id=client_id)

    def get_price(self, client_id: str) -> Response:
        return self._http.request("GET", "/price", client_id=client_id)

    def set_client_override(
        self, client_id: str, parameter: Literal["time", "price"], value: str | int
    ) -> Response:
        return self._http.request(
            "POST",
            f"{CLIENT_OVERRIDES_PATH}/{quote(client_id, safe='')}",
            payload={"parameter": parameter, "value": value},
        )

    def set_client_override_raw(
        self, client_id: str, body: bytes | Iterator[bytes],
        *, expected_status: int = HTTPStatus.OK,
    ) -> Response:
        response = self._http.request(
            "POST", f"{CLIENT_OVERRIDES_PATH}/{quote(client_id, safe='')}", raw_body=body,
        )
        assert_response(
            response, expected_status,
            content_type="application/problem+json" if expected_status != HTTPStatus.OK else None,
        )
        if expected_status != HTTPStatus.OK:
            assert_problem_response(response, expected_type="/problems/content-too-large")
        else:
            assert response.content == b"", "Override success response must be empty"
        return response

    def reset_client_overrides(self, client_id: str) -> Response:
        return self._http.request(
            "DELETE", f"{CLIENT_OVERRIDES_PATH}/{quote(client_id, safe='')}"
        )
