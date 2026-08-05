"""AReaL DB-backed tmux remote shell runner.

This module contains the AReaL-host side of the remote shell feature:

* :class:`RemoteShellDB` -- async Supabase access for the ``areal_remote_commands``
  queue (claim / mark-running / heartbeat / complete / sweep / cancel).
* :class:`RemoteShellRunner` -- the polling state machine that claims commands,
  executes them through a :class:`~db_bridge.shell_executor.ShellExecutor`, keeps
  the lease alive with heartbeats, and writes terminal status, exit code and
  bounded logs back to the database.

The runner uses service-role credentials because it claims and updates rows
across users. User-facing authorization (feature flag, task access) lives in the
le-agent backend that creates the rows; the runner trusts that any ``PENDING``
row was authorized when it was enqueued.
"""

from __future__ import annotations

import asyncio
import logging
import os
import time
import uuid
from dataclasses import dataclass, field
from typing import Any, Final

from supabase import AsyncClient, create_async_client
from supabase.lib.client_options import AsyncClientOptions

from .config import RemoteShellConfig
from .db import _build_httpx_pool
from .shell_executor import (
    CaptureResult,
    LaunchSpec,
    ShellExecutor,
    ShellExecutorError,
)

logger = logging.getLogger("db_bridge.remote_shell")

_TABLE: Final = "areal_remote_commands"

# Ceiling for the claim-retry backoff. Supabase has wedged for minutes at a
# time; without a ceiling the runner would either keep hammering a database
# that is already struggling or drift into sleeps far longer than the outage.
_CLAIM_BACKOFF_MAX_S: Final = 60.0

# Status constants (mirror the schema check constraint).
STATUS_PENDING: Final = "PENDING"
STATUS_CLAIMED: Final = "CLAIMED"
STATUS_RUNNING: Final = "RUNNING"
STATUS_SUCCEEDED: Final = "SUCCEEDED"
STATUS_FAILED: Final = "FAILED"
STATUS_CANCEL_REQUESTED: Final = "CANCEL_REQUESTED"
STATUS_CANCELLED: Final = "CANCELLED"
STATUS_TIMED_OUT: Final = "TIMED_OUT"
STATUS_STALE: Final = "STALE"


@dataclass(slots=True)
class ShellCommand:
    """A claimed command row, decoded for execution."""

    id: str
    user_id: str
    tmux_id: str
    command: str
    timeout_seconds: int
    status: str
    agent_run_id: str | None = None
    cwd: str | None = None
    runner_id: str | None = None
    metadata: dict[str, Any] = field(default_factory=dict)

    @classmethod
    def from_row(cls, row: dict[str, Any]) -> ShellCommand:
        return cls(
            id=row["id"],
            user_id=row["user_id"],
            tmux_id=row["tmux_id"],
            command=row["command"],
            timeout_seconds=int(row.get("timeout_seconds") or 0),
            status=row.get("status", STATUS_CLAIMED),
            agent_run_id=row.get("agent_run_id"),
            cwd=row.get("cwd"),
            runner_id=row.get("runner_id"),
            metadata=row.get("metadata") or {},
        )


@dataclass(slots=True)
class Heartbeat:
    """Result of a heartbeat: the current status and cancel flag."""

    status: str
    cancel_requested: bool


