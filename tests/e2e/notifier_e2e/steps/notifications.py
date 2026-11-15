from http import HTTPStatus
from uuid import UUID

from sqlalchemy.engine import Engine

from notifier_e2e.api.notifier_api import NotifierApiClient
from notifier_e2e.db.notification import Notification


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
    assert actual == expected, f"Expected {expected!r}, got {actual!r}"


def update_notification(
    api: NotifierApiClient,
    database: Engine,
    *,
    original: dict[str, object],
    expected: Notification,
) -> None:
    updated = api.update_notification(expected.id, expected.data).json()
    expected_response = {**original, "notification_params": expected.data}
    assert updated == expected_response, (
        f"PUT returned {updated!r}, expected {expected_response!r}"
    )
    fetched = fetch_notification(api, expected.id)
    assert fetched == expected_response, (
        f"GET after PUT returned {fetched!r}, expected {expected_response!r}"
    )
    with database.connect() as connection:
        stored = Notification.get(connection, expected.client_id, expected.id)
    assert stored == expected, f"PostgreSQL returned {stored!r}, expected {expected!r}"


def delete_notification(
    api: NotifierApiClient,
    database: Engine,
    *,
    client_id: str,
    notification_id: UUID,
) -> None:
    api.delete_notification(notification_id)
    api.get_notification(notification_id, expected_status=HTTPStatus.NOT_FOUND)
    with database.connect() as connection:
        stored = Notification.get(connection, client_id, notification_id)
    assert stored is None, f"Notification {notification_id} still exists in PostgreSQL"
