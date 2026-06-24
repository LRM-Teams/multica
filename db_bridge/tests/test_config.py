"""Tests for bridge configuration and the channel registry."""

from __future__ import annotations

import pytest

from db_bridge import channels
from db_bridge.config import BridgeConfig

_MINIMAL = {
    "SUPABASE_URL": "https://example.supabase.co",
    "SUPABASE_SERVICE_ROLE_KEY": "service-key",
}


def test_minimal_from_env_applies_defaults():
    cfg = BridgeConfig.from_env(_MINIMAL)
    assert cfg.supabase_url == "https://example.supabase.co"
    assert cfg.supabase_key == "service-key"
    assert cfg.poll_interval_s == pytest.approx(0.075)
    assert cfg.stale_seconds == 300
    assert cfg.stub_host == "127.0.0.1"
    assert cfg.gateway_stub_port == 9100
    assert cfg.leagent_stub_port == 9101
    assert cfg.stats_interval_s == 0.0
    assert cfg.cleanup_interval_s == 300.0
    assert cfg.row_retention_seconds == 86400
    assert cfg.cleanup_batch_limit == 1000
    assert cfg.bridge_user_id is None


def test_anon_key_is_not_accepted_for_bridge():
    with pytest.raises(RuntimeError):
        BridgeConfig.from_env(
            {"SUPABASE_URL": "https://x.co", "SUPABASE_ANON_KEY": "anon"}
        )


def test_service_key_preferred_over_anon():
    cfg = BridgeConfig.from_env(
        {
            "SUPABASE_URL": "https://x.co",
            "SUPABASE_SERVICE_ROLE_KEY": "svc",
            "SUPABASE_ANON_KEY": "anon",
        }
    )
    assert cfg.supabase_key == "svc"


def test_missing_url_raises():
    with pytest.raises(RuntimeError):
        BridgeConfig.from_env({"SUPABASE_SERVICE_ROLE_KEY": "k"})


def test_missing_key_raises():
    with pytest.raises(RuntimeError):
        BridgeConfig.from_env({"SUPABASE_URL": "https://x.co"})


def test_blank_values_treated_as_missing():
    with pytest.raises(RuntimeError):
        BridgeConfig.from_env({"SUPABASE_URL": "  ", "SUPABASE_SERVICE_ROLE_KEY": "k"})


def test_upstream_urls_strip_trailing_slash():
    cfg = BridgeConfig.from_env(
        {
            **_MINIMAL,
            "BRIDGE_GATEWAY_UPSTREAM_URL": "http://127.0.0.1:8080/",
            "BRIDGE_LEAGENT_UPSTREAM_URL": "http://127.0.0.1:8000/",
        }
    )
    assert cfg.gateway_upstream_url == "http://127.0.0.1:8080"
    assert cfg.leagent_upstream_url == "http://127.0.0.1:8000"


def test_numeric_env_validation():
    with pytest.raises(ValueError):
        BridgeConfig.from_env({**_MINIMAL, "BRIDGE_POLL_INTERVAL": "fast"})
    with pytest.raises(ValueError):
        BridgeConfig.from_env({**_MINIMAL, "BRIDGE_STALE_SECONDS": "soon"})
    with pytest.raises(ValueError):
        BridgeConfig.from_env({**_MINIMAL, "BRIDGE_POLL_INTERVAL": "0"})
    with pytest.raises(ValueError):
        BridgeConfig.from_env({**_MINIMAL, "BRIDGE_CONCURRENCY_RL_SET_REWARD": "0"})
    with pytest.raises(ValueError):
        BridgeConfig.from_env({**_MINIMAL, "BRIDGE_TIMEOUT_RL_SET_REWARD": "-1"})
    with pytest.raises(ValueError):
        BridgeConfig.from_env({**_MINIMAL, "BRIDGE_STATS_INTERVAL": "-1"})
    with pytest.raises(ValueError):
        BridgeConfig.from_env({**_MINIMAL, "BRIDGE_CLEANUP_INTERVAL": "-1"})
    with pytest.raises(ValueError):
        BridgeConfig.from_env({**_MINIMAL, "BRIDGE_ROW_RETENTION_SECONDS": "0"})
    with pytest.raises(ValueError):
        BridgeConfig.from_env({**_MINIMAL, "BRIDGE_CLEANUP_BATCH_LIMIT": "0"})
    with pytest.raises(ValueError):
        BridgeConfig.from_env({**_MINIMAL, "BRIDGE_USER_ID": "not-a-uuid"})


