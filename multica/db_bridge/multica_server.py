"""Multica-side public bridge server.

This is the internet-exposed counterpart to the loopback stub servers. It runs
on the multica host (which can be reached from the public internet but cannot
reach the AReaL intranet) and offers two authenticated surfaces:

* **LLM** -- an OpenAI-compatible ``POST /v1/chat/completions`` (alias
  ``/chat/completions``). The request is parked in the shared ``rpc_chat_completions``
  table; the already-running AReaL-side executor claims it, forwards it to the
  real AReaL gateway, and writes the response back, which this server returns.
* **Remote shell** -- ``/shell/commands`` endpoints that enqueue / inspect /
  cancel rows in ``areal_remote_commands`` for the existing AReaL shell runner.

Both surfaces are guarded by *separate* static API keys (see ``multica_auth``).
Every call is DB-routed; multica never connects to AReaL directly.
"""

from __future__ import annotations

import contextlib
import json
import logging
import time
from typing import Any, Final

from fastapi import APIRouter, Depends, FastAPI, Request, Response
from fastapi.responses import JSONResponse, StreamingResponse
from supabase import AsyncClient, create_async_client
from supabase.lib.client_options import AsyncClientOptions

from . import relay
from .channels import CHANNELS_BY_NAME
from .config import MulticaConfig
from .db import BridgeDB, _build_httpx_pool
from .multica_auth import require_api_key

logger = logging.getLogger("db_bridge.multica")

_CHAT_CHANNEL: Final = CHANNELS_BY_NAME["chat_completions"]
_AREAL_MODEL_PREFIX: Final = "areal/"
_SHELL_TABLE: Final = "areal_remote_commands"
_SHELL_STATUS_PENDING: Final = "PENDING"

# Columns returned to a status query. Excludes the (potentially huge) command
# text and avoids exposing internal lease bookkeeping.
_SHELL_STATUS_COLUMNS: Final = (
    "id,status,exit_code,stdout_tail,stderr_tail,error_message,"
    "tmux_id,created_at,started_at,finished_at"
)

# Bridge-credential headers that must never be forwarded to the real upstream:
# they authenticate the caller to *this* server, not to the AReaL gateway.
_BRIDGE_CRED_HEADERS: Final = frozenset({"authorization", "x-api-key"})


# ---------------------------------------------------------------------------
# Remote-shell DB access layer
# ---------------------------------------------------------------------------


class MulticaShellDB:
    """Async Supabase access for enqueuing / inspecting / cancelling commands.

    The multica server is the (user-authorized) creator of ``areal_remote_commands``
    rows; the AReaL-host runner claims and executes them. This layer only writes
    ``PENDING`` rows, reads status, and requests cancellation -- it never claims
    or executes anything itself.
    """

    def __init__(self, config: MulticaConfig, client: AsyncClient | None = None):
        self._config = config
        self._client = client
        self._httpx = None

    @property
    def config(self) -> MulticaConfig:
        return self._config

    async def connect(self) -> MulticaShellDB:
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
            raise RuntimeError("MulticaShellDB.connect() must be called first")
        return self._client

    async def enqueue_command(
        self,
        *,
        user_id: str,
        tmux_id: str,
        command: str,
        cwd: str | None = None,
        timeout_seconds: int | None = None,
        metadata: dict[str, Any] | None = None,
    ) -> dict[str, Any]:
        """Insert a PENDING command row; returns ``{id, status}``."""
        payload: dict[str, Any] = {
            "user_id": user_id,
            "tmux_id": tmux_id,
            "command": command,
            "cwd": cwd or self._config.shell_default_cwd,
            "timeout_seconds": self._config.resolve_shell_timeout(timeout_seconds),
            "status": _SHELL_STATUS_PENDING,
            "metadata": metadata or {},
        }
        res = await self.client.table(_SHELL_TABLE).insert(payload).execute()
        rows = getattr(res, "data", None) or []
        if not rows:
            raise RuntimeError(f"insert into {_SHELL_TABLE} returned no row")
        row = rows[0]
        return {"id": row["id"], "status": row.get("status", _SHELL_STATUS_PENDING)}

    async def get_command(
        self, command_id: str, *, user_id: str
    ) -> dict[str, Any] | None:
        """Return status columns for a command owned by ``user_id``, or None."""
        res = (
            await self.client.table(_SHELL_TABLE)
            .select(_SHELL_STATUS_COLUMNS)
            .eq("id", command_id)
            .eq("user_id", user_id)
            .execute()
        )
        rows = getattr(res, "data", None) or []
        return rows[0] if rows else None

    async def request_cancel(
        self, command_id: str, *, user_id: str
    ) -> dict[str, Any] | None:
        """Request cancellation; returns the RPC ``{ok, status}`` or None."""
        res = await self.client.rpc(
            "areal_shell_request_cancel",
            {"p_id": command_id, "p_user_id": user_id},
        ).execute()
        return getattr(res, "data", None)


