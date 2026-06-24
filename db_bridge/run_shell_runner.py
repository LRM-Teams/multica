#!/usr/bin/env python3
"""Entrypoint: run the AReaL DB-backed tmux remote shell runner.

Runs on the AReaL host only. Claims rows from ``areal_remote_commands`` and
executes them in command-scoped tmux sessions. Guarded by the
``AREAL_REMOTE_SHELL_ENABLED`` feature flag.

Usage:
    python -m db_bridge.run_shell_runner
"""

from __future__ import annotations

from .entrypoints import run_shell_runner

if __name__ == "__main__":
    run_shell_runner()
