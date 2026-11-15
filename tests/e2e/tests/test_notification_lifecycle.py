from uuid import UUID, uuid4

from sqlalchemy.engine import Engine

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
    notifier_api: NotifierApiClient, client: Client, database: Engine
) -> None:
    external_id = f"e2e-{uuid4().hex}"
    original_params = [{"type": "price", "operation": "greater_than", "value": 0}]
    replacement_params = [{"type": "price", "operation": "less_than", "value": 2**63 - 1}]

    created = notifier_api.create_notification(external_id, original_params)
    notification_id = UUID(created.json()["notification_id"])

    original = fetch_notification(notifier_api, notification_id)
    assert_notification(
        original,
        notification_id=notification_id,
        external_id=external_id,
        status="active",
        notification_params=original_params,
    )

    update_notification(
        notifier_api,
        database,
        original=original,
        expected=Notification(
            id=notification_id,
            client_id=client.client_id,
            external_id=external_id,
            data=replacement_params,
        ),
    )

    delete_notification(
        notifier_api,
        database,
        client_id=client.client_id,
        notification_id=notification_id,
    )
