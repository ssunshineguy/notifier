import json

import pytest
from requests import Request, Response

from notifier_e2e.support.diagnostics import bounded_details, response_details, transport_details
from notifier_e2e.support.matchers import assert_notification_response, assert_problem_response, assert_response, response_object

REQUEST_ID = "e2e-" + "1" * 32
SECRET = "SYNTHETIC_PRIVATE_SENTINEL"


def response(*, status=500, body=None, raw=None, content_type="application/problem+json"):
    result = Response()
    result.status_code = status
    result.request = Request("GET", "http://localhost/v1/notifications/0193a1f4-6c2e-7d55-9b1a-3f0c2e8d4a71?token=" + SECRET,
                             headers={"X-Request-ID": REQUEST_ID, "Authorization": "Bearer " + SECRET}).prepare()
    result.headers["X-Request-ID"] = REQUEST_ID
    result.headers["Content-Type"] = content_type
    result._content = raw if raw is not None else json.dumps(body).encode()
    return result


def test_invalid_json_preserves_original_http_failure():
    result = response(raw=("<html>" + SECRET + "</html>").encode(), content_type="text/html")
    with pytest.raises(AssertionError) as error:
        assert_response(result, 200, content_type="application/json")
    message = str(error.value)
    assert "GET /v1/notifications/{notification_id}" in message
    assert "expected_status=200 actual_status=500" in message
    assert "content_type=text/html" in message
    assert "json=invalid" in message
    assert "response_bytes=" in message
    assert REQUEST_ID in message
    assert SECRET not in message


def test_success_with_invalid_json_has_safe_assertion():
    result = response(status=200, raw=SECRET.encode(), content_type="application/json")
    with pytest.raises(AssertionError, match="JSON decode failed") as error:
        response_object(result)
    assert SECRET not in str(error.value)


def test_problem_diagnostics_only_include_safe_fields():
    result = response(body={"type": "/problems/internal-error", "title": SECRET, "status": 500,
                            "request_id": REQUEST_ID, "detail": SECRET, "errors": [{"field": SECRET, "message": SECRET}]})
    details = response_details(result)
    assert "problem.type=/problems/internal-error" in details
    assert "problem.status=500" in details
    assert "problem.request_id=" + REQUEST_ID in details
    assert SECRET not in details


def test_unknown_headers_and_problem_fields_are_redacted():
    result = response(body={"type": SECRET, "title": SECRET, "status": SECRET, "request_id": SECRET})
    result.headers["X-Request-ID"] = SECRET
    result.headers["Content-Type"] = SECRET
    result.request.headers["X-Request-ID"] = SECRET
    result.request.url = "http://localhost/" + SECRET
    details = response_details(result)
    assert SECRET not in details
    assert "<redacted>" in details
    assert "<unrecognized-route>" in details


@pytest.mark.parametrize("body", [None, [], 3, SECRET, {SECRET: SECRET}, {"type": SECRET, "title": SECRET, "status": 500}])
def test_rewritten_problem_assertions_do_not_expose_unknown_body(body):
    result = response(body=body)
    with pytest.raises(AssertionError) as error:
        assert_problem_response(result)
    assert SECRET not in str(error.value)
    assert len(str(error.value).encode("utf-8")) <= 2048


def test_rewritten_shape_assertion_does_not_expose_unknown_keys():
    with pytest.raises(AssertionError) as error:
        assert_notification_response(response(status=200, body={SECRET: SECRET}, content_type="application/json"))
    assert SECRET not in str(error.value)


def test_request_id_mismatch_is_visible_without_leaking_header():
    result = response(status=200, body={}, content_type="application/json")
    result.headers["X-Request-ID"] = SECRET
    with pytest.raises(AssertionError, match="Request ID was not echoed") as error:
        assert_response(result)
    assert SECRET not in str(error.value)


def test_utf8_diagnostic_budget_is_bytes():
    details = bounded_details("Ж" * 5000)
    assert len(details.encode("utf-8")) <= 2048
    assert details.endswith("...")


def test_large_untrusted_response_remains_bounded():
    result = response(body={"type": SECRET * 5000, "title": SECRET * 5000, "status": 500, "detail": SECRET * 5000})
    details = response_details(result)
    assert len(details.encode("utf-8")) <= 2048
    assert SECRET not in details


@pytest.mark.parametrize("status", [200, 500])
def test_json_recursion_error_does_not_mask_http_or_decode_failure(status, monkeypatch):
    result = response(status=status, raw=SECRET.encode())

    def decode_failure():
        raise RecursionError(SECRET)

    monkeypatch.setattr(result, "json", decode_failure)
    with pytest.raises(AssertionError) as error:
        if status == 500:
            assert_response(result, 200)
        else:
            response_object(result)
    details = str(error.value)
    assert f"actual_status={status}" in details
    assert "json=invalid" in details
    assert SECRET not in details


def test_transport_diagnostics_redact_untrusted_inputs():
    details = transport_details(SECRET, "/" + SECRET + "?key=" + SECRET, SECRET, SECRET)
    assert SECRET not in details
    assert "<unknown> <unrecognized-route>" in details
    assert "request_id=<redacted>" in details
    assert "transport_error=RequestException" in details
    assert len(details.encode("utf-8")) <= 2048
