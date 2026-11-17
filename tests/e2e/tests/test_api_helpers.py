import copy
import json
from http import HTTPStatus
from uuid import UUID

import pytest
from requests import ConnectionError, Request, RequestException, Response, Timeout

from notifier_e2e.api.http_client import HttpClient, HttpRequestError
from notifier_e2e.api.notifier_api import NotifierApiClient

NOTIFICATION_ID = UUID("0193a1f4-6c2e-7d55-9b1a-3f0c2e8d4a71")
PARAMS = [{"type": "price", "operation": "greater_than", "value": 100}]
NOTIFICATION = {
    "notification_id": str(NOTIFICATION_ID),
    "external_id": "test-notification", "notification_params": PARAMS,
    "notification_status": "active",
    "start_dt": "2026-11-15T10:00:00Z", "deadline_dt": "2026-11-15T11:00:00Z",
}


class StubSession:
    def __init__(self, *, status=200, body=None, content_type="application/json", raw=None):
        self.status = status
        self.body = body
        self.content_type = content_type
        self.raw = raw
        self.calls = []

    def request(self, method, url, **kwargs):
        self.calls.append((method, url, kwargs))
        response = Response()
        response.status_code = self.status
        response.request = Request(method, url, headers=kwargs["headers"]).prepare()
        response.headers["X-Request-ID"] = kwargs["headers"]["X-Request-ID"]
        if self.content_type:
            response.headers["Content-Type"] = self.content_type
        body = copy.deepcopy(self.body)
        if isinstance(body, dict) and "request_id" in body:
            body["request_id"] = kwargs["headers"]["X-Request-ID"]
        response._content = self.raw if self.raw is not None else json.dumps(body).encode()
        return response


def api_for(session):
    return NotifierApiClient(session, "test-client", base_url="http://localhost:1")


def invoke(api, method, **kwargs):
    if method == "POST":
        return api.create_notification("test-notification", PARAMS, **kwargs)
    if method == "GET":
        return api.get_notification(NOTIFICATION_ID, **kwargs)
    if method == "PUT":
        return api.update_notification(NOTIFICATION_ID, PARAMS, **kwargs)
    return api.delete_notification(NOTIFICATION_ID, **kwargs)


def problem(kind="notification-in-progress", status=409):
    return {"type": f"/problems/{kind}", "title": "Test problem", "status": status, "request_id": "echo"}


@pytest.mark.parametrize("method", ["POST", "GET", "PUT", "DELETE"])
def test_success_defaults_to_200(method):
    body = {"notification_id": str(NOTIFICATION_ID)} if method == "POST" else NOTIFICATION
    session = StubSession(body=body, raw=b"" if method == "DELETE" else None)
    assert invoke(api_for(session), method).status_code == HTTPStatus.OK
    assert len(session.calls) == 1


@pytest.mark.parametrize("method", ["POST", "GET", "PUT", "DELETE"])
def test_unexpected_status_fails_without_retry(method):
    session = StubSession(status=503, content_type="text/html", raw=b"not JSON")
    with pytest.raises(AssertionError, match="expected_status=200 actual_status=503"):
        invoke(api_for(session), method)
    assert len(session.calls) == 1


@pytest.mark.parametrize("method", ["GET", "PUT", "DELETE"])
def test_explicit_ownership_error(method):
    session = StubSession(status=404, content_type="application/problem+json", body=problem("not-found", 404))
    invoke(api_for(session), method, expected_status=HTTPStatus.NOT_FOUND)


@pytest.mark.parametrize("body,content_type", [
    ({"notification_id": str(NOTIFICATION_ID)}, "application/json; charset=utf-8"),
    (problem("notification-duplicate-disappeared"), "application/problem+json"),
])
def test_both_post_conflict_contracts(body, content_type):
    session = StubSession(status=409, body=body, content_type=content_type)
    invoke(api_for(session), "POST", expected_status=409)
    assert len(session.calls) == 1


@pytest.mark.parametrize("body,content_type", [
    ({"notification_id": "invalid"}, "application/json"),
    ({"notification_id": str(NOTIFICATION_ID), "extra": "forbidden"}, "application/json"),
    (problem(), "application/problem+json"),
    (problem("notification-duplicate-disappeared", 500), "application/problem+json"),
    (problem("notification-duplicate-disappeared"), "application/json"),
    (problem("notification-duplicate-disappeared"), "text/plain"),
    ({k: v for k, v in problem("notification-duplicate-disappeared").items() if k != "request_id"}, "application/problem+json"),
])
def test_invalid_post_conflict_is_rejected(body, content_type):
    session = StubSession(status=409, body=body, content_type=content_type)
    with pytest.raises(AssertionError):
        invoke(api_for(session), "POST", expected_status=409)
    assert len(session.calls) == 1


@pytest.mark.parametrize("method", ["PUT", "DELETE"])
def test_mutation_reservation_requires_explicit_opt_in(method):
    session = StubSession(status=409, content_type="application/problem+json", body=problem())
    with pytest.raises(AssertionError, match="expected_status=200 actual_status=409"):
        invoke(api_for(session), method)
    invoke(api_for(session), method, expected_status=409)
    invoke(api_for(session), method, allow_in_progress=True)
    assert len(session.calls) == 3


