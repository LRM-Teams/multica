"""Tests for the executor worker pool (Task 5: end-to-end set_reward)."""

from __future__ import annotations

import asyncio

import httpx

from db_bridge.channels import CHANNELS_BY_NAME
from db_bridge.config import BridgeConfig
from db_bridge.db import BridgeDB
from db_bridge.executor import Executor
from db_bridge.stub_server import create_stub_app

from _fakes import FakeSupabaseClient

SET_REWARD = CHANNELS_BY_NAME["rl_set_reward"]
USER_ID = "00000000-0000-0000-0000-00000000000a"


def _config(**overrides: str) -> BridgeConfig:
    env = {
        "SUPABASE_URL": "https://example.supabase.co",
        "SUPABASE_SERVICE_ROLE_KEY": "k",
        "BRIDGE_POLL_INTERVAL": "0.01",
        "BRIDGE_USER_ID": USER_ID,
        # Keep the pool small so tests spawn few workers.
        "BRIDGE_CONCURRENCY_CHAT_COMPLETIONS": "2",
        **overrides,
    }
    return BridgeConfig.from_env(env)


def _exec_client(handler) -> httpx.AsyncClient:
    return httpx.AsyncClient(transport=httpx.MockTransport(handler))


async def test_process_one_forwards_and_completes():
    cfg = _config()
    db = BridgeDB(cfg, client=FakeSupabaseClient())
    captured: dict = {}

    def handler(request: httpx.Request) -> httpx.Response:
        captured["method"] = request.method
        captured["url"] = str(request.url)
        captured["auth"] = request.headers.get("authorization")
        captured["body"] = request.content
        return httpx.Response(
            200, json={"interaction_count": 5}, headers={"x-test": "1"}
        )

    ex = Executor(db, "areal", config=cfg, client=_exec_client(handler))
    row_id = await db.insert_request(
        SET_REWARD,
        user_id=USER_ID,
        method="POST",
        path="/rl/set_reward",
        headers={"authorization": "Bearer s", "content-type": "application/json"},
        content_type="application/json",
        body=b'{"reward":1.0}',
    )

    assert await ex.process_one(SET_REWARD, "w0") is True
    # Pass-through: method, path, auth and body replayed verbatim to upstream.
    assert captured["method"] == "POST"
    assert captured["url"].endswith("/rl/set_reward")
    assert captured["auth"] == "Bearer s"
    assert captured["body"] == b'{"reward":1.0}'
    # Configured gateway upstream host was used.
    assert "127.0.0.1:8080" in captured["url"]

    resp = await db.wait_for_response(SET_REWARD, row_id, timeout=1.0, user_id=USER_ID)
    assert resp is not None
    assert resp.status == "done"
    assert resp.response_status == 200
    assert resp.body == b'{"interaction_count":5}'
    assert resp.headers.get("x-test") == "1"
    # Decoded body relay must drop content-length so the stub recomputes it.
    assert "content-length" not in resp.headers


async def test_process_one_noop_when_empty():
    cfg = _config()
    db = BridgeDB(cfg, client=FakeSupabaseClient())
    ex = Executor(
        db, "areal", config=cfg, client=_exec_client(lambda r: httpx.Response(200))
    )
    assert await ex.process_one(SET_REWARD, "w0") is False


async def test_transport_failure_marks_error():
    cfg = _config()
    db = BridgeDB(cfg, client=FakeSupabaseClient())

    def handler(request: httpx.Request) -> httpx.Response:
        raise httpx.ConnectError("connection refused")

    ex = Executor(db, "areal", config=cfg, client=_exec_client(handler))
    row_id = await db.insert_request(
        SET_REWARD,
        user_id=USER_ID,
        method="POST",
        path="/rl/set_reward",
        headers={},
        content_type="application/json",
        body=b"{}",
    )
    assert await ex.process_one(SET_REWARD, "w0") is True
    resp = await db.wait_for_response(SET_REWARD, row_id, timeout=1.0, user_id=USER_ID)
    assert resp is not None
    assert resp.status == "error"
    assert "ConnectError" in (resp.error or "")


async def test_end_to_end_stub_db_executor_roundtrip():
    cfg = _config()
    db = BridgeDB(cfg, client=FakeSupabaseClient())

    def handler(request: httpx.Request) -> httpx.Response:
        assert request.headers.get("authorization") == "Bearer s"
        return httpx.Response(200, json={"interaction_count": 9})

    ex = Executor(db, "areal", config=cfg, client=_exec_client(handler))
    run_task = asyncio.create_task(ex.run())
    try:
        stub = create_stub_app(db, "leagent", cfg)
        async with httpx.AsyncClient(
            transport=httpx.ASGITransport(app=stub), base_url="http://stub"
        ) as client:
            resp = await client.post(
                "/rl/set_reward",
                headers={"Authorization": "Bearer s", "X-Bridge-User-Id": USER_ID},
                json={"reward": 1.0},
            )
        assert resp.status_code == 200
        assert resp.json() == {"interaction_count": 9}
    finally:
        ex.stop()
        await run_task


async def test_no_duplicate_processing_under_concurrency():
    n_rows = 20
    cfg = _config(BRIDGE_CONCURRENCY_RL_SET_REWARD="8")
    db = BridgeDB(cfg, client=FakeSupabaseClient())
    for _ in range(n_rows):
        await db.insert_request(
            SET_REWARD,
            user_id=USER_ID,
            method="POST",
            path="/rl/set_reward",
            headers={},
            content_type="application/json",
            body=b"{}",
        )

    forwarded = {"n": 0}

    def handler(request: httpx.Request) -> httpx.Response:
        forwarded["n"] += 1
        return httpx.Response(200, json={"ok": True})

    ex = Executor(db, "areal", config=cfg, client=_exec_client(handler))
    run_task = asyncio.create_task(ex.run())
    try:

        async def all_done() -> bool:
            for _ in range(500):
                rows = list(db.client.tables.get(SET_REWARD.table, {}).values())
                if len(rows) == n_rows and all(r["status"] == "done" for r in rows):
                    return True
                await asyncio.sleep(0.01)
            return False

        assert await all_done()
    finally:
        ex.stop()
        await run_task

    # Each row forwarded exactly once -> no double processing.
    assert forwarded["n"] == n_rows
    complete_calls = [c for c in db.client.rpc_calls if c[0] == "bridge_complete"]
    assert len(complete_calls) == n_rows
