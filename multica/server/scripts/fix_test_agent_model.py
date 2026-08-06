#!/usr/bin/env python3
"""Add model column only to exact INSERT INTO agent (...) statements in backtick SQL."""

from __future__ import annotations

import re
from pathlib import Path

MODEL = "composer-1.5"
AGENT_INSERT = re.compile(r"INSERT INTO agent\s*\(", re.IGNORECASE)


def patch_agent_insert(sql: str) -> str:
    if not AGENT_INSERT.search(sql):
        return sql
    header = sql.split("VALUES")[0] if "VALUES" in sql else sql.split("SELECT")[0]
    if re.search(r"\bmodel\b", header):
        return sql

    sql = re.sub(
        r"(INSERT INTO agent\s*\([^)]*?)(\))",
        lambda m: m.group(1) + ", model" + m.group(2),
        sql,
        count=1,
        flags=re.DOTALL | re.IGNORECASE,
    )

    if "VALUES" in sql:
        # Match outermost VALUES (...) by finding last ) before RETURNING/ON CONFLICT/;
        m = re.search(r"VALUES\s*\((.*)\)(\s*(?:RETURNING|ON CONFLICT|;|$))", sql, re.DOTALL | re.IGNORECASE)
        if m:
            vals, tail = m.group(1), m.group(2)
            sql = sql[: m.start()] + f"VALUES ({vals}, '{MODEL}'){tail}" + sql[m.end() :]
    elif "SELECT" in sql:
        sql = re.sub(r"(\s+FROM\s+)", f", '{MODEL}'\\1", sql, count=1, flags=re.IGNORECASE)

    return sql


def patch_create_agent_params(text: str) -> str:
    """Add Model field to db.CreateAgentParams literals missing it."""
    needle = "db.CreateAgentParams{"
    out = []
    i = 0
    while True:
        start = text.find(needle, i)
        if start == -1:
            out.append(text[i:])
            break
        out.append(text[i:start])
        brace = text.find("{", start)
        depth = 0
        end = brace
        for j in range(brace, len(text)):
            if text[j] == "{":
                depth += 1
            elif text[j] == "}":
                depth -= 1
                if depth == 0:
                    end = j
                    break
        block = text[start : end + 1]
        if "Model:" not in block:
            indent = "\t"
            for line in block.splitlines():
                if line.strip() and not line.strip().startswith("db.CreateAgentParams"):
                    indent = line[: len(line) - len(line.lstrip())]
                    break
            block = block[:-1] + f"{indent}Model:              pgtype.Text{{String: \"{MODEL}\", Valid: true}},\n" + "}"
        out.append(block)
        i = end + 1
    return "".join(out)


def patch_api_agent_maps(text: str) -> str:
    """Add model to JSON map bodies used for CreateAgent when missing."""
    lines = text.splitlines(keepends=True)
    out: list[str] = []
    i = 0
    while i < len(lines):
        line = lines[i]
        if '"runtime_id"' not in line:
            out.append(line)
            i += 1
            continue
        # Look ahead for CreateAgent within a short window.
        ahead = "".join(lines[i : min(len(lines), i + 20)])
        if "CreateAgent" not in ahead:
            out.append(line)
            i += 1
            continue
        # Walk back to the opening brace of this map literal.
        start = i
        while start > 0 and "map[string]any{" not in lines[start] and "map[string]any {" not in lines[start]:
            start -= 1
        block = "".join(lines[start : min(len(lines), i + 20)])
        if '"model"' in block or "'model'" in block:
            out.append(line)
            i += 1
            continue
        indent = line[: len(line) - len(line.lstrip())]
        out.append(line)
        out.append(f'{indent}"model":                "{MODEL}",\n')
        i += 1
    return "".join(out)


def patch_file(text: str) -> str:
    def repl(m: re.Match[str]) -> str:
        inner = m.group(1)
        if not AGENT_INSERT.search(inner):
            return m.group(0)
        return "`" + patch_agent_insert(inner) + "`"

    text = re.sub(r"`((?:[^`\\]|\\.)*)`", repl, text, flags=re.DOTALL)
    text = patch_create_agent_params(text)
    text = patch_api_agent_maps(text)
    return text


def main() -> None:
    root = Path(__file__).resolve().parents[1]
    changed = []
    for path in sorted(root.rglob("*_test.go")):
        if "cmd/migrate" in path.parts:
            continue
        original = path.read_text()
        updated = patch_file(original)
        if updated != original:
            path.write_text(updated)
            changed.append(str(path.relative_to(root)))
    print(f"updated {len(changed)} files")


if __name__ == "__main__":
    main()
