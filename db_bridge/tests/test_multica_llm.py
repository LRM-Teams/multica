"""Tests for the multica LLM router (Task 3)."""

from __future__ import annotations

import asyncio

import httpx

from db_bridge.channels import CHANNELS_BY_NAME
from db_bridge.config import BridgeConfig, MulticaConfig
from db_bridge.db import BridgeDB
from db_bridge.multica_server import create_multica_app

from _fakes import FakeSupabaseClient

CHAT = CHANNELS_BY_NAME["chat_completions"]
USER_ID = "00000000-0000-0000-0000-0000000000aa"
LLM_KEY = "llm-secret"


def _config(**overrides: str) -> MulticaConfig:
    env = {
        "SUPABASE_URL": "https://example.supabase.co",
        "SUPABASE_SERVICE_ROLE_KEY": "k",
        "MULTICA_BRIDGE_USER_ID": USER_ID,
        "MULTICA_LLM_API_KEYS": LLM_KEY,
        "BRIDGE_POLL_INTERVAL": "0.01",
        **overrides,
    }
    return MulticaConfig.from_env(env)


def _bridge_db(cfg: MulticaConfig, client: FakeSupabaseClient) -> BridgeDB:
    bcfg = BridgeConfig.from_env(
        {
            "SUPABASE_URL": cfg.supabase_url,
            "SUPABASE_SERVICE_ROLE_KEY": cfg.supabase_key,
            "BRIDGE_POLL_INTERVAL": "0.01",
            "BRIDGE_MAX_BODY_BYTES": str(cfg.max_body_bytes),
        }
    )
    return BridgeDB(bcfg, client=client)


def _client(app) -> httpx.AsyncClient:
    return httpx.AsyncClient(
        transport=httpx.ASGITransport(app=app), base_url="http://multica"
    )


def _auth(key: str = LLM_KEY) -> dict[str, str]:
    return {"Authorization": f"Bearer {key}"}


async def _executor_complete_one(db: BridgeDB, *, status, headers, body):
    while True:
        req = await db.claim_next(CHAT, "test-exec")
        if req is not None:
            await db.complete(
                CHAT,
                req.id,
                worker_id=req.worker_id,
                response_status=status,
                response_headers=headers,
                body=body,
            )
            return req
        await asyncio.sleep(0.005)


async def _executor_stream_one(
    db: BridgeDB,
    *,
    chunks,
    status: int = 200,
    headers: dict | None = None,
):
    """Simulate the AReaL-side executor relaying an SSE stream via the DB."""
    headers = headers or {"content-type": "text/event-stream"}
    while True:
        req = await db.claim_next(CHAT, "test-exec")
        if req is not None:
            break
        await asyncio.sleep(0.005)
    await db.start_stream(
        CHAT,
        req.id,
        worker_id=req.worker_id,
        response_status=status,
        response_headers=headers,
    )
    seq = 0
    for chunk in chunks:
        await db.append_chunk(
            CHAT,
            req.id,
            worker_id=req.worker_id,
            user_id=req.user_id,
            seq=seq,
            body=chunk,
            is_final=False,
        )
        seq += 1
        await asyncio.sleep(0.001)
    await db.append_chunk(
        CHAT,
        req.id,
        worker_id=req.worker_id,
        user_id=req.user_id,
        seq=seq,
        body=b"",
        is_final=True,
    )
    await db.complete(
        CHAT,
        req.id,
        worker_id=req.worker_id,
        response_status=status,
        response_headers=headers,
        body=b"",
    )
    return req


async def test_areal_model_enqueues_and_relays():
    cfg = _config()
    db = _bridge_db(cfg, FakeSupabaseClient())
    app = create_multica_app(cfg, bridge_db=db)

    exec_task = asyncio.create_task(
        _executor_complete_one(
            db,
            status=200,
            headers={"content-type": "application/json"},
            body=b'{"id":"chatcmpl-1","choices":[]}',
        )
    )
    async with _client(app) as client:
        resp = await client.post(
            "/v1/chat/completions",
            headers=_auth(),
            json={
                "model": "areal/qwen3",
                "messages": [{"role": "user", "content": "hi"}],
            },
        )
    claimed = await exec_task

    assert resp.status_code == 200
    assert resp.json()["id"] == "chatcmpl-1"
    # Enqueued under the fixed shared user, at the bridged chat path.
    row = next(iter(db.client.tables[CHAT.table].values()))
    assert row["user_id"] == USER_ID
    assert claimed.path == "/chat/completions"
    # Inbound multica credential is stripped before forwarding upstream.
    assert "authorization" not in claimed.headers
    assert "x-api-key" not in claimed.headers


async def test_alias_path_works():
    cfg = _config()
    db = _bridge_db(cfg, FakeSupabaseClient())
    app = create_multica_app(cfg, bridge_db=db)
    exec_task = asyncio.create_task(
        _executor_complete_one(db, status=200, headers={}, body=b"{}")
    )
    async with _client(app) as client:
        resp = await client.post(
            "/chat/completions",
            headers=_auth(),
            json={"model": "areal/qwen3", "messages": []},
        )
    await exec_task
    assert resp.status_code == 200


