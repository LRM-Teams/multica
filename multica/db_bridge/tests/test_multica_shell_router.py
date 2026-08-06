"""Tests for the multica remote-shell enqueue router (Task 5)."""

from __future__ import annotations

import httpx

from db_bridge.config import MulticaConfig
from db_bridge.multica_server import MulticaShellDB, create_multica_app

from _fakes import FakeSupabaseClient

USER_ID = "00000000-0000-0000-0000-0000000000cc"
SHELL_KEY = "shell-secret"
LLM_KEY = "llm-secret"
SHELL_TABLE = "areal_remote_commands"


def _config(**overrides: str) -> MulticaConfig:
    env = {
        "SUPABASE_URL": "https://example.supabase.co",
        "SUPABASE_SERVICE_ROLE_KEY": "k",
        "MULTICA_BRIDGE_USER_ID": USER_ID,
        "MULTICA_LLM_API_KEYS": LLM_KEY,
        "MULTICA_SHELL_API_KEYS": SHELL_KEY,
        **overrides,
    }
    return MulticaConfig.from_env(env)


def _app(cfg: MulticaConfig, client: FakeSupabaseClient):
    shell_db = MulticaShellDB(cfg, client=client)
    return create_multica_app(cfg, shell_db=shell_db)


def _client(app) -> httpx.AsyncClient:
    return httpx.AsyncClient(
        transport=httpx.ASGITransport(app=app), base_url="http://multica"
    )


def _auth(key: str = SHELL_KEY) -> dict[str, str]:
    return {"Authorization": f"Bearer {key}"}


async def test_create_get_cancel_roundtrip():
    cfg = _config()
    client = FakeSupabaseClient()
    app = _app(cfg, client)
    async with _client(app) as c:
        created = await c.post(
            "/shell/commands",
            headers=_auth(),
            json={"tmux_id": "debug", "command": "nvidia-smi", "cwd": "/tmp"},
        )
        assert created.status_code == 201
        cmd_id = created.json()["id"]
        assert created.json()["status"] == "PENDING"

        got = await c.get(f"/shell/commands/{cmd_id}", headers=_auth())
        assert got.status_code == 200
        assert got.json()["status"] == "PENDING"
        assert got.json()["tmux_id"] == "debug"

        cancelled = await c.post(f"/shell/commands/{cmd_id}/cancel", headers=_auth())
        assert cancelled.status_code == 200
        assert cancelled.json() == {"ok": True, "status": "CANCELLED"}

    # Enqueued under the fixed shared user.
    row = client.tables[SHELL_TABLE][cmd_id]
    assert row["user_id"] == USER_ID


async def test_missing_shell_key_returns_401():
    cfg = _config()
    app = _app(cfg, FakeSupabaseClient())
    async with _client(app) as c:
        resp = await c.post(
            "/shell/commands", json={"tmux_id": "t", "command": "ls"}
        )
    assert resp.status_code == 401


async def test_llm_key_cannot_access_shell():
    cfg = _config()
    client = FakeSupabaseClient()
    app = _app(cfg, client)
    async with _client(app) as c:
        resp = await c.post(
            "/shell/commands",
            headers=_auth(LLM_KEY),
            json={"tmux_id": "t", "command": "ls"},
        )
    assert resp.status_code == 401
    assert client.tables.get(SHELL_TABLE, {}) == {}


async def test_missing_command_returns_400():
    cfg = _config()
    client = FakeSupabaseClient()
    app = _app(cfg, client)
    async with _client(app) as c:
        resp = await c.post(
            "/shell/commands", headers=_auth(), json={"tmux_id": "t"}
        )
    assert resp.status_code == 400
    assert client.tables.get(SHELL_TABLE, {}) == {}


async def test_missing_tmux_id_returns_400():
    cfg = _config()
    client = FakeSupabaseClient()
    app = _app(cfg, client)
    async with _client(app) as c:
        resp = await c.post(
            "/shell/commands", headers=_auth(), json={"command": "ls"}
        )
    assert resp.status_code == 400


async def test_get_unknown_command_returns_404():
    cfg = _config()
    app = _app(cfg, FakeSupabaseClient())
    async with _client(app) as c:
        resp = await c.get(
            "/shell/commands/11111111-1111-1111-1111-111111111111",
            headers=_auth(),
        )
    assert resp.status_code == 404


async def test_cancel_unknown_command_returns_404():
    cfg = _config()
    app = _app(cfg, FakeSupabaseClient())
    async with _client(app) as c:
        resp = await c.post(
            "/shell/commands/11111111-1111-1111-1111-111111111111/cancel",
            headers=_auth(),
        )
    assert resp.status_code == 404
