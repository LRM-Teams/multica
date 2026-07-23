"""End-to-end streaming relay: multica server -> DB -> executor -> upstream.

Exercises the full SSE path with a real ``Executor`` streaming a mocked
upstream response into ``bridge_stream_chunks`` and the multica public server
replaying those chunks to the client as a ``StreamingResponse``. Both sides
share one in-memory ``FakeSupabaseClient`` so the DB is the only transport.
"""

from __future__ import annotations

import asyncio

import httpx

from db_bridge.channels import CHANNELS_BY_NAME
from db_bridge.config import BridgeConfig, MulticaConfig
from db_bridge.db import BridgeDB
from db_bridge.executor import Executor
from db_bridge.multica_server import create_multica_app

from _fakes import FakeSupabaseClient

CHAT = CHANNELS_BY_NAME["chat_completions"]
USER_ID = "00000000-0000-0000-0000-0000000000aa"
LLM_KEY = "llm-secret"


def _multica_config() -> MulticaConfig:
    return MulticaConfig.from_env(
        {
            "SUPABASE_URL": "https://example.supabase.co",
            "SUPABASE_SERVICE_ROLE_KEY": "k",
            "MULTICA_BRIDGE_USER_ID": USER_ID,
            "MULTICA_LLM_API_KEYS": LLM_KEY,
            "MULTICA_STREAM_POLL_INTERVAL": "0.01",
        }
    )


def _executor_config() -> BridgeConfig:
    return BridgeConfig.from_env(
        {
            "SUPABASE_URL": "https://example.supabase.co",
            "SUPABASE_SERVICE_ROLE_KEY": "k",
            "BRIDGE_POLL_INTERVAL": "0.01",
            "BRIDGE_USER_ID": USER_ID,
            "BRIDGE_CONCURRENCY_CHAT_COMPLETIONS": "1",
            # Keep the pool quiet; the sweep loop is irrelevant here.
            "BRIDGE_STREAM_SWEEP_INTERVAL": "0",
            "BRIDGE_STATS_INTERVAL": "0",
            "BRIDGE_CLEANUP_INTERVAL": "0",
        }
    )


def _sse_upstream(chunks):
    def handler(request: httpx.Request) -> httpx.Response:
        async def _body():
            for chunk in chunks:
                yield chunk

        return httpx.Response(
            200, headers={"content-type": "text/event-stream"}, content=_body()
        )

    return handler


async def test_streaming_end_to_end():
    fake = FakeSupabaseClient()

    mcfg = _multica_config()
    bridge_db = BridgeDB(
        BridgeConfig.from_env(
            {
                "SUPABASE_URL": mcfg.supabase_url,
                "SUPABASE_SERVICE_ROLE_KEY": mcfg.supabase_key,
                "BRIDGE_POLL_INTERVAL": "0.01",
            }
        ),
        client=fake,
    )
    app = create_multica_app(mcfg, bridge_db=bridge_db)

    ecfg = _executor_config()
    exec_db = BridgeDB(ecfg, client=fake)
    chunks = [
        b'data: {"choices":[{"delta":{"content":"Hel"}}]}\n\n',
        b'data: {"choices":[{"delta":{"content":"lo"}}]}\n\n',
        b"data: [DONE]\n\n",
    ]
    ex = Executor(
        exec_db,
        "areal",
        config=ecfg,
        client=httpx.AsyncClient(transport=httpx.MockTransport(_sse_upstream(chunks))),
    )
    run_task = asyncio.create_task(ex.run())
    try:
        async with httpx.AsyncClient(
            transport=httpx.ASGITransport(app=app), base_url="http://multica"
        ) as client:
            resp = await client.post(
                "/v1/chat/completions",
                headers={"Authorization": f"Bearer {LLM_KEY}"},
                json={"model": "areal/qwen3", "messages": [], "stream": True},
            )
    finally:
        ex.stop()
        await run_task

    assert resp.status_code == 200
    assert resp.headers["content-type"].startswith("text/event-stream")
    assert resp.content == b"".join(chunks)

    # The shared row was finalized by the executor after the stream completed.
    row = next(iter(fake.tables[CHAT.table].values()))
    assert row["status"] == "done"
    assert row["response_status"] == 200
