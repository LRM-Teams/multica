"""Static structural check: env-dispatch channel tables exist in schema.sql."""

from __future__ import annotations

from pathlib import Path

_SCHEMA = (Path(__file__).resolve().parents[1] / "schema.sql").read_text()


def test_env_dispatch_tables_in_array():
    assert "'rpc_env_dispatch'" in _SCHEMA
    assert "'rpc_env_dispatch_delete'" in _SCHEMA