# ---------------------------------------------------------------------------
# LLM router
# ---------------------------------------------------------------------------


def _parse_chat_payload(body: bytes) -> dict[str, Any] | None:
    try:
        payload = json.loads(body)
    except json.JSONDecodeError:
        return None
    return payload if isinstance(payload, dict) else None


def _upstream_headers(request: Request, config: MulticaConfig) -> dict[str, str]:
    """Headers to store for replay: drop the inbound multica credential and,
    if configured, inject the real upstream API key."""
    headers = relay.filter_request_headers(relay.capture_headers(request.headers))
    headers = {k: v for k, v in headers.items() if k not in _BRIDGE_CRED_HEADERS}
    if config.upstream_api_key:
        headers["authorization"] = f"Bearer {config.upstream_api_key}"
    return headers


def create_llm_router(db: BridgeDB, config: MulticaConfig) -> APIRouter:
    router = APIRouter()
    guard = require_api_key(config.llm_api_keys, surface="llm")
    channel = _CHAT_CHANNEL
    timeout = config.chat_timeout_s
    max_body = config.max_body_bytes
    user_id = config.bridge_user_id
    first_chunk_timeout = config.stream_first_chunk_timeout_s
    inter_chunk_timeout = config.stream_inter_chunk_timeout_s
    stream_poll = config.stream_poll_interval_s

    async def _serve_stream(request: Request, row_id: str, started: float) -> Response:
        """Wait for the stream to begin, then relay chunks as an SSE response."""
        start = await db.wait_for_stream_start(
            channel,
            row_id,
            first_chunk_timeout,
            user_id=user_id,
            poll_interval=stream_poll,
        )
        if start is None:
            error = (
                f"bridge timed out after {first_chunk_timeout:.0f}s waiting for the "
                f"chat completion stream to begin (request {row_id})"
            )
            logger.warning("multica stream start timeout id=%s", row_id)
            try:
                await db.abandon(channel, row_id, user_id=user_id, error=error)
            except Exception as exc:  # noqa: BLE001 -- response must still return
                logger.warning("multica failed to abandon id=%s: %s", row_id, exc)
            return JSONResponse(status_code=504, content={"detail": error})
        if start.status == "error":
            logger.warning("multica stream relay error id=%s: %s", row_id, start.error)
            return JSONResponse(
                status_code=502,
                content={"detail": start.error or "bridge relay error"},
            )

        resp_headers = relay.filter_response_headers(start.headers)
        # StreamingResponse owns the Content-Type via media_type; drop any
        # captured content-type header to avoid emitting it twice.
        media_type = resp_headers.pop("content-type", None) or "text/event-stream"

        async def _body_iter():
            try:
                async for chunk in db.stream_chunks(
                    channel,
                    row_id,
                    user_id=user_id,
                    first_chunk_timeout=inter_chunk_timeout,
                    inter_chunk_timeout=inter_chunk_timeout,
                    poll_interval=stream_poll,
                ):
                    if await request.is_disconnected():
                        logger.info("multica stream client disconnected id=%s", row_id)
                        break
                    yield chunk
            except Exception as exc:  # noqa: BLE001 -- never crash the ASGI stream
                logger.warning("multica stream body error id=%s: %s", row_id, exc)

        logger.info(
            "multica streaming chat completion id=%s status=%s latency_ms=%.1f",
            row_id,
            start.response_status or 200,
            (time.monotonic() - started) * 1000,
        )
        return StreamingResponse(
            _body_iter(),
            status_code=start.response_status or 200,
            headers=resp_headers,
            media_type=media_type,
        )

    async def chat_completions(
        request: Request, _key: str = Depends(guard)
    ) -> Response:
        body = await request.body()
        if len(body) > max_body:
            return JSONResponse(
                status_code=413,
                content={
                    "detail": (
                        f"request body {len(body)} bytes exceeds limit {max_body} bytes"
                    )
                },
            )

        payload = _parse_chat_payload(body)
        if payload is None:
            return JSONResponse(
                status_code=400, content={"detail": "request body must be JSON object"}
            )
        model = payload.get("model")
        if not isinstance(model, str) or not model.strip().lower().startswith(
            _AREAL_MODEL_PREFIX
        ):
            return JSONResponse(
                status_code=400,
                content={
                    "detail": (
                        "model must start with 'areal/'; this endpoint only serves "
                        "AReaL-routed models"
                    )
                },
            )
        is_stream = bool(payload.get("stream"))

        headers = _upstream_headers(request, config)
        started = time.monotonic()
        row_id = await db.insert_request(
            channel,
            user_id=user_id,
            method="POST",
            path=channel.path,
            headers=headers,
            content_type=request.headers.get("content-type"),
            body=body,
            meta={
                "content_length": len(body),
                "source": "multica",
                "stream": is_stream,
            },
        )
        logger.info(
            "multica enqueued chat completion id=%s model=%s bytes=%d stream=%s",
            row_id,
            model,
            len(body),
            is_stream,
        )

        if is_stream:
            return await _serve_stream(request, row_id, started)

        result = await db.wait_for_response(channel, row_id, timeout, user_id=user_id)
        if result is None:
            error = (
                f"bridge timed out after {timeout:.0f}s waiting for chat completion "
                f"(request {row_id})"
            )
            logger.warning("multica chat timeout id=%s after %.1fs", row_id, timeout)
            try:
                await db.abandon(channel, row_id, user_id=user_id, error=error)
            except Exception as exc:  # noqa: BLE001 -- response must still return
                logger.warning("multica failed to abandon id=%s: %s", row_id, exc)
            return JSONResponse(status_code=504, content={"detail": error})
        if result.status == "error":
            logger.warning("multica chat relay error id=%s: %s", row_id, result.error)
            return JSONResponse(
                status_code=502,
                content={"detail": result.error or "bridge relay error"},
            )

        logger.info(
            "multica returning chat completion id=%s status=%s bytes=%d latency_ms=%.1f",
            row_id,
            result.response_status or 200,
            len(result.body),
            (time.monotonic() - started) * 1000,
        )
        return Response(
            content=result.body,
            status_code=result.response_status or 200,
            headers=relay.filter_response_headers(result.headers),
        )

    router.add_api_route(
        "/v1/chat/completions",
        chat_completions,
        methods=["POST"],
        name="multica_chat_completions_v1",
    )
    router.add_api_route(
        "/chat/completions",
        chat_completions,
        methods=["POST"],
        name="multica_chat_completions",
    )
    return router


