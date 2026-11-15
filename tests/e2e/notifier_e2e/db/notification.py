from __future__ import annotations

from dataclasses import dataclass
from uuid import UUID

from sqlalchemy import DateTime, Uuid, column, func, select, table
from sqlalchemy.dialects.postgresql import JSONB
from sqlalchemy.engine import Connection

notifications = table(
    "t_notification",
    column("id", Uuid),
    column("client_id"),
    column("external_id"),
    column("data", JSONB),
    column("start_dt", DateTime(timezone=True)),
    column("deadline_dt", DateTime(timezone=True)),
    column("notification_status"),
    column("delivery_status"),
    column("stop_dt", DateTime(timezone=True)),
    column("failed_attempt_count"),
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
                select(
                    notifications.c.id,
                    notifications.c.client_id,
                    notifications.c.external_id,
                    notifications.c.data,
                ).where(
                    notifications.c.client_id == client_id,
                    notifications.c.id == notification_id,
                )
            )
            .mappings()
            .one_or_none()
        )
        return cls(**row) if row is not None else None

    @staticmethod
    def get_state(
        connection: Connection, client_id: str, notification_id: UUID
    ) -> dict[str, object] | None:
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
        return dict(row) if row is not None else None

    @staticmethod
    def count_for_external_id(
        connection: Connection, client_id: str, external_id: str
    ) -> int:
        return connection.execute(
            select(func.count()).select_from(notifications).where(
                notifications.c.client_id == client_id,
                notifications.c.external_id == external_id,
            )
        ).scalar_one()

    @staticmethod
    def delete_for_client(connection: Connection, client_id: str) -> None:
        connection.execute(
            notifications.delete().where(notifications.c.client_id == client_id)
        )
