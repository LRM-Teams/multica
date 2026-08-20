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
| Max 3 retries per collector | `notePeriodBriefCollectorMaxRetries` |
| Narrow retry API | `POST /api/agent/notes/period-briefs/{draftPageId}/retry-collectors` → `RetryAgentNotePeriodBriefCollectors` |
| Collector submit-pack API | `POST /api/agent/notes/period-briefs/{draftPageId}/submit-pack` → `SubmitAgentNotePeriodBriefPack` |
| CLI | `multica notes period-brief retry-collectors` → `server/cmd/multica/cmd_notes.go` |
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

