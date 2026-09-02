---
status: accepted
supersedes: docs/adr/0018-machine-work-journal-period-brief.md
---

# Runtime Agents collect machine work; a dedicated Brief Agent synthesizes

> **Amendment (2026-08-24):** Failure no longer shares a write wake. If a
> collector still owes the one assistant retry, the platform wakes **retry-only**
> (`notePeriodBriefRetryInstruction` — no `--note-write`). After that attempt
> settles, a separate write wake delivers the Brief. Harvest ignores folder
> `note_write` before that write wake. `submit-pack` patches one collector via
> `jsonb ||` so two machines cannot clobber each other; await counts a pack only
> when `pack_job_id` matches this job. 「材料齐了」 requires at least one ready
> pack. Composer unlocks when the insert card is posted (`awaiting_confirm`) —
> the human may discard the Brief without inserting. Satellite `Write
> report` / `Report` is period-brief intent. FAB create omits `chat_session_id`
> so progress does not append onto an old Q&A thread.
>
> **Amendment (2026-08-21):** The bubble result card must show **this run**.
> Do not harvest the latest `note_write` on `工作介绍/` — that is usually
> last week's brief, posted the moment the synthesizer is woken. Scope
> harvest to writes with `created_at >= run.created_at`, wait for that
> write before posting 「汇报稿整理完成了」, and fall back to **this run's
> draft** (current packs), never a prior folder write.
>
> **Amendment (2026-08-21):** 写汇报 is composed in the Notes assistant
> bubble (satellite → chips for day/week/month/custom and owned computers,
> plus the bubble composer). There is no header button and no dedicated
> dialog. Typed text wins when it conflicts with chips. After dispatch,
> stay in the bubble — do not auto-open Messages.
>
> **Amendment (2026-08-21):** Optional human `focus` on 写汇报. Empty focus
> keeps today's full-scope collect. Non-empty focus wakes 笔记助手 first as
> collect-plan commander (`submit-collect-plan`); it restates paths/topics/
> aspects and assigns only the needed roster collectors. Collectors then
> harvest that scope. After packs settle, the same 笔记助手 synthesizes and
> honors `<focus>`. One Agent, three wakes (Notes FAB bubble /
> collect-plan / synthesizer).
>
> **Amendment (2026-08-20):** Synthesizer identity is the Workspace Notes
> Assistant (「笔记助手」 / `notes-assistant`). 「周报」 / `weekly-report` is
> retired and archived on Ensure. Collectors are unchanged. One Agent, two
> wakes (Notes FAB bubble vs `force_fresh_session` 写汇报).
>
> **Amendment (2026-08-20):** Collector scan roots are `SCAN_ROOTS` = `$HOME`
> ∪ `/workspace` (when that directory exists) ∪ other visible project dirs
> outside agent-private `.multica`. HOME-only is incomplete on container
> sandboxes. Non-git in-window source files are evidence.
>
> **Amendment (2026-09-01):** Collect recipes share one harvest (git probe +
> file mtime). Only `SCAN_ROOTS` / denylist are OS-shaped. **Do not
> deep-scan `$HOME`** (skip `AppData`, `Library`, `Downloads`). Roots are
> first-level project parents (`code` `src` `work` `Documents` `Desktop`
> / `文档` `桌面`), `/workspace`, HOME symlink children, Windows
> first-level folders of those names on other volumes, and shallow Linux
> `/opt` `/srv` git. Non-git harvest includes office/notes (`txt` `docx`
> `xlsx` `pptx` `pdf`). Cheap git probe first; idle repos (0 in-window
> commits, clean tree) are recorded, not diff-dumped. Do not prune on
> parent-directory mtime.
>
> **Amendment (2026-09-01):** A collector job that **completes without an
> error** and without `submit-pack` is a **settled empty pack**, not a
> failure. Do not ask the Notes Assistant to re-collect. Hand the empty
> slot to synthesis (`status=empty`, `retryable=false`). Retry remains
> for `failed` / `stalled` and for a completed turn that still carried
> an error string.
>
> **Amendment (2026-09-02):** Each owned Computer may declare Period Work
> collect roots in `~/.multica/computer/period-brief-collect-roots.json`.
> The Computer file is authoritative. Empty / missing keeps today's
> heuristic `SCAN_ROOTS`. A non-empty list **replaces** the heuristic —
> collectors must not add HOME parents, `/opt`, or other extras. The
> collector setup UI (create and reopen on an existing collector) writes
> the file through the live Computer; `multica computer collect-roots
> --print` and the `<scan-roots>` wake partition are how collectors read
> it. Collectors must not walk `.multica`. denylist, OWN COMPUTER, STRICT
> WINDOW, and per-run `<focus>` still apply inside those roots.
>
> **Amendment (2026-09-01):** “Other visible project dirs” on Linux includes
> one level of HOME symlink children, common parents (`~/go`, `~/repos`,
> `~/src/github.com`, …), and shallow git roots under `/opt` / `/srv` /
> `/usr/local/src` — not those parents as whole-tree scan roots. Git harvest
> uses `--all` (not local branches only) and keeps porcelain-dirty paths
> when mtime is outside the window (labeled). Non-git names include shell,
> yaml, Docker/Make, Terraform, and Nix. Still no browser, journal, Docker
> daemon, or other-machine harvest.

