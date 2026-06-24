"""In-memory fakes for testing the bridge without a live Supabase.

``FakeSupabaseClient`` implements just enough of the supabase-py async surface
used by ``db_bridge.db.BridgeDB`` (``table().insert()/select()/eq()/single()/
execute()`` and ``rpc().execute()``) plus the server-side semantics of the
``bridge_claim_next`` / ``bridge_complete`` / ``bridge_redact_headers``
functions, including single-row claiming and stale reclaim. The same fake backs
the higher-level stub/executor tests so they exercise real ``BridgeDB`` code.
"""

from __future__ import annotations

import itertools
import time
import uuid
from typing import Any


class _Result:
    def __init__(self, data: Any, count: int | None = None):
        self.data = data
        self.count = count


class _TableQuery:
    def __init__(self, client: "FakeSupabaseClient", table: str):
        self._client = client
        self._table = table
        self._op: str | None = None
        self._payload: dict[str, Any] | None = None
        self._columns: str | None = None
        self._count: str | None = None
        self._filters: list[tuple[str, Any]] = []
        self._single = False

    def insert(self, payload: dict[str, Any]) -> "_TableQuery":
        self._op = "insert"
        self._payload = payload
        return self

    def select(self, columns: str = "*", count: str | None = None) -> "_TableQuery":
        self._op = "select"
        self._columns = columns
        self._count = count
        self._client.last_select_columns = columns
        return self

    def eq(self, column: str, value: Any) -> "_TableQuery":
        self._filters.append((column, value))
        return self

    def single(self) -> "_TableQuery":
        self._single = True
        return self

    async def execute(self) -> _Result:
        store = self._client.tables.setdefault(self._table, {})
        if self._op == "insert":
            row = dict(self._payload or {})
            row.setdefault("id", str(uuid.uuid4()))
            row.setdefault("status", "pending")
            row.setdefault("user_id", None)
            row["created_at"] = next(self._client.seq)
            row.setdefault("claimed_at", None)
            row.setdefault("completed_at", None)
            row.setdefault("worker_id", None)
            store[row["id"]] = row
            return _Result([dict(row)])
        if self._op == "select":
            rows = list(store.values())
            for col, val in self._filters:
                rows = [r for r in rows if r.get(col) == val]
            cols = (
                None
                if self._columns in (None, "*")
                else [c.strip() for c in self._columns.split(",")]
            )
            projected = [{c: r.get(c) for c in cols} if cols else dict(r) for r in rows]
            if self._single:
                return _Result(projected[0] if projected else None)
            count = len(projected) if self._count else None
            return _Result(projected, count=count)
        raise AssertionError(f"unsupported op {self._op!r}")


class _RpcQuery:
    def __init__(self, client: "FakeSupabaseClient", fn: str, params: dict[str, Any]):
        self._client = client
        self._fn = fn
        self._params = params

    async def execute(self) -> _Result:
        return _Result(self._client.run_rpc(self._fn, self._params))


