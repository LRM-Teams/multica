"""Unit tests for the async DB access layer (backed by the in-memory fake)."""

from __future__ import annotations

import pytest

from db_bridge import codec
from db_bridge.channels import CHANNELS_BY_NAME
from db_bridge.config import BridgeConfig
from db_bridge.db import BridgeDB

from _fakes import FakeSupabaseClient

_MINIMAL = {
    "SUPABASE_URL": "https://example.supabase.co",
    "SUPABASE_SERVICE_ROLE_KEY": "service-key",
    "BRIDGE_POLL_INTERVAL": "0.01",
}

CHAT = CHANNELS_BY_NAME["chat_completions"]
SET_REWARD = CHANNELS_BY_NAME["rl_set_reward"]
USER_A = "00000000-0000-0000-0000-00000000000a"
USER_B = "00000000-0000-0000-0000-00000000000b"


@pytest.fixture
def db() -> BridgeDB:
    return BridgeDB(BridgeConfig.from_env(_MINIMAL), client=FakeSupabaseClient())


async def test_insert_request_encodes_body_and_returns_id(db: BridgeDB):
    row_id = await db.insert_request(
        SET_REWARD,
        user_id=USER_A,
        method="POST",
        path="/rl/set_reward",
        headers={"authorization": "Bearer k"},
        content_type="application/json",
        body=b'{"reward": 1.0}',
    )
    store = db.client.tables[SET_REWARD.table]
    assert row_id in store
    row = store[row_id]
    assert row["status"] == "pending"
    assert row["request_method"] == "POST"
    assert row["request_headers"] == {"authorization": "Bearer k"}
    # Small JSON stays raw for auditability.
    assert row["request_body_encoding"] == codec.RAW
    assert row["request_body"] == '{"reward": 1.0}'


async def test_poll_does_not_fetch_request_body_columns(db: BridgeDB):
    row_id = await db.insert_request(
        SET_REWARD,
        user_id=USER_A,
        method="POST",
        path="/rl/set_reward",
        headers={},
        content_type=None,
        body=b"{}",
    )
    await db.poll_response(SET_REWARD, row_id, user_id=USER_A)
    cols = db.client.last_select_columns or ""
    assert "request_body" not in cols
    assert "request_headers" not in cols
    assert "status" in cols and "response_body" in cols


async def test_claim_next_decodes_request(db: BridgeDB):
    big = b'{"messages": "' + b"x" * 5000 + b'"}'  # forces gzip+base64
    await db.insert_request(
        CHAT,
        user_id=USER_A,
        method="POST",
        path="/chat/completions",
        headers={"authorization": "Bearer s"},
        content_type="application/json",
        body=big,
    )
    claimed = await db.claim_next(CHAT, "worker-1")
    assert claimed is not None
    assert claimed.worker_id == "worker-1"
    assert claimed.method == "POST"
    assert claimed.path == "/chat/completions"
    assert claimed.headers == {"authorization": "Bearer s"}
    assert claimed.body == big  # decoded back to original bytes
    # Row is now marked claimed in the store.
    assert db.client.tables[CHAT.table][claimed.id]["status"] == "claimed"


async def test_claim_next_returns_none_when_empty(db: BridgeDB):
    assert await db.claim_next(CHAT, "w") is None


async def test_complete_then_wait_roundtrips_response(db: BridgeDB):
    row_id = await db.insert_request(
        SET_REWARD,
        user_id=USER_A,
        method="POST",
        path="/rl/set_reward",
        headers={},
        content_type="application/json",
        body=b"{}",
    )
    claimed = await db.claim_next(SET_REWARD, "worker-1")
    assert claimed is not None
    assert (
        await db.complete(
            SET_REWARD,
            row_id,
            worker_id=claimed.worker_id,
            response_status=200,
            response_headers={"content-type": "application/json"},
            body=b'{"interaction_count": 3}',
        )
        is True
    )
    resp = await db.wait_for_response(SET_REWARD, row_id, timeout=1.0, user_id=USER_A)
    assert resp is not None
    assert resp.status == "done"
    assert resp.response_status == 200
    assert resp.headers == {"content-type": "application/json"}
    assert resp.body == b'{"interaction_count": 3}'
    assert resp.error is None


async def test_fail_marks_error(db: BridgeDB):
    row_id = await db.insert_request(
        SET_REWARD,
        user_id=USER_A,
        method="POST",
        path="/rl/set_reward",
        headers={},
        content_type=None,
        body=b"{}",
    )
    claimed = await db.claim_next(SET_REWARD, "worker-1")
    assert claimed is not None
    assert (
        await db.fail(
            SET_REWARD,
            row_id,
            worker_id=claimed.worker_id,
            error="upstream connection refused",
        )
        is True
    )
    resp = await db.wait_for_response(SET_REWARD, row_id, timeout=1.0, user_id=USER_A)
    assert resp is not None
    assert resp.status == "error"
    assert resp.error == "upstream connection refused"


