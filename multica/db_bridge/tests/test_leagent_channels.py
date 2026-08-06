"""le-agent API channel tests (Task 7): /api/agent/start.

The relay is generic, so these verify the leagent_api group end to end:
  * agent/start multipart (form fields + an uploaded file) is replayed to the
    real API byte-for-byte (files inline), with audit metadata captured, and
  * oversized uploads are rejected with 413 before anything is enqueued.

For this group the stub runs on the AReaL side and the executor on the le-agent
side (forwarding to the real le-agent API).
"""

from __future__ import annotations

import asyncio
import contextlib

import httpx

from db_bridge.channels import CHANNELS_BY_NAME
from db_bridge.config import BridgeConfig
from db_bridge.db import BridgeDB
from db_bridge.executor import Executor
from db_bridge.stub_server import create_stub_app

from _fakes import FakeSupabaseClient

USER_ID = "00000000-0000-0000-0000-00000000000a"

AGENT_START = CHANNELS_BY_NAME["agent_start"]


def test_agent_start_branch_channel_removed():
    # The agent_start_branch channel was removed (branch now via env-dispatch).
    assert "agent_start_branch" not in CHANNELS_BY_NAME
    assert "/api/agent/start-branch" not in {c.path for c in CHANNELS_BY_NAME.values()}


def _config(**overrides: str) -> BridgeConfig:
    env = {
        "SUPABASE_URL": "https://example.supabase.co",
        "SUPABASE_SERVICE_ROLE_KEY": "k",
        "BRIDGE_HEADER_ENCRYPTION_KEY": (
            "MDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDA="
        ),
        "BRIDGE_POLL_INTERVAL": "0.01",
        "BRIDGE_USER_ID": USER_ID,
        **overrides,
    }
    return BridgeConfig.from_env(env)


@contextlib.asynccontextmanager
async def leagent_harness(handler, **cfg_overrides):
    cfg = _config(**cfg_overrides)
    db = BridgeDB(cfg, client=FakeSupabaseClient())
    ex = Executor(
        db,
        "leagent",
        config=cfg,
        client=httpx.AsyncClient(transport=httpx.MockTransport(handler)),
    )
    run_task = asyncio.create_task(ex.run())
    stub = create_stub_app(db, "areal", cfg)
    try:
        async with httpx.AsyncClient(
            transport=httpx.ASGITransport(app=stub), base_url="http://stub"
        ) as client:
            yield client, db
    finally:
        ex.stop()
        await run_task


async def test_agent_start_multipart_with_file_roundtrips():
    file_bytes = b"PK\x03\x04 fake-xlsx-content " + bytes(range(256))
    captured = {}

    def handler(request: httpx.Request) -> httpx.Response:
        captured["content_type"] = request.headers.get("content-type")
        captured["auth"] = request.headers.get("authorization")
        captured["raw"] = request.content
        return httpx.Response(
            200,
            json={
                "task_id": "task-xyz",
                "agent_run_id": "run-xyz",
                "project_id": None,
                "status": "pending",
                "message": None,
            },
        )

    async with leagent_harness(handler) as (client, db):
        resp = await client.post(
            "/api/agent/start",
            headers={"Authorization": "Bearer jwt-token"},
            data={
                "model_name": "openrouter/qwen",
                "agent_id": "agent-1",
                "prompt": "Solve the spreadsheet question",
                "skip_check_pending": "true",
            },
            files={"files": ("data.xlsx", file_bytes, "application/octet-stream")},
        )

    assert resp.status_code == 200
    assert resp.json()["task_id"] == "task-xyz"

    # The executor replayed an identical multipart request to the real API:
    assert captured["auth"] == "Bearer jwt-token"
    assert captured["content_type"].startswith("multipart/form-data; boundary=")
    # File payload + form fields survived inline, byte-for-byte.
    assert file_bytes in captured["raw"]
    assert b"openrouter/qwen" in captured["raw"]
    assert b"data.xlsx" in captured["raw"]

    # Audit metadata captured the file (best-effort multipart parse).
    rows = list(db.client.tables[AGENT_START.table].values())
    assert len(rows) == 1
    meta = rows[0]["request_meta"]
    assert meta["content_length"] > 0
    filenames = [f["filename"] for f in meta.get("files", [])]
    assert "data.xlsx" in filenames
    assert meta["form_fields"]["model_name"] == "openrouter/qwen"


async def test_agent_start_oversized_returns_413():
    big_file = b"x" * 5000

    def handler(request: httpx.Request) -> httpx.Response:  # pragma: no cover
        raise AssertionError("upstream must not be reached for oversized body")

    async with leagent_harness(handler, BRIDGE_MAX_BODY_BYTES="1024") as (client, db):
        resp = await client.post(
            "/api/agent/start",
            headers={"Authorization": "Bearer jwt-token"},
            data={"model_name": "m", "agent_id": "a"},
            files={"files": ("big.bin", big_file, "application/octet-stream")},
        )

    assert resp.status_code == 413
    assert db.client.tables.get(AGENT_START.table, {}) == {}
