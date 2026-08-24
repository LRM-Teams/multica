# notes-assistant source map

Evidence layer for `multica-notes-assistant`. Contracts in `SKILL.md` trace
to these sources.

| Claim | Source |
|-------|--------|
| Bubble binds `chat_session.context_note_page_id` | migration `420_chat_session_context_note_page`; `CreateChatSession` |
| Wake prefix `<note_chat_context>` (root id/title; no full subtree dump) | `buildNoteChatWakePrefix` |
| Agent read ACL via active note-scoped session | `resolveNoteChatSessionViewer`, `GetAgentNotePage`, `ListAgentNoteTree` |
| Exact-page share grant (no descendants) | `resolveAgentNoteShareViewer`; `note_page_share_agent` / `note_page_share_channel`; `TestAgentNoteShareAllowsCurrentPageOnly` |
| CLI `notes get` / `notes tree` | `server/cmd/multica/cmd_notes.go`; `GET /api/agent/notes/pages/{id}` (+ `/tree`) |
| Subtree authorization | `notePageIsUnderRoot`; `agentNoteGrantAllowsSubtree`; contract `docs/notes-editor-worker-contract.md` § Agent read path |
| No `notes write` in bubble — propose markdown in final output | `SKILL.md` Delivery/Writes; chat bubble has no `note_write` renderer |
| Editor `note_ai_job` formulas use `$` / `$$` only | `buildNotePageEditPrompt`; `note-ai-edit-prompt.test.ts`; contract § Editor formula markdown |
| Bubble Q&A = final assistant output (not `message send --target chat:`) | `formatStandaloneChatTurnPrompt`; `writebackStandaloneChatTurn`; engineering-principles §1.5 |
| Wake prefix rebuilt on redelivery | `redeliverUnacknowledgedStandaloneChat` + `buildNoteChatWakePrefix` |
| Workspace Notes Assistant persona | template `notes-assistant.json`; `EnsureNotesAssistantAgent` |
| Triple wake: bubble vs collect-plan vs synthesizer | `notes-assistant.json` (`Period Brief collect-plan wake` / `Period Brief synthesizer wake`); `multica-period-work-plan`; `multica-period-work-brief` |
| Stale instructions refresh marker | `notesAssistantInstructionsCapabilityMarker` (`Period Brief collect-plan wake`) |
| Product contract | `docs/notes-editor-worker-contract.md` § Notes assistant bubble |
