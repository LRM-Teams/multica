"""Tests for the generic stub server (Task 4: /rl/set_reward)."""

from __future__ import annotations

import asyncio

import httpx

from db_bridge.channels import CHANNELS_BY_NAME
from db_bridge.config import BridgeConfig
from db_bridge.db import BridgeDB
from db_bridge.stub_server import create_stub_app

from _fakes import FakeSupabaseClient

SET_REWARD = CHANNELS_BY_NAME["rl_set_reward"]
USER_ID = "00000000-0000-0000-0000-00000000000a"


def _config(**overrides: str) -> BridgeConfig:
    env = {
        "SUPABASE_URL": "https://example.supabase.co",
        "SUPABASE_SERVICE_ROLE_KEY": "k",
        "BRIDGE_POLL_INTERVAL": "0.01",
        **overrides,
    }
    return BridgeConfig.from_env(env)


def _client(app) -> httpx.AsyncClient:
    return httpx.AsyncClient(
        transport=httpx.ASGITransport(app=app), base_url="http://stub"
    )


async def _executor_complete_one(db: BridgeDB, channel, *, status, headers, body):
    """Minimal stand-in executor: claim the next request and complete it."""
    while True:
        req = await db.claim_next(channel, "test-exec")
        if req is not None:
            await db.complete(
                channel,
                req.id,
                worker_id=req.worker_id,
                response_status=status,
                response_headers=headers,
                body=body,
            )
            return req
        await asyncio.sleep(0.005)


async def test_set_reward_roundtrip_passthrough():
    cfg = _config()
    db = BridgeDB(cfg, client=FakeSupabaseClient())
    app = create_stub_app(db, "leagent", cfg)

    exec_task = asyncio.create_task(
        _executor_complete_one(
            db,
            SET_REWARD,
            status=200,
            headers={"content-type": "application/json"},
            body=b'{"interaction_count": 2}',
        )
    )
    async with _client(app) as client:
        resp = await client.post(
            "/rl/set_reward",
            headers={
                "Authorization": "Bearer session-key",
                "X-Bridge-User-Id": USER_ID,
            },
            json={"interaction_id": None, "reward": 1.0},
        )
    claimed = await exec_task

    assert resp.status_code == 200
    assert resp.json() == {"interaction_count": 2}
    # Pass-through auth + body captured verbatim for the executor.
    assert claimed.method == "POST"
    assert claimed.path == "/rl/set_reward"
    assert claimed.headers.get("authorization") == "Bearer session-key"
    assert claimed.headers.get("content-type") == "application/json"
    assert b"reward" in claimed.body


async def test_missing_user_id_returns_400_and_does_not_enqueue():
    cfg = _config()
    db = BridgeDB(cfg, client=FakeSupabaseClient())
    app = create_stub_app(db, "leagent", cfg)

    async with _client(app) as client:
        resp = await client.post("/rl/set_reward", json={"reward": 1.0})

    assert resp.status_code == 400
    assert "X-Bridge-User-Id" in resp.json()["detail"]
    assert db.client.tables.get(SET_REWARD.table, {}) == {}


async def test_user_id_header_is_stored_but_not_forwarded():
    cfg = _config()
    db = BridgeDB(cfg, client=FakeSupabaseClient())
    app = create_stub_app(db, "leagent", cfg)

    exec_task = asyncio.create_task(
        _executor_complete_one(
            db,
            SET_REWARD,
            status=200,
            headers={},
            body=b"{}",
        )
    )
    async with _client(app) as client:
        resp = await client.post(
            "/rl/set_reward",
            headers={"X-Bridge-User-Id": USER_ID},
            json={"reward": 1.0},
        )
    claimed = await exec_task

    assert resp.status_code == 200
    row = next(iter(db.client.tables[SET_REWARD.table].values()))
    assert row["user_id"] == USER_ID
    assert "x-bridge-user-id" not in claimed.headers


async def test_timeout_returns_504():
    cfg = _config(BRIDGE_TIMEOUT_RL_SET_REWARD="0.05")
    db = BridgeDB(cfg, client=FakeSupabaseClient())
    app = create_stub_app(db, "leagent", cfg)
    async with _client(app) as client:
        resp = await client.post(
            "/rl/set_reward",
            headers={"X-Bridge-User-Id": USER_ID},
            json={"reward": 1.0},
        )
    assert resp.status_code == 504
    assert "timed out" in resp.json()["detail"]


async def test_timeout_abandons_request_so_executor_does_not_process_later():
    cfg = _config(BRIDGE_TIMEOUT_RL_SET_REWARD="0.05")
    db = BridgeDB(cfg, client=FakeSupabaseClient())
    app = create_stub_app(db, "leagent", cfg)

    async with _client(app) as client:
        resp = await client.post(
            "/rl/set_reward",
            headers={"X-Bridge-User-Id": USER_ID},
            json={"reward": 1.0},
        )

    assert resp.status_code == 504
    row = next(iter(db.client.tables[SET_REWARD.table].values()))
    assert row["status"] == "error"
    assert "timed out" in row["error"]
    assert await db.claim_next(SET_REWARD, "late-worker", user_id=USER_ID) is None


async def test_oversized_body_returns_413():
    cfg = _config(BRIDGE_MAX_BODY_BYTES="16")
    db = BridgeDB(cfg, client=FakeSupabaseClient())
    app = create_stub_app(db, "leagent", cfg)
    async with _client(app) as client:
        resp = await client.post(
            "/rl/set_reward",
            headers={"X-Bridge-User-Id": USER_ID},
            json={"reward": 1.0, "pad": "x" * 100},
        )
    assert resp.status_code == 413
    # Nothing should have been enqueued.
    assert db.client.tables.get(SET_REWARD.table, {}) == {}


async def test_relay_error_returns_502():
    cfg = _config()
    db = BridgeDB(cfg, client=FakeSupabaseClient())
    app = create_stub_app(db, "leagent", cfg)

    async def fail_one():
        while True:
            req = await db.claim_next(SET_REWARD, "test-exec")
            if req is not None:
                await db.fail(
                    SET_REWARD,
                    req.id,
                    worker_id=req.worker_id,
                    error="connection refused",
                )
                return
            await asyncio.sleep(0.005)

    task = asyncio.create_task(fail_one())
    async with _client(app) as client:
        resp = await client.post(
            "/rl/set_reward",
            headers={"X-Bridge-User-Id": USER_ID},
            json={"reward": 1.0},
        )
    await task
    assert resp.status_code == 502
    assert resp.json()["detail"] == "connection refused"


async def test_healthz_lists_channels():
    cfg = _config()
    db = BridgeDB(cfg, client=FakeSupabaseClient())
    app = create_stub_app(db, "leagent", cfg)
    async with _client(app) as client:
        resp = await client.get("/healthz")
    assert resp.status_code == 200
    payload = resp.json()
    assert payload["side"] == "leagent"
    assert "rl_set_reward" in payload["channels"]
    assert "chat_completions" in payload["channels"]
