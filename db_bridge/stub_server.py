"""Generic transparent stub server for the DB bridge.

The stub mirrors the *remote* API the local app calls. For each channel whose
stub runs on this side it registers the channel's path; the handler captures the
raw request (method, path+query, headers, body), enqueues it via ``BridgeDB``,
polls for the response, and returns it verbatim. A timeout yields 504; an
executor-side failure yields 502; an oversized body yields 413.

Bind to 127.0.0.1 only -- the local application is the sole intended caller.
"""

from __future__ import annotations

import asyncio
import contextlib
import json
import logging
import time
import uuid

import httpx
from fastapi import FastAPI, Request, Response
from fastapi.responses import JSONResponse

from . import crypto, relay
from .channels import Channel, Side, stub_channels
from .config import BridgeConfig
from .db import BridgeDB
from .metrics import get_metrics

logger = logging.getLogger("db_bridge.stub")

# Above this body size, skip detailed multipart parsing for audit metadata to
# avoid spooling huge uploads to disk just to record file names/sizes.
_MULTIPART_META_MAX_BYTES = 16 * 1024 * 1024
# Models routed through the AReaL db-bridge relay. pi's areal provider registers
# ids like "areal-default", "areal-distill" and "areal/<vendor>/<model>", so match
# both the "areal/" namespace and the "areal-" alias family (not just "areal/").
_AREAL_MODEL_PREFIXES = ("areal/", "areal-")
_BRIDGE_USER_HEADER = "x-bridge-user-id"


def _full_path(request: Request) -> str:
    query = request.url.query
    return f"{request.url.path}?{query}" if query else request.url.path


def _resolve_user_id(request: Request, config: BridgeConfig) -> str | None:
    raw = request.headers.get(_BRIDGE_USER_HEADER) or config.bridge_user_id
    if raw is None or not raw.strip():
        return None
    try:
        return str(uuid.UUID(raw.strip()))
    except ValueError:
        return None


def _chat_model_name(body: bytes) -> str | None:
    try:
        payload = json.loads(body)
    except json.JSONDecodeError:
        return None
    if not isinstance(payload, dict):
        return None
    model = payload.get("model")
    return model if isinstance(model, str) else None


def _is_areal_model(model: str | None) -> bool:
    """True when the model id targets the AReaL bridge relay (areal/ or areal-)."""
    if not model:
        return False
    m = model.strip().lower()
    return m == "areal" or m.startswith(_AREAL_MODEL_PREFIXES)


def _should_bypass_chat_completions(channel: Channel, body: bytes) -> bool:
    if channel.name != "chat_completions":
        return False
    model = _chat_model_name(body)
    # Bypass (direct forward) only for non-areal models. All areal-* / areal/*
    # ids must take the DB-bridge relay path (Supabase -> executor -> gateway).
    # A missing model name is not bypassed (relayed), matching prior behavior.
    return bool(model) and not _is_areal_model(model)


def _is_streaming_chat(channel: Channel, body: bytes) -> bool:
    """True when a chat_completions request opts into SSE streaming.

    The loopback stub relays buffered request/response bodies only; streaming
    is served exclusively by the multica public server (which owns the
    bridge_stream_chunks relay). Rejecting ``stream: true`` here keeps the
    shared rpc_chat_completions table's buffered path well-defined.
    """
    if channel.name != "chat_completions":
        return False
    try:
        payload = json.loads(body)
    except json.JSONDecodeError:
        return False
    return isinstance(payload, dict) and bool(payload.get("stream"))


def _strip_stream_flag(body: bytes) -> bytes:
    """Drop stream/stream_options so the request takes the buffered relay path.

    pi clients always send ``stream: true``. The stub has no token-streaming
    channel, so it relays the request buffered and re-emits the completed
    response as SSE (see ``_sse_from_buffered``). Stripping the flag here keeps
    the shared ``rpc_chat_completions`` buffered path well-defined.
    """
    try:
        payload = json.loads(body)
    except (json.JSONDecodeError, ValueError):
        return body
    if isinstance(payload, dict) and ("stream" in payload or "stream_options" in payload):
        payload.pop("stream", None)
        payload.pop("stream_options", None)
        return json.dumps(payload).encode()
    return body


