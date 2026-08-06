"""End-to-end tests for the multica server app + entrypoint (Task 6)."""

from __future__ import annotations

import asyncio

import httpx

from db_bridge.channels import CHANNELS_BY_NAME
from db_bridge.config import BridgeConfig, MulticaConfig
from db_bridge.db import BridgeDB
from db_bridge.multica_server import MulticaShellDB, create_multica_app

from _fakes import FakeSupabaseClient

CHAT = CHANNELS_BY_NAME["chat_completions"]
USER_ID = "00000000-0000-0000-0000-0000000000dd"
LLM_KEY = "llm-secret"
SHELL_KEY = "shell-secret"


def _config() -> MulticaConfig:
    return MulticaConfig.from_env(
        {
            "SUPABASE_URL": "https://example.supabase.co",
            "SUPABASE_SERVICE_ROLE_KEY": "k",
            "MULTICA_BRIDGE_USER_ID": USER_ID,
            "MULTICA_LLM_API_KEYS": LLM_KEY,
            "MULTICA_SHELL_API_KEYS": SHELL_KEY,
            "BRIDGE_POLL_INTERVAL": "0.01",
            "MULTICA_CHAT_TIMEOUT": "2",
        }
    )


def _app(cfg: MulticaConfig, client: FakeSupabaseClient):
    bcfg = BridgeConfig.from_env(
        {
            "SUPABASE_URL": cfg.supabase_url,
            "SUPABASE_SERVICE_ROLE_KEY": cfg.supabase_key,
            "BRIDGE_POLL_INTERVAL": "0.01",
        }
    )
    bridge_db = BridgeDB(bcfg, client=client)
    shell_db = MulticaShellDB(cfg, client=client)
    return create_multica_app(cfg, bridge_db=bridge_db, shell_db=shell_db), bridge_db


def _client(app) -> httpx.AsyncClient:
    return httpx.AsyncClient(
        transport=httpx.ASGITransport(app=app), base_url="http://multica"
    )


async def test_healthz():
    app, _ = _app(_config(), FakeSupabaseClient())
    async with _client(app) as c:
        resp = await c.get("/healthz")
    assert resp.status_code == 200
    assert resp.json() == {"status": "ok", "service": "multica"}


async def test_llm_and_shell_share_one_client():
    cfg = _config()
    client = FakeSupabaseClient()
    app, bridge_db = _app(cfg, client)

    async def complete_chat():
        while True:
            req = await bridge_db.claim_next(CHAT, "exec")
            if req is not None:
                await bridge_db.complete(
                    CHAT,
                    req.id,
                    worker_id=req.worker_id,
                    response_status=200,
                    response_headers={},
                    body=b'{"ok":true}',
                )
                return
            await asyncio.sleep(0.005)

    async with _client(app) as c:
        # LLM path
        exec_task = asyncio.create_task(complete_chat())
        llm = await c.post(
            "/v1/chat/completions",
            headers={"Authorization": f"Bearer {LLM_KEY}"},
            json={"model": "areal/qwen3", "messages": []},
        )
        await exec_task
        assert llm.status_code == 200
        assert llm.json() == {"ok": True}

        # Shell path on the same app/client
        sh = await c.post(
            "/shell/commands",
            headers={"Authorization": f"Bearer {SHELL_KEY}"},
            json={"tmux_id": "t", "command": "ls"},
        )
        assert sh.status_code == 201

    # Both tables populated through the single injected fake client.
    assert client.tables[CHAT.table]
    assert client.tables["areal_remote_commands"]


async def test_auth_separation_between_surfaces():
    cfg = _config()
    app, _ = _app(cfg, FakeSupabaseClient())
    async with _client(app) as c:
        # Shell key must not unlock the LLM endpoint.
        llm = await c.post(
            "/v1/chat/completions",
            headers={"Authorization": f"Bearer {SHELL_KEY}"},
            json={"model": "areal/qwen3", "messages": []},
        )
        assert llm.status_code == 401
        # LLM key must not unlock the shell endpoint.
        sh = await c.post(
            "/shell/commands",
            headers={"Authorization": f"Bearer {LLM_KEY}"},
            json={"tmux_id": "t", "command": "ls"},
        )
        assert sh.status_code == 401


def test_run_multica_importable():
    # The entrypoint must import cleanly (wiring smoke test).
    from db_bridge.entrypoints import run_multica
    from db_bridge.run_multica import run_multica as run_multica_module

    assert callable(run_multica)
    assert callable(run_multica_module)
