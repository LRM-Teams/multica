# period-work-collect source map

Evidence layer for `multica-period-work-collect`. Contracts in `SKILL.md` trace
to these sources.

| Claim | Source |
|-------|--------|
| Custom inclusive date range (`window=custom`) | `resolveNotePeriodBriefWindow` / `resolveNotePeriodBriefCustomWindow` |
| Collector instruction + draft id + submit-pack | `server/internal/handler/note_period_brief.go` `notePeriodBriefCollectorInstruction`, `dispatchNotePeriodBriefCollector` |
| `POST /api/notes/period-briefs` orchestrates collectors then synthesizer | `server/internal/handler/note_period_brief.go` `CreateNotePeriodBrief` |
| Collectors only on Computers the caller owns | `EnsurePeriodBriefCollectors` (`ListAgentRuntimesByOwner`); `parsePeriodBriefCollectorAgentIDs` runtime `owner_id` check; FE `listOwnedPeriodBriefCollectorAgents` |
| Pack stored on run JSONB (not Notes page) | `SubmitAgentNotePeriodBriefPack`; `notePeriodBriefCollectorRef.PackMarkdown` |
| Settle reads `pack_markdown` | `awaitPeriodBriefCollectorPacks` |
| Packs purged after synth wake | `clearCollectorPackMarkdown` when status → `done` |
| Collector must add `## Work groups` + optional Mermaid (evidence remains required) | ADR 0019 Detail level; this skill `SKILL.md` pack shape; `notePeriodBriefCollectorInstruction` |
| Product contract | `docs/adr/0019-runtime-agent-collectors-period-brief.md` |
| Scan roots = HOME ∪ `/workspace` ∪ visible project dirs; non-git in-window files | this skill `SKILL.md`; `references/collect-recipes.md` `SCAN_ROOTS`; `notePeriodBriefCollectorInstruction` |

## Delivery / submit-pack

| Claim | Source |
|-------|--------|
| `multica notes period-brief submit-pack` | `server/cmd/multica/cmd_notes.go` |
| Agent API | `POST /api/agent/notes/period-briefs/{draftPageId}/submit-pack` |
