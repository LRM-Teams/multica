"""Process entrypoints for the DB bridge.

Each host runs two standalone processes per side:

  le-agent host:
    * stub     (--side leagent)  serves the AReaL gateway endpoints locally
    * executor (--side leagent)  forwards le-agent API calls to the real API

  AReaL host:
    * stub     (--side areal)    serves the le-agent API endpoints locally
    * executor (--side areal)    forwards gateway calls to the real gateway

The stub's stale-claim recovery is built into ``bridge_claim_next`` (it reclaims
rows stuck in ``claimed`` past ``stale_seconds``), so no separate reaper process
is required: any executor worker re-claims an abandoned row after the window.
"""

from __future__ import annotations

import argparse
import asyncio
import logging
import signal
from typing import get_args

import uvicorn

from .channels import Side, executor_channels, stub_channels
from .config import BridgeConfig
from .db import BridgeDB
from .executor import Executor
from .stub_server import create_stub_app

logger = logging.getLogger("db_bridge.entrypoint")

_VALID_SIDES = get_args(Side)
_NOISY_LOGGERS = (
    "httpcore",
    "httpx",
    "postgrest",
    "supabase",
    "uvicorn",
    "uvicorn.access",
    "uvicorn.error",
)


def _configure_logging(level: str) -> None:
    """Keep bridge event logs visible without logging every poll/access event."""
    logging.basicConfig(
        level=logging.WARNING,
        format="%(asctime)s %(levelname)s %(name)s %(message)s",
    )
    logging.getLogger("db_bridge").setLevel(level.upper())
    for name in _NOISY_LOGGERS:
        logging.getLogger(name).setLevel(logging.WARNING)


def _parse_side(argv: list[str] | None, prog: str) -> Side:
    parser = argparse.ArgumentParser(prog=prog)
    parser.add_argument(
        "--side",
        required=True,
        choices=_VALID_SIDES,
        help="Which host this process runs on.",
    )
    parser.add_argument(
        "--log-level", default="info", help="Logging level (default: info)."
    )
    args = parser.parse_args(argv)
    _configure_logging(args.log_level)
    return args.side


# ---------------------------------------------------------------------------
# Stub server
# ---------------------------------------------------------------------------


def run_stub(argv: list[str] | None = None) -> None:
    side = _parse_side(argv, prog="db_bridge.run_stub")
    try:
        config = BridgeConfig.from_env()
        # DB is connected by the app's lifespan hook under uvicorn.
        db = BridgeDB(config)
        app = create_stub_app(db, side, config)
        host = config.stub_host
        port = config.stub_port(side)
        served = [c.path for c in stub_channels(side)]
        logger.debug(
            "starting stub side=%s bind=%s:%d channels=%s", side, host, port, served
        )
        # Bind to 127.0.0.1 by default: the local app is the only intended caller.
        uvicorn.run(app, host=host, port=port, log_level="warning", access_log=False)
    except Exception:
        logger.exception("stub crashed during startup or runtime side=%s", side)
        raise


# ---------------------------------------------------------------------------
# Executor worker pool
# ---------------------------------------------------------------------------


async def run_executor_async(
    side: Side, config: BridgeConfig | None = None, *, install_signals: bool = True
) -> None:
    config = config or BridgeConfig.from_env()
    db = await BridgeDB(config).connect()
    executor = Executor(db, side, config=config)
    await executor.connect()

    if install_signals:
        loop = asyncio.get_running_loop()
        for sig in (signal.SIGINT, signal.SIGTERM):
            try:
                loop.add_signal_handler(sig, executor.stop)
            except (NotImplementedError, RuntimeError):
                # Signal handlers may be unavailable (e.g. non-main thread).
                pass

    channels = [c.name for c in executor_channels(side)]
    logger.debug("starting executor side=%s channels=%s", side, channels)
    try:
        await executor.run()
    finally:
        await executor.aclose()
        await db.aclose()
        logger.debug("executor shut down side=%s", side)


def run_executor(argv: list[str] | None = None) -> None:
    side = _parse_side(argv, prog="db_bridge.run_executor")
    try:
        asyncio.run(run_executor_async(side))
    except Exception:
        logger.exception("executor crashed during startup or runtime side=%s", side)
        raise


# ---------------------------------------------------------------------------
# Remote shell runner (AReaL host only)
# ---------------------------------------------------------------------------


def _parse_shell_args(argv: list[str] | None, prog: str) -> None:
    parser = argparse.ArgumentParser(prog=prog)
    parser.add_argument(
        "--log-level", default="info", help="Logging level (default: info)."
    )
    args = parser.parse_args(argv)
    _configure_logging(args.log_level)


async def run_shell_runner_async(
    config=None, *, executor=None, install_signals: bool = True
) -> None:
    # Local imports keep the bridge stub/executor entrypoints free of the
    # remote-shell dependency graph until the runner is actually started.
    from .config import RemoteShellConfig
    from .remote_shell import RemoteShellDB, RemoteShellRunner
    from .shell_executor import TmuxShellExecutor

    config = config or RemoteShellConfig.from_env()
    db = await RemoteShellDB(config).connect()
    executor = executor or TmuxShellExecutor(
        work_dir=config.work_dir, tmux_bin=config.tmux_bin
    )
    runner = RemoteShellRunner(db, executor, config)

    if install_signals:
        loop = asyncio.get_running_loop()
        for sig in (signal.SIGINT, signal.SIGTERM):
            try:
                loop.add_signal_handler(sig, runner.stop)
            except (NotImplementedError, RuntimeError):
                pass

    try:
        await runner.run()
    finally:
        await executor.aclose()
        await db.aclose()
        logger.debug("remote shell runner shut down")


def run_shell_runner(argv: list[str] | None = None) -> None:
    _parse_shell_args(argv, prog="db_bridge.run_shell_runner")
    try:
        asyncio.run(run_shell_runner_async())
    except Exception:
        logger.exception("remote shell runner crashed during startup or runtime")
        raise
