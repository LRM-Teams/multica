# notes-assistant source map

Evidence layer for `multica-notes-assistant`. Contracts in `SKILL.md` trace
to these sources.

| Claim | Source |
|-------|--------|
| Assistant reads packs / brief / current note; human owns collect scope | `docs/notes-period-brief-assistant-contract.md`; `tryHandlePeriodBriefPlanAsk` |
| Bubble binds `chat_session.context_note_page_id` | migration `420_chat_session_context_note_page`; `CreateChatSession` |
| Wake prefix `<note_chat_context>` (session id, root id/title; no full subtree dump) | `buildNoteChatWakePrefix` |
| Wake prefix `<period_brief_residue>` after a session 写汇报 | `formatPeriodBriefChatResidue`; `loadPeriodBriefChatResidue`; persist `result_page_id` / `result_mode` in `applyPeriodBriefInsert`; persist `result_markdown` in `postPeriodBriefResultMessage`; migrations `465_note_period_brief_run_result`, `466_note_period_brief_run_result_markdown` |
| Session harvests are read-only context | `loadPeriodBriefSessionMaterials`; `periodBriefPackMachineIdentity`; `formatPeriodBriefSessionMaterialsBoard` (`os` / `hostname`) |
| Speech / FAB 写汇报 opens the plan card; 开始采集 walks every selected computer then synthesizes | `tryHandlePeriodBriefPlanAsk`; `looksLikePeriodBriefPlanAsk`; `POST /api/notes/period-briefs` (`CreateNotePeriodBrief`); Highlights decide harvest (`periodBriefPackHasHarvest`); a selected computer that failed with no pack after retry does not post a new official brief (`periodBriefOfficialBriefBlocked`) |
| Stale instructions refresh marker | `notesAssistantInstructionsCapabilityMarker` (`Period Brief card owns collect scope`) |
| Wake prefix `<period_brief_progress>` during an in-flight run | `formatPeriodBriefProgressBoard`; `loadOpenPeriodBriefRunForSession`; `wakePeriodBriefProgress`; `buildNoteChatWakePrefix` |
| Complete Start restates plan then waits | `wakePeriodBriefRunStarted`; `<period_brief_progress>` `event: run_started` |
| Lock means a collector or synthesizer is actually running | `reconcilePeriodBriefLock`; `periodBriefHasLiveWorker`; written orphan → settle; dead lock → abandon; 409 only while a worker job is live |
| Insert names a writable target | `POST /api/agent/notes/period-briefs/{runId}/insert`; CLI `notes period-brief insert`; human confirm on non-issuing page |
| One current plan per bubble | CLI `notes period-brief plan` (bare session id creates the visible card); `GET`/`PUT /api/agent/notes/period-briefs/plan`; human `GET`/`PUT`/`DELETE /api/notes/period-briefs/plan`; `formatPeriodBriefCurrentPlanBoard`; plan-card 取消 closes the plan and stops an in-flight run (`DeleteNotePeriodBriefPlan`, `stopActivePeriodBriefRunForPage`) |
| Plan / result cards are not chat XML | Plan: session plan API + `notes:period_brief_plan`; result: `postPeriodBriefResultMessage` (`note_brief` + `period_brief_insert` parts) |
| Bubble `notes get` of 写汇报 draft/result outside the context subtree | `resolvePeriodBriefResidueViewer` (exact page only) |
| Agent read ACL via active note-scoped session | `resolveNoteChatSessionViewer`, `GetAgentNotePage`, `ListAgentNoteTree` |
| Exact-page share grant (no descendants) | `resolveAgentNoteShareViewer`; `note_page_share_agent` / `note_page_share_channel`; `TestAgentNoteShareAllowsCurrentPageOnly` |
| CLI `notes get` / `notes tree` | `server/cmd/multica/cmd_notes.go`; `GET /api/agent/notes/pages/{id}` (+ `/tree`) |
| Subtree authorization | `notePageIsUnderRoot`; `agentNoteGrantAllowsSubtree`; contract `docs/notes-editor-worker-contract.md` § Agent read path |
| Ordinary bubble rewrite — propose markdown, human clicks insert | `SKILL.md` Writes; `NoteChatInsertActions`; `buildChatNoteWriteConfirmationByMessageId` |
| Editor `note_ai_job` formulas use `$` / `$$` only | `buildNotePageEditPrompt`; `note-ai-edit-prompt.test.ts`; contract § Editor formula markdown |
| Bubble Q&A = final assistant output (not `message send --target chat:`) | `formatStandaloneChatTurnPrompt`; `writebackStandaloneChatTurn` |
| Wake prefix rebuilt on redelivery | `redeliverUnacknowledgedStandaloneChat` + `buildNoteChatWakePrefix` |
| Workspace Notes Assistant persona | template `notes-assistant.json`; `EnsureNotesAssistantAgent` |
| Four wakes: bubble / progress / collect-plan / synthesizer | `notes-assistant.json`; `docs/notes-period-brief-assistant-contract.md` |
| Empty focus uses per-computer collect roots | `isPeriodBriefPlanComplete`; ADR 0019 collect-roots; empty file → `SCAN_ROOTS` |
| Product contract | `docs/notes-period-brief-assistant-contract.md`; `docs/notes-editor-worker-contract.md` § Notes assistant bubble |
