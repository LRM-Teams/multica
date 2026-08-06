"""Behavioural tests for schema.sql against a real Postgres.

Skipped unless ``BRIDGE_TEST_PG_DSN`` points at a disposable Postgres
(e.g. ``postgresql://postgres:postgres@localhost:5432/postgres``). The DSN's
database is mutated (schema applied, ``rpc_rl_set_reward`` truncated), so point
it at a throwaway instance only.

Verifies the two concurrency guarantees the bridge relies on:
  * concurrent ``bridge_claim_next`` calls never return the same row
    (FOR UPDATE SKIP LOCKED), and
  * a row abandoned in ``claimed`` past ``stale_seconds`` is reclaimed.
"""

from __future__ import annotations

import asyncio
import os
from pathlib import Path

import pytest

psycopg = pytest.importorskip("psycopg")
from psycopg import AsyncConnection  # noqa: E402

_DSN = os.environ.get("BRIDGE_TEST_PG_DSN")
pytestmark = pytest.mark.skipif(
    not _DSN, reason="BRIDGE_TEST_PG_DSN not set; skipping live Postgres schema tests"
)

_SCHEMA = (Path(__file__).resolve().parents[1] / "schema.sql").read_text()
_TABLE = "rpc_rl_set_reward"
_USER_ID = "00000000-0000-0000-0000-00000000000a"
_OTHER_USER_ID = "00000000-0000-0000-0000-00000000000b"

_INSERT = (
    f"insert into public.{_TABLE} "
    "(channel, user_id, status, request_method, request_path) "
    f"values ('rl_set_reward', '{_USER_ID}', 'pending', 'POST', '/rl/set_reward') returning id"
)


async def _apply_schema() -> None:
    async with await AsyncConnection.connect(_DSN, autocommit=True) as conn:
        await conn.execute(_SCHEMA)
        await conn.execute(f"truncate public.{_TABLE}")


@pytest.fixture
async def _ready_db():
    await _apply_schema()
    yield


async def test_concurrent_claims_are_unique(_ready_db):
    # Seed more rows than concurrent claimers.
    n_rows, n_claimers = 10, 6
    async with await AsyncConnection.connect(_DSN, autocommit=True) as conn:
        for _ in range(n_rows):
            await conn.execute(_INSERT)

    async def claim_holding_lock(worker: str):
        # Open a transaction and keep it open so the row lock is held while the
        # other claimers run — this is what forces SKIP LOCKED to take effect.
        conn = await AsyncConnection.connect(_DSN)
        cur = await conn.execute(
            "select public.bridge_claim_next(%s, %s, %s)",
            (_TABLE, worker, 300),
        )
        row = (await cur.fetchone())[0]
        return conn, row

    results = await asyncio.gather(
        *(claim_holding_lock(f"w{i}") for i in range(n_claimers))
    )
    try:
        claimed_ids = [r["id"] for _, r in results if r is not None]
        assert len(claimed_ids) == n_claimers, "every claimer should get a row"
        assert len(set(claimed_ids)) == n_claimers, "no row claimed twice"
    finally:
        for conn, _ in results:
            await conn.rollback()
            await conn.close()


async def test_claim_next_can_filter_by_user_id(_ready_db):
    other_insert = (
        f"insert into public.{_TABLE} "
        "(channel, user_id, status, request_method, request_path) "
        f"values ('rl_set_reward', '{_OTHER_USER_ID}', 'pending', 'POST', '/rl/set_reward') returning id"
    )
    async with await AsyncConnection.connect(_DSN, autocommit=True) as conn:
        await conn.execute(_INSERT)
        cur = await conn.execute(other_insert)
        other_id = (await cur.fetchone())[0]

        cur = await conn.execute(
            "select public.bridge_claim_next(%s, %s, %s, %s)",
            (_TABLE, "w-other", 300, _OTHER_USER_ID),
        )
        claimed = (await cur.fetchone())[0]

    assert claimed is not None
    assert claimed["id"] == str(other_id)
    assert claimed["user_id"] == _OTHER_USER_ID


