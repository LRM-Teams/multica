"""Env-var configuration for the env-dispatch issue E2E harness.

Contract: specs/002-env-dispatch-issue-e2e/contracts/harness-config.md
All values come from the process environment (conftest optionally loads a
gitignored `.env` via python-dotenv before this module is used).
"""

from __future__ import annotations

import os
from dataclasses import dataclass

# Env-var name constants (single source of truth for the contract).
ENV_MULTICA_BASE_URL = "MULTICA_BASE_URL"
ENV_MULTICA_WORKSPACE_ID = "MULTICA_WORKSPACE_ID"
ENV_MULTICA_WORKSPACE_SLUG = "MULTICA_WORKSPACE_SLUG"
ENV_MULTICA_API_KEY = "MULTICA_API_KEY"
ENV_MULTICA_CREDENTIALS_FILE = "MULTICA_CREDENTIALS_FILE"
ENV_MULTICA_AGENT_ID = "MULTICA_AGENT_ID"
ENV_MULTICA_BASE_ENV_ID = "MULTICA_BASE_ENV_ID"
ENV_CUBE_PROXY_URL = "CUBE_PROXY_URL"
ENV_CUBE_DOMAIN = "CUBE_DOMAIN"
ENV_E2E_STAGE_TIMEOUT_SEC = "E2E_STAGE_TIMEOUT_SEC"
ENV_E2E_DAG_POLL_INTERVAL_SEC = "E2E_DAG_POLL_INTERVAL_SEC"
ENV_E2E_NEGATIVE_CONTROL = "E2E_NEGATIVE_CONTROL"

# Defaults per contracts/harness-config.md.
DEFAULT_CUBE_DOMAIN = "cube.app"
DEFAULT_STAGE_TIMEOUT_SEC = 1200
DEFAULT_DAG_POLL_INTERVAL_SEC = 10

_TRUE_VALUES = {"1", "true", "yes", "on"}


class ConfigError(ValueError):
    """Raised when required configuration is missing or invalid."""


@dataclass(frozen=True)
class HarnessConfig:
    """Resolved harness configuration (contracts/harness-config.md)."""

    base_url: str
    agent_id: str
    base_env_id: str
    cube_proxy_url: str
    workspace_id: str | None = None
    workspace_slug: str | None = None
    api_key: str | None = None
    credentials_file: str | None = None
    cube_domain: str = DEFAULT_CUBE_DOMAIN
    stage_timeout_sec: int = DEFAULT_STAGE_TIMEOUT_SEC
    dag_poll_interval_sec: int = DEFAULT_DAG_POLL_INTERVAL_SEC
    negative_control: bool = False


def parse_negative_control(raw: str | None) -> bool:
    """Parse E2E_NEGATIVE_CONTROL: unset/"0" => False; 1/true/yes/on => True."""
    if raw is None:
        return False
    return raw.strip().lower() in _TRUE_VALUES


def missing_required_vars(env: dict[str, str]) -> list[str]:
    """Return the names of every missing required variable (contract table)."""
    missing: list[str] = []

    def _blank(name: str) -> bool:
        return not env.get(name, "").strip()

    if _blank(ENV_MULTICA_BASE_URL):
        missing.append(ENV_MULTICA_BASE_URL)
    if _blank(ENV_MULTICA_WORKSPACE_ID) and _blank(ENV_MULTICA_WORKSPACE_SLUG):
        missing.append(f"{ENV_MULTICA_WORKSPACE_ID} or {ENV_MULTICA_WORKSPACE_SLUG}")
    if _blank(ENV_MULTICA_API_KEY) and _blank(ENV_MULTICA_CREDENTIALS_FILE):
        missing.append(f"{ENV_MULTICA_API_KEY} or {ENV_MULTICA_CREDENTIALS_FILE}")
    for name in (
        ENV_MULTICA_AGENT_ID,
        ENV_MULTICA_BASE_ENV_ID,
        ENV_CUBE_PROXY_URL,
    ):
        if _blank(name):
            missing.append(name)
    return missing


def load_config(env: dict[str, str] | None = None) -> HarnessConfig:
    """Build a HarnessConfig from the environment.

    Raises ConfigError listing every missing required variable.
    """
    if env is None:
        env = dict(os.environ)
    missing = missing_required_vars(env)
    if missing:
        raise ConfigError(
            "missing required env vars for the env-dispatch e2e harness: "
            + ", ".join(missing)
        )

    def _int(name: str, default: int) -> int:
        raw = env.get(name, "").strip()
        if not raw:
            return default
        try:
            value = int(raw)
        except ValueError as exc:
            raise ConfigError(f"{name} must be an integer, got {raw!r}") from exc
        if value <= 0:
            raise ConfigError(f"{name} must be > 0, got {value}")
        return value

    return HarnessConfig(
        base_url=env[ENV_MULTICA_BASE_URL].strip().rstrip("/"),
        workspace_id=env.get(ENV_MULTICA_WORKSPACE_ID, "").strip() or None,
        workspace_slug=env.get(ENV_MULTICA_WORKSPACE_SLUG, "").strip() or None,
        api_key=env.get(ENV_MULTICA_API_KEY, "").strip() or None,
        credentials_file=env.get(ENV_MULTICA_CREDENTIALS_FILE, "").strip() or None,
        agent_id=env[ENV_MULTICA_AGENT_ID].strip(),
        base_env_id=env[ENV_MULTICA_BASE_ENV_ID].strip(),
        cube_proxy_url=env[ENV_CUBE_PROXY_URL].strip().rstrip("/"),
        cube_domain=env.get(ENV_CUBE_DOMAIN, "").strip() or DEFAULT_CUBE_DOMAIN,
        stage_timeout_sec=_int(ENV_E2E_STAGE_TIMEOUT_SEC, DEFAULT_STAGE_TIMEOUT_SEC),
        dag_poll_interval_sec=_int(
            ENV_E2E_DAG_POLL_INTERVAL_SEC, DEFAULT_DAG_POLL_INTERVAL_SEC
        ),
        negative_control=parse_negative_control(env.get(ENV_E2E_NEGATIVE_CONTROL)),
    )
