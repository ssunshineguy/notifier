from __future__ import annotations

from dataclasses import dataclass
from uuid import UUID

from sqlalchemy import Uuid, column, table
from sqlalchemy.dialects.postgresql import JSONB
from sqlalchemy.engine import Connection

notifications = table(
    "t_notification",
    column("id", Uuid),
    column("client_id"),
    column("external_id"),
    column("data", JSONB),
    schema="notifier",
)


@dataclass(frozen=True)
class Notification:
    id: UUID
    client_id: str
    external_id: str
    data: list[dict[str, object]]

    @classmethod
    def get(
        cls, connection: Connection, client_id: str, notification_id: UUID
    ) -> Notification | None:
        row = (
            connection.execute(
                notifications.select().where(
                    notifications.c.client_id == client_id,
                    notifications.c.id == notification_id,
                )
            )
            .mappings()
            .one_or_none()
        )
        return cls(**row) if row is not None else None

    @staticmethod
    def delete_for_client(connection: Connection, client_id: str) -> None:
        connection.execute(
            notifications.delete().where(notifications.c.client_id == client_id)
        )
