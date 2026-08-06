"""Command executor abstraction for the AReaL remote shell runner.

The runner drives commands through a small :class:`ShellExecutor` interface so
tests can swap the real ``tmux``-backed executor for an in-memory fake. The real
executor maps each task to a stable tmux session and runs the (arbitrary) shell
text inside it, capturing bounded stdout/stderr tails and the process exit code.

``tmux`` is used for lifecycle management only -- named sessions, output capture,
and decisive termination. It does NOT sandbox the command: by design the runner
executes arbitrary host shell code for authorized callers.
"""

from __future__ import annotations

import asyncio
import os
import shlex
from abc import ABC, abstractmethod
from dataclasses import dataclass


class ShellExecutorError(RuntimeError):
    """Raised when the executor backend (e.g. tmux) is unavailable or fails."""


@dataclass(slots=True)
class LaunchSpec:
    """Everything the executor needs to start one command."""

    session: str
    """tmux session name (stable per task)."""

    command: str
    """Raw, arbitrary shell text to run."""

    cwd: str | None
    """Working directory to change into before running, if any."""

    marker: str
    """Unique completion marker used to detect the captured exit code."""


@dataclass(slots=True)
class CaptureResult:
    """A snapshot of a running (or finished) command's output and state."""

    stdout_tail: bytes
    stderr_tail: bytes
    log_bytes: int
    exit_code: int | None
    """``None`` while the command is still running, else the captured code."""


class ShellExecutor(ABC):
    """Lifecycle interface for executing a command and observing its output."""

    @abstractmethod
    async def launch(self, spec: LaunchSpec) -> None:
        """Start ``spec.command`` running in ``spec.session``.

        Must raise :class:`ShellExecutorError` if the backend cannot start the
        command (so the runner can mark the row ``FAILED``).
        """

    @abstractmethod
    async def poll(self, session: str, *, max_log_bytes: int) -> CaptureResult:
        """Return the current bounded output tail and exit state for ``session``."""

    @abstractmethod
    async def terminate(self, session: str) -> None:
        """Kill the active command/session (cancellation or timeout)."""

    async def aclose(self) -> None:  # pragma: no cover - default no-op
        """Release any resources held by the executor."""


def _tail(data: bytes, max_bytes: int) -> bytes:
    return data[-max_bytes:] if max_bytes > 0 and len(data) > max_bytes else data


def build_command_script(
    *,
    command: str,
    cwd: str | None,
    stdout_path: str,
    stderr_path: str,
    exit_path: str,
    marker: str,
) -> str:
    """Build the shell wrapper for one arbitrary command.

    The user command runs in a subshell. That keeps commands such as ``exit 7``
    from terminating the wrapper before the completion marker is written.
    """
    cd_line = ""
    if cwd:
        cd_line = f"cd {shlex.quote(cwd)} || exit 97\n"
    return (
        "#!/usr/bin/env bash\n"
        f"{cd_line}"
        f"(\n{command}\n) > {shlex.quote(stdout_path)} "
        f"2> {shlex.quote(stderr_path)}\n"
        "__areal_code=$?\n"
        f"printf '%s %d\\n' {shlex.quote(marker)} "
        f'"$__areal_code" > {shlex.quote(exit_path)}\n'
    )