# ---------------------------------------------------------------------------
# Remote-shell enqueue router
# ---------------------------------------------------------------------------


def create_shell_router(db: MulticaShellDB, config: MulticaConfig) -> APIRouter:
    router = APIRouter(prefix="/shell")
    guard = require_api_key(config.shell_api_keys, surface="shell")
    user_id = config.bridge_user_id

    async def create_command(request: Request, _key: str = Depends(guard)) -> Response:
        try:
            payload = await request.json()
        except Exception:  # noqa: BLE001 -- any parse failure is a client error
            return JSONResponse(
                status_code=400, content={"detail": "request body must be JSON object"}
            )
        if not isinstance(payload, dict):
            return JSONResponse(
                status_code=400, content={"detail": "request body must be JSON object"}
            )

        command = payload.get("command")
        tmux_id = payload.get("tmux_id")
        if not isinstance(command, str) or not command.strip():
            return JSONResponse(
                status_code=400, content={"detail": "'command' is required"}
            )
        if not isinstance(tmux_id, str) or not tmux_id.strip():
            return JSONResponse(
                status_code=400, content={"detail": "'tmux_id' is required"}
            )

        cwd = payload.get("cwd")
        if cwd is not None and not isinstance(cwd, str):
            return JSONResponse(
                status_code=400, content={"detail": "'cwd' must be a string"}
            )
        timeout_seconds = payload.get("timeout_seconds")
        if timeout_seconds is not None and not isinstance(timeout_seconds, int):
            return JSONResponse(
                status_code=400,
                content={"detail": "'timeout_seconds' must be an integer"},
            )
        metadata = payload.get("metadata")
        if metadata is not None and not isinstance(metadata, dict):
            return JSONResponse(
                status_code=400, content={"detail": "'metadata' must be an object"}
            )

        result = await db.enqueue_command(
            user_id=user_id,
            tmux_id=tmux_id.strip(),
            command=command,
            cwd=cwd,
            timeout_seconds=timeout_seconds,
            metadata=metadata,
        )
        logger.info(
            "multica enqueued shell command id=%s tmux_id=%s", result["id"], tmux_id
        )
        return JSONResponse(status_code=201, content=result)

    async def get_command(command_id: str, _key: str = Depends(guard)) -> Response:
        row = await db.get_command(command_id, user_id=user_id)
        if row is None:
            return JSONResponse(
                status_code=404, content={"detail": "command not found"}
            )
        return JSONResponse(status_code=200, content=row)

    async def cancel_command(command_id: str, _key: str = Depends(guard)) -> Response:
        result = await db.request_cancel(command_id, user_id=user_id)
        if result is None:
            return JSONResponse(
                status_code=404, content={"detail": "command not found"}
            )
        return JSONResponse(status_code=200, content=result)

    router.add_api_route(
        "/commands", create_command, methods=["POST"], name="multica_shell_create"
    )
    router.add_api_route(
        "/commands/{command_id}",
        get_command,
        methods=["GET"],
        name="multica_shell_get",
    )
    router.add_api_route(
        "/commands/{command_id}/cancel",
        cancel_command,
        methods=["POST"],
        name="multica_shell_cancel",
    )
    return router


