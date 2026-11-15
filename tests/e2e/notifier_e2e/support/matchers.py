from http import HTTPStatus

from requests import Response


def assert_response(
    response: Response, status: HTTPStatus, *, content_type: str | None = None
) -> None:
    request = response.request
    request_id = request.headers["X-Request-ID"]
    details = f"{request.method} {request.path_url} request_id={request_id}"

    assert response.status_code == status, (
        f"{details} returned HTTP {response.status_code}, expected {status.value}"
    )
    assert response.headers.get("X-Request-ID") == request_id, (
        f"{details} did not echo the request ID"
    )
    if content_type is not None:
        actual_type = response.headers.get("Content-Type", "").split(";", 1)[0]
        assert actual_type == content_type, (
            f"{details} returned Content-Type {actual_type!r}, expected {content_type!r}"
        )
