#!/usr/bin/env python3
"""Probe Cube sandbox -> host -> db_bridge stub connectivity."""

from __future__ import annotations

import argparse
import json
import os
import subprocess
import sys
import urllib.error
import urllib.request

DEFAULT_API = "http://127.0.0.1:3000"
DEFAULT_PROXY = "http://127.0.0.1"
DEFAULT_HOST_IPS = ("10.110.158.143",)
DEFAULT_BRIDGE_PORT = 9100
DEFAULT_USER_ID = "00000000-0000-0000-0000-00000000000a"


def _sandbox_code(host_ips: tuple[str, ...], bridge_port: int, user_id: str) -> str:
    hosts = list(host_ips)
    return f'''
import json
import subprocess
import urllib.error
import urllib.request

HOSTS = {hosts!r}
PORT = {bridge_port}
USER_ID = {user_id!r}

def gateway_ip():
    try:
        out = subprocess.check_output(["ip", "route"], text=True)
        for line in out.splitlines():
            if line.startswith("default"):
                return line.split()[2]
    except Exception:
        return None
    return None

def probe(url, method="GET", body=None, headers=None):
  headers = dict(headers or {{}})
  req = urllib.request.Request(url, data=body, headers=headers, method=method)
  try:
    with urllib.request.urlopen(req, timeout=8) as resp:
      raw = resp.read().decode("utf-8", "replace")
      return {{"ok": True, "status": resp.status, "body": raw[:500]}}
  except urllib.error.HTTPError as exc:
    raw = exc.read().decode("utf-8", "replace")
    return {{"ok": False, "status": exc.code, "body": raw[:500]}}
  except Exception as exc:
    return {{"ok": False, "error": f"{{type(exc).__name__}}: {{exc}}"}}

candidates = []
gw = gateway_ip()
if gw:
    candidates.append(gw)
for host in HOSTS:
    if host not in candidates:
        candidates.append(host)

results = {{"candidates": candidates, "checks": []}}
for host in candidates:
    base = f"http://{{host}}:{{PORT}}"
    health = probe(f"{{base}}/healthz")
    item = {{"host": host, "healthz": health}}
    if health.get("ok"):
        payload = json.dumps({{
            "model": "areal/qwen/qwen3_5-9b",
            "messages": [{{"role": "user", "content": "cube-link-smoke"}}],
        }}).encode()
        item["chat_probe"] = probe(
            f"{{base}}/v1/chat/completions",
            method="POST",
            body=payload,
            headers={{
                "Content-Type": "application/json",
                "X-Bridge-User-Id": USER_ID,
                "Authorization": "Bearer cube-smoke",
            }},
        )
    results["checks"].append(item)

print(json.dumps(results, ensure_ascii=False))
'''


def _api_execute(proxy_http: str, sandbox_id: str, code: str) -> str:
    payload = {"language": "python", "code": code}
    req = urllib.request.Request(
        f"{proxy_http.rstrip('/')}/execute",
        data=json.dumps(payload).encode(),
        headers={
            "Content-Type": "application/json",
            "Host": f"49999-{sandbox_id}.cube.app",
        },
        method="POST",
    )
    with urllib.request.urlopen(req, timeout=120) as resp:
        raw = resp.read().decode()

    stdout_parts: list[str] = []
    for line in raw.splitlines():
        line = line.strip()
        if not line:
            continue
        try:
            event = json.loads(line)
        except json.JSONDecodeError:
            continue
        if event.get("type") == "stdout":
            stdout_parts.append(event.get("text", ""))
        if event.get("type") == "error":
            stdout_parts.append(event.get("text", ""))
    return "".join(stdout_parts)


def _list_sandboxes(api_url: str) -> list[dict]:
    req = urllib.request.Request(f"{api_url.rstrip('/')}/sandboxes", method="GET")
    with urllib.request.urlopen(req, timeout=30) as resp:
        return json.loads(resp.read().decode())


def _pick_sandbox(api_url: str, sandbox_id: str | None) -> str:
    if sandbox_id:
        return sandbox_id
    sandboxes = _list_sandboxes(api_url)
    if not sandboxes:
        raise SystemExit("no cube sandboxes available")
    return sandboxes[-1]["sandboxID"]


def _sandbox_alive(proxy_http: str, sandbox_id: str) -> bool:
    try:
        req = urllib.request.Request(
            f"{proxy_http.rstrip('/')}/health",
            headers={"Host": f"49999-{sandbox_id}.cube.app"},
            method="GET",
        )
        with urllib.request.urlopen(req, timeout=10) as resp:
            return resp.status == 200
    except Exception:
        return False


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--sandbox-id")
    parser.add_argument("--api-url", default=os.environ.get("CUBE_API_URL", DEFAULT_API))
    parser.add_argument("--proxy-http", default=os.environ.get("CUBE_PROXY_HTTP", DEFAULT_PROXY))
    parser.add_argument("--host-ip", action="append", dest="host_ips")
    parser.add_argument("--bridge-port", type=int, default=DEFAULT_BRIDGE_PORT)
    parser.add_argument("--user-id", default=DEFAULT_USER_ID)
    args = parser.parse_args()

    host_ips = tuple(args.host_ips or os.environ.get("CUBE_NODE_IP", DEFAULT_HOST_IPS[0]).split())
    sandbox_id = _pick_sandbox(args.api_url.rstrip("/"), args.sandbox_id)

    print("=== Cube sandbox -> host -> db_bridge connectivity test ===")
    print(f"sandbox:    {sandbox_id}")
    print(f"host ips:   {', '.join(host_ips)}")
    print(f"bridge:     :{args.bridge_port}")
    print()

    if not _sandbox_alive(args.proxy_http, sandbox_id):
        print(f"WARN: sandbox {sandbox_id} /health not reachable; trying execute anyway")

    payload_code = _sandbox_code(host_ips, args.bridge_port, args.user_id)
    stdout = _api_execute(args.proxy_http, sandbox_id, payload_code)

    print("sandbox output:")
    print(stdout.strip() or "<empty>")
    print()

    try:
        data = json.loads(stdout.strip().splitlines()[-1])
    except Exception:
        print("RESULT: FAIL — could not parse sandbox probe output")
        return 1

    passed = False
    for check in data.get("checks", []):
        host = check.get("host")
        health = check.get("healthz", {})
        chat = check.get("chat_probe", {})
        print(f"host {host}:")
        if health.get("ok"):
            print(f"  healthz: OK ({health.get('status')}) {health.get('body', '')[:120]}")
            passed = True
        else:
            print(f"  healthz: FAIL {health}")
        if chat:
            if chat.get("ok"):
                print(f"  chat:    reached stub ({chat.get('status')})")
            else:
                print(f"  chat:    {chat}")
        print()

    if passed:
        print("RESULT: PASS — Cube sandbox can reach db_bridge stub on host")
        return 0

    print("RESULT: FAIL — Cube sandbox could not reach db_bridge stub on any candidate host IP")
    return 1


if __name__ == "__main__":
    raise SystemExit(main())
