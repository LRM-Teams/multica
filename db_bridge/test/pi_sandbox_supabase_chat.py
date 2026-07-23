#!/usr/bin/env python3
"""与 Cube 沙箱中的 Pi 对话（使用 .env 里 TEAM_* 模型配置）。

运行（任选其一）：
    cd /home/jian40/db_bridge
    python3 test/pi_sandbox_supabase_chat.py
    uv run python test/pi_sandbox_supabase_chat.py

改输入：编辑下方「可修改参数」，或修改项目根目录 `.env` 中的 TEAM_* 字段。

说明：
  - 对话前会写入沙箱 /home/user/.pi/agent/models.json（openai + areal 配置）
  - Cube execute 以 root 运行，调用 Pi 时必须 HOME=/home/user，否则会读 /root/.pi/agent
  - Pi 凭据来自 models.json / auth.json，不要用 --api-key（会绕过 baseUrl 连 api.openai.com）
  - PI_PROVIDER=openai：pi --provider openai --model <TEAM_MODEL> -p --no-session（直连 TEAM API）
  - PI_PROVIDER=areal：pi --provider areal --model <PI_AREAL_MODEL> -p --no-session（经 db_bridge）
  - areal 路径请求会写入 Supabase；openai/TEAM 模型不经 db_bridge
  - Pi CLI 默认 stream=true；dev gateway-mock 需返回 SSE（见 scripts/dev_areal_gateway.py）
  - VERIFY_SUPABASE_BRIDGE=True 时额外做 HTTP 探测并校验 Supabase 落库
"""

from __future__ import annotations

import json
import textwrap
import time
import urllib.error
import urllib.request
from datetime import datetime, timezone
from pathlib import Path
from urllib.parse import quote

# ---------------------------------------------------------------------------
# 可修改参数 — 直接改这里（TEAM_* 默认从 ../.env 读取）
# ---------------------------------------------------------------------------

ENV_FILE = Path(__file__).resolve().parents[1] / ".env"

# Cube 沙箱 ID（留空则自动选最后一个 running 沙箱）
SANDBOX_ID = "abdc892fd65e467ca7afc745abebf709"

# 发给 Pi 的用户消息
USER_MESSAGE = "你好，请用一句话介绍你自己。"

# Pi 对话 provider：openai（TEAM 直连）或 areal（经 db_bridge）
PI_PROVIDER = "areal"

# areal 模式下 pi CLI 使用的 model id（models.json 中注册，如 areal-default）
PI_AREAL_MODEL = "areal/qwen/qwen3_5-9b"

# bridge 用户隔离 ID（areal / bridge 路径需要）
BRIDGE_USER_ID = "ae36de93-5969-4629-866d-7a5ffe82f152"

# 是否验证 Supabase 落库，并额外做 areal bridge HTTP 探测
VERIFY_SUPABASE_BRIDGE = True

# areal bridge 配置
AREAL_BASE_URL = "http://10.110.158.143:9100/v1"
# HTTP 探测用的完整 model 名（需 areal/ 前缀）
AREAL_MODEL = "areal/qwen/qwen3_5-9b"

# Cube 代理
CUBE_PROXY_HTTP = "http://127.0.0.1"
CUBE_API_URL = "http://127.0.0.1:3000"

SANDBOX_GATEWAY_IP: str | None = None
HOST_LAN_IP = "10.110.158.143"
# Cube execute 默认 uid=0；Pi 配置写在 /home/user/.pi/agent
PI_USER = "user"
PI_HOME = "/home/user"

SUPABASE_URL = "http://82.157.184.89:54321"
SUPABASE_SERVICE_ROLE_KEY = (
    "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9."
    "eyJpc3MiOiJzdXBhYmFzZS1kZW1vIiwicm9sZSI6InNlcnZpY2Vfcm9sZSIsImV4cCI6MTk4MzgxMjk5Nn0."
    "EGIM96RAZx35lJzdJsyH-qQwv8Hdp7fsn3W0YpN81IU"
)

