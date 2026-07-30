#!/usr/bin/env python3
"""One-time fixture provisioning (research R4).

Bakes `fixture_repo/` into a Cube template and creates the base env the E2E
chain forks from:

1. Boot a sandbox from the `default` template (POST /api/sandboxes).
2. Wait until it is running and its Cube sandbox id (local_ref) is known.
3. Write every `fixture_repo/` file into it via the Cube /execute endpoint.
4. Snapshot it into a Cube template
   (POST /api/sandboxes/{id}/create-template -> poll node snapshots until
   the snapshot row is `ready` and carries the Cube template id).
5. Create a base env from that template (POST /api/v1/env).
6. Delete the builder sandbox (best effort) and print
   `MULTICA_BASE_ENV_ID=<uuid>` — add it to `.env`.

Required env: MULTICA_BASE_URL, MULTICA_WORKSPACE_ID|MULTICA_WORKSPACE_SLUG,
MULTICA_API_KEY (or MULTICA_CREDENTIALS_FILE), CUBE_PROXY_URL. Optional:
CUBE_DOMAIN (default cube.app).

Run from the suite root: `python provision_fixture.py` (cannot run offline —
needs the shared deployment).
"""

from __future__ import annotations

import base64
import json
import os
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
from pathlib import Path
from typing import Any

SUITE_ROOT = Path(__file__).resolve().parent
if str(SUITE_ROOT) not in sys.path:
    sys.path.insert(0, str(SUITE_ROOT))

try:
    from dotenv import load_dotenv
except ImportError:  # python-dotenv is optional
    load_dotenv = None

if load_dotenv is not None:
    load_dotenv(SUITE_ROOT / ".env")

from e2e_harness.auth import resolve_api_key
from e2e_harness.config import (
    DEFAULT_CUBE_DOMAIN,
    ENV_CUBE_DOMAIN,
    ENV_CUBE_PROXY_URL,
    ENV_MULTICA_BASE_URL,
    ENV_MULTICA_WORKSPACE_ID,
    ENV_MULTICA_WORKSPACE_SLUG,
)
from e2e_harness.sandbox_exec import run_in_sandbox

FIXTURE_REPO_DIR = SUITE_ROOT / "fixture_repo"
# Fixture path inside the sandbox (contracts/sandbox-exec.md caveat; the
# harness runs acceptance/lineage checks here).
SANDBOX_REPO_PATH = "/workspace/repo"
SOURCE_TEMPLATE = "default"
SNAPSHOT_NAME_PREFIX = "e2e-fixture-calc"

_BOOT_TIMEOUT_SEC = 600
_SNAPSHOT_TIMEOUT_SEC = 900
_POLL_INTERVAL_SEC = 5
_EXEC_TIMEOUT_SEC = 60


class ProvisionError(RuntimeError):
    """Provisioning failed; message names the step."""


def _env(name: str) -> str:
    value = os.environ.get(name, "").strip()
    if not value:
        raise ProvisionError(f"missing required env var: {name}")
    return value


class _ServerAPI:
    """Minimal Bearer-authed JSON helper (stdlib urllib only)."""

    def __init__(
        self,
        base_url: str,
        api_key: str,
        workspace_id: str | None,
        workspace_slug: str | None,
    ) -> None:
        self.base_url = base_url.rstrip("/")
        self.api_key = api_key
        self.workspace_id = workspace_id
        self.workspace_slug = workspace_slug

    def request(
        self, method: str, path: str, body: dict[str, Any] | None = None
    ) -> tuple[int, Any]:
        query: dict[str, str] = {}
        if self.workspace_id:
            query["workspace_id"] = self.workspace_id
        elif self.workspace_slug:
            query["workspace_slug"] = self.workspace_slug
        url = f"{self.base_url}{path}?{urllib.parse.urlencode(query)}"
        data = json.dumps(body).encode() if body is not None else None
        req = urllib.request.Request(
            url,
            data=data,
            headers={
                "Authorization": f"Bearer {self.api_key}",
                "Content-Type": "application/json",
                "Accept": "application/json",
            },
            method=method,
        )
        try:
            with urllib.request.urlopen(req, timeout=60) as resp:
                raw = resp.read().decode()
                return resp.status, json.loads(raw) if raw else {}
        except urllib.error.HTTPError as exc:
            raw = exc.read().decode() if exc.fp is not None else ""
            raise ProvisionError(
                f"{method} {path} -> HTTP {exc.code}: {raw[:500]}"
            ) from exc
        except (urllib.error.URLError, TimeoutError, OSError) as exc:
            raise ProvisionError(f"{method} {path} transport error: {exc}") from exc


