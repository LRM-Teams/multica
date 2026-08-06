"""Unit tests for the Cube /execute NDJSON parser (T013).

HTTP is mocked: urllib.request.urlopen is replaced with a fake returning a
canned NDJSON event stream.
"""

from __future__ import annotations

import io
import json
import urllib.request

import pytest

from e2e_harness import sandbox_exec
from e2e_harness.sandbox_exec import build_execute_payload, run_in_sandbox


class _FakeResponse:
    def __init__(self, body: str, status: int = 200):
        self._body = body.encode()
        self.status = status

    def read(self) -> bytes:
        return self._body

    def __enter__(self) -> _FakeResponse:
        return self

    def __exit__(self, *args: object) -> None:
        return None


def _ndjson(events: list[dict[str, object]]) -> str:
    return "\n".join(json.dumps(e) for e in events)


def _mock_urlopen(monkeypatch: pytest.MonkeyPatch, body: str) -> list[object]:
    requests: list[object] = []

    def fake_urlopen(req: object, timeout: float = 0) -> _FakeResponse:
        requests.append(req)
        return _FakeResponse(body)

    monkeypatch.setattr(urllib.request, "urlopen", fake_urlopen)
    return requests


def _run(monkeypatch: pytest.MonkeyPatch, body: str) -> sandbox_exec.ExecResult:
    _mock_urlopen(monkeypatch, body)
    return run_in_sandbox(
        "http://cube-proxy", "cube.app", "sbx-1", "echo hi", 30
    )


def test_clean_exit_zero_is_ok(monkeypatch: pytest.MonkeyPatch) -> None:
    body = _ndjson(
        [
            {"type": "stdout", "text": "hello\n"},
            {"type": "stdout", "text": "\n\n__EXIT_CODE__=0\n"},
            {"type": "number_of_executions", "value": 1},
            {"type": "end_of_execution"},
        ]
    )
    result = _run(monkeypatch, body)
    assert result.exit_code == 0
    assert result.transport_error is None
    assert result.ok is True
    assert "hello" in result.stdout
    assert "__EXIT_CODE__" not in result.stdout


def test_nonzero_exit_via_sentinel_fails(monkeypatch: pytest.MonkeyPatch) -> None:
    body = _ndjson(
        [
            {"type": "stdout", "text": "boom\n__EXIT_CODE__=2\n"},
            {
                "type": "error",
                "name": "SystemExit",
                "value": "2",
                "traceback": "Traceback...",
            },
            {"type": "end_of_execution"},
        ]
    )
    result = _run(monkeypatch, body)
    assert result.exit_code == 2
    assert result.ok is False
    assert "SystemExit" in result.stderr


def test_error_event_means_failure(monkeypatch: pytest.MonkeyPatch) -> None:
    body = _ndjson(
        [
            {"type": "stdout", "text": "partial output\n"},
            {
                "type": "error",
                "name": "NameError",
                "value": "name 'x' is not defined",
                "traceback": "Traceback (most recent call last)...",
            },
        ]
    )
    result = _run(monkeypatch, body)
    assert result.ok is False
    assert result.transport_error is not None
    assert "NameError" in result.stderr


def test_error_event_with_zero_exit_is_failure(monkeypatch: pytest.MonkeyPatch) -> None:
    body = _ndjson(
        [
            {"type": "stdout", "text": "__EXIT_CODE__=0\n"},
            {"type": "error", "name": "SystemExit", "value": "0", "traceback": ""},
        ]
    )
    result = _run(monkeypatch, body)
    assert result.ok is False
    assert result.transport_error is not None


def test_missing_sentinel_is_transport_failure(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    body = _ndjson(
        [
            {"type": "stdout", "text": "some output without a sentinel\n"},
            {"type": "end_of_execution"},
        ]
    )
    result = _run(monkeypatch, body)
    assert result.exit_code is None
    assert result.ok is False
    assert result.transport_error is not None
    assert "sentinel" in result.transport_error


def test_http_error_is_transport_failure(monkeypatch: pytest.MonkeyPatch) -> None:
    def fake_urlopen(req: object, timeout: float = 0) -> object:
        raise urllib.error.HTTPError(
            "http://x/execute", 502, "Bad Gateway", None, io.BytesIO(b"bad gateway")
        )

    monkeypatch.setattr(urllib.request, "urlopen", fake_urlopen)
    result = run_in_sandbox("http://cube-proxy", "cube.app", "sbx-1", "true", 30)
    assert result.ok is False
    assert result.exit_code is None
    assert result.transport_error is not None
    assert "502" in result.transport_error


def test_request_uses_execute_endpoint_and_vhost(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    body = _ndjson([{"type": "stdout", "text": "__EXIT_CODE__=0\n"}])
    requests = _mock_urlopen(monkeypatch, body)
    result = run_in_sandbox("http://cube-proxy/", "example.dev", "sbx-9", "true", 30)
    assert result.ok is True
    assert len(requests) == 1
    req = requests[0]
    assert req.full_url == "http://cube-proxy/execute"
    assert req.get_header("Host") == "49999-sbx-9.example.dev"
    assert req.get_method() == "POST"


def test_payload_contains_sentinel_convention() -> None:
    payload = build_execute_payload("pytest tests/ -q", 120)
    assert 'subprocess.run(["bash", "-lc"' in payload
    assert "pytest tests/ -q" in payload
    assert "timeout=120" in payload
    assert "__EXIT_CODE__" in payload
    assert "raise SystemExit(p.returncode)" in payload
