# period-work-brief source map

Evidence layer for `multica-period-work-brief`. Contracts in `SKILL.md` trace
to these sources.

| Claim | Source |
|-------|--------|
| Custom inclusive date range (`window=custom`) | `resolveNotePeriodBriefWindow` / `resolveNotePeriodBriefCustomWindow` |
| Strict in-window collect + synthesize | `notePeriodBriefCollectorInstruction`, `notePeriodBriefInstruction`, this skill |
| Failed job still ready if `--note-write` landed | `classifyPeriodBriefCollectorOutcome` (`packReady` first); `TestCreateNotePeriodBriefHarvestsNoteWriteAfterFailedTask` |
| Collect / retry / synth one-shot session | `persistPeriodBriefNoteBriefContext`; daemon `ForceFreshSession` |
| Status board fields (status/retryable/abandon_why) | `formatNotePeriodBriefPacks`, `classifyPeriodBriefCollectorOutcome` |
| Permanent vs retryable classification | `server/internal/handler/note_period_brief_classify.go` |
| One Notes-Assistant retry per collector; inbox does not auto-retry | `notePeriodBriefCollectorMaxRetries`; `SetAgentTaskMaxAttempts(..., 1)` |
| Retry-only wake vs write wake | `notePeriodBriefRetryInstruction`; `dispatchNotePeriodBriefWorker(..., retryOnly)`; harvest `created_at >= writeAfter` |
| Atomic per-collector pack write | `mergeNotePeriodBriefCollector`; await matches `pack_job_id` |
| Narrow retry API | `POST /api/agent/notes/period-briefs/{draftPageId}/retry-collectors` → `RetryAgentNotePeriodBriefCollectors` |
| Collector submit-pack API | `POST /api/agent/notes/period-briefs/{draftPageId}/submit-pack` → `SubmitAgentNotePeriodBriefPack` |
| CLI | `multica notes period-brief retry-collectors` → `server/cmd/multica/cmd_notes.go` |
| Optional human focus + collect plan | `normalizePeriodBriefUserFocus`; `applyNotePeriodBriefCollectPlan`; `<focus>` in `buildNotePeriodBriefPrompt` |
| Collect-plan wake (earlier) | `multica-period-work-plan`; `submit-collect-plan` |
| Durable run + retry counts | `note_period_brief_run` migration `414_note_period_brief_run` |
| Product contract | `docs/adr/0019-runtime-agent-collectors-period-brief.md` |
| Collector skill (do not confuse) | `multica-period-work-collect` |

## Delivery

| Claim | Source |
|-------|--------|
| `--note-write` to folder under 工作介绍/ | `notePeriodBriefInstruction` |
| Fixed board: Summary (Work Summary + Next Steps); optional Technique / Achievements / Research | `notePeriodBriefInstruction`; `notes-assistant.json` synthesizer wake; this skill |
| Audience-facing Brief — no evidence layer (hashes/diffs/「证据」) | `notePeriodBriefInstruction`; this skill `Audience` / `No evidence layer` |
| Wake system_contract repeats Summary board + Mermaid | `buildNotePeriodBriefPrompt` |
| Synthesizer identity is 笔记助手 | `resolvePeriodBriefSynthesizerId`; `EnsurePeriodBriefAgent` archives leftover `weekly-report` |
| Work Summary expands collector Work groups (main title + nested work) | this skill `Work Summary grouping`; `notePeriodBriefInstruction` |
| Notes are not Period Brief source material | `normalizeNotePeriodBriefSources` always skips `touched_notes`; `formatNotePeriodBriefFacts` omits notes; instruction §12; this skill `Notes pages are not a source` |
| `agent_runs` excludes 写汇报 machinery | `loadNotePeriodBriefRunFacts` (`note_worker`, `notes-assistant`, `period-collect-*`, `weekly-report`); `TestLoadNotePeriodBriefFactsExcludesMachineryAndNotes` |
| Fact lines never paste `trigger_summary` | `formatNotePeriodBriefRunLine`; `TestFormatNotePeriodBriefFactsOmitsNotesAndPromptBlobs` |

