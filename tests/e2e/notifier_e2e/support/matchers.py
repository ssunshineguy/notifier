import re
from datetime import datetime
from http import HTTPStatus
from uuid import UUID

from requests import Response

from .diagnostics import PROBLEM_TYPES, media_type, response_details


def _require(valid: bool, field: str, response: Response) -> None:
    assert valid, f"Invalid {field}; {response_details(response, response.status_code)}"


def assert_response(
    response: Response, status: int = HTTPStatus.OK, *, content_type: str | None = None
) -> None:
    details = response_details(response, status)
    status_matches = response.status_code == status
    assert status_matches, details
    request_id = response.request.headers.get("X-Request-ID") if response.request is not None else None
    echoed = request_id is not None and response.headers.get("X-Request-ID") == request_id
    assert echoed, f"Request ID was not echoed; {details}"
    if content_type is not None:
        type_matches = media_type(response) == content_type
        assert type_matches, f"Expected Content-Type {content_type}; {details}"


def response_object(response: Response) -> dict[str, object]:
    try:
        body = response.json()
    except (ValueError, RecursionError):
        raise AssertionError(f"JSON decode failed; {response_details(response, response.status_code)}") from None
    _require(isinstance(body, dict), "JSON object", response)
    return body


def _uuid(value: object) -> bool:
    if not isinstance(value, str):
        return False
    try:
        return str(UUID(value)) == value.lower()
    except ValueError:
        return False


def _timestamp(value: object) -> bool:
    if not isinstance(value, str) or not re.fullmatch(
        r"[0-9]{4}-[0-9]{2}-[0-9]{2}[Tt][0-9]{2}:[0-9]{2}:[0-9]{2}(?:\.[0-9]+)?(?:[Zz]|[+-](?:[01][0-9]|2[0-3]):[0-5][0-9])", value
    ):
        return False
    try:
        parsed = datetime.fromisoformat(value.upper().replace("Z", "+00:00"))
        return parsed.utcoffset() is not None
    except ValueError:
        return False


def assert_created_response(response: Response) -> None:
    body = response_object(response)
    _require(set(body) == {"notification_id"}, "created response fields", response)
    _require(_uuid(body["notification_id"]), "notification_id UUID", response)


def _condition_leaves(value: object, response: Response, depth: int = 0) -> int:
    _require(isinstance(value, dict), "condition object", response)
    kind = value.get("type")
    _require(isinstance(kind, str) and kind in {"time", "price", "and", "or"}, "condition type", response)
    if kind in {"and", "or"}:
        _require(set(value) == {"type", "conditions"}, "group fields", response)
        _require(depth < 3, "condition group depth", response)
        children = value["conditions"]
        _require(isinstance(children, list) and 2 <= len(children) <= 10, "group conditions", response)
        return sum(_condition_leaves(child, response, depth + 1) for child in children)
    _require(set(value) == {"type", "operation", "value"}, "condition fields", response)
    operation = value["operation"]
    _require(isinstance(operation, str) and operation in {"greater_than", "less_than"}, "condition operation", response)
    threshold = value["value"]
    if kind == "time":
        _require(_timestamp(threshold), "time condition timestamp", response)
    else:
        _require(type(threshold) is int and 0 <= threshold <= 2**63 - 1, "price condition int64", response)
    return 1


def assert_notification_response(response: Response) -> None:
    body = response_object(response)
    required = {"notification_id", "external_id", "notification_params", "deadline_dt", "start_dt"}
    _require(required <= set(body) <= required | {"notification_status", "delivery_status"}, "notification fields", response)
    _require(_uuid(body["notification_id"]), "notification_id UUID", response)
    external_id = body["external_id"]
    _require(isinstance(external_id, str) and re.fullmatch(r"[A-Za-z0-9._:-]{1,128}", external_id) is not None, "external_id", response)
    for field in ("start_dt", "deadline_dt"):
        _require(_timestamp(body[field]), field + " timestamp", response)
    for field, allowed in (
        ("notification_status", {"active", "success", "deadline_exceeded"}),
        ("delivery_status", {"trying_to_deliver", "delivered", "delivery_failed"}),
    ):
        if field in body:
            _require(isinstance(body[field], str) and body[field] in allowed, field, response)
    params = body["notification_params"]
    _require(isinstance(params, list) and 1 <= len(params) <= 10, "notification_params", response)
    leaves = sum(_condition_leaves(condition, response) for condition in params)
    _require(leaves <= 20, "condition leaf count", response)


def assert_problem_response(response: Response, *, expected_type: str | None = None) -> None:
    body = response_object(response)
    _require({"type", "title", "status"} <= set(body), "Problem required fields", response)
    problem_type = body["type"]
    _require(isinstance(problem_type, str) and problem_type in PROBLEM_TYPES, "Problem type", response)
    if expected_type is not None:
        _require(problem_type == expected_type, "Problem type for operation", response)
    _require(isinstance(body["title"], str) and bool(body["title"]), "Problem title", response)
    _require(type(body["status"]) is int and body["status"] == response.status_code, "Problem status", response)
    if "request_id" in body:
        _require(body["request_id"] == response.request.headers.get("X-Request-ID"), "Problem request_id", response)
    if expected_type in {"/problems/notification-in-progress", "/problems/notification-duplicate-disappeared", "/problems/content-too-large"}:
        _require("request_id" in body, "Problem request_id", response)
    if "detail" in body:
        _require(isinstance(body["detail"], str), "Problem detail", response)
    if "errors" in body:
        errors = body["errors"]
        _require(isinstance(errors, list), "Problem errors", response)
        for error in errors:
            _require(isinstance(error, dict) and set(error) == {"field", "message"}, "Problem field error", response)
            _require(isinstance(error["field"], str) and isinstance(error["message"], str), "Problem field error strings", response)
