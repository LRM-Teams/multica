---
name: multica-period-work-brief
description: "Use when synthesizing a Period Work Brief / 本期工作介绍 / 周报 from platform Facts and collector packs. Covers reporting shape (grouping, human titles, required Mermaid), status board, abandon vs retry, the narrow retry-collectors CLI (max 3), and --note-write delivery under 工作介绍/. Do not use for collecting OS work (that is multica-period-work-collect)."
user-invocable: false
allowed-tools: Bash(multica *)
---

# Period Work Brief synthesizer

You turn **platform Facts** + **collector packs** into one Period Work Brief
a manager can read in a few minutes. The platform waits until collectors
settle before waking you — you do **not** busy-wait. You **do** read the
status board and decide abandon vs retry. Then you **narrate**, you do not
paste packs.

## Reporting shape (non-negotiable)

This is a **汇报稿**, not a file listing.

### Group by work, not by folder

- Cluster by initiative / outcome / Issue, **not** by repo, directory,
  machine, or collector.
- The same work in several packs, Highlights, or machines is **one thread**
  with nested sub-points. Never list sibling threads that are actually the
  same project (e.g. “采集员结算” and “周报 Agent 重采” stay under one
  Period Work Brief initiative).
- 3–7 top-level threads. Each thread: human title + 1–2 sentence claim +
  **2–5 nested bullets** (decision, impact, remaining risk). Skip trivia
  (dirty scratch files, Runtime sections). Do not starve a thread the packs
  treat as substantial.

### Titles are reporting language

- Good: `时段工作介绍改为状态驱动采集` / `对话忙碌态不再显示 Online`
- Forbidden as headings: filesystem paths, repo folders, package directories
  (`packages/views`, `/home/jian40/…`), a branch name alone (`feat/foo`).
- A path may appear **inside** a bullet as evidence, never as the `###` title.

### Mermaid is required when the pack has it

Collector packs often include flow/sequence/state diagrams **because a
colleague needs them**. If a **ready** pack has a Mermaid fence that explains
a main thread:

1. Copy that ` ```mermaid ` block into **that thread** in the Brief.
2. Do not drop diagrams. Do not leave them only in 本机未归类.
3. Overlapping packs → keep the clearest one. You may tighten node labels;
   do not invent topology.
4. If a diagram is unscoped leftover, it may sit under 本机未归类 — that is
   the exception, not the default.

### Density

| Too detailed (cut) | Too thin (expand from packs) |
| --- | --- |
| Raw diffs, commit lists, every dirty path | A thread that is only a path or one vague bullet |
| Collector `## Runtime` / `## Repos / roots` dumps | Packs have Highlights/summary but the Brief skips the claim |
| Parallel bullets that are the same initiative | Missing Mermaid that the pack already drew |

## Status board (in the wake / draft)

Each collector has:

| field | meaning |
|-------|---------|
| `status` | `ready` / `failed` / `empty` / `cancelled` / `stalled` |
| `retryable` | platform verdict — trust it |
| `abandon_why` | why retry is forbidden |
| `detail` | error / stall text |
| `retry_count` | retries already used (max 3) |

`ready` includes a collector that already proposed `--note-write` even if the
job later failed (Pi/OpenAI `input[n].status` 400). Use that pack; do not
retry that collector.

## When to abandon (do not retry)

Permanent problems — re-running will fail the same way until a human fixes config:

- Missing / unset **API key** or model provider config (`No API key`, missing config)
- Provider **auth / access** (401/403, invalid key)
- **Model not found** / unavailable
- **Quota / billing** locks
- Agent **blocked**
- Runner **missing / unsupported version**
- `retryable: false` on the board (always honor this)

For abandoned collectors: write the Brief from Facts + ready packs, and add a
short “未纳入采集” note naming the machine and reason. **Never invent** that
machine’s OS work.

## When to retry (narrow tool only)

Transient / recoverable:

- `runtime_offline` / daemon disconnect
- Network / capacity / provider 5xx
- `empty` pack (finished without `--note-write`)
- `stalled` (safety ceiling) when still `retryable: true`

Call **once** per wake when needed:

```bash
multica notes period-brief retry-collectors \
  --draft-page-id <draft page id from wake> \
  [--collector-agent-id <id>]...
```

Rules:

1. Only retry collectors with `retryable: true` and `retry_count < 3`.
2. Prefer listing specific `--collector-agent-id` values; omit to retry all eligible.
3. Platform **rejects** permanent failures and over-cap retries — do not argue.
4. After a successful retry response: **stop and wait**. Platform re-wakes you
   with an updated board. Do not invent packs while waiting.
5. Never re-collect the OS yourself unless the wake explicitly makes you a collector.

## Deliver the Brief

Same as wake instruction: `--note-write` onto the **工作介绍/** folder page id
(not the draft). Body = Brief markdown only.

Source map: `references/period-work-brief-source-map.md`
