#!/usr/bin/env python3
"""Live e2e probe: enqueue a chat_completions bridge row into the REAL Supabase
and wait for the running AReaL-side executor to forward it to the gateway
(BRIDGE_GATEWAY_UPSTREAM_URL) and write the response back.

Run from the db_bridge dir with .env.areal sourced:
    set -a && source .env.areal && set +a
    uv run --no-sync python scripts/live_chat_probe.py
"""

from __future__ import annotations

import asyncio
import json
import uuid

from db_bridge.channels import CHANNELS_BY_NAME
from db_bridge.config import BridgeConfig
from db_bridge.db import BridgeDB

USER_ID = "00000000-0000-0000-0000-00000000000a"


async def main() -> int:
    cfg = BridgeConfig.from_env()
    ch = CHANNELS_BY_NAME["chat_completions"]
    body = json.dumps(
        {
            "model": "areal/qwen3",
            "max_tokens": 200,
            "messages": [
                {"role": "user", "content": "Reply with exactly the word: BRIDGED"}
            ],
        }
    ).encode()

    db = BridgeDB(cfg)
    try:
        await db.connect()
        row_id = await db.insert_request(
            ch,
            user_id=USER_ID,
            method="POST",
            path="/chat/completions",
            headers={"content-type": "application/json"},
            content_type="application/json",
            body=body,
            meta={"probe": str(uuid.uuid4())},
        )
        print(f"enqueued row id={row_id} channel={ch.name}")
        resp = await db.wait_for_response(ch, row_id, timeout=120.0, user_id=USER_ID)
    finally:
        await db.aclose()

    if resp is None:
        print("RESULT: TIMEOUT — no terminal response within 120s")
        return 1
    print(f"status={resp.status} response_status={resp.response_status}")
    if resp.error:
        print(f"error={resp.error}")
    try:
        obj = json.loads(resp.body.decode())
        choice = (obj.get("choices") or [{}])[0]
        msg = choice.get("message", {})
        print("assistant content =", repr(msg.get("content")))
        print("finish_reason     =", choice.get("finish_reason"))
        print("usage             =", obj.get("usage"))
        ok = resp.status == "done" and resp.response_status == 200 and bool(obj.get("choices"))
    except Exception as exc:  # noqa: BLE001
        print(f"could not parse body: {exc}; raw={resp.body[:400]!r}")
        ok = False
    print("RESULT:", "PASS — stub-less enqueue -> live executor -> gateway -> DeepSeek" if ok else "FAIL")
    return 0 if ok else 1


if __name__ == "__main__":
    raise SystemExit(asyncio.run(main()))
