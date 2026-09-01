---
name: multica-period-work-brief
description: "Use when the Notes Assistant (笔记助手) is woken as the 写汇报 synthesizer from platform Facts and collector packs. Covers audience-facing reporting (no evidence layer), honoring optional human <focus>, fixed section shape (Summary with Work Summary + Next Steps; optional Technique / Achievements / Research), titles, Mermaid for intuition, status board, abandon vs one Notes-Assistant retry-collectors call, and --note-write delivery under 工作介绍/. Do not use for Notes FAB bubble chat (multica-notes-assistant), collect-plan command (multica-period-work-plan), or collecting OS work (multica-period-work-collect)."
user-invocable: false
allowed-tools: Bash(multica *)
---

# Period Work Brief synthesizer

You turn **platform Facts** + **collector packs** into one Period Work Brief
**for other people to read** (manager / colleague). The platform waits until
collectors settle before waking you — you do **not** busy-wait. Inbox will
**not** auto-retry. There are two synthesizer wakes: a **retry-only** wake
(`retry-collectors` once, then stop — no `--note-write`) and a later **write**
wake (results are final; narrate for humans). Do not paste packs or show how
you verified anything.

## Audience (non-negotiable)

This note is a **对外稿给别人看** — strong structure, intuitive skim path,
plain reporting language. Readers should understand what changed and why it
matters **without** seeing engineering forensics.

Facts and collector packs are **private source material**. Digest them; do
not reprint their evidence layer.

**Notes pages are not a source.** Do not `notes` read / search / quote
workspace notes (current page, 工作介绍 drafts, or any other page) to fill
the Brief. Platform Facts for 写汇报 are Issues/PRs and real agent runs
only — never `touched_notes`, never previous 写汇报 / collector
`note_worker` wakes.

If the wake includes a `<focus>` partition (human request and/or planner
summary), **honor that scope** when integrating. Cover the requested paths,
topics, or aspects. Do not pad the Brief with unrelated pack material just
because a collector mentioned it.

## Reporting shape (non-negotiable)

### STRICT TIME WINDOW

Narrate **only** work inside the wake window (platform Facts timestamps +
collector claims dated in that half-open range). Do not widen to earlier or
later history to “complete” a story. If a pack cites out-of-window commits,
ignore them.

### No evidence layer in the Brief

**Forbidden in the Brief body:**

- Commit hashes, `git` output, diffs, patches, file snippets
- Labels like `evidence:` / 「证据」 / “as proven by”
- Collector `## Runtime` / `## Repos / roots` dumps, dirty-path lists
- Explaining *how* you know something (verification prose)

**Allowed:**

- Human titles and outcome claims
- Nested bullets on decision / impact / leftover risk
- Optional Issue/PR **identifiers as references** (e.g. `MUL-123`) — not as
  proof blocks
- Mermaid that makes a flow intuitive for a colleague

### Fixed sections (English headings)

```markdown
# 工作介绍 <window label>

## Summary

### Work Summary
…

### Next Steps
…

## Technique
…   <!-- omit entire section if no related work -->

## Achievements
…   <!-- omit entire section if no related work -->

## Research
…   <!-- omit entire section if no related work -->
```

- **`## Summary` always required.** It contains exactly:
  - **`### Work Summary`** — **priority**. Detailed **reader-facing** account
    of what mattered in the period (see grouping below).
  - **`### Next Steps`** — plausible follow-ups inferred from current work
    and unfinished threads; label speculation honestly.
- **`## Technique` / `## Achievements` / `## Research`** — include **only**
  when Facts + ready packs have related work. If none, **omit the section**
  (no empty heading, no “N/A”).

### Work Summary grouping

**Start from collector ## Work groups.** Then refine for readers.
**Group by initiative identity**, not by clock.

- Each collector Work group → **one main titled thread** under Work Summary.
  Nest different work inside that group as **sub-bullets / nested sub-points**.
- Trust collector defaults: same-repo/project groups, and cross-repo groups
  the collector marked related (read their **why**).
- Merge groups **across collectors** only when they share the same initiative
  identity / outcome / Issue. Do **not** invent merges of unrelated groups.
