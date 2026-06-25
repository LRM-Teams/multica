"""Tests for the multica static-key auth dependency (Task 2)."""

from __future__ import annotations

import httpx
from fastapi import Depends, FastAPI

from db_bridge.multica_auth import require_api_key

LLM_KEYS = frozenset({"llm-key-1", "llm-key-2"})


def _app(allowed: frozenset[str]) -> FastAPI:
    app = FastAPI()
    guard = require_api_key(allowed, surface="llm")

    @app.get("/protected")
    async def protected(_key: str = Depends(guard)) -> dict[str, str]:
        return {"ok": "yes"}

    return app


def _client(app: FastAPI) -> httpx.AsyncClient:
    return httpx.AsyncClient(
        transport=httpx.ASGITransport(app=app), base_url="http://m"
    )


async def test_valid_bearer_key_passes():
    async with _client(_app(LLM_KEYS)) as client:
        resp = await client.get(
            "/protected", headers={"Authorization": "Bearer llm-key-1"}
        )
    assert resp.status_code == 200


async def test_valid_x_api_key_passes():
    async with _client(_app(LLM_KEYS)) as client:
        resp = await client.get("/protected", headers={"X-API-Key": "llm-key-2"})
    assert resp.status_code == 200


async def test_missing_key_returns_401():
    async with _client(_app(LLM_KEYS)) as client:
        resp = await client.get("/protected")
    assert resp.status_code == 401
    assert resp.headers.get("www-authenticate") == "Bearer"


async def test_wrong_key_returns_401():
    async with _client(_app(LLM_KEYS)) as client:
        resp = await client.get(
            "/protected", headers={"Authorization": "Bearer nope"}
        )
    assert resp.status_code == 401


async def test_empty_key_set_is_fail_closed():
    async with _client(_app(frozenset())) as client:
        resp = await client.get(
            "/protected", headers={"Authorization": "Bearer anything"}
        )
    assert resp.status_code == 401
