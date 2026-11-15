from datetime import datetime, timedelta, timezone
from uuid import UUID, uuid4

from sqlalchemy.engine import Engine

from notifier_e2e.api.emulator import EmulatorClient
from notifier_e2e.api.notifier_api import NotifierApiClient
from notifier_e2e.db.client import Client
from notifier_e2e.db.notification import Notification
from notifier_e2e.steps.notifications import (
    assert_notification,
    delete_notification,
    fetch_notification,
    update_notification,
)


def test_notification_lifecycle(
    notifier_api: NotifierApiClient,
    emulator_api: EmulatorClient,
    client: Client,
    database: Engine,
) -> None:
    external_id = f"e2e-{uuid4().hex}"
    price = 100
    emulator_api.set_client_override(client.client_id, "price", price).raise_for_status()
    assert emulator_api.get_price(client.client_id).json()["price"] == price
    original_params = [{"type": "price", "operation": "greater_than", "value": price}]
    replacement_params = [{"type": "price", "operation": "greater_than", "value": price + 1}]

    before_create = datetime.now(timezone.utc)
    created = notifier_api.create_notification(external_id, original_params)
    after_create = datetime.now(timezone.utc)
    notification_id = UUID(created.json()["notification_id"])

    original = fetch_notification(notifier_api, notification_id)
    assert_notification(
        original,
        notification_id=notification_id,
        external_id=external_id,
        status="active",
        notification_params=original_params,
    )
    start = datetime.fromisoformat(original["start_dt"])
    deadline = datetime.fromisoformat(original["deadline_dt"])
    assert deadline - start == timedelta(seconds=client.max_lifetime_sec)
    assert "delivery_status" not in original
    with database.connect() as connection:
        original_state = Notification.get_state(connection, client.client_id, notification_id)
    assert original_state is not None
    stored_start = original_state["start_dt"]
    stored_deadline = original_state["deadline_dt"]
    assert before_create <= stored_start <= after_create
    assert stored_deadline - stored_start == timedelta(seconds=client.max_lifetime_sec)
    assert start == stored_start.replace(microsecond=0)
    assert deadline == stored_deadline.replace(microsecond=0)
    assert original_state == {
        "id": notification_id,
        "client_id": client.client_id,
        "external_id": external_id,
        "data": original_params,
        "start_dt": stored_start,
        "deadline_dt": stored_deadline,
        "notification_status": "active",
        "delivery_status": None,
        "stop_dt": None,
        "failed_attempt_count": 0,
    }

    update_notification(
        notifier_api,
        database,
        expected_response={
            "notification_id": str(notification_id),
            "external_id": external_id,
            "notification_status": "active",
            "notification_params": replacement_params,
            "start_dt": original["start_dt"],
            "deadline_dt": original["deadline_dt"],
        },
        expected=Notification(
            id=notification_id,
            client_id=client.client_id,
            external_id=external_id,
            data=replacement_params,
        ),
    )
    with database.connect() as connection:
        updated_state = Notification.get_state(connection, client.client_id, notification_id)
    assert updated_state == {**original_state, "data": replacement_params}

    delete_notification(
        notifier_api,
        database,
        client_id=client.client_id,
        notification_id=notification_id,
    )
