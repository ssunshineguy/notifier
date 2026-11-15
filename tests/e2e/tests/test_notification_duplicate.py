from collections.abc import Callable
from http import HTTPStatus
from uuid import UUID, uuid4

from sqlalchemy.engine import Engine

from notifier_e2e.api.emulator import EmulatorClient
from notifier_e2e.api.notifier_api import NotifierApiClient
from notifier_e2e.db.client import Client
from notifier_e2e.db.notification import Notification
from notifier_e2e.steps.notifications import assert_notification, fetch_notification


def test_duplicate_keeps_original_id_and_one_unchanged_record(
    notifier_api: NotifierApiClient,
    client: Client,
    client_factory: Callable[[], Client],
    notifier_api_factory: Callable[[Client], NotifierApiClient],
    emulator_api: EmulatorClient,
    database: Engine,
) -> None:
    external_id = f"e2e-{uuid4().hex}"
    price = 100
    emulator_api.set_client_override(client.client_id, "price", price).raise_for_status()
    assert emulator_api.get_price(client.client_id).json()["price"] == price
    params = [{"type": "price", "operation": "greater_than", "value": price}]
    replacement_params = [{"type": "price", "operation": "greater_than", "value": price + 1}]

    created = notifier_api.create_notification(external_id, params)
    notification_id = UUID(created.json()["notification_id"])
    original = fetch_notification(notifier_api, notification_id)
    assert_notification(
        original,
        notification_id=notification_id,
        external_id=external_id,
        status="active",
        notification_params=params,
    )
    with database.connect() as connection:
        original_state = Notification.get_state(connection, client.client_id, notification_id)
    assert original_state is not None
    assert original_state["data"] == params

    duplicate = notifier_api.create_notification(
        external_id, replacement_params, expected_status=HTTPStatus.CONFLICT
    )
    assert duplicate.json() == {"notification_id": str(notification_id)}
    assert fetch_notification(notifier_api, notification_id) == original
    with database.connect() as connection:
        stored = Notification.get_state(connection, client.client_id, notification_id)
        count = Notification.count_for_external_id(connection, client.client_id, external_id)
    assert stored == original_state
    assert count == 1

    another_client = client_factory()
    another_api = notifier_api_factory(another_client)
    emulator_api.set_client_override(another_client.client_id, "price", price).raise_for_status()
    assert emulator_api.get_price(another_client.client_id).json()["price"] == price
    another_created = another_api.create_notification(external_id, replacement_params)
    another_id = UUID(another_created.json()["notification_id"])
    assert another_id != notification_id
    assert_notification(
        fetch_notification(another_api, another_id),
        notification_id=another_id,
        external_id=external_id,
        status="active",
        notification_params=replacement_params,
    )
    with database.connect() as connection:
        another_stored = Notification.get(connection, another_client.client_id, another_id)
        another_count = Notification.count_for_external_id(
            connection, another_client.client_id, external_id
        )
        original_stored = Notification.get_state(connection, client.client_id, notification_id)
    assert another_stored == Notification(
        id=another_id,
        client_id=another_client.client_id,
        external_id=external_id,
        data=replacement_params,
    )
    assert another_count == 1
    assert original_stored == original_state
