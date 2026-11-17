import time
from collections.abc import Callable
from http import HTTPStatus
from uuid import UUID

from requests import RequestException, Response
from sqlalchemy.engine import Engine

from notifier_e2e.api.http_client import HttpRequestError
from notifier_e2e.api.notifier_api import NotifierApiClient
from notifier_e2e.db.notification import Notification
from notifier_e2e.support.diagnostics import bounded_details, response_details

MUTATION_WAIT_SECONDS = 5.0
MUTATION_RETRY_INTERVAL_SECONDS = 0.1
IN_PROGRESS_TYPE = "/problems/notification-in-progress"


def fetch_notification(
    api: NotifierApiClient, notification_id: UUID
) -> dict[str, object]:
    return api.get_notification(notification_id).json()


def assert_notification(
    notification: dict[str, object],
    *,
    notification_id: UUID,
    external_id: str,
    status: str,
    notification_params: list[dict[str, object]],
) -> None:
    expected = {
        "notification_id": str(notification_id),
        "external_id": external_id,
        "notification_status": status,
        "notification_params": notification_params,
    }
    actual = {field: notification.get(field) for field in expected}
    assert actual == expected
    if status == "active":
        assert "delivery_status" not in notification


def update_notification(
    api: NotifierApiClient,
    database: Engine,
    *,
    notification_params: list[dict[str, object]],
    expected_response: dict[str, object],
    expected: Notification,
) -> None:
    updated = wait_for_mutation(
        lambda remaining: api.update_notification(
            expected.id, notification_params, allow_in_progress=True, timeout_seconds=remaining
        )
    ).json()
    assert updated == expected_response
    fetched = fetch_notification(api, expected.id)
    assert fetched == expected_response
    with database.connect() as connection:
        stored = Notification.get(connection, expected.client_id, expected.id)
    assert stored == expected


def delete_notification(
    api: NotifierApiClient,
    database: Engine,
    *,
    client_id: str,
    notification_id: UUID,
) -> None:
    wait_for_mutation(
        lambda remaining: api.delete_notification(
            notification_id, allow_in_progress=True, timeout_seconds=remaining
        )
    )
    api.get_notification(notification_id, expected_status=HTTPStatus.NOT_FOUND)
    with database.connect() as connection:
        stored = Notification.get(connection, client_id, notification_id)
    assert stored is None, f"Notification {notification_id} still exists in PostgreSQL"


def wait_for_mutation(operation: Callable[[float], Response]) -> Response:
    started = time.monotonic()
    deadline = started + MUTATION_WAIT_SECONDS
    attempts = 0
    last_state = "no response"

    def failure(reason: str) -> AssertionError:
        elapsed = time.monotonic() - started
        message = (
            f"{reason}; attempts={attempts}; elapsed={elapsed:.3f}s; "
            f"last_state: {last_state}"
        )
        return AssertionError(bounded_details(message))

    while True:
        remaining = deadline - time.monotonic()
        if remaining <= 0:
            raise failure("Mutation wait exhausted its 5s budget")
        attempts += 1
        try:
            response = operation(remaining)
        except RequestException as error:
            last_state = (
                str(error) if isinstance(error, HttpRequestError)
                else f"transport error: {type(error).__name__}"
            )
            raise failure("Mutation request failed") from None
        except AssertionError as error:
            last_state = str(error)
            raise failure("Mutation response failed validation") from None
        last_state = response_details(response)
        if time.monotonic() >= deadline:
            raise failure("Mutation wait exhausted its 5s budget")
        if response.status_code == HTTPStatus.OK:
            return response
        try:
            problem = response.json()
        except (ValueError, RecursionError):
            problem = None
        if (
            response.status_code != HTTPStatus.CONFLICT
            or not isinstance(problem, dict)
            or problem.get("type") != IN_PROGRESS_TYPE
        ):
            raise failure("Mutation returned a non-retryable response")
        remaining = deadline - time.monotonic()
        if remaining > 0:
            time.sleep(min(MUTATION_RETRY_INTERVAL_SECONDS, remaining))