class FakeSupabaseClient:
    """Minimal in-memory stand-in for a supabase async client."""

    def __init__(self) -> None:
        self.tables: dict[str, dict[str, dict[str, Any]]] = {}
        self.seq = itertools.count()
        self.last_select_columns: str | None = None
        self.rpc_calls: list[tuple[str, dict[str, Any]]] = []

    def table(self, name: str) -> _TableQuery:
        return _TableQuery(self, name)

    def rpc(self, fn: str, params: dict[str, Any]) -> _RpcQuery:
        return _RpcQuery(self, fn, params)

    # -- emulated server-side functions ------------------------------------

    def run_rpc(self, fn: str, params: dict[str, Any]) -> Any:
        self.rpc_calls.append((fn, params))
        if fn == "bridge_claim_next":
            return self._claim_next(params)
        if fn == "bridge_complete":
            return self._complete(params)
        if fn == "bridge_abandon":
            return self._abandon(params)
        if fn == "bridge_cleanup_stale":
            return self._cleanup_stale(params)
        if fn == "bridge_redact_headers":
            return self._redact(params)
        if fn == "areal_shell_claim_next":
            return self._shell_claim_next(params)
        if fn == "areal_shell_mark_running":
            return self._shell_mark_running(params)
        if fn == "areal_shell_heartbeat":
            return self._shell_heartbeat(params)
        if fn == "areal_shell_complete":
            return self._shell_complete(params)
        if fn == "areal_shell_sweep_stale":
            return self._shell_sweep_stale(params)
        if fn == "areal_shell_request_cancel":
            return self._shell_request_cancel(params)
        if fn == "areal_shell_cleanup":
            return self._shell_cleanup(params)
        raise AssertionError(f"unknown rpc {fn!r}")

    def _claim_next(self, params: dict[str, Any]) -> dict[str, Any] | None:
        store = self.tables.setdefault(params["p_table"], {})
        stale = params.get("p_stale_seconds", 300)
        user_id = params.get("p_user_id")
        now = time.time()

        def claimable(r: dict[str, Any]) -> bool:
            if user_id is not None and r.get("user_id") != user_id:
                return False
            if r["status"] == "pending":
                return True
            if r["status"] == "claimed" and r.get("claimed_epoch") is not None:
                return now - r["claimed_epoch"] > stale
            return False

        candidates = [r for r in store.values() if claimable(r)]
        if not candidates:
            return None
        row = min(candidates, key=lambda r: r["created_at"])
        row["status"] = "claimed"
        row["worker_id"] = params["p_worker_id"]
        row["claimed_epoch"] = now
        row["claimed_at"] = now
        return dict(row)

    def _complete(self, params: dict[str, Any]) -> bool:
        store = self.tables.setdefault(params["p_table"], {})
        row = store.get(params["p_id"])
        if row is None:
            return False
        if row.get("status") != "claimed":
            return False
        if row.get("worker_id") != params["p_worker_id"]:
            return False
        row["status"] = params["p_status"]
        row["response_status"] = params.get("p_response_status")
        row["response_headers"] = params.get("p_response_headers")
        row["response_body"] = params.get("p_response_body")
        row["response_body_encoding"] = params.get("p_response_body_encoding") or "raw"
        row["error"] = params.get("p_error")
        row["completed_at"] = time.time()
        return True

    def _abandon(self, params: dict[str, Any]) -> bool:
        store = self.tables.setdefault(params["p_table"], {})
        row = store.get(params["p_id"])
        if row is None or row.get("status") not in {"pending", "claimed"}:
            return False
        user_id = params.get("p_user_id")
        if user_id is not None and row.get("user_id") != user_id:
            return False
        row["status"] = "error"
        row["response_status"] = None
        row["response_headers"] = None
        row["response_body"] = None
        row["response_body_encoding"] = "raw"
        row["error"] = params.get("p_error")
        row["completed_at"] = time.time()
        return True

    def _cleanup_stale(self, params: dict[str, Any]) -> int:
        store = self.tables.setdefault(params["p_table"], {})
        retention = params.get("p_retention_seconds", 86400)
        limit = params.get("p_limit", 1000)
        cutoff = time.time() - retention
        candidates = [
            row_id
            for row_id, row in sorted(
                store.items(), key=lambda item: item[1].get("completed_at") or 0
            )
            if row.get("status") in {"done", "error"}
            and row.get("completed_at") is not None
            and row["completed_at"] < cutoff
        ][:limit]
        for row_id in candidates:
            del store[row_id]
        return len(candidates)

    def _redact(self, params: dict[str, Any]) -> None:
        store = self.tables.setdefault(params["p_table"], {})
        row = store.get(params["p_id"])
        if row is None:
            return None
        headers = row.get("request_headers") or {}
        if "authorization" in headers:
            headers = {**headers, "authorization": "REDACTED"}
            row["request_headers"] = headers
        return None

    # -- emulated areal_remote_commands functions --------------------------

    _SHELL_TABLE = "areal_remote_commands"
    _SHELL_TERMINAL = {"SUCCEEDED", "FAILED", "CANCELLED", "TIMED_OUT", "STALE"}

    def _shell_store(self) -> dict[str, dict[str, Any]]:
        return self.tables.setdefault(self._SHELL_TABLE, {})

    def _shell_claim_next(self, params: dict[str, Any]) -> dict[str, Any] | None:
        store = self._shell_store()
        runner_id = params["p_runner_id"]
        lease = params.get("p_lease_seconds", 60)
        now = time.time()

        def claimable(r: dict[str, Any]) -> bool:
            if r.get("status") == "PENDING":
                return True
            return (
                r.get("status") == "CLAIMED"
                and r.get("lease_epoch") is not None
                and r["lease_epoch"] < now
            )

        def tmux_has_active(candidate: dict[str, Any]) -> bool:
            return any(
                row is not candidate
                and row.get("tmux_id") == candidate.get("tmux_id")
                and row.get("status") in {"CLAIMED", "RUNNING", "CANCEL_REQUESTED"}
                for row in store.values()
            )

        candidates = [r for r in store.values() if claimable(r) and not tmux_has_active(r)]
        first_per_tmux: dict[str, dict[str, Any]] = {}
        for candidate in sorted(candidates, key=lambda r: r["created_at"]):
            first_per_tmux.setdefault(candidate["tmux_id"], candidate)
        candidates = list(first_per_tmux.values())
        if not candidates:
            return None
        row = min(candidates, key=lambda r: r["created_at"])
        row["status"] = "CLAIMED"
        row["runner_id"] = runner_id
        row["lease_epoch"] = now + lease
        row["heartbeat_at"] = now
        return dict(row)

    def _shell_mark_running(self, params: dict[str, Any]) -> bool:
        row = self._shell_store().get(params["p_id"])
        if row is None or row.get("runner_id") != params["p_runner_id"]:
            return False
        if row.get("status") != "CLAIMED":
            return False
        row["status"] = "RUNNING"
        row["started_at"] = time.time()
        row["lease_epoch"] = time.time() + params.get("p_lease_seconds", 60)
        return True

    def _shell_heartbeat(self, params: dict[str, Any]) -> dict[str, Any] | None:
        row = self._shell_store().get(params["p_id"])
        if row is None or row.get("runner_id") != params["p_runner_id"]:
            return None
        if row.get("status") not in {"CLAIMED", "RUNNING", "CANCEL_REQUESTED"}:
            return None
        row["lease_epoch"] = time.time() + params.get("p_lease_seconds", 60)
        row["heartbeat_at"] = time.time()
        if params.get("p_stdout_tail") is not None:
            row["stdout_tail"] = params["p_stdout_tail"]
        if params.get("p_stderr_tail") is not None:
            row["stderr_tail"] = params["p_stderr_tail"]
        if params.get("p_log_bytes") is not None:
            row["log_bytes"] = params["p_log_bytes"]
        return {
            "status": row["status"],
            "cancel_requested": row.get("cancel_requested_at") is not None,
        }

    def _shell_complete(self, params: dict[str, Any]) -> bool:
        status = params["p_status"]
        if status not in {"SUCCEEDED", "FAILED", "CANCELLED", "TIMED_OUT"}:
            raise AssertionError(f"invalid terminal status {status!r}")
        row = self._shell_store().get(params["p_id"])
        if row is None or row.get("runner_id") != params["p_runner_id"]:
            return False
        if row.get("status") not in {"CLAIMED", "RUNNING", "CANCEL_REQUESTED"}:
            return False
        row["status"] = status
        row["exit_code"] = params.get("p_exit_code")
        if params.get("p_stdout_tail") is not None:
            row["stdout_tail"] = params["p_stdout_tail"]
        if params.get("p_stderr_tail") is not None:
            row["stderr_tail"] = params["p_stderr_tail"]
        if params.get("p_log_bytes") is not None:
            row["log_bytes"] = params["p_log_bytes"]
        row["error_message"] = params.get("p_error_message")
        row["finished_at"] = time.time()
        row["lease_epoch"] = None
        return True

    def _shell_sweep_stale(self, params: dict[str, Any]) -> int:
        store = self._shell_store()
        limit = params.get("p_limit", 100)
        now = time.time()
        candidates = [
            row
            for row in sorted(store.values(), key=lambda r: r.get("lease_epoch") or 0)
            if row.get("status") in {"RUNNING", "CANCEL_REQUESTED"}
            and row.get("lease_epoch") is not None
            and row["lease_epoch"] < now
        ][:limit]
        for row in candidates:
            row["status"] = "STALE"
            row["finished_at"] = now
            row["error_message"] = row.get("error_message") or "runner lease expired"
        return len(candidates)

    def _shell_request_cancel(self, params: dict[str, Any]) -> dict[str, Any] | None:
        row = self._shell_store().get(params["p_id"])
        user_id = params.get("p_user_id")
        if row is None:
            return None
        if user_id is not None and row.get("user_id") != user_id:
            return None
        if row.get("status") in {"PENDING", "CLAIMED", "RUNNING"}:
            was_pending = row["status"] == "PENDING"
            row["status"] = "CANCELLED" if was_pending else "CANCEL_REQUESTED"
            row["cancel_requested_at"] = time.time()
            if was_pending:
                row["finished_at"] = time.time()
            return {"ok": True, "status": row["status"]}
        return {"ok": False, "status": row.get("status")}

    def _shell_cleanup(self, params: dict[str, Any]) -> int:
        store = self._shell_store()
        retention = params.get("p_retention_seconds", 604800)
        limit = params.get("p_limit", 1000)
        cutoff = time.time() - retention
        candidates = [
            row_id
            for row_id, row in sorted(
                store.items(), key=lambda item: item[1].get("finished_at") or 0
            )
            if row.get("status") in self._SHELL_TERMINAL
            and row.get("finished_at") is not None
            and row["finished_at"] < cutoff
        ][:limit]
        for row_id in candidates:
            del store[row_id]
        return len(candidates)