def test_per_channel_overrides():
    cfg = BridgeConfig.from_env(
        {
            **_MINIMAL,
            "BRIDGE_TIMEOUT_CHAT_COMPLETIONS": "240",
            "BRIDGE_CONCURRENCY_CHAT_COMPLETIONS": "64",
        }
    )
    chat = channels.CHANNELS_BY_NAME["chat_completions"]
    set_reward = channels.CHANNELS_BY_NAME["rl_set_reward"]
    assert cfg.timeout_for(chat) == pytest.approx(240.0)
    assert cfg.concurrency_for(chat) == 64
    # Untouched channels keep their defaults.
    assert cfg.timeout_for(set_reward) == pytest.approx(set_reward.default_timeout_s)


def test_cleanup_config_overrides():
    cfg = BridgeConfig.from_env(
        {
            **_MINIMAL,
            "BRIDGE_CLEANUP_INTERVAL": "12.5",
            "BRIDGE_ROW_RETENTION_SECONDS": "3600",
            "BRIDGE_CLEANUP_BATCH_LIMIT": "25",
        }
    )
    assert cfg.cleanup_interval_s == pytest.approx(12.5)
    assert cfg.row_retention_seconds == 3600
    assert cfg.cleanup_batch_limit == 25


def test_bridge_user_id_config_override():
    cfg = BridgeConfig.from_env(
        {
            **_MINIMAL,
            "BRIDGE_USER_ID": "00000000-0000-0000-0000-00000000000a",
        }
    )
    assert cfg.bridge_user_id == "00000000-0000-0000-0000-00000000000a"


def test_bool_flags():
    cfg = BridgeConfig.from_env(
        {**_MINIMAL, "BRIDGE_REDACT_TOKENS_AFTER_COMPLETE": "true"}
    )
    assert cfg.redact_tokens_after_complete is True


def test_stub_port_and_upstream_dispatch():
    cfg = BridgeConfig.from_env(_MINIMAL)
    assert cfg.stub_port("leagent") == cfg.gateway_stub_port
    assert cfg.stub_port("areal") == cfg.leagent_stub_port
    assert cfg.upstream_for_group("gateway") == cfg.gateway_upstream_url
    assert cfg.upstream_for_group("leagent_api") == cfg.leagent_upstream_url


# -- channel registry invariants -------------------------------------------


def test_table_names_unique_and_prefixed():
    tables = [c.table for c in channels.CHANNELS]
    assert len(tables) == len(set(tables))
    assert all(t.startswith("rpc_") for t in tables)


def test_stub_and_executor_sides_are_opposite():
    for c in channels.CHANNELS:
        assert c.stub_side != c.executor_side
        if c.group == "gateway":
            assert c.stub_side == "leagent" and c.executor_side == "areal"
        else:
            assert c.stub_side == "areal" and c.executor_side == "leagent"


def test_side_channel_partition_is_complete():
    # Every channel is served as a stub on exactly one side and executed on the other.
    leagent_stub = set(channels.stub_channels("leagent"))
    areal_stub = set(channels.stub_channels("areal"))
    assert leagent_stub | areal_stub == set(channels.CHANNELS)
    assert not (leagent_stub & areal_stub)
    assert set(channels.executor_channels("leagent")) == areal_stub
    assert set(channels.executor_channels("areal")) == leagent_stub


# -- remote shell runner config --------------------------------------------

