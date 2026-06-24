"""Entrypoint / lifecycle tests (Task 8): arg parsing, stale reclaim, shutdown."""

from __future__ import annotations

import asyncio
import logging
import time

import httpx
import pytest

from db_bridge.channels import CHANNELS_BY_NAME
from db_bridge.config import BridgeConfig
from db_bridge.db import BridgeDB
from db_bridge.entrypoints import _configure_logging, _parse_side
from db_bridge.executor import Executor

from _fakes import FakeSupabaseClient

SET_REWARD = CHANNELS_BY_NAME["rl_set_reward"]
USER_ID = "00000000-0000-0000-0000-00000000000a"


def _config(**overrides: str) -> BridgeConfig:
    env = {
        "SUPABASE_URL": "https://example.supabase.co",
        "SUPABASE_SERVICE_ROLE_KEY": "k",
        "BRIDGE_POLL_INTERVAL": "0.01",
        "BRIDGE_USER_ID": USER_ID,
        "BRIDGE_STALE_SECONDS": "60",
        "BRIDGE_CONCURRENCY_CHAT_COMPLETIONS": "1",
        **overrides,
    }
    return BridgeConfig.from_env(env)


# -- argument parsing -------------------------------------------------------


def test_parse_side_valid():
    assert _parse_side(["--side", "leagent"], "p") == "leagent"
    assert _parse_side(["--side", "areal"], "p") == "areal"


def test_parse_side_rejects_missing_or_invalid():
    with pytest.raises(SystemExit):
        _parse_side([], "p")
    with pytest.raises(SystemExit):
        _parse_side(["--side", "nope"], "p")


def test_configure_logging_suppresses_polling_noise():
    _configure_logging("info")
    assert logging.getLogger("db_bridge").level == logging.INFO
    assert logging.getLogger("httpx").level == logging.WARNING
    assert logging.getLogger("postgrest").level == logging.WARNING
    assert logging.getLogger("uvicorn.access").level == logging.WARNING


# -- stale-claim reaper (built into claim_next) -----------------------------


async def test_fresh_claimed_row_not_reclaimed():
    cfg = _config()
    db = BridgeDB(cfg, client=FakeSupabaseClient())
    row_id = await db.insert_request(
        SET_REWARD,
        user_id=USER_ID,
        method="POST",
        path="/rl/set_reward",
        headers={},
        content_type=None,
        body=b"{}",
    )
    store = db.client.tables[SET_REWARD.table]
    store[row_id]["status"] = "claimed"
    store[row_id]["claimed_epoch"] = time.time()  # just now -> not stale
    assert await db.claim_next(SET_REWARD, "w") is None


async def test_stale_claimed_row_is_reclaimed():
    cfg = _config()
    db = BridgeDB(cfg, client=FakeSupabaseClient())
    row_id = await db.insert_request(
        SET_REWARD,
        user_id=USER_ID,
        method="POST",
        path="/rl/set_reward",
        headers={},
        content_type=None,
        body=b"{}",
    )
    store = db.client.tables[SET_REWARD.table]
    store[row_id]["status"] = "claimed"
    store[row_id]["worker_id"] = "dead-worker"
    store[row_id]["claimed_epoch"] = time.time() - 10_000  # well past stale window

    reclaimed = await db.claim_next(SET_REWARD, "live-worker")
    assert reclaimed is not None
    assert reclaimed.id == row_id
    assert store[row_id]["worker_id"] == "live-worker"


async def test_executor_recovers_abandoned_row():
    """A row abandoned in 'claimed' is reclaimed and processed by the pool."""
    cfg = _config(BRIDGE_CONCURRENCY_RL_SET_REWARD="2")
    db = BridgeDB(cfg, client=FakeSupabaseClient())
    row_id = await db.insert_request(
        SET_REWARD,
        user_id=USER_ID,
        method="POST",
        path="/rl/set_reward",
        headers={},
        content_type="application/json",
        body=b"{}",
    )
    store = db.client.tables[SET_REWARD.table]
    store[row_id]["status"] = "claimed"
    store[row_id]["worker_id"] = "dead-worker"
    store[row_id]["claimed_epoch"] = time.time() - 10_000

    def handler(request: httpx.Request) -> httpx.Response:
        return httpx.Response(200, json={"recovered": True})

    ex = Executor(
        db,
        "areal",
        config=cfg,
        client=httpx.AsyncClient(transport=httpx.MockTransport(handler)),
    )
    run_task = asyncio.create_task(ex.run())
    try:
        for _ in range(200):
            if store[row_id]["status"] == "done":
                break
            await asyncio.sleep(0.01)
    finally:
        ex.stop()
        await run_task

    assert store[row_id]["status"] == "done"
    assert store[row_id]["response_status"] == 200


async def test_executor_periodically_cleans_stale_terminal_rows():
    cfg = _config(
        BRIDGE_CLEANUP_INTERVAL="0.01",
        BRIDGE_ROW_RETENTION_SECONDS="1",
        BRIDGE_CLEANUP_BATCH_LIMIT="10",
        BRIDGE_CONCURRENCY_RL_SET_REWARD="1",
    )
    db = BridgeDB(cfg, client=FakeSupabaseClient())
    row_id = await db.insert_request(
        SET_REWARD,
        user_id=USER_ID,
        method="POST",
        path="/rl/set_reward",
        headers={},
        content_type=None,
        body=b"{}",
    )
    store = db.client.tables[SET_REWARD.table]
    store[row_id]["status"] = "done"
    store[row_id]["completed_at"] = time.time() - 10

    ex = Executor(
        db,
        "areal",
        config=cfg,
        client=httpx.AsyncClient(
            transport=httpx.MockTransport(lambda r: httpx.Response(200))
        ),
    )
    run_task = asyncio.create_task(ex.run())
    try:
        for _ in range(200):
            if row_id not in store:
                break
            await asyncio.sleep(0.01)
    finally:
        ex.stop()
        await run_task

    assert row_id not in store


# -- graceful shutdown ------------------------------------------------------


async def test_executor_run_stops_cleanly_and_quickly():
    cfg = _config()
    db = BridgeDB(cfg, client=FakeSupabaseClient())
    ex = Executor(
        db,
        "areal",
        config=cfg,
        client=httpx.AsyncClient(
            transport=httpx.MockTransport(lambda r: httpx.Response(200))
        ),
    )
    run_task = asyncio.create_task(ex.run())
    await asyncio.sleep(0.05)  # let the pool spin up
    ex.stop()
    # Must finish promptly after stop().
    await asyncio.wait_for(run_task, timeout=5)
    assert run_task.done()
