---
name: multica-research-fleet
description: "Use when operating the sealed Research Fleet — exploration graph, weighted sources, reports, stage gates, hire/optimize/archive roster via 罗纳尔多. Prefer `multica research` CLI over generic web_search dumps."
user-invocable: false
allowed-tools: Bash(multica *)
---

# Multica Research Fleet

Sealed research squad led by **罗纳尔多**. Users talk to 罗纳尔多 by default.
The session UI is **canvas-first** (exploration graph is the truth surface); fleet chat is an optional hideable card feed. Tool ops should keep the canvas dense via `graph-append` / `source-upsert` / `presence` — the server also emits process cards for ops.
Fleet members write graph/sources/report through server tools; lead owns roster and stage eval.

## Commands

```bash
multica research session get <session-id> --output json
multica research graph-append <session-id> --type probe --title "..." --summary "..." --from <node-id>
multica research source-upsert <session-id> --url "..." --title "..." --class docs --weight 0.85 --summary "..." --why "..."
multica research report-patch <session-id> --content "## Findings..."
multica research presence <session-id> --activity "reading RFC..."
multica research message <session-id> --body "..." [--target <agent-id>]
multica research report-to-lead <session-id> --body "validation conflicts..."
multica research stage-eval <session-id>          # lead only
multica research hire --name "专利检索手" --role "patent_scout" \
  [--model composer-1.5] [--instructions "..."] [--reason "S2 缺专利专长"]
multica research optimize <member-id> --instructions "..." [--model ...] [--activate] [--reason "..."]
multica research archive <member-id> --reason "S2 后闲置"   # lead only; soft-delete + stop wakes
```

Domain playbooks (tech / market / academic + fine domains) live under `references/playbooks/` and are seeded fleet-scoped on first ensure.

## Hard rules

- Do not complete a session with a single generic web_search.
- Append exploration nodes for probes, findings, conflicts, dead ends, pivots.
- Never delete dead_end / refuted / pivot history.
- New hires stay `pending_prompt_review` until 罗纳尔多 optimizes + activates; hire requires a real specialty-gap reason; activate assigns work (no empty pads).
- Only lead may hire / optimize / archive. Soft roster cap = 12 non-archived (depth budget). Capacity/409 tests use `--fixture` (no user-canvas pad walls). Shell hire↔archive within 30m without work is rejected.
- Fleet agents must not rewrite the user session goal (user mid-flight only).
- Stage eval only at S1–S4 gates.
- Archive roster_change nodes use status `archived` (not ACTIVE).

See `references/research-fleet-source-map.md` for source-traced API paths.
