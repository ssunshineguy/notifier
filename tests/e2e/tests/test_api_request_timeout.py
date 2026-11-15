from concurrent.futures import ThreadPoolExecutor
from http import HTTPStatus
from time import monotonic
from uuid import uuid4

from requests import Response
from sqlalchemy.engine import Engine

from notifier_e2e import config
from notifier_e2e.api.notifier_api import NotifierApiClient
from notifier_e2e.db.client import Client
from notifier_e2e.db.notification import Notification
from notifier_e2e.db.request_timeout import pending_notification
from notifier_e2e.support.matchers import assert_problem_response


def test_request_timeout_cancels_blocked_database_operation(
    notifier_api: NotifierApiClient, client: Client, database: Engine,
) -> None:
    external_id = f"e2e-{uuid4().hex}"
    params = [{"type": "price", "operation": "greater_than", "value": 100}]

    def create() -> tuple[Response, float]:
        started = monotonic()
        response = notifier_api.create_notification(
            external_id, params, expected_status=HTTPStatus.SERVICE_UNAVAILABLE
        )
        return response, monotonic() - started

    with pending_notification(database, client, external_id, params) as pending:
        with ThreadPoolExecutor(max_workers=1) as executor:
            request = executor.submit(create)
            pending.wait_for_request(blocked=True, timeout_seconds=config.TIMEOUT_SECONDS)
            response, elapsed = request.result(timeout=config.TIMEOUT_SECONDS)
        assert_problem_response(response, expected_type="/problems/request-timeout")
        assert response.json()["request_id"] == response.request.headers["X-Request-ID"]
        assert response.headers.get("Connection") == "close"
        assert config.API_REQUEST_TIMEOUT_SECONDS - 0.05 <= elapsed < 0.9
        pending.wait_for_request(blocked=False, timeout_seconds=0.5)

    with database.connect() as connection:
        assert Notification.count_for_external_id(connection, client.client_id, external_id) == 0
