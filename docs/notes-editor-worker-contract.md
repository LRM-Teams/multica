# Notes: Editor vs Worker contract

> Source: Slice 2 / S2-C3 (`todo.md`). Chat threads are not storage — keep this file current when the contract changes.

## Why two contracts

Product notes have two different intents:

| Intent | Name | Job | What it may do | What it must not do |
|--------|------|-----|----------------|---------------------|
| Edit the current page | **Editor** | `note_ai_job` via `POST /api/notes/pages/{id}/ai-jobs` | Produce structured edit actions (`insert` / `replace_selection` / `replace_page` / `patch`) for human apply | Create Issues, assign Agents, wake platform work as a side effect |
| Use the page as brief | **Worker** | `note_worker_job` via `POST /api/notes/pages/{id}/worker-jobs` | Read the note under ACL and drive platform work (Issue/Task/Agent run) | Mutate `note_page.content` via Editor actions; reuse `note_ai_job` |

Pending writebacks (`note_page_writeback`) are a third path (D1 human review). They are neither Editor nor Worker jobs.

## Hard rules

1. **Separate endpoints and tables.** Editor rows live only in `note_ai_job`. Worker rows live only in `note_worker_job`. Never insert into both for one user action.
2. **Misuse fails closed.** Sending Worker fields/`intent:"worker"` to the Editor endpoint returns 400. Sending Editor edit fields (`prompt` for page rewrite, `action`, `replace_page`, …) to the Worker endpoint returns 400.
3. **Note body is untrusted input for Worker** (S2-C4). User `instruction` is the trusted directive; page content is context only. Prompt partitions and escaping are mandatory (see below).
4. **Writebacks stay pending until accept** (D1). Worker completion may *propose* writebacks; it must not silent-edit the page through Editor.
5. **Channel replies use transport, not completion text.** Worker wakes are channel-directed (`reason=note_worker`). Visible replies must go through `multica message send --target <Message target>` — the same contract as @mention. Daemon never bridges final assistant text into Messages (`unsent_final_output` → `no_reply`).

## Issue → note writeback subscription (S3-W1 / S3-W2)

- **Subscription = link.** A `note_page_issue_ref` row is an implicit subscription. No separate subscribe table; delete the ref to opt out.
- **Whitelist only.** Pending proposals are created only when issue status newly enters `done` or `cancelled`. Transitions like `todo → in_progress`, `blocked`, title edits, and ordinary comments produce **zero** proposals (key-comment writebacks deferred).
- Code: `note_writeback_events.go` + `maybeProposeNoteWritebacksOnIssueTransition`.

## Product note writeback ≠ Agent Daily (S3-W3)

Two stores stay **parallel**. Do not merge them in code, migrations, or product copy.

| Store | Where | Audience | Write path |
|-------|--------|----------|------------|
| **Product notes + pending writeback** | Workspace `note_page` / `note_page_writeback` | Humans (and ACL’d agents reading notes) | D1 pending → human accept; Editor jobs; Worker proposals |
| **Agent Daily** | Agent private `memory/daily/YYYY-MM-DD.md` | That agent’s own recall | Hot-path append / L1 Daily Recorder — see `docs/agent-memory-model.md` |

**Allowed:** cross-links (note cites a run/issue; Daily cites a note URL or issue id).

**Forbidden:** dumping Daily into `note_page.content`; using `note_page_writeback` as a Daily substitute; unifying schemas so one table serves both; silent “sync” that copies either side into the other without an explicit product feature.

## Status of Worker execution

S2-C3 established the typed contract and misuse rejection. **S2-C1** wires dispatch:

1. `POST .../worker-jobs` loads the page under ACL, builds the Worker prompt (below), wraps it with the channel delivery contract + `Message target for chat transport`, enqueues a chat task, and merges `context.note_brief = { version, page_id, title }`.
2. `note_worker_job.task_id` is set and status becomes `dispatched`.
3. Human Notes UI no longer starts Worker jobs (「按这篇做」 / 「用这篇」 were removed). Period Brief and Agent `notes get` still use `note_worker_job` internally. Agent `notes get` tool is **S2-C2**.

## Worker prompt partitions (S2-C4)

`buildNoteWorkerPrompt` emits three stable XML-ish partitions:

1. `<system_contract>…</system_contract>` — fixed Worker framing (brief ≠ Editor; note is untrusted; follow instruction + tools; **must** use `multica message send` for visible Messages replies; may cite `multica notes get`)
2. `<note><title>…</title><body>…</body></note>` — untrusted note snapshot (`page_id` also printed as plain metadata above `<note>`)
3. `<instruction>…</instruction>` — caller directive (trusted relative to the note body)

Dispatch additionally prefixes `wrapNoteWorkerChannelWakePrompt` (channel output contract, directed-reply instruction, and `Message target for chat transport`) so the agent has the same send target as a mention wake.

