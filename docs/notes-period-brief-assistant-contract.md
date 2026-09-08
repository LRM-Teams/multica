# Notes Assistant and 写汇报

**Status:** accepted product contract (2026-09-08). Replaces the
2026-09-07 same-session always-re-walk wording where it conflicts;
keeps the 2026-09-07 human-owns-scope / assistant-reads model.
**Implementation:** one flow — ambiguous speech matching 写汇报
(e.g. 「帮我写汇报」) first asks 「是 / 否」; 「是」 opens the plan
card; 「否」 answers the original ask normally. Exact 「写汇报」
(in-window quick action / short typed ask) opens the plan card
directly. The human then chooses range / 取消 / 开始采集; 开始采集
walks every selected computer then writes the brief from **this walk's**
packs.
Session harvests are read-only wake context and are **not** used for a
new report unless the human **explicitly** asks to write from a prior
harvest in this bubble. Plan and result cards are session state /
platform parts, not chat XML.
Acceptance is the human path (speech / one plan card / one result card),
not leftover fences.
**Does not supersede:** ADR 0019 (Computer-owner collectors, strict
window, ephemeral packs, synthesizer identity, Worker
`force_fresh_session`).

The platform intercepts 写汇报 and opens the plan card. The Notes
Assistant **reads** packs, the finished brief, and the current note
page, speaks progress, and inserts. It does **not** decide window,
computers, or focus, and it does not call start. The platform is the
**hard boundary**: ACL, collector provision, OS harvest, settle,
synthesizer write.

## Decisions (locked)

1. **One collect → brief pipeline.** 开始采集 always walks every
   selected computer, then the synthesizer writes the official brief
   from **this run's** packs only. Same-session packs are not reused
   to skip a walk. A plain 「写汇报」 / 「重新采集」 without naming a
   prior harvest must not treat last run's packs as source for the new
   brief.
2. **Explicit prior-harvest reuse (bubble only).** If the human
   clearly asks to generate report content from a prior harvest in
   this bubble (e.g. 用上次采集 / 用某次写汇报采到的), the assistant
   may use session materials for that ask. That is not a substitute for
   开始采集, and it does not invent a new result card without platform
   synthesis after a walk (or an explicit reuse path the platform adds
   later).
3. **The human owns collect scope.** Window, computers, focus, 取消,
   and 开始采集 live on the plan card. Ambiguous speech matching
   写汇报 (e.g. 「帮我写汇报」) soft-confirms first; 「是」 opens
   (or reopens) that card. Exact 「写汇报」 (quick action / short
   typed ask) opens the card directly. 「重新采集」 soft-confirms.
4. **The assistant reads, it does not collect.** It may call `plan`
   with only the session id when the card is missing, speak progress,
   and call insert. It does not PUT window / computers / focus and
   does not call start.
5. **Partial harvest still writes.** After the one allowed retry, if
   at least one selected computer is `ready`, the synthesizer writes a
   new official brief from ready packs only and speaks which computers
   failed. A new official brief is blocked only when **no** ready pack
   remains (all failed / stalled / cancelled / empty of usable
   harvest).
6. **Settle ceiling is 15 minutes.** Platform waits for real
   ready / failed / cancelled / empty. It does **not** fail a still-
   running collector early. Past 15 minutes, remaining runners are
   marked `stalled` (then retry / partial / missing-harvest rules
   apply).
7. **Lock means work is running.** Composer lock is
   `planning | collecting | synthesizing` only while a collector or
   synthesizer process is actually in flight. It is not “an HTTP
   request has not returned.” Orphan synthesizing after a dead request
   is a platform bug; settle or unlock, do not block the next start.
8. **The in-window 写汇报 button is a direct funnel.** It seeds exact
   「写汇报」, which opens the plan card (no soft-confirm). It is not
   a parallel “chips then POST” product.
9. **Insert may target any note** the human can write. Non-issuing-page
   targets still require a human confirm.

## What the human sees

1. **Talk.** Ask 写汇报, ask about a finished brief, say where to
   insert. Collect range is not decided in chat.
2. **One plan card.** Time range and computers for this run. **取消**
   dismisses the plan and stops any in-flight collect / synthesize on
   that note page. **开始采集** walks every selected computer, then
   writes the brief from this walk.
3. **One result card.** The brief plus insert actions. Progress is
   spoken by the assistant. While collectors or the synthesizer are
   in flight, the bubble keeps the same in-thread running indicator
   (status pill) as a normal assistant reply — not only the FAB stop
   pulse.

## Tools (bubble wake)

These are Notes-assistant bubble tools (CLI under `multica notes`).
They are **not** Worker collect-plan / synthesizer wakes.

| Tool | When | In | Out |
|---|---|---|---|
| `notes period-brief plan` | Plan card missing after 写汇报 | `chat_session_id` from `<note_chat_context>` only | the current plan (**creates** the visible card) |
| `notes period-brief insert` | Human said where to put a finished brief | `run_id`, target page, `append` / `child` | inserted, or `needs_confirm` when the target is not the issuing page |

Collect starts from the human plan card (`POST /api/notes/period-briefs`).
Do not invent fences to mean start, resynth, compose, or insert.
Do not walk anyone’s OS. Do not `--note-write` from the bubble.

