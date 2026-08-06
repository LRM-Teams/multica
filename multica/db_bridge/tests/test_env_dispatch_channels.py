"""env-dispatch multica_api channel tests: POST /api/v1/env-dispatch and
DELETE /api/v1/env-dispatch/{projectID}.

Stub runs on the AReaL side; executor on the multica side (forwarding to the
real multica API). Verifies the env-dispatch contract relays end to end:
status code, rollouts[] JSON body, path params, and the Authorization header.
"""

from __future__ import annotations

import asyncio
import contextlib
import json

import httpx

from db_bridge.channels import CHANNELS_BY_NAME
from db_bridge.config import BridgeConfig
from db_bridge.db import BridgeDB
from db_bridge.executor import Executor
from db_bridge.stub_server import create_stub_app

from _fakes import FakeSupabaseClient

USER_ID = "00000000-0000-0000-0000-00000000000a"


def _config(**overrides: str) -> BridgeConfig:
    env = {
        "SUPABASE_URL": "https://example.supabase.co",
        "SUPABASE_SERVICE_ROLE_KEY": "k",
        "BRIDGE_HEADER_ENCRYPTION_KEY": (
            "MDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDA="
        ),
        "BRIDGE_POLL_INTERVAL": "0.01",
        "BRIDGE_USER_ID": USER_ID,
        **overrides,
    }
    return BridgeConfig.from_env(env)


@contextlib.asynccontextmanager
async def areal_harness(handler, **cfg_overrides):
    cfg = _config(**cfg_overrides)
    db = BridgeDB(cfg, client=FakeSupabaseClient())
    ex = Executor(
        db,
        "multica",
        config=cfg,
        client=httpx.AsyncClient(transport=httpx.MockTransport(handler)),
    )
    run_task = asyncio.create_task(ex.run())
    stub = create_stub_app(db, "areal", cfg)
    try:
        async with httpx.AsyncClient(
            transport=httpx.ASGITransport(app=stub), base_url="http://stub"
        ) as client:
            yield client, db
    finally:
        ex.stop()
        await run_task


def test_channels_registered_in_multica_api_group():
    disp = CHANNELS_BY_NAME["env_dispatch"]
    dele = CHANNELS_BY_NAME["env_dispatch_delete"]
    assert disp.group == "multica_api"
    assert disp.method == "POST"
    assert disp.path == "/api/v1/env-dispatch"
    assert disp.table == "rpc_env_dispatch"
    assert disp.stub_side == "areal"
    assert disp.executor_side == "multica"
    assert disp.default_timeout_s == 600.0
    assert dele.method == "DELETE"
    assert dele.path == "/api/v1/env-dispatch/{projectID}"
    assert dele.table == "rpc_env_dispatch_delete"
    assert dele.group == "multica_api"
    assert dele.executor_side == "multica"


def test_env_dispatch_post_relays_caller_authorization():
    seen = {}

    def handler(request: httpx.Request) -> httpx.Response:
        seen["auth"] = request.headers.get("authorization")
        seen["path"] = request.url.path
        seen["body"] = json.loads(request.content)
        return httpx.Response(
            201,
            json={
                "rollouts": [
                    {"env_id": "e1", "project_id": "p1", "issue_id": "i1",
                     "agent_run_id": "r1"},
                ]
            },
        )

    async def run():
        async with areal_harness(handler) as (client, _db):
            return await client.post(
                "/api/v1/env-dispatch",
                headers={"Authorization": "Bearer multica-key"},
                json={
                    "mode": "scratch", "env_id": "base", "domain": "swe_lego",
                    "dispatch_type": "issue", "group_size": 1, "agent_id": "ag",
                    "issue": {"title": "t"},
                },
            )

    resp = asyncio.run(run())
    assert resp.status_code == 201
    assert resp.json()["rollouts"][0]["agent_run_id"] == "r1"
    assert seen["auth"] == "Bearer multica-key"
    assert seen["path"] == "/api/v1/env-dispatch"
    assert seen["body"]["mode"] == "scratch"


def test_env_dispatch_delete_relays_path_param():
    seen = {}

    def handler(request: httpx.Request) -> httpx.Response:
        seen["path"] = request.url.path
        seen["method"] = request.method
        return httpx.Response(204)

    async def run():
        async with areal_harness(handler) as (client, _db):
            return await client.delete(
                "/api/v1/env-dispatch/proj-123",
                headers={"Authorization": "Bearer multica-key"},
            )

    resp = asyncio.run(run())
    assert resp.status_code == 204
    assert seen["method"] == "DELETE"
    assert seen["path"] == "/api/v1/env-dispatch/proj-123"


def test_env_dispatch_dag_get_relays_path_param_and_status():
    seen: dict = {}

    def handler(request: httpx.Request) -> httpx.Response:
        seen["method"] = request.method
        seen["path"] = request.url.path
        return httpx.Response(
            200, json={"segments": [], "edges": [], "session_to_agent_run": {}}
        )

    async def run():
        async with areal_harness(handler) as (client, _db):
            return await client.get(
                "/api/v1/env-dispatch/proj-456/dag",
                headers={"Authorization": "Bearer caller-key"},
            )

    resp = asyncio.run(run())
    # GET with a {projectID} path param relays end to end (200 assembled DAG).
    assert resp.status_code == 200
    assert seen["method"] == "GET"
    assert seen["path"] == "/api/v1/env-dispatch/proj-456/dag"


def test_env_dispatch_dag_404_passes_through():
    def handler(request: httpx.Request) -> httpx.Response:
        return httpx.Response(404, json={"detail": "project not found"})

    async def run():
        async with areal_harness(handler) as (client, _db):
            return await client.get("/api/v1/env-dispatch/missing/dag")

    resp = asyncio.run(run())
    # 404 (unknown project) must survive the relay for the client to map to
    # DagNotFound, not be turned into a 502.
    assert resp.status_code == 404


async def test_env_dispatch_authorization_is_encrypted_before_enqueue():
    cfg = _config()
    db = BridgeDB(cfg, client=FakeSupabaseClient())
    stub = create_stub_app(db, "areal", cfg)
    channel = CHANNELS_BY_NAME["env_dispatch"]

    async def complete_after_inspection():
        while not db.client.tables.get(channel.table):
            await asyncio.sleep(0.001)
        row = next(iter(db.client.tables[channel.table].values()))
        stored_auth = row["request_headers"]["authorization"]
        assert stored_auth.startswith("enc:v1:")
        assert "multica-key" not in stored_auth
        request = await db.claim_next(channel, "test-executor")
        assert request is not None
        await db.complete(
            channel,
            request.id,
            worker_id=request.worker_id,
            response_status=201,
            response_headers={},
            body=b'{"project_id":"p1"}',
        )

    responder = asyncio.create_task(complete_after_inspection())
    async with httpx.AsyncClient(
        transport=httpx.ASGITransport(app=stub), base_url="http://stub"
    ) as client:
        response = await client.post(
            "/api/v1/env-dispatch",
            headers={"Authorization": "Bearer multica-key"},
            json={"mode": "scratch", "dispatch_type": "issue"},
        )
    await responder
    assert response.status_code == 201
