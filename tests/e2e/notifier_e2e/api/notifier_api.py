from http import HTTPStatus
from uuid import UUID

from requests import Response, Session

from notifier_e2e.support.matchers import assert_response

from .http_client import HttpClient

NOTIFICATIONS_PATH = "/v1/notifications"
JSON_CONTENT_TYPE = "application/json"


class NotifierApiClient:
    def __init__(self, session: Session, client_id: str, *, base_url: str) -> None:
        self._http = HttpClient(session, base_url)
        self._client_id = client_id

    def create_notification(
        self, external_id: str, notification_params: list[dict[str, object]]
    ) -> Response:
        response = self._http.request(
            "POST",
            NOTIFICATIONS_PATH,
            client_id=self._client_id,
            payload={
                "external_id": external_id,
                "notification_params": notification_params,
            },
        )
        assert_response(response, HTTPStatus.OK, content_type=JSON_CONTENT_TYPE)
        return response

    def get_notification(
        self, notification_id: UUID, *, expected_status: HTTPStatus = HTTPStatus.OK
    ) -> Response:
        response = self._http.request(
            "GET", f"{NOTIFICATIONS_PATH}/{notification_id}", client_id=self._client_id
        )
        content_type = (
            JSON_CONTENT_TYPE
            if expected_status == HTTPStatus.OK
            else "application/problem+json"
        )
        assert_response(response, expected_status, content_type=content_type)
        if expected_status == HTTPStatus.NOT_FOUND:
            problem_type = response.json()["type"]
            assert problem_type == "/problems/not-found", (
                f"Unexpected problem type {problem_type!r}"
            )
        return response

    def update_notification(
        self, notification_id: UUID, notification_params: list[dict[str, object]]
    ) -> Response:
        response = self._http.request(
            "PUT",
            f"{NOTIFICATIONS_PATH}/{notification_id}",
            client_id=self._client_id,
            payload={"notification_params": notification_params},
        )
        assert_response(response, HTTPStatus.OK, content_type=JSON_CONTENT_TYPE)
        return response

    def delete_notification(self, notification_id: UUID) -> Response:
        response = self._http.request(
            "DELETE",
            f"{NOTIFICATIONS_PATH}/{notification_id}",
            client_id=self._client_id,
        )
        assert_response(response, HTTPStatus.OK)
        assert response.content == b""
        return response
