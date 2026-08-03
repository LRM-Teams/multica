"""Personal Access Token resolution for the E2E harness.

Precedence (mirrors customized_areal/tree_search/agents/multica_auth.py
`resolve_api_key`, reimplemented self-contained because that module lives in
the outer areal repo and is not importable from multica/):

1. explicit argument
2. MULTICA_API_KEY environment variable
3. MULTICA_CREDENTIALS_FILE -> JSON file {"api_key": "pat_..."}
"""

from __future__ import annotations

import json
import os
from pathlib import Path

from e2e_harness.config import ENV_MULTICA_API_KEY, ENV_MULTICA_CREDENTIALS_FILE


class MulticaAuthError(RuntimeError):
    """Raised when no API key can be resolved."""


def _load_credentials_file(path: Path) -> str:
    if not path.is_file():
        raise MulticaAuthError(
            f"{ENV_MULTICA_CREDENTIALS_FILE} does not point at a regular file: {path}"
        )
    try:
        payload = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        raise MulticaAuthError(
            f"failed to read credentials file {path}: {exc}"
        ) from exc
    api_key = payload.get("api_key") if isinstance(payload, dict) else None
    if not isinstance(api_key, str) or not api_key.strip():
        raise MulticaAuthError(
            f"credentials file {path} must be JSON with a non-empty "
            '"api_key" field'
        )
    return api_key.strip()


def resolve_api_key(
    explicit_api_key: str | None = None,
    *,
    credentials_path: Path | None = None,
    env: dict[str, str] | None = None,
) -> str:
    """Resolve explicit, environment, then saved-file credentials."""
    if explicit_api_key and explicit_api_key.strip():
        return explicit_api_key.strip()
    if env is None:
        env = dict(os.environ)
    environment_api_key = env.get(ENV_MULTICA_API_KEY, "").strip()
    if environment_api_key:
        return environment_api_key
    path = credentials_path
    if path is None:
        raw = env.get(ENV_MULTICA_CREDENTIALS_FILE, "").strip()
        if raw:
            path = Path(raw)
    if path is not None:
        return _load_credentials_file(path)
    raise MulticaAuthError(
        "no multica API key: pass one explicitly, set "
        f"{ENV_MULTICA_API_KEY}, or point {ENV_MULTICA_CREDENTIALS_FILE} at "
        'a JSON file containing {"api_key": "pat_..."}'
    )