A Period Work Brief remains a Notes narrative for colleagues or a manager —
not an activity dump and not a PPT file. Collection and synthesis are split:

1. **Collectors** — One dedicated Agent **per Computer**:
   - **Local** — keyed by `daemon_id` (multiple local runtimes on the same
     machine share one collector). Display: `采集 · <label>`.
   - **Cloud** — one collector per cloud runtime. Display:
     `采集 · 云端 · <label>` so humans never confuse it with a laptop.
  Opening Period Work Brief ensures collectors exist for each Computer the
  member **owns** (never another member's machine, even when that runtime is
  visible or `public`).   Collectors gather recent work on that OS
  (`SCAN_ROOTS`: first-level project parents, never a deep HOME / AppData
  walk, plus `/workspace` and OS extras — see collect-recipes).
  The collector UI lists **only** these owned-Computer Agents.
2. **Synthesizer** — The Workspace Notes Assistant (「笔记助手」 /
   `notes-assistant`) in its 写汇报 wake. Leftover 「周报」 /
   `weekly-report` agents are archived on Ensure. It reads platform Facts
   plus all collector packs and writes the Brief into Notes.

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
key snippets). In addition they **must**:

1. **Preliminary Work groups** — classify traces for *this machine* under
   `## Work groups` without dropping underlying evidence:
   - Same git repo / project root → one group by default.
   - Different repos, files, or surfaces that share one outcome / initiative
     → **one** group, with an explicit **why**.
   - Unrelated work → separate groups (never glue by calendar).
   Completeness beats clever compression: Highlights and repo lines remain
   the evidence layer; Work groups organize them for the synthesizer.
2. **Local-context diagrams** — when a multi-step flow, dependency, or state
   change is hard to narrate from bullets alone, include a **Mermaid**
   flowchart / sequence / state diagram in the pack. Diagrams are optional and
   only when they clarify; never decorative, never a substitute for evidence.

### Pack delivery (not Notes pages)

Collectors deliver packs with
`multica notes period-brief submit-pack --draft-page-id <draft>`.
The platform stores markdown on `note_period_brief_run.collectors[].pack_markdown`
(implicit artifact). **Do not** create「采集包」Notes pages and **do not**
`--note-write` the pack into Notes. After the synthesizer is woken with the
packs text, the platform **purges** `pack_markdown`.

They still **must not** stream keymouse, screenshots, clipboard, browser
history, full repo dumps, secrets (`.env` / `.ssh` / keys), or runtime
diagnostics into the model context. Denylist paths remain excluded. Prefer
bounded excerpts over whole files. Collectors still **must not** write the
final Period Work Brief — that remains the synthesizer's job.

## Selection

The human picks among the provisioned per-Computer collectors **on
Computers they own**. Empty selection is invalid for the machine-work
channel. Default: all **online** collectors on the caller's Computers.

