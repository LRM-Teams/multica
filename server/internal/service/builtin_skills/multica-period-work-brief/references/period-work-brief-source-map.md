# period-work-brief source map

Evidence layer for `multica-period-work-brief`. Contracts in `SKILL.md` trace
to these sources.

| Claim | Source |
|-------|--------|
| Platform waits until collectors settle (not fixed empty timeout) | `server/internal/handler/note_period_brief.go` `awaitPeriodBriefCollectorPacks` |
| Failed job still ready if `--note-write` landed | `classifyPeriodBriefCollectorOutcome` (`packReady` first); `TestCreateNotePeriodBriefHarvestsNoteWriteAfterFailedTask` |
| Collect / retry / synth one-shot session | `persistPeriodBriefNoteBriefContext`; daemon `ForceFreshSession` |
| Status board fields (status/retryable/abandon_why) | `formatNotePeriodBriefPacks`, `classifyPeriodBriefCollectorOutcome` |
| Permanent vs retryable classification | `server/internal/handler/note_period_brief_classify.go` |
| Max 3 retries per collector | `notePeriodBriefCollectorMaxRetries` |
| Narrow retry API | `POST /api/agent/notes/period-briefs/{draftPageId}/retry-collectors` → `RetryAgentNotePeriodBriefCollectors` |
| CLI | `multica notes period-brief retry-collectors` → `server/cmd/multica/cmd_notes.go` |
| Durable run + retry counts | `note_period_brief_run` migration `414_note_period_brief_run` |
| Product contract | `docs/adr/0019-runtime-agent-collectors-period-brief.md` |
| Collector skill (do not confuse) | `multica-period-work-collect` |

## Delivery

| Claim | Source |
|-------|--------|
| `--note-write` to folder under 工作介绍/ | `notePeriodBriefInstruction` |
| Group by initiative; nested sub-points; never path-as-title; carry pack Mermaid | `notePeriodBriefInstruction`; `weekly-report.json`; this skill |
| Wake system_contract repeats grouping + Mermaid | `buildNotePeriodBriefPrompt` |
| Platform weekly-report persona refresh on stale instructions | `EnsurePeriodBriefAgent` / `refreshPeriodBriefInstructionsIfStale` |