async def test_upstream_api_key_injected():
    cfg = _config(MULTICA_UPSTREAM_API_KEY="upstream-token")
    db = _bridge_db(cfg, FakeSupabaseClient())
    app = create_multica_app(cfg, bridge_db=db)
    exec_task = asyncio.create_task(
        _executor_complete_one(db, status=200, headers={}, body=b"{}")
    )
    async with _client(app) as client:
        resp = await client.post(
            "/v1/chat/completions",
            headers=_auth(),
            json={"model": "areal/qwen3", "messages": []},
        )
    claimed = await exec_task
    assert resp.status_code == 200
    assert claimed.headers.get("authorization") == "Bearer upstream-token"


async def test_non_areal_model_rejected_400():
    cfg = _config()
    db = _bridge_db(cfg, FakeSupabaseClient())
    app = create_multica_app(cfg, bridge_db=db)
    async with _client(app) as client:
        resp = await client.post(
            "/v1/chat/completions",
            headers=_auth(),
            json={"model": "gpt-4o", "messages": []},
        )
    assert resp.status_code == 400
    assert db.client.tables.get(CHAT.table, {}) == {}


async def test_stream_true_streams_chunks():
    cfg = _config()
    db = _bridge_db(cfg, FakeSupabaseClient())
    app = create_multica_app(cfg, bridge_db=db)
    chunks = [
        b'data: {"delta":"a"}\n\n',
        b'data: {"delta":"b"}\n\n',
        b"data: [DONE]\n\n",
    ]
    exec_task = asyncio.create_task(_executor_stream_one(db, chunks=chunks))
    async with _client(app) as client:
        resp = await client.post(
            "/v1/chat/completions",
            headers=_auth(),
            json={"model": "areal/qwen3", "messages": [], "stream": True},
        )
    await exec_task

    assert resp.status_code == 200
    assert resp.headers["content-type"].startswith("text/event-stream")
    assert resp.content == b"".join(chunks)
    # The parent row records the streamed request for auditing.
    row = next(iter(db.client.tables[CHAT.table].values()))
    assert row["request_meta"]["stream"] is True


async def test_stream_first_chunk_timeout_returns_504():
    cfg = _config(MULTICA_STREAM_FIRST_CHUNK_TIMEOUT="0.05")
    db = _bridge_db(cfg, FakeSupabaseClient())
    app = create_multica_app(cfg, bridge_db=db)
    # No executor ever starts the stream → the caller times out waiting.
    async with _client(app) as client:
        resp = await client.post(
            "/v1/chat/completions",
            headers=_auth(),
            json={"model": "areal/qwen3", "messages": [], "stream": True},
        )
    assert resp.status_code == 504
    # The pending row is abandoned so no executor serves it after the 504.
    row = next(iter(db.client.tables[CHAT.table].values()))
    assert row["status"] == "error"


async def test_stream_error_before_start_returns_502():
    cfg = _config()
    db = _bridge_db(cfg, FakeSupabaseClient())
    app = create_multica_app(cfg, bridge_db=db)

    async def _fail_one():
        while True:
            req = await db.claim_next(CHAT, "test-exec")
            if req is not None:
                await db.fail(
                    CHAT, req.id, worker_id=req.worker_id, error="upstream down"
                )
                return
            await asyncio.sleep(0.005)

    exec_task = asyncio.create_task(_fail_one())
    async with _client(app) as client:
        resp = await client.post(
            "/v1/chat/completions",
            headers=_auth(),
            json={"model": "areal/qwen3", "messages": [], "stream": True},
        )
    await exec_task
    assert resp.status_code == 502
    assert resp.json()["detail"] == "upstream down"


async def test_missing_key_returns_401():
    cfg = _config()
    db = _bridge_db(cfg, FakeSupabaseClient())
    app = create_multica_app(cfg, bridge_db=db)
    async with _client(app) as client:
        resp = await client.post(
            "/v1/chat/completions",
            json={"model": "areal/qwen3", "messages": []},
        )
    assert resp.status_code == 401


async def test_oversized_body_returns_413():
    cfg = _config(BRIDGE_MAX_BODY_BYTES="32")
    db = _bridge_db(cfg, FakeSupabaseClient())
    app = create_multica_app(cfg, bridge_db=db)
    async with _client(app) as client:
        resp = await client.post(
            "/v1/chat/completions",
            headers=_auth(),
            json={
                "model": "areal/qwen3",
                "messages": [{"role": "user", "content": "x" * 200}],
            },
        )
    assert resp.status_code == 413
    assert db.client.tables.get(CHAT.table, {}) == {}


async def test_timeout_returns_504():
    cfg = _config(MULTICA_CHAT_TIMEOUT="0.05")
    db = _bridge_db(cfg, FakeSupabaseClient())
    app = create_multica_app(cfg, bridge_db=db)
    async with _client(app) as client:
        resp = await client.post(
            "/v1/chat/completions",
            headers=_auth(),
            json={"model": "areal/qwen3", "messages": []},
        )
    assert resp.status_code == 504
    # Abandoned so a late executor never processes it.
    row = next(iter(db.client.tables[CHAT.table].values()))
    assert row["status"] == "error"
