---
name: multica-research-fleet
description: "Use when operating the sealed Research Fleet — exploration graph, weighted sources, reports, stage gates, hire/optimize roster via 罗纳尔多. Prefer `multica research` CLI over generic web_search dumps."
user-invocable: false
allowed-tools: Bash(multica *)
---

# Multica Research Fleet

Sealed research squad led by **罗纳尔多**. Users talk to 罗纳尔多 by default.
Fleet members write graph/sources/report through server tools; lead owns roster and stage eval.

## Commands

```bash
multica research session get <session-id> --output json
multica research graph-append <session-id> --type probe --title "..." --summary "..." --from <node-id>
multica research source-upsert <session-id> --url "..." --title "..." --class docs --weight 0.85 --summary "..."
multica research report-patch <session-id> --content "## Findings..."
multica research presence <session-id> --activity "reading RFC..."
multica research message <session-id> --body "..." [--target <agent-id>]
multica research report-to-lead <session-id> --body "validation conflicts..."
multica research stage-eval <session-id>          # lead only
multica research hire --name "专利检索手" --role "patent_scout"
multica research optimize <member-id> --instructions "..." --activate
```

Domain playbooks (tech / market / academic) live under `references/playbooks/` and are seeded fleet-scoped on first ensure.

## Hard rules

- Do not complete a session with a single generic web_search.
- Append exploration nodes for probes, findings, conflicts, dead ends, pivots.
- Never delete dead_end / refuted / pivot history.
- New hires stay `pending_prompt_review` until 罗纳尔多 optimizes + activates.
- Stage eval only at S1–S4 gates.

See `references/research-fleet-source-map.md` for source-traced API paths.
