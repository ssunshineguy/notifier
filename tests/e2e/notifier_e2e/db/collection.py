from datetime import datetime, timedelta
from uuid import UUID, uuid4

from sqlalchemy import text
from sqlalchemy.engine import Connection


def seed_collection_fixture(
    connection: Connection, client_id: str, now: datetime
) -> dict[str, UUID]:
    ids = {name: uuid4() for name in ("expired", "recent", "active", "dlq")}
    rows = []
    for name, notification_id in ids.items():
        rows.append({
            "id": notification_id,
            "client_id": client_id,
            "external_id": name,
            "data": '{}' if name == "dlq" else '[{"type":"price","operation":"greater_than","value":100}]',
            "start": now - timedelta(days=800),
            "deadline": now + timedelta(hours=1),
            "stop": None if name in ("active", "dlq") else now - timedelta(days=1),
            "status": "active" if name in ("active", "dlq") else "success",
            "delivery": None if name in ("active", "dlq") else "delivered",
            "schedule": now + timedelta(minutes=30) if name in ("active", "dlq") else None,
        })
    connection.execute(text("""
        INSERT INTO notifier.t_notification
            (id, client_id, external_id, data, start_dt, deadline_dt, stop_dt,
             notification_status, delivery_status, next_scheduled_dt)
        VALUES (:id, :client_id, :external_id,
                CAST(:data AS jsonb),
                :start, :deadline, :stop, :status, :delivery, :schedule)
    """), rows)
    connection.execute(text("""
        WITH original AS (
            SELECT notification.* FROM notifier.t_notification AS notification
            WHERE id = :id AND client_id = :client_id
        ), archived AS (
            INSERT INTO notifier.t_dlq (notification_id, notification, reason, created_dt)
            SELECT id, to_jsonb(original), 'invalid_stored_conditions', :stop FROM original
            RETURNING notification_id
        )
        UPDATE notifier.t_notification
        SET notification_status = 'dlq', delivery_status = NULL,
            stop_dt = :stop, next_scheduled_dt = NULL, lock_until_dt = NULL
        WHERE id IN (SELECT notification_id FROM archived)
    """), {"id": ids["dlq"], "client_id": client_id, "stop": now - timedelta(days=400)})
    return ids


def read_collection_dlq_state(
    connection: Connection, client_id: str, notification_id: UUID
) -> dict[str, object]:
    row = connection.execute(text("""
        SELECT to_jsonb(notification) AS notification, to_jsonb(archive) AS archive
        FROM notifier.t_notification AS notification
        JOIN notifier.t_dlq AS archive ON archive.notification_id = notification.id
        WHERE notification.id = :id AND notification.client_id = :client_id
    """), {"id": notification_id, "client_id": client_id}).mappings().one()
    return dict(row)


def expire_collection_fixture(
    connection: Connection, client_id: str, notification_id: UUID, now: datetime
) -> None:
    connection.execute(
        text("""
            UPDATE notifier.t_notification SET stop_dt = :stop
            WHERE client_id = :client_id AND id = :id
        """),
        {"stop": now - timedelta(days=400), "client_id": client_id, "id": notification_id},
    )
