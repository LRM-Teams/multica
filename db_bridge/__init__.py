"""Supabase-as-springboard RPC bridge between le-agent and AReaL.

Two hosts that cannot reach each other but share a Supabase database relay all
cross-service HTTP calls through per-endpoint tables using a request/response
queue, polled aggressively. Each host runs a stub server (mirrors the remote
API) and an executor worker (forwards claimed requests to the real local
service over loopback). See ``README.md`` for the architecture overview.
"""

from __future__ import annotations

__all__ = ["__version__"]

__version__ = "0.1.0"