- Never split one collector group by calendar. Interleaved moments stay one
  thread (e.g. product change + collaboration copy for the same flow).
- Unrelated initiatives stay **separate** even if they shared a day or a
  machine. Never write “另一条主线是 A，同时 B…” when A and B have no shared
  outcome (e.g. stock screening vs Docker image automation).
- Self-check before delivery: for every top-level thread, ask (1) would a
  reader name this as **one** piece of work? (2) did I glue two unrelated
  products with “和/同时/另外”? If (2) is yes, split. If two threads answer
  the same initiative question, merge.
- Each thread: **human main title** + 1–2 sentence claim + **nested bullets**
  (decision, impact, remaining risk) a non-author can follow. Skip trivia.
- Delegated leverage (agents / teammates) belongs inside Work Summary when
  relevant — still in plain language.
- Abandoned collectors: short “未纳入采集” note with machine + reason —
  **never invent** that machine’s OS work. Do not attach evidence dumps.

### Titles are reporting language

- Good: `时段工作介绍改为状态驱动采集` / `对话忙碌态不再显示 Online`
- Forbidden as headings: filesystem paths, repo folders, package directories
  (`packages/views`, `/home/jian40/…`), a branch name alone (`feat/foo`).
- Prefer **no paths** in the Brief body. Ground claims in product language,
  not filesystem breadcrumbs.

### Mermaid for intuition (when the pack has it)

If a **ready** pack has a Mermaid fence that helps a colleague understand a
flow:

1. Copy that ` ```mermaid ` block next to **that work**.
2. Tighten labels for readability; do not invent topology.
3. Do not drop useful diagrams. Overlapping packs → keep the clearest one.
4. Diagrams are for **intuition**, not for dumping graph evidence.

### Density

| Cut from the Brief | Expand from packs into narrative |
| --- | --- |
| Raw diffs, commit lists, dirty paths,「证据」 | Vague Work Summary with no outcome claim |
| Collector Runtime / Repos dumps | Packs have substance but Brief skips the story |
| Empty Technique/Achievements/Research stubs | Missing Mermaid that would help a reader |
| One bullet gluing unrelated initiatives | Same initiative split into time-sliced siblings |

## Status board (in the wake / draft)

Each collector has:

| field | meaning |
|-------|---------|
| `status` | `ready` / `failed` / `empty` / `cancelled` / `stalled` |
| `retryable` | platform verdict — trust it |
| `abandon_why` | why retry is forbidden |
| `detail` | error / stall text |
| `retry_count` | assistant retries already used (max 1) |

`ready` includes a collector that already proposed `--note-write` even if the
job later failed (Pi/OpenAI `input[n].status` 400). Use that pack; do not
retry that collector. Failed collectors without a pack: say the collector
call failed — do not invent OS work.

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
- `empty` pack only when the board says `retryable: true` (completed
  turn still carried an error). Clean complete + no pack is settled
  empty — write the Brief; do not retry
- `stalled` (safety ceiling) when still `retryable: true`

Call **once** per wake when needed:

```bash
multica notes period-brief retry-collectors \
  --draft-page-id <draft page id from wake> \
  [--collector-agent-id <id>]...
```

Rules:

1. On a **retry-only** wake (instruction says do not `--note-write`): if any
   collector is `retryable: true` and `retry_count` is 0, you **MUST** call
   this CLI once now and **stop**. Do not write the Brief.
2. Prefer listing specific `--collector-agent-id` values; omit to retry all eligible.
3. Inbox will **not** auto-retry collectors. Platform rejects permanent failures
   and a second retry — do not argue.
4. After a successful retry response: **stop and wait**. Platform re-wakes you
   for the **write** wake when that attempt settles. Then the result is final —
   write the Brief; do not retry again.
5. On a **write** wake, do **not** call retry-collectors. Deliver the Brief.
6. Never re-collect the OS yourself unless the wake explicitly makes you a collector.

## Deliver the Brief

Only on the write wake: `--note-write` onto the **工作介绍/** folder page id
(not the draft). Body = Brief markdown only. Retry-only wakes must not write.

Source map: `references/period-work-brief-source-map.md`