async def test_wait_times_out_when_no_response(db: BridgeDB):
    row_id = await db.insert_request(
        SET_REWARD,
        user_id=USER_A,
        method="POST",
        path="/rl/set_reward",
        headers={},
        content_type=None,
        body=b"{}",
    )
    resp = await db.wait_for_response(SET_REWARD, row_id, timeout=0.05, user_id=USER_A)
    assert resp is None


async def test_abandon_pending_request_prevents_later_claim(db: BridgeDB):
    row_id = await db.insert_request(
        SET_REWARD,
        user_id=USER_A,
        method="POST",
        path="/rl/set_reward",
        headers={},
        content_type=None,
        body=b"{}",
    )

    assert (
        await db.abandon(SET_REWARD, row_id, user_id=USER_A, error="stub timed out")
        is True
    )
    assert await db.claim_next(SET_REWARD, "worker-1") is None

    resp = await db.wait_for_response(SET_REWARD, row_id, timeout=1.0, user_id=USER_A)
    assert resp is not None
    assert resp.status == "error"
    assert resp.error == "stub timed out"


async def test_abandon_claimed_request_rejects_late_completion(db: BridgeDB):
    row_id = await db.insert_request(
        SET_REWARD,
        user_id=USER_A,
        method="POST",
        path="/rl/set_reward",
        headers={},
        content_type=None,
        body=b"{}",
    )
    claimed = await db.claim_next(SET_REWARD, "worker-1")
    assert claimed is not None

    assert (
        await db.abandon(SET_REWARD, row_id, user_id=USER_A, error="stub timed out")
        is True
    )
    assert (
        await db.complete(
            SET_REWARD,
            row_id,
            worker_id=claimed.worker_id,
            response_status=200,
            response_headers={},
            body=b"late",
        )
        is False
    )

    resp = await db.wait_for_response(SET_REWARD, row_id, timeout=1.0, user_id=USER_A)
    assert resp is not None
    assert resp.status == "error"
    assert resp.error == "stub timed out"


async def test_user_id_is_stored_and_used_for_response_polling(db: BridgeDB):
    row_id = await db.insert_request(
        SET_REWARD,
        user_id=USER_A,
        method="POST",
        path="/rl/set_reward",
        headers={},
        content_type=None,
        body=b"{}",
    )
    row = db.client.tables[SET_REWARD.table][row_id]
    assert row["user_id"] == USER_A

    assert await db.poll_response(SET_REWARD, row_id, user_id=USER_A)
    assert await db.poll_response(SET_REWARD, row_id, user_id=USER_B) == {}


async def test_claim_next_can_be_scoped_by_user_id(db: BridgeDB):
    row_a = await db.insert_request(
        SET_REWARD,
        user_id=USER_A,
        method="POST",
        path="/rl/set_reward",
        headers={},
        content_type=None,
        body=b"{}",
    )
    row_b = await db.insert_request(
        SET_REWARD,
        user_id=USER_B,
        method="POST",
        path="/rl/set_reward",
        headers={},
        content_type=None,
        body=b"{}",
    )

    claimed = await db.claim_next(SET_REWARD, "worker-b", user_id=USER_B)

    assert claimed is not None
    assert claimed.id == row_b
    assert db.client.tables[SET_REWARD.table][row_a]["status"] == "pending"
    assert db.client.tables[SET_REWARD.table][row_b]["status"] == "claimed"


async def test_abandon_is_scoped_by_user_id(db: BridgeDB):
    row_id = await db.insert_request(
        SET_REWARD,
        user_id=USER_A,
        method="POST",
        path="/rl/set_reward",
        headers={},
        content_type=None,
        body=b"{}",
    )

    assert (
        await db.abandon(SET_REWARD, row_id, user_id=USER_B, error="wrong user")
        is False
    )
    assert db.client.tables[SET_REWARD.table][row_id]["status"] == "pending"

    assert (
        await db.abandon(SET_REWARD, row_id, user_id=USER_A, error="right user") is True
    )
    assert db.client.tables[SET_REWARD.table][row_id]["status"] == "error"


async def test_large_response_roundtrip(db: BridgeDB):
    row_id = await db.insert_request(
        CHAT,
        user_id=USER_A,
        method="POST",
        path="/chat/completions",
        headers={},
        content_type="application/json",
        body=b"{}",
    )
    claimed = await db.claim_next(CHAT, "worker-1")
    assert claimed is not None
    big_response = b'{"logprobs": [' + b"0.1," * 100000 + b"0.0]}"
    assert (
        await db.complete(
            CHAT,
            row_id,
            worker_id=claimed.worker_id,
            response_status=200,
            response_headers={"content-type": "application/json"},
            body=big_response,
        )
        is True
    )
    resp = await db.wait_for_response(CHAT, row_id, timeout=1.0, user_id=USER_A)
    assert resp is not None
    assert resp.body == big_response
    # Stored compressed.
    assert (
        db.client.tables[CHAT.table][row_id]["response_body_encoding"]
        == codec.GZIP_BASE64
    )