class RemoteShellDB:
    """Async Supabase access for the remote-shell command queue."""

    def __init__(self, config: RemoteShellConfig, client: AsyncClient | None = None):
        self._config = config
        self._client = client
        self._httpx = None

    @property
    def config(self) -> RemoteShellConfig:
        return self._config

    async def connect(self) -> RemoteShellDB:
        if self._client is None:
            self._httpx = _build_httpx_pool()
            self._client = await create_async_client(
                self._config.supabase_url,
                self._config.supabase_key,
                AsyncClientOptions(httpx_client=self._httpx),
            )
        return self

    async def aclose(self) -> None:
        if self._httpx is not None:
            try:
                await self._httpx.aclose()
            finally:
                self._httpx = None

    @property
    def client(self) -> AsyncClient:
        if self._client is None:
            raise RuntimeError("RemoteShellDB.connect() must be called first")
        return self._client

    async def __aenter__(self) -> RemoteShellDB:
        return await self.connect()

    async def __aexit__(self, *exc: object) -> None:
        await self.aclose()

    # -- queue operations --------------------------------------------------

    async def claim_next(
        self, runner_id: str, lease_seconds: int
    ) -> ShellCommand | None:
        res = await self.client.rpc(
            "areal_shell_claim_next",
            {"p_runner_id": runner_id, "p_lease_seconds": lease_seconds},
        ).execute()
        row = getattr(res, "data", None)
        if not row:
            return None
        return ShellCommand.from_row(row)

    async def mark_running(
        self, command_id: str, runner_id: str, lease_seconds: int
    ) -> bool:
        res = await self.client.rpc(
            "areal_shell_mark_running",
            {
                "p_id": command_id,
                "p_runner_id": runner_id,
                "p_lease_seconds": lease_seconds,
            },
        ).execute()
        return bool(getattr(res, "data", None))

    async def heartbeat(
        self,
        command_id: str,
        runner_id: str,
        lease_seconds: int,
        *,
        stdout_tail: str | None = None,
        stderr_tail: str | None = None,
        log_bytes: int | None = None,
    ) -> Heartbeat | None:
        res = await self.client.rpc(
            "areal_shell_heartbeat",
            {
                "p_id": command_id,
                "p_runner_id": runner_id,
                "p_lease_seconds": lease_seconds,
                "p_stdout_tail": stdout_tail,
                "p_stderr_tail": stderr_tail,
                "p_log_bytes": log_bytes,
            },
        ).execute()
        row = getattr(res, "data", None)
        if not row:
            return None
        return Heartbeat(
            status=row.get("status", ""),
            cancel_requested=bool(row.get("cancel_requested")),
        )

    async def complete(
        self,
        command_id: str,
        runner_id: str,
        status: str,
        *,
        exit_code: int | None = None,
        stdout_tail: str | None = None,
        stderr_tail: str | None = None,
        log_bytes: int | None = None,
        error_message: str | None = None,
        metadata: dict[str, Any] | None = None,
    ) -> bool:
        err = error_message[:8000] if error_message else None
        rpc_body: dict[str, Any] = {
            "p_id": command_id,
            "p_runner_id": runner_id,
            "p_status": status,
            "p_exit_code": exit_code,
            "p_stdout_tail": stdout_tail,
            "p_stderr_tail": stderr_tail,
            "p_log_bytes": log_bytes,
            "p_error_message": err,
        }
        if metadata is not None:
            rpc_body["p_metadata"] = metadata
        res = await self.client.rpc(
            "areal_shell_complete",
            rpc_body,
        ).execute()
        return bool(getattr(res, "data", None))

    async def sweep_stale(self, limit: int = 100) -> int:
        res = await self.client.rpc(
            "areal_shell_sweep_stale", {"p_limit": limit}
        ).execute()
        return int(getattr(res, "data", None) or 0)

    async def request_cancel(
        self, command_id: str, *, user_id: str | None = None
    ) -> dict[str, Any] | None:
        res = await self.client.rpc(
            "areal_shell_request_cancel",
            {"p_id": command_id, "p_user_id": user_id},
        ).execute()
        return getattr(res, "data", None)

    async def cleanup(self, *, retention_seconds: int, limit: int) -> int:
        res = await self.client.rpc(
            "areal_shell_cleanup",
            {"p_retention_seconds": retention_seconds, "p_limit": limit},
        ).execute()
        return int(getattr(res, "data", None) or 0)


def _decode_tail(data: bytes) -> str:
    return data.decode("utf-8", errors="replace")


