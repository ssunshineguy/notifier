import json
from http import HTTPStatus
from unittest.mock import Mock

import pytest
from requests import Request, Response, Timeout

from notifier_e2e.steps import notifications


class Clock:
    def __init__(self) -> None:
        self.now = 0.0
        self.sleeps = []

    def monotonic(self) -> float:
        return self.now

    def sleep(self, seconds: float) -> None:
        self.sleeps.append(seconds)
        self.now += seconds


@pytest.fixture
def clock(monkeypatch) -> Clock:
    clock = Clock()
    monkeypatch.setattr(notifications, "time", clock)
    return clock


def response(status: int, problem_type: str = notifications.IN_PROGRESS_TYPE) -> Response:
    result = Response()
    result.status_code = status
    result.request = Request(
        "PUT", "http://localhost/v1/notifications/00000000-0000-0000-0000-000000000001",
        headers={"X-Request-ID": "e2e-test"},
    ).prepare()
    result.headers = {"Content-Type": "application/problem+json", "X-Request-ID": "e2e-test"}
    result._content = json.dumps({"type": problem_type, "status": status}).encode()
    return result


def test_immediate_success_uses_one_attempt(clock) -> None:
    success = response(200)
    operation = Mock(return_value=success)
    assert notifications.wait_for_mutation(operation) is success
    operation.assert_called_once_with(5.0)
    assert clock.sleeps == []


def test_temporary_reservation_retries_with_remaining_budget(clock) -> None:
    budgets = []
    success = response(200)

    def operation(remaining: float) -> Response:
        budgets.append(remaining)
        clock.now += 0.4
        return response(409) if len(budgets) == 1 else success

    assert notifications.wait_for_mutation(operation) is success
    assert budgets == [5.0, 4.5]
    assert clock.sleeps == [0.1]


def test_permanent_reservation_reports_last_state_and_exhausts_one_budget(clock) -> None:
    budgets = []

    def operation(remaining: float) -> Response:
        budgets.append(remaining)
        clock.now += min(0.45, remaining)
        return response(409)

    with pytest.raises(AssertionError) as error:
        notifications.wait_for_mutation(operation)
    message = str(error.value)
    assert clock.now == pytest.approx(5.0)
    assert len(budgets) == 10
    assert budgets[-1] == pytest.approx(0.05)
    assert "attempts=10" in message
    assert "elapsed=5.000s" in message
    assert "409" in message and notifications.IN_PROGRESS_TYPE in message
    assert len(message.encode()) <= 2048


@pytest.mark.parametrize("status,problem_type", [
    (409, "/problems/notification-duplicate-disappeared"),
    (409, "/problems/notification-in-progress-extra"),
    (500, "/problems/notification-in-progress"),
    (503, "/problems/request-timeout"),
    (404, "/problems/not-found"),
])
def test_other_http_errors_fail_without_retry(clock, status, problem_type) -> None:
    operation = Mock(return_value=response(status, problem_type))
    with pytest.raises(AssertionError, match="non-retryable"):
        notifications.wait_for_mutation(operation)
    operation.assert_called_once()
    assert clock.sleeps == []


def test_invalid_json_fails_without_retry_and_preserves_http_status(clock) -> None:
    malformed = response(503)
    malformed._content = b"not JSON containing secret body"
    operation = Mock(return_value=malformed)
    with pytest.raises(AssertionError) as error:
        notifications.wait_for_mutation(operation)
    message = str(error.value)
    assert "503" in message
    assert "not JSON containing secret body" not in message
    operation.assert_called_once()
    assert clock.sleeps == []


def test_transport_error_fails_without_retry_or_exception_payload(clock) -> None:
    operation = Mock(side_effect=Timeout("credentials or DSN must not appear"))
    with pytest.raises(AssertionError) as error:
        notifications.wait_for_mutation(operation)
    message = str(error.value)
    assert "attempts=1" in message and "Timeout" in message
    assert "credentials or DSN" not in message
    operation.assert_called_once()
    assert clock.sleeps == []


def test_response_validation_failure_is_not_retried(clock) -> None:
    operation = Mock(side_effect=AssertionError("PUT /v1/notifications status=500"))
    with pytest.raises(AssertionError, match="attempts=1"):
        notifications.wait_for_mutation(operation)
    operation.assert_called_once()
    assert clock.sleeps == []


def test_response_after_deadline_does_not_pass(clock) -> None:
    def operation(remaining: float) -> Response:
        clock.now += remaining + 0.01
        return response(HTTPStatus.OK)

    with pytest.raises(AssertionError, match="5s budget"):
        notifications.wait_for_mutation(operation)
    assert clock.sleeps == []


def test_final_sleep_uses_only_remaining_budget(clock) -> None:
    operation = Mock()

    def conflict(remaining: float) -> Response:
        clock.now += 4.95
        return response(409)

    operation.side_effect = conflict
    with pytest.raises(AssertionError, match="attempts=1"):
        notifications.wait_for_mutation(operation)
    assert clock.sleeps == pytest.approx([0.05])
    assert clock.now == pytest.approx(5.0)
    operation.assert_called_once()


def test_transport_failure_keeps_safe_request_context_in_waiter(clock) -> None:
    from uuid import UUID

    from notifier_e2e.api.notifier_api import NotifierApiClient

    session = Mock()
    session.request.side_effect = Timeout("SYNTHETIC_PRIVATE_TRANSPORT_DETAIL")
    api = NotifierApiClient(session, "test-client", base_url="http://localhost")
    with pytest.raises(AssertionError) as error:
        notifications.wait_for_mutation(
            lambda remaining: api.delete_notification(
                UUID("00000000-0000-0000-0000-000000000001"),
                allow_in_progress=True, timeout_seconds=remaining,
            )
        )
    message = str(error.value)
    assert "DELETE /v1/notifications/{notification_id}" in message
    assert "request_id=e2e-" in message
    assert "attempts=1" in message
    assert "SYNTHETIC_PRIVATE_TRANSPORT_DETAIL" not in message
    session.request.assert_called_once()
    assert clock.sleeps == []
