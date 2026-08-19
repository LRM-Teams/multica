---
status: accepted
supersedes: docs/adr/0018-machine-work-journal-period-brief.md
---

# Runtime Agents collect machine work; a dedicated Brief Agent synthesizes

A Period Work Brief remains a Notes narrative for colleagues or a manager —
not an activity dump and not a PPT file. Collection and synthesis are split:

1. **Collectors** — One dedicated Agent **per Computer**:
   - **Local** — keyed by `daemon_id` (multiple local runtimes on the same
     machine share one collector). Display: `采集 · <label>`.
   - **Cloud** — one collector per cloud runtime. Display:
     `采集 · 云端 · <label>` so humans never confuse it with a laptop.
   Opening Period Work Brief ensures collectors exist for each Computer the
   member can bind Agents on. Collectors gather recent work on that OS
   (whole-machine HOME locally; the cloud runtime environment for cloud).
   The collector UI lists **only** these Agents.
2. **Synthesizer** — One dedicated Workspace Agent (「周报」 / `weekly-report`)
   that reads platform Facts plus all collector packs and writes the Brief
   into Notes.

This supersedes ADR 0018's **Host Digest** path. Computer Host no longer
silently harvests Work Digests for Period Work. Agents collect; one Agent
summarizes. Platform Facts stay server-side and deterministic. The
retrospective API still does not run a model.

## Per-Computer collector identity

- Permanent name: `period-collect-<slug>` (stable; Workspace-unique).
- Display name encodes machine kind (`采集 · …` vs `采集 · 云端 · …`).
- Instructions point at the always-on builtin skill
  `multica-period-work-collect` (shell recipes on the bound runtime).

## Detail level (collectors)

Collectors see the **fullest local picture** on that Computer. They **must**
still gather evidence (repos, commits, dirty paths, short diffs / summaries /
key snippets). In addition they **may and should**:

1. **Preliminary integration** — group traces into themes / threads for *this
   machine only* (an integrated summary section), without dropping underlying
   evidence. Completeness beats clever compression: Highlights and repo lines
   remain the evidence layer; the summary is additive.
2. **Local-context diagrams** — when a multi-step flow, dependency, or state
   change is hard to narrate from bullets alone, include a **Mermaid**
   flowchart / sequence / state diagram in the pack. Diagrams are optional and
   only when they clarify; never decorative, never a substitute for evidence.

They still **must not** stream keymouse, screenshots, clipboard, browser
history, full repo dumps, secrets (`.env` / `.ssh` / keys), or runtime
diagnostics into the model context. Denylist paths remain excluded. Prefer
bounded excerpts over whole files. Collectors still **must not** write the
final Period Work Brief — that remains the synthesizer's job.

## Selection

The human picks among the provisioned per-Computer collectors. Empty
selection is invalid for the machine-work channel. Default: all **online**
collectors.

## Synthesis

A Workspace-provisioned **Period Brief Agent** (weekly-report specialist) is
the default synthesizer. The human may override to another Agent. Synthesis
uses Note Worker + `--note-write` under `工作介绍/`; humans confirm before
body lands. Collector packs are untrusted partitions in the wake prompt
(same escape rules as note/facts/digest).

### Collector settle + status board

The platform **waits until each collector settles**. A pack is **ready** when
the note page has real content **or** the collector already proposed
`--note-write` onto that pack page — including when the inbox task later
fails (`api_invalid_request` / Pi `input[n].status` 400). It does **not**
treat “N minutes elapsed while still running” as empty. An absolute safety
ceiling only marks remaining runners as **stalled**.

Each collect, retry, and synthesizer wake sets `force_fresh_session=true`.
These are one-shot prompts: they must not resume a prior Pi conversation.

The synthesizer wake includes a **status board** per collector (`status`,
`retryable`, `abandon_why`, `detail`, `retry_count`). Permanent failures
(missing API key / model config / auth / quota / blocked / unusable runner)
are `retryable=false` — abandon and narrate gaps; do not invent OS work.

The Brief is a **reporting narrative** (提纲挈领), not a pack paste:

- Group by initiative/outcome; nest sub-points under one thread. Do not
  flatten the same work into sibling bullets.
- Thread titles are human reporting language — never a filesystem path or
  package directory as a heading.
- When a ready collector pack includes Mermaid that explains a thread, the
  synthesizer **must** carry that diagram into the Brief.

Skill: `multica-period-work-brief`.

### Narrow retry (synthesizer tool)

For transient failures (`runtime_offline`, network/capacity, empty pack,
retryable stalled), the synthesizer may call:

`multica notes period-brief retry-collectors --draft-page-id <draft>`

Platform enforces: skip permanent failures; **max 3 retries** per collector;
then re-wait and re-wake the synthesizer. Skill:
`multica-period-work-brief`.

## Rejected alternatives

- Host-only Journal Digest as the machine source of truth for Briefs
- Using arbitrary specialty Agents as default collectors (persona fight /
  incomplete HOME coverage)
- Single Agent that both collects on one machine and is the only narrator
  without an explicit multi-collector step when the human selected multiple
- Exporting `.pptx`
- Putting an LLM inside `POST /api/notes/retrospectives`