def _wait_for_sandbox_running(api: _ServerAPI, instance_id: str) -> str:
    """Poll the instance until running; return the Cube sandbox id (local_ref)."""
    deadline = time.monotonic() + _BOOT_TIMEOUT_SEC
    while time.monotonic() < deadline:
        status, body = api.request("GET", f"/api/sandboxes/{instance_id}")
        if status == 200 and isinstance(body, dict):
            state = body.get("status")
            if state == "failed":
                raise ProvisionError(
                    f"builder sandbox failed: {body.get('error') or body}"
                )
            local_ref = (body.get("local_ref") or "").strip()
            if state == "running" and local_ref:
                return local_ref
        time.sleep(_POLL_INTERVAL_SEC)
    raise ProvisionError(
        f"builder sandbox {instance_id} not running after {_BOOT_TIMEOUT_SEC}s"
    )


def _write_fixture_files(
    cube_proxy_url: str, cube_domain: str, cube_sandbox_id: str
) -> None:
    files = sorted(p for p in FIXTURE_REPO_DIR.rglob("*") if p.is_file())
    if not files:
        raise ProvisionError(f"no fixture files under {FIXTURE_REPO_DIR}")
    for path in files:
        rel = path.relative_to(FIXTURE_REPO_DIR).as_posix()
        dest = f"{SANDBOX_REPO_PATH}/{rel}"
        encoded = base64.b64encode(path.read_bytes()).decode()
        cmd = (
            f"mkdir -p {Path(dest).parent.as_posix()} && "
            f"printf %s '{encoded}' | base64 -d > {dest}"
        )
        result = run_in_sandbox(
            cube_proxy_url, cube_domain, cube_sandbox_id, cmd, _EXEC_TIMEOUT_SEC
        )
        if not result.ok:
            raise ProvisionError(
                f"writing {dest} failed "
                f"(exit={result.exit_code}, transport={result.transport_error}): "
                f"{result.stdout}{result.stderr}"[:500]
            )
        print(f"  wrote {dest}")


def _verify_fixture(cube_proxy_url: str, cube_domain: str, cube_sandbox_id: str) -> None:
    cmd = (
        f"cd {SANDBOX_REPO_PATH} && "
        "test -f pyproject.toml && "
        "test -f src/fixture_calc/calculator.py && "
        "test -f tests/test_calculator.py && "
        "grep -q NotImplementedError src/fixture_calc/calculator.py"
    )
    result = run_in_sandbox(
        cube_proxy_url, cube_domain, cube_sandbox_id, cmd, _EXEC_TIMEOUT_SEC
    )
    if not result.ok:
        raise ProvisionError(
            "fixture verification inside the sandbox failed "
            f"(exit={result.exit_code}, transport={result.transport_error}): "
            f"{result.stdout}{result.stderr}"[:500]
        )
    print(f"  fixture verified at {SANDBOX_REPO_PATH}")