@pytest.mark.parametrize("method", ["PUT", "DELETE"])
@pytest.mark.parametrize("body,content_type", [
    (problem("notification-duplicate-disappeared"), "application/problem+json"),
    (problem(), "application/json"),
    (problem(status=500), "application/problem+json"),
])
def test_other_conflicts_are_not_retryable(method, body, content_type):
    session = StubSession(status=409, content_type=content_type, body=body)
    with pytest.raises(AssertionError):
        invoke(api_for(session), method, allow_in_progress=True)
    assert len(session.calls) == 1


@pytest.mark.parametrize("method", ["PUT", "DELETE"])
def test_mutation_passes_timeout_budget_to_transport(method):
    session = StubSession(body=NOTIFICATION, raw=b"" if method == "DELETE" else None)
    invoke(api_for(session), method, timeout_seconds=0.07)
    timeout = session.calls[0][2]["timeout"]
    assert timeout.total == pytest.approx(0.07)
    assert timeout.connect_timeout <= 0.07
    assert timeout.read_timeout <= 0.07


@pytest.mark.parametrize("field,value", [
    ("notification_id", "invalid"), ("external_id", ""), ("external_id", "has spaces"),
    ("start_dt", "2026-11-15"), ("deadline_dt", "2026-99-15T11:00:00Z"),
    ("deadline_dt", "2026-11-15T11:00:00+00:99"),
    ("deadline_dt", "2026-11-15T11:00:00-01:60"),
    ("deadline_dt", "2026-11-15T11:00:00"), ("notification_status", "dlq"),
    ("delivery_status", "active"), ("notification_params", []),
    ("notification_params", [{"type": "price", "operation": "greater_than", "value": -1}]),
    ("notification_params", [{"type": "price", "operation": "greater_than", "value": True}]),
    ("notification_params", [{"type": "price", "operation": "greater_than", "value": 2**63}]),
    ("notification_params", [{"type": "price", "operation": "equal", "value": 100}]),
    ("notification_params", [{"type": "time", "operation": "less_than", "value": "yesterday"}]),
    ("notification_params", [{"type": "and", "conditions": PARAMS}]),
])
def test_invalid_notification_response_is_rejected(field, value):
    body = {**NOTIFICATION, field: value}
    with pytest.raises(AssertionError):
        invoke(api_for(StubSession(body=body)), "GET")


@pytest.mark.parametrize("field", ["notification_id", "external_id", "notification_params", "start_dt", "deadline_dt"])
def test_missing_required_field_is_rejected(field):
    body = {key: value for key, value in NOTIFICATION.items() if key != field}
    with pytest.raises(AssertionError, match="notification fields"):
        invoke(api_for(StubSession(body=body)), "GET")


def test_nested_conditions_and_delivery_state_are_valid():
    body = {**NOTIFICATION, "notification_status": "success", "delivery_status": "trying_to_deliver",
            "notification_params": [{"type": "or", "conditions": [*PARAMS, {"type": "time", "operation": "less_than", "value": "2026-11-15T10:30:00+03:00"}]}]}
    invoke(api_for(StubSession(body=body)), "GET")


def test_delete_rejects_body():
    with pytest.raises(AssertionError, match="DELETE response must be empty"):
        invoke(api_for(StubSession(body={})), "DELETE")


def test_success_rejects_wrong_media_type():
    with pytest.raises(AssertionError, match="Expected Content-Type application/json"):
        invoke(api_for(StubSession(body=NOTIFICATION, content_type="text/plain")), "GET")


@pytest.mark.parametrize("exception_type", [ConnectionError, Timeout])
def test_transport_failures_keep_safe_context_without_retry(exception_type):
    secret = "SYNTHETIC_PRIVATE_TRANSPORT_SENTINEL"
    calls = []

    class FailingSession:
        def request(self, method, url, **kwargs):
            calls.append((method, url, kwargs))
            request = Request(method, url, headers={"Authorization": "Bearer " + secret}).prepare()
            raise exception_type(secret, request=request)

    api = HttpClient(FailingSession(), "http://user:" + secret + "@localhost")
    with pytest.raises(RequestException) as error:
        api.request("PUT", "/v1/notifications/" + str(NOTIFICATION_ID) + "?token=" + secret)
    assert isinstance(error.value, HttpRequestError)
    assert len(calls) == 1
    details = str(error.value)
    assert "PUT /v1/notifications/{notification_id}" in details
    assert "actual_status=<unavailable>" in details
    assert "content_type=<unavailable>" in details
    assert "request_id=" + calls[0][2]["headers"]["X-Request-ID"] in details
    assert "transport_error=" + exception_type.__name__ in details
    assert secret not in details
    assert len(details.encode("utf-8")) <= 2048
    assert error.value.request is None
    assert error.value.response is None
    assert error.value.__suppress_context__