def _sse_from_buffered(body: bytes, fallback_model: str | None) -> Response:
    """Wrap a buffered OpenAI chat-completion JSON body as an SSE event-stream.

    Emits the OpenAI streaming chunk shape (role delta -> content delta ->
    finish_reason) terminated by ``[DONE]``. This is fake streaming (a single
    content chunk), which is sufficient for pi clients that require an SSE
    response but do not depend on true incremental token delivery.
    """
    try:
        payload = json.loads(body)
    except (json.JSONDecodeError, ValueError):
        payload = {}
    if not isinstance(payload, dict):
        payload = {}
    choice = (payload.get("choices") or [{}])[0] or {}
    content = (choice.get("message") or {}).get("content") or ""
    cid = payload.get("id") or "chatcmpl-stub"
    model = payload.get("model") or fallback_model or "areal-default"

    def _chunk(delta: dict, finish: str | None) -> dict:
        return {
            "id": cid,
            "object": "chat.completion.chunk",
            "model": model,
            "choices": [{"index": 0, "delta": delta, "finish_reason": finish}],
        }

    frames = [
        _chunk({"role": "assistant", "content": ""}, None),
        _chunk({"content": content}, None),
        _chunk({}, choice.get("finish_reason") or "stop"),
    ]
    sse = b"".join(
        f"data: {json.dumps(frame, ensure_ascii=False)}\n\n".encode() for frame in frames
    )
    sse += b"data: [DONE]\n\n"
    return Response(
        content=sse,
        status_code=200,
        headers={
            "Content-Type": "text/event-stream",
            "Cache-Control": "no-cache",
            "Connection": "close",
        },
    )


def _build_direct_client() -> httpx.AsyncClient:
    return httpx.AsyncClient(trust_env=False, follow_redirects=False)


async def _forward_direct_chat_completion(
    request: Request,
    channel: Channel,
    config: BridgeConfig,
    body: bytes,
) -> Response:
    headers = relay.filter_request_headers(relay.capture_headers(request.headers))
    url = str(request.url)
    async with _build_direct_client() as client:
        resp = await client.request(
            request.method,
            url,
            headers=headers,
            content=body,
            timeout=config.timeout_for(channel),
        )
    return Response(
        content=resp.content,
        status_code=resp.status_code,
        headers=relay.filter_response_headers(resp.headers),
    )


async def _build_request_meta(
    request: Request, channel: Channel, body: bytes
) -> dict[str, object]:
    """Audit metadata stored alongside the relayed request.

    The raw body (including any multipart file payloads) is what gets relayed;
    this only records human-readable form/file metadata for auditability and is
    strictly best-effort -- failures never block the relay.
    """
    meta: dict[str, object] = {"content_length": len(body)}
    if channel.kind != "multipart" or not body:
        return meta
    if len(body) > _MULTIPART_META_MAX_BYTES:
        meta["multipart_meta_skipped"] = "body too large to parse for audit"
        return meta
    try:
        form = await request.form()
        fields: dict[str, str] = {}
        files: list[dict[str, object]] = []
        for key, value in form.multi_items():
            filename = getattr(value, "filename", None)
            if filename is not None:  # UploadFile
                files.append(
                    {
                        "field": key,
                        "filename": filename,
                        "content_type": getattr(value, "content_type", None),
                        "size": getattr(value, "size", None),
                    }
                )
            else:
                text = value if isinstance(value, str) else str(value)
                fields[key] = text[:500]
        meta["form_fields"] = fields
        meta["files"] = files
    except Exception as exc:  # noqa: BLE001 -- audit only, never fail the relay
        meta["multipart_meta_error"] = f"{type(exc).__name__}: {exc}"
    return meta


