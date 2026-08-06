#!/usr/bin/env python3
"""Smoke-test the Cube/Pi -> stub -> executor -> upstream link without real Supabase.

Uses the in-memory FakeSupabaseClient so nothing is written to a live database.
"""

from __future__ import annotations

import argparse
import asyncio
import json
import sys
import time

import httpx

from db_bridge.config import BridgeConfig
from db_bridge.db import BridgeDB
from db_bridge.executor import Executor
from db_bridge.stub_server import create_stub_app

# Reuse the test fake from the package test suite.
sys.path.insert(0, str(__import__("pathlib").Path(__file__).resolve().parents[1] / "tests"))
from _fakes import FakeSupabaseClient  # noqa: E402

USER_ID = "00000000-0000-0000-0000-00000000000a"
DEFAULT_MODEL = "areal/qwen/qwen3_5-9b"


def _config() -> BridgeConfig:
    return BridgeConfig.from_env(
        {
            "SUPABASE_URL": "https://smoke-test.invalid",
            "SUPABASE_SERVICE_ROLE_KEY": "smoke-key",
            "BRIDGE_POLL_INTERVAL": "0.01",
            "BRIDGE_USER_ID": USER_ID,
            "BRIDGE_CONCURRENCY_CHAT_COMPLETIONS": "2",
            "BRIDGE_GATEWAY_UPSTREAM_URL": "http://mock-gateway.local",
        }
    )


def _mock_gateway_handler(seen: dict[str, object]):
    def handler(request: httpx.Request) -> httpx.Response:
        seen["path"] = request.url.path
        seen["method"] = request.method
        seen["authorization"] = request.headers.get("authorization")
        seen["body"] = json.loads(request.content)
        if request.url.path != "/chat/completions":
            return httpx.Response(404, json={"detail": f"unexpected path {request.url.path}"})
        return httpx.Response(
            200,
            json={
                "id": "chatcmpl-smoke",
                "object": "chat.completion",
                "choices": [
                    {
                        "index": 0,
                        "finish_reason": "stop",
                        "message": {"role": "assistant", "content": "smoke-ok"},
                    }
                ],
            },
        )

    return handler


async def _run_smoke(*, use_v1_path: bool) -> int:
    cfg = _config()
    fake = FakeSupabaseClient()
    db = BridgeDB(cfg, client=fake)
    seen: dict[str, object] = {}

    ex = Executor(
        db,
        "areal",
        config=cfg,
        client=httpx.AsyncClient(transport=httpx.MockTransport(_mock_gateway_handler(seen))),
    )
    run_task = asyncio.create_task(ex.run())
    stub = create_stub_app(db, "leagent", cfg)

    path = "/v1/chat/completions" if use_v1_path else "/chat/completions"
    payload = {
        "model": DEFAULT_MODEL,
        "messages": [{"role": "user", "content": "ping"}],
    }

    print("=== db_bridge link smoke test (in-memory DB only) ===")
    print(f"request path: POST {path}")
    print(f"model:        {DEFAULT_MODEL}")
    print(f"user_id:      {USER_ID}")
    print()

    try:
        async with httpx.AsyncClient(
            transport=httpx.ASGITransport(app=stub),
            base_url="http://smoke-stub",
            timeout=30.0,
        ) as client:
            started = time.monotonic()
            resp = await client.post(
                path,
                json=payload,
                headers={"X-Bridge-User-Id": USER_ID, "Authorization": "Bearer smoke-token"},
            )
            elapsed_ms = (time.monotonic() - started) * 1000
    finally:
        ex.stop()
        await run_task

    body = resp.json()
    rows = fake.tables.get("rpc_chat_completions", {})

    print(f"stub status:      {resp.status_code}")
    print(f"stub latency:     {elapsed_ms:.1f} ms")
    print(f"assistant reply:  {body.get('choices', [{}])[0].get('message', {}).get('content')}")
    print()
    print("upstream seen:")
    print(f"  path:  {seen.get('path')}")
    print(f"  model: {(seen.get('body') or {}).get('model')}")
    print()
    print(f"in-memory rows in rpc_chat_completions: {len(rows)} (not sent to real Supabase)")

    ok = (
        resp.status_code == 200
        and body.get("choices")
        and seen.get("path") == "/chat/completions"
        and (seen.get("body") or {}).get("model") == DEFAULT_MODEL
        and len(rows) >= 1
    )
    if ok:
        print()
        print("RESULT: PASS — stub -> in-memory queue -> executor -> mock upstream is working")
        return 0

    print()
    print("RESULT: FAIL — chain did not complete as expected")
    return 1


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--plain-path",
        action="store_true",
        help="Use /chat/completions instead of /v1/chat/completions",
    )
    args = parser.parse_args()
    raise SystemExit(asyncio.run(_run_smoke(use_v1_path=not args.plain_path)))


if __name__ == "__main__":
    main()
