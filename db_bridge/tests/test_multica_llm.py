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
            json={"model": "areal/qwen3", "messages": [{"role": "user", "content": "hi"}]},
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


async def test_stream_true_rejected_400():
    cfg = _config()
    db = _bridge_db(cfg, FakeSupabaseClient())
    app = create_multica_app(cfg, bridge_db=db)
    async with _client(app) as client:
        resp = await client.post(
            "/v1/chat/completions",
            headers=_auth(),
            json={"model": "areal/qwen3", "messages": [], "stream": True},
        )
    assert resp.status_code == 400
    assert db.client.tables.get(CHAT.table, {}) == {}


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
            json={"model": "areal/qwen3", "messages": [{"role": "user", "content": "x" * 200}]},
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