**Hard ownership boundary:** Period Work collection is Computer-owner-only.
Seeing another member's runtime in the workspace (including `public`
visibility) or holding workspace owner/admin does **not** authorize
provisioning or selecting a collector for that machine. Ensure and
`collector_agent_ids` both fail closed on foreign Computers.

The human picks a **time window**: calendar day / ISO week / calendar month
(anchor date), or a **custom inclusive** start→end date range in the viewing
timezone (half-open UTC internally). Collectors and synthesis **must** stay
strictly inside that window.

## Synthesis

The Workspace **Notes Assistant** is the synthesizer (写汇报 wake,
`force_fresh_session`). The human cannot pick another Agent. Synthesis uses
Note Worker + `--note-write` still produces the Brief body. When the human
starts from a Notes bubble, progress is narrated in that page's
`chat_session`. Collector packs and the finished brief appear in that
bubble as collapsed `note_brief` cards. The brief card offers **插入笔记下面**
(append onto the issuing page) and **插入子笔记** (child of that page), not
under global `工作介绍/`. Collector packs are untrusted partitions in the
wake prompt (same escape rules as note/facts/digest).

### Collector settle + status board

The platform **waits until each collector settles**. A pack is **ready** when
`note_period_brief_run.collectors[].pack_markdown` is non-empty (via
`submit-pack`) — including when the inbox task later fails
(`api_invalid_request` / Pi `input[n].status` 400). It does **not**
treat “N minutes elapsed while still running” as empty. An absolute safety
ceiling only marks remaining runners as **stalled**. After the synthesizer
wake, the platform **clears** `pack_markdown` (ephemeral artifact).

Each collect, retry, and synthesizer wake sets `force_fresh_session=true`.
These are one-shot prompts: they must not resume a prior Pi conversation.

The synthesizer wake includes a **status board** per collector (`status`,
`retryable`, `abandon_why`, `detail`, `retry_count`). Permanent failures
(missing API key / model config / auth / quota / blocked / unusable runner)
are `retryable=false` — abandon and narrate gaps; do not invent OS work.

The Brief is a **汇报稿 for other people** — strong structure and plain
reporting language, not a pack paste and **not an evidence log**:

- Fixed English sections: always `## Summary` with `### Work Summary`
  (detailed, reader-facing) and `### Next Steps`; optionally `## Technique`,
  `## Achievements`, `## Research` — **omit** any of those three when
  Facts+packs have no related work (no empty stubs).
- Inside Work Summary: **Start from collector ## Work groups.** Each group
  becomes one main titled thread; nest different work as sub-points. Merge
  across collectors only for the same initiative identity. Never invent
  merges of unrelated groups; never split one group by calendar order.
  Nest sub-points under one thread for the same work even when moments
  interleaved. Never merge unrelated initiatives into one bullet just
  because they shared a window or machine.
- Thread titles are human reporting language — never a filesystem path or
  package directory as a heading. Prefer no paths in the body.
- **No evidence layer** in the Brief (no commit hashes, diffs, snippets,
  or「证据」). Facts/packs stay private source material.
- When a ready collector pack includes Mermaid that helps a reader, the
  synthesizer **must** carry that diagram into the Brief (intuition, not
  forensics).

Skill: `multica-period-work-brief`.

### Narrow retry (synthesizer tool)

For transient failures (`runtime_offline`, network/capacity, retryable
stalled, or empty-with-error), the synthesizer may call:

`multica notes period-brief retry-collectors --draft-page-id <draft>`

Platform enforces: skip permanent failures; **exactly one** Notes-Assistant
retry per collector; inbox does not auto-retry. After that attempt settles
(ready or failed), the collector result is received and the synthesizer
writes the Brief. Skill: `multica-period-work-brief`.

## Rejected alternatives

- Host-only Journal Digest as the machine source of truth for Briefs
- Using arbitrary specialty Agents as default collectors (persona fight /
  incomplete HOME coverage)
- Single Agent that both collects on one machine and is the only narrator
  without an explicit multi-collector step when the human selected multiple
- Exporting `.pptx`
- Putting an LLM inside `POST /api/notes/retrospectives`
