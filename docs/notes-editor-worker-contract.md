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
3. **Note body is untrusted input for Worker** (S2-C4 will wrap it). User `instruction` is the trusted directive; page content is context only.
4. **Writebacks stay pending until accept** (D1). Worker completion may *propose* writebacks; it must not silent-edit the page through Editor.

## Status of Worker execution

S2-C3 established the typed contract and misuse rejection. **S2-C1** wires dispatch:

1. `POST .../worker-jobs` loads the page under ACL, builds an untrusted `<note>` + trusted `<instruction>` prompt, enqueues a chat task, and merges `context.note_brief = { version, page_id, title }`.
2. `note_worker_job.task_id` is set and status becomes `dispatched`.
3. UI trigger is still **S2-A2**; Agent `notes get` tool is **S2-C2**; richer prompt partitions are **S2-C4**.

## Code pointers

- Editor create/get/cancel: `server/internal/handler/notes.go` (`CreateNoteAIJob`, …)
- Worker create/get + cross-rejection: `server/internal/handler/note_worker.go`
- Note brief context helper: `server/internal/service/note_brief.go`
- Shared intent constants: `server/internal/handler/note_intent.go`
- FE types: `packages/core/types/note.ts` (`NoteIntent`, `CreateNoteWorkerJobRequest`, …)
- Product todo: `todo.md` Slice 2
