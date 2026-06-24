#!/usr/bin/env python3
"""Entrypoint: run the DB-bridge executor worker pool for one side.

Usage:
    python -m db_bridge.run_executor --side leagent
    python -m db_bridge.run_executor --side areal
"""

from __future__ import annotations

from .entrypoints import run_executor

if __name__ == "__main__":
    run_executor()
