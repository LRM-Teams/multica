#!/usr/bin/env python3
"""Minimal local AReaL proxy gateway for db_bridge dev.

Cube hosts bind 192.168.0.1:8080 to cube-egress (TPROXY/MITM), not the real
AReaL gateway. Point BRIDGE_GATEWAY_UPSTREAM_URL here when no AReaL gateway
process is running on the machine.

Usage:
    python3 scripts/dev_areal_gateway.py
    # or via docker compose service gateway-mock
"""

from __future__ import annotations

import json
import os
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

HOST = os.environ.get("BRIDGE_DEV_GATEWAY_HOST", "127.0.0.1")
PORT = int(os.environ.get("BRIDGE_DEV_GATEWAY_PORT", "18080"))


class _Handler(BaseHTTPRequestHandler):
    server_version = "dev-areal-gateway/0.1"

    def log_message(self, fmt: str, *args: object) -> None:
        print(f"[dev-gateway] {self.address_string()} - {fmt % args}")

    def _read_body(self) -> bytes:
        length = int(self.headers.get("Content-Length", "0") or 0)
        return self.rfile.read(length) if length else b""

    def _json_response(self, status: int, payload: object) -> None:
        raw = json.dumps(payload).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(raw)))
        self.end_headers()
        self.wfile.write(raw)

    def _sse_response(self, status: int, chunks: list[object]) -> None:
        body = b"".join(
            f"data: {json.dumps(chunk, ensure_ascii=False)}\n\n".encode()
            for chunk in chunks
        ) + b"data: [DONE]\n\n"
        self.send_response(status)
        self.send_header("Content-Type", "text/event-stream")
        self.send_header("Cache-Control", "no-cache")
        self.send_header("Connection", "keep-alive")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def _chat_completion_payload(self, body: bytes) -> tuple[str, bool, str]:
        model = "areal/dev"
        stream = False
        try:
            payload = json.loads(body.decode() or "{}")
            if isinstance(payload, dict):
                if isinstance(payload.get("model"), str):
                    model = payload["model"]
                stream = bool(payload.get("stream"))
        except json.JSONDecodeError:
            pass
        return model, stream, "bridge-dev-gateway-ok"

    def _chat_completion_response(self, body: bytes) -> None:
        model, stream, content = self._chat_completion_payload(body)
        if stream:
            self._sse_response(
                200,
                [
                    {
                        "id": "chatcmpl-dev",
                        "object": "chat.completion.chunk",
                        "model": model,
                        "choices": [
                            {
                                "index": 0,
                                "delta": {"role": "assistant", "content": ""},
                                "finish_reason": None,
                            }
                        ],
                    },
                    {
                        "id": "chatcmpl-dev",
                        "object": "chat.completion.chunk",
                        "model": model,
                        "choices": [
                            {
                                "index": 0,
                                "delta": {"content": content},
                                "finish_reason": None,
                            }
                        ],
                    },
                    {
                        "id": "chatcmpl-dev",
                        "object": "chat.completion.chunk",
                        "model": model,
                        "choices": [
                            {
                                "index": 0,
                                "delta": {},
                                "finish_reason": "stop",
                            }
                        ],
                    },
                ],
            )
            return
        self._json_response(
            200,
            {
                "id": "chatcmpl-dev",
                "object": "chat.completion",
                "model": model,
                "choices": [
                    {
                        "index": 0,
                        "finish_reason": "stop",
                        "message": {
                            "role": "assistant",
                            "content": content,
                        },
                    }
                ],
                "usage": {"prompt_tokens": 1, "completion_tokens": 3},
            },
        )

    def do_POST(self) -> None:
        path = self.path.split("?", 1)[0]
        body = self._read_body()

        if path == "/rl/start_session":
            self._json_response(
                200, {"session_id": "dev-session", "api_key": "sk-dev-session"}
            )
            return

        if path == "/rl/set_reward":
            self._json_response(200, {})
            return

        if path == "/rl/end_session":
            self._json_response(200, {"interaction_count": 1})
            return

        if path == "/chat/completions":
            self._chat_completion_response(body)
            return

        self._json_response(404, {"detail": f"unknown path {path}"})

    def do_GET(self) -> None:
        if self.path in {"/healthz", "/health"}:
            self._json_response(200, {"status": "ok", "service": "dev-areal-gateway"})
            return
        self._json_response(404, {"detail": "not found"})


def main() -> None:
    server = ThreadingHTTPServer((HOST, PORT), _Handler)
    print(f"[dev-gateway] listening on http://{HOST}:{PORT}")
    server.serve_forever()


if __name__ == "__main__":
    main()
