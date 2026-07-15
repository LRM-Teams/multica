"""rl_close_segment gateway channel test: POST /rl/close_segment.

The multica arealrl client posts close_segment to the le-agent-side stub with a
session-key Bearer token; the areal-side executor forwards it to the real AReaL
gateway. Verifies the stub no longer 404s (the channel is registered) and the
session-key Authorization passes through end to end, mirroring rl_set_reward.
"""

from __future__ import annotations

import asyncio
import contextlib

import httpx

from db_bridge.config import BridgeConfig
from db_bridge.db import BridgeDB
from db_bridge.executor import Executor
from db_bridge.stub_server import create_stub_app

from _fakes import FakeSupabaseClient

USER_ID = "00000000-0000-0000-0000-00000000000a"


def _config(**overrides: str) -> BridgeConfig:
    env = {
        "SUPABASE_URL": "https://example.supabase.co",
        "SUPABASE_SERVICE_ROLE_KEY": "k",
        "BRIDGE_POLL_INTERVAL": "0.01",
        "BRIDGE_USER_ID": USER_ID,
        **overrides,
    }
    return BridgeConfig.from_env(env)


@contextlib.asynccontextmanager
async def gateway_harness(handler, **cfg_overrides):
    cfg = _config(**cfg_overrides)
    db = BridgeDB(cfg, client=FakeSupabaseClient())
    ex = Executor(
        db,
        "areal",
        config=cfg,
        client=httpx.AsyncClient(transport=httpx.MockTransport(handler)),
    )
    run_task = asyncio.create_task(ex.run())
    stub = create_stub_app(db, "multica", cfg)
    try:
        async with httpx.AsyncClient(
            transport=httpx.ASGITransport(app=stub), base_url="http://stub"
        ) as client:
            yield client, db
    finally:
        ex.stop()
        await run_task


async def test_close_segment_relays_session_key_and_response():
    seen: dict = {}

    def handler(request: httpx.Request) -> httpx.Response:
        seen["method"] = request.method
        seen["path"] = request.url.path
        seen["auth"] = request.headers.get("authorization")
        return httpx.Response(200, json={"trajectory_id": 42})

    async with gateway_harness(handler) as (client, _db):
        resp = await client.post(
            "/rl/close_segment",
            headers={"Authorization": "Bearer proxy-key"},
            json={},
        )

    # The stub serves the route (no 404) and relays the gateway response.
    assert resp.status_code == 200
    assert resp.json() == {"trajectory_id": 42}
    assert seen["method"] == "POST"
    assert seen["path"] == "/rl/close_segment"
    # Session-key auth passes through end to end (gateway group, like set_reward).
    assert seen["auth"] == "Bearer proxy-key"


async def test_close_segment_no_active_segment_passes_through_400():
    def handler(request: httpx.Request) -> httpx.Response:
        return httpx.Response(400, json={"detail": "no active segment"})

    async with gateway_harness(handler) as (client, _db):
        resp = await client.post(
            "/rl/close_segment",
            headers={"Authorization": "Bearer proxy-key"},
            json={},
        )

    # A 400 (no active segment to close) must survive the relay, not become a 502.
    assert resp.status_code == 400
    assert resp.json()["detail"] == "no active segment"