def _extract_log_metadata(command: str, cwd: str | None) -> dict[str, Any] | None:
    """Extract log file path from the command and parse AReaL training results.

    Parses the ``tee training_<run_id>.log`` pattern from the command, reads the
    log file, and attempts to extract structured training results using
    ``parse_areal_log`` (if available on this host). Returns metadata including
    log_file, log_path, and any parsed training results.
    """
    import re

    m = re.search(r"\|\s*tee\s+(\S+\.log)\s*$", command)
    if not m:
        return None
    log_name = m.group(1)
    log_path = os.path.join(cwd or "", log_name) if cwd else log_name
    meta: dict[str, Any] = {"log_file": log_name, "log_path": log_path}

    # Try to parse the log file for structured training results.
    # parse_areal_log lives in the AReaL repo; it may not be on sys.path here.
    try:
        result = _parse_areal_log_result(log_path)
        if result is not None:
            meta["train_id"] = result.get("train_id")
            meta["starting_time"] = result.get("starting_time")
            meta["completion"] = result.get("completion")
            meta["n_steps"] = result.get("n_steps")
            meta["n_errors"] = result.get("n_errors")
            meta["total_train_steps"] = result.get("total_train_steps")
            meta["completed_train_steps"] = result.get("completed_train_steps")
            meta["flat_stats"] = result.get("flat_stats")
            meta["save_events"] = result.get("save_events")
            meta["save_events_completed"] = result.get("save_events_completed")
            meta["save_events_count"] = result.get("save_events_count")
            meta["recover_paths"] = result.get("recover_paths")
            meta["recover_info_path"] = result.get("recover_info_path")
    except Exception:
        # Parsing is best-effort; never let it break finalization.
        logger.debug("failed to parse log file %s", log_path, exc_info=True)

    return meta


def _parse_areal_log_result(log_path: str) -> dict[str, Any] | None:
    """Parse an AReaL training log file and return a summary dict.

    Tries to import ``parse_areal_log`` from the AReaL repo. Returns ``None``
    when the module is unavailable or the file cannot be read.
    """
    if not os.path.isfile(log_path):
        return None

    # Ensure the AReaL repo root (where the log file lives) is on sys.path
    # so that ``parse_areal_log`` can be imported.  The runner process is
    # started from the rivercoaching project and does not include the AReaL
    # checkout in its default sys.path.
    import sys

    log_dir = os.path.dirname(os.path.abspath(log_path))
    if log_dir and log_dir not in sys.path:
        sys.path.insert(0, log_dir)

    # Attempt to import parse_areal_log from the AReaL repo.
    # It may be in the repo root or in a scripts/ subdirectory.
    parse_fn = None
    for candidate in (
        "parse_areal_log",
        "scripts.parse_areal_log",
    ):
        try:
            mod = __import__(candidate, fromlist=["parse_areal_log"])
            parse_fn = getattr(mod, "parse_areal_log", None)
            if parse_fn is not None:
                break
        except ImportError:
            continue

    if parse_fn is None:
        return None

    result = parse_fn(log_path)
    from dataclasses import asdict

    d = asdict(result)
    # Include flat_stats and metadata for convenience.
    d["flat_stats"] = result.flat_stats()
    d["n_steps"] = len(d.get("steps", []))
    d["n_errors"] = len(d.get("errors", []))
    d["total_train_steps"] = result.total_train_steps
    d["completed_train_steps"] = result.completed_train_steps
    meta = result.to_metadata()
    d["save_events_completed"] = meta.get("save_events_completed")
    d["save_events_count"] = meta.get("save_events_count")
    return d


