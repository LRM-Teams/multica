"""Tests for the AReaL remote shell runner state machine and DB layer.

These use the in-memory ``FakeSupabaseClient`` (which emulates the
``areal_shell_*`` RPCs) plus a programmable fake executor, so the full claim ->
run -> terminal lifecycle is exercised without a live database or real tmux.
"""

from __future__ import annotations

import uuid
from collections.abc import Callable

from db_bridge.config import RemoteShellConfig
from db_bridge.remote_shell import RemoteShellDB, RemoteShellRunner
from db_bridge.shell_executor import CaptureResult, LaunchSpec, ShellExecutor, build_command_script

from _fakes import FakeSupabaseClient

USER = "00000000-0000-0000-0000-0000000000b1"
OTHER_USER = "00000000-0000-0000-0000-0000000000b2"
TMUX = "debug-gpu"
OTHER_TMUX = "train-loop"
SHELL_TABLE = "areal_remote_commands"


def _config(**overrides: str) -> RemoteShellConfig:
    env = {
        "SUPABASE_URL": "https://example.supabase.co",
        "SUPABASE_SERVICE_ROLE_KEY": "k",
        "AREAL_REMOTE_SHELL_ENABLED": "true",
        "AREAL_REMOTE_SHELL_RUNNER_ID": "runner-1",
        "AREAL_REMOTE_SHELL_POLL_INTERVAL": "0.01",
        "AREAL_REMOTE_SHELL_LEASE_SECONDS": "60",
        **overrides,
    }
    return RemoteShellConfig.from_env(env)


def _db(
    client: FakeSupabaseClient | None = None,
) -> tuple[RemoteShellDB, FakeSupabaseClient]:
    fake = client or FakeSupabaseClient()
    return RemoteShellDB(_config(), client=fake), fake


def _insert_pending(
    fake: FakeSupabaseClient,
    *,
    command: str = "echo hi",
    user_id: str = USER,
    tmux_id: str = TMUX,
    timeout: int = 30,
    status: str = "PENDING",
    runner_id: str | None = None,
    lease_epoch: float | None = None,
    cwd: str | None = None,
) -> str:
    cmd_id = str(uuid.uuid4())
    fake.tables.setdefault(SHELL_TABLE, {})[cmd_id] = {
        "id": cmd_id,
        "user_id": user_id,
        "tmux_id": tmux_id,
        "agent_run_id": None,
        "command": command,
        "cwd": cwd,
        "timeout_seconds": timeout,
        "status": status,
        "exit_code": None,
        "stdout_tail": "",
        "stderr_tail": "",
        "log_bytes": 0,
        "runner_id": runner_id,
        "lease_epoch": lease_epoch,
        "cancel_requested_at": None,
        "started_at": None,
        "finished_at": None,
        "metadata": {},
        "created_at": next(fake.seq),
    }
    return cmd_id


class FakeShellExecutor(ShellExecutor):
    """Programmable executor: scripts poll results per session."""

    def __init__(self, on_poll: Callable[[str], None] | None = None) -> None:
        self.launched: list[LaunchSpec] = []
        self.terminated: list[str] = []
        self.scripts: dict[str, list[CaptureResult]] = {}
        self.running = CaptureResult(b"", b"", 0, None)
        self._on_poll = on_poll

    async def launch(self, spec: LaunchSpec) -> None:
        self.launched.append(spec)

    async def poll(self, session: str, *, max_log_bytes: int) -> CaptureResult:
        if self._on_poll is not None:
            self._on_poll(session)
        seq = self.scripts.get(session)
        if seq:
            return seq.pop(0)
        return self.running

    async def terminate(self, session: str) -> None:
        self.terminated.append(session)


def _row(fake: FakeSupabaseClient, cmd_id: str) -> dict:
    return fake.tables[SHELL_TABLE][cmd_id]


def _session(cmd_id: str) -> str:
    return _config().session_name(TMUX)


def test_command_script_runs_arbitrary_shell_in_subshell_before_marker():
    script = build_command_script(
        command="echo before; exit 7",
        cwd=None,
        stdout_path="/tmp/out",
        stderr_path="/tmp/err",
        exit_path="/tmp/exit",
        marker="marker-1",
    )

    assert "(\necho before; exit 7\n)" in script
    assert "{\necho before; exit 7\n}" not in script
    assert "marker-1" in script


