"""Tests for the multica remote-shell DB access layer (Task 4)."""

from __future__ import annotations

from db_bridge.config import MulticaConfig
from db_bridge.multica_server import MulticaShellDB

from _fakes import FakeSupabaseClient

USER_ID = "00000000-0000-0000-0000-0000000000bb"
SHELL_TABLE = "areal_remote_commands"


def _config(**overrides: str) -> MulticaConfig:
    env = {
        "SUPABASE_URL": "https://example.supabase.co",
        "SUPABASE_SERVICE_ROLE_KEY": "k",
        "MULTICA_BRIDGE_USER_ID": USER_ID,
        "MULTICA_SHELL_API_KEYS": "shell-key",
        "MULTICA_SHELL_DEFAULT_TIMEOUT": "120",
        "MULTICA_SHELL_MAX_TIMEOUT": "600",
        **overrides,
    }
    return MulticaConfig.from_env(env)


async def test_enqueue_inserts_pending_row_with_clamped_timeout():
    cfg = _config()
    client = FakeSupabaseClient()
    db = MulticaShellDB(cfg, client=client)

    result = await db.enqueue_command(
        user_id=USER_ID,
        tmux_id="debug-gpu",
        command="nvidia-smi",
        cwd="/tmp",
        timeout_seconds=5000,  # exceeds max -> clamped to 600
        metadata={"src": "test"},
    )
    assert result["status"] == "PENDING"
    row = client.tables[SHELL_TABLE][result["id"]]
    assert row["user_id"] == USER_ID
    assert row["tmux_id"] == "debug-gpu"
    assert row["command"] == "nvidia-smi"
    assert row["cwd"] == "/tmp"
    assert row["timeout_seconds"] == 600
    assert row["status"] == "PENDING"
    assert row["metadata"] == {"src": "test"}


async def test_enqueue_uses_default_cwd_and_timeout():
    cfg = _config(MULTICA_SHELL_DEFAULT_CWD="/root/AReaL")
    client = FakeSupabaseClient()
    db = MulticaShellDB(cfg, client=client)

    result = await db.enqueue_command(
        user_id=USER_ID, tmux_id="t1", command="ls", timeout_seconds=None
    )
    row = client.tables[SHELL_TABLE][result["id"]]
    assert row["cwd"] == "/root/AReaL"
    assert row["timeout_seconds"] == 120


async def test_get_command_returns_status_fields():
    cfg = _config()
    client = FakeSupabaseClient()
    db = MulticaShellDB(cfg, client=client)
    result = await db.enqueue_command(user_id=USER_ID, tmux_id="t1", command="ls")

    got = await db.get_command(result["id"], user_id=USER_ID)
    assert got is not None
    assert got["id"] == result["id"]
    assert got["status"] == "PENDING"
    assert got["tmux_id"] == "t1"
    # Not yet executed.
    assert got["exit_code"] is None


async def test_get_command_other_user_returns_none():
    cfg = _config()
    client = FakeSupabaseClient()
    db = MulticaShellDB(cfg, client=client)
    result = await db.enqueue_command(user_id=USER_ID, tmux_id="t1", command="ls")

    other = await db.get_command(result["id"], user_id="11111111-1111-1111-1111-111111111111")
    assert other is None


async def test_request_cancel_pending_marks_cancelled():
    cfg = _config()
    client = FakeSupabaseClient()
    db = MulticaShellDB(cfg, client=client)
    result = await db.enqueue_command(user_id=USER_ID, tmux_id="t1", command="sleep 100")

    res = await db.request_cancel(result["id"], user_id=USER_ID)
    assert res == {"ok": True, "status": "CANCELLED"}


async def test_request_cancel_running_marks_cancel_requested():
    cfg = _config()
    client = FakeSupabaseClient()
    db = MulticaShellDB(cfg, client=client)
    result = await db.enqueue_command(user_id=USER_ID, tmux_id="t1", command="sleep 100")
    # Simulate the runner having claimed + started the command.
    client.tables[SHELL_TABLE][result["id"]]["status"] = "RUNNING"

    res = await db.request_cancel(result["id"], user_id=USER_ID)
    assert res == {"ok": True, "status": "CANCEL_REQUESTED"}


async def test_request_cancel_terminal_is_noop():
    cfg = _config()
    client = FakeSupabaseClient()
    db = MulticaShellDB(cfg, client=client)
    result = await db.enqueue_command(user_id=USER_ID, tmux_id="t1", command="ls")
    client.tables[SHELL_TABLE][result["id"]]["status"] = "SUCCEEDED"

    res = await db.request_cancel(result["id"], user_id=USER_ID)
    assert res == {"ok": False, "status": "SUCCEEDED"}
