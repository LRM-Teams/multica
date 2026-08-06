"""Tests for MulticaConfig (Task 1)."""

from __future__ import annotations

import pytest
from db_bridge.config import MulticaConfig

USER_ID = "00000000-0000-0000-0000-0000000000ff"


def _env(**overrides: str) -> dict[str, str]:
    env = {
        "SUPABASE_URL": "https://example.supabase.co",
        "SUPABASE_SERVICE_ROLE_KEY": "service-key",
        "MULTICA_BRIDGE_USER_ID": USER_ID,
    }
    env.update(overrides)
    return env


def test_from_env_defaults():
    cfg = MulticaConfig.from_env(_env())
    assert cfg.supabase_url == "https://example.supabase.co"
    assert cfg.bridge_user_id == USER_ID
    assert cfg.bind_host == "0.0.0.0"
    assert cfg.port == 9200
    assert cfg.chat_timeout_s == 180.0
    assert cfg.shell_default_timeout_s == 300
    assert cfg.shell_max_timeout_s == 3600
    assert cfg.shell_default_cwd is None
    assert cfg.upstream_api_key is None
    # Fail-closed: no keys configured means no caller is authorized.
    assert cfg.llm_api_keys == frozenset()
    assert cfg.shell_api_keys == frozenset()


def test_streaming_defaults():
    cfg = MulticaConfig.from_env(_env())
    assert cfg.stream_first_chunk_timeout_s == pytest.approx(60.0)
    assert cfg.stream_inter_chunk_timeout_s == pytest.approx(120.0)
    assert cfg.stream_poll_interval_s == pytest.approx(1.0)


def test_streaming_overrides_and_validation():
    cfg = MulticaConfig.from_env(
        _env(
            MULTICA_STREAM_FIRST_CHUNK_TIMEOUT="10",
            MULTICA_STREAM_INTER_CHUNK_TIMEOUT="20",
            MULTICA_STREAM_POLL_INTERVAL="0.01",
        )
    )
    assert cfg.stream_first_chunk_timeout_s == pytest.approx(10.0)
    assert cfg.stream_inter_chunk_timeout_s == pytest.approx(20.0)
    assert cfg.stream_poll_interval_s == pytest.approx(0.01)
    with pytest.raises(ValueError):
        MulticaConfig.from_env(_env(MULTICA_STREAM_POLL_INTERVAL="0"))


def test_missing_supabase_raises():
    env = _env()
    del env["SUPABASE_URL"]
    with pytest.raises(RuntimeError, match="SUPABASE_URL"):
        MulticaConfig.from_env(env)


def test_missing_user_id_raises():
    env = _env()
    del env["MULTICA_BRIDGE_USER_ID"]
    with pytest.raises(RuntimeError, match="MULTICA_BRIDGE_USER_ID"):
        MulticaConfig.from_env(env)


def test_invalid_user_id_raises():
    with pytest.raises(ValueError, match="valid UUID"):
        MulticaConfig.from_env(_env(MULTICA_BRIDGE_USER_ID="not-a-uuid"))


def test_api_keys_split_and_trim():
    cfg = MulticaConfig.from_env(
        _env(
            MULTICA_LLM_API_KEYS=" k1 , k2,, k3 ",
            MULTICA_SHELL_API_KEYS="shell-1",
        )
    )
    assert cfg.llm_api_keys == {"k1", "k2", "k3"}
    assert cfg.shell_api_keys == {"shell-1"}


def test_port_and_timeout_overrides():
    cfg = MulticaConfig.from_env(
        _env(
            MULTICA_PORT="9300",
            MULTICA_CHAT_TIMEOUT="90",
            MULTICA_SHELL_DEFAULT_TIMEOUT="120",
            MULTICA_SHELL_MAX_TIMEOUT="600",
            MULTICA_SHELL_DEFAULT_CWD="/root/AReaL",
            MULTICA_UPSTREAM_API_KEY="upstream-secret",
        )
    )
    assert cfg.port == 9300
    assert cfg.chat_timeout_s == 90.0
    assert cfg.shell_default_timeout_s == 120
    assert cfg.shell_max_timeout_s == 600
    assert cfg.shell_default_cwd == "/root/AReaL"
    assert cfg.upstream_api_key == "upstream-secret"


def test_default_timeout_exceeding_max_raises():
    with pytest.raises(ValueError, match="must not exceed"):
        MulticaConfig.from_env(
            _env(
                MULTICA_SHELL_DEFAULT_TIMEOUT="700",
                MULTICA_SHELL_MAX_TIMEOUT="600",
            )
        )


def test_resolve_shell_timeout_clamps():
    cfg = MulticaConfig.from_env(
        _env(MULTICA_SHELL_DEFAULT_TIMEOUT="120", MULTICA_SHELL_MAX_TIMEOUT="600")
    )
    assert cfg.resolve_shell_timeout(None) == 120
    assert cfg.resolve_shell_timeout(0) == 120
    assert cfg.resolve_shell_timeout(-5) == 120
    assert cfg.resolve_shell_timeout(300) == 300
    assert cfg.resolve_shell_timeout(5000) == 600