# -- DB layer / claiming ----------------------------------------------------


async def test_claims_only_pending_or_stale_eligible():
    db, fake = _db()
    pending = _insert_pending(fake, command="echo a")
    # A freshly leased CLAIMED row is not eligible.
    _insert_pending(
        fake,
        command="echo b",
        tmux_id=OTHER_TMUX,
        status="CLAIMED",
        runner_id="other",
        lease_epoch=1e18,
    )

    cmd = await db.claim_next("runner-1", 60)
    assert cmd is not None
    assert cmd.id == pending
    assert cmd.status == "CLAIMED"
    assert cmd.runner_id == "runner-1"

    # Nothing else is claimable now.
    assert await db.claim_next("runner-1", 60) is None


async def test_does_not_double_claim_a_leased_command():
    db, fake = _db()
    _insert_pending(fake, status="CLAIMED", runner_id="other", lease_epoch=1e18)
    assert await db.claim_next("runner-1", 60) is None


async def test_claim_serializes_pending_commands_with_same_tmux_id():
    db, fake = _db()
    first = _insert_pending(fake, command="echo first", tmux_id=TMUX)
    second = _insert_pending(fake, command="echo second", tmux_id=TMUX)
    other = _insert_pending(fake, command="echo other", tmux_id=OTHER_TMUX)

    claimed = await db.claim_next("runner-1", 60)
    assert claimed is not None
    assert claimed.id == first
    assert await db.claim_next("runner-1", 60) is not None
    assert _row(fake, other)["status"] == "CLAIMED"
    assert _row(fake, second)["status"] == "PENDING"


async def test_sweep_marks_running_stale_without_reexecution():
    db, fake = _db()
    cmd_id = _insert_pending(
        fake,
        status="RUNNING",
        runner_id="dead",
        lease_epoch=1.0,  # long expired
    )
    # An expired RUNNING row is never reclaimed for execution...
    assert await db.claim_next("runner-1", 60) is None
    # ...it is swept to STALE instead.
    assert await db.sweep_stale() == 1
    assert _row(fake, cmd_id)["status"] == "STALE"


# -- runner lifecycle -------------------------------------------------------


async def test_success_moves_through_states_and_records_exit_and_logs():
    db, fake = _db()
    cmd_id = _insert_pending(fake, command="echo hello")
    cmd = await db.claim_next("runner-1", 60)
    assert cmd is not None and cmd.id == cmd_id

    ex = FakeShellExecutor()
    ex.scripts[_session(cmd_id)] = [
        CaptureResult(b"hello\n", b"", 6, 0),
    ]
    runner = RemoteShellRunner(db, ex, _config())
    await runner.execute_command(cmd)

    row = _row(fake, cmd_id)
    assert row["status"] == "SUCCEEDED"
    assert row["exit_code"] == 0
    assert row["stdout_tail"] == "hello\n"
    assert row["started_at"] is not None
    assert row["finished_at"] is not None
    assert ex.launched and ex.launched[0].command == "echo hello"
    assert ex.launched[0].session == _session(cmd_id)
    assert ex.terminated == []


async def test_nonzero_exit_marks_failed():
    db, fake = _db()
    cmd_id = _insert_pending(fake, command="false")
    cmd = await db.claim_next("runner-1", 60)
    ex = FakeShellExecutor()
    ex.scripts[_session(cmd_id)] = [CaptureResult(b"", b"boom\n", 5, 3)]
    await RemoteShellRunner(db, ex, _config()).execute_command(cmd)

    row = _row(fake, cmd_id)
    assert row["status"] == "FAILED"
    assert row["exit_code"] == 3
    assert row["stderr_tail"] == "boom\n"


async def test_empty_command_fails_fast():
    db, fake = _db()
    cmd_id = _insert_pending(fake, command="   ")
    cmd = await db.claim_next("runner-1", 60)
    ex = FakeShellExecutor()
    await RemoteShellRunner(db, ex, _config()).execute_command(cmd)

    row = _row(fake, cmd_id)
    assert row["status"] == "FAILED"
    assert "empty command" in (row["error_message"] or "")
    assert not ex.launched  # never launched


