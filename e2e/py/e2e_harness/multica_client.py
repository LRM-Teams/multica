"""Thin stdlib-urllib client for the multica server env-dispatch API subset.

Contract: specs/002-env-dispatch-issue-e2e/contracts/env-dispatch-api.md
Auth: `Authorization: Bearer <PAT>`; workspace scoping via `?workspace_id=`
or `?workspace_slug=` query param. No third-party HTTP dependencies.
"""

from __future__ import annotations

import json
import urllib.error
import urllib.parse
import urllib.request
from dataclasses import dataclass
from typing import Any

DEFAULT_HTTP_TIMEOUT_SEC = 60
_ISSUE_DOMAIN = "swe_lego"
_DISPATCH_TYPE_ISSUE = "issue"
_BRANCH_MODES = ("branch", "resume")


class MulticaAPIError(RuntimeError):
    """HTTP-level failure talking to the multica server."""

    def __init__(self, message: str, *, status: int | None = None, body: Any = None):
        super().__init__(message)
        self.status = status
        self.body = body


@dataclass(frozen=True)
class DispatchHandle:
    """Server identifiers captured from a 201 EnvDispatchResponse.

    See data-model.md "DispatchHandle". `raw` keeps the full response body
    for diagnostics (FailureReport.dispatch_response).
    """

    project_id: str
    env_id: str
    issue_id: str | None
    agent_run_id: str | None
    leader_run_id: str | None
    leader_sandbox_id: str | None
    rollout_error: str | None
    raw: dict[str, Any]
    source_task_id: str | None = None
    run_id: str | None = None

    @property
    def submit_ok(self) -> bool:
        """Partial-success 201 detection: error set or no agent run."""
        return self.rollout_error is None and bool(self.agent_run_id)


def _sandbox_id_from_ref(ref: dict[str, Any]) -> str | None:
    """Extract a /execute-target sandbox id from one sandbox ref.

    data-model.md documents `sandbox_instance_id` (agent_sandbox_refs) and
    `instance_id` (sandbox_refs); the server's SandboxInstanceRef actually
    serializes `instance_id`/`local_ref` while AgentSandboxStatus carries
    `sandbox_instance_id`. Accept all three keys.
    """
    for key in ("sandbox_instance_id", "instance_id", "local_ref"):
        value = ref.get(key)
        if isinstance(value, str) and value.strip():
            return value.strip()
    return None


def parse_dispatch_response(body: dict[str, Any]) -> DispatchHandle:
    """Parse a 201 EnvDispatchResponse into a DispatchHandle."""
    project_id = str(body.get("project_id") or "")
    rollouts = body.get("rollouts") or []
    rollout: dict[str, Any] = rollouts[0] if rollouts else {}

    leader_run_id = rollout.get("leader_run_id") or None

    leader_sandbox_id: str | None = None
    agent_sandbox_refs = rollout.get("agent_sandbox_refs") or {}
    agent_sandboxes = rollout.get("agent_sandboxes") or {}
    # The refs map is keyed by agent_id; the leader is the entry the server
    # marked ready (peers stay pending) — or the only entry.
    leader_key: str | None = None
    for agent_id, status in agent_sandboxes.items():
        if isinstance(status, dict) and status.get("status") == "ready":
            leader_key = agent_id
            break
    if leader_key is None and len(agent_sandbox_refs) == 1:
        leader_key = next(iter(agent_sandbox_refs))
    if leader_key is not None:
        ref = agent_sandbox_refs.get(leader_key)
        if isinstance(ref, dict):
            leader_sandbox_id = _sandbox_id_from_ref(ref)
        if leader_sandbox_id is None:
            status = agent_sandboxes.get(leader_key)
            if isinstance(status, dict):
                leader_sandbox_id = _sandbox_id_from_ref(status)
    if leader_sandbox_id is None:
        for ref in agent_sandbox_refs.values():
            if isinstance(ref, dict):
                leader_sandbox_id = _sandbox_id_from_ref(ref)
                if leader_sandbox_id:
                    break
    if leader_sandbox_id is None:
        sandbox_refs = rollout.get("sandbox_refs") or []
        if sandbox_refs and isinstance(sandbox_refs[0], dict):
            leader_sandbox_id = _sandbox_id_from_ref(sandbox_refs[0])

    error = rollout.get("error") or None
    traceback = rollout.get("traceback") or None
    rollout_error: str | None = None
    if error:
        rollout_error = f"{error}\n{traceback}" if traceback else str(error)

    return DispatchHandle(
        project_id=project_id,
        env_id=str(rollout.get("env_id") or ""),
        issue_id=rollout.get("issue_id") or None,
        agent_run_id=rollout.get("agent_run_id") or None,
        leader_run_id=leader_run_id,
        leader_sandbox_id=leader_sandbox_id,
        rollout_error=rollout_error,
        raw=body,
        source_task_id=rollout.get("source_task_id") or None,
        run_id=rollout.get("run_id") or None,
    )


