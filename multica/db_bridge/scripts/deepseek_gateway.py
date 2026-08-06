#!/usr/bin/env python3
"""OpenAI-compatible LLM gateway backed by DeepSeek's OpenAI-compatible API.

This process is a drop-in target for ``BRIDGE_GATEWAY_UPSTREAM_URL`` (the "real
AReaL proxy gateway" the db_bridge AReaL-side executor forwards
``/chat/completions`` to). It accepts OpenAI Chat Completions requests
(streaming and non-streaming), forwards them to DeepSeek's OpenAI-compatible
endpoint (``https://api.deepseek.com/v1/chat/completions``), and relays
Server-Sent Events incrementally so ``stream: true`` works end to end.

Also accepts ``POST /responses`` (Anthropic Responses API format sent by pi
daemon v0.3.64) and converts ``"input"`` → ``"messages"`` on the fly.

Zero third-party dependencies (stdlib only) so it runs under any Python 3.12+
without touching a virtualenv.

Config (environment variables):
  GATEWAY_UPSTREAM_URL        OpenAI-compatible base (default
                              https://api.deepseek.com)
  GATEWAY_UPSTREAM_API_KEY    DeepSeek API key (required)
  GATEWAY_MODEL               Upstream model to force (default deepseek-v4-pro).
                              Incoming OpenAI ``model`` names (e.g. ``areal/...``)
                              are remapped to this so the bridge's model names
                              still resolve.
  GATEWAY_HOST                Bind host (default 127.0.0.1 — loopback only)
  GATEWAY_PORT                Bind port (default 8080)
  GATEWAY_MAX_TOKENS          Default max_tokens when the caller omits it
                              (default 4096)
  GATEWAY_UPSTREAM_TIMEOUT    Upstream socket timeout seconds (default 300)

Security: binds to 127.0.0.1 by default and is intended to be reached only over
loopback by the local db_bridge executor. It performs NO inbound auth (the
loopback caller is trusted, matching the existing gateway assumption). Do not
bind it to a public interface.
"""

from __future__ import annotations

import hmac
import json
import os
import sys
import threading
import time
import urllib.error
import urllib.request
import uuid
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

# --- configuration ---------------------------------------------------------

UPSTREAM_BASE = os.environ.get(
    "GATEWAY_UPSTREAM_URL", "https://api.deepseek.com"
).rstrip("/")
UPSTREAM_KEY = os.environ.get("GATEWAY_UPSTREAM_API_KEY", "").strip()
MODEL = os.environ.get("GATEWAY_MODEL", "deepseek-v4-pro").strip()
HOST = os.environ.get("GATEWAY_HOST", "127.0.0.1")
PORT = int(os.environ.get("GATEWAY_PORT", "8080"))
DEFAULT_MAX_TOKENS = int(os.environ.get("GATEWAY_MAX_TOKENS", "4096"))
UPSTREAM_TIMEOUT = float(os.environ.get("GATEWAY_UPSTREAM_TIMEOUT", "300"))

# Optional inbound admin-key enforcement on the RL / trajectory endpoints. When
# GATEWAY_ADMIN_API_KEY is set, /rl/* and /export_trajectories require
# "Authorization: Bearer <key>" (or "x-api-key: <key>"), matching the real AReaL
# gateway's require_admin_key. /chat/completions is intentionally left open — the
# sandbox agent authenticates chat with its per-session proxy key, not the admin
# key. Empty disables enforcement (accept all), preserving the earlier behaviour.
ADMIN_API_KEY = os.environ.get("GATEWAY_ADMIN_API_KEY", "").strip()
_ADMIN_PATHS = frozenset(
    {
        "/rl/start_session",
        "/rl/set_reward",
        "/rl/end_session",
        "/rl/close_segment",
        "/export_trajectories",
        "/rl/export_trajectories",
    }
)

# OpenAI-compatible upstream endpoint.
CHAT_URL = f"{UPSTREAM_BASE}/v1/chat/completions"


