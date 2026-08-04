from __future__ import annotations

from typing import Any

import pytest

from e2e_harness.multica_client import (
    DispatchHandle,
    MulticaAPIError,
    MulticaClient,
    parse_dispatch_response,
)


def _client() -> MulticaClient:
    return MulticaClient(
        base_url="http://server",
        api_key="token",
        squad_id="squad-1",
        base_env_id="env-base",
        workspace_id="workspace-1",
    )


def _handle() -> DispatchHandle:
    return DispatchHandle(
        project_id="project-1",
        env_id="env-1",
        issue_id="issue-1",
        agent_run_id="agent-run-1",
        leader_run_id="leader-run-1",
        leader_sandbox_id="sandbox-1",
        rollout_error=None,
        raw={},
    )


def test_parse_dispatch_response_exposes_source_and_run_identity() -> None:
    handle = parse_dispatch_response(
        {
            "project_id": "project-1",
            "rollouts": [
                {
                    "env_id": "env-1",
                    "source_task_id": "source-1",
                    "run_id": "run-1",
                }
            ],
        }
    )

    assert handle.source_task_id == "source-1"
    assert handle.run_id == "run-1"


def test_register_source_task_posts_payload_and_returns_server_identity(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    client = _client()
    calls: list[tuple[str, str, dict[str, Any] | None]] = []

    def request(
        method: str, path: str, body: dict[str, Any] | None = None
    ) -> tuple[int, Any]:
        calls.append((method, path, body))
        return 201, {"source_task_id": "source-1"}

    monkeypatch.setattr(client, "_request", request)

    assert client.register_source_task("issue", {"title": "Fix calculator"}) == "source-1"
    assert calls == [
        (
            "POST",
            "/api/v1/source-tasks",
            {"type": "issue", "payload": {"title": "Fix calculator"}},
        )
    ]


@pytest.mark.parametrize(
    ("status", "body"),
    [(500, {"error": "unavailable"}), (201, {}), (201, {"source_task_id": ""})],
)
def test_register_source_task_rejects_unsuccessful_or_missing_identity(
    monkeypatch: pytest.MonkeyPatch, status: int, body: Any
) -> None:
    client = _client()
    monkeypatch.setattr(client, "_request", lambda *_args, **_kwargs: (status, body))

    with pytest.raises(MulticaAPIError):
        client.register_source_task("issue", {"title": "Fix calculator"})


def test_dispatch_scratch_sends_source_identity_without_inline_issue(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    client = _client()
    payloads: list[dict[str, Any]] = []

    def dispatch(payload: dict[str, Any]) -> DispatchHandle:
        payloads.append(payload)
        return _handle()

    monkeypatch.setattr(client, "_dispatch", dispatch)

    assert client.dispatch_scratch(
        {"title": "Fix calculator"}, source_task_id="source-1"
    ) == _handle()
    assert payloads == [
        {
            "mode": "scratch",
            "env_id": "env-base",
            "domain": "swe_lego",
            "dispatch_type": "issue",
            "group_size": 1,
            "squad_id": "squad-1",
            "training_mode": False,
            "source_task_id": "source-1",
        }
    ]


def test_dispatch_scratch_keeps_legacy_inline_issue_without_source_identity(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    client = _client()
    payloads: list[dict[str, Any]] = []
    issue = {"title": "Fix calculator"}
    monkeypatch.setattr(
        client, "_dispatch", lambda payload: payloads.append(payload) or _handle()
    )

    client.dispatch_scratch(issue)

    assert payloads[0]["issue"] == issue
    assert "source_task_id" not in payloads[0]
