"""Cube sandbox `/execute` transport for the E2E harness.

Runs a bash command inside a Cube sandbox via the Cube proxy and returns a
structured ExecResult. Protocol per
specs/002-env-dispatch-issue-e2e/contracts/sandbox-exec.md:

- POST {CUBE_PROXY_URL}/execute with Host: 49999-{sandbox_id}.{CUBE_DOMAIN},
  body {"language": "python", "code": <payload>}.
- The response is an NDJSON event stream; there is no shell exit-code field.
  The payload runs the real command via subprocess, prints stdout/stderr, then
  a `__EXIT_CODE__=<rc>` sentinel line, and raises SystemExit(rc) so non-zero
  exits surface as an `error` event.
- Verdict rule (FR-006): PASS iff the sentinel reports 0 AND no `error` event.

NDJSON parser adapted from multica/db_bridge/scripts/cube_to_bridge_smoke.py
(`_api_execute`, lines ~90-117).
"""

from __future__ import annotations

import json
import re
import urllib.error
import urllib.request
from dataclasses import dataclass

EXIT_CODE_SENTINEL = "__EXIT_CODE__"
_SENTINEL_RE = re.compile(rf"{re.escape(EXIT_CODE_SENTINEL)}=(\d+)")

# Outer HTTP timeout headroom over the in-payload subprocess timeout.
_HTTP_TIMEOUT_HEADROOM_SEC = 60

_PAYLOAD_TEMPLATE = """\
import subprocess
p = subprocess.run(["bash", "-lc", {cmd!r}], capture_output=True, text=True, timeout={timeout})
print(p.stdout)
print(p.stderr)
print(f"{sentinel}={{p.returncode}}")
raise SystemExit(p.returncode)
"""


@dataclass(frozen=True)
class ExecResult:
    """Outcome of one `/execute` call.

    `exit_code` is None when the sentinel was never seen (transport/payload
    failure). `transport_error` is set for HTTP failures, timeouts, a missing
    sentinel, or an `error` event that contradicts a zero exit code.
    """

    exit_code: int | None
    stdout: str
    stderr: str
    transport_error: str | None = None

    @property
    def ok(self) -> bool:
        """FR-006 verdict: sentinel exit code 0 and no error/transport failure."""
        return self.transport_error is None and self.exit_code == 0


def build_execute_payload(bash_cmd: str, timeout_sec: int) -> str:
    """Python source posted to /execute (exit-code sentinel convention)."""
    return _PAYLOAD_TEMPLATE.format(
        cmd=bash_cmd, timeout=timeout_sec, sentinel=EXIT_CODE_SENTINEL
    )


def parse_ndjson_stream(raw: str) -> ExecResult:
    """Parse an /execute NDJSON event stream into an ExecResult.

    Accumulates `stdout` event text (minus the sentinel line), captures
    `error` events, and extracts the `__EXIT_CODE__=` sentinel.
    """
    stdout_parts: list[str] = []
    error_events: list[str] = []
    exit_code: int | None = None
    for line in raw.splitlines():
        line = line.strip()
        if not line:
            continue
        try:
            event = json.loads(line)
        except json.JSONDecodeError:
            continue
        event_type = event.get("type")
        if event_type == "stdout":
            text = event.get("text", "")
            match = _SENTINEL_RE.search(text)
            if match:
                exit_code = int(match.group(1))
                text = _SENTINEL_RE.sub("", text)
            stdout_parts.append(text)
        elif event_type == "error":
            name = event.get("name", "error")
            value = event.get("value", "")
            traceback = event.get("traceback", "")
            rendered = f"{name}: {value}".strip()
            if traceback:
                rendered = f"{rendered}\n{traceback}"
            error_events.append(rendered)

    stdout = "".join(stdout_parts)
    stderr = "\n".join(error_events)
    transport_error: str | None = None
    if exit_code is None:
        transport_error = (
            f"missing {EXIT_CODE_SENTINEL} sentinel in /execute stream "
            "(payload did not complete)"
        )
    elif exit_code == 0 and error_events:
        transport_error = (
            "error event in /execute stream despite zero exit code: "
            + error_events[0]
        )
    return ExecResult(
        exit_code=exit_code,
        stdout=stdout,
        stderr=stderr,
        transport_error=transport_error,
    )


def run_in_sandbox(
    cube_proxy_url: str,
    cube_domain: str,
    sandbox_id: str,
    bash_cmd: str,
    timeout_sec: int,
) -> ExecResult:
    """Run `bash_cmd` inside `sandbox_id` via the Cube proxy /execute endpoint.

    Never raises for remote failures: HTTP/transport problems are reported in
    `transport_error` so callers can attach them to diagnostics.
    """
    payload = {
        "language": "python",
        "code": build_execute_payload(bash_cmd, timeout_sec),
    }
    req = urllib.request.Request(
        f"{cube_proxy_url.rstrip('/')}/execute",
        data=json.dumps(payload).encode(),
        headers={
            "Content-Type": "application/json",
            "Host": f"49999-{sandbox_id}.{cube_domain}",
        },
        method="POST",
    )
    http_timeout = timeout_sec + _HTTP_TIMEOUT_HEADROOM_SEC
    try:
        with urllib.request.urlopen(req, timeout=http_timeout) as resp:
            raw = resp.read().decode()
    except urllib.error.HTTPError as exc:
        body = ""
        try:
            body = exc.read().decode()[:500]
        except Exception:
            pass
        return ExecResult(
            exit_code=None,
            stdout="",
            stderr="",
            transport_error=f"/execute HTTP {exc.code}: {body or exc.reason}",
        )
    except (urllib.error.URLError, TimeoutError, OSError) as exc:
        return ExecResult(
            exit_code=None,
            stdout="",
            stderr="",
            transport_error=f"/execute transport error: {exc}",
        )
    return parse_ndjson_stream(raw)