class MulticaClient:
    """Env-dispatch API subset client (contracts/env-dispatch-api.md)."""

    def __init__(
        self,
        base_url: str,
        api_key: str,
        squad_id: str,
        base_env_id: str,
        *,
        workspace_id: str | None = None,
        workspace_slug: str | None = None,
        http_timeout_sec: int = DEFAULT_HTTP_TIMEOUT_SEC,
    ) -> None:
        if not workspace_id and not workspace_slug:
            raise MulticaAPIError("workspace_id or workspace_slug is required")
        self.base_url = base_url.rstrip("/")
        self.api_key = api_key
        self.squad_id = squad_id
        self.base_env_id = base_env_id
        self.workspace_id = workspace_id
        self.workspace_slug = workspace_slug
        self.http_timeout_sec = http_timeout_sec

    @classmethod
    def from_config(cls, config: Any, api_key: str) -> MulticaClient:
        """Build from an e2e_harness.config.HarnessConfig."""
        return cls(
            base_url=config.base_url,
            api_key=api_key,
            squad_id=config.squad_id,
            base_env_id=config.base_env_id,
            workspace_id=config.workspace_id,
            workspace_slug=config.workspace_slug,
        )

    def _request(
        self,
        method: str,
        path: str,
        body: dict[str, Any] | None = None,
    ) -> tuple[int, Any]:
        query: dict[str, str] = {}
        if self.workspace_id:
            query["workspace_id"] = self.workspace_id
        elif self.workspace_slug:
            query["workspace_slug"] = self.workspace_slug
        url = f"{self.base_url}{path}?{urllib.parse.urlencode(query)}"
        data = json.dumps(body).encode() if body is not None else None
        req = urllib.request.Request(
            url,
            data=data,
            headers={
                "Authorization": f"Bearer {self.api_key}",
                "Content-Type": "application/json",
                "Accept": "application/json",
            },
            method=method,
        )
        try:
            with urllib.request.urlopen(req, timeout=self.http_timeout_sec) as resp:
                raw = resp.read().decode()
                return resp.status, json.loads(raw) if raw else {}
        except urllib.error.HTTPError as exc:
            raw = ""
            try:
                raw = exc.read().decode()
            except Exception:
                pass
            parsed: Any = raw
            try:
                parsed = json.loads(raw) if raw else {}
            except json.JSONDecodeError:
                pass
            return exc.code, parsed
        except (urllib.error.URLError, TimeoutError, OSError) as exc:
            raise MulticaAPIError(f"{method} {path} transport error: {exc}") from exc

    def _dispatch(self, payload: dict[str, Any]) -> DispatchHandle:
        status, body = self._request("POST", "/api/v1/env-dispatch", payload)
        if status != 201 or not isinstance(body, dict):
            raise MulticaAPIError(
                f"env-dispatch failed with status {status}", status=status, body=body
            )
        return parse_dispatch_response(body)

    def register_source_task(self, task_type: str, payload: dict[str, Any]) -> str:
        status, body = self._request(
            "POST", "/api/v1/source-tasks", {"type": task_type, "payload": payload}
        )
        if status not in (200, 201) or not isinstance(body, dict):
            raise MulticaAPIError(
                f"source-task registration failed with status {status}",
                status=status,
                body=body,
            )
        source_task_id = body.get("source_task_id")
        if not isinstance(source_task_id, str) or not source_task_id:
            raise MulticaAPIError(
                "source-task registration response missing source_task_id",
                status=status,
                body=body,
            )
        return source_task_id

    def dispatch_scratch(
        self, issue_spec: dict[str, Any], source_task_id: str | None = None
    ) -> DispatchHandle:
        """Scratch stage: fork the base env and dispatch a durable issue source."""
        payload: dict[str, Any] = {
            "mode": "scratch",
            "env_id": self.base_env_id,
            "domain": _ISSUE_DOMAIN,
            "dispatch_type": _DISPATCH_TYPE_ISSUE,
            "group_size": 1,
            "squad_id": self.squad_id,
            "training_mode": False,
        }
        if source_task_id:
            payload["source_task_id"] = source_task_id
        else:
            payload["issue"] = issue_spec
        return self._dispatch(payload)

    def dispatch_branch(self, env_id: str, mode: str) -> DispatchHandle:
        """Branch/resume stage: fork a prior rollout's env; issue is copied.

        `mode` is "branch" or "resume" (resume is normalized server-side).
        The issue payload is forbidden here (contracts/env-dispatch-api.md).
        """
        if mode not in _BRANCH_MODES:
            raise ValueError(f"mode must be one of {_BRANCH_MODES}, got {mode!r}")
        return self._dispatch(
            {
                "mode": mode,
                "env_id": env_id,
                "domain": _ISSUE_DOMAIN,
                "dispatch_type": _DISPATCH_TYPE_ISSUE,
                "group_size": 1,
                "squad_id": self.squad_id,
                "training_mode": False,
            }
        )

    def get_dag(self, project_id: str) -> tuple[int, Any]:
        """Poll the interaction DAG: 202 in_progress / 200 assembled|failed."""
        return self._request("GET", f"/api/v1/env-dispatch/{project_id}/dag")

    def get_issue(self, issue_id: str) -> tuple[int, Any]:
        """Issue read-back (acceptance data lives in metadata JSONB)."""
        return self._request("GET", f"/api/issues/{issue_id}")

    def get_task_runs(self, issue_id: str) -> tuple[int, Any]:
        """Terminal detail: task status/result/error/failure_reason."""
        return self._request("GET", f"/api/issues/{issue_id}/task-runs")

    def delete_dispatch(self, project_id: str) -> int:
        """DELETE the dispatch project (204, idempotent). Returns the status."""
        status, _ = self._request("DELETE", f"/api/v1/env-dispatch/{project_id}")
        return status
