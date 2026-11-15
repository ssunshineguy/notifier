from collections.abc import Callable
from http import HTTPStatus
from uuid import UUID, uuid4

import pytest
from sqlalchemy.engine import Engine

from notifier_e2e.api.emulator import EmulatorClient
from notifier_e2e.api.notifier_api import NotifierApiClient
from notifier_e2e.db.client import Client
from notifier_e2e.db.notification import Notification
from notifier_e2e.steps.notifications import assert_notification, fetch_notification


@pytest.mark.parametrize("operation", ["GET", "PUT", "DELETE"])
def test_foreign_notification_is_hidden_and_unchanged(
    operation: str,
    client_factory: Callable[[], Client],
    notifier_api_factory: Callable[[Client], NotifierApiClient],
    emulator_api: EmulatorClient,
    database: Engine,
) -> None:
    owner = client_factory()
    stranger = client_factory()
    owner_api = notifier_api_factory(owner)
    stranger_api = notifier_api_factory(stranger)
    external_id = f"e2e-{uuid4().hex}"
    price = 100
    emulator_api.set_client_override(owner.client_id, "price", price).raise_for_status()
    assert emulator_api.get_price(owner.client_id).json()["price"] == price
    params = [{"type": "price", "operation": "greater_than", "value": price}]
    replacement_params = [{"type": "price", "operation": "greater_than", "value": price + 1}]

    created = owner_api.create_notification(external_id, params)
    notification_id = UUID(created.json()["notification_id"])
    original = fetch_notification(owner_api, notification_id)
    assert_notification(
        original,
        notification_id=notification_id,
        external_id=external_id,
        status="active",
        notification_params=params,
    )
    with database.connect() as connection:
        original_state = Notification.get_state(connection, owner.client_id, notification_id)
    assert original_state is not None
    assert original_state["data"] == params
    assert original_state["notification_status"] == "active"
    assert original_state["delivery_status"] is None

    if operation == "GET":
        stranger_api.get_notification(notification_id, expected_status=HTTPStatus.NOT_FOUND)
    elif operation == "PUT":
        stranger_api.update_notification(
            notification_id, replacement_params, expected_status=HTTPStatus.NOT_FOUND
        )
    else:
        stranger_api.delete_notification(notification_id, expected_status=HTTPStatus.NOT_FOUND)

    assert fetch_notification(owner_api, notification_id) == original
    with database.connect() as connection:
        stored = Notification.get_state(connection, owner.client_id, notification_id)
        foreign = Notification.get_state(connection, stranger.client_id, notification_id)
        owner_count = Notification.count_for_external_id(connection, owner.client_id, external_id)
        foreign_count = Notification.count_for_external_id(
            connection, stranger.client_id, external_id
        )
    assert stored == original_state
    assert foreign is None
    assert owner_count == 1
    assert foreign_count == 0
