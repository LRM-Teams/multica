"""End-to-end simulated-isolation test (Task 9).

Models the production topology where le-agent and AReaL share ONLY the Supabase
database and cannot reach each other directly:

  * le-agent app  -> gateway stub (le-agent host) --DB--> gateway executor
                     (AReaL host) -> real AReaL gateway
  * AReaL backend  -> le-agent-API stub (AReaL host) --DB--> le-agent-API
                     executor (le-agent host) -> real le-agent API

The two sides share one in-memory FakeSupabaseClient (the "database") and
nothing else: each side's stub is reached over its own loopback client and each
executor forwards to its own mocked upstream. The test runs a full agent_start
plus a complete RL session lifecycle.
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


def _config() -> BridgeConfig:
    env = {
        "SUPABASE_URL": "https://example.supabase.co",
        "SUPABASE_SERVICE_ROLE_KEY": "k",
        "BRIDGE_POLL_INTERVAL": "0.01",
        "BRIDGE_USER_ID": USER_ID,
        # Keep the worker pools tiny for the test.
        "BRIDGE_CONCURRENCY_CHAT_COMPLETIONS": "2",
        "BRIDGE_CONCURRENCY_RL_START_SESSION": "2",
        "BRIDGE_CONCURRENCY_RL_SET_REWARD": "2",
        "BRIDGE_CONCURRENCY_RL_END_SESSION": "2",
        "BRIDGE_CONCURRENCY_AGENT_START": "2",
        "BRIDGE_CONCURRENCY_AGENT_START_BRANCH": "2",
    }
    return BridgeConfig.from_env(env)


def _gateway_upstream(counters: dict):
    """Mocked real AReaL proxy gateway (lives on the AReaL host)."""

    def handler(request: httpx.Request) -> httpx.Response:
        path = request.url.path
        counters[path] = counters.get(path, 0) + 1
        if path == "/rl/start_session":
            assert request.headers["authorization"] == "Bearer admin-key"
            return httpx.Response(
                200, json={"session_id": "sess-1", "api_key": "sk-sess"}
            )
        if path == "/chat/completions":
            assert request.headers["authorization"] == "Bearer sk-sess"
            return httpx.Response(
                200,
                json={
                    "id": "chatcmpl-1",
                    "choices": [
                        {
                            "index": 0,
                            "finish_reason": "stop",
                            "message": {
                                "role": "assistant",
                                "content": "<answer>42</answer>",
                            },
                        }
                    ],
                    "usage": {
                        "prompt_tokens": 10,
                        "completion_tokens": 5,
                        "total_tokens": 15,
                    },
                },
            )
        if path == "/rl/set_reward":
            return httpx.Response(200, json={})
        if path == "/rl/end_session":
            return httpx.Response(200, json={"interaction_count": 2})
        return httpx.Response(404)

    return handler


def _leagent_upstream(counters: dict):
    """Mocked real le-agent API (lives on the le-agent host)."""

    def handler(request: httpx.Request) -> httpx.Response:
        path = request.url.path
        counters[path] = counters.get(path, 0) + 1
        assert request.headers["authorization"] == "Bearer jwt-token"
        if path == "/api/agent/start":
            assert request.headers["content-type"].startswith("multipart/form-data")
            return httpx.Response(
                200,
                json={
                    "task_id": "task-1",
                    "agent_run_id": "run-1",
                    "project_id": None,
                    "status": "pending",
                    "message": None,
                },
            )
        if path == "/api/agent/start-branch":
            return httpx.Response(
                200,
                json={
                    "task_id": "task-1",
                    "agent_run_id": "run-2",
                    "project_id": None,
                    "status": "pending",
                    "message": None,
                },
            )
        return httpx.Response(404)

    return handler


@contextlib.asynccontextmanager
async def two_isolated_sides():
    cfg = _config()
    db = BridgeDB(cfg, client=FakeSupabaseClient())  # the ONLY shared resource
    gw_counts: dict = {}
    la_counts: dict = {}

    gateway_executor = Executor(  # runs on AReaL host
        db,
        "areal",
        config=cfg,
        client=httpx.AsyncClient(
            transport=httpx.MockTransport(_gateway_upstream(gw_counts))
        ),
    )
    leagent_executor = Executor(  # runs on le-agent host
        db,
        "leagent",
        config=cfg,
        client=httpx.AsyncClient(
            transport=httpx.MockTransport(_leagent_upstream(la_counts))
        ),
    )
    tasks = [
        asyncio.create_task(gateway_executor.run()),
        asyncio.create_task(leagent_executor.run()),
    ]

    gateway_stub = create_stub_app(db, "leagent", cfg)  # le-agent app calls this
    leagent_api_stub = create_stub_app(db, "areal", cfg)  # AReaL backend calls this
    try:
        async with (
            httpx.AsyncClient(
                transport=httpx.ASGITransport(app=gateway_stub),
                base_url="http://leagent-local",
            ) as leagent_app_client,
            httpx.AsyncClient(
                transport=httpx.ASGITransport(app=leagent_api_stub),
                base_url="http://areal-local",
            ) as areal_backend_client,
        ):
            yield leagent_app_client, areal_backend_client, gw_counts, la_counts
    finally:
        gateway_executor.stop()
        leagent_executor.stop()
        await asyncio.gather(*tasks)


async def test_full_agent_and_rl_lifecycle_over_shared_db_only():
    async with two_isolated_sides() as (
        leagent_app,
        areal_backend,
        gw_counts,
        la_counts,
    ):
        # 1) AReaL backend_run starts an agent run on le-agent (multipart + file).
        start = await areal_backend.post(
            "/api/agent/start",
            headers={"Authorization": "Bearer jwt-token"},
            data={
                "model_name": "openrouter/qwen",
                "agent_id": "agent-1",
                "prompt": "Solve it",
            },
            files={
                "files": (
                    "data.xlsx",
                    b"PK\x03\x04 content",
                    "application/octet-stream",
                )
            },
        )
        assert start.status_code == 200
        assert start.json()["agent_run_id"] == "run-1"

        # 2) le-agent opens an RL session on AReaL.
        sess = await leagent_app.post(
            "/rl/start_session",
            headers={"Authorization": "Bearer admin-key"},
            json={"task_id": "task-1"},
        )
        assert sess.status_code == 200
        api_key = sess.json()["api_key"]
        assert api_key == "sk-sess"

        # 3) Two proxied LLM calls through the bridge.
        for _ in range(2):
            chat = await leagent_app.post(
                "/chat/completions",
                headers={"Authorization": f"Bearer {api_key}"},
                json={
                    "model": "areal/qwen",
                    "messages": [{"role": "user", "content": "q"}],
                    "stream": False,
                },
            )
            assert chat.status_code == 200
            assert "answer" in chat.json()["choices"][0]["message"]["content"]

        # 4) Finalize: set_reward then end_session.
        reward = await leagent_app.post(
            "/rl/set_reward",
            headers={"Authorization": f"Bearer {api_key}"},
            json={"interaction_id": None, "reward": 1.0},
        )
        assert reward.status_code == 200
        end = await leagent_app.post(
            "/rl/end_session", headers={"Authorization": f"Bearer {api_key}"}
        )
        assert end.status_code == 200
        assert end.json()["interaction_count"] == 2

    # Every hop actually traversed the bridge to the correct upstream.
    assert la_counts["/api/agent/start"] == 1
    assert gw_counts["/rl/start_session"] == 1
    assert gw_counts["/chat/completions"] == 2
    assert gw_counts["/rl/set_reward"] == 1
    assert gw_counts["/rl/end_session"] == 1


async def test_branch_start_over_shared_db_only():
    async with two_isolated_sides() as (
        leagent_app,
        areal_backend,
        gw_counts,
        la_counts,
    ):
        resp = await areal_backend.post(
            "/api/agent/start-branch",
            headers={"Authorization": "Bearer jwt-token"},
            json={
                "task_id": "task-1",
                "model_name": "openrouter/qwen",
                "proxy_base_url": "http://127.0.0.1:9100",
                "proxy_api_key": "sk-sess",
                "stream": False,
            },
        )
        assert resp.status_code == 200
        assert resp.json()["agent_run_id"] == "run-2"
    assert la_counts["/api/agent/start-branch"] == 1
