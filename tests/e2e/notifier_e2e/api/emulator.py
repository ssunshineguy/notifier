from typing import Literal
from urllib.parse import quote

from requests import Response, Session

from notifier_e2e import config

from .http_client import HttpClient


CLIENT_OVERRIDES_PATH = "/v1/client-overrides"


class EmulatorClient:
    def __init__(self, session: Session) -> None:
        self._http = HttpClient(session, config.EMULATOR_URL)

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

    def reset_client_overrides(self, client_id: str) -> Response:
        return self._http.request(
            "DELETE", f"{CLIENT_OVERRIDES_PATH}/{quote(client_id, safe='')}"
        )