def _create_template(api: _ServerAPI, instance_id: str, node_id: str) -> str:
    """Snapshot the sandbox into a Cube template; return the template id."""
    name = f"{SNAPSHOT_NAME_PREFIX}-{int(time.time())}"
    status, body = api.request(
        "POST",
        f"/api/sandboxes/{instance_id}/create-template",
        {
            "name": name,
            "description": "fixture-calc base template for the env-dispatch issue e2e",
        },
    )
    if status != 202 or not isinstance(body, dict) or not body.get("id"):
        raise ProvisionError(f"create-template -> HTTP {status}: {body}")
    snapshot_id = body["id"]

    deadline = time.monotonic() + _SNAPSHOT_TIMEOUT_SEC
    while time.monotonic() < deadline:
        status, rows = api.request("GET", f"/api/sandbox/nodes/{node_id}/snapshots")
        if status == 200 and isinstance(rows, list):
            for row in rows:
                if isinstance(row, dict) and row.get("id") == snapshot_id:
                    state = row.get("status")
                    if state == "failed":
                        raise ProvisionError(
                            f"snapshot failed: {row.get('error') or row}"
                        )
                    if state == "ready":
                        template_id = (row.get("cube_snapshot_id") or "").strip()
                        if not template_id:
                            raise ProvisionError(
                                "snapshot ready but cube_snapshot_id is empty"
                            )
                        return template_id
        time.sleep(_POLL_INTERVAL_SEC)
    raise ProvisionError(
        f"snapshot {snapshot_id} not ready after {_SNAPSHOT_TIMEOUT_SEC}s"
    )


def main() -> int:
    base_url = _env(ENV_MULTICA_BASE_URL)
    cube_proxy_url = _env(ENV_CUBE_PROXY_URL)
    cube_domain = os.environ.get(ENV_CUBE_DOMAIN, "").strip() or DEFAULT_CUBE_DOMAIN
    workspace_id = os.environ.get(ENV_MULTICA_WORKSPACE_ID, "").strip() or None
    workspace_slug = os.environ.get(ENV_MULTICA_WORKSPACE_SLUG, "").strip() or None
    if not workspace_id and not workspace_slug:
        raise ProvisionError(
            f"missing required env var: {ENV_MULTICA_WORKSPACE_ID} or "
            f"{ENV_MULTICA_WORKSPACE_SLUG}"
        )
    api_key = resolve_api_key()
    api = _ServerAPI(base_url, api_key, workspace_id, workspace_slug)

    print(f"1/6 booting builder sandbox from template {SOURCE_TEMPLATE!r} ...")
    status, body = api.request(
        "POST",
        "/api/sandboxes",
        {"template": SOURCE_TEMPLATE, "name": f"e2e-fixture-builder-{int(time.time())}"},
    )
    if status != 201 or not isinstance(body, dict) or not body.get("id"):
        raise ProvisionError(f"POST /api/sandboxes -> HTTP {status}: {body}")
    instance_id = body["id"]
    node_id = body.get("node_id") or ""

    print("2/6 waiting for the sandbox to run ...")
    cube_sandbox_id = _wait_for_sandbox_running(api, instance_id)
    print(f"  cube sandbox id: {cube_sandbox_id}")

    print(f"3/6 writing fixture_repo into {SANDBOX_REPO_PATH} ...")
    _write_fixture_files(cube_proxy_url, cube_domain, cube_sandbox_id)
    _verify_fixture(cube_proxy_url, cube_domain, cube_sandbox_id)

    print("4/6 snapshotting into a Cube template ...")
    template_id = _create_template(api, instance_id, node_id)
    print(f"  cube template id: {template_id}")

    print("5/6 creating the base env ...")
    status, env_body = api.request("POST", "/api/v1/env", {"image_ref": template_id})
    if status != 201 or not isinstance(env_body, dict) or not env_body.get("env_id"):
        raise ProvisionError(f"POST /api/v1/env -> HTTP {status}: {env_body}")
    env_id = env_body["env_id"]

    print("6/6 deleting the builder sandbox (best effort) ...")
    try:
        api.request("DELETE", f"/api/sandboxes/{instance_id}")
    except ProvisionError as exc:
        print(f"  warning: builder sandbox cleanup failed: {exc}", file=sys.stderr)

    print()
    print(f"MULTICA_BASE_ENV_ID={env_id}")
    print("Add the line above to multica/e2e/py/.env")
    return 0


if __name__ == "__main__":
    try:
        sys.exit(main())
    except ProvisionError as exc:
        print(f"provisioning failed: {exc}", file=sys.stderr)
        sys.exit(1)
