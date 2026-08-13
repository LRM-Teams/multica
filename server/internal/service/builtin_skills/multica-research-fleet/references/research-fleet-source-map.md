# Research Fleet source map

| Claim | Source |
| --- | --- |
| HTTP routes under `/api/research` | `server/cmd/server/router.go` Research Fleet block |
| Durable Run HTTP and task-bound result authorization | `server/internal/handler/research_run_http.go`, `server/cmd/server/router.go` |
| Run scheduler, leases, retries, recovery, typed remediation routing, bounded evaluation feedback, ranked question frontier, canonical information gain, marginal-gain and delivery decisions | `server/internal/researchrun/engine.go`, `store.go`, `postgres_tasks.go`, `postgres_gate.go`, `postgres_gate_v4.go`, `information_gain.go`, `server/internal/scheduler/jobs_research_run.go` |
| Canonical task/progress/evidence/event ledgers | migrations `274_research_run_backend`, `276_research_report_quality`, `284_research_evidence_fitness`; `server/internal/researchrun/postgres*.go` |
| Current contract and accepted Research Method read model, plus validated steering limits | `server/internal/researchrun/config.go`, `postgres.go`, `postgres_result.go`, `postgres_tasks.go` |
| Strict structured result envelope, pinned V1–V4 behavior, V4+ Method/evidence-standard and executable delivery-path contract, V5 structured evaluation defects, and Claim-level evidence-fitness Gate | `server/internal/researchrun/result.go`, `result_v2.go`, `result_v3.go`, `result_v4.go`, `result_v5.go`, `result_test.go`, `result_v5_test.go`, `postgres_result.go`, `postgres_gate.go`, `postgres_gate_v4.go` |
| Inbox dispatch idempotency and event projection | `server/internal/handler/research_run_adapter.go` |
| Legacy authoritative mutations rejected for initialized runs | `server/internal/handler/research_run_guard.go` plus guarded handlers in `research_ops.go` and `research_product_rounds.go` |
| Fleet ensure + seed roles | `server/internal/handler/research_fleet.go`, `research_templates.go` |
| Dynamic roster hire/optimize/archive (lead-only, cap, audit) | `server/internal/handler/research_ops.go`, `research_roster.go` |
| Legacy graph/source/report/message/stage/handoff for uninitialized sessions | `server/internal/handler/research_ops.go`, `research_stage.go`, `research_handoff.go` |
| Session kickoff graph + process cards | `server/internal/handler/research_kickoff.go`, `research_process.go` |
| Message `card_kind` / `meta` | migration `246_research_message_cards` |
| Unique active fleet role + dedupe | migration `247_research_fleet_member_role_unique` |
| Mirror agent chat replies into research drawer | `server/internal/service/research_chat_mirror.go` |
| Session wake / fleet dispatch + archived wake gate | `server/internal/handler/research_dispatch.go`, `server/internal/researchwake` |
| Domain playbooks | `references/playbooks/*.md` + `seedResearchFleetPlaybooks` |
| Schema | `server/migrations/244_research_fleet.up.sql` |
| SQL | `server/pkg/db/queries/research.sql` |
| WS events | `server/pkg/protocol/research_events.go` |
| CLI | `server/cmd/multica/cmd_research.go` (`task-result`, session snapshot, roster administration) |
| Frontend | `packages/views/research/`, paths `research` / `researchDetail` |
| Artifact passport (D): policy, manifest freeze, dispatch outbox binding, acceptance grant/membership fence, result artifact lineage | migrations `318`–`330`, `346`–`349`; `server/internal/researchrun/artifact*.go`, `artifact_context.go`, `artifact_manifest.go`, `artifact_dispatch_prompt.go`, `artifact_representation.go`, `artifact_taint.go`, `postgres_artifact.go`, `postgres_tasks.go`, `postgres_result.go`, `postgres_evidence.go`; Source Snapshot/Observation/Claim reads decode frozen bytes; Quality Gate/Citation Audit use `evaluation` purpose with separate normal + evaluation-private grants and only their assigned grader Prompt receives frozen Stage Evaluation context; manifest hash binds scope/purpose/watermarks/representations; dispatch/replay use frozen principal header; acceptance re-locks both exact grants and active Fleet membership before commit; accepted Result and materialized outputs inherit the most sensitive manifest access level, and unsafe reuse fails closed; Agent snapshot requires assigned `attempt_id`; `packages/core/research/schemas.ts` parses bounded `attempt_context` |
| Inquiry Graph (E, not yet exposed to Agent tasks) | migrations `348_research_inquiry_graph`, `350_research_inquiry_graph_guards`; `server/internal/researchrun/inquiry.go`; lifecycle transitions, polymorphic endpoint existence and dependency DAG are fail-closed while V6 remains disabled |
| Auditable source-screening contract (F, persistence wiring pending) | `server/internal/researchrun/screening_policy.go`, `screening_policy_test.go`; versioned inclusion/exclusion criteria, disposition matrix, reviewer identity, located facts, canonical duplicate identity and stable decision fingerprint |
| Frozen V6 design contract (production disabled) | `docs/contracts/research-run-v6.schema.json`, `docs/research-run-v6-contract.md`, `server/internal/researchrun/v6_contract_test.go`; V5 remains default and V6 remains unsupported until E–K exits |
