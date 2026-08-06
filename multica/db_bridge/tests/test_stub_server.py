"""Tests for the generic stub server (Task 4: /rl/set_reward)."""

from __future__ import annotations

import asyncio
import json

import httpx

from db_bridge.channels import CHANNELS_BY_NAME
from db_bridge.config import BridgeConfig
from db_bridge.db import BridgeDB
from db_bridge.stub_server import create_stub_app

from _fakes import FakeSupabaseClient

SET_REWARD = CHANNELS_BY_NAME["rl_set_reward"]
CHAT = CHANNELS_BY_NAME["chat_completions"]
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
    app = create_stub_app(db, "multica", cfg)

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
    app = create_stub_app(db, "multica", cfg)

    async with _client(app) as client:
        resp = await client.post("/rl/set_reward", json={"reward": 1.0})

    assert resp.status_code == 400
    assert "X-Bridge-User-Id" in resp.json()["detail"]
    assert db.client.tables.get(SET_REWARD.table, {}) == {}


async def test_user_id_header_is_stored_but_not_forwarded():
    cfg = _config()
    db = BridgeDB(cfg, client=FakeSupabaseClient())
    app = create_stub_app(db, "multica", cfg)

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
    app = create_stub_app(db, "multica", cfg)
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
    app = create_stub_app(db, "multica", cfg)

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
    app = create_stub_app(db, "multica", cfg)
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
    app = create_stub_app(db, "multica", cfg)

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


async def test_streaming_chat_relayed_buffered_and_returned_as_sse():
    """stream=true is relayed buffered, then re-emitted as an SSE event-stream."""
    cfg = _config()
    db = BridgeDB(cfg, client=FakeSupabaseClient())
    app = create_stub_app(db, "multica", cfg)

    buffered = (
        b'{"id":"cmpl-9","model":"areal/qwen3",'
        b'"choices":[{"index":0,'
        b'"message":{"role":"assistant","content":"Hello world"},'
        b'"finish_reason":"stop"}]}'
    )
    exec_task = asyncio.create_task(
        _executor_complete_one(
            db,
            CHAT,
            status=200,
            headers={"content-type": "application/json"},
            body=buffered,
        )
    )
    async with _client(app) as client:
        resp = await client.post(
            "/chat/completions",
            headers={"X-Bridge-User-Id": USER_ID},
            json={
                "model": "areal/qwen3",
                "stream": True,
                "stream_options": {"include_usage": True},
                "messages": [{"role": "user", "content": "hi"}],
            },
        )
    claimed = await exec_task

    # Client sees a valid SSE stream terminated by [DONE], content preserved.
    assert resp.status_code == 200
    assert resp.headers["content-type"].startswith("text/event-stream")
    text = resp.text
    assert text.count("data: ") == 4  # role + content + finish + [DONE]
    assert text.strip().endswith("data: [DONE]")
    assert "Hello world" in text
    frames = [
        line[len("data: ") :]
        for line in text.splitlines()
        if line.startswith("data: ") and "[DONE]" not in line
    ]
    objs = [json.loads(f) for f in frames]
    assert objs[0]["choices"][0]["delta"]["role"] == "assistant"
    assert objs[1]["choices"][0]["delta"]["content"] == "Hello world"
    assert objs[2]["choices"][0]["finish_reason"] == "stop"
    assert all(o["object"] == "chat.completion.chunk" for o in objs)

    # The relayed (enqueued) request took the buffered path: stream flags dropped.
    relayed = json.loads(claimed.body)
    assert "stream" not in relayed
    assert "stream_options" not in relayed
    assert relayed["model"] == "areal/qwen3"
    assert relayed["messages"] == [{"role": "user", "content": "hi"}]


async def test_non_streaming_chat_returns_plain_json():
    """A buffered (stream=false) request is returned verbatim, not wrapped."""
    cfg = _config()
    db = BridgeDB(cfg, client=FakeSupabaseClient())
    app = create_stub_app(db, "multica", cfg)

    buffered = (
        b'{"id":"cmpl-1","model":"areal/qwen3",'
        b'"choices":[{"index":0,'
        b'"message":{"role":"assistant","content":"Hi"},"finish_reason":"stop"}]}'
    )
    exec_task = asyncio.create_task(
        _executor_complete_one(
            db,
            CHAT,
            status=200,
            headers={"content-type": "application/json"},
            body=buffered,
        )
    )
    async with _client(app) as client:
        resp = await client.post(
            "/chat/completions",
            headers={"X-Bridge-User-Id": USER_ID},
            json={
                "model": "areal/qwen3",
                "messages": [{"role": "user", "content": "hi"}],
            },
        )
    await exec_task

    assert resp.status_code == 200
    assert resp.headers["content-type"].startswith("application/json")
    assert resp.json()["choices"][0]["message"]["content"] == "Hi"