def _log(msg: str) -> None:
    print(f"[deepseek-gateway] {msg}", flush=True)


# --- mock RL trajectory state ----------------------------------------------
# The gateway does not run real RL training; close_segment / export_trajectories
# return simulated data so callers that exercise the trajectory lifecycle get a
# well-formed response. A single monotonic counter hands out trajectory ids.

_TRAJ_LOCK = threading.Lock()
_TRAJ_COUNTER = 0


def _next_trajectory_id() -> int:
    global _TRAJ_COUNTER
    with _TRAJ_LOCK:
        tid = _TRAJ_COUNTER
        _TRAJ_COUNTER += 1
    return tid


def _mock_close_segment(payload: dict) -> dict:
    """Simulated CloseSegmentResponse (mirrors data_proxy CloseSegmentResponse)."""
    trajectory_id = _next_trajectory_id()
    session_id = payload.get("session_id") or ("gw-" + uuid.uuid4().hex)
    interaction_count = int(payload.get("interaction_count") or 1)
    return {
        "message": "success",
        "interaction_count": interaction_count,
        "session_id": session_id,
        "trajectory_id": trajectory_id,
        "trajectory_ready": True,
        "ready_transition": True,
    }


def _mock_export_trajectories(payload: dict) -> dict:
    """Simulated ExportTrajectoriesResponse ({"traj": <serialized value>}).

    Returns a well-formed, string-style trajectory (as concat_string_interactions
    would yield) echoing the requested session ids so downstream callers can
    parse a plausible shape without a real training backend.
    """
    session_ids = payload.get("session_ids") or []
    trajectory_id = payload.get("trajectory_id")
    interactions = []
    for i, sid in enumerate(session_ids):
        interactions.append(
            {
                "session_id": sid,
                "interaction_id": f"{sid}:0",
                "trajectory_id": trajectory_id if trajectory_id is not None else i,
                "messages": [
                    {"role": "user", "content": "mock prompt"},
                    {"role": "assistant", "content": "mock completion"},
                ],
                "reward": 1.0,
                "discount": payload.get("discount", 1.0),
            }
        )
    return {
        "traj": {
            "kind": "string_interactions",
            "session_ids": list(session_ids),
            "count": len(interactions),
            "interactions": interactions,
            "mock": True,
        }
    }


# --- Anthropic → OpenAI body normalisation ---------------------------------


def _responses_input_to_messages(inp: list) -> list:
    """Convert Responses API ``input`` items into OpenAI chat messages.

    Responses history items are not all chat messages: ``function_call`` and
    ``function_call_output`` items have no ``role`` and DeepSeek rejects them
    verbatim ("messages[N]: missing field `role`"). Map them to the OpenAI
    tool-calling shapes; drop items with no chat equivalent (e.g. reasoning).
    Consecutive ``function_call`` items are merged into a single assistant
    message with parallel ``tool_calls`` — DeepSeek requires every tool_call
    of an assistant message to be answered immediately by tool messages, so
    one message per call breaks parallel tool use.
    """
    messages = []
    pending_calls: list[dict] = []

    def _flush_calls() -> None:
        if not pending_calls:
            return
        messages.append(
            {
                "role": "assistant",
                "content": "",
                # DeepSeek thinking models require reasoning_content to be
                # echoed back on assistant tool-call messages; the bridge
                # does not carry the original reasoning, so send an empty
                # one to satisfy the validator.
                "reasoning_content": "",
                "tool_calls": list(pending_calls),
            }
        )
        pending_calls.clear()

    for item in inp:
        if not isinstance(item, dict):
            continue
        itype = item.get("type")
        if itype == "function_call":
            pending_calls.append(
                {
                    "id": item.get("call_id") or item.get("id") or "",
                    "type": "function",
                    "function": {
                        "name": item.get("name") or "",
                        "arguments": item.get("arguments") or "",
                    },
                }
            )
            continue
        if itype == "function_call_output":
            _flush_calls()
            out = item.get("output")
            messages.append(
                {
                    "role": "tool",
                    "tool_call_id": item.get("call_id") or "",
                    "content": out
                    if isinstance(out, str)
                    else json.dumps(out, ensure_ascii=False),
                }
            )
            continue
        if itype in (None, "message"):
            _flush_calls()
            content = item.get("content")
            if isinstance(content, list):
                parts = []
                for part in content:
                    if isinstance(part, dict) and part.get("type") in (
                        "input_text",
                        "output_text",
                        "text",
                    ):
                        parts.append({"type": "text", "text": part.get("text") or ""})
                    else:
                        parts.append(part)
                content = parts
            messages.append({"role": item.get("role") or "user", "content": content})
    _flush_calls()
    return messages