def _make_handler(db: BridgeDB, channel: Channel, config: BridgeConfig, cipher):
    timeout = config.timeout_for(channel)
    max_body = config.max_body_bytes
    metrics = get_metrics()

    async def handler(request: Request) -> Response:
        body = await request.body()
        if len(body) > max_body:
            logger.warning(
                "stub rejecting oversized body channel=%s size=%d max=%d",
                channel.name,
                len(body),
                max_body,
            )
            metrics.record_result(channel.name, "error")
            return JSONResponse(
                status_code=413,
                content={
                    "detail": (
                        f"request body {len(body)} bytes exceeds bridge limit "
                        f"{max_body} bytes for channel {channel.name}"
                    )
                },
            )

        want_stream = _is_streaming_chat(channel, body)
        if want_stream:
            # Fake-SSE: relay buffered (drop the stream flag), then re-emit the
            # completed response as a single SSE sequence so pi clients (which
            # always send stream=true) receive a valid event-stream instead of a
            # 400. See _strip_stream_flag / _sse_from_buffered.
            logger.info(
                "stub wrapping streaming chat completion as SSE channel=%s path=%s",
                channel.name,
                _full_path(request),
            )
            body = _strip_stream_flag(body)

        user_id = _resolve_user_id(request, config)
        if user_id is None:
            metrics.record_result(channel.name, "error")
            return JSONResponse(
                status_code=400,
                content={
                    "detail": (
                        "X-Bridge-User-Id header or BRIDGE_USER_ID must be "
                        "set to a valid user UUID"
                    )
                },
            )

        headers = relay.capture_headers(request.headers)
        if _should_bypass_chat_completions(channel, body):
            logger.info(
                "stub forwarding chat completion directly channel=%s model=%s path=%s bytes=%d",
                channel.name,
                _chat_model_name(body),
                _full_path(request),
                len(body),
            )
            try:
                bypass_resp = await _forward_direct_chat_completion(
                    request,
                    channel,
                    config,
                    body,
                )
                if want_stream and 200 <= bypass_resp.status_code < 300:
                    return _sse_from_buffered(bypass_resp.body, _chat_model_name(body))
                return bypass_resp
            except httpx.HTTPError as exc:
                metrics.record_result(channel.name, "error")
                return JSONResponse(
                    status_code=502,
                    content={"detail": f"chat completion bypass failed: {exc}"},
                )

        # Optional: encrypt sensitive tokens at rest before they touch the DB.
        headers = relay.filter_request_headers(headers)
        headers = crypto.encrypt_headers(headers, cipher)
        meta = await _build_request_meta(request, channel, body)

        started = time.monotonic()
        metrics.record_enqueue(channel.name, len(body))
        row_id = await db.insert_request(
            channel,
            user_id=user_id,
            method=request.method,
            path=_full_path(request),
            headers=headers,
            content_type=request.headers.get("content-type"),
            body=body,
            meta=meta,
        )
        logger.info(
            "stub enqueued request channel=%s id=%s path=%s bytes=%d",
            channel.name,
            row_id,
            _full_path(request),
            len(body),
        )

        result = await db.wait_for_response(channel, row_id, timeout, user_id=user_id)
        if result is None:
            error = (
                f"bridge timed out after {timeout:.0f}s waiting for "
                f"channel {channel.name} (request {row_id})"
            )
            logger.warning(
                "stub timeout channel=%s id=%s after %.1fs",
                channel.name,
                row_id,
                timeout,
            )
            try:
                abandoned = await db.abandon(
                    channel, row_id, user_id=user_id, error=error
                )
                if not abandoned:
                    logger.info(
                        "stub timeout found terminal row channel=%s id=%s",
                        channel.name,
                        row_id,
                    )
            except Exception as exc:  # noqa: BLE001 -- timeout response must still return
                logger.warning(
                    "stub failed to abandon timed-out row channel=%s id=%s: %s",
                    channel.name,
                    row_id,
                    exc,
                )
            metrics.record_result(channel.name, "timeout")
            return JSONResponse(
                status_code=504,
                content={"detail": error},
            )
        if result.status == "error":
            logger.warning(
                "stub relay error channel=%s id=%s: %s",
                channel.name,
                row_id,
                result.error,
            )
            metrics.record_result(channel.name, "error")
            return JSONResponse(
                status_code=502,
                content={"detail": result.error or "bridge relay error"},
            )

        metrics.record_result(
            channel.name,
            "done",
            response_bytes=len(result.body),
            latency_s=time.monotonic() - started,
        )
        logger.info(
            "stub returning response channel=%s id=%s status=%s bytes=%d latency_ms=%.1f",
            channel.name,
            row_id,
            result.response_status or 200,
            len(result.body),
            (time.monotonic() - started) * 1000,
        )
        if want_stream and 200 <= (result.response_status or 200) < 300:
            return _sse_from_buffered(result.body, _chat_model_name(body))
        return Response(
            content=result.body,
            status_code=result.response_status or 200,
            headers=relay.filter_response_headers(result.headers),
        )

    handler.__name__ = f"stub_{channel.name}"
    return handler