async def test_areal_default_model_takes_relay_not_bypass():
    """model=areal-default must be relayed via the DB (not direct-forwarded)."""
    from db_bridge.stub_server import (
        _is_areal_model,
        _should_bypass_chat_completions,
    )

    assert _is_areal_model("areal-default") is True
    assert _is_areal_model("areal-distill") is True
    assert _is_areal_model("areal/qwen/qwen3_5-9b") is True
    assert _is_areal_model("gpt-4o") is False
    assert _is_areal_model(None) is False

    body = json.dumps({"model": "areal-default", "messages": []}).encode()
    # relayed (not bypassed) -> reaches the DB-bridge path
    assert _should_bypass_chat_completions(CHAT, body) is False
    # a plain openai model still bypasses (direct forward)
    body_openai = json.dumps({"model": "gpt-4o", "messages": []}).encode()
    assert _should_bypass_chat_completions(CHAT, body_openai) is True

    # End-to-end via the relay: executor completes the enqueued areal-default row.
    cfg = _config()
    db = BridgeDB(cfg, client=FakeSupabaseClient())
    app = create_stub_app(db, "multica", cfg)
    exec_task = asyncio.create_task(
        _executor_complete_one(
            db,
            CHAT,
            status=200,
            headers={"content-type": "application/json"},
            body=b'{"id":"c1","model":"areal-default",'
            b'"choices":[{"index":0,"message":{"role":"assistant","content":"ok"},'
            b'"finish_reason":"stop"}]}',
        )
    )
    async with _client(app) as client:
        resp = await client.post(
            "/chat/completions",
            headers={"X-Bridge-User-Id": USER_ID},
            json={"model": "areal-default", "messages": [{"role": "user", "content": "hi"}]},
        )
    claimed = await exec_task
    assert resp.status_code == 200
    assert resp.json()["choices"][0]["message"]["content"] == "ok"
    # Proof it went through the DB relay (row enqueued), not a direct forward.
    assert db.client.tables.get(CHAT.table, {}) != {}
    assert claimed.path == "/chat/completions"


async def test_healthz_lists_channels():
    cfg = _config()
    db = BridgeDB(cfg, client=FakeSupabaseClient())
    app = create_stub_app(db, "multica", cfg)
    async with _client(app) as client:
        resp = await client.get("/healthz")
    assert resp.status_code == 200
    payload = resp.json()
    assert payload["side"] == "multica"
    assert "rl_set_reward" in payload["channels"]
    assert "chat_completions" in payload["channels"]


async def test_responses_api_streaming_returns_responses_sse():
    """POST /responses with stream=true gets response.* SSE, not chat chunks."""
    cfg = _config()
    db = BridgeDB(cfg, client=FakeSupabaseClient())
    app = create_stub_app(db, "multica", cfg)

    # Shape of a real DeepSeek reply observed through the relay: empty content
    # plus a tool call.
    buffered = (
        b'{"id":"cmpl-r1","model":"areal-default","created":1785125742,'
        b'"choices":[{"index":0,'
        b'"message":{"role":"assistant","content":"",'
        b'"tool_calls":[{"index":0,"id":"call_00_abc",'
        b'"function":{"name":"run_command","arguments":"{\\"cmd\\":\\"ls\\"}"}}]},'
        b'"finish_reason":"tool_calls"}],'
        b'"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}'
    )
    exec_task = asyncio.create_task(
        _executor_complete_one(
            db,
            CHAT,
            status=200,
            headers={"content-type": "application/json"},
            body=buffered,
        )
    )
    async with _client(app) as client:
        resp = await client.post(
            "/responses",
            headers={"X-Bridge-User-Id": USER_ID},
            json={
                "model": "areal-default",
                "stream": True,
                "input": [{"role": "user", "content": "hi"}],
            },
        )
    claimed = await exec_task

    assert resp.status_code == 200
    assert resp.headers["content-type"].startswith("text/event-stream")
    text = resp.text
    assert "chat.completion.chunk" not in text
    assert "[DONE]" not in text
    assert "event: response.created" in text
    assert "event: response.function_call_arguments.done" in text
    assert "event: response.completed" in text

    events = {}
    for block in text.strip().split("\n\n"):
        for line in block.splitlines():
            if line.startswith("data: "):
                data = json.loads(line[len("data: ") :])
                events[data["type"]] = data
    completed = events["response.completed"]["response"]
    assert completed["object"] == "response"
    assert completed["status"] == "completed"
    assert completed["model"] == "areal-default"
    fc = completed["output"][0]
    assert fc["type"] == "function_call"
    assert fc["call_id"] == "call_00_abc"
    assert fc["name"] == "run_command"
    assert fc["arguments"] == '{"cmd":"ls"}'
    assert completed["usage"] == {
        "input_tokens": 10,
        "output_tokens": 5,
        "total_tokens": 15,
    }

    # The relayed request still took the buffered path (stream flag dropped).
    relayed = json.loads(claimed.body)
    assert "stream" not in relayed
    assert relayed["input"] == [{"role": "user", "content": "hi"}]
    assert claimed.path == "/responses"


async def test_responses_api_non_stream_returns_responses_json():
    """POST /responses without stream gets a Responses-shaped JSON body."""
    cfg = _config()
    db = BridgeDB(cfg, client=FakeSupabaseClient())
    app = create_stub_app(db, "multica", cfg)

    buffered = (
        b'{"id":"cmpl-r2","model":"areal-default",'
        b'"choices":[{"index":0,'
        b'"message":{"role":"assistant","content":"Hi there"},'
        b'"finish_reason":"stop"}]}'
    )
    exec_task = asyncio.create_task(
        _executor_complete_one(
            db,
            CHAT,
            status=200,
            headers={"content-type": "application/json"},
            body=buffered,
        )
    )
    async with _client(app) as client:
        resp = await client.post(
            "/responses",
            headers={"X-Bridge-User-Id": USER_ID},
            json={
                "model": "areal-default",
                "input": [{"role": "user", "content": "hi"}],
            },
        )
    await exec_task

    assert resp.status_code == 200
    payload = resp.json()
    assert payload["object"] == "response"
    assert payload["status"] == "completed"
    assert payload["output"][0]["type"] == "message"
    assert payload["output"][0]["content"][0]["type"] == "output_text"
    assert payload["output"][0]["content"][0]["text"] == "Hi there"
