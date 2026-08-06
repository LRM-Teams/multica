#!/usr/bin/env python3
"""Live RL probe: enqueue an rl_start_session row into the shared Supabase with
an admin Bearer header — exactly what the multica-side stub does — and confirm
the running AReaL-side executor forwards it to the gateway (which enforces the
admin key) and returns session creds.

    set -a && source .env.areal && set +a
    GATEWAY_ADMIN_API_KEY=<key> uv run --no-sync python scripts/rl_probe.py
"""

from __future__ import annotations

import asyncio
import json
import os
import sys

from db_bridge.channels import CHANNELS_BY_NAME
from db_bridge.config import BridgeConfig
from db_bridge.db import BridgeDB

USER_ID = "00000000-0000-0000-0000-00000000000a"


async def main() -> int:
    admin_key = os.environ.get("GATEWAY_ADMIN_API_KEY", "").strip()
    channel_name = sys.argv[1] if len(sys.argv) > 1 else "rl_start_session"
    ch = CHANNELS_BY_NAME[channel_name]
    path = {"rl_start_session": "/rl/start_session"}.get(channel_name, f"/{channel_name}")
    body = json.dumps({"task_id": "probe-task", "env_id": ""}).encode()
    headers = {"content-type": "application/json"}
    if admin_key:
        headers["authorization"] = f"Bearer {admin_key}"

    cfg = BridgeConfig.from_env()
    db = BridgeDB(cfg)
    try:
        await db.connect()
        row_id = await db.insert_request(
            ch, user_id=USER_ID, method="POST", path=path,
            headers=headers, content_type="application/json", body=body,
        )
        print(f"enqueued {channel_name} row id={row_id} (admin_key={'yes' if admin_key else 'no'})")
        resp = await db.wait_for_response(ch, row_id, timeout=60.0, user_id=USER_ID)
    finally:
        await db.aclose()

    if resp is None:
        print("RESULT: TIMEOUT — executor did not relay within 60s")
        return 1
    print(f"status={resp.status} response_status={resp.response_status}")
    print(f"body={resp.body.decode(errors='replace')[:300]}")
    ok = resp.status == "done" and resp.response_status == 200
    print("RESULT:", "PASS — enqueue -> areal executor -> gateway (admin-key OK)" if ok else "FAIL")
    return 0 if ok else 1


if __name__ == "__main__":
    raise SystemExit(asyncio.run(main()))
