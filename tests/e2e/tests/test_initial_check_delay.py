from datetime import timedelta
from time import monotonic, sleep
from uuid import UUID, uuid4

from sqlalchemy.engine import Engine

from notifier_e2e import config
from notifier_e2e.api.emulator import EmulatorClient
from notifier_e2e.api.notifier_api import NotifierApiClient
from notifier_e2e.db.client import Client
from notifier_e2e.db.notification_schedule import NotificationSchedule


def test_initial_check_delay(
    notifier_api: NotifierApiClient,
    emulator_api: EmulatorClient,
    client: Client,
    database: Engine,
) -> None:
    emulator_api.set_client_override(client.client_id, "price", 100).raise_for_status()
    threshold = 99 if config.INITIAL_CHECK_DELAY_SEC == 0 else 100
    created = notifier_api.create_notification(
        f"e2e-{uuid4().hex}",
        [{"type": "price", "operation": "greater_than", "value": threshold}],
    )
    notification_id = UUID(created.json()["notification_id"])

    if config.INITIAL_CHECK_DELAY_SEC == 0:
        expires_at = monotonic() + 8
        observed: dict[str, object] = {}
        while monotonic() < expires_at:
            observed = notifier_api.get_notification(notification_id).json()
            if (
                observed.get("notification_status") == "success"
                and observed.get("delivery_status") == "delivered"
            ):
                break
            sleep(0.1)
        else:
            raise AssertionError(f"Zero-delay notification was not delivered: {observed!r}")

    with database.connect() as connection:
        schedule = NotificationSchedule.get(connection, client.client_id, notification_id)

    if config.INITIAL_CHECK_DELAY_SEC == 0:
        assert schedule.next_scheduled_dt is None
    else:
        assert schedule.next_scheduled_dt == schedule.start_dt + timedelta(
            seconds=config.INITIAL_CHECK_DELAY_SEC
        )
    assert schedule.deadline_dt == schedule.start_dt + timedelta(
        seconds=client.max_lifetime_sec
    )
