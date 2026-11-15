import subprocess
from datetime import datetime, timezone
from http import HTTPStatus
from pathlib import Path
from uuid import UUID

import pytest
from sqlalchemy.engine import Engine

from notifier_e2e.api.notifier_api import NotifierApiClient
from notifier_e2e.db.client import Client
from notifier_e2e.db.collection import (
    expire_collection_fixture,
    read_collection_dlq_state,
    seed_collection_fixture,
)
from notifier_e2e.db.notification import Notification


@pytest.mark.parametrize("batch_size", [1000, 1001])
def test_collector_retention_and_external_id_reuse(
    notifier_api: NotifierApiClient, client: Client, database: Engine, tmp_path: Path, batch_size: int
) -> None:
    with database.begin() as connection:
        ids = seed_collection_fixture(connection, client.client_id, datetime.now(timezone.utc))
        before = {
            name: Notification.get_state(connection, client.client_id, notification_id)
            for name, notification_id in ids.items()
        }
        dlq_before = read_collection_dlq_state(connection, client.client_id, ids["dlq"])
    params = [{"type": "price", "operation": "greater_than", "value": 100}]
    notifier_api.get_notification(ids["expired"])
    duplicate = notifier_api.create_notification("expired", params, expected_status=HTTPStatus.CONFLICT)
    assert UUID(duplicate.json()["notification_id"]) == ids["expired"]

    with database.begin() as connection:
        expire_collection_fixture(connection, client.client_id, ids["expired"], datetime.now(timezone.utc))

    checkout = Path(__file__).resolve().parents[3]
    collector_config = tmp_path / "collector.yaml"
    collector_config.write_text(
        f"notification:\n  scheduled_time_sec: 10\ncollector:\n  batch_size: {batch_size}\n",
        encoding="utf-8",
    )
    container_config = "/tmp/collector-config.yaml"
    for _ in range(2):
        _docker(
            checkout, "compose", "run", "--rm", "--no-deps", "-T",
            "--volume", f"{collector_config}:{container_config}:ro",
            "collector", "-config", container_config,
            "-database-dsn-file", "/run/secrets/database_dsn", "collector",
        )
        notifier_api.get_notification(ids["expired"], expected_status=HTTPStatus.NOT_FOUND)
        with database.connect() as connection:
            assert Notification.get(connection, client.client_id, ids["expired"]) is None
            for name in ("recent", "active", "dlq"):
                assert Notification.get_state(connection, client.client_id, ids[name]) == before[name]
            assert read_collection_dlq_state(connection, client.client_id, ids["dlq"]) == dlq_before
        duplicate = notifier_api.create_notification("dlq", params, expected_status=HTTPStatus.CONFLICT)
        assert UUID(duplicate.json()["notification_id"]) == ids["dlq"]
    created = notifier_api.create_notification("expired", params)
    assert UUID(created.json()["notification_id"]) != ids["expired"]


def _docker(checkout: Path, *args: str) -> str:
    completed = subprocess.run(
        ["docker", *args], cwd=checkout, capture_output=True, text=True, timeout=30, check=False
    )
    assert completed.returncode == 0, f"Docker {args[0]} exited with code {completed.returncode}"
    return completed.stdout.strip()
