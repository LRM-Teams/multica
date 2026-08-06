# Research Fleet source map

| Claim | Source |
| --- | --- |
| HTTP routes under `/api/research` | `server/cmd/server/router.go` Research Fleet block |
| Fleet ensure + seed roles | `server/internal/handler/research_fleet.go`, `research_templates.go` |
| Dynamic roster hire/optimize/archive (lead-only, cap, audit) | `server/internal/handler/research_ops.go`, `research_roster.go` |
| Graph/source/report/message/stage/handoff | `server/internal/handler/research_ops.go`, `research_stage.go`, `research_handoff.go` |
| Session kickoff graph + process cards | `server/internal/handler/research_kickoff.go`, `research_process.go` |
| Message `card_kind` / `meta` | migration `246_research_message_cards` |
| Unique active fleet role + dedupe | migration `247_research_fleet_member_role_unique` |
| Mirror agent chat replies into research drawer | `server/internal/service/research_chat_mirror.go` |
| Session wake / fleet dispatch + archived wake gate | `server/internal/handler/research_dispatch.go`, `server/internal/researchwake` |
| Domain playbooks | `references/playbooks/*.md` + `seedResearchFleetPlaybooks` |
| Schema | `server/migrations/244_research_fleet.up.sql` |
| SQL | `server/pkg/db/queries/research.sql` |
| WS events | `server/pkg/protocol/research_events.go` |
| CLI | `server/cmd/multica/cmd_research.go` (`hire` / `optimize` / `archive`) |
| Frontend | `packages/views/research/`, paths `research` / `researchDetail` |
