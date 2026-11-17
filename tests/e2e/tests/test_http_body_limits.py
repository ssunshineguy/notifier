import json
import re
from collections.abc import Iterator
from datetime import datetime, timedelta, timezone
from http import HTTPStatus
from uuid import UUID, uuid4

import pytest
from sqlalchemy.engine import Engine

from notifier_e2e.api.emulator import EmulatorClient
from notifier_e2e.api.notifier_api import NotifierApiClient
from notifier_e2e.db.client import Client
from notifier_e2e.db.notification import Notification

BODY_LIMIT_BYTES = 32768


def request_body(payload: dict[str, object], size: int, chunked: bool) -> bytes | Iterator[bytes]:
    encoded = json.dumps(payload, separators=(",", ":")).encode()
    assert len(encoded) <= size
    body = encoded + b" " * (size - len(encoded))
    return (body[offset:offset + 4096] for offset in range(0, len(body), 4096)) if chunked else body


@pytest.mark.parametrize("chunked", [False, True], ids=["content-length", "chunked"])
@pytest.mark.parametrize("size,expected_status", [
    (BODY_LIMIT_BYTES, HTTPStatus.OK),
    (BODY_LIMIT_BYTES + 1, HTTPStatus.REQUEST_ENTITY_TOO_LARGE),
])
def test_notifier_request_body_limit(
    notifier_api: NotifierApiClient, client: Client, database: Engine,
    chunked: bool, size: int, expected_status: int,
) -> None:
    external_id = f"e2e-{uuid4().hex}"
    params = [{"type": "time", "operation": "greater_than", "value": "9999-12-31T23:59:59Z"}]
    payload = {"external_id": external_id, "notification_params": params}
    response = notifier_api.create_notification_raw(
        request_body(payload, size, chunked), expected_status=expected_status,
    )
    assert (response.request.headers.get("Transfer-Encoding") == "chunked") == chunked
    assert ("Content-Length" in response.request.headers) != chunked
    with database.connect() as connection:
        assert Notification.count_for_external_id(connection, client.client_id, external_id) == (
            1 if expected_status == HTTPStatus.OK else 0
        )
        if expected_status == HTTPStatus.OK:
            stored = Notification.get(connection, client.client_id, UUID(response.json()["notification_id"]))
            assert stored is not None and stored.data == params


@pytest.mark.parametrize("chunked", [False, True], ids=["content-length", "chunked"])
@pytest.mark.parametrize("size,expected_status", [
    (BODY_LIMIT_BYTES, HTTPStatus.OK),
    (BODY_LIMIT_BYTES + 1, HTTPStatus.REQUEST_ENTITY_TOO_LARGE),
])
def test_emulator_request_body_limit(
    emulator_api: EmulatorClient, client: Client,
    chunked: bool, size: int, expected_status: int,
) -> None:
    emulator_api.set_client_override(client.client_id, "price", 100).raise_for_status()
    response = emulator_api.set_client_override_raw(
        client.client_id, request_body({"parameter": "price", "value": 200}, size, chunked),
        expected_status=expected_status,
    )
    assert (response.request.headers.get("Transfer-Encoding") == "chunked") == chunked
    assert ("Content-Length" in response.request.headers) != chunked
    assert emulator_api.get_price(client.client_id).json()["price"] == (
        200 if expected_status == HTTPStatus.OK else 100
    )


def test_maximum_condition_forest_with_escaped_json(
    notifier_api: NotifierApiClient, client: Client, database: Engine,
) -> None:
    def binary_group(depth: int, timestamp: str) -> dict[str, object]:
        if depth == 0:
            return {"type": "time", "operation": "greater_than", "value": timestamp}
        return {"type": "and", "conditions": [binary_group(depth - 1, timestamp), binary_group(depth - 1, timestamp)]}

    external_id = "x" * 128
    params = [binary_group(depth, "9999-12-31T23:59:59.123456789+03:00") for depth in (3, 3, 2)]
    deadline = (datetime.now(timezone.utc) + timedelta(minutes=30)).strftime("%Y-%m-%dT%H:%M:%S")
    payload = {"external_id": external_id, "notification_params": params, "deadline_dt": deadline + ".123456789+00:00"}
    body = re.sub(
        r'"([^"\\]*)"',
        lambda match: '"' + "".join(f"\\u{ord(char):04x}" for char in match[1]) + '"',
        json.dumps(payload, indent=2),
    ).encode()
    assert len(body) < BODY_LIMIT_BYTES
    response = notifier_api.create_notification_raw(body)
    with database.connect() as connection:
        stored = Notification.get(connection, client.client_id, UUID(response.json()["notification_id"]))
    assert stored is not None
    assert stored.external_id == external_id
    assert stored.data == [binary_group(depth, "9999-12-31T23:59:59+03:00") for depth in (3, 3, 2)]
