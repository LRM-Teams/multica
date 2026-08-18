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

Collectors **may** include short diffs, file summaries, and key file
snippets when needed to explain work. They still **must not** stream
keymouse, screenshots, clipboard, browser history, full repo dumps, secrets
(`.env` / `.ssh` / keys), or runtime diagnostics into the model context.
Denylist paths remain excluded. Prefer bounded excerpts over whole files.

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

## Rejected alternatives

- Host-only Journal Digest as the machine source of truth for Briefs
- Using arbitrary specialty Agents as default collectors (persona fight /
  incomplete HOME coverage)
- Single Agent that both collects on one machine and is the only narrator
  without an explicit multi-collector step when the human selected multiple
- Exporting `.pptx`
- Putting an LLM inside `POST /api/notes/retrospectives`
