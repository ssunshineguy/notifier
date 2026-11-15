from dataclasses import dataclass
from datetime import datetime
from uuid import UUID

from sqlalchemy import DateTime, Uuid, column, table
from sqlalchemy.engine import Connection


notification_schedules = table(
    "t_notification",
    column("id", Uuid),
    column("client_id"),
    column("start_dt", DateTime(timezone=True)),
    column("next_scheduled_dt", DateTime(timezone=True)),
    column("deadline_dt", DateTime(timezone=True)),
    schema="notifier",
)


@dataclass(frozen=True)
class NotificationSchedule:
    start_dt: datetime
    next_scheduled_dt: datetime | None
    deadline_dt: datetime

    @classmethod
    def get(
        cls, connection: Connection, client_id: str, notification_id: UUID
    ) -> "NotificationSchedule":
        row = (
            connection.execute(
                notification_schedules.select().where(
                    notification_schedules.c.client_id == client_id,
                    notification_schedules.c.id == notification_id,
                )
            )
            .mappings()
            .one()
        )
        return cls(
            start_dt=row["start_dt"],
            next_scheduled_dt=row["next_scheduled_dt"],
            deadline_dt=row["deadline_dt"],
        )
