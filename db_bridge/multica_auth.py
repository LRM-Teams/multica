"""Static API-key authentication for the multica-side public bridge server.

Unlike the loopback stub servers (which trust their single local caller), the
multica server is exposed to the open internet, so every route must authenticate
the caller. Callers present a static key via either::

    Authorization: Bearer <key>
    X-API-Key: <key>

and the key is checked against a configured set with a constant-time compare.
The LLM and remote-shell surfaces use *separate* key sets because the shell
endpoint enqueues arbitrary code for execution on the trusted AReaL host.

Auth is fail-closed: an empty key set rejects every request.
"""

from __future__ import annotations

import hmac
from collections.abc import Iterable

from fastapi import Header, HTTPException, status

_BEARER_PREFIX = "bearer "


def _extract_key(authorization: str | None, x_api_key: str | None) -> str | None:
    """Pull the presented key from either supported header form."""
    if x_api_key and x_api_key.strip():
        return x_api_key.strip()
    if authorization:
        value = authorization.strip()
        if value.lower().startswith(_BEARER_PREFIX):
            return value[len(_BEARER_PREFIX):].strip() or None
        # Tolerate a bare token without the "Bearer " scheme.
        return value or None
    return None


def _key_authorized(presented: str, allowed: Iterable[str]) -> bool:
    """Constant-time membership check so timing does not leak which key matched."""
    matched = False
    for candidate in allowed:
        if hmac.compare_digest(presented, candidate):
            matched = True
    return matched


def require_api_key(allowed_keys: frozenset[str], *, surface: str):
    """Build a FastAPI dependency that authorizes against ``allowed_keys``.

    ``surface`` is only used in the error/diagnostic message (e.g. ``"llm"`` or
    ``"shell"``). The returned dependency raises ``401`` on any miss.
    """

    async def dependency(
        authorization: str | None = Header(default=None),
        x_api_key: str | None = Header(default=None, alias="X-API-Key"),
    ) -> str:
        presented = _extract_key(authorization, x_api_key)
        if presented is None or not _key_authorized(presented, allowed_keys):
            raise HTTPException(
                status_code=status.HTTP_401_UNAUTHORIZED,
                detail=f"missing or invalid API key for the multica {surface} endpoint",
                headers={"WWW-Authenticate": "Bearer"},
            )
        return presented

    dependency.__name__ = f"require_{surface}_api_key"
    return dependency
