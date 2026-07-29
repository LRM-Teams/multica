# Research Fleet source map

| Claim | Source |
| --- | --- |
| HTTP routes under `/api/research` | `server/cmd/server/router.go` Research Fleet block |
| Fleet ensure + seed roles | `server/internal/handler/research_fleet.go`, `research_templates.go` |
| Graph/source/report/message/stage/handoff | `server/internal/handler/research_ops.go`, `research_stage.go`, `research_handoff.go` |
| Session wake / fleet dispatch | `server/internal/handler/research_dispatch.go` (`EnqueueChatTask`) |
| Domain playbooks | `references/playbooks/*.md` + `seedResearchFleetPlaybooks` |
| Schema | `server/migrations/244_research_fleet.up.sql` |
| SQL | `server/pkg/db/queries/research.sql` |
| WS events | `server/pkg/protocol/research_events.go` |
| CLI | `server/cmd/multica/cmd_research.go` |
| Frontend | `packages/views/research/`, paths `research` / `researchDetail` |
