"""Unit tests for the async DB access layer (backed by the in-memory fake)."""

from __future__ import annotations

import time

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


# ---------------------------------------------------------------------------
# Streaming (SSE) relay
# ---------------------------------------------------------------------------


async def _claimed_stream_row(db: BridgeDB, *, user_id: str = USER_A) -> str:
    row_id = await db.insert_request(
        CHAT,
        user_id=user_id,
        method="POST",
        path="/chat/completions",
        headers={},
        content_type="application/json",
        body=b'{"model": "areal/x", "stream": true}',
    )
    claimed = await db.claim_next(CHAT, "w1", user_id=user_id)
    assert claimed is not None and claimed.id == row_id
    started = await db.start_stream(
        CHAT,
        row_id,
        worker_id="w1",
        response_status=200,
        response_headers={"content-type": "text/event-stream"},
    )
    assert started is True
    return row_id


async def test_start_stream_sets_streaming_status_and_meta(db: BridgeDB):
    row_id = await _claimed_stream_row(db)
    start = await db.wait_for_stream_start(CHAT, row_id, 1.0, user_id=USER_A)
    assert start is not None
    assert start.status == "streaming"
    assert start.response_status == 200
    assert start.headers["content-type"] == "text/event-stream"


async def test_start_stream_requires_owner_and_claimed(db: BridgeDB):
    row_id = await _claimed_stream_row(db)
    # Already streaming; a second start (even by owner) must not re-apply.
    assert (
        await db.start_stream(
            CHAT, row_id, worker_id="w1", response_status=200, response_headers={}
        )
        is False
    )
    # A fresh claimed row cannot be started by a non-owner worker.
    row2 = await db.insert_request(
        CHAT,
        user_id=USER_A,
        method="POST",
        path="/chat/completions",
        headers={},
        content_type=None,
        body=b"{}",
    )
    await db.claim_next(CHAT, "w1", user_id=USER_A)
    assert (
        await db.start_stream(
            CHAT, row2, worker_id="other", response_status=200, response_headers={}
        )
        is False
    )


async def test_append_and_stream_chunks_in_order(db: BridgeDB):
    row_id = await _claimed_stream_row(db)
    await db.append_chunk(
        CHAT, row_id, worker_id="w1", user_id=USER_A, seq=0, body=b"data: a\n\n"
    )
    await db.append_chunk(
        CHAT, row_id, worker_id="w1", user_id=USER_A, seq=1, body=b"data: b\n\n"
    )
    await db.append_chunk(
        CHAT, row_id, worker_id="w1", user_id=USER_A, seq=2, body=b"", is_final=True
    )
    out = [
        chunk
        async for chunk in db.stream_chunks(
            CHAT,
            row_id,
            user_id=USER_A,
            first_chunk_timeout=1.0,
            inter_chunk_timeout=1.0,
        )
    ]
    assert b"".join(out) == b"data: a\n\ndata: b\n\n"


async def test_append_chunk_rejects_non_owner_and_non_streaming(db: BridgeDB):
    row_id = await _claimed_stream_row(db)
    # Wrong worker cannot append.
    assert (
        await db.append_chunk(
            CHAT, row_id, worker_id="intruder", user_id=USER_A, seq=0, body=b"x"
        )
        is False
    )
    # Idempotent on seq: a duplicate seq is accepted but not duplicated.
    assert (
        await db.append_chunk(
            CHAT, row_id, worker_id="w1", user_id=USER_A, seq=0, body=b"data: a\n\n"
        )
        is True
    )
    assert (
        await db.append_chunk(
            CHAT, row_id, worker_id="w1", user_id=USER_A, seq=0, body=b"data: dup\n\n"
        )
        is True
    )
    chunks = await db.poll_chunks(CHAT, row_id, user_id=USER_A, after_seq=-1)
    assert len(chunks) == 1
    assert chunks[0].body == b"data: a\n\n"


async def test_poll_chunks_is_user_scoped(db: BridgeDB):
    row_id = await _claimed_stream_row(db)
    await db.append_chunk(
        CHAT, row_id, worker_id="w1", user_id=USER_A, seq=0, body=b"secret"
    )
    assert await db.poll_chunks(CHAT, row_id, user_id=USER_B, after_seq=-1) == []
    mine = await db.poll_chunks(CHAT, row_id, user_id=USER_A, after_seq=-1)
    assert [c.body for c in mine] == [b"secret"]


async def test_stream_chunks_stops_on_terminal_row_without_final(db: BridgeDB):
    row_id = await _claimed_stream_row(db)
    await db.append_chunk(
        CHAT, row_id, worker_id="w1", user_id=USER_A, seq=0, body=b"data: a\n\n"
    )
    # Simulate a crash finalized by the sweep: row errored, no is_final chunk.
    db.client.tables[CHAT.table][row_id]["status"] = "error"
    out = [
        chunk
        async for chunk in db.stream_chunks(
            CHAT,
            row_id,
            user_id=USER_A,
            first_chunk_timeout=1.0,
            inter_chunk_timeout=0.05,
        )
    ]
    assert out == [b"data: a\n\n"]


async def test_stream_chunks_first_chunk_timeout(db: BridgeDB):
    row_id = await _claimed_stream_row(db)  # streaming, no chunks
    out = [
        chunk
        async for chunk in db.stream_chunks(
            CHAT,
            row_id,
            user_id=USER_A,
            first_chunk_timeout=0.05,
            inter_chunk_timeout=0.05,
            poll_interval=0.01,
        )
    ]
    assert out == []


async def test_wait_for_stream_start_returns_none_on_timeout(db: BridgeDB):
    # Pending (never claimed/started) → wait times out.
    row_id = await db.insert_request(
        CHAT,
        user_id=USER_A,
        method="POST",
        path="/chat/completions",
        headers={},
        content_type=None,
        body=b"{}",
    )
    start = await db.wait_for_stream_start(
        CHAT, row_id, 0.05, user_id=USER_A, poll_interval=0.01
    )
    assert start is None


async def test_wait_for_stream_start_reports_error(db: BridgeDB):
    row_id = await db.insert_request(
        CHAT,
        user_id=USER_A,
        method="POST",
        path="/chat/completions",
        headers={},
        content_type=None,
        body=b"{}",
    )
    await db.abandon(CHAT, row_id, user_id=USER_A, error="boom")
    start = await db.wait_for_stream_start(CHAT, row_id, 1.0, user_id=USER_A)
    assert start is not None
    assert start.status == "error"
    assert start.error == "boom"


async def test_sweep_stale_streams_marks_error(db: BridgeDB):
    row_id = await _claimed_stream_row(db)
    # Age the heartbeat well past the stale window.
    db.client.tables[CHAT.table][row_id]["claimed_epoch"] = time.time() - 10_000
    swept = await db.sweep_stale_streams(CHAT, stale_seconds=300)
    assert swept == 1
    row = db.client.tables[CHAT.table][row_id]
    assert row["status"] == "error"
    # A healthy (recently heartbeated) stream is not swept.
    row2 = await _claimed_stream_row(db)
    assert await db.sweep_stale_streams(CHAT, stale_seconds=300) == 0
    assert db.client.tables[CHAT.table][row2]["status"] == "streaming"
