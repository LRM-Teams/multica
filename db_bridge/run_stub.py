#!/usr/bin/env python3
"""Entrypoint: run the DB-bridge stub server for one side.

Usage:
    python -m db_bridge.run_stub --side leagent
    python -m db_bridge.run_stub --side areal
"""

from __future__ import annotations

from .entrypoints import run_stub

if __name__ == "__main__":
    run_stub()