def _normalise_for_openai(payload: dict) -> None:
    """Convert Anthropic-isms in-place so DeepSeek's OpenAI endpoint accepts the body.

    pi daemon v0.3.64 always emits Anthropic-inflected JSON even when
    ``--provider openai`` is used: ``input_text`` content-block types,
    Anthropic-style tool definitions, etc.
    """
    # 1. Tools: Anthropic flat shape → OpenAI function-call wrapper.
    tools = payload.get("tools")
    if isinstance(tools, list):
        fixed = []
        for t in tools:
            if isinstance(t, dict):
                if "function" not in t:
                    schema = t.get("input_schema") or {}
                    # DeepSeek requires "type": "object" at minimum.
                    if not isinstance(schema, dict) or schema.get("type") != "object":
                        schema = {"type": "object", "properties": schema if isinstance(schema, dict) else {}}
                    t = {
                        "type": "function",
                        "function": {
                            "name": t.get("name", ""),
                            "description": t.get("description", ""),
                            "parameters": schema,
                        },
                    }
                else:
                    fn = t.get("function") or {}
                    if isinstance(fn, dict):
                        if "parameters" not in fn and "input_schema" in fn:
                            schema = fn.pop("input_schema") or {}
                            if not isinstance(schema, dict) or schema.get("type") != "object":
                                schema = {"type": "object", "properties": schema if isinstance(schema, dict) else {}}
                            fn["parameters"] = schema
                # Ensure strict: false (Anthropic sends this, OpenAI doesn't want it).
                t.pop("strict", None)
                fixed.append(t)
        payload["tools"] = fixed

    tool_choice = payload.get("tool_choice")
    if isinstance(tool_choice, dict):
        if tool_choice.get("type") == "auto":
            payload["tool_choice"] = "auto"

    # 2. Messages: fix content-block types and normalise role names.
    messages = payload.get("messages")
    if not isinstance(messages, list):
        return
    for m in messages:
        if not isinstance(m, dict):
            continue
        content = m.get("content")
        if isinstance(content, list):
            for part in content:
                if isinstance(part, dict):
                    if part.get("type") in ("input_text", "output_text"):
                        part["type"] = "text"
        elif isinstance(content, str):
            pass
        elif content is None:
            m["content"] = ""

    # 3. Strip Anthropic-specific fields that OpenAI rejects.
    for key in ("input", "prompt_cache_key", "store"):
        payload.pop(key, None)


def _upstream_request(payload: dict, *, stream: bool):
    """Send an OpenAI-format body to DeepSeek's /v1/chat/completions."""
    data = json.dumps(payload).encode()
    req = urllib.request.Request(CHAT_URL, data=data, method="POST")
    req.add_header("content-type", "application/json")
    req.add_header("Authorization", f"Bearer {UPSTREAM_KEY}")
    if stream:
        req.add_header("accept", "text/event-stream")
    return urllib.request.urlopen(req, timeout=UPSTREAM_TIMEOUT)


# --- HTTP handler -----------------------------------------------------------


