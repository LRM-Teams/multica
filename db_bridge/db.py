"""Async Supabase access layer for the DB bridge.

Wraps the Supabase async client with the request/response queue operations used
by the stub server (``insert_request`` + ``wait_for_response``) and the executor
worker (``claim_next`` + ``complete``/``fail``). Bodies are transparently
codec-encoded on the way in and decoded on the way out.

The httpx pool mirrors AReaL's ``backend_run`` convention: a dedicated pool that
ignores ambient proxy env vars (``trust_env=False``).
"""

from __future__ import annotations

import asyncio
import time
from dataclasses import dataclass, field
from typing import Any

from httpx import AsyncClient as AsyncHttpxClient
from httpx import Limits, Timeout
from supabase import AsyncClient, create_async_client
from supabase.lib.client_options import AsyncClientOptions

from . import codec
from .channels import Channel
from .config import BridgeConfig

# Lightweight columns the stub polls. Deliberately excludes request_* body
# columns so polling never de-TOASTs large payloads.
_POLL_COLUMNS = (
    "id,status,response_status,response_headers,"
    "response_body,response_body_encoding,error"
)

_TERMINAL = frozenset({"done", "error"})


@dataclass(slots=True)
class RelayRequest:
    """A claimed request, decoded for forwarding to the real upstream."""

    id: str
    worker_id: str
    channel: str
    user_id: str
    method: str
    path: str
    headers: dict[str, str]
    content_type: str | None
    body: bytes
    meta: dict[str, Any] | None = None


@dataclass(slots=True)
class RelayResponse:
    """A finalized response, decoded for returning from the stub."""

    status: str  # 'done' | 'error'
    response_status: int | None = None
    headers: dict[str, str] = field(default_factory=dict)
    body: bytes = b""
    error: str | None = None


def _build_httpx_pool() -> AsyncHttpxClient:
    return AsyncHttpxClient(
        timeout=Timeout(connect=30.0, read=120.0, write=30.0, pool=60.0),
        limits=Limits(
            max_connections=100, max_keepalive_connections=50, keepalive_expiry=30
        ),
        trust_env=False,
    )