class TmuxShellExecutor(ShellExecutor):
    """Real executor that runs commands in command-scoped tmux sessions.

    Each command writes combined output to per-command-session stdout/stderr capture
    files and, on completion, an unambiguous ``<marker> <exit_code>`` line to a
    result file. The runner polls those files. Sessions are killed on
    cancellation/timeout and recreated on the next launch.

    This path is not exercised by the unit suite (it requires a real ``tmux``
    binary); it is verified by a local/manual integration check on an AReaL host.
    """

    def __init__(self, *, work_dir: str, tmux_bin: str = "tmux") -> None:
        self._work_dir = work_dir
        self._tmux = tmux_bin
        os.makedirs(self._work_dir, exist_ok=True)

    # -- file layout -------------------------------------------------------

    def _paths(self, session: str) -> tuple[str, str, str, str]:
        base = os.path.join(self._work_dir, session)
        return (
            f"{base}.out",  # stdout capture
            f"{base}.err",  # stderr capture
            f"{base}.exit",  # "<marker> <code>" on completion
            f"{base}.cmd.sh",  # wrapped script
        )

    # -- tmux helpers ------------------------------------------------------

    async def _tmux_run(self, *args: str) -> tuple[int, bytes, bytes]:
        try:
            proc = await asyncio.create_subprocess_exec(
                self._tmux,
                *args,
                stdout=asyncio.subprocess.PIPE,
                stderr=asyncio.subprocess.PIPE,
            )
        except FileNotFoundError as exc:
            raise ShellExecutorError(
                f"tmux binary {self._tmux!r} not found on PATH"
            ) from exc
        out, err = await proc.communicate()
        return proc.returncode or 0, out, err

    async def _has_session(self, session: str) -> bool:
        code, _, _ = await self._tmux_run("has-session", "-t", session)
        return code == 0

    async def _ensure_session(self, session: str) -> None:
        if await self._has_session(session):
            return
        code, _, err = await self._tmux_run(
            "new-session", "-d", "-s", session, "-x", "200", "-y", "50"
        )
        if code != 0:
            raise ShellExecutorError(
                f"failed to create tmux session {session!r}: {err.decode(errors='replace')}"
            )

    # -- interface ---------------------------------------------------------

    async def launch(self, spec: LaunchSpec) -> None:
        out_path, err_path, exit_path, cmd_path = self._paths(spec.session)
        # Fresh capture files for this command.
        for path in (out_path, err_path, exit_path):
            try:
                if os.path.exists(path):
                    os.remove(path)
            except OSError:
                pass

        # The command text is arbitrary by design; it runs verbatim inside a
        # subshell. Output is redirected per-stream and the exit code captured
        # via the marker.
        script = build_command_script(
            command=spec.command,
            cwd=spec.cwd,
            stdout_path=out_path,
            stderr_path=err_path,
            exit_path=exit_path,
            marker=spec.marker,
        )
        try:
            with open(cmd_path, "w", encoding="utf-8") as fh:
                fh.write(script)
        except OSError as exc:
            raise ShellExecutorError(f"failed to write command script: {exc}") from exc

        await self._ensure_session(spec.session)
        code, _, err = await self._tmux_run(
            "send-keys",
            "-t",
            spec.session,
            f"bash {shlex.quote(cmd_path)}",
            "Enter",
        )
        if code != 0:
            raise ShellExecutorError(
                f"failed to send command to tmux session {spec.session!r}: "
                f"{err.decode(errors='replace')}"
            )

    async def poll(self, session: str, *, max_log_bytes: int) -> CaptureResult:
        out_path, err_path, exit_path, _ = self._paths(session)
        stdout = _read_tail(out_path, max_log_bytes)
        stderr = _read_tail(err_path, max_log_bytes)
        exit_code = _read_exit(exit_path)
        return CaptureResult(
            stdout_tail=stdout,
            stderr_tail=stderr,
            log_bytes=_file_size(out_path) + _file_size(err_path),
            exit_code=exit_code,
        )

    async def terminate(self, session: str) -> None:
        if await self._has_session(session):
            await self._tmux_run("kill-session", "-t", session)


def _file_size(path: str) -> int:
    try:
        return os.path.getsize(path)
    except OSError:
        return 0


def _read_tail(path: str, max_bytes: int) -> bytes:
    try:
        with open(path, "rb") as fh:
            if max_bytes > 0:
                size = os.fstat(fh.fileno()).st_size
                if size > max_bytes:
                    fh.seek(size - max_bytes)
            return _tail(fh.read(), max_bytes)
    except OSError:
        return b""


def _read_exit(path: str) -> int | None:
    try:
        with open(path, "rb") as fh:
            content = fh.read().strip()
    except OSError:
        return None
    if not content:
        return None
    # Format: "<marker> <code>"; the code is the final whitespace-delimited token.
    try:
        return int(content.split()[-1])
    except (ValueError, IndexError):
        return None