from db_bridge.config import RemoteShellConfig  # noqa: E402

_SHELL_MINIMAL = {
    "SUPABASE_URL": "https://example.supabase.co",
    "SUPABASE_SERVICE_ROLE_KEY": "service-key",
}


def test_shell_config_defaults():
    cfg = RemoteShellConfig.from_env(_SHELL_MINIMAL)
    assert cfg.enabled is False
    assert cfg.runner_id.startswith("shell-runner-")
    assert cfg.poll_interval_s == pytest.approx(1.0)
    assert cfg.lease_seconds == 60
    assert cfg.sweep_interval_s == pytest.approx(30.0)
    assert cfg.default_timeout_s == 300
    assert cfg.max_timeout_s == 3600
    assert cfg.max_log_bytes == 64 * 1024
    assert cfg.max_concurrency == 4
    assert cfg.session_prefix == "areal_"
    assert cfg.default_cwd is None


def test_shell_config_requires_service_credentials():
    with pytest.raises(RuntimeError):
        RemoteShellConfig.from_env({"SUPABASE_URL": "https://x.co"})


def test_shell_config_enabled_flag_and_runner_id():
    cfg = RemoteShellConfig.from_env(
        {
            **_SHELL_MINIMAL,
            "AREAL_REMOTE_SHELL_ENABLED": "true",
            "AREAL_REMOTE_SHELL_RUNNER_ID": "areal-box-1",
        }
    )
    assert cfg.enabled is True
    assert cfg.runner_id == "areal-box-1"


def test_shell_config_lease_must_exceed_poll():
    with pytest.raises(ValueError):
        RemoteShellConfig.from_env(
            {
                **_SHELL_MINIMAL,
                "AREAL_REMOTE_SHELL_POLL_INTERVAL": "30",
                "AREAL_REMOTE_SHELL_LEASE_SECONDS": "10",
            }
        )


def test_shell_config_default_timeout_must_not_exceed_max():
    with pytest.raises(ValueError):
        RemoteShellConfig.from_env(
            {
                **_SHELL_MINIMAL,
                "AREAL_REMOTE_SHELL_DEFAULT_TIMEOUT": "5000",
                "AREAL_REMOTE_SHELL_MAX_TIMEOUT": "3600",
            }
        )


def test_shell_config_rejects_nonpositive_values():
    with pytest.raises(ValueError):
        RemoteShellConfig.from_env(
            {**_SHELL_MINIMAL, "AREAL_REMOTE_SHELL_MAX_CONCURRENCY": "0"}
        )
    with pytest.raises(ValueError):
        RemoteShellConfig.from_env(
            {**_SHELL_MINIMAL, "AREAL_REMOTE_SHELL_POLL_INTERVAL": "0"}
        )


def test_shell_resolve_timeout_clamps():
    cfg = RemoteShellConfig.from_env(
        {
            **_SHELL_MINIMAL,
            "AREAL_REMOTE_SHELL_DEFAULT_TIMEOUT": "120",
            "AREAL_REMOTE_SHELL_MAX_TIMEOUT": "600",
        }
    )
    assert cfg.resolve_timeout(None) == 120
    assert cfg.resolve_timeout(0) == 120
    assert cfg.resolve_timeout(-5) == 120
    assert cfg.resolve_timeout(300) == 300
    assert cfg.resolve_timeout(99999) == 600


def test_shell_session_name():
    cfg = RemoteShellConfig.from_env(
        {**_SHELL_MINIMAL, "AREAL_REMOTE_SHELL_SESSION_PREFIX": "x_"}
    )
    assert cfg.session_name("abc") == "x_abc"


def test_shell_session_name_is_tmux_id_scoped():
    cfg = RemoteShellConfig.from_env(
        {**_SHELL_MINIMAL, "AREAL_REMOTE_SHELL_SESSION_PREFIX": "x_"}
    )
    assert cfg.session_name("debug-gpu") == "x_debug-gpu"
