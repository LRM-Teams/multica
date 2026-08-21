# period-work-plan source map

Evidence layer for `multica-period-work-plan`. Contracts in `SKILL.md` trace
to these sources.

| Claim | Source |
|-------|--------|
| Optional human focus on create | `createNotePeriodBriefRequest.Focus`; `normalizePeriodBriefUserFocus` |
| Empty focus skips this wake | `CreateNotePeriodBrief` dispatches collectors immediately when focus is empty |
| Planner wake + submit-collect-plan | `dispatchNotePeriodBriefPlanner`; `POST /api/agent/notes/period-briefs/{draftPageId}/submit-collect-plan` |
| CLI | `multica notes period-brief submit-collect-plan` → `server/cmd/multica/cmd_notes.go` |
| Plan apply (selected only, skip, all-skip fallback) | `applyNotePeriodBriefCollectPlan` |
| Durable plan | `note_period_brief_run.user_focus` / `collect_plan`; migration `432_note_period_brief_collect_plan` |
| Product contract | `docs/adr/0019-runtime-agent-collectors-period-brief.md` |
| Synthesizer skill (later wake) | `multica-period-work-brief` |
| Collector skill | `multica-period-work-collect` |