# ---------------------------------------------------------------------------
# App factory
# ---------------------------------------------------------------------------


def create_multica_app(
    config: MulticaConfig,
    *,
    bridge_db: BridgeDB | None = None,
    shell_db: MulticaShellDB | None = None,
) -> FastAPI:
    """Build the multica server app.

    ``bridge_db`` / ``shell_db`` may be injected for tests (with a fake Supabase
    client); in production they are created from ``config`` and connected by the
    lifespan hook. Test harnesses driving the app over ``httpx.ASGITransport`` do
    not trigger lifespan events, so they should inject already-usable DBs;
    ``connect()`` is a no-op when a client is already present.
    """
    bridge_db = bridge_db or BridgeDB(_bridge_config(config))
    shell_db = shell_db or MulticaShellDB(config)

    @contextlib.asynccontextmanager
    async def lifespan(_app: FastAPI):
        await bridge_db.connect()
        await shell_db.connect()
        logger.info(
            "multica server ready llm_keys=%d shell_keys=%d bind=%s:%d",
            len(config.llm_api_keys),
            len(config.shell_api_keys),
            config.bind_host,
            config.port,
        )
        try:
            yield
        finally:
            await bridge_db.aclose()
            await shell_db.aclose()
            logger.info("multica server stopped")

    app = FastAPI(title="db_bridge multica server", version="0.1.0", lifespan=lifespan)
    app.include_router(create_llm_router(bridge_db, config))
    app.include_router(create_shell_router(shell_db, config))

    @app.get("/healthz")
    async def healthz() -> dict[str, str]:
        return {"status": "ok", "service": "multica"}

    return app


def _bridge_config(config: MulticaConfig):
    """Build a BridgeConfig for the BridgeDB queue ops from the multica env."""
    from .config import BridgeConfig

    return BridgeConfig.from_env(
        {
            "SUPABASE_URL": config.supabase_url,
            "SUPABASE_SERVICE_ROLE_KEY": config.supabase_key,
            "BRIDGE_POLL_INTERVAL": str(config.poll_interval_s),
            "BRIDGE_MAX_BODY_BYTES": str(config.max_body_bytes),
        }
    )