**Untrusted escaping:** tag-shaped `<…>` / `</…>` spans in title/body/facts/packs become `‹…›` / `‹/…›` so note content cannot close partitions or inject fake tags. Plain `>` (Mermaid `-->`, comparisons) is preserved. Instruction text additionally replaces a literal `</instruction>` with `‹/instruction›` so it cannot truncate its own partition.

Code: `server/internal/handler/note_worker_prompt.go`. Tests lock tag strings and breakout cases.

## UI trigger (S2-A2 / S3-A4) — removed

The Notes page no longer exposes **Use this note** / 「用这篇…」, **Work from this note** / 「按这篇做」, or **Create issue from note**. Editor AI (selection / empty-line) uses the Workspace Notes Assistant. Worker jobs are started by Period Brief and other server wakes, not a notes-page picker.

Agent replies that **propose a write** (`note_write` part) after a Worker `note_brief` trigger show two actions under the message body. Ordinary chat/status replies in that thread stay button-free.

1. **Insert below note** — appends a blank line + `## {title}` + the proposed body onto the original note (`PATCH` content). Title is derived from the first line of the proposal.
2. **Create child note** — `POST` a child page under the original note (titled), then write the proposal into that child's content.

The same two actions appear when a chat `note_write` part targets an existing page (`--note-page-id`, or a `/notes/<uuid>` link in the preceding user message).

**Period Work Brief (J3-T4):** the channel `note_brief` sticky points at the private `工作介绍/` **folder** (write parent), while the wake prompt / task `note_brief` context still names the **draft Facts** page for `notes get`. Agent `--note-write --note-page-id <folder>` + human **Create child** lands the Brief under the folder — not on the draft.

FE: `packages/views/notes/note-worker-reply-actions.tsx`; helpers in `@multica/core/notes/worker-reply-actions` and `@multica/core/notes/period-brief`.

## Chat → note confirmation (DM and channel)

Agents must not silent-write `note_page` or claim a local `notes/*.md` file is a product note. Confirmation buttons appear when:

- the agent opts this send in with `--note-write`, or
- the immediately preceding human message asked to insert/write a note (or asked for the confirm button) **and** the agent reply looks like the payload (not a one-line ack).

The stdin / message body should be only the cleaned note markdown. Omitting `--note-page-id` is the default (Create note). Pass `--note-page-id` only when the human gave a UUID or `/notes/<uuid>` link. The Server attaches a `note_write` part when the agent passes the flag — agents never submit Parts.