class BridgeDB:
    """Async queue operations over Supabase."""

    def __init__(self, config: BridgeConfig, client: AsyncClient | None = None):
        self._config = config
        self._client = client
        self._httpx: AsyncHttpxClient | None = None

    @property
    def config(self) -> BridgeConfig:
        return self._config

    async def connect(self) -> BridgeDB:
        if self._client is None:
            self._httpx = _build_httpx_pool()
            self._client = await create_async_client(
                self._config.supabase_url,
                self._config.supabase_key,
                AsyncClientOptions(httpx_client=self._httpx),
            )
        return self

    async def aclose(self) -> None:
        if self._httpx is not None:
            try:
                await self._httpx.aclose()
            finally:
                self._httpx = None

    @property
    def client(self) -> AsyncClient:
        if self._client is None:
            raise RuntimeError("BridgeDB.connect() must be called first")
        return self._client

    async def __aenter__(self) -> BridgeDB:
        return await self.connect()

    async def __aexit__(self, *exc: object) -> None:
        await self.aclose()

    # -- stub side ---------------------------------------------------------

    async def insert_request(
        self,
        channel: Channel,
        *,
        user_id: str,
        method: str,
        path: str,
        headers: dict[str, str],
        content_type: str | None,
        body: bytes,
        meta: dict[str, Any] | None = None,
    ) -> str:
        """Enqueue a request row; returns the new row id."""
        encoding, text = codec.encode(body, threshold=self._config.codec_threshold)
        payload: dict[str, Any] = {
            "channel": channel.name,
            "user_id": user_id,
            "status": "pending",
            "request_method": method,
            "request_path": path,
            "request_headers": headers,
            "request_content_type": content_type,
            "request_body": text,
            "request_body_encoding": encoding,
            "request_meta": meta,
        }
        res = await self.client.table(channel.table).insert(payload).execute()
        rows = getattr(res, "data", None) or []
        if not rows:
            raise RuntimeError(f"insert into {channel.table} returned no row")
        return rows[0]["id"]

    async def poll_response(
        self, channel: Channel, row_id: str, *, user_id: str
    ) -> dict[str, Any]:
        """Fetch lightweight status/response columns for ``row_id`` owned by ``user_id``."""
        res = (
            await self.client.table(channel.table)
            .select(_POLL_COLUMNS)
            .eq("id", row_id)
            .eq("user_id", user_id)
            .single()
            .execute()
        )
        return getattr(res, "data", None) or {}

    async def wait_for_response(
        self, channel: Channel, row_id: str, timeout: float, *, user_id: str
    ) -> RelayResponse | None:
        """Poll until the row is terminal, the timeout elapses, or None on timeout."""
        deadline = time.monotonic() + timeout
        while True:
            row = await self.poll_response(channel, row_id, user_id=user_id)
            if row.get("status") in _TERMINAL:
                return self._decode_response(row)
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                return None
            await asyncio.sleep(min(self._config.poll_interval_s, remaining))

    @staticmethod
    def _decode_response(row: dict[str, Any]) -> RelayResponse:
        return RelayResponse(
            status=row["status"],
            response_status=row.get("response_status"),
            headers=row.get("response_headers") or {},
            body=codec.decode(
                row.get("response_body_encoding"), row.get("response_body")
            ),
            error=row.get("error"),
        )

    # -- executor side -----------------------------------------------------

    async def claim_next(
        self, channel: Channel, worker_id: str, *, user_id: str | None = None
    ) -> RelayRequest | None:
        """Atomically claim the next pending/stale row, optionally scoped by user."""
        res = await self.client.rpc(
            "bridge_claim_next",
            {
                "p_table": channel.table,
                "p_worker_id": worker_id,
                "p_stale_seconds": self._config.stale_seconds,
                "p_user_id": user_id,
            },
        ).execute()
        row = getattr(res, "data", None)
        if not row:
            return None
        return RelayRequest(
            id=row["id"],
            worker_id=row.get("worker_id", worker_id),
            channel=row.get("channel", channel.name),
            user_id=row["user_id"],
            method=row["request_method"],
            path=row["request_path"],
            headers=row.get("request_headers") or {},
            content_type=row.get("request_content_type"),
            body=codec.decode(
                row.get("request_body_encoding"), row.get("request_body")
            ),
            meta=row.get("request_meta"),
        )

    async def complete(
        self,
        channel: Channel,
        row_id: str,
        *,
        worker_id: str,
        response_status: int,
        response_headers: dict[str, str],
        body: bytes,
    ) -> bool:
        """Mark a row done with a relayed response."""
        encoding, text = codec.encode(body, threshold=self._config.codec_threshold)
        res = await self.client.rpc(
            "bridge_complete",
            {
                "p_table": channel.table,
                "p_id": row_id,
                "p_worker_id": worker_id,
                "p_status": "done",
                "p_response_status": response_status,
                "p_response_headers": response_headers,
                "p_response_body": text,
                "p_response_body_encoding": encoding,
                "p_error": None,
            },
        ).execute()
        return bool(getattr(res, "data", None))

    async def fail(
        self, channel: Channel, row_id: str, *, worker_id: str, error: str
    ) -> bool:
        """Mark a row errored with a diagnostic message."""
        res = await self.client.rpc(
            "bridge_complete",
            {
                "p_table": channel.table,
                "p_id": row_id,
                "p_worker_id": worker_id,
                "p_status": "error",
                "p_response_status": None,
                "p_response_headers": None,
                "p_response_body": None,
                "p_response_body_encoding": "raw",
                "p_error": error[:8000],
            },
        ).execute()
        return bool(getattr(res, "data", None))

    async def abandon(
        self, channel: Channel, row_id: str, *, user_id: str, error: str
    ) -> bool:
        """Mark a timed-out pending/claimed row as abandoned.

        This is intentionally not worker-owned: the stub owns the client timeout
        and needs a terminal marker so executors do not process requests after
        callers already received a 504.
        """
        res = await self.client.rpc(
            "bridge_abandon",
            {
                "p_table": channel.table,
                "p_id": row_id,
                "p_user_id": user_id,
                "p_error": error[:8000],
            },
        ).execute()
        return bool(getattr(res, "data", None))

    async def cleanup_stale_rows(
        self, channel: Channel, *, retention_seconds: int, limit: int
    ) -> int:
        """Delete old terminal rows for ``channel`` and return deleted count."""
        res = await self.client.rpc(
            "bridge_cleanup_stale",
            {
                "p_table": channel.table,
                "p_retention_seconds": retention_seconds,
                "p_limit": limit,
            },
        ).execute()
        return int(getattr(res, "data", None) or 0)

    async def redact_headers(self, channel: Channel, row_id: str) -> None:
        """Drop the Authorization header from a retained row (post-completion)."""
        await self.client.rpc(
            "bridge_redact_headers",
            {"p_table": channel.table, "p_id": row_id},
        ).execute()

    async def count_pending(
        self, channel: Channel, *, user_id: str | None = None
    ) -> int:
        """Return pending row count, optionally scoped by user."""
        query = (
            self.client.table(channel.table)
            .select("id", count="exact")
            .eq("status", "pending")
        )
        if user_id is not None:
            query = query.eq("user_id", user_id)
        res = await query.execute()
        count = getattr(res, "count", None)
        if count is not None:
            return count
        data = getattr(res, "data", None) or []
        return len(data)