Worker tools stay Worker-only: `submit-pack`, `submit-collect-plan`,
`retry-collectors`, synthesizer `--note-write`.

## Two kinds of session (do not collapse)

| Session | Continuity | Why |
|---|---|---|
| Bubble `chat_session` | Reuse from talk → plan → progress → brief → insert | One conversation |
| Collector / planner / synthesizer Worker | `force_fresh_session` + replace live Pi | One-shot prompts; resuming Pi reprints a prior brief |

Progress wakes are FAB standalone turns on the **bubble** session.
They must not resume a Worker Pi conversation.

## Plan

Complete when both are set:

- **Window** — day / week / month / custom range
- **Computers** — at least one owned Period Work collector (or an
  owned Computer the human is configuring). Foreign Computers stay
  forbidden (ADR 0019).

Typed composer text is optional `focus`. Empty focus means each
selected computer uses its collect-roots file; an empty file on that
machine falls back to heuristic `SCAN_ROOTS`. No global 「全部」 chip.

If a slot is missing, the card stays usable until the human fills it.
Configuring a missing collector is optional; the run may proceed with
the remaining owned collectors.

## Progress

The platform updates the run and wakes the assistant with a short
untrusted board (who settled, who is waiting). Pack bodies stay
collapsed cards. The assistant speaks one reminder. It does not
start synthesis. It does not emit chat XML.

**Silence fence:** if a progress wake produces no assistant row, the
platform may post one fallback line. That is recovery, not the happy
path.

## Brief, session harvests, insert

Synthesis is unchanged in shape: platform waits for collectors (≤15
minutes safety ceiling), then the write Worker. The finished brief
appears as the result card.

Session harvests (latest ready pack per owned computer in this
bubble) are **read-only** context the assistant can see. The materials
board labels each pack with name, id, os, and hostname so speech about a
machine can match a row without dumping pack bodies. They do not skip
the next walk. 「写汇报」 / 「重新采集」 in the same bubble reopens the
plan card; 开始采集 walks every selected computer and writes a new
official brief from **that walk only**. Only when the human
**explicitly** asks to use a prior harvest in this bubble may the
assistant draw on those packs for that ask. The result card (with
insert) is the official brief — not a chat markdown rewrite. A new
bubble session is a clean slate.

After collectors are final: if **any** selected computer is `ready`,
write a new official result card from ready packs and speak failures.
If **none** are ready after the one allowed retry, do **not** post a
new official result card; the previous brief (if any) stays; speak the
failure. Empty-scan detection looks at Highlights bullets, not a
substring anywhere in the pack.

After `awaiting_confirm` / `done`:

- Result-card actions **插入笔记下面** / **插入子笔记** (default:
  issuing page).
- Speech in the same bubble. The assistant resolves the page with
  `notes tree` / `notes get`, then calls insert. Ambiguous names →
  ask. Confirm when the target is not the issuing page.

Insert modes: `append` (heading + body under the target) and `child`
(new child). Global `工作介绍/` is not the default destination.

## Platform still owns

- Collector provision / Computer-owner gate / collect-roots file
- `submit-pack`, pack harvest, settle / **15-minute** stall ceiling
- Platform owns **one** collector retry after transient failure; inbox
  does not auto-retry and the Notes Assistant is not required to call
  `retry-collectors`. After that retry, **no ready pack** → no new
  official brief; **some ready** → new official brief from ready packs only
- Pack harvest is Highlights that are not an empty-scan bullet;
  a later “no in-window” sentence does not wipe a ready pack
- Speech 写汇报 opens the plan card (`tryHandlePeriodBriefPlanAsk`);
  exact 「写汇报」 skips soft-confirm.
  开始采集 walks every selected computer, then synthesizes from this
  run’s packs. Session harvests are not used to skip a walk and are
  not default source for a new report
- Lock only while a collector or synthesizer job is **actually running**.
  A written orphan is settled (`awaiting_confirm`); a dead lock with no
  live job is abandoned. Stop settles a written orphan instead of
  cancelling it. Plan-card **取消** and speech 取消 close the current
  plan and stop the page’s in-flight collect / synthesize
  (`DELETE /api/notes/period-briefs/plan`). Start / insert return after
  dispatch; posting the result card is the run’s job
- Synthesizer `--note-write` scoped to this run’s collector packs

## Assistant owns

- Answering from packs, the finished brief, and the current note page
- Opening the plan card with `plan` if it is missing
- Speaking each progress beat (including partial failures)
- Using session harvests for a new report **only** when the human
  explicitly asks to write from a prior harvest
- Resolving an insert target from speech

## Non-goals

- Chat XML as the dispatch protocol (`compose` / `plan` / `start` /
  `started` / `resynth` / `insert_propose`)
- Notes Assistant walking anyone’s OS
- Sharing one Pi conversation across FAB progress and the synthesizer
- Auto-insert without confirm when the target is not the issuing page
- Removing the FAB satellite
- Compatibility layers for the old fence intercepts

## Delete with the change

When a tool or shared plan replaces a fence path, delete that path in
the same change: intercept effects, fence parsers, leftover routes,
skill lines, and tests that only existed for the old wire. Do not
leave dual-write or “still accept the old marker.”