UI (human click writes with the clicker's note ACL):

| Specified note? | Buttons |
|-----------------|---------|
| No (`note_write` without `ref_id`) | **Create note** — `POST` a top-level page, then `PATCH` the proposed body |
| Yes (`note_brief` sticky, `--note-page-id`, or `/notes/<uuid>` on the preceding user message) | **Insert below note** / **Create child note** (same as Worker replies) |

Do not show Create note / Insert below on ordinary agent replies (a poem request with no save intent stays button-free). A one-line ack after “写入笔记” also stays button-free.

## Agent read path (S2-C2)

- CLI: `multica notes get <page-id> --output json` (agent task token only)
- CLI: `multica notes tree <page-id>` — flat outline of that page and descendants
- API: `GET /api/agent/notes/pages/{id}`
- API: `GET /api/agent/notes/pages/{id}/tree`
- ACL: current task must authorize the page via `note_worker_job`, matching `note_brief`, **or** an active `chat_session.context_note_page_id` (Notes assistant bubble). The human viewer (`creator_id` / initiator) must still pass `noteAccess`. Agent `OwnerUserID` is never the note viewer.
- **Subtree:** authorization on a root page also covers its descendants. Agents may `notes get` / `notes tree` any page under the authorized root.

## Notes assistant bubble (page FAB)

Stay-on-page chat (not Worker → Messages):

1. Notes page shows a bottom-right FAB when a page is selected. Open/closed is session-only: refresh starts with the rail closed. Switching notes (or leaving the page) also closes the rail — returning to the previous note does not reopen it.
2. First open **soft-probes** the Workspace Notes Assistant (`notes-assistant` / 「笔记助手」) via `POST /api/members/agents/notes-assistant` with `{}` — never auto-creates. Missing agent → conversation setup card. Create only on button click: **clone Wendy** (`clone_onboarding`) or **Create Agent dialog** (identity locked; human picks Computer + runtime + model → `runtime_id`+`model`). Archived-after-delete is restored on create, not a hard error. No agent picker in the bubble. The same card + dialog returns if the assistant is later deleted.
3. Opens a right-side full-height sidebar (`chat_session`) with `context_note_page_id` set to the current page, locked to that assistant. Desktop slides in from the right (left-edge drag resizes 280–640px). Rail width lives in the chat store (`noteBubbleSidebarWidth`) so the page dock and overlay stay in lockstep both ways — narrowing must move the `mx-auto` editor right again, not leave a stale reserve. The Notes body row reserves that width (`noteAssistantSidebarReservePx`) so the editor recenters in the uncovered pane — the rail does not cover the prose. Closed rail is **not in the DOM** (`noteAssistantSidebarPresence` omit) — a `motion.div` with `initial={false}` writes `transform: none` and would pin the rail on a refresh. Exit keeps the node 200ms for the CSS slide, then unmounts. Mobile stays fullscreen. Hovering a bubble message shows Copy, then **插入笔记下面** (append a `##` section onto the current page) and **插入子笔记** (create a child of that page) — same write path as Worker confirm / Period Brief cards, using the copied message text.
4. Standalone chat delivery prefixes `<note_chat_context>` with **root id + title only** (no full subtree dump). Idle wake uses `formatStandaloneChatTurnPrompt` (chat turn — **not** channel Canonical Message / `message send`). The agent must use `notes tree` / `notes get` selectively (skill `multica-notes-assistant`) and answer via **final assistant output** (daemon writeback). Redelivery rebuilds the same prefix. Turn failures still write an assistant error row so the UI leaves 排队中.
5. Distinct from Editor (`note_ai_job`) and Worker (`note_worker_job`); replies stay in the bubble transcript. Bubble has no channel `note_write` confirm UI — rewrite proposals stay in final output for the human to apply. Selection sparkle on the Notes editor opens this sidebar immediately and quotes the excerpt in the composer (`askAboutNoteSelection` / `noteSelectionQuote`); a later selection on the same page appends another excerpt. It does not create an Editor AI job. Empty-line Space / `openPageAI` always opens the in-note prompt. Send uses `note_ai_job` only when 笔记助手 already exists. If it is missing, Enter / send opens the same first-open setup card (`setNoteBubbleOpenPageId` / `requestInlineNotePageAI`) and does not start an Editor job.
6. 写汇报 starts from the Notes FAB satellite **or** a spoken 写汇报 ask in the bubble (`looksLikePeriodBriefRequest`, same intent as Go). Either path opens the same time + owned-computer chips for **one send**; typed text wins on conflict. Missing, deleted, or wrongly-bound collectors do **not** auto-create: the chip list shows a dismissible reminder per broken owned Computer. Configuring is optional — 写汇报 continues with the remaining collectors. Configure opens the Create Agent dialog with that Computer locked (runtime + model only) → `POST /api/members/agents/period-brief-collectors` with `runtime_id`+`model`. Probe without `runtime_id` creates nothing. The first 写汇报 send only opens chips and keeps the draft — the next send starts the run. The chip card has **取消** to abort before send; after send the button is gone/disabled (`periodBriefSubmitting` / chips unmount). Chips always sit above the input; an empty thread keeps a flex spacer so opening chips cannot lift the composer. After send, chips disappear, the composer locks only while the run is `planning|collecting|synthesizing`. The insert card (`awaiting_confirm`) **unlocks** the composer — discarding without insert is allowed, and a new 写汇报 may start. The Notes FAB **omits** `chat_session_id` so each 写汇报 opens a new `chat_session`. Passing an id still reuses that thread (spoken chip intake). A second create on the same page is `409` only while collect/synthesis is still running. Collectors / planner / synthesizer still run as Worker `force_fresh_session` wakes. Incoming collector packs and the finished brief render as collapsed `note_brief` cards in this sidebar (packs have no Notes page link). The brief card snapshots **this run's** synthesizer `--note-write` (`created_at >= run.created_at`) or this run's draft — never the latest write on `工作介绍/` from a previous run. The brief card has **插入笔记下面** (append a `##` section onto the issuing page) and **插入子笔记** (create a child of that page) — not under global `工作介绍/`. Leftover 「周报」 agents are archived on Ensure. Collectors stay separate.

FE: `packages/views/notes/note-assistant-bubble.tsx`, `notes-assistant-setup-card.tsx`, `notes-collector-setup-card.tsx`; BE: `EnsureNotesAssistantAgent`, `EnsurePeriodBriefCollectors` (probe vs one-runtime create/repair), `chat_session.context_note_page_id`, `resolveAgentNoteViewer`, `ListAgentNoteTree`.

## Code pointers

- Editor create/get/cancel: `server/internal/handler/notes.go` (`CreateNoteAIJob`, …)
- Worker create/get + cross-rejection: `server/internal/handler/note_worker.go`
- Agent note read: `server/internal/handler/agent_notes.go`
- Worker prompt partitions/escape: `server/internal/handler/note_worker_prompt.go`
- Chat note-write confirmation: `appendAgentNoteWritePart` in `agent_transport.go`; CLI `--note-write` / `--note-page-id`; FE `NoteWorkerReplyActions`
- Note brief context helper: `server/internal/service/note_brief.go`
- Shared intent constants: `server/internal/handler/note_intent.go`
- FE types: `packages/core/types/note.ts` (`NoteIntent`, `CreateNoteWorkerJobRequest`, …)
- Product todo: `todo.md` Slice 2–3
