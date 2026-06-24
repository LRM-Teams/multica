"""Gateway-channel tests (Task 6): start_session, end_session, chat/completions.

The stub/executor relay is generic, so these tests assert the endpoint-specific
behaviours the le-agent -> AReaL gateway path depends on:
  * admin / session bearer tokens pass through verbatim,
  * the gateway's 429 "no capacity" propagates (not turned into a 502),
  * end_session's response shape is preserved, and
  * large chat-completion payloads (logprobs) round-trip byte-for-byte and are
    stored compressed.
"""

from __future__ import annotations

import asyncio
import contextlib
import json

import httpx

from db_bridge import codec
from db_bridge.channels import CHANNELS_BY_NAME
from db_bridge.config import BridgeConfig
from db_bridge.db import BridgeDB
from db_bridge.executor import Executor
from db_bridge.stub_server import create_stub_app

from _fakes import FakeSupabaseClient

USER_ID = "00000000-0000-0000-0000-00000000000a"

START_SESSION = CHANNELS_BY_NAME["rl_start_session"]
END_SESSION = CHANNELS_BY_NAME["rl_end_session"]
CHAT = CHANNELS_BY_NAME["chat_completions"]


def _config(**overrides: str) -> BridgeConfig:
    env = {
        "SUPABASE_URL": "https://example.supabase.co",
        "SUPABASE_SERVICE_ROLE_KEY": "k",
        "BRIDGE_POLL_INTERVAL": "0.01",
        "BRIDGE_USER_ID": USER_ID,
        "BRIDGE_CONCURRENCY_CHAT_COMPLETIONS": "2",
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
    stub = create_stub_app(db, "leagent", cfg)
    try:
        async with httpx.AsyncClient(
            transport=httpx.ASGITransport(app=stub), base_url="http://stub"
        ) as client:
            yield client, db
    finally:
        ex.stop()
        await run_task


async def test_start_session_admin_auth_and_response():
    seen = {}

    def handler(request: httpx.Request) -> httpx.Response:
        seen["auth"] = request.headers.get("authorization")
        seen["path"] = request.url.path
        seen["body"] = json.loads(request.content)
        return httpx.Response(
            200, json={"session_id": "sess-1", "api_key": "sk-session-abc"}
        )

    async with gateway_harness(handler) as (client, _db):
        resp = await client.post(
            "/rl/start_session",
            headers={"Authorization": "Bearer admin-key"},
            json={"task_id": "t-123"},
        )

    assert resp.status_code == 200
    assert resp.json() == {"session_id": "sess-1", "api_key": "sk-session-abc"}
    assert seen["auth"] == "Bearer admin-key"  # admin token passed through
    assert seen["path"] == "/rl/start_session"
    assert seen["body"] == {"task_id": "t-123"}


async def test_start_session_429_propagates():
    def handler(request: httpx.Request) -> httpx.Response:
        return httpx.Response(429, json={"detail": "no capacity"})

    async with gateway_harness(handler) as (client, _db):
        resp = await client.post(
            "/rl/start_session",
            headers={"Authorization": "Bearer admin-key"},
            json={"task_id": "t-1"},
        )

    # 429 must survive the relay so areal_online.start_session falls back.
    assert resp.status_code == 429
    assert resp.json() == {"detail": "no capacity"}


async def test_end_session_response_shape():
    def handler(request: httpx.Request) -> httpx.Response:
        assert request.headers.get("authorization") == "Bearer sk-session"
        return httpx.Response(200, json={"interaction_count": 7})

    async with gateway_harness(handler) as (client, _db):
        resp = await client.post(
            "/rl/end_session", headers={"Authorization": "Bearer sk-session"}
        )

    assert resp.status_code == 200
    assert resp.json() == {"interaction_count": 7}


async def test_chat_completions_large_logprobs_roundtrip():
    # Build a sizable OpenAI-style completion with logprobs/top_logprobs.
    content_tokens = [
        {
            "token": f"tok{i}",
            "logprob": -0.0001 * i,
            "top_logprobs": [
                {"token": f"alt{i}_{j}", "logprob": -1.0 * j} for j in range(5)
            ],
        }
        for i in range(4000)
    ]
    completion = {
        "id": "chatcmpl-xyz",
        "object": "chat.completion",
        "choices": [
            {
                "index": 0,
                "finish_reason": "stop",
                "message": {"role": "assistant", "content": "answer " * 50},
                "logprobs": {"content": content_tokens},
            }
        ],
        "usage": {
            "prompt_tokens": 1200,
            "completion_tokens": 4000,
            "total_tokens": 5200,
        },
    }
    body = json.dumps(completion).encode()
    assert len(body) > 2048  # ensures the compression path is exercised

    def handler(request: httpx.Request) -> httpx.Response:
        assert request.headers.get("authorization") == "Bearer sk-session"
        return httpx.Response(
            200, content=body, headers={"content-type": "application/json"}
        )

    async with gateway_harness(handler) as (client, db):
        resp = await client.post(
            "/chat/completions",
            headers={"Authorization": "Bearer sk-session"},
            json={
                "model": "areal/qwen",
                "messages": [{"role": "user", "content": "hi"}],
                "logprobs": True,
                "top_logprobs": 5,
                "stream": False,
            },
        )

        assert resp.status_code == 200
        assert resp.content == body  # byte-for-byte
        assert resp.json()["choices"][0]["logprobs"]["content"][0]["token"] == "tok0"

        # Stored compressed (gzip+base64), not raw.
        rows = list(db.client.tables[CHAT.table].values())
        assert len(rows) == 1
        assert rows[0]["response_body_encoding"] == codec.GZIP_BASE64


async def test_non_areal_chat_completion_forwards_directly_without_db_bridge(
    monkeypatch,
):
    seen = {}

    class DirectClient:
        def __init__(self, *args, **kwargs):
            seen["client_kwargs"] = kwargs

        async def __aenter__(self):
            return self

        async def __aexit__(self, *args):
            return False

        async def request(self, method, url, headers, content, timeout):
            seen["method"] = method
            seen["url"] = url
            seen["headers"] = headers
            seen["body"] = json.loads(content)
            seen["timeout"] = timeout
            return httpx.Response(200, json={"id": "direct", "choices": []})

    import db_bridge.stub_server as stub_server

    monkeypatch.setattr(stub_server, "_build_direct_client", DirectClient)
    cfg = _config()
    db = BridgeDB(cfg, client=FakeSupabaseClient())
    stub = create_stub_app(db, "leagent", cfg)

    async with httpx.AsyncClient(
        transport=httpx.ASGITransport(app=stub), base_url="https://received.example"
    ) as client:
        resp = await client.post(
            "/chat/completions?trace=1",
            headers={"Authorization": "Bearer sk-session"},
            json={"model": "openai/gpt-5.4", "messages": []},
        )

    assert resp.status_code == 200
    assert resp.json() == {"id": "direct", "choices": []}
    assert seen["url"] == "https://received.example/chat/completions?trace=1"
    assert seen["headers"]["authorization"] == "Bearer sk-session"
    assert seen["body"] == {"model": "openai/gpt-5.4", "messages": []}
    assert db.client.tables.get(CHAT.table, {}) == {}


async def test_distinct_session_tokens_pass_through():
    seen: list[str] = []

    def handler(request: httpx.Request) -> httpx.Response:
        seen.append(request.headers.get("authorization"))
        return httpx.Response(200, json={"ok": True})

    async with gateway_harness(handler) as (client, _db):
        await client.post(
            "/rl/set_reward",
            headers={"Authorization": "Bearer key-A"},
            json={"reward": 1.0},
        )
        await client.post(
            "/rl/set_reward",
            headers={"Authorization": "Bearer key-B"},
            json={"reward": 0.5},
        )

    assert set(seen) == {"Bearer key-A", "Bearer key-B"}
