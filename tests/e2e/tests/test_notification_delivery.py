from time import monotonic, sleep
from uuid import UUID, uuid4

import pytest

from notifier_e2e import config
from notifier_e2e.api.emulator import EmulatorClient
from notifier_e2e.api.notifier_api import NotifierApiClient
from notifier_e2e.db.client import Client


@pytest.mark.skipif(
    config.INITIAL_CHECK_DELAY_SEC != 0,
    reason="Callback delivery scenario uses the default immediate initial check",
)
def test_notification_delivery(
    notifier_api: NotifierApiClient, emulator_api: EmulatorClient, client: Client
) -> None:
    external_id = f"e2e-{uuid4().hex}"
    threshold = 100
    emulator_api.set_client_override(client.client_id, "price", threshold).raise_for_status()
    created = notifier_api.create_notification(
        external_id, [{"type": "price", "operation": "greater_than", "value": threshold}]
    )
    notification_id = UUID(created.json()["notification_id"])

    try:
        emulator_api.enable_callback_capture(notification_id)
        emulator_api.set_client_override(client.client_id, "price", threshold + 1).raise_for_status()

        expires_at = monotonic() + config.CALLBACK_DELIVERY_TIMEOUT_SECONDS
        while monotonic() < expires_at:
            event = emulator_api.get_received_callback(notification_id)
            if event is not None:
                assert event["notification_id"] == str(notification_id)
                assert event["external_id"] == external_id
                assert event["notification_status"] == "success"
                notification = notifier_api.get_notification(notification_id).json()
                if notification.get("delivery_status") == "delivered":
                    return
            sleep(0.1)
        pytest.fail(f"Notification {notification_id} did not complete callback delivery before the deadline")
    finally:
        emulator_api.reset_callback_override(notification_id)