# ---------------------------------------------------------------------------
# 从 .env 加载 TEAM_*（可在下方覆盖）
# ---------------------------------------------------------------------------

_DOTENV = {}


def _load_dotenv(path: Path) -> dict[str, str]:
    if not path.is_file():
        return {}
    out: dict[str, str] = {}
    for raw in path.read_text(encoding="utf-8").splitlines():
        line = raw.strip()
        if not line or line.startswith("#") or "=" not in line:
            continue
        key, _, val = line.partition("=")
        key = key.strip()
        val = val.strip().strip('"').strip("'")
        out[key] = val
    return out


_DOTENV = _load_dotenv(ENV_FILE)

TEAM_API_KEY = _DOTENV.get("TEAM_API_KEY", "")
TEAM_BASE_URL = _DOTENV.get("TEAM_BASE_URL", "https://claude-code.club/openai/v1")
TEAM_MODEL = _DOTENV.get("TEAM_MODEL", "gpt-5.5")

# ---------------------------------------------------------------------------
# 实现
# ---------------------------------------------------------------------------

CUBELET_STATE_DIR = Path("/data/cubelet/network-agent/state")


def _list_sandboxes() -> list[dict]:
    req = urllib.request.Request(f"{CUBE_API_URL.rstrip('/')}/sandboxes", method="GET")
    with urllib.request.urlopen(req, timeout=30) as resp:
        return json.loads(resp.read().decode())


def _pick_sandbox_id() -> str:
    if SANDBOX_ID.strip():
        return SANDBOX_ID.strip()
    sandboxes = _list_sandboxes()
    running = [s for s in sandboxes if s.get("state") == "running"]
    if not running:
        raise SystemExit("没有 running 状态的 Cube 沙箱，请填写 SANDBOX_ID")
    return running[-1]["sandboxID"]


def _sandbox_gateway_ip(sandbox_id: str) -> str:
    if SANDBOX_GATEWAY_IP:
        return SANDBOX_GATEWAY_IP
    state_file = CUBELET_STATE_DIR / f"{sandbox_id}.json"
    if not state_file.is_file():
        raise SystemExit(
            f"找不到沙箱网络状态文件 {state_file}，请手动设置 SANDBOX_GATEWAY_IP"
        )
    data = json.loads(state_file.read_text(encoding="utf-8"))
    meta = data.get("persistMetadata") or {}
    gateway = meta.get("gateway_ip") or meta.get("gatewayIp")
    if not gateway:
        raise SystemExit(f"{state_file} 中没有 gateway_ip，请手动设置 SANDBOX_GATEWAY_IP")
    return str(gateway)


def _bridge_base_candidates(areal_base_url: str, gateway_ip: str) -> list[str]:
    bases: list[str] = []
    if "db_bridge_stub" in areal_base_url:
        for host in (gateway_ip, HOST_LAN_IP):
            candidate = areal_base_url.replace("db_bridge_stub", host)
            if candidate not in bases:
                bases.append(candidate)
    else:
        bases.append(areal_base_url)
    return bases