async def test_returns_null_when_empty(_ready_db):
    async with await AsyncConnection.connect(_DSN, autocommit=True) as conn:
        cur = await conn.execute(
            "select public.bridge_claim_next(%s, %s, %s)", (_TABLE, "w", 300)
        )
        assert (await cur.fetchone())[0] is None


async def test_stale_claim_is_reclaimed(_ready_db):
    async with await AsyncConnection.connect(_DSN, autocommit=True) as conn:
        cur = await conn.execute(_INSERT)
        row_id = (await cur.fetchone())[0]
        # Abandon it in 'claimed' well in the past.
        await conn.execute(
            f"update public.{_TABLE} set status='claimed', worker_id='dead', "
            "claimed_at = now() - interval '10 minutes' where id = %s",
            (row_id,),
        )
        # Fresh stale window must NOT reclaim it yet.
        cur = await conn.execute(
            "select public.bridge_claim_next(%s, %s, %s)", (_TABLE, "w", 3600)
        )
        assert (await cur.fetchone())[0] is None
        # Short stale window reclaims it.
        cur = await conn.execute(
            "select public.bridge_claim_next(%s, %s, %s)", (_TABLE, "w2", 60)
        )
        reclaimed = (await cur.fetchone())[0]
        assert reclaimed is not None
        assert reclaimed["id"] == str(row_id)
        assert reclaimed["worker_id"] == "w2"


async def test_complete_marks_done_and_stores_response(_ready_db):
    async with await AsyncConnection.connect(_DSN, autocommit=True) as conn:
        cur = await conn.execute(_INSERT)
        row_id = (await cur.fetchone())[0]
        cur = await conn.execute(
            "select public.bridge_claim_next(%s, %s, %s)", (_TABLE, "w", 300)
        )
        claimed = (await cur.fetchone())[0]
        assert claimed["id"] == str(row_id)
        await conn.execute(
            "select public.bridge_complete(%s, %s, %s, %s, %s, %s, %s, %s, %s)",
            (
                _TABLE,
                row_id,
                "w",
                "done",
                200,
                '{"content-type": "application/json"}',
                '{"ok": true}',
                "raw",
                None,
            ),
        )
        cur = await conn.execute(
            f"select status, response_status, response_body, completed_at "
            f"from public.{_TABLE} where id = %s",
            (row_id,),
        )
        status, resp_status, body, completed_at = await cur.fetchone()
        assert status == "done"
        assert resp_status == 200
        assert body == '{"ok": true}'
        assert completed_at is not None


async def test_stale_worker_completion_cannot_overwrite_reclaimed_row(_ready_db):
    async with await AsyncConnection.connect(_DSN, autocommit=True) as conn:
        cur = await conn.execute(_INSERT)
        row_id = (await cur.fetchone())[0]
        cur = await conn.execute(
            "select public.bridge_claim_next(%s, %s, %s)", (_TABLE, "old", 300)
        )
        assert (await cur.fetchone())[0]["id"] == str(row_id)
        await conn.execute(
            f"update public.{_TABLE} set claimed_at = now() - interval '10 minutes' "
            "where id = %s",
            (row_id,),
        )
        cur = await conn.execute(
            "select public.bridge_claim_next(%s, %s, %s)", (_TABLE, "new", 60)
        )
        assert (await cur.fetchone())[0]["worker_id"] == "new"

        cur = await conn.execute(
            "select public.bridge_complete(%s, %s, %s, %s, %s, %s, %s, %s, %s)",
            (
                _TABLE,
                row_id,
                "new",
                "done",
                200,
                '{"x-worker": "new"}',
                "new",
                "raw",
                None,
            ),
        )
        assert (await cur.fetchone())[0] is True
        cur = await conn.execute(
            "select public.bridge_complete(%s, %s, %s, %s, %s, %s, %s, %s, %s)",
            (
                _TABLE,
                row_id,
                "old",
                "done",
                200,
                '{"x-worker": "old"}',
                "old",
                "raw",
                None,
            ),
        )
        assert (await cur.fetchone())[0] is False
        cur = await conn.execute(
            f"select response_headers, response_body from public.{_TABLE} where id = %s",
            (row_id,),
        )
        headers, body = await cur.fetchone()
        assert headers == {"x-worker": "new"}
        assert body == "new"
