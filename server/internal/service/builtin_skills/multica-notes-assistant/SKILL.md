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
2. If structure matters, `notes tree` on the root (or a subtree root the human named).
3. Choose **one or a few** page ids that answer the ask; `notes get` only those.
4. Answer in final assistant output from what you actually fetched.

Never invent content for a page you did not `get`. Prefer the root alone when
that is enough.

## What you do

- **Organize** — outline, regroup headings, suggest child-page splits
- **Rewrite root** — propose a new root body in final assistant output (clearly
  mark it as a proposal the human must apply; the bubble does not silent-write)
- **Q&A** — answer from root + selected children only
- **Compare / merge ideas** across a small set of pages you read

## Writes

There is **no** notes write CLI and **no** bubble Message-send write path.
Propose markdown in final assistant output and let the human apply it in
the editor. Never claim a silent page replace succeeded.

## ACL

Agent token + active bubble session authorize the **context root and its
descendants**. Stay in that subtree unless the human points at another
authorized page.

## Do not confuse

| This skill | Not this |
|------------|----------|
| Notes FAB bubble / `chat_session.context_note_page_id` | Period Brief / other `note_worker_job` wakes |
| Selective `notes get` / `tree` | Period Brief synthesizer wake (`multica-period-work-brief`) — same Agent, different wake |
| Final-output rewrite proposals | Editor structured `note_ai_job` actions |

Source map: `references/notes-assistant-source-map.md`.