def _sandbox_pi_chat_code(
    *,
    team_api_key: str,
    team_base_url: str,
    team_model: str,
    pi_provider: str,
    pi_areal_model: str,
    user_message: str,
    bridge_user_id: str,
    areal_base_url: str,
    verify_bridge: bool,
    bridge_bases: list[str],
    areal_model: str,
    pi_home: str,
    pi_user: str,
) -> str:
    return textwrap.dedent(
        f'''
        import json
        import os
        import pathlib
        import subprocess

        TEAM_API_KEY = {json.dumps(team_api_key)}
        TEAM_BASE_URL = {json.dumps(team_base_url)}
        TEAM_MODEL = {json.dumps(team_model)}
        PI_PROVIDER = {json.dumps(pi_provider)}
        PI_AREAL_MODEL = {json.dumps(pi_areal_model)}
        USER_MESSAGE = {json.dumps(user_message)}
        BRIDGE_USER_ID = {json.dumps(bridge_user_id)}
        AREAL_BASE_URL = {json.dumps(areal_base_url)}
        VERIFY_BRIDGE = {verify_bridge!r}
        BRIDGE_BASES = {json.dumps(bridge_bases)}
        AREAL_MODEL = {json.dumps(areal_model)}
        PI_HOME = {json.dumps(pi_home)}
        PI_USER = {json.dumps(pi_user)}
        AGENT_DIR = pathlib.Path(PI_HOME) / ".pi/agent"
        MODELS_FILE = AGENT_DIR / "models.json"
        SETTINGS_FILE = AGENT_DIR / "settings.json"
        AUTH_FILE = AGENT_DIR / "auth.json"

        def _chown_pi_agent(path: pathlib.Path) -> None:
            if os.getuid() != 0:
                return
            try:
                import pwd
                pw = pwd.getpwnam(PI_USER)
                os.chown(path, pw.pw_uid, pw.pw_gid)
            except Exception:
                pass

        def write_pi_models_json() -> None:
            AGENT_DIR.mkdir(parents=True, exist_ok=True)
            areal_model_ids = ["areal-distill", "areal-default"]
            if PI_AREAL_MODEL not in areal_model_ids:
                areal_model_ids.append(PI_AREAL_MODEL)
            models = {{
                "providers": {{
                    "openai": {{
                        "baseUrl": TEAM_BASE_URL,
                        "apiKey": TEAM_API_KEY,
                    }},
                    "areal": {{
                        "baseUrl": AREAL_BASE_URL,
                        "api": "openai-completions",
                        "apiKey": "bridge",
                        "headers": {{
                            "X-Bridge-User-Id": BRIDGE_USER_ID,
                        }},
                        "compat": {{
                            "supportsDeveloperRole": False,
                            "supportsReasoningEffort": False,
                        }},
                        "models": [{{"id": mid}} for mid in areal_model_ids],
                    }},
                }},
            }}
            MODELS_FILE.write_text(json.dumps(models, indent=2) + "\\n", encoding="utf-8")
            default_provider = PI_PROVIDER if PI_PROVIDER in ("openai", "areal") else "openai"
            default_model = PI_AREAL_MODEL if default_provider == "areal" else TEAM_MODEL
            settings = {{
                "defaultProvider": default_provider,
                "defaultModel": default_model,
                "theme": "light",
            }}
            SETTINGS_FILE.write_text(json.dumps(settings, indent=2) + "\\n", encoding="utf-8")
            auth = {{
                "openai": {{
                    "type": "api_key",
                    "key": TEAM_API_KEY,
                }},
                "areal": {{
                    "type": "api_key",
                    "key": "bridge",
                }},
            }}
            AUTH_FILE.write_text(json.dumps(auth, indent=2) + "\\n", encoding="utf-8")
            os.chmod(AUTH_FILE, 0o600)
            for path in (AGENT_DIR, MODELS_FILE, SETTINGS_FILE, AUTH_FILE):
                _chown_pi_agent(path)

        def _pi_env() -> dict[str, str]:
            env = os.environ.copy()
            env["HOME"] = PI_HOME
            env["PATH"] = "/usr/local/bin:/home/user/.npm-global/bin:" + env.get("PATH", "")
            return env

        def pi_chat() -> dict:
            if PI_PROVIDER == "areal":
                provider, model, via = "areal", PI_AREAL_MODEL, "pi-areal"
            else:
                provider, model, via = "openai", TEAM_MODEL, "pi-openai"
            env = _pi_env()
            try:
                proc = subprocess.run(
                    [
                        "pi",
                        "--provider", provider,
                        "--model", model,
                        "-p", "--no-session",
                        USER_MESSAGE,
                    ],
                    env=env,
                    text=True,
                    capture_output=True,
                    timeout=180,
                )
            except FileNotFoundError:
                return {{"ok": False, "error": "pi CLI not found"}}
            if proc.returncode != 0:
                return {{
                    "ok": False,
                    "error": (proc.stderr or proc.stdout or "pi failed")[:800],
                    "rc": proc.returncode,
                    "provider": provider,
                    "model": model,
                }}
            reply = proc.stdout.strip()
            return {{
                "ok": bool(reply),
                "via": via,
                "provider": provider,
                "model": model,
                "reply": reply,
            }}

        def areal_bridge_probe() -> dict | None:
            if not VERIFY_BRIDGE:
                return None
            import urllib.error
            import urllib.request
            payload = json.dumps({{
                "model": AREAL_MODEL,
                "messages": [{{"role": "user", "content": USER_MESSAGE}}],
            }}).encode()
            headers = {{
                "Content-Type": "application/json",
                "Authorization": "Bearer bridge",
                "X-Bridge-User-Id": BRIDGE_USER_ID,
            }}
            for base in BRIDGE_BASES:
                url = base.rstrip("/") + "/chat/completions"
                req = urllib.request.Request(url, data=payload, headers=headers, method="POST")
                try:
                    with urllib.request.urlopen(req, timeout=45) as resp:
                        body = json.loads(resp.read().decode())
                        reply = body.get("choices", [{{}}])[0].get("message", {{}}).get("content", "")
                        return {{"ok": True, "via": "areal-bridge", "bridge_url": url, "reply": reply}}
                except urllib.error.HTTPError as exc:
                    if exc.code < 500:
                        return {{
                            "ok": True,
                            "via": "areal-bridge",
                            "bridge_url": url,
                            "status": exc.code,
                            "reply": exc.read().decode()[:500],
                        }}
                except Exception:
                    continue
            return {{"ok": False, "via": "areal-bridge", "error": "bridge unreachable"}}

        write_pi_models_json()
        chat_provider = PI_PROVIDER if PI_PROVIDER in ("openai", "areal") else "openai"
        chat_model = PI_AREAL_MODEL if chat_provider == "areal" else TEAM_MODEL
        chat_base = AREAL_BASE_URL if chat_provider == "areal" else TEAM_BASE_URL
        print(f"=== Pi 对话（{{chat_provider}}）===")
        print(f"provider: {{chat_provider}}")
        print(f"baseUrl:  {{chat_base}}")
        print(f"model:    {{chat_model}}")
        print(f"命令:     pi --provider {{chat_provider}} --model {{chat_model}} -p --no-session ...")
        print(f"用户:     {{USER_MESSAGE}}")
        print(f"Pi HOME:  {{PI_HOME}} (execute uid={{os.getuid()}})")
        print(f"models.json: {{MODELS_FILE}}")
        print(json.dumps(json.loads(MODELS_FILE.read_text()), ensure_ascii=False, indent=2)[:800])

        result = pi_chat()
        if result.get("ok"):
            print(f"Pi: {{result.get('reply', '')[:2000]}}")
        else:
            print(f"Pi 错误: {{result}}")

        bridge_result = areal_bridge_probe()
        if bridge_result is not None:
            print("=== areal bridge HTTP 探测 ===")
            print(json.dumps(bridge_result, ensure_ascii=False))

        print("__RESULT__" + json.dumps({{
            "ok": result.get("ok"),
            "chat": result,
            "bridge": bridge_result,
        }}, ensure_ascii=False))
        '''
    ).strip()


