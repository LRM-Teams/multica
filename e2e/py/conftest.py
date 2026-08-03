"""Pytest bootstrap for the env-dispatch issue E2E suite.

- Inserts the suite root into sys.path so `e2e_harness` is importable.
- Loads a gitignored `.env` via python-dotenv when available (optional dep).
- Gates e2e tests on required env vars (contracts/harness-config.md): the
  module-scoped `harness_config` fixture skips when any is absent.
"""

from __future__ import annotations

import os
import sys
from pathlib import Path

import pytest

SUITE_ROOT = Path(__file__).resolve().parent
if str(SUITE_ROOT) not in sys.path:
    sys.path.insert(0, str(SUITE_ROOT))

try:
    from dotenv import load_dotenv
except ImportError:  # python-dotenv is optional; env vars alone are enough
    load_dotenv = None

if load_dotenv is not None:
    load_dotenv(SUITE_ROOT / ".env")

from e2e_harness.config import load_config, missing_required_vars

REQUIRED_ENV_MISSING: list[str] = missing_required_vars(dict(os.environ))

# Decorator for e2e test modules: skip the whole module when the shared
# deployment is not configured (contracts/harness-config.md "Required" table).
requires_env = pytest.mark.skipif(
    bool(REQUIRED_ENV_MISSING),
    reason="missing required env vars: " + ", ".join(REQUIRED_ENV_MISSING)
    if REQUIRED_ENV_MISSING
    else "",
)


@pytest.fixture(scope="module")
def harness_config():
    """Module-scoped resolved HarnessConfig; skips when required env is absent."""
    if REQUIRED_ENV_MISSING:
        pytest.skip(
            "missing required env vars: " + ", ".join(REQUIRED_ENV_MISSING)
        )
    return load_config()
