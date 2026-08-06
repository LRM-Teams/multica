"""Unit tests for e2e_harness.config (T012)."""

from __future__ import annotations

import pytest

from e2e_harness.config import (
    ConfigError,
    HarnessConfig,
    load_config,
    missing_required_vars,
    parse_negative_control,
)


def _full_env(**overrides: str) -> dict[str, str]:
    env = {
        "MULTICA_BASE_URL": "http://server:8090",
        "MULTICA_WORKSPACE_ID": "ws-uuid",
        "MULTICA_API_KEY": "pat_test",
        "MULTICA_AGENT_ID": "agent-uuid",
        "MULTICA_BASE_ENV_ID": "env-uuid",
        "CUBE_PROXY_URL": "http://cube-proxy",
    }
    env.update(overrides)
    return env


def test_load_config_applies_defaults() -> None:
    config = load_config(_full_env())
    assert isinstance(config, HarnessConfig)
    assert config.base_url == "http://server:8090"
    assert config.cube_domain == "cube.app"
    assert config.stage_timeout_sec == 1200
    assert config.dag_poll_interval_sec == 10
    assert config.negative_control is False
    assert config.workspace_id == "ws-uuid"
    assert config.workspace_slug is None


def test_load_config_strips_trailing_slashes() -> None:
    config = load_config(
        _full_env(MULTICA_BASE_URL="http://server:8090/", CUBE_PROXY_URL="http://p/")
    )
    assert config.base_url == "http://server:8090"
    assert config.cube_proxy_url == "http://p"


def test_load_config_applies_optional_overrides() -> None:
    config = load_config(
        _full_env(
            CUBE_DOMAIN="example.dev",
            E2E_STAGE_TIMEOUT_SEC="30",
            E2E_DAG_POLL_INTERVAL_SEC="2",
        )
    )
    assert config.cube_domain == "example.dev"
    assert config.stage_timeout_sec == 30
    assert config.dag_poll_interval_sec == 2


def test_missing_required_error_lists_every_missing_var() -> None:
    with pytest.raises(ConfigError) as excinfo:
        load_config({})
    message = str(excinfo.value)
    for expected in (
        "MULTICA_BASE_URL",
        "MULTICA_WORKSPACE_ID or MULTICA_WORKSPACE_SLUG",
        "MULTICA_API_KEY or MULTICA_CREDENTIALS_FILE",
        "MULTICA_AGENT_ID",
        "MULTICA_BASE_ENV_ID",
        "CUBE_PROXY_URL",
    ):
        assert expected in message


def test_missing_required_vars_empty_env() -> None:
    missing = missing_required_vars({})
    assert len(missing) == 6


def test_workspace_slug_satisfies_workspace_requirement() -> None:
    env = _full_env()
    del env["MULTICA_WORKSPACE_ID"]
    env["MULTICA_WORKSPACE_SLUG"] = "my-team"
    config = load_config(env)
    assert config.workspace_slug == "my-team"
    assert config.workspace_id is None


def test_credentials_file_satisfies_auth_requirement() -> None:
    env = _full_env()
    del env["MULTICA_API_KEY"]
    env["MULTICA_CREDENTIALS_FILE"] = "/tmp/creds.json"
    config = load_config(env)
    assert config.credentials_file == "/tmp/creds.json"
    assert config.api_key is None


@pytest.mark.parametrize(
    ("raw", "expected"),
    [
        (None, False),
        ("", False),
        ("0", False),
        ("no", False),
        ("1", True),
        ("true", True),
        ("TRUE", True),
        ("yes", True),
        ("on", True),
        (" 1 ", True),
    ],
)
def test_parse_negative_control(raw: str | None, expected: bool) -> None:
    assert parse_negative_control(raw) is expected


def test_negative_control_round_trips_through_load_config() -> None:
    assert load_config(_full_env(E2E_NEGATIVE_CONTROL="1")).negative_control is True
    assert load_config(_full_env(E2E_NEGATIVE_CONTROL="0")).negative_control is False


def test_invalid_integer_timeout_raises() -> None:
    with pytest.raises(ConfigError, match="E2E_STAGE_TIMEOUT_SEC"):
        load_config(_full_env(E2E_STAGE_TIMEOUT_SEC="abc"))
    with pytest.raises(ConfigError, match="E2E_STAGE_TIMEOUT_SEC"):
        load_config(_full_env(E2E_STAGE_TIMEOUT_SEC="-5"))