def _execute_in_sandbox(sandbox_id: str, code: str) -> tuple[str, dict | None]:
    payload = json.dumps({"language": "python", "code": code}).encode()
    req = urllib.request.Request(
        f"{CUBE_PROXY_HTTP.rstrip('/')}/execute",
        data=payload,
        headers={
            "Content-Type": "application/json",
            "Host": f"49999-{sandbox_id}.cube.app",
        },
        method="POST",
    )
    with urllib.request.urlopen(req, timeout=240) as resp:
        raw = resp.read().decode()

    stdout_parts: list[str] = []
    errors: list[str] = []
    for line in raw.splitlines():
        line = line.strip()
        if not line:
            continue
        try:
            event = json.loads(line)
        except json.JSONDecodeError:
            continue
        etype = event.get("type")
        if etype == "stdout":
            stdout_parts.append(event.get("text", ""))
        elif etype == "error":
            name = event.get("name", "Error")
            value = event.get("value", event.get("text", ""))
            tb = event.get("traceback", "")
            errors.append(f"{name}: {value}\n{tb}")
        elif etype not in ("number_of_executions", "end_of_execution"):
            errors.append(json.dumps(event, ensure_ascii=False))

    stdout = "".join(stdout_parts)
    if errors and not stdout:
        stdout = "\n".join(errors)

    stdout = "".join(stdout_parts)
    result: dict | None = None
    for line in stdout.splitlines():
        if line.startswith("__RESULT__"):
            result = json.loads(line[len("__RESULT__") :])
    return stdout, result