class RemoteShellRunner:
    """Polls the command queue and executes claimed commands via tmux."""

    def __init__(
        self,
        db: RemoteShellDB,
        executor: ShellExecutor,
        config: RemoteShellConfig | None = None,
    ):
        self._db = db
        self._executor = executor
        self._config = config or db.config
        self._stop = asyncio.Event()
        self._active: set[asyncio.Task[None]] = set()
        self._last_sweep = 0.0
        self._last_cleanup = 0.0

    def stop(self) -> None:
        self._stop.set()

    # -- single command execution -----------------------------------------

    async def execute_command(self, cmd: ShellCommand) -> None:
        """Run one claimed command through its full lifecycle to a terminal row.

        Safe to call directly (tests do); ``run`` schedules it as a task.
        """
        runner_id = self._config.runner_id
        lease = self._config.lease_seconds

        if not cmd.command.strip():
            await self._db.complete(
                cmd.id,
                runner_id,
                STATUS_FAILED,
                exit_code=None,
                error_message="empty command",
            )
            return

        if not await self._db.mark_running(cmd.id, runner_id, lease):
            logger.info("shell command ownership lost before start id=%s", cmd.id)
            return

        session = self._config.session_name(cmd.tmux_id)
        marker = uuid.uuid4().hex
        cwd = cmd.cwd or self._config.default_cwd
        timeout = self._config.resolve_timeout(cmd.timeout_seconds)

        try:
            await self._executor.launch(
                LaunchSpec(session=session, command=cmd.command, cwd=cwd, marker=marker)
            )
        except ShellExecutorError as exc:
            logger.warning("shell launch failed id=%s: %s", cmd.id, exc)
            await self._db.complete(
                cmd.id,
                runner_id,
                STATUS_FAILED,
                error_message=f"executor error: {exc}",
            )
            return

        await self._monitor(cmd, session, timeout)

    async def _monitor(self, cmd: ShellCommand, session: str, timeout: int) -> None:
        runner_id = self._config.runner_id
        lease = self._config.lease_seconds
        max_log = self._config.max_log_bytes
        deadline = time.monotonic() + timeout

        while True:
            capture = await self._executor.poll(session, max_log_bytes=max_log)
            stdout = _decode_tail(capture.stdout_tail)
            stderr = _decode_tail(capture.stderr_tail)

            hb = await self._db.heartbeat(
                cmd.id,
                runner_id,
                lease,
                stdout_tail=stdout,
                stderr_tail=stderr,
                log_bytes=capture.log_bytes,
            )
            if hb is None:
                # Lease lost (row reclaimed or swept STALE); stop touching it.
                logger.info(
                    "shell command lease lost id=%s session=%s", cmd.id, session
                )
                await self._safe_terminate(session)
                return

            if hb.cancel_requested:
                await self._safe_terminate(session)
                await self._finalize(
                    cmd, STATUS_CANCELLED, capture, error_message="cancelled by request"
                )
                return

            if capture.exit_code is not None:
                status = STATUS_SUCCEEDED if capture.exit_code == 0 else STATUS_FAILED
                await self._finalize(cmd, status, capture)
                return

            if time.monotonic() >= deadline:
                await self._safe_terminate(session)
                await self._finalize(
                    cmd,
                    STATUS_TIMED_OUT,
                    capture,
                    error_message=f"timed out after {timeout}s",
                )
                return

            await asyncio.sleep(self._config.poll_interval_s)

    async def _finalize(
        self,
        cmd: ShellCommand,
        status: str,
        capture: CaptureResult,
        *,
        error_message: str | None = None,
    ) -> None:
        metadata = _extract_log_metadata(cmd.command, cmd.cwd or self._config.default_cwd)
        ok = await self._db.complete(
            cmd.id,
            self._config.runner_id,
            status,
            exit_code=capture.exit_code,
            stdout_tail=_decode_tail(capture.stdout_tail),
            stderr_tail=_decode_tail(capture.stderr_tail),
            log_bytes=capture.log_bytes,
            error_message=error_message,
            metadata=metadata,
        )
        if not ok:
            logger.info(
                "shell command finalize skipped (ownership lost) id=%s status=%s",
                cmd.id,
                status,
            )
        else:
            logger.info(
                "shell command finalized id=%s status=%s exit=%s metadata=%s",
                cmd.id,
                status,
                capture.exit_code,
                metadata,
            )

    async def _safe_terminate(self, session: str) -> None:
        try:
            await self._executor.terminate(session)
        except Exception as exc:  # noqa: BLE001 -- termination is best-effort
            logger.warning("shell terminate failed session=%s: %s", session, exc)

    # -- poll loop ---------------------------------------------------------

    def _track(self, task: asyncio.Task[None]) -> None:
        self._active.add(task)
        task.add_done_callback(self._active.discard)

    async def _maybe_sweep(self) -> None:
        now = time.monotonic()
        if now - self._last_sweep < self._config.sweep_interval_s:
            return
        self._last_sweep = now
        try:
            swept = await self._db.sweep_stale()
            if swept:
                logger.info("shell swept stale commands count=%d", swept)
        except Exception:  # noqa: BLE001 -- sweep must never crash the loop
            logger.exception("shell stale sweep failed")

    async def _maybe_cleanup(self) -> None:
        if self._config.cleanup_interval_s <= 0:
            return
        now = time.monotonic()
        if now - self._last_cleanup < self._config.cleanup_interval_s:
            return
        self._last_cleanup = now
        try:
            deleted = await self._db.cleanup(
                retention_seconds=self._config.retention_seconds, limit=1000
            )
            if deleted:
                logger.info("shell cleanup deleted=%d", deleted)
        except Exception:  # noqa: BLE001 -- cleanup must never crash the loop
            logger.exception("shell cleanup failed")

    async def poll_once(self) -> bool:
        """Claim and schedule at most one command. Returns True if one started."""
        if len(self._active) >= self._config.max_concurrency:
            return False
        cmd = await self._db.claim_next(
            self._config.runner_id, self._config.lease_seconds
        )
        if cmd is None:
            return False
        logger.info(
            "shell claimed command id=%s tmux=%s user=%s",
            cmd.id,
            cmd.tmux_id,
            cmd.user_id,
        )
        self._track(asyncio.create_task(self._guarded_execute(cmd)))
        return True

    async def _guarded_execute(self, cmd: ShellCommand) -> None:
        try:
            await self.execute_command(cmd)
        except Exception:  # noqa: BLE001 -- never let one command kill the loop
            logger.exception("shell command crashed id=%s", cmd.id)
            try:
                await self._db.complete(
                    cmd.id,
                    self._config.runner_id,
                    STATUS_FAILED,
                    error_message="runner internal error",
                )
            except Exception:  # noqa: BLE001
                logger.exception("shell failed to mark crashed command id=%s", cmd.id)

    async def _sleep(self, delay: float) -> None:
        """Sleep for ``delay`` seconds, waking early once :meth:`stop` is called."""
        try:
            await asyncio.wait_for(self._stop.wait(), timeout=delay)
        except TimeoutError:
            pass

    async def run(self) -> None:
        """Run the poll loop until :meth:`stop` is called."""
        if not self._config.enabled:
            logger.warning(
                "remote shell runner started but %s is disabled; refusing to claim "
                "commands. Set AREAL_REMOTE_SHELL_ENABLED=true on a trusted host.",
                "AREAL_REMOTE_SHELL_ENABLED",
            )
            return

        logger.info(
            "remote shell runner ready runner_id=%s poll=%.2fs lease=%ds max_concurrency=%d",
            self._config.runner_id,
            self._config.poll_interval_s,
            self._config.lease_seconds,
            self._config.max_concurrency,
        )
        backoff = 0.0
        try:
            while not self._stop.is_set():
                await self._maybe_sweep()
                await self._maybe_cleanup()
                try:
                    started = await self.poll_once()
                except Exception:  # noqa: BLE001 -- a wedged DB must not end the run
                    backoff = min(
                        max(2 * backoff, self._config.poll_interval_s),
                        _CLAIM_BACKOFF_MAX_S,
                    )
                    logger.exception("shell claim failed; retrying in %.1fs", backoff)
                    await self._sleep(backoff)
                    continue
                backoff = 0.0
                if not started:
                    await self._sleep(self._config.poll_interval_s)
        finally:
            for task in list(self._active):
                task.cancel()
            await asyncio.gather(*self._active, return_exceptions=True)
            logger.info(
                "remote shell runner stopped runner_id=%s", self._config.runner_id
            )
