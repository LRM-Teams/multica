#!/usr/bin/env python3
"""Entrypoint: run the multica-side public bridge server.

Runs on the multica host (public internet). Serves an OpenAI-compatible LLM
endpoint and a remote-shell enqueue API, both guarded by static API keys, and
routes all work through the shared database to the AReaL host.

Usage:
    python -m db_bridge.run_multica
"""

from __future__ import annotations

from .entrypoints import run_multica

if __name__ == "__main__":
    run_multica()
