import re
from http import HTTPStatus

from requests import Response

DIAGNOSTIC_LIMIT_BYTES = 2048
PROBLEM_TYPES = frozenset({
    "/problems/validation-error", "/problems/unauthorized", "/problems/not-found",
    "/problems/incorrect-notification-status", "/problems/notification-in-progress",
    "/problems/notification-duplicate-disappeared", "/problems/too-many-requests",
    "/problems/internal-error", "/problems/request-timeout", "/problems/content-too-large",
})


def bounded_details(details: str) -> str:
    encoded = details.encode("utf-8")
    if len(encoded) <= DIAGNOSTIC_LIMIT_BYTES:
        return details
    return encoded[:DIAGNOSTIC_LIMIT_BYTES - 3].decode("utf-8", errors="ignore") + "..."


def _request_id(value: object) -> str:
    if isinstance(value, str) and re.fullmatch(
        r"(?:e2e-[0-9a-f]{32}|[0-9a-fA-F]{8}(?:-[0-9a-fA-F]{4}){3}-[0-9a-fA-F]{12})", value
    ):
        return value
    return "<missing>" if value is None else "<redacted>"


def _method(value: object) -> str:
    return value if isinstance(value, str) and value in {"GET", "POST", "PUT", "DELETE", "HEAD", "OPTIONS", "PATCH"} else "<unknown>"


def _route(path: str) -> str:
    route = path.split("?", 1)[0]
    if re.fullmatch(r"/v1/notifications(?:/[0-9a-fA-F-]{36})?", route):
        return "/v1/notifications/{notification_id}" if route.count("/") == 3 else route
    return "<unrecognized-route>"


def transport_details(method: str, path: str, request_id: str, error_type: str) -> str:
    known_errors = {
        "RequestException", "ConnectionError", "Timeout", "ConnectTimeout", "ReadTimeout",
        "ProxyError", "SSLError", "HTTPError", "TooManyRedirects", "InvalidURL",
        "InvalidSchema", "MissingSchema", "ChunkedEncodingError", "ContentDecodingError",
    }
    kind = error_type if error_type in known_errors else "RequestException"
    return bounded_details(
        f"{_method(method)} {_route(path)} expected_status=<unavailable> actual_status=<unavailable> "
        f"content_type=<unavailable> request_id={_request_id(request_id)} transport_error={kind}"
    )


def media_type(response: Response) -> str:
    return response.headers.get("Content-Type", "").split(";", 1)[0].strip().lower()


def response_details(response: Response, expected_status: int = HTTPStatus.OK) -> str:
    request = response.request
    method = _method(request.method if request is not None else None)
    route = _route(request.path_url if request is not None else "")
    content_type = media_type(response)
    if content_type not in {"application/json", "application/problem+json", "text/plain", "text/html", ""}:
        content_type = "<redacted>"
    request_id = request.headers.get("X-Request-ID") if request is not None else None
    details = (
        f"{method} {route} expected_status={int(expected_status)} actual_status={response.status_code} "
        f"content_type={content_type or '<missing>'} request_id={_request_id(request_id)} "
        f"response_request_id={_request_id(response.headers.get('X-Request-ID'))} "
        f"response_bytes={len(response.content)}"
    )
    try:
        body = response.json()
    except (ValueError, RecursionError):
        return bounded_details(details + " json=invalid")
    kind = {dict: "object", list: "array", str: "string", int: "number", float: "number", bool: "boolean", type(None): "null"}.get(type(body), "unknown")
    details += f" json={kind}"
    if isinstance(body, dict) and media_type(response) == "application/problem+json":
        problem_type = body.get("type")
        safe_type = problem_type if isinstance(problem_type, str) and problem_type in PROBLEM_TYPES else "<redacted>"
        status = body.get("status")
        safe_status = str(status) if type(status) is int and 100 <= status <= 599 else "<invalid>"
        details += f" problem.type={safe_type} problem.status={safe_status} problem.request_id={_request_id(body.get('request_id'))}"
    return bounded_details(details)
