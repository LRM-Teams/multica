#!/usr/bin/env python3
"""Report whether a Turbo dry-run file contains any scheduled tasks."""

import json
import pathlib
import sys


def main() -> None:
    if len(sys.argv) != 2:
        raise SystemExit("usage: ci-turbo-web-dry-run.py <dry-run-file>")
    raw = pathlib.Path(sys.argv[1]).read_text(encoding="utf-8").strip()
    if not raw:
        raise SystemExit("Turbo dry-run output is empty")
    try:
        payload = json.loads(raw)
    except json.JSONDecodeError:
        # Turbo may print a banner before the JSON document.
        start = raw.find("{")
        if start < 0:
            raise
        payload = json.loads(raw[start:])
    tasks = payload.get("tasks") or payload.get("packages") or []
    print("true" if tasks else "false")


if __name__ == "__main__":
    main()
