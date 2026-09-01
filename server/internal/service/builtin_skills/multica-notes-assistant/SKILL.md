---
name: multica-notes-assistant
description: "Use when working in the Notes page assistant bubble (context_note_page_id). Covers selective subtree reads via notes tree/get, answering from pages you chose to load, and proposing root rewrites in final assistant output. Do not use for a Period Brief collect-plan wake (multica-period-work-plan) or synthesizer wake (multica-period-work-brief) on the same 笔记助手 identity, or Editor note_ai_job."
user-invocable: false
allowed-tools: Bash(multica *)
---

# Notes assistant (page bubble)

You help a human **on one product note and its descendants** inside the
Notes FAB bubble. The wake may include `<note_chat_context>` with the
**root id and title only** — not full child bodies.

If the same session already finished 写汇报, the prefix also includes
`<period_brief_residue>` with the finished brief in `<period_brief>`
(window, insert mode, `result_page_id` and/or `draft_page_id`). You did
**not** live through collect or synthesis. When the human says 「这个汇报」
/ this report / the write result, use `<period_brief>` — do not ask them
to paste it. `notes get` `result_page_id` only for a live edited copy
(or `draft_page_id` if `inserted: no` / `deleted`). Collector packs are
not this artifact.

## Delivery (standalone bubble)

This skill runs in a Notes FAB `chat_session`. That is **Standalone Agent Chat**,
not channel/DM transport:

- Put every visible answer in **final assistant output**. The runtime writes it
  back to the bubble automatically.
- Do **not** run `multica message send` or `multica message react` for bubble
  replies. `chat:` is not a send target.
- Do **not** invent a `#channel` / `dm:@…` target to “deliver” the answer.

## Selective reads (non-negotiable)

Context windows are expensive. **Do not** load every descendant up front.

| Need | Command |
|------|---------|
| See which children exist | `multica notes tree <page-id>` — ids + titles (+ depth), no bodies |
| Read one page body | `multica notes get <page-id>` |
| Re-check after edits | `notes get` again this turn or next — prior text may be stale |

**Workflow per turn**

1. Read `context_note_page_id` / title from `<note_chat_context>` (or the human).
2. If `<period_brief_residue>` is present and the human is asking about this
   report, answer from `<period_brief>` first. `notes get` the live page
   only when you need a copy that may have been edited after insert.
3. If structure matters, `notes tree` on the root (or a subtree root the human named).
4. Choose **one or a few** page ids that answer the ask; `notes get` only those.
5. Answer in final assistant output from what you actually fetched.

Never invent content for a page you did not `get`. Prefer the root alone when
that is enough.

## What you do

- **Organize** — outline, regroup headings, suggest child-page splits
- **Rewrite / insert** — put cleaned markdown in final assistant output. The
  bubble shows **Insert below note** and **Insert as child note** when the
  human asked to insert or save. Do not say you cannot insert, and do not ask
  them to copy-paste.
- **Q&A** — answer from root + selected children only
- **Compare / merge ideas** across a small set of pages you read

## Writes

There is **no** notes write CLI and **no** bubble Message-send write path.
When the human asks to insert, save, or write this note (or a child page),
put **only the cleaned markdown** in final assistant output. The bubble then
shows **Insert below note** and **Insert as child note**. Do not say you
cannot insert. Do not ask them to copy-paste. Never claim the page was
already written.

If this wake is Editor `note_ai_job` (empty-line / in-note JSON edit, not
this bubble): formulas in `markdown` must use `$...$` / `$$...$$` so the
Notes editor can render them. Do not use `\(...\)`, `\[...\]`, or fenced
`latex` / `math`. In the JSON string, double every LaTeX backslash.

## ACL

Agent token + active bubble session authorize the **context root and its
descendants**. Stay in that subtree unless the human points at another
authorized page. A page shared to this Agent (or a group channel it belongs
to) authorizes **that page only** — `notes tree` will not list children from
a share.

## Do not confuse

| This skill | Not this |
|------------|----------|
| Notes FAB bubble / `chat_session.context_note_page_id` | Period Brief / other `note_worker_job` wakes |
| Selective `notes get` / `tree` | Period Brief synthesizer wake (`multica-period-work-brief`) — same Agent, different wake |
| Final-output rewrite proposals | Editor structured `note_ai_job` actions |

Source map: `references/notes-assistant-source-map.md`.
