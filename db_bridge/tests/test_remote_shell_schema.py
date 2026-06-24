"""Static structural checks for the remote-shell additions to schema.sql.

These run without a database and guard the table, status constraint, claim
ordering, lease sweep, owner guards, RLS, and service-role lockdown from
drifting away from the runner's expectations.
"""

from __future__ import annotations

from pathlib import Path

_SCHEMA = (Path(__file__).resolve().parents[1] / "schema.sql").read_text()


def test_table_and_core_columns_exist():
    assert "create table if not exists public.areal_remote_commands" in _SCHEMA
    for column in (
        "user_id             uuid not null",
        "tmux_id             text not null",
        "command             text not null",
        "timeout_seconds     integer not null",
        "stdout_tail         text not null default ''",
        "stderr_tail         text not null default ''",
        "log_bytes           integer not null default 0",
        "lease_expires_at    timestamptz",
        "cancel_requested_at timestamptz",
    ):
        assert column in _SCHEMA, f"missing column definition: {column!r}"


def test_status_check_constraint_lists_all_states():
    for status in (
        "PENDING",
        "CLAIMED",
        "RUNNING",
        "SUCCEEDED",
        "FAILED",
        "CANCEL_REQUESTED",
        "CANCELLED",
        "TIMED_OUT",
        "STALE",
    ):
        assert f"'{status}'" in _SCHEMA, f"status {status} missing from schema"
    assert "areal_remote_commands_status_chk" in _SCHEMA


def test_indexes_exist():
    assert "areal_remote_commands_claim_idx" in _SCHEMA
    assert "areal_remote_commands_lease_idx" in _SCHEMA
    assert "areal_remote_commands_tmux_idx" in _SCHEMA
    assert "areal_remote_commands_user_idx" in _SCHEMA
    assert "create index if not exists areal_remote_commands_task_idx" not in _SCHEMA
    assert "create index if not exists areal_remote_commands_account_idx" not in _SCHEMA
    assert "drop index if exists areal_remote_commands_task_idx" in _SCHEMA
    assert "drop index if exists areal_remote_commands_account_idx" in _SCHEMA


def test_functions_exist():
    for fn in (
        "areal_shell_claim_next",
        "areal_shell_mark_running",
        "areal_shell_heartbeat",
        "areal_shell_complete",
        "areal_shell_sweep_stale",
        "areal_shell_request_cancel",
        "areal_shell_cleanup",
    ):
        assert f"create or replace function public.{fn}" in _SCHEMA


def test_claim_uses_skip_locked_and_ordering():
    assert "for update skip locked" in _SCHEMA
    assert "order by created_at" in _SCHEMA
    assert "tmux_id = c.tmux_id" in _SCHEMA
    assert "status in ('CLAIMED', 'RUNNING', 'CANCEL_REQUESTED')" in _SCHEMA
    # Claim reclaims only CLAIMED-expired rows (not RUNNING).
    assert "status = 'CLAIMED'" in _SCHEMA
    assert "lease_expires_at < now()" in _SCHEMA


def test_sweep_targets_running_and_cancel_requested_only():
    assert "status in ('RUNNING', 'CANCEL_REQUESTED')" in _SCHEMA
    assert "'STALE'" in _SCHEMA


def test_owner_guards_present():
    # complete/mark_running/heartbeat must be owner-guarded.
    assert _SCHEMA.count("and runner_id = p_runner_id") >= 3


def test_rls_enabled_and_functions_locked_down():
    assert (
        "alter table public.areal_remote_commands enable row level security" in _SCHEMA
    )
    assert "grant all on table public.areal_remote_commands to service_role" in _SCHEMA
    assert "grant execute on function %s to service_role" in _SCHEMA
    # The shell permission block lists every function signature.
    assert "$shell_perm$" in _SCHEMA
