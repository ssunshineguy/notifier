import os
from pathlib import Path
import subprocess
import sys


def test_helpers_show_rewritten_assertions_in_a_failing_pytest_run(tmp_path) -> None:
    conftest = Path(__file__).resolve().parents[1] / "conftest.py"
    (tmp_path / "conftest.py").write_text(conftest.read_text())
    (tmp_path / "test_probe.py").write_text('''
from uuid import UUID
from requests import Request, Response
from notifier_e2e.steps.notifications import assert_notification
from notifier_e2e.support.matchers import assert_response


def test_business_diff():
    assert_notification(
        {"notification_id": "00000000-0000-0000-0000-000000000001", "external_id": "actual-order",
         "notification_status": "active", "notification_params": []},
        notification_id=UUID("00000000-0000-0000-0000-000000000001"),
        external_id="expected-order", status="active", notification_params=[],
    )


def test_http_diff():
    response = Response()
    response.status_code = 503
    response._content = b"invalid JSON"
    response.request = Request("GET", "http://localhost/v1/notifications", headers={"X-Request-ID": "e2e-0123456789abcdef0123456789abcdef"}).prepare()
    response.headers["X-Request-ID"] = "e2e-0123456789abcdef0123456789abcdef"
    assert_response(response)
''')
    result = subprocess.run(
        [sys.executable, "-m", "pytest", "-q", "--tb=short", str(tmp_path)],
        text=True, capture_output=True, timeout=15,
        env={**os.environ, "PYTEST_DISABLE_PLUGIN_AUTOLOAD": "1", "PYTEST_ADDOPTS": ""},
        cwd=tmp_path,
    )
    output = result.stdout + result.stderr
    assert result.returncode == 1, output
    assert "2 failed" in output
    assert "Differing items" in output
    assert "actual-order" in output and "expected-order" in output
    assert "503" in output and "200" in output
    assert "GET" in output and "e2e-0123456789abcdef0123456789abcdef" in output
    assert "PytestAssertRewriteWarning" not in output