async def test_cancellation_during_run_marks_cancelled():
    db, fake = _db()
    cmd_id = _insert_pending(fake, command="sleep 100")
    cmd = await db.claim_next("runner-1", 60)

    # When the runner polls, simulate a backend cancellation arriving.
    def cancel_on_poll(_session: str) -> None:
        _row(fake, cmd_id)["cancel_requested_at"] = 1.0

    ex = FakeShellExecutor(on_poll=cancel_on_poll)
    runner = RemoteShellRunner(db, ex, _config())
    await runner.execute_command(cmd)

    row = _row(fake, cmd_id)
    assert row["status"] == "CANCELLED"
    assert ex.terminated == [_session(cmd_id)]


async def test_timeout_marks_timed_out_and_terminates():
    db, fake = _db()
    cmd_id = _insert_pending(fake, command="sleep 100", timeout=1)
    cmd = await db.claim_next("runner-1", 60)

    ex = FakeShellExecutor()  # always "running", never exits
    runner = RemoteShellRunner(db, ex, _config(AREAL_REMOTE_SHELL_POLL_INTERVAL="0.02"))
    await runner.execute_command(cmd)

    row = _row(fake, cmd_id)
    assert row["status"] == "TIMED_OUT"
    assert ex.terminated == [_session(cmd_id)]


async def test_lost_lease_stops_without_finalizing():
    db, fake = _db()
    cmd_id = _insert_pending(fake, command="sleep 100")
    cmd = await db.claim_next("runner-1", 60)

    # Simulate the row being swept STALE between polls: heartbeat loses ownership.
    def steal_on_poll(_session: str) -> None:
        _row(fake, cmd_id)["status"] = "STALE"

    ex = FakeShellExecutor(on_poll=steal_on_poll)
    runner = RemoteShellRunner(db, ex, _config())
    await runner.execute_command(cmd)

    row = _row(fake, cmd_id)
    # Runner must not overwrite the swept terminal state.
    assert row["status"] == "STALE"
    assert ex.terminated == [_session(cmd_id)]


# -- cancellation semantics (backend-owned) ---------------------------------


async def test_request_cancel_pending_is_immediate():
    db, fake = _db()
    cmd_id = _insert_pending(fake)
    res = await db.request_cancel(cmd_id, user_id=USER)
    assert res == {"ok": True, "status": "CANCELLED"}
    assert _row(fake, cmd_id)["status"] == "CANCELLED"


async def test_request_cancel_running_flags_for_runner():
    db, fake = _db()
    cmd_id = _insert_pending(
        fake, status="RUNNING", runner_id="runner-1", lease_epoch=1e18
    )
    res = await db.request_cancel(cmd_id, user_id=USER)
    assert res == {"ok": True, "status": "CANCEL_REQUESTED"}


async def test_request_cancel_terminal_is_conflict():
    db, fake = _db()
    cmd_id = _insert_pending(fake, status="SUCCEEDED")
    res = await db.request_cancel(cmd_id, user_id=USER)
    assert res == {"ok": False, "status": "SUCCEEDED"}


async def test_request_cancel_other_user_is_not_found():
    db, fake = _db()
    cmd_id = _insert_pending(fake)
    assert await db.request_cancel(cmd_id, user_id=OTHER_USER) is None
    # Untouched.
    assert _row(fake, cmd_id)["status"] == "PENDING"


# -- poll loop integration --------------------------------------------------


async def test_poll_once_claims_and_runs():
    db, fake = _db()
    cmd_id = _insert_pending(fake, command="echo hi")
    ex = FakeShellExecutor()
    ex.scripts[_session(cmd_id)] = [CaptureResult(b"hi\n", b"", 3, 0)]
    runner = RemoteShellRunner(db, ex, _config())

    assert await runner.poll_once() is True
    # Drain the spawned execution task.
    import asyncio

    while fake.tables[SHELL_TABLE][cmd_id]["status"] not in {
        "SUCCEEDED",
        "FAILED",
    }:
        await asyncio.sleep(0.01)
    assert _row(fake, cmd_id)["status"] == "SUCCEEDED"
    assert await runner.poll_once() is False  # nothing left


async def test_disabled_runner_refuses_to_claim():
    db, fake = _db()
    _insert_pending(fake)
    ex = FakeShellExecutor()
    cfg = _config(AREAL_REMOTE_SHELL_ENABLED="false")
    runner = RemoteShellRunner(db, ex, cfg)
    await runner.run()  # returns immediately without claiming
    assert not ex.launched