class Handler(BaseHTTPRequestHandler):
    server_version = "deepseek-gateway/2.0"
    # Keep default HTTP/1.0 semantics: streaming bodies are delimited by
    # connection close (no Content-Length), which stdlib/httpx clients honour.

    def log_message(self, fmt: str, *args: object) -> None:  # quieter default log
        return

    def _read_body(self) -> bytes:
        length = int(self.headers.get("Content-Length", "0") or 0)
        return self.rfile.read(length) if length else b""

    def _admin_ok(self) -> bool:
        """Constant-time check of the inbound admin key (Bearer or x-api-key)."""
        auth = self.headers.get("Authorization", "")
        if auth.lower().startswith("bearer "):
            token = auth[7:].strip()
        else:
            token = (self.headers.get("x-api-key") or "").strip()
        return hmac.compare_digest(token, ADMIN_API_KEY)

    def _parse_json(self, body: bytes) -> dict | None:
        """Parse a JSON object body; on failure send 400 and return None."""
        try:
            obj = json.loads(body.decode() or "{}")
            if not isinstance(obj, dict):
                raise ValueError("body must be a JSON object")
            return obj
        except (ValueError, UnicodeDecodeError) as exc:
            self._json(400, {"error": {"message": f"invalid JSON body: {exc}"}})
            return None

    def _json(self, status: int, payload: object) -> None:
        raw = json.dumps(payload).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(raw)))
        self.end_headers()
        self.wfile.write(raw)

    def _begin_sse(self) -> None:
        self.send_response(200)
        self.send_header("Content-Type", "text/event-stream")
        self.send_header("Cache-Control", "no-cache")
        self.send_header("Connection", "close")
        self.end_headers()

    def do_GET(self) -> None:
        if self.path.split("?", 1)[0] in ("/healthz", "/health"):
            self._json(
                200,
                {"status": "ok", "service": "deepseek-gateway", "model": MODEL},
            )
            return
        self._json(404, {"error": {"message": f"unknown path {self.path}"}})

    def do_POST(self) -> None:
        path = self.path.split("?", 1)[0]
        body = self._read_body()

        if ADMIN_API_KEY and path in _ADMIN_PATHS and not self._admin_ok():
            self._json(401, {"error": {"message": "missing or invalid admin API key"}})
            return

        # RL session endpoints: harmless stubs so the gateway is a functional
        # drop-in even in online-training mode (no trajectory collection here).
        if path == "/rl/start_session":
            # Real AReaL returns a per-session api_key that the sandbox agent uses
            # as its chat proxy key; multica's arealrl client rejects an empty one
            # ("start_session response missing api_key"). Mint a non-empty key.
            session_id = "gw-" + uuid.uuid4().hex
            self._json(
                200,
                {"session_id": session_id, "api_key": "sk-areal-" + uuid.uuid4().hex},
            )
            return
        if path == "/rl/set_reward":
            self._json(200, {})
            return
        if path == "/rl/end_session":
            self._json(200, {"interaction_count": 0})
            return

        # Simulated trajectory lifecycle (mock data — no real RL backend here).
        if path == "/rl/close_segment":
            payload = self._parse_json(body)
            if payload is None:
                return
            self._json(200, _mock_close_segment(payload))
            return
        if path in ("/export_trajectories", "/rl/export_trajectories"):
            payload = self._parse_json(body)
            if payload is None:
                return
            if not payload.get("session_ids"):
                self._json(400, {"error": "session_ids is required"})
                return
            self._json(200, _mock_export_trajectories(payload))
            return

        if path not in ("/chat/completions", "/v1/chat/completions", "/responses"):
            self._json(404, {"error": {"message": f"unknown path {path}"}})
            return

        try:
            payload = json.loads(body.decode() or "{}")
            if not isinstance(payload, dict):
                raise ValueError("body must be a JSON object")
        except (ValueError, UnicodeDecodeError) as exc:
            self._json(400, {"error": {"message": f"invalid JSON body: {exc}"}})
            return

        if not UPSTREAM_KEY:
            self._json(
                500, {"error": {"message": "GATEWAY_UPSTREAM_API_KEY is not set"}}
            )
            return

        # pi daemon (openai-responses api) POSTs Responses API format
        # ("input" items) instead of chat.completions "messages". Convert
        # input -> messages so the body is valid OpenAI format before
        # forwarding; history items like function_call/function_call_output
        # are mapped to OpenAI tool-calling shapes by the converter.
        if "input" in payload and "messages" not in payload:
            inp = payload["input"]
            if isinstance(inp, str):
                payload["messages"] = [{"role": "user", "content": inp}]
            elif isinstance(inp, list):
                payload["messages"] = _responses_input_to_messages(inp)

        # Fix Anthropic-isms (content block types, tool shapes) so DeepSeek's
        # OpenAI-compatible endpoint accepts the body.
        _normalise_for_openai(payload)

        # Ensure the model label is preserved for the caller's benefit (the
        # upstream always sees MODEL, but we echo the original model back).
        model_label = payload.get("model") or MODEL
        stream = bool(payload.get("stream"))

        # Override model to our configured upstream model (DeepSeek).
        payload["model"] = MODEL

        try:
            if stream:
                self._handle_stream(payload, model_label)
            else:
                self._handle_nonstream(payload, model_label)
        except urllib.error.HTTPError as exc:
            detail = exc.read().decode(errors="replace")
            _log(f"upstream HTTP {exc.code}: {detail[:500]}")
            if not self._headers_sent():
                self._json(
                    exc.code if 400 <= exc.code < 600 else 502,
                    {"error": {"message": "upstream error", "detail": detail[:2000]}},
                )
        except Exception as exc:  # noqa: BLE001 - surface any relay failure
            _log(f"relay error: {exc!r}")
            if not self._headers_sent():
                self._json(502, {"error": {"message": f"relay error: {exc}"}})

    def _headers_sent(self) -> bool:
        return getattr(self, "_sent", False)

    def _handle_nonstream(self, payload: dict, model_label: str) -> None:
        """Forward non-streaming request to DeepSeek, return response as-is."""
        resp = _upstream_request(payload, stream=False)
        with resp:
            raw = resp.read()
        self._sent = True
        obj = json.loads(raw.decode())
        # Restore the original model label so callers see their own model name.
        if isinstance(obj, dict):
            obj["model"] = model_label
        self._json(200, obj)

    def _handle_stream(self, payload: dict, model_label: str) -> None:
        """Relay streaming SSE from DeepSeek to the client.

        DeepSeek's OpenAI endpoint returns standard OpenAI SSE chunks
        (data: {...}\n\n).  We relay them directly, only rewriting the
        model label in each chunk so callers see their own model name.
        """
        resp = _upstream_request(payload, stream=True)
        self._begin_sse()
        self._sent = True
        try:
            for raw in resp:
                line = raw.decode(errors="replace")
                # Rewrite the model label in SSE chunks.
                if line.startswith("data:") and '"model"' in line:
                    # Best-effort: only replace the first "model" value.
                    import re
                    line = re.sub(
                        r'("model"\s*:\s*)"[^"]*"',
                        rf'\1"{model_label}"',
                        line,
                    )
                self.wfile.write(line.encode() if isinstance(line, str) else raw)
                self.wfile.flush()
        finally:
            resp.close()


def main() -> None:
    if not UPSTREAM_KEY:
        _log("WARNING: GATEWAY_UPSTREAM_API_KEY is empty; /chat/completions will 500.")
    _log(f"upstream={CHAT_URL} model={MODEL} bind={HOST}:{PORT}")
    _log(
        "admin key enforcement: "
        + ("ON (/rl/*, /export_trajectories)" if ADMIN_API_KEY else "OFF")
    )
    server = ThreadingHTTPServer((HOST, PORT), Handler)
    _log(f"listening on http://{HOST}:{PORT} (loopback-only unless HOST changed)")
    try:
        server.serve_forever()
    except KeyboardInterrupt:
        pass
    finally:
        server.server_close()


if __name__ == "__main__":
    sys.exit(main())
