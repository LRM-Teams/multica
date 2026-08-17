---
status: accepted
supersedes: docs/adr/0018-machine-work-journal-period-brief.md
---

# Runtime Agents collect machine work; a dedicated Brief Agent synthesizes

A Period Work Brief remains a Notes narrative for colleagues or a manager —
not an activity dump and not a PPT file. Collection and synthesis are split:

1. **Collectors** — Agents on runtimes the human selects (local and cloud).
   Each collector gathers recent work on the OS where that runtime runs
   (whole-machine HOME for local hosts; the runtime environment for cloud).
2. **Synthesizer** — One dedicated Workspace Agent (e.g.「周报」) that reads
   platform Facts plus all collector packs and writes the Brief into Notes.

This supersedes ADR 0018's **Host Digest** path. Computer Host no longer
silently harvests Work Digests for Period Work. Agents collect; one Agent
summarizes. Platform Facts stay server-side and deterministic. The
retrospective API still does not run a model.

## Detail level (collectors)

Collectors **may** include short diffs, file summaries, and key file
snippets when needed to explain work. They still **must not** stream
keymouse, screenshots, clipboard, browser history, full repo dumps, secrets
(`.env` / `.ssh` / keys), or runtime diagnostics into the model context.
Denylist paths remain excluded. Prefer bounded excerpts over whole files.

## Selection

The human picks which runtimes / Agents collect for a window. Cloud runtimes
are allowed; they report the cloud OS environment, not the owner's laptop.
Empty selection is invalid for the machine-work channel (platform Facts alone
may still degrade if the product allows it — default is to require at least
one collector when machine work is requested).

## Synthesis

A Workspace-provisioned **Period Brief Agent** (weekly-report specialist) is
the default synthesizer. The human may override to another Agent. Synthesis
uses Note Worker + `--note-write` under `工作介绍/`; humans confirm before
body lands. Collector packs are untrusted partitions in the wake prompt
(same escape rules as note/facts/digest).

## Rejected alternatives

- Host-only Journal Digest as the machine source of truth for Briefs
- Single Agent that both collects on one machine and is the only narrator
  without an explicit multi-collector step when the human selected multiple
- Exporting `.pptx`
- Putting an LLM inside `POST /api/notes/retrospectives`