async def _stats_loop(side: Side, interval: float) -> None:
    metrics = get_metrics()
    while True:
        await asyncio.sleep(interval)
        snap = metrics.snapshot()
        if snap:
            logger.info("stub stats side=%s %s", side, snap)


def create_stub_app(
    db: BridgeDB, side: Side, config: BridgeConfig | None = None
) -> FastAPI:
    """Build the stub FastAPI app serving every channel hosted on ``side``.

    A lifespan hook connects the DB on startup and closes it on shutdown when
    run under a real ASGI server (uvicorn). Test harnesses that drive the app
    over ``httpx.ASGITransport`` do not trigger lifespan events, so they should
    inject an already-connected ``db``; ``BridgeDB.connect`` is a no-op when a
    client is already present.
    """
    config = config or db.config
    cipher = config.build_cipher(
        required=any(channel.group == "multica_api" for channel in stub_channels(side))
    )

    @contextlib.asynccontextmanager
    async def lifespan(_app: FastAPI):
        await db.connect()
        stats_task = (
            asyncio.create_task(_stats_loop(side, config.stats_interval_s))
            if config.stats_interval_s > 0
            else None
        )
        logger.info(
            "stub ready side=%s channels=%s",
            side,
            [c.name for c in stub_channels(side)],
        )
        logger.debug("stub started side=%s host bound by server", side)
        try:
            yield
        finally:
            if stats_task is not None:
                stats_task.cancel()
                with contextlib.suppress(asyncio.CancelledError):
                    await stats_task
            await db.aclose()
            logger.debug("stub stopped side=%s", side)

    app = FastAPI(title=f"db_bridge stub ({side})", version="0.1.0", lifespan=lifespan)

    channels = stub_channels(side)
    for channel in channels:
        app.add_api_route(
            channel.path,
            _make_handler(db, channel, config, cipher),
            methods=[channel.method],
            name=f"stub_{channel.name}",
        )

    # pi / OpenAI SDKs append /v1/chat/completions to baseUrl, but the
    # stub's canonical path is /chat/completions.  Add an alias so both work.
    _cc = {c.name: c for c in channels}.get("chat_completions")
    if _cc is not None:
        _handler = _make_handler(db, _cc, config, cipher)
        app.add_api_route(
            "/v1/chat/completions",
            _handler,
            methods=["POST"],
            name="stub_chat_completions_v1",
        )
        # pi daemon v0.3.64 uses the Anthropic-compatible /responses
        # endpoint with the responses API format.  Route it through the
        # same chat_completions channel; the areal-side gateway forwards
        # to DeepSeek's anthropic endpoint which understands both formats.
        app.add_api_route(
            "/responses",
            _handler,
            methods=["POST"],
            name="stub_chat_completions_responses",
        )

    @app.get("/healthz")
    async def healthz() -> dict[str, object]:
        return {"status": "ok", "side": side, "channels": [c.name for c in channels]}

    return app