async def test_stale_worker_cannot_overwrite_reclaimed_completion(db: BridgeDB):
    row_id = await db.insert_request(
        SET_REWARD,
        user_id=USER_A,
        method="POST",
        path="/rl/set_reward",
        headers={},
        content_type="application/json",
        body=b"{}",
    )
    first = await db.claim_next(SET_REWARD, "worker-old")
    assert first is not None

    row = db.client.tables[SET_REWARD.table][row_id]
    row["claimed_epoch"] -= db.config.stale_seconds + 1
    second = await db.claim_next(SET_REWARD, "worker-new")
    assert second is not None
    assert second.id == row_id

    assert (
        await db.complete(
            SET_REWARD,
            row_id,
            worker_id=second.worker_id,
            response_status=200,
            response_headers={"x-worker": "new"},
            body=b"new",
        )
        is True
    )
    assert (
        await db.complete(
            SET_REWARD,
            row_id,
            worker_id=first.worker_id,
            response_status=200,
            response_headers={"x-worker": "old"},
            body=b"old",
        )
        is False
    )

    resp = await db.wait_for_response(SET_REWARD, row_id, timeout=1.0, user_id=USER_A)
    assert resp is not None
    assert resp.body == b"new"
    assert resp.headers == {"x-worker": "new"}


async def test_cleanup_stale_terminal_rows_deletes_only_old_terminal(db: BridgeDB):
    old_done = await db.insert_request(
        SET_REWARD,
        user_id=USER_A,
        method="POST",
        path="/rl/set_reward",
        headers={},
        content_type=None,
        body=b"{}",
    )
    fresh_done = await db.insert_request(
        SET_REWARD,
        user_id=USER_A,
        method="POST",
        path="/rl/set_reward",
        headers={},
        content_type=None,
        body=b"{}",
    )
    pending = await db.insert_request(
        SET_REWARD,
        user_id=USER_A,
        method="POST",
        path="/rl/set_reward",
        headers={},
        content_type=None,
        body=b"{}",
    )

    store = db.client.tables[SET_REWARD.table]
    store[old_done]["status"] = "done"
    store[old_done]["completed_at"] = 1.0
    store[fresh_done]["status"] = "done"
    store[fresh_done]["completed_at"] = 9_999_999_999.0

    deleted = await db.cleanup_stale_rows(SET_REWARD, retention_seconds=60, limit=100)

    assert deleted == 1
    assert old_done not in store
    assert fresh_done in store
    assert pending in store


async def test_cleanup_stale_terminal_rows_honors_limit(db: BridgeDB):
    ids = [
        await db.insert_request(
            SET_REWARD,
            user_id=USER_A,
            method="POST",
            path="/rl/set_reward",
            headers={},
            content_type=None,
            body=b"{}",
        )
        for _ in range(3)
    ]
    store = db.client.tables[SET_REWARD.table]
    for row_id in ids:
        store[row_id]["status"] = "error"
        store[row_id]["completed_at"] = 1.0

    deleted = await db.cleanup_stale_rows(SET_REWARD, retention_seconds=60, limit=2)

    assert deleted == 2
    assert len(store) == 1


async def test_count_pending_can_be_scoped_by_user_id(db: BridgeDB):
    await db.insert_request(
        SET_REWARD,
        user_id=USER_A,
        method="POST",
        path="/rl/set_reward",
        headers={},
        content_type=None,
        body=b"{}",
    )
    await db.insert_request(
        SET_REWARD,
        user_id=USER_B,
        method="POST",
        path="/rl/set_reward",
        headers={},
        content_type=None,
        body=b"{}",
    )

    assert await db.count_pending(SET_REWARD) == 2
    assert await db.count_pending(SET_REWARD, user_id=USER_A) == 1
    assert await db.count_pending(SET_REWARD, user_id=USER_B) == 1


async def test_redact_headers(db: BridgeDB):
    row_id = await db.insert_request(
        SET_REWARD,
        user_id=USER_A,
        method="POST",
        path="/rl/set_reward",
        headers={"authorization": "Bearer secret"},
        content_type=None,
        body=b"{}",
    )
    await db.redact_headers(SET_REWARD, row_id)
    assert db.client.tables[SET_REWARD.table][row_id]["request_headers"] == {
        "authorization": "REDACTED"
    }
