from collections.abc import Iterator
from contextlib import contextmanager
from datetime import datetime, timedelta, timezone
from time import monotonic, sleep
from uuid import uuid4

from sqlalchemy import text
from sqlalchemy.engine import Connection, Engine

from .client import Client
from .notification import notifications


class PendingNotification:
    def __init__(self, connection: Connection) -> None:
        self._connection = connection

    def wait_for_request(self, *, blocked: bool, timeout_seconds: float) -> None:
        expires_at = monotonic() + timeout_seconds
        while True:
            waiting = self._connection.execute(text(
                "SELECT EXISTS (SELECT 1 FROM pg_locks "
                "WHERE locktype = 'transactionid' AND NOT granted "
                "AND transactionid = pg_current_xact_id()::text::xid)"
            )).scalar_one()
            if waiting == blocked:
                return
            remaining = expires_at - monotonic()
            assert remaining > 0, f"Timed out waiting for request blocked={blocked}"
            sleep(min(0.01, remaining))


@contextmanager
def pending_notification(
    database: Engine, client: Client, external_id: str,
    notification_params: list[dict[str, object]],
) -> Iterator[PendingNotification]:
    with database.connect() as connection:
        transaction = connection.begin()
        try:
            connection.execute(notifications.insert().values(
                id=uuid4(), client_id=client.client_id, external_id=external_id,
                data=notification_params,
                deadline_dt=datetime.now(timezone.utc) + timedelta(
                    seconds=client.max_lifetime_sec
                ),
            ))
            yield PendingNotification(connection)
        finally:
            transaction.rollback()
