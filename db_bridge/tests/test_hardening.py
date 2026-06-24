"""Hardening + observability tests (Task 10).

Covers optional at-rest token encryption, post-completion token redaction,
queue-depth counting, and in-process metrics.
"""

from __future__ import annotations

import asyncio
import contextlib

import httpx
import pytest

from db_bridge import stub_server
from db_bridge import crypto
from db_bridge.channels import CHANNELS_BY_NAME
from db_bridge.config import BridgeConfig
from db_bridge.db import BridgeDB
from db_bridge.executor import Executor
from db_bridge.metrics import get_metrics
from db_bridge.stub_server import create_stub_app

from _fakes import FakeSupabaseClient

SET_REWARD = CHANNELS_BY_NAME["rl_set_reward"]
USER_ID = "00000000-0000-0000-0000-00000000000a"


def _config(**overrides: str) -> BridgeConfig:
    env = {
        "SUPABASE_URL": "https://example.supabase.co",
        "SUPABASE_SERVICE_ROLE_KEY": "k",
        "BRIDGE_POLL_INTERVAL": "0.01",
        "BRIDGE_USER_ID": USER_ID,
        "BRIDGE_STATS_INTERVAL": "0",  # disable periodic logger in tests
        "BRIDGE_CONCURRENCY_CHAT_COMPLETIONS": "1",
        **overrides,
    }
    return BridgeConfig.from_env(env)


@contextlib.asynccontextmanager
async def gateway_harness(cfg, handler):
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


# -- crypto unit tests ------------------------------------------------------


def test_build_cipher_none_without_key():
    assert crypto.build_cipher(None) is None
    assert crypto.build_cipher("") is None


@pytest.mark.skipif(
    not crypto.encryption_available(), reason="cryptography not installed"
)
def test_cipher_roundtrip_and_idempotence():
    from cryptography.fernet import Fernet

    cipher = crypto.build_cipher(Fernet.generate_key().decode())
    enc = cipher.encrypt("Bearer secret")
    assert enc.startswith("enc:v1:")
    assert cipher.decrypt(enc) == "Bearer secret"
    # Encrypt is idempotent; decrypt passes plaintext through.
    assert cipher.encrypt(enc) == enc
    assert cipher.decrypt("Bearer plain") == "Bearer plain"


@pytest.mark.skipif(
    not crypto.encryption_available(), reason="cryptography not installed"
)
def test_encrypt_headers_only_touches_authorization():
    from cryptography.fernet import Fernet

    cipher = crypto.build_cipher(Fernet.generate_key().decode())
    headers = {"authorization": "Bearer x", "content-type": "application/json"}
    enc = crypto.encrypt_headers(headers, cipher)
    assert enc["authorization"].startswith("enc:v1:")
    assert enc["content-type"] == "application/json"
    assert crypto.decrypt_headers(enc, cipher)["authorization"] == "Bearer x"


# -- encryption end to end --------------------------------------------------


@pytest.mark.skipif(
    not crypto.encryption_available(), reason="cryptography not installed"
)
async def test_token_encrypted_at_rest_but_replayed_plaintext():
    from cryptography.fernet import Fernet

    cfg = _config(BRIDGE_HEADER_ENCRYPTION_KEY=Fernet.generate_key().decode())
    seen = {}

    def handler(request: httpx.Request) -> httpx.Response:
        seen["auth"] = request.headers.get("authorization")
        return httpx.Response(200, json={"ok": True})

    async with gateway_harness(cfg, handler) as (client, db):
        resp = await client.post(
            "/rl/set_reward",
            headers={"Authorization": "Bearer sk-session", "X-Bridge-User-Id": USER_ID},
            json={"reward": 1.0},
        )
        assert resp.status_code == 200

    # Upstream received the real token (decrypted just before replay)...
    assert seen["auth"] == "Bearer sk-session"
    # ...but the stored row holds ciphertext, not the plaintext token.
    row = next(iter(db.client.tables[SET_REWARD.table].values()))
    stored = row["request_headers"]["authorization"]
    assert stored.startswith("enc:v1:")
    assert "sk-session" not in stored


# -- redaction after completion --------------------------------------------


async def test_redaction_after_complete_keeps_relay_then_scrubs():
    cfg = _config(BRIDGE_REDACT_TOKENS_AFTER_COMPLETE="true")
    seen = {}

    def handler(request: httpx.Request) -> httpx.Response:
        seen["auth"] = request.headers.get("authorization")
        return httpx.Response(200, json={"ok": True})

    async with gateway_harness(cfg, handler) as (client, db):
        resp = await client.post(
            "/rl/set_reward",
            headers={"Authorization": "Bearer sk-session", "X-Bridge-User-Id": USER_ID},
            json={"reward": 1.0},
        )
        assert resp.status_code == 200

    # Token was relayed first...
    assert seen["auth"] == "Bearer sk-session"
    # ...then scrubbed from the retained audit row.
    row = next(iter(db.client.tables[SET_REWARD.table].values()))
    assert row["request_headers"]["authorization"] == "REDACTED"


# -- queue depth + metrics --------------------------------------------------


async def test_count_pending():
    cfg = _config()
    db = BridgeDB(cfg, client=FakeSupabaseClient())
    assert await db.count_pending(SET_REWARD) == 0
    await db.insert_request(
        SET_REWARD,
        user_id=USER_ID,
        method="POST",
        path="/rl/set_reward",
        headers={},
        content_type=None,
        body=b"{}",
    )
    await db.insert_request(
        SET_REWARD,
        user_id=USER_ID,
        method="POST",
        path="/rl/set_reward",
        headers={},
        content_type=None,
        body=b"{}",
    )
    assert await db.count_pending(SET_REWARD) == 2


async def test_metrics_increment_on_roundtrip():
    cfg = _config()
    metrics = get_metrics()
    before = metrics.snapshot().get(SET_REWARD.name, {})
    base_done = before.get("done", 0)
    base_fwd = before.get("forwarded", 0)

    def handler(request: httpx.Request) -> httpx.Response:
        return httpx.Response(200, json={"ok": True})

    async with gateway_harness(cfg, handler) as (client, _db):
        resp = await client.post(
            "/rl/set_reward",
            headers={"X-Bridge-User-Id": USER_ID},
            json={"reward": 1.0},
        )
        assert resp.status_code == 200

    after = metrics.snapshot()[SET_REWARD.name]
    assert after["done"] >= base_done + 1
    assert after["forwarded"] >= base_fwd + 1


async def test_stub_stats_interval_zero_does_not_start_stats_task(monkeypatch):
    cfg = _config(BRIDGE_STATS_INTERVAL="0")
    db = BridgeDB(cfg, client=FakeSupabaseClient())
    created = []

    def fake_create_task(coro):
        created.append(coro)
        raise AssertionError("stats task should not be created")

    monkeypatch.setattr(stub_server.asyncio, "create_task", fake_create_task)
    app = create_stub_app(db, "leagent", cfg)
    async with app.router.lifespan_context(app):
        pass
    assert created == []
