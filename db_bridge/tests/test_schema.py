"""Static structural checks for schema.sql.

These run without a database and guard against the migration drifting from the
channel registry or losing its concurrency primitives. The behavioural
guarantees (SKIP LOCKED, stale reclaim) are covered by
``test_schema_integration.py`` when a Postgres DSN is available.
"""

from __future__ import annotations

from pathlib import Path

from db_bridge import channels

_SCHEMA = (Path(__file__).resolve().parents[1] / "schema.sql").read_text()


def test_every_channel_table_is_created():
    for table in channels.TABLE_NAMES:
        assert f"'{table}'" in _SCHEMA, f"{table} missing from schema.sql table list"


def test_no_unexpected_tables_in_loop():
    # The array literal in the DO block must match the registry exactly.
    import re

    block = _SCHEMA.split("tables text[] := array[")[1].split("]")[0]
    listed = set(re.findall(r"'(rpc_[a-z0-9_]+)'", block))
    assert listed == set(channels.TABLE_NAMES)


def test_user_id_column_and_claim_index_exist():
    assert "user_id                uuid not null" in _SCHEMA
    assert "add column if not exists user_id uuid" in _SCHEMA
    assert "alter column user_id set not null" in _SCHEMA
    assert "user_status_created_idx" in _SCHEMA
    assert "(user_id, status, created_at)" in _SCHEMA


def test_claim_function_uses_skip_locked_and_ordering():
    assert "create or replace function public.bridge_claim_next" in _SCHEMA
    assert "for update skip locked" in _SCHEMA
    assert "order by created_at" in _SCHEMA
    # Stale reclaim path.
    assert "make_interval(secs => $1)" in _SCHEMA
    assert "status = 'claimed'" in _SCHEMA
    assert "p_user_id       uuid" in _SCHEMA
    assert "($3 is null or user_id = $3)" in _SCHEMA


def test_complete_redact_and_cleanup_functions_exist():
    assert "create or replace function public.bridge_complete" in _SCHEMA
    assert "create or replace function public.bridge_redact_headers" in _SCHEMA
    assert "create or replace function public.bridge_abandon" in _SCHEMA
    assert "p_user_id uuid" in _SCHEMA
    assert "create or replace function public.bridge_cleanup_stale" in _SCHEMA


def test_cleanup_has_terminal_status_index_and_limit():
    assert "status_completed_idx" in _SCHEMA
    assert "limit $2" in _SCHEMA
    assert "status in ('done', 'error')" in _SCHEMA


def test_status_check_constraint():
    assert "check (status in ('pending', 'claimed', 'done', 'error'))" in _SCHEMA


def test_rls_enabled_and_functions_locked_down():
    assert "enable row level security" in _SCHEMA
    assert "revoke all on function" in _SCHEMA
    assert "to service_role" in _SCHEMA


def test_table_name_allowlist_guards_dynamic_sql():
    # Every dynamic-SQL function must validate the table name before EXECUTE.
    assert _SCHEMA.count("invalid bridge table name") >= 5
    assert _SCHEMA.count("unknown bridge table") >= 5