def _supabase_headers() -> dict[str, str]:
    return {
        "apikey": SUPABASE_SERVICE_ROLE_KEY,
        "Authorization": f"Bearer {SUPABASE_SERVICE_ROLE_KEY}",
    }


def _fetch_supabase_rows(*, since_iso: str, limit: int = 5) -> list[dict]:
    params = (
        "select=id,status,response_status,user_id,created_at,request_path"
        f"&user_id=eq.{quote(BRIDGE_USER_ID, safe='')}"
        f"&created_at=gte.{quote(since_iso, safe='')}"
        "&order=created_at.desc"
        f"&limit={limit}"
    )
    url = f"{SUPABASE_URL.rstrip('/')}/rest/v1/rpc_chat_completions?{params}"
    req = urllib.request.Request(url, headers=_supabase_headers(), method="GET")
    with urllib.request.urlopen(req, timeout=30) as resp:
        return json.loads(resp.read().decode())


def main() -> int:
    provider = PI_PROVIDER.strip().lower()
    if provider not in ("openai", "areal"):
        raise SystemExit(f"PI_PROVIDER 必须是 openai 或 areal，当前为 {PI_PROVIDER!r}")
    if provider == "openai" and not TEAM_API_KEY:
        raise SystemExit(f"openai 模式请在 {ENV_FILE} 中设置 TEAM_API_KEY")

    sandbox_id = _pick_sandbox_id()
    gateway_ip = _sandbox_gateway_ip(sandbox_id)
    bridge_bases = _bridge_base_candidates(AREAL_BASE_URL, gateway_ip)
    chat_model = PI_AREAL_MODEL if provider == "areal" else TEAM_MODEL

    print("=== Pi 沙箱对话测试 ===")
    print(f"沙箱 ID:       {sandbox_id}")
    print(f".env 文件:     {ENV_FILE}")
    print(f"PI_PROVIDER:   {provider}")
    if provider == "openai":
        print(f"TEAM_BASE_URL: {TEAM_BASE_URL}")
        print(f"TEAM_MODEL:    {TEAM_MODEL}")
    else:
        print(f"AREAL_BASE_URL: {AREAL_BASE_URL}")
        print(f"PI_AREAL_MODEL: {PI_AREAL_MODEL}")
    print(f"用户消息:       {USER_MESSAGE}")
    print(f"验证 Supabase:  {VERIFY_SUPABASE_BRIDGE}")
    if VERIFY_SUPABASE_BRIDGE:
        print(f"Bridge 候选:    {', '.join(bridge_bases)}")
        print(f"AREAL_MODEL:    {AREAL_MODEL}")
    print()

    started_at = datetime.now(timezone.utc).isoformat()
    code = _sandbox_pi_chat_code(
        team_api_key=TEAM_API_KEY,
        team_base_url=TEAM_BASE_URL,
        team_model=TEAM_MODEL,
        pi_provider=provider,
        pi_areal_model=PI_AREAL_MODEL,
        user_message=USER_MESSAGE,
        bridge_user_id=BRIDGE_USER_ID,
        areal_base_url=AREAL_BASE_URL.replace("db_bridge_stub", HOST_LAN_IP)
        if "db_bridge_stub" in AREAL_BASE_URL
        else AREAL_BASE_URL,
        verify_bridge=VERIFY_SUPABASE_BRIDGE,
        bridge_bases=bridge_bases,
        areal_model=AREAL_MODEL,
        pi_home=PI_HOME,
        pi_user=PI_USER,
    )

    print("[1/2] 写入 models.json 并发起 Pi 对话...")
    stdout, chat_result = _execute_in_sandbox(sandbox_id, code)
    print(stdout.replace("__RESULT__", "").strip())
    print()

    chat = (chat_result or {}).get("chat") or (chat_result or {}).get("team") or {}
    if not chat.get("ok"):
        print("RESULT: FAIL — Pi 对话未成功")
        if chat:
            print(f"  详情: {chat}")
            err = str(chat.get("error", ""))
            if "No API key found" in err:
                print()
                print("  可能原因：Cube execute 以 root 运行，Pi 默认读 /root/.pi/agent。")
                print(f"  → 脚本已设置 HOME={PI_HOME}；若仍失败请检查 models.json / auth.json 是否存在")
            elif "Connection error" in err or "timed out" in err.lower():
                print()
                print("  models.json 已写入，但 Pi 仍可能连接失败。常见原因：")
                print("  1. 使用了 --api-key 会绕过 models.json baseUrl，连 api.openai.com（沙箱不可达）")
                print("  2. 沙箱 egress 未放行外网 / 宿主机地址")
                print(f"     → Cube 模板 allowOut 需包含外网 API 与 {HOST_LAN_IP}/32")
                print("     → 访问 db_bridge 还需 scripts/cube-tap-tproxy-init.sh up")
        elif not stdout.strip():
            print("  沙箱未返回输出，请检查 SANDBOX_ID 是否仍 running")
        return 1

    print("[2/2] 结果检查...")
    if VERIFY_SUPABASE_BRIDGE:
        time.sleep(0.5)
        rows = _fetch_supabase_rows(since_iso=started_at)
        if not rows:
            rows = _fetch_supabase_rows(since_iso="1970-01-01T00:00:00+00:00", limit=3)
        matched = [r for r in rows if r.get("status") in {"done", "claimed", "pending"}]
        print(f"  Supabase 行数: {len(matched)}")
        for row in matched[:3]:
            print(
                f"  - id={row.get('id')} status={row.get('status')} "
                f"response_status={row.get('response_status')}"
            )
        bridge = (chat_result or {}).get("bridge") or {}
        expect_supabase = provider == "areal"
        if expect_supabase and not matched:
            print("RESULT: FAIL — Pi areal 对话成功但 Supabase 无 bridge 记录")
            if bridge:
                print(f"  bridge 探测: {bridge}")
            return 1
        if matched:
            bad = [
                r
                for r in matched
                if r.get("response_status") not in (200, 201, 204)
            ]
            if bad:
                print("RESULT: FAIL — bridge 已写入 Supabase 但 upstream 非 2xx")
                for row in bad[:3]:
                    print(
                        f"  - id={row.get('id')} status={row.get('status')} "
                        f"response_status={row.get('response_status')}"
                    )
                if bridge:
                    print(f"  bridge 探测: {bridge}")
                return 1
        if bridge.get("status") not in (None, 200, 201, 204):
            print("RESULT: FAIL — bridge 探测返回非 2xx")
            print(f"  bridge 探测: {bridge}")
            return 1
        if not expect_supabase and not matched:
            print("  Supabase: openai 模式无落库记录（预期行为）")
    else:
        if provider == "areal":
            print("  Supabase: 跳过（VERIFY_SUPABASE_BRIDGE=False）")
        else:
            print("  Supabase: 跳过（openai 模型不经 db_bridge 写入）")

    reply = chat.get("reply", "")
    print()
    print(
        f"RESULT: PASS — Pi 对话成功 "
        f"(provider={chat.get('provider', provider)}, model={chat.get('model', chat_model)})"
    )
    if reply:
        print(f"助手回复: {reply[:500]}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
