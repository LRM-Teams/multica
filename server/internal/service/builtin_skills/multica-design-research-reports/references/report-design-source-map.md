# Report design source map

| Claim | Source |
| --- | --- |
| Reporter-only and task-bound V6 report built-in skill delivery | `server/internal/service/builtin_skills.go`; `server/internal/handler/onboarding_agent_capability.go`; `server/internal/handler/agent_inbox.go` |
| Explicit V6 report Work activation | `server/internal/researchrun/work_prompt_v6.go`; `server/internal/handler/research_templates.go` |
| One maximum input per top-level direction, full persisted input documents and revision trigger | `server/internal/researchrun/report_input_selector_v6.go`; `server/internal/researchrun/postgres_director_brief.go`; `server/internal/researchrun/director_proposal_preflight_v6.go`; `server/internal/researchrun/postgres_report_work_v6.go` |
| Report resource upload and immutable package acceptance | `server/internal/researchrun/postgres_report_package_v6.go`; `server/cmd/multica/cmd_research.go` |
| Server binds frozen report inputs and compiled package hashes at apply; receive drops malformed agent copies | `server/internal/researchrun/contract_v6.go`; `server/internal/researchrun/postgres_report_package_v6.go` |
| Self-contained HTML, media, size, URL, DOM and script/style validation, plus required Design Dossier extraction and revision persistence | `server/internal/researchrun/report_package_v6.go`; `server/internal/researchrun/postgres_report_package_v6.go`; migration `459_research_v6_report_boss` |
| Isolated report origin and CSP sandbox | `server/internal/handler/research_report_document.go`; `server/internal/researchrun/report_sandbox_policy.go` |
| Report evidence, revision and publication contract | `docs/research-run-v6-contract.md`; `server/internal/researchrun/report_lineage.go`; `server/internal/researchrun/postgres_report_review_v6.go` |
| Design-read and anti-template principles adapted for research publishing | Taste Skill at `https://github.com/leonxlnx/taste-skill/tree/ccbc15639c97057cbfcf32ecebc38ef716e4bb37/skills/taste-skill`, MIT License |
