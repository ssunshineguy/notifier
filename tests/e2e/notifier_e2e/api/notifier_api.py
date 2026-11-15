from collections.abc import Iterator
from http import HTTPStatus
from uuid import UUID

from requests import Response, Session

from notifier_e2e.support.diagnostics import media_type, response_details
from notifier_e2e.support.matchers import (
    assert_created_response,
    assert_notification_response,
    assert_problem_response,
    assert_response,
)

from .http_client import HttpClient

NOTIFICATIONS_PATH = "/v1/notifications"
JSON_CONTENT_TYPE = "application/json"
PROBLEM_CONTENT_TYPE = "application/problem+json"
IN_PROGRESS_TYPE = "/problems/notification-in-progress"
DUPLICATE_DISAPPEARED_TYPE = "/problems/notification-duplicate-disappeared"


class NotifierApiClient:
    def __init__(self, session: Session, client_id: str, *, base_url: str) -> None:
        self._http = HttpClient(session, base_url)
        self._client_id = client_id

    def create_notification(
        self, external_id: str, notification_params: list[dict[str, object]],
        *, expected_status: int = HTTPStatus.OK,
    ) -> Response:
        response = self._http.request(
            "POST", NOTIFICATIONS_PATH, client_id=self._client_id,
            payload={"external_id": external_id, "notification_params": notification_params},
        )
        if expected_status == HTTPStatus.OK or (
            expected_status == HTTPStatus.CONFLICT and media_type(response) == JSON_CONTENT_TYPE
        ):
            assert_response(response, expected_status, content_type=JSON_CONTENT_TYPE)
            assert_created_response(response)
        else:
            assert_response(response, expected_status, content_type=PROBLEM_CONTENT_TYPE)
            assert_problem_response(response, expected_type=(
                DUPLICATE_DISAPPEARED_TYPE if expected_status == HTTPStatus.CONFLICT else None
            ))
        return response

    def create_notification_raw(
        self, body: bytes | Iterator[bytes], *, expected_status: int = HTTPStatus.OK,
    ) -> Response:
        response = self._http.request(
            "POST", NOTIFICATIONS_PATH, client_id=self._client_id, raw_body=body,
        )
        if expected_status == HTTPStatus.OK:
            assert_response(response, expected_status, content_type=JSON_CONTENT_TYPE)
            assert_created_response(response)
        else:
            assert_response(response, expected_status, content_type=PROBLEM_CONTENT_TYPE)
            assert_problem_response(response, expected_type="/problems/content-too-large")
        return response

    def get_notification(
        self, notification_id: UUID, *, expected_status: int = HTTPStatus.OK
    ) -> Response:
        response = self._http.request(
            "GET", f"{NOTIFICATIONS_PATH}/{notification_id}", client_id=self._client_id
        )
        if expected_status == HTTPStatus.OK:
            assert_response(response, expected_status, content_type=JSON_CONTENT_TYPE)
            assert_notification_response(response)
        else:
            assert_response(response, expected_status, content_type=PROBLEM_CONTENT_TYPE)
            assert_problem_response(response, expected_type=(
                "/problems/not-found" if expected_status == HTTPStatus.NOT_FOUND else None
            ))
        return response

    def update_notification(
        self, notification_id: UUID, notification_params: list[dict[str, object]],
        *, expected_status: int = HTTPStatus.OK, allow_in_progress: bool = False,
        timeout_seconds: float | None = None,
    ) -> Response:
        timeout = {} if timeout_seconds is None else {"timeout_seconds": timeout_seconds}
        response = self._http.request(
            "PUT", f"{NOTIFICATIONS_PATH}/{notification_id}", client_id=self._client_id,
            payload={"notification_params": notification_params}, **timeout,
        )
        self._assert_mutation_response(response, expected_status, allow_in_progress, has_body=True)
        return response

    def delete_notification(
        self, notification_id: UUID, *, expected_status: int = HTTPStatus.OK,
        allow_in_progress: bool = False, timeout_seconds: float | None = None,
    ) -> Response:
        timeout = {} if timeout_seconds is None else {"timeout_seconds": timeout_seconds}
        response = self._http.request(
            "DELETE", f"{NOTIFICATIONS_PATH}/{notification_id}", client_id=self._client_id,
            **timeout,
        )
        self._assert_mutation_response(response, expected_status, allow_in_progress, has_body=False)
        return response

    @staticmethod
    def _assert_mutation_response(
        response: Response, expected_status: int, allow_in_progress: bool, *, has_body: bool
    ) -> None:
        if allow_in_progress and expected_status == HTTPStatus.OK and response.status_code == HTTPStatus.CONFLICT:
            assert_response(response, HTTPStatus.CONFLICT, content_type=PROBLEM_CONTENT_TYPE)
            assert_problem_response(response, expected_type=IN_PROGRESS_TYPE)
        elif expected_status == HTTPStatus.OK:
            assert_response(response, expected_status, content_type=JSON_CONTENT_TYPE if has_body else None)
            if has_body:
                assert_notification_response(response)
            else:
                empty = response.content == b""
                assert empty, f"DELETE response must be empty; {response_details(response)}"
        else:
            assert_response(response, expected_status, content_type=PROBLEM_CONTENT_TYPE)
            expected_type = {
                HTTPStatus.NOT_FOUND: "/problems/not-found",
                HTTPStatus.CONFLICT: IN_PROGRESS_TYPE,
            }.get(expected_status)
            assert_problem_response(response, expected_type=expected_type)
