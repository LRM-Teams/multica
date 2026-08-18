# period-work-collect source map

Evidence layer for `multica-period-work-collect`. Contracts in `SKILL.md` trace
to these sources.

## Period Brief collector path (ADR 0019)

| Behavior | Source |
| --- | --- |
| `POST /api/notes/period-briefs` orchestrates collectors then synthesizer | `server/internal/handler/note_period_brief.go` `CreateNotePeriodBrief` |
| Collector instruction + pack stub + `--note-write` pack page id | `server/internal/handler/note_period_brief.go` `notePeriodBriefCollectorInstruction`, `notePeriodBriefCollectorPackStub`, `dispatchNotePeriodBriefCollector` |
| Collector wake partitions (`<window>`, `<instruction>`, stub note) | `server/internal/handler/note_worker_prompt.go` `buildNotePeriodBriefCollectorPrompt` |
| Short wait / empty degrade for packs (does not fail whole request) | `server/internal/handler/note_period_brief.go` `awaitPeriodBriefCollectorPacks` (waits for collector job terminal state up to ~4m; harvests pending `--note-write` proposals) |
| Synthesizer must not be confused with collectors | `server/internal/agenttmpl/templates/weekly-report.json`; `notePeriodBriefInstruction` |
| Product contract | `docs/adr/0019-runtime-agent-collectors-period-brief.md` |

## Delivery / note-write

| Behavior | Source |
| --- | --- |
| `multica message send --note-write --note-page-id` | `server/cmd/multica/cmd_message.go` |
| Agent `note_write` part attachment | `server/internal/handler/agent_transport.go` `appendAgentNoteWritePart` |
| Human confirm writes note page (no silent replace) | `packages/core/notes/worker-reply-actions.ts`; Notes Worker UI |

## OS discovery (Agent shell — not Host Digest)

| Behavior | Source |
| --- | --- |
| Host Digest harvest is **not** the Brief collector path (superseded) | ADR 0019; legacy `server/internal/computer/work_journal.go` `HarvestWorkJournal` |
| Denylist / noise dir names (skill mirrors for Agent walks) | `server/internal/computer/work_journal_denylist.go` |
| Recipes shipped with this skill | `references/collect-recipes.md` |

## Builtin injection

| Behavior | Source |
| --- | --- |
| Always-on for ordinary Agents (not hiring-only) | `server/internal/service/builtin_skills.go` `BuiltinSkillsForAgent` / embed |
| Skill files land in provider skill dirs | `server/internal/daemon/execenv/context.go` `writeSkillFiles` |

## Verification

```bash
cd server && go test ./internal/service/ -run 'TestBuiltinSkillsConformToTemplate|TestPeriodWorkCollectSkill'
cd server && go test ./internal/handler/ -run 'TestCreateNotePeriodBrief'
```
