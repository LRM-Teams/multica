# graph-memory-dive-judge — Build Implementation Plan

Date: 2026-08-18
Change: Comet Native `graph-memory-dive-judge` (phase: build)
Authoritative artifacts: AReaL Comet worktree `docs/comet/changes/graph-memory-dive-judge/{brief.md,specs/graph-memory/spec.md}`
Implementation repo: Multica Build worktree `/workspaces/leagent/backend/areal/multica/.worktrees/graph-memory-dive-judge-build` (branch `comet/graph-memory-dive-judge-build`, base `feature/graph-memory-type-rename` @ d91801925)

## Rules in force

- TDD mandatory: every behavior gets a failing test first, run and observed failing for the right reason, then minimal implementation, then green.
- No commits unless the user explicitly asks. No new dependencies without explicit confirmation.
- After SQL changes: `make sqlc` and check generated drift.
- Frontend changes: `pnpm lint`, `pnpm typecheck`, `pnpm test`, `pnpm react:doctor`.
- All implementation in the Build worktree only; never in the original Multica checkout.
- Build container command prefix:
  `ssh zhoujie22@10.110.158.142 "docker exec zhoujie22-dev-leagent-workspace-1 bash -c 'export PATH=/usr/local/go/bin:$PATH; ...'"`

## Baseline state (2026-08-18, verified)

- `go test ./internal/memorygraph -count=1` — PASS (6.8s)
- `go test ./internal/service -run 'GraphMemory|OfflineTrajectory|Training' -count=1` — PASS
- `go test ./internal/handler -count=1` (full, 442s) — pre-existing failures at base commit d91801925, none caused by this change:
  1. `TestGetGraphMemoryChannelLineage` — fixture inserts a `member`-role row into an ownerless workspace; migration 301 deferred trigger `member_workspace_exactly_one_owner` raises at commit (reproduced directly with psql: BEGIN; INSERT workspace; INSERT member role='member'; COMMIT → ERROR 23514).
  2. `TestGraphMemoryRouteSchema` — `SELECT scoped_writer_ready FROM graph_memory_profile LIMIT 1` assumes an ambient profile row; table is empty in a clean run.
  3. `TestResolveChannelRouteConcurrentSingleGeneration` — channel fixture without workspace membership trips "ordinary group must have at least one human owner" (23514).
  4. `TestEnsureWindy_*` (2), `TestQuickCreateIssueParentTrustBoundary`/`SourceTrustBoundary` (6 subtests) — "runtime is offline or heartbeat is stale", environment/time-dependent, unrelated to graph memory.
  5. `TestPostResearchMessageClientRequestIDValidation` — expected 200 got 201, unrelated research-message path.
- Items 1–3 are in graph-memory test files this change touches; Task 0 repairs them (test-only). Items 4–5 are out of scope; recorded as pre-existing known limits for the builder handoff.

Additional pre-existing failures observed during Build (2026-08-18, both in files this change does not touch, recorded as known limits):

- `go test ./internal/memorygraph -count=1` — `TestDailySealIsImmutableAndLateEventsLandInOpenDaily` fails on 2026-08-18 (late event lands in the open daily with empty `late_for_date`, want 2026-08-17); date/timezone-dependent — the same suite passed in the earlier baseline run.
- `go test ./internal/handler -count=1` (Phase 1 checkpoint, 581s) — additionally `TestTeardownRuntimeWithoutActiveAgents_ProductionScaleSelfFKLookup` times out inserting 300k inbox decoys within its 90s context deadline on the shared dev DB; environmental (passes when the DB is fast, fails in isolation under load too), unrelated to graph memory.
- `pnpm typecheck` in `packages/views` — `research/components/research-session-page.tsx(526,36)` TS2448/TS2454 `messages` used before declaration, introduced by bfa79612e; unrelated to graph memory (file has no graph-memory imports).
- `go test ./internal/daemon -count=1` — timing-sensitive watcher/lease tests flake under shared-container load (one per full run, each passing 3/3 in isolation): `TestWatchTaskCancellation_RunningTaskNotInterrupted` (poll count 3 < 5 within 150ms) and `TestHandleTask_InboxLeaseLossAfterRunnerDoesNotCancelTerminalReport` (racing lease rejection). daemon_test.go paths this change does not touch.
- `sqlc generate` (v1.31.1 and v1.30.0) fails at HEAD: migrations 380–383 `ALTER FUNCTION research_artifact_scan_session_migration_diagnostics RENAME` + re-`CREATE FUNCTION` trips sqlc's catalog ("relation already exists"). Pre-existing; CI has no sqlc drift check. Generated code for the Phase 1 queries was therefore hand-written in sqlc's exact output shape (models.go struct extension + graph_memory.sql.go), with `queries/graph_memory.sql` kept as the source of truth so a future working sqlc regenerates identical code.

## Verified current-state facts (from Shape code review, to be TDD-covered)

1. `server/internal/memorygraph/explore_tools.go` — `/view` has no `expansion_id` and does not record successful views; `/expand` increments `st.rounds` unconditionally (~line 373), retries double-count; `/submit` overwrites `st.submission` (~line 543) and only checks node existence, not viewed-subset.
2. `server/internal/memorygraph/explore.go` — `runTrajectory` writes `run.Found/Summary/NodeIDs` from model final JSON (~lines 292–294) instead of tool-server submission.
3. `server/internal/handler/graph_memory_judge.go` — starts an in-memory goroutine then returns 202; crash loses the judge job.
4. `server/internal/memorygraph/reward.go` + `server/internal/arealrl/reward_sink.go` — pending/key deleted before external `SetReward` success; transient 5xx cannot reliably retry.
5. `server/internal/memorygraph/store.go` / `graph.go` — GC ignores active pinned consumers; LoadGraph loads nodes/edges non-atomically; store mutex is instance-local.
6. `server/internal/service/graph_memory_ingest.go` + `memorygraph/consolidate.go` — segment body published before scope sidecar; crash leaves partial source; missing sidecar/empty visibility fails open to project-visible.
7. `server/internal/handler/file.go` — attachment clone copies URL; deleting one row deletes shared S3 object.
8. `server/internal/memorygraph/backtest.go` — nil runner counts as coverage pass; empty cohort returns perfect recall (1,1); round stats filter by old JudgeThreshold+Found.
9. `server/pkg/db/queries/graph_memory.sql` — profile upsert is full-row without config version (lost update).
10. `packages/core/api/schemas.ts` — invalid `memory_type` uses `.catch("legacy")`, hiding server/schema errors.
11. `server/internal/handler/graph_memory_lineage.go` — checks workspace membership only, not private-channel membership/admin.
12. `server/internal/service/training.go` — remote StartSession before proxy persistence; crash can orphan sessions (needs opening intent/generation/reconciliation).

## Acceptance coverage model

A1–A32 come from brief.md acceptance examples. A33–A160 were extracted by Comet Runtime from normative spec statements; every spec section is mapped to at least one task below, and final verification maps each acceptance ID to pass/fail/blocked evidence.

---

## Task 0 — Repair graph-memory fixture baseline (test-only)

Goal: green graph-memory handler tests at the base commit so later RED/GREEN signals are trustworthy.

- 0.1 Fix owner-invariant fixtures. `createGraphMemoryTestWorkspace` gains a companion that also inserts an owner member in one transaction (deferred trigger checks at commit). `TestGetGraphMemoryChannelLineage`: create owner + non-owner member in one tx. Files: `server/internal/handler/graph_memory_route_test.go`, `graph_memory_lineage_test.go`, `graph_memory_consolidation_test.go` (shared helpers). Follow the fixture pattern d91801925 applied elsewhere.
- 0.2 Fix channel fixture: `createGraphMemoryTestChannel` must satisfy the ordinary-group human-owner invariant (creator must be a workspace member / owner first). Fix `TestResolveChannelRouteConcurrentSingleGeneration` accordingly.
- 0.3 Make `TestGraphMemoryRouteSchema` self-contained: insert its own workspace+owner+profile row instead of relying on ambient rows.
- Verify: `go test ./internal/handler -run 'GraphMemory|ResolveChannelRoute' -count=1` green; full handler package shows only pre-existing items 4–5.

## Phase 1 — Profile, schema, DB lifecycle foundation

Spec §2, §16; brief D2, D22, D25; acceptance A21 (server half), A30 (profile part), A31 (numeric boundaries).

- 1.1 Migration `418_graph_memory_dive_judge_profile.up/down.sql`: extend `graph_memory_profile` with `ttt_enabled bool NOT NULL DEFAULT false`, `ttt_concurrency int NOT NULL DEFAULT 4`, `explore_nodes_per_expansion int NOT NULL DEFAULT 1`, `max_hierarchy_fanout int NOT NULL DEFAULT 8`, `max_relation_edges_per_node int NOT NULL DEFAULT 8`, `dive_max_rounds int NOT NULL DEFAULT 6`, `dive_max_viewed_nodes int NOT NULL DEFAULT 24`, `dive_max_source_files int NOT NULL DEFAULT 4`, `dive_timeout_seconds int NOT NULL DEFAULT 600`, `w_round double precision NOT NULL DEFAULT 0.1`, `source_max_file_bytes bigint NOT NULL DEFAULT 20971520`, `source_max_total_bytes bigint NOT NULL DEFAULT 52428800`, `source_max_pdf_pages int NOT NULL DEFAULT 50`, `source_max_av_seconds int NOT NULL DEFAULT 600`, `source_max_image_megapixels int NOT NULL DEFAULT 40`, `dive_model text NOT NULL DEFAULT ''`, `dive_provider text NOT NULL DEFAULT ''`, `config_version bigint NOT NULL DEFAULT 1`, `schema_version int NOT NULL DEFAULT 1`. CHECK constraints reject negative/over-ceiling values at the storage boundary. Existing rows migrate with TTT disabled (A21/A30).
  - RED: migration test asserting columns/defaults/CHECKs and legacy-row preservation.
- 1.2 Server env config `graph_memory_config.go` (service): defaults + hard ceilings for every tunable; workspace values clamp/reject above ceilings; env-dispatch may select training mode only (D22). Provider allow-list for Dive override (D24).
  - RED: unit tests for default resolution, ceiling enforcement, invalid override rejection.
- 1.3 sqlc queries in `server/pkg/db/queries/graph_memory.sql`: profile read returns all new columns; CAS update `WHERE workspace_id=$1 AND config_version=$2` returning conflict on 0 rows. Run `make sqlc`, check drift.
  - RED: sqlc drift + query tests against test DB: stale full-row write returns conflict (spec §16), concurrent update loses nothing.
- 1.4 Profile handler (`graph_memory_profile.go`): expose new fields; ETag/config-version on PUT; numeric validation rejects non-finite/negative/over-ceiling before persistence (A31); switching memory_type keeps existing confirm-empty-start contract.
  - RED: handler tests for new fields round-trip, stale ETag 409, over-ceiling 400.
- 1.5 Frontend schema strictness (`packages/core/api/schemas.ts`): remove `.catch("legacy")` on `memory_type`; malformed/unsupported Graph profile state surfaces a parse error (spec §16). API client profile types updated.
  - RED: vitest in `packages/core` for malformed profile rejection (no silent legacy coercion).

## Phase 2 — Durable recall lifecycle, K barrier, traversal state

Spec §1, §3, §4, §14; brief D1, D3, D4, D5, D27, D28; acceptance A1–A4, A13–A15, A22–A24.

- 2.1 Migration `419_graph_memory_recall_ledger.up/down.sql`: tables for recall (server-issued id, canonical workspace/task/daemon/runtime ids, graph identity, pinned version, status, idempotency key), trajectory (recall id, seed index, status found/miss/error/budget, summary, viewed_node_ids[], submitted_node_ids[], server-counted rounds, model/runtime metadata, artifact ref, schema_version), expansion batch (trajectory id, expansion_id, round, anchor node, candidate ids[], idempotency key, unique per request key), view events (batch id, node id, distinct-view accounting), submission (one per trajectory, CAS), version pin lease. FKs enforce tenant/graph-kind consistency (spec §16).
  - RED: migration + storage-consistency tests (cross-tenant/foreign-kind inserts rejected).
- 2.2 `GraphMemoryRecallService` (new, `server/internal/service/graph_memory_recall.go`): canonical task load, effective profile resolution, routing/authorization/graph identity/view resolution, training-mode resolution (spec §5), single version pin for the lifecycle, hybrid retrieval seeds as expansion round 0, K = 1 when `ttt_enabled=false` else saved per-recall K (A1), no workspace-wide semaphore (A22). Caller-supplied scope/version/profile/training fields are diagnostics only (A14).
  - RED: service tests with test DB — K resolution, caller-override ignored, failure non-fatal contract.
- 2.3 Traversal state rewrite in `memorygraph/explore_tools.go`: per-`/expand` unique `expansion_id` + persisted candidate membership; `/view` requires trajectory/expansion/node and validates batch membership + graph view (A3); distinct-view quota per batch with atomic reserve/commit and reservation release on failed load (A2, A24); idempotent re-view of same node (A2); viewed-anchor-only expansion with branching from any previously viewed node (A3); `/expand` request idempotency key — replay returns original batch without consuming a round, conflicting reuse rejected (A24); final allowed expansion distinguishes "no further expansion" from budget violation (spec §14).
  - RED: `explore_tools_test.go` cases per acceptance A2/A3/A24 including racing views for the last slot (admit at most one).
- 2.4 Submission authority: exactly one immutable server-side `/submit` per trajectory; `submitted_node_ids` ordered, unique, graph-view-valid subset of successful `viewed_node_ids`; identical replay idempotent, conflicting replay rejected; missing submission before terminal timeout = execution failure; model final JSON audit-only. `runTrajectory` persists from tool submission, not model final JSON (fact 2). Persist `viewed_node_ids` and `submitted_node_ids` separately (A4).
  - RED: explore_test.go — double-submit rejection, non-viewed submit rejection, model-JSON-cannot-override, timeout-without-submit = execution failure.
- 2.5 Adoption + injection: fewest server-counted rounds among successful trajectories; bounded summary + qualified citations returned without waiting for Dive (spec §3); recall failure never fails the business task; Graph mode keeps only permitted legacy user/agent memory, never project/channel/daily/workspace/team fallback (A15).
  - RED: service tests — adoption picks fewest rounds; injection bounded; failure path returns no injection and no legacy fallback.
- 2.6 Recall endpoint for daemon: authenticated, server-issued identities, scoped daemon capability; unknown/mismatched/stale/conflicting/finalized replays fail closed with zero provider calls/mutations/jobs/rewards (A23); 202 only after durable ledger commit (A25 first half).
  - RED: handler tests per A23 matrix.

## Phase 3 — Dive Judge, scoring/reward, sessions, outbox

Spec §5, §6, §7; brief D6–D9, D13–D15, D24, D26; acceptance A5–A9, A12, A25, A29.

- 3.1 Remove Outcome Judge and downstream-history scoring (`memorygraph/judge.go`, `service/graph_memory_judge.go`, `handler/graph_memory_judge.go` old paths). Dive Judge is the sole judge (D6). Keep legacy rows readable for the Phase 7 migration.
  - RED: compile-level removal + tests asserting old endpoint gone / replaced.
- 3.2 Durable Dive job queue (migration `420_graph_memory_dive_jobs.up/down.sql`): one job per recall after all K trajectories terminal (K barrier), trace-idempotent key, lease/attempt state, bounded retries; terminal infra failure → `judge_failed` + reward 0 for normal runs + no ground truth (A8). Crash-after-accept resumes from durable state (A25).
  - RED: recovery test — enqueue, kill worker mid-attempt, restart, exactly-one effective outcome.
- 3.3 Dive execution (`memorygraph/dive.go`): pins exact Explore graph version, never falls back to current; missing/corrupt version fails the job (A6). Budgets from profile (rounds 6, viewed 24, source files 4, timeout 10 min). Separately configurable model/provider, default inherits Explore, workspace override only within server policy (D24). Receives every normal run incl. found=false with summary/viewed/submitted/rounds/status (A5); Explore error/timeout/budget-violation bypass grading with reward 0 (A5).
  - RED: dive tests with fake model — version pin enforcement, input payload completeness, error-run bypass.
- 3.4 Scoring/reward (`memorygraph/reward.go` rewrite): relevance/groundedness/completeness in [0,1], overall=min, `reward = overall − w_round*rounds`, no clamp/threshold/pass-fail/fixed miss penalty; negative rewards allowed (A7). Incomplete Dive may supply scores/rewards but no authoritative ground truth (A9).
  - RED: reward unit tests incl. negative reward and min-dimension cases.
- 3.5 Online sessions + reward outbox: per-trajectory independent AReaL session for `online_rl` (A12); durable session id/proxy-key mapping surviving restart; reward delivery via durable outbox + CAS + idempotency; pending/key cleared only after durable terminal ack (fixes fact 4); opening intent/generation/reconciliation for StartSession (fixes fact 12); stale-session reaper owns eventual cleanup (no immediate export/remove). Proxy keys never in logs/API/artifacts/errors/metrics/model context/process args (A29).
  - RED: arealrl/reward_sink tests — transient 5xx retried, exactly-one effective reward, key cleared after ack, key canary absence scan.
- 3.6 Offline trajectories: PG holds identity/status/links/export eligibility; artifacts in durable object/shared store; export eligibility = complete + labeled `incomplete=true`; `judge_failed` audit-only, not trainable/exportable (A12, D26); no roster-agent impersonation for provider-call ledger (D15).
  - RED: export-eligibility matrix tests.

## Phase 4 — Immutable multimodal source layer, extraction, retention

Spec §10, §11, §15; brief D16–D20, D29, D30, D32; acceptance A16–A19, A26–A28.

- 4.1 Source layer (migration `422_graph_memory_source_layer.up/down.sql` + `memorygraph/source.go`): internal level −1 immutable segment/file source nodes; immutable ingest-owned `has_attachment` edges; management cannot update/delete; quotas not consumed (A16). Description/statement nodes start at level 0.
  - RED: immutability + quota-exemption tests.
- 4.2 Shared source store + per-version source watermark: versions see only sources at/before watermark (A19); file identity = graph scope + attachment ID; SHA-256 dedupes blob bytes only, never merges identity/permissions across scopes (A17).
  - RED: watermark visibility + cross-scope identity tests.
- 4.3 Atomic source publication: body/scope/provenance/extraction identity/commit marker visible atomically; partial/missing/corrupt scope quarantined, never project fail-open (fixes fact 6; A27); management derives scope from canonical provenance, only explicit authorized channel→project promotion, no invented channel identity.
  - RED: crash-injection test — duplicate/partial publication yields one committed record or quarantine.
- 4.4 Extraction pipeline: file source node created before description generation; unsupported/pending/failed never blocks ingest (A18); multiple controlled description kinds with extractor/provider/model/version/language/coverage/status/artifact ref; unknown kinds retained but not masquerading; immutable artifacts published before generation marked complete; generation-idempotent; retries/upgrades never overwrite prior generations (A32 first half); management edits versioned + op-logged retaining artifact ref.
  - RED: ingest/extraction tests per above.
- 4.5 Source media loading for Dive: server-owned tools only, no arbitrary URLs; scope/attachment/authorization/MIME capability checks; re-sniff independent of declared MIME; sandboxed parsers; cumulative compressed/decoded/recursion/page/duration/pixel/context limits; over-limit → audited description fallback with truncated/unsupported evidence state (A32).
  - RED: MIME-spoof, zip-bomb, page/pixel/duration overflow tests.
- 4.6 Retention leases + GC coordination: version pin opens durable lease covering retrieval/Explore/Dive/export/backtest consumers (A26); reads open one immutable graph+source snapshot (atomic LoadGraph, fixes fact 5); GC/source retirement/artifact cleanup/blob collection recheck durable references under cross-process lock/transaction; failed physical deletes retryable/idempotent (A26).
  - RED: lease-blocks-GC and atomic-snapshot tests.
- 4.7 Attachment/blob decoupling (`handler/file.go` + storage): blob identity separate from attachment row; clones share bytes via durable blob refs (fixes fact 7); user-visible deletion keeps bytes while source/version references exist; future authorized Dive loads raw media under scope/watermark/budget checks (A28, D32); final-reference retirement ends access; zero-ref collection locked/rechecked/exactly-once.
  - RED: clone/delete-order/shared-bytes/exactly-once-collection tests.

## Phase 5 — Information catalog, backtest, management limits

Spec §8, §9, §12; brief D10–D12, D21; acceptance A10, A11, A20, A31.

- 5.1 Information catalog (migration `423_graph_memory_info_catalog.up/down.sql`): persistent graph-scoped catalog, stable information IDs deduped across queries (D23); per-query links to required items; item = normalized fact + source segment/file/extraction refs + equivalent node IDs + rationale/evidence coordinates; Dive may add lower-level nodes no trajectory visited; duplicate nodes grouped in one item (A10).
  - RED: catalog dedupe/link/grouping tests.
- 5.2 Backtest semantics: AND across items, OR within item (A10); two-stage candidate matching — deterministic ID/provenance discovery, then bounded semantic confirmation that candidate fully expresses the fact; source overlap never passes (A11); item stays meaningful after management replaces node IDs.
  - RED: matching tests incl. provenance-overlap-rejected and node-replacement cases.
- 5.3 Backtest rewrite (`memorygraph/backtest.go`): only complete authoritative ground truth enters; incomplete/failed audit-only (A9); required-runner-missing blocks candidate promotion; empty cohort = unavailable, not perfect recall (fixes fact 8); all normal found+miss runs contribute mean/p95 rounds regardless of score (D10); captured-watermark pagination with stable identity dedupe; candidate gates unchanged + no cost-offset for missing information (spec §9).
  - RED: backtest tests for each fixed defect.
- 5.4 Management limits (`memorygraph/consolidate.go` / mutation paths): `max_hierarchy_fanout=8` on outgoing `summarizes` children; `max_relation_edges_per_node=8` total incident node-to-node degree counted at both endpoints; node-to-edge consumes source slot only; referenced edge no cap; source provenance edges exempt (A20, D21). Enforcement before mutation; rejection leaves graph unchanged + audit/rejection record (spec §12).
  - RED: limit-boundary tests (8th allowed, 9th rejected, both-endpoint counting, provenance exemption, audit record written).

## Phase 6 — Daemon thin client + frontend

Spec §1, §2; brief D1, D2; acceptance A13–A15, A21.

- 6.1 Daemon recall client (`server/internal/daemon/` + daemon module): call authenticated recall endpoint with task identity/query; inject bounded result only; failure non-fatal to business task; no legacy project/channel/daily/workspace/team fallback in Graph mode (A14, A15); env-dispatch `none`/missing → no Graph/Explore/Dive started (A13).
  - RED: daemon client tests with fake server — timeout/error injection, mode gating.
- 6.2 Evolution Center UI (`packages/views/evolution/components/graph-memory-cards.tsx`, `evolution-center-page.tsx`): Graph mode, explicit TTT switch (default off), TTT concurrency field; toggle-off forces effective K=1 while retaining saved K (A21); malformed profile surfaces error state (with 1.5). No management-TTT knob (D31).
  - RED: vitest component tests; then `pnpm lint && pnpm typecheck && pnpm test && pnpm react:doctor`.

## Phase 7 — Migration, security, observability, full verification

Spec §13, §16; brief D33; acceptance A23, A29, A30, A32.

- 7.1 Legacy migration: flat judge/query rows → `legacy_non_authoritative` audit-only; excluded from catalog/backtest/training export; no fabricated scores/views/submissions/groups (A30, D33); resumable/idempotent migration; schema/interpretation version on all persisted record types.
  - RED: interrupted-migration idempotency + legacy-row exclusion tests.
- 7.2 Private-channel lineage reads require channel membership or workspace admin with not-found semantics (fixes fact 11; A32); storage-level tenant/kind/owner/route/attachment/source consistency enforcement.
  - RED: cross-tenant/private-channel denial tests.
- 7.3 Observability: auditable status for recall traces, traversal, Dive attempts, evidence use/truncation, score dimensions, rewards, info-item authority, reward delivery, export eligibility, management rejections; secrets redacted on every surface; source tools fail closed; recall errors observable but non-fatal (spec §13).
  - RED: redaction canary scans + status/audit endpoint tests.
- 7.4 Full verification: `go test ./...` (server), `make sqlc` drift check, `pnpm lint`, `pnpm typecheck`, `pnpm test`, `pnpm react:doctor`; map every acceptance A1–A160 to passed/failed/blocked evidence; construct builder-handoff JSON (addressed ids, checks, known limits incl. pre-existing failures 4–5) and run `comet native next graph-memory-dive-judge --runner-input <file>` from the AReaL Comet worktree; then `comet native status graph-memory-dive-judge --details --json`.

## Dependency order

Task 0 → Phase 1 (schema/profile) → Phase 2 (recall ledger/traversal) → Phase 3 (Dive/reward) → Phase 4 (source layer) → Phase 5 (catalog/backtest/limits) → Phase 6 (daemon/frontend) → Phase 7 (migration/security/verification). Phases 4 and 5 depend on Phase 3's Dive output shape; Phase 6 depends on 2–3 API surfaces; Phase 7 spans everything.

## Execution mode

Subagent-driven per task: one agent per task runs test RED → implementation GREEN → self-review, in the Build worktree. Each task ends with its focused tests green plus no regressions in affected packages. Integration checkpoints after each phase run the full `memorygraph` + targeted `service`/`handler` suites.

## Build progress log

### 2026-08-18 — 3.1 Outcome Judge removed (D6: Dive Judge is the sole judge)

- RED: `cmd/server/graph_memory_judge_route_retired_test.go` asserted `POST /api/daemon/graph-memory/judge` absent and `POST /api/daemon/graph-memory/recalls` present; observed failing for the right reason (judge route still registered).
- Removed: `memorygraph/judge.go`+test (Judge/JudgeConfig/JudgeResult/HistoryProvider/Message), `memorygraph/reward.go`+test (RewardComposer/RewardParams), `service/graph_memory_judge.go`+test, `service/graph_memory_history.go` (judge-only downstream-history provider), `handler/graph_memory_judge.go`+test, `arealrl/reward_sink.go`+test (judge-only), daemon kicker chain (`graphMemoryJudgeKicker`, `notifyAsyncJudge`, `Client.KickGraphMemoryJudge`), protocol payloads (`GraphMemoryJudgeKickPayload`, `GraphMemoryExploreRunPayload`), main.go judge wiring + `RunRewardSweep`, router route, `Handler.GraphMemoryJudge` field, `QueryRecorder.ApplyJudge`, e2e smoke stage 4 (judge+reward).
- Kept legacy-readable: query-log `JudgeScore`/`JudgeDone` fields, audit `JudgedQueries24h` read path, `trace_writer.go` `RewardTraceRecord`/`AppendRewardTrace`, backtest query-log gating (decoupled from the deleted judge config: `JudgeThreshold` default now the literal 0.6; Phase 5 rebuilds backtests on Dive ground truth per spec §16). `BaselineSignal` moved next to `ComputeBaselineCoverage` in backtest.go; `extractJSONObject` moved into consolidate.go (its only remaining consumer).
- GREEN: `go build ./...` clean; `go test ./cmd/server -run TestGraphMemoryJudgeRouteRetired` ok; `go test ./internal/arealrl ./internal/memorygraph` ok (4.0s); `go test ./internal/service` ok (3.1s); `go test ./internal/handler -run 'GraphMemory|ResolveChannelRoute'` ok (13.0s); `go test ./internal/daemon` shows only the recorded load-dependent flake `TestWatchTaskCancellation_RunningTaskNotInterrupted` (passes 3/3 in isolation).

### 2026-08-18 — 3.2 Durable Dive job queue (migration 420)

- RED: `handler/graph_memory_dive_test.go` (4 tests) failed at compile: `undefined: service.NewGraphMemoryDiveService`.
- Migration `420_graph_memory_dive_jobs`: `graph_memory_dive_job` (one per recall via `UNIQUE (recall_id)` + `UNIQUE (workspace_id, trace_id)`, lease/attempt state, `max_attempts` default 4, `incomplete`, `result` jsonb) + identity trigger mirroring the recall's tenant/graph/pinned-version/trace; per-trajectory grading columns on `graph_memory_trajectory` (`dive_status`, three dimension scores + overall with [0,1] CHECKs, unclamped `reward`).
- `service/graph_memory_dive.go`: `EnqueueIfBarrierMet` (K barrier: all trajectories non-`running`; idempotent; no-op on terminal recalls; recall → `dive_queued`), `Lease` (queued-then-expired-lease order, `FOR UPDATE SKIP LOCKED`, attempts++, recall → `diving`; reaps expired leases with exhausted attempts into terminal failure), `Complete` (fenced on live lease; recall → `completed`), `Fail` (retryable re-queue while attempts remain; terminal → job `failed` + recall `judge_failed` + reward 0/`dive_status=judge_failed` on `found`/`miss` trajectories, empty result = no ground truth, A8).
- GREEN: all 4 tests pass (17.3s) — barrier/idempotency, terminal-recall skip, lease fencing + crash recovery (stale worker fenced, exactly-one terminal outcome, A25), bounded retries → judge_failed with reward 0 (A8). Migration applied cleanly by the handler TestMain migrator. `go build ./...` clean; gofmt clean on touched files (remaining `gofmt -l` hits are pre-existing HEAD files this change does not touch).

### 2026-08-18 — 3.3 Dive execution (memorygraph/dive.go)

- RED: `memorygraph/dive_test.go` failed at compile (`undefined: NewDiver/DiveConfig`).
- `Diver.Dive(ctx, query, version, runs)`: verifies the pinned version exists via `ListVersions` and loads cleanly (missing → "no fallback to current"; corrupt node file → dive failure — A6); partitions runs via `PartitionDiveRuns` (found/miss graded; error/budget/timeout bypassed, never in the prompt — A5); one-shot grading prompt carries query, pinned version, budget declaration, and each normal run's trajectory id/seed/status/server-counted rounds/summary/submitted ids plus viewed-node bodies from the pinned graph (grounding evidence, 400-rune cap); strict-JSON response validated (every normal run scored exactly once, no unknown/duplicate ids, all dimensions in [0,1] — violations are dive failures for the job layer's bounded retry); `Incomplete` plumbed (budget exhaustion preserves grading, blocks ground truth — A9); `Rounds` server-counted; `RawResponse` audit-only.
- `DiveConfig` (rounds 6 / viewed 24 / source files 4 / timeout 10min defaults; separate `Model` knob, D24) normalized like `ExploreConfig`.
- GREEN: partition, version-pin (missing/corrupt/intact), payload completeness + bypass exclusion, score-validation matrix (5 cases), incomplete flag, backend failure — all pass; full `memorygraph` suite 8.9s green; gofmt clean.

### 3.4 Scoring/reward rewrite — completed

RED: handler tests referenced `svc.ApplyDiveResult`, `sc.Overall()`, `memorygraph.ExploreReward` — all undefined (compile-fail RED confirmed before implementation).

Changes:
- `internal/memorygraph/reward.go` (new): `DiveTrajectoryScore.Overall()` = min(relevance, groundedness, completeness); `ExploreReward(overall, wRound, rounds)` = `overall - wRound*rounds`, unclamped (negative rewards allowed, A7).
- `internal/memorygraph/reward_test.go` (new): min-dimension selection across 6 case shapes; formula cases including negative rewards (-0.3, -2.0) and miss-trajectory parity.
- `internal/service/graph_memory_dive_reward.go` (new): `ApplyDiveResult(ctx, jobID, workerID, res, wRound)` — lease-fenced job load (FOR UPDATE, worker match), per-score `SELECT rounds ... FOR UPDATE` on the trajectory (foreign/non-normal ids rejected), Go-side Overall+ExploreReward with server-counted rounds, trajectory -> dive_status='graded' with 4 score columns + reward; bypassed runs -> dive_status='bypassed', reward=0 with RowsAffected enforcement; audit JSON (scores, necessary_information, incomplete, rounds; raw model response excluded) persisted to job.result; job completed + recall completed atomically in one tx. Incomplete=true preserves grading and rewards but is recorded on the job (A9 — authoritative ground-truth gating lands with the Phase 5 catalog).
- `internal/handler/graph_memory_dive_reward_test.go` (new, 3 tests): grading+reward math end-to-end (found rounds=3 -> reward 0.1; miss rounds=5 -> reward -0.3; error run bypassed reward 0); fencing (stale worker is a no-op, foreign trajectory rejected, live holder succeeds); incomplete=true still rewards (0.5 = 0.6 - 0.1*1) and persists job.incomplete.

GREEN evidence: `go test ./internal/memorygraph -run "TestOverallScore|TestExploreReward" -count=1` ok 0.154s; `go test ./internal/handler -run "TestApplyDiveResult" -count=1` ok 6.301s (3/3); gofmt clean on all touched files.

### 3.5 Online sessions + reward outbox — completed

RED: arealrl reward_sink tests (8) referenced `PendingReward`/`RewardStore`/`RewardSink`/`HTTPError`; handler tests (7) referenced `service.GraphMemoryRLSessionService` — all undefined (compile-fail RED confirmed via `go vet`).

Changes:
- `migrations/421_graph_memory_rl_session.up/down.sql` (new): `graph_memory_rl_session` — one row per trajectory (UNIQUE trajectory_id), opening-intent lifecycle ('opening'/'open'/'rewarded'/'closed'/'failed') with generation fencing, session_id/proxy_key, key_cleared_at. `graph_memory_reward_outbox` — one row per trajectory (UNIQUE trajectory_id), reward, status ('pending'/'delivering'/'delivered'/'failed'), attempts/max_attempts=8, next_attempt_at, delivered_at. Identity triggers mirror the trajectory's workspace/recall at the storage layer. NOTE: this takes migration 421; Phase 4's source layer renumbered 422, Phase 5's info catalog 423 (plan references updated).
- `internal/arealrl/client.go` (edited): `checkStatus` now returns typed `*HTTPError{Op, Status, Body}` so the sink can classify 429/5xx as transient and other 4xx as terminal; body snippets are documented as potentially containing echoed secrets (callers must redact, A29).
- `internal/arealrl/reward_sink.go` (new): `RewardSink` over a `RewardStore` interface + `*Client`. Exactly-one effective reward: re-delivery after a crash always replays the durable row's value; delivered/failed rows are never re-claimed. Transient failures retried with exponential backoff (ceiling 5min) up to maxAttempts (8); non-retryable 4xx and exhausted attempts fail terminally. Claimed rows with an empty proxy key fail without any HTTP call. `sanitizeRewardError` strips the proxy key from every stored/returned error (canary test: bridge echoes the key in its 500 body; neither the returned error nor stored last_error contains it).
- `internal/service/graph_memory_rl_session.go` (new): `GraphMemoryRLSessionService` implements `arealrl.RewardStore`. `OpenForTrajectory` — online_rl recall required (offline_capture/offline_rl rejected before any row is written); fenced opening intent persisted before the StartSession RPC (generation CAS), crashed-'opening'/failed rows reconciled by fencing a new generation; open/rewarded rows reused with no second RPC; closed rows error. `EnqueueReward` — first write wins (ON CONFLICT DO NOTHING), requires an open/rewarded session. `ClaimPending` — due pending rows + in-flight rows stale past a 2-minute window, `FOR UPDATE OF o SKIP LOCKED`, joins the session for the proxy key. `MarkDelivered` — outbox 'delivered' + session proxy_key cleared + key_cleared_at set + status 'rewarded' atomically in one tx. `MarkRetry`/`MarkFailed` — fenced to 'delivering' rows. `ReapStaleSessions` — rewarded sessions and stale open/failed sessions are torn down via RemoveSession (failed teardown leaves the row for the next cycle) and closed with proxy_key cleared; fresh open sessions untouched.
- Tests: `internal/arealrl/reward_sink_test.go` (8 tests: transient 5xx retried to success with attempt counting; exactly-one effective reward incl. crash-after-ack reclaim; key survives failures and clears only on ack; redaction canary; 4xx terminal; attempts exhausted; missing key fails without HTTP; typed HTTPError classification). `internal/handler/graph_memory_rl_session_test.go` (7 tests: open+reuse single RPC; reconcile failed/stuck-opening across generations; online_rl gating; enqueue idempotent + no-session rejection; deliver clears key atomically; claim CAS fencing + stale reclaim + retry bookkeeping; reaper closes stale/rewarded only and clears keys).

Fix during GREEN: `ReapStaleSessions` initially closed rows without clearing proxy_key (caught by TestGraphMemoryRLSessionReapStale) — close now also clears proxy_key and stamps key_cleared_at.

GREEN evidence: `go test ./internal/arealrl` ok 0.095s; `go test ./internal/handler -run "TestGraphMemoryRLSession|TestGraphMemoryRewardOutbox"` ok 5.4s (7/7); `go test ./internal/service` ok 1.7s; `go build ./...` clean; gofmt clean on all touched files (remaining `gofmt -l` hits are pre-existing HEAD files).

### 3.6 Offline trajectory export eligibility — completed (executed by cursor-agent/Grok 4.6, orchestrated+verified by builder)

RED: test file written first referencing `service.ClassifyOfflineExportEligibility` / `service.NewGraphMemoryOfflineExportService` (undefined before implementation). NOTE: the cursor-agent session lost its API connection after writing files (timeout exit), so Grok's own RED log was not captured; compile-fail RED is structurally guaranteed (the test file references symbols that did not exist) and all GREEN verification was rerun independently by the builder.

Changes (author: cursor-agent cursor-grok-4.6-high; review/verification: builder):
- `internal/service/graph_memory_offline_export.go` (new): `GraphMemoryOfflineExportService{pool}` with pure classifier `ClassifyOfflineExportEligibility(trainingMode, diveStatus, recallTerminal, jobCompleted, jobIncomplete)` implementing the spec §5/A12/D26 matrix — offline_rl+graded+completed job eligible (incomplete=true labeled but still eligible); judge_failed/bypassed/not-yet-graded excluded; online_rl/offline_capture wrong-mode excluded. `ListOfflineExports(ctx, workspaceID, limit)` joins trajectory+recall+(LEFT)dive_job, emits one NDJSON-style line per trajectory (eligible "trajectory" lines with full scores/reward/rounds/incomplete/artifact_ref/summary; "excluded" lines with ids+reason only), deterministic order by recall created_at + seed_index, limit clamped to 1000. Self-contained over graph_memory_* tables — never touches `pi_provider_call` (D15); the export line type has no session-id/proxy-key fields at all (A29 by construction).
- `internal/handler/graph_memory_offline_export_test.go` (new, 9 tests): table-driven classifier covering all 11 matrix cells; graded-complete export with exact score/reward values (reuses `ApplyDiveResult` with wRound=0.1: found rounds=3 → reward 0.1, miss rounds=5 → reward −0.3); graded+incomplete label; judge_failed via terminal dive Fail; bypassed error/budget/timeout; not-yet-graded; wrong-mode online_rl and offline_capture; D15 ledger-canary test (pi_provider_call count unchanged + serialized lines contain neither session-id nor proxy-key canaries).

GREEN evidence (rerun independently by builder): `go test ./internal/handler -run TestGraphMemoryOfflineExport -count=1 -v` — 9/9 PASS (2.3s); `go test ./internal/service` ok 2.2s; `go build ./...` clean; gofmt clean on both new files; `git status --porcelain` confirms only the two intended new files, no stray edits, no commits.

### 4.1+4.2 Immutable source layer + shared source store with watermarks (spec §10; A16/A17/A19; D16/D17) — DONE 2026-08-19

RED first (author: cursor-agent cursor-grok-4.6-high; review/verification: builder): `source_test.go` written first; `go test ./internal/memorygraph -run TestSource` failed to compile (`AppendSourceSegment`/`AppendSourceFile`/`LoadSources`/`SourceLayerLevel`/... undefined) before any implementation existed.

Changes:
- `internal/memorygraph/types.go`: `SourceLayerLevel = -1`, `SourceKindSegment/File`, `ExtractionPending/Unsupported/Failed`, `EdgeTypeHasAttachment`; `Node` gains omitempty source frontmatter (`source_kind`, `attachment_id`, `blob_sha256`, `mime`, `size_bytes`, `extraction_status`); `Manifest.SourceWatermark` (omitempty, 0 = pre-source-era / no sources visible — old manifests load unchanged).
- `internal/memorygraph/source.go` (new): shared append-only source store at `<graph root>/shared/sources/` (`nodes/*.md` yaml-frontmatter, `edges.jsonl`, `journal.jsonl` with per-store monotonic seq). `AppendSourceSegment`, `AppendSourceFile(SourceFileInput)` (idempotent by graph-scope+attachment ID; new UUID node ids; equal SHA-256 across scopes shares blob bytes only, never identity/provenance), `AppendSourceHasAttachment` (ingest-owned, deduped), `LoadSources(watermark)` (fail-closed on corrupt journal or journal-referenced missing nodes; an edge is visible only when both endpoints are within the watermark), `CurrentSourceSeq`. Quota-exemption plumbing (enforcement lands in 5.4): `IsSourceLayerNode`, `IsSourceProvenanceEdge`, `CountableHierarchyFanout`, `CountableRelationDegree` skip source-layer nodes/edges.
- `internal/memorygraph/store.go`: `Init` creates `shared/sources/`; `CreateVersionFrom` stamps the current journal seq into the new version's manifest (A19 watermark).
- `internal/memorygraph/consolidate.go`: `applyOne` guards every management mutation through `sourceLayerReject` — add/update/delete node targeting a level -1 node or any id colliding with a source id, delete/prune/update of a `has_attachment` edge, and management-created `has_attachment` edges are all rejected with `source_layer_immutable: ...`, graph unchanged, rejection recorded like any other management reject (A16). `OpUpdateEdge` named so it is rejected rather than silently unknown.
- `migrations/422_graph_memory_source_layer.up/down.sql` (new): `graph_memory_source` PG registry (identity, scope, blob metadata, per-graph `source_seq`, published/quarantined status) with UNIQUE (graph_kind, graph_owner_id, source_kind, source_node_id), partial UNIQUE (graph_kind, graph_owner_id, attachment_id) WHERE attachment_id IS NOT NULL, segment/file vs attachment_id CHECK, and an identity trigger mirroring `graph_memory_dive_job_validate_identity` (project graphs: channel_id NULL; channel graphs: channel_id == graph_owner_id; owner/agent/task must exist in the same workspace — spec §16 fail-closed even when app validation is bypassed). No Go writer yet (ingest wiring is a later task); no sqlc involvement.

Tests (new `internal/memorygraph/source_test.go`, 8 tests): append segment+file+has_attachment with correct level/kinds; watermark visibility (A at seq 1, version stamped 1, B at seq 2; LoadSources(1) sees only A; version created later carries watermark 2); idempotent file re-append (same node id, no second journal entry); cross-scope SHA-256 dedup keeps distinct identities; management immutability (all op classes rejected, graph unchanged); quota helpers; corrupt journal fails closed; old manifest without watermark loads zero.

GREEN evidence (rerun independently by builder): `go test ./internal/memorygraph -count=1` ok 13.3s (full suite, all pre-existing tests unmodified); 8/8 TestSource* PASS; `go build ./...` clean; gofmt clean on touched files; handler TestMain applies migration 422 cleanly against the shared test DB; `git status --porcelain` shows only the intended files on top of the existing change set; no commits.

### 4.3 Atomic source publication + scope quarantine (spec §15; A27; D30) — DONE 2026-08-19

RED first (author: cursor-agent cursor-grok-4.6-high; review/verification: builder): `source_publish_test.go` written first; `go test -run 'TestSourcePublish|TestSourceAudit|TestSourceQuarantine|TestSourcePromotion|TestSourceScope'` failed to compile (`testHookBeforeJournal`/`AuditSources`/`SourceSegmentInput`/`SourceAuditFinding`/provenance fields undefined) before implementation.

Changes:
- `internal/memorygraph/source.go`: two-phase publish for all source appends — phase 1 writes the complete node file to `shared/sources/pending/` and validates it by re-reading+parsing+id-checking; phase 2 `os.Rename`s into `nodes/` and appends the journal entry as the commit marker. Unexported `testHookBeforeJournal` crash hook fires at the phase boundary. Stale pending files are cleaned at the next prepare; orphan node files (no journal entry) stay invisible and are surfaced, never adopted. New `SourceSegmentInput`/`AppendSourceSegmentInput` (old `AppendSourceSegment(id, body)` kept as the empty-provenance form) and provenance fields on `SourceFileInput` (visibility/channel/agent/task). `resolveSourceProvenanceLocked` derives scope from the canonical `.graph_identity.json`: channel graphs default to channel visibility bound to the identity owner and reject project-visible sources outright; project graphs reject channel visibility and any channel_id; unknown visibility fails closed; provenance on a store without an identity marker fails closed (bare-root legacy test stores still accept empty provenance). `AuditSources()` walks journal+nodes+edges and records idempotent findings (`orphan_node`, `missing_node`, `corrupt_node`, `invalid_scope`, `dangling_edge`) to `shared/sources/quarantine.jsonl` — quarantined records are never visibility-widened; LoadSources keeps failing closed on journal-referenced missing/corrupt nodes. `PromoteFileSourceToProject(attachmentID, authorized)` is the only channel→project path: unauthorized or repeat calls fail; channel identity comes from the store identity/node, never the caller; success rewrites visibility via the same two-phase publish and appends a `promoted_to_project` audit line to `shared/sources/audit.jsonl`.
- `internal/memorygraph/types.go`: omitempty `promoted_from_channel_id` on Node.
- `internal/memorygraph/store.go`: `testHookBeforeJournal` field; `Init` creates `shared/sources/pending/`.

Tests (new `source_publish_test.go`, 10 tests): two-phase happy path (node lands in nodes/, journal entry, LoadSources sees it); crash before journal (invisible after reopen, next seq correct, pending cleaned); orphan node quarantined + idempotent re-audit; corrupt node fails LoadSources closed + audit finding; dangling edge finding + LoadSources exclusion; channel/owner mismatch append fails with nothing published; project graph rejects channel visibility; unknown visibility fails closed; provenance without identity marker fails closed; promotion requires authorization, is one-shot, records promoted_from_channel_id.

GREEN evidence (rerun independently by builder): `go test ./internal/memorygraph -count=1` ok 13.5s — all 10 new tests + all 8 prior source tests + every pre-existing package test PASS unmodified; `go build ./...` clean; gofmt clean on touched files; `git status --porcelain` shows only `source_publish_test.go` added on top of the prior change set; no commits.

### 4.4 Extraction + description-node pipeline (spec §11, §16; A18/D18, D19; A32 first half) — DONE 2026-08-19

RED first (author: cursor-agent cursor-grok-4.6-high; review/verification: builder): `extract_test.go` written first; `go test -run 'TestExtraction|TestDescription'` failed to compile (`RecordExtraction`/`ListExtractions`/`DescriptionKindCaption`/... undefined) before implementation.

Changes:
- `internal/memorygraph/extract.go` (new): `ExtractionInput{Kind, Extractor, Provider, Model, ModelVersion, Language, Coverage, Output, Status}`; `NormalizeDescriptionKind` (canonical `caption`/`ocr`/`transcript`/`extracted_text`; unknown kinds retained verbatim, never remapped — D19); `RecordExtraction(sourceNodeID, in)` — requires a published source (source-before-description ordering, A18), assigns gen = max+1, never overwrites an indexed gen (orphan unindexed artifact files are removed, never adopted), two-phase durability (artifact JSON written to `.tmp`, re-read+validated, renamed into `shared/sources/artifacts/<source>/<kind>/gen_N.json`, then the index line appended — a generation is complete only with artifact+index); failed/unsupported statuses are recorded as evidence, not error returns (A18/D18); validation errors (missing source, empty kind/extractor) write nothing. `ListExtractions` (gen order), `LoadExtractionArtifact` (corrupt/missing indexed artifact fails closed), `PublishDescriptionNode` (level-0 version node with nested omitempty `ExtractionMeta{source_ref, kind, kind_known, extractor, provider, model, model_version, language, coverage, generation, artifact_ref}`, fails for missing source/gen).
- `internal/memorygraph/types.go`: description-kind constants, `ExtractionCompleted`, `Node.Extraction *ExtractionMeta`.
- `internal/memorygraph/store.go`: `testHookBeforeExtractionIndex` crash hook (separate from the source-publish hook).
- `internal/memorygraph/consolidate.go`: update_node rejects clearing/changing `extraction.artifact_ref`/`source_ref`/`generation` on a description node (graph unchanged, RejectReason recorded); a body-only update that omits Extraction preserves the existing identity by copy-defaulting (management body edits stay allowed, versioned, op-logged — §11).

Tests (new `extract_test.go`, 9 tests): ordering gate; pending file source never blocks ingest (LoadSources returns it without any extraction); kind normalization table incl. unknown-verbatim; generation immutability (v1 artifact bytes unchanged after v2 recorded); crash between artifact and index → generation invisible, next gen correct; corrupt indexed artifact fails closed; failed/unsupported recorded with output preserved; description node round-trip + missing-gen failure; management identity guard (clear/change rejected, body-only edit succeeds).

GREEN evidence (rerun independently by builder): `go test ./internal/memorygraph -count=1` ok 14.4s (full suite, all pre-existing tests unmodified); 9/9 new tests PASS; `go build ./...` clean; gofmt clean on the five touched files; `git status` shows only `extract.go`/`extract_test.go` added; no commits.

### 4.6 Version retention leases + GC coordination + immutable snapshots (spec §15; A26, D29) — DONE 2026-08-19

Author: cursor-agent cursor-grok-4.6-high. NOTE: the Grok run aborted at the end (Cursor API `resource_exhausted` after connection retries, EXIT=1; no final report captured). All deliverable files were complete on disk; the builder reviewed every file and reran all verification independently (below). RED-first evidence was not captured for this task; the compile-fail RED is structurally implied (new APIs exercised by new tests) but only the GREEN state was observed.

Changes:
- `internal/service/graph_memory_lease.go` (new): `GraphMemoryLeaseService{pool}` with `AcquireVersionLease` (consumer_kind validated against recall|dive|export|backtest before SQL; workspace derived from project/channel owner lookup, unknown owner fails closed; idempotent per (kind, owner, version, consumer_kind, consumer_id) — an open row is reused, never duplicated), `ReleaseVersionLease` (released_at = COALESCE(released_at, now()); already-released is a no-op, unknown id fails closed), `OpenLeasedVersions` (map of versions with open leases). Internals go through a small `graphMemoryQuerier` pool-or-tx interface so Dive can acquire/release inside its own transaction.
- `internal/service/graph_memory_dive.go`: dive lease wiring (A26) — `EnqueueIfBarrierMet` acquires a consumer_kind='dive' lease for the recall's pinned (graph_kind, graph_owner_id, graph_version) with consumer_id = job id in the same tx as job creation; terminal `Complete`/terminal `Fail` release it in the same tx as the terminal update; retryable failures keep the lease.
- `internal/memorygraph/gc.go` (new): `GCWithPinned(keep, pinned)` — pinned versions are never collected even outside the keep window; current version never collected; `GC(keep)` delegates with an empty pinned set (existing tests unmodified). Cross-process `gc.lock` (O_CREATE|O_EXCL, {pid, ts}): fresh lock (<30min) → `ErrGCLockBusy`; stale lock reclaimed. Failed `RemoveAll` is logged and collected into a joined error while remaining versions are still processed — partial dirs stay collectible on retry (D29).
- `internal/memorygraph/snapshot.go` (new): `OpenSnapshot(version)` loads manifest + graph + `LoadSources(manifest.SourceWatermark)` under one lock into `GraphSnapshot{Manifest, Graph, SourceNodes, SourceEdges}`; missing version fails closed (A26 single immutable graph+source snapshot).

Tests: memorygraph `gc_test.go` (4 new): pinned kept outside keep window; busy lock refuses GC and deletes nothing, stale lock reclaimed; manifest-less partial version dir collected + rerun no-op; OpenSnapshot watermark visibility (v2 with watermark 1 sees only seq<=1 sources), missing version fails, mutating the returned graph does not leak into the store. handler `graph_memory_lease_test.go` (5 new): acquire idempotent per tuple (same id, one row), distinct consumers get distinct rows; release idempotent + unknown id error + OpenLeasedVersions open/released transitions; invalid consumer_kind rejected pre-SQL; dive wiring (enqueue → exactly one open dive lease with consumer_id = job id; terminal complete → released); dive lease fencing crash recovery.

GREEN evidence (rerun independently by builder): `go test ./internal/memorygraph -count=1` ok 13.2s (full suite unmodified incl. TestGCKeepsCurrent); 4/4 GC/snapshot + 5/5 handler lease tests PASS; dive/recall handler regression ok 11.7s; `go build ./...` clean; gofmt clean; no commits.

### 4.7 Attachment/blob decoupling + retention-coordinated collection (spec §15; A28, D32) — DONE 2026-08-19

RED first (author: cursor-agent cursor-grok-4.6-high; review/verification: builder): compile-fail RED observed (`service.NewGraphMemoryBlobService` / `Handler.GraphMemoryBlobs` undefined) before implementation.

Changes:
- `migrations/424_graph_memory_blobs.up/down.sql` (new): `graph_memory_blob` (blob identity separate from attachment-row identity: workspace FK, UNIQUE (workspace_id, storage_url), sha256, size, active/retired status + retired_at) and `graph_memory_blob_ref` (durable refs: ref_kind attachment|graph_source|graph_version, partial UNIQUE (blob_id, ref_kind, ref_id) WHERE released_at IS NULL). Identity trigger: ref workspace must equal blob workspace; graph_source refs must exist in graph_memory_source (same workspace), attachment refs in attachment (same workspace); target checks skip released rows so releases can land after a referrer disappears; graph_version refs are workspace-checked only.
- `internal/service/graph_memory_blob.go` (new): `RegisterBlob` (idempotent per workspace+url), `RetainBlob` (enum-validated, idempotent per open tuple), `ReleaseBlobRefsFor` (COALESCE release, zero-match no-op), `AttachmentURLShared` (any OTHER attachment row on the same URL — clone-shared bytes), `AttachmentBytesRetained` (registered blob owns the bytes), `CollectZeroRefBlobs(ctx, BlobDeleter, limit)` — per-blob tx with `pg_advisory_xact_lock(hashtext(blob_id))`, open-ref RECHECK inside the tx, idempotent physical delete via the injected deleter, status→retired in the same commit; deleter failure rolls back (stays active, retryable) and joins into the error while other blobs continue; retired blobs are never collected again (zero-ref collection locked/rechecked/exactly-once, D32).
- `internal/handler/file.go`: `DeleteAttachment` now calls `maybeDeleteAttachmentBytes` — physical delete only when the URL is neither clone-shared nor blob-retained; nil blob service keeps legacy unconditional delete; check errors SKIP the physical delete (fail safe: leaked bytes recoverable, deleted bytes are not).
- `internal/handler/handler.go`: `GraphMemoryBlobs *service.GraphMemoryBlobService` field, constructed in `New` when the pool exists.

Tests (new `graph_memory_blob_test.go`, 8): clone-shared delete skips physical delete (bytes survive for the sibling row); unshared+unregistered keeps legacy delete; registered blob skips physical delete; check-error fail-safe (closed-pool service → 204 with no physical delete); register/retain/release idempotency; collection exactly-once with counting fake deleter (1 delete, retired, rerun 0) + fail-then-succeed retry; recheck invariant (open ref never collected; ref retained after a failed collect blocks the next collect); identity trigger rejects cross-workspace graph_source refs.

GREEN evidence (rerun independently by builder): 8/8 new tests PASS; file-handler regression (`TestUploadFile|TestGetAttachment|TestListAttachments`) ok 7.2s; migration 424 applied by TestMain against the shared test DB (identity-trigger test exercised it); `go build ./...` clean; gofmt clean; `git status` shows only the intended files; no commits. Ingest-side RegisterBlob/RetainBlob wiring is deliberately out of scope (later phase).

### 4.5 Server-owned source media loading with hard limits (spec §16/§13; A32, D20) — DONE 2026-08-19

RED first (author: kiro-cli gpt-5.6-terra; review/verification: builder): compile-fail RED observed (`MediaLimits`/`MediaPayload`/`MediaLoader` undefined) before implementation. (Note: terra read the "never touch files outside the working directory" constraint literally and declined to write /tmp/report_45.md; its report content was captured from the run log instead.)

Changes (`internal/memorygraph/sourcemedia.go`, new, stdlib-only):
- `MediaLimits` + `DefaultMediaLimits()` (compressed 32MiB / decoded 256MiB / recursion 3 / pages 200 / duration 300s reserved / pixels 40M / context 1MiB; zero fields default up).
- `MediaLoader{Resolver, Limits}` — bytes come ONLY from the injected `MediaByteResolver` (handed a validated source node; the loader never constructs paths/URLs; no network, no os/exec).
- `Load(store, view, sourceID, watermark)` fail-closed chain: published level -1 source → GraphView.Allows scope → journal seq <= watermark → file-kind with attachment → declared-MIME capability gate (unsupported ≠ denied). Each denial returns State="denied" + AuditDetail (not a Go error); internal inconsistencies are Go errors.
- Re-sniff independent of declared MIME (http.DetectContentType on the first 512 bounded bytes): declared pdf without %PDF magic, or declared image/text with octet-stream/cross-family sniff → denied with both MIMEs recorded (D20).
- Cumulative streaming limits (A32): compressed cap via a bounded reader (overflow → truncated/compressed with whatever text was captured); zip/gzip decode via a streaming member pump with a cumulative decoded-byte budget (zip bomb → truncated/decoded, no OOM); nested archive depth cap (truncated/recursion); PDF page-count heuristic (documented /Type /Page marker counting) over MaxPages → truncated/pages; image dimensions via DecodeConfig only (never full Decode) over MaxPixels → truncated/pixels with dimensions retained; extracted text capped at MaxContextBytes → truncated/context.
- Over-limit is an evidence state, not an error: payload State="truncated" with capped content + TruncatedFields + AuditDetail (feeds the 4.4 extraction record Output); unsupported → State="unsupported". Denied and truncated loads append one `media_load` audit line to `shared/sources/audit.jsonl` (4.3 idiom).

Tests (`sourcemedia_test.go`, 9): text happy path; MIME spoof denied + audited; zip bomb (10MiB zeros, 1MiB decoded cap) truncated without OOM; nested zip-in-zip-in-zip recursion cap; PNG pixel overflow with dimensions; context cap; unsupported MIME is a state not an error; scope/segment-kind/watermark denials; empty declared MIME proceeds on sniffed type.

GREEN evidence (rerun independently by builder): 9/9 new tests PASS; full `go test ./internal/memorygraph -count=1` ok 11.6s (all pre-existing tests unmodified); `go build ./...` clean; gofmt clean; only the two new files added; no commits.

### 5.1 Management graph-shape limits (spec §12; A16 quota exemption) — DONE 2026-08-19

RED first (author: cursor-agent cursor-grok-4.6-high; review/verification: builder): two-stage RED observed — compile-fail (`cfg.MaxRelationEdges` undefined), then after adding only the config field, assertion failures (`applied = 1, want 0` etc.) before enforcement was wired.

Changes:
- `internal/memorygraph/consolidate.go`: `ConsolidateConfig.MaxRelationEdges` (default 8 via DefaultConsolidateConfig + normalized()); `applyOne` `OpAddRelationEdge` calls `rejectRelationDegree(g, &e, limit)` BEFORE mutation — node-to-node relations consume one slot at BOTH endpoints (`CountableRelationDegree` on From and To), node-to-edge relations consume one slot at the source only, the referenced edge has no reference-count cap; rejection leaves the graph unchanged and flows into the existing audit/reject record; the consolidator prompt now states the relation-degree cap alongside levels/fanout.
- `internal/memorygraph/graph.go`: `AddHierarchyEdge` fanout check now uses `CountableHierarchyFanout` (skips ingest-owned has_attachment provenance edges and source-layer children) instead of raw `len(childrenOf)` — source provenance consumes neither quota (A16/§12). Signature unchanged.
- `internal/memorygraph/source.go`: comment update marking CountableRelationDegree as the enforcement helper.
- `internal/service/graph_memory_config.go`: NOT changed (profile surface already carries MaxHierarchyFanout/MaxRelationEdgesPerNode with storage bound 64; this task is the memorygraph enforcement layer).

Tests (new `limits_test.go`, 7): node-to-node rejection when From is full; when To is full; node-to-edge source-slot semantics + no cap on edge refs (20 incoming refs fine); exactly-at-limit acceptance boundary; fanout exemption with hand-built provenance edges over the raw count; relation exemption (has_attachment edges don't consume degree); zero-config defaults to 8. No existing fixture needed adjustment.

GREEN evidence (rerun independently by builder): 7/7 new tests PASS; full `go test ./internal/memorygraph -count=1` ok 11.1s; `go build ./...` clean; gofmt clean; no commits.

### 5.2 Necessary-information catalog (spec §8, §16; migration 423) — DONE 2026-08-19

RED first (author: cursor-agent cursor-grok-4.6-high; review/verification: builder): compile-fail RED observed (`NewGraphMemoryInfoCatalogService`/`GraphMemoryInfoItem` undefined) before implementation.

Changes:
- `migrations/423_graph_memory_info_catalog.up/down.sql` (new): `graph_memory_info_item` (stable identity: UNIQUE (graph_kind, graph_owner_id, statement_hash); status authoritative|incomplete|judge_failed|legacy_non_authoritative; source_refs jsonb; schema_version), `graph_memory_info_item_node` (OR-equivalence node group, PK (item_id, node_id), added_by explore|dive|migration), `graph_memory_recall_info_item` (per-query required links, PK (recall_id, item_id) — catalog membership alone never makes an item required). Identity triggers: item graph owner must exist in the matching table of the same workspace; a link must match the recall's workspace AND (graph_kind, graph_owner_id).
- `internal/service/graph_memory_info_catalog.go` (new): `NormalizeInfoStatement` (lowercase/collapse-whitespace/trim → sha256 dedup hash); `UpsertDiveInformationItems` (unknown recall fails closed; existing item by hash → STABLE id reuse, node-id UNION via ON CONFLICT DO NOTHING, source_refs SQL-level unioned jsonb, first statement/rationale text preserved, status never downgraded on reuse; new item status authoritative or 'incomplete'; empty statements skipped; recall↔item link idempotent); `ItemsForRecall`; `BacktestEligibleItems` (authoritative only — incomplete/judge_failed/legacy_non_authoritative never enter backtests).
- `internal/service/graph_memory_dive_reward.go`: `ApplyDiveResult` upserts `result.NecessaryInformation` in the SAME tx as score application with `authoritative = !result.Incomplete`; empty slice no-op.
- `internal/service/graph_memory_lease.go`: `graphMemoryQuerier` gained `Exec` for in-tx catalog writes.

Tests (new `graph_memory_info_catalog_test.go`, 7): create via real ApplyDiveResult flow (2 items, links, node groups); dedup/stable-id across recalls with case/whitespace variation + node union + first-text-wins; membership≠required (unlinked recall doesn't see the item); incomplete items stored+linked but excluded from BacktestEligibleItems, no authoritative downgrade; identity trigger rejects cross-workspace link; legacy_non_authoritative excluded; NormalizeInfoStatement table test.

GREEN evidence (rerun independently by builder): 7/7 new tests PASS; regression (`TestGraphMemoryDive|TestGraphMemoryOfflineExport|TestGraphMemoryLease`) ok 6.3s; `go build ./...` clean; gofmt clean; migration 423 applied by TestMain (identity-trigger test exercised it); only intended files added; no commits.

### 5.3 Candidate-version backtest rewrite (spec §8/§9; fact 8, D10) — DONE 2026-08-19

RED first (author: kiro-cli gpt-5.6-terra; review/verification: builder): compile-fail RED observed for all 10 new memorygraph tests (`BacktestItem`, `BacktestQuery.Items`, `QueryBacktestStat.ItemsTotal/ItemsSatisfied/ItemMisses/ConfirmedNodeIDs` undefined) and the handler-side `AttachBacktestGroundTruth` tests before implementation.

Changes:
- `internal/memorygraph/backtest.go` (rewritten in place): `BacktestItem{ID, Statement, NodeIDs, SourceRefs}`; `BacktestQuery.Items` authoritative when present, legacy `RelevantNodes` resolves to single-node items preserving AND semantics; itemless queries skipped. Item satisfaction: AND across items, OR across `NodeIDs` against the n-hop top-k neighborhood (stage 1 deterministic); stage 2 semantic confirmation via injected `BacktestConfirmer.ConfirmNode(ctx, statement, node)` over sorted candidate IDs capped by `MaxConfirmationCandidates` (default 200), confirmer errors fail closed, nil confirmer = unsatisfied (fail closed); `SourceRefs` never satisfy an item. `QueryBacktestStat` gains `ItemsTotal/ItemsSatisfied/ItemMisses` (cap 20, stable miss IDs: catalog ID → `statement:<sha256>` → `item:<index>`) and `ConfirmedNodeIDs`. Empty cohort: `recallRates` returns ok=false → hard gate `no_eligible_backtest_ground_truth` (no more perfect-recall 1.0 on zero queries). `BacktestConfig.RequireFullBacktest` + nil runner → hard gate `full_backtest_runner_required`. D10: mean/p95 rounds over every successful full-backtest run (found or miss, judge-score-independent); coverage-only estimates excluded. `BacktestQueries` dedupes window entries by TraceID (first wins). `SelectWinner` verified cost-only over gate survivors (missing info is a hard gate, never cost-offset).
- `internal/service/graph_memory_backtest.go` (new): `GraphMemoryInfoCatalogService.AttachBacktestGroundTruth(ctx, graphKind, graphOwnerID, queries)` — snapshot watermark (max created_at of scope) + keyset pagination `(created_at, id)` page 500 + dedupe by recall id; adopted trajectory = lowest-seed found, else terminal with most rounds; ledger overrides BaselineRounds/BaselineFound/query text; only `authoritative` linked items attach (incomplete/judge_failed/legacy excluded); unknown trace IDs untouched; DB errors fail closed.
- `internal/memorygraph/consolidate.go`: `ConsolidateConfig.BacktestGroundTruth` hook invoked after `BacktestQueries`, error aborts consolidation; `recordRegressions` aligned for skipped itemless queries.
- `internal/scheduler/jobs_graph_memory.go`: `consolidateOneGraphWithPool` (old signature preserved as nil-pool delegate) wires the hook via `ReadGraphIdentity` + `NewGraphMemoryInfoCatalogService(pool)`; identity read failure aborts (fail closed); nil pool = legacy behavior.

Tests: memorygraph +10 (AND/OR semantics, legacy fallback, source-overlap-never-satisfies, stage-2 confirm + error-skip, nil-confirmer fail-closed, empty-cohort gate, RequireFullBacktest both modes, D10 rounds incl. miss runs, TraceID dedupe); handler DB +5 (attach with baselines, incomplete-only excluded, real 500-row keyset page boundary, unknown trace untouched, ledger-overrides-log). Existing tests modified: exactly one — `TestEvaluateCoveredQueryPassesWithoutRunner` rounds expectation 1→0 (D10: coverage estimates no longer feed round stats).

GREEN evidence (rerun independently by builder): `go test ./internal/memorygraph` ok 11.3s; `go test ./internal/handler -run GraphMemory` ok 17.3s; `go test ./internal/scheduler` ok 19.6s; `go build ./...` clean; `go vet ./internal/service` clean; 5.3 files gofmt-clean (8 gofmt-dirty files elsewhere are unformatted at base commit, untouched); HEAD still d91801925, no commits; only intended files changed.

### Task 6.1a — server-side synchronous recall execution + bounded injection (2026-08-19)

- Executor: kiro-cli gpt-5.6-terra (108 min), TDD (RED: missing `Handler.GraphMemoryRecallExecutor`/`service.NewGraphMemoryRecallExecutor`; then green).
- New `internal/service/graph_memory_recall_execute.go` (`GraphMemoryRecallExecutor`): pinned-version snapshot check → per-version HybridRetriever → ExploreConfig from plan tunables (Agents=plan.K, MaxRounds/ViewsPerExpansion from tunables, model from `MULTICA_PI_MODEL`) → `explorer.Explore` → persist AgentRuns to `graph_memory_trajectory` + recall `explore_terminal` in one tx → `EnqueueIfBarrierMet` → bounded injection (4000-rune summary + `…[truncated]`, 16 citations + `…and N more`, mirroring daemon caps). Replay path `LoadReplayInjection` reads terminal ledger rows only — zero provider calls; adopts found by (rounds, seed_index); re-qualifies nodes from the pinned version. Execution/provider failures terminalize trajectories as error data and never fail the HTTP request (A15).
- Handler `RequestGraphMemoryRecall` now executes synchronously and returns `found/summary/citations/rounds/injection`; main.go wires the executor with a PI backend factory (`MULTICA_PI_PATH`/`MULTICA_PI_MODEL`), duplicating the scheduler's PI construction deliberately (no new package).
- Reviewer fix (orchestrator): `persistRuns` wrote `error_kind = status` for ALL runs (found/miss polluted the error column); now `error_kind` stays '' except for error/timeout/budget. Verified: gofmt clean, build OK, `GraphMemoryRecallExecution` tests pass.
- Independent verification (verify61a.sh): GOFMT-CLEAN, BUILD-OK, 5/5 symbols, all 7 new execution tests + full handler GraphMemory suite (154s) + memorygraph + service suites green, HEAD unchanged d91801925, no commits.

### Task 6.1b — Dive worker + reward outbox flusher (2026-08-19)

- Executor: started on cursor-agent Grok 4.6; the agent endpoint hung mid-run ("Retry attempt 1" never recovers — observed 3x across 6.1b/6.2), so 6.1b was reassigned to kiro gpt-5.6-terra, which delivered.
- New `internal/service/graph_memory_dive_worker.go` (`GraphMemoryDiveWorker.RunOnce`): Lease (15-min conservative TTL) → load recall query/training_mode/trajectories → DiveConfig from profile tunables with D24 fail-closed model/provider override (both non-empty AND `ValidateDiveOverride`, else defaults) → DirForScope + VerifyGraphIdentity → `Diver.Dive` under a profile-timeout child ctx → fenced `ApplyDiveResult` (no longer terminalizes the job) → online_rl reward outbox re-read from the ledger for ALL terminal statuses (found/miss/error/budget/timeout, zero rewards included; idempotent via outbox trajectory uniqueness) → explicit `Complete`. Failures go through the retryable `Fail` path.
- New `internal/scheduler/jobs_graph_memory_dive.go`: `graph_memory_dive_worker` (1-min cadence, ≤32 jobs/tick, worker identity = scheduler RunnerID) and `graph_memory_reward_outbox` (`DeliverOnce(100)` + `ReapStaleSessions(24h, 100)`); main.go wires both, RL service/sink gated on `BridgeStubURL`.
- `memorygraph/dive.go` (untracked since 5.x) now forwards `DiveConfig.Model` into `agent.ExecOptions`.
- Three existing test files updated for the explicit `Complete` boundary (apply keeps the lease; Complete releases) — justified in kiro's report.
- Pre-existing time-bomb fixed by orchestrator: `TestDailySealIsImmutableAndLateEventsLandInOpenDaily` computed `yesterday` from wall clock, so `yesterday+2h` crosses midnight when the suite runs 22:00-24:00 CST (it passed at 18:52 CST during 6.1a verification and failed at 22:55 CST for 6.1b). Anchored to midday; memorygraph suite green.
- Independent verification (verify61b.sh): GOFMT-CLEAN, BUILD-OK, 7/7 symbols, focused dive-worker/scheduler tests OK, handler GraphMemory (110s) + service + scheduler + memorygraph suites all green, HEAD unchanged d91801925, no commits.

### Task 6.1c — daemon thin recall client (2026-08-19)

- Executor: kiro gpt-5.6-terra (cursor-agent/Grok abandoned after three observed "Retry attempt 1" endpoint hangs on 6.1b/6.2 — no output ever returns from that state).
- `pkg/protocol/messages.go`: typed `GraphMemoryRecallResponse` (+`GraphMemoryRecallCitation`) matching the 6.1a handler contract (status/replayed/k/graph_version/found/summary/citations/rounds/injection).
- `internal/daemon/client.go`: `Client.RequestGraphMemoryRecall` on a dedicated recall HTTP client with 15-min timeout (the default 30s client was too short for synchronous server-side Explore), workspace daemon token auth, typed `*requestError` for non-2xx.
- `internal/daemon/graph_memory.go`: `graphExecutionMemories` rewritten — local dir resolution/retrieval/Explore/injection-rendering/metrics deleted; one authenticated server recall per invocation; only `found=true` + non-empty injection creates the `MemoryContextForEnv`; every other outcome logs and returns nil with NO legacy fallback (A15); env-dispatch none/missing gating preserved (A13). `daemon.go`: obsolete `graphProvs` cache removed.
- Server follow-through: `GraphMemoryRecallExecutor.Execute` now best-effort appends the `daemon` query-log entry (trace id/query/version/node ids/rounds/found) that 5.3 `BacktestQueries` reads; failure is `slog.Warn` only.
- Test updates (justified in kiro's report): daemon-local renderer assertions deleted (rendering moved server-side), local-provider tests rewritten against httptest fake servers, happy-path handler test extended with the query-log assertion.
- Independent verification (verify61c.sh): GOFMT-CLEAN, BUILD-OK, symbols present, dead machinery (`graphMemoryProvider`/`prepareGraphExecutionMemory`/`graphDirsForTask`/`graphProvs`…) fully removed, no `NewExplorer` in daemon. handler GraphMemory + service + protocol suites green. Daemon suite shows only two load-dependent timing flakes (`TestWatchTaskCancellation_RunningTaskNotInterrupted` — known pre-existing; `TestRunRuntimePollerClaimsImmediatelyBeforeInitialOffset` — newly recorded, files untouched by this change); both pass 3/3 in isolation. HEAD unchanged d91801925, no commits.

### 2026-08-19 — 6.2 Evolution Center TTT UI (A21 frontend half)

- Execution history: two cursor-agent (cursor-grok-4.6-high) attempts died mid-run to the known `agentn.global.api5.cursor.sh` reconnect hang, but left substantial partial work (implementation + tests + locales + page composition). kiro-cli (gpt-5.6-terra) verified presence/completeness against the 6.2 contract instead of rewriting; final acceptance verification was run directly by the orchestrator.
- Implementation: `packages/views/evolution/components/graph-memory-cards.tsx` — `GraphMemoryTttCard` + `GraphMemoryTttEditor` (switch default off; concurrency input enabled only while TTT on, clamped 1..64; effective-K-1 hint while off); `graphMemoryTttUpdatePayload` sends the full profile incl. `config_version` for the 1.3/1.4 CAS contract; toggle-off retains saved `explore_agents` (effective K=1 is server-side, A21); 409 → profile-query invalidation + conflict toast, no silent overwrite; `isGraphMemoryParseFallback` distinguishes the strict-schema parse fallback from `EMPTY_GRAPH_MEMORY_PROFILE` (1.5) and renders an error state with retry and no editing controls; non-admins see disabled controls; `memory_type=legacy` renders nothing (no management-TTT knob, D31). Locales: 10 `graphTtt*` keys in both `en` and `zh-Hans` evolution.json. Page composition in `evolution-center-page.tsx`.
- Tests: `graph-memory-ttt-card.test.tsx` (7 scenarios, 240 lines) — default-off rendering + disabled concurrency + saved K + effective-K-1 hint; toggle-on edit/save payload exactness (all unchanged fields + config_version); toggle-off retains saved K=8; 409 conflict → invalidate + toast, no success toast; parse-error state (no switch/input/save); non-admin disabled; legacy renders nothing.
- GREEN evidence (orchestrator-run, host loadavg ~330/48): `vitest run graph-memory-ttt-card` from `packages/views` 7/7 pass; direct `npx eslint .` in packages/views ESLINT_EXIT=0 (0 errors, 13 warnings, none in touched files); `pnpm react:doctor` EXIT=0 with views 0 issues; `@multica/core` graph-memory tests 12/12 pass.
- Known limits recorded: `pnpm typecheck` fails only on pre-existing `research/components/research-session-page.tsx(526,36)` TS2448/TS2454 (bfa79612e, no graph-memory imports); root `pnpm lint` pipelines typecheck via turbo so it inherits that pre-existing failure while eslint itself is clean; `pnpm test` at repo root shows ~20 failures in unrelated perf/session/presence tests with 9–74s durations — load-induced timeouts on the overloaded shared host (each unrelated to graph memory; core graph-memory tests pass).

### 2026-08-19 — 7.1 Legacy migration to audit-only (A30, D33)

- RED: `memorygraph/migrate_legacy_test.go` failed at compile (`undefined: MigrateLegacyQueryLogs`); handler-side `graph_memory_legacy_migration_test.go` observed failing before marker/exclusion support existed.
- Implementation: `memorygraph/migrate_legacy.go` — `MigrateLegacyQueryLogs(store)` explicit (not startup) migration under the store mutex: per-query-log-window and `regression_set.jsonl` classification. Current-format records (info_items / ledger_id / trajectory_id present) and already-marked records are skipped; flat legacy rows (judge_done, no info items/ledger/trajectory links) get additive `legacy_non_authoritative` markers (`omitempty`, legacy-readable); undecodable lines, unknown shapes, and numeric-range violations (rounds ≤ 64, agent runs ≤ 16, judge_score finite within [0,1]) are quarantined to `legacy_migration/quarantine.jsonl` with SHA-256 hash dedupe — never fabricated into authoritative data (D33). Windows rewritten atomically (tmp + rename); per-source checkpoints in `legacy_migration/<window>.json`; reruns rescan-and-converge (idempotent, resumable after interruption). No scores/views/submissions/groups are synthesized (A30).
- Exclusions: `QueryRecorder.QueriesBetween` and backtest candidate loading skip `legacy_non_authoritative` records, so migrated rows never enter catalog/backtest/training export paths; regression-gate consumption of marked rows is blocked.
- GREEN evidence (verify71): gofmt clean, `go build ./...` OK; 6/6 focused tests pass (marking+backtest exclusion, regression marking + gate prevention, idempotent resume of a partial window, no-fabrication, quarantine of malformed/over-ceiling data, legacy-only store leaves no eligible ground truth); regression suites green — memorygraph 64.4s, service 3.1s, handler 97.9s (incl. legacy-migration handler test proving `ttt_enabled=false` default and saved `explore_agents` preserved); git status shows only the three new files, HEAD unchanged at d91801925.

### 2026-08-19 — 7.2 Private-channel lineage authorization + storage boundaries (fact 11, A32 second half)

- RED: `handler/graph_memory_lineage_test.go` extended with authorization/cross-tenant/kind-mismatch regression tests; observed failing — unknown channel returned 200 with the stable empty lineage payload, cross-tenant channel id returned 200 instead of 404.
- Implementation: `handler/graph_memory_lineage.go` rewritten onto raw pgx (sqlc remains broken at HEAD): resolves the requested channel inside the requested workspace first (cross-tenant id → 404); private ordinary-group channels (`kind='group'` without system key) require channel membership via `channelUserIsMember` or workspace owner/admin via `isWorkspaceOwnerOrAdmin`, else not-found semantics; route row is joined to the channel with identity binding (channel graph owner must be the channel itself; project graph owner must be the channel's project in the same workspace) — a route row that exists but fails identity binding answers not-found (fail closed); lineage rows are filtered to identity-consistent records and ANY invalid lineage row fails the whole response closed. System-keyed channels (#general) remain workspace-member visible.
- Pre-existing defect repaired (1 line, documented): `handler/channel_read_rewind.go` `isWorkspaceOwnerOrAdmin` queried the nonexistent relation `workspace_member`; corrected to the canonical `member` table (only `CREATE TABLE member` exists, migration 001). The defect made the admin-bypass path always error at HEAD; it is also used by `channel.go:489`. No production invariant bypassed.
- Fixture deviation (documented by executor, accepted): a literal "public channel non-member" state cannot be constructed — the system `#general` roster trigger auto-maintains membership for every workspace member and rejects removal (`system_general_roster_managed`); the public-channel test verifies normal workspace-member 200 access instead.
- GREEN evidence (verify72): gofmt clean; `go build ./...` OK; focused `GraphMemoryChannelLineage` 4/4 pass (original + private-authorization + public-access + cross-tenant/kind-mismatch); regression `go test ./internal/handler -run 'GraphMemory|ResolveChannelRoute'` 39.7s and `go test ./internal/service` 2.0s green; HEAD unchanged at d91801925, only the three intended files touched.
- Residual note: project-kind lineage rows are bound to "a project in the same workspace" rather than specifically the channel's own project; cross-project-same-workspace lineage references are outside the specified 6-test matrix and remain a theoretical gap for a future hardening pass.

### 2026-08-19 — 7.3 Observability + secret-redaction canaries (spec §13, A29, A145, A146)

- Execution note: kiro (gpt-5.6-terra) completed all implementation and test iterations but the CLI died at the very end (network dispatch failure to runtime.us-east-1.kiro.dev, then a tool-approval prompt under `--no-interactive`) before writing its report; acceptance was completed by direct orchestrator verification (verify73) plus diff review.
- Auditable status (A145): `service/graph_memory_audit.go` gains a PG-ledger section in `GraphMemoryAuditSummary` — recalls by status and error_kind, trajectories by status/dive_status, graded-trajectory count with overall reward min/avg, dive jobs by status with attempts and redacted last-failure kind/message, reward outbox by status with oldest-pending age, plus export-eligibility/info-item-authority/management-rejection observability wired through the same summary (see `handler/graph_memory_audit_ledger_test.go`).
- Management rejection audit: rejections in `Consolidator.applyOperations` are now durably appended to the op log as `rejected_management` entries with operation + reason (previously only in-memory `RejectReason` results). Two pre-existing consolidate tests asserting exact op-log entry counts were updated to expect the additional rejection entries — the change strengthens (does not weaken) the assertions; rejection semantics unchanged. Focused proof: `memorygraph/management_rejection_audit_test.go`.
- Redaction (A29/A146): `diagnosticlog.SanitizeText` exported (behavior unchanged; `sanitizeText` delegates). New `service.RedactGraphMemoryObservability` wraps it for graph-memory surfaces. Applied at: dive worker `Fail` path (job error messages), audit summary last-failure message, arealrl reward-sink error construction (`SanitizeText` on HTTPError-bearing messages). Canary tests plant `sk-live-CANARYKEY…` style secrets and assert absence + redaction marker: `arealrl/reward_sink_canary_test.go`, `handler` `TestGraphMemoryRedactionCanary`.
- Source tools fail closed (A146): `memorygraph/source_fail_closed_canary_test.go` proves scope-violation and resource-limit loads produce audited `MediaEvidenceDenied`/`MediaEvidenceTruncated` fallback states and never silent content.
- Recall errors observable + non-fatal: covered by the ledger error_kind counts in the audit section + existing 6.1c daemon non-fatal tests (no duplication).
- GREEN evidence (verify73): gofmt clean; `go build ./...` OK; 5/5 focused tests pass (TestGraphMemoryAuditLedger 0.82s, TestGraphMemoryRedactionCanary 0.54s, TestRewardSinkCanaryRedaction, TestManagementRejectionIsAppendedToOperationAudit, TestSourceToolFailClosedCanaryAuditsFallbacks incl. resource_limit + scope subtests); regressions green — memorygraph 6.7s, service 2.4s, arealrl 0.2s, handler GraphMemory 43.8s; HEAD unchanged at d91801925.

### 2026-08-19 — 7.4 Full verification + migration 425 FK-index repair

- Full server suite `go test ./...` (verify74_go): 60 packages ok; failures triaged below. New pre-existing/environmental failures recorded as known limits; one REAL defect in this change found and fixed.
- REAL defect (fixed, TDD via the existing failing test): migrations 419/421 added `ON DELETE CASCADE` FKs without supporting indexes — `graph_memory_recall.task_id` → `agent_inbox_event`, `graph_memory_rl_session.recall_id` → `graph_memory_recall` — tripping `cmd/migrate` `TestAgentDeleteCascadeFKIndexesCoverRecursiveDeleteClosureAndAreIdempotent` (agent hard-delete cascade would seq-scan the ledger). Fix follows the project's hook idiom: migration `425_graph_memory_fk_indexes.up.sql` is a `SELECT 1;` marker (CREATE INDEX CONCURRENTLY must be top-level), down drops both indexes; `cmd/migrate/main.go` registers `runAgentDeleteCascadeFKIndexesHook` for key `425_graph_memory_fk_indexes`, adds specs `idx_graph_memory_recall_task` + `idx_graph_memory_rl_session_recall`, and lists both in `agentDeleteIndexOptionalRelations` so fresh-deploy hook runs at 227/229 skip the not-yet-created tables. GREEN: both FK-index tests pass (0.91s/0.95s); post-425 regressions green — handler GraphMemory 70.0s, memorygraph 7.1s, service 3.0s, arealrl 0.2s; gofmt clean; build OK.
- Full-suite failures classified as NOT caused by this change (all in files this change does not touch; `git diff --stat` evidence):
  - Load flakes passing in isolation: `TestSupervisorDuplicateRestartRequestsCoalesceUntilReplacementStarts`, `TestRunRuntimePollerClaimsImmediatelyBeforeInitialOffset`, `TestWatchTaskCancellation_RunningTaskNotInterrupted`, `TestPreviewAndDeleteLegacyRuntimeSystemMessagesIntegration`, `TestAgentReminderOrdinaryIdentityMigrationPreservesOnlyAgentReminders` (57s isolated vs 257s under load), full `cmd/migrate` package hitting the 10-minute test timeout under host load (targeted tests pass).
  - Shared-dev-DB drift (proven): the database contains `default_agent_avatar_url`/`agent_assign_durable_avatar_on_insert` producing `agent-avatars/v3/...` URLs — the v3 literal exists in NEITHER this worktree's NOR the original checkout's migrations (both carry v2 in migration 314); installed by another tenant of the shared dev DB. Fails `TestAgentAvatar_DurableCreateAndVerifiedUpdateProvenance`, `_DirectInsertUsesSameDurableBoundary`, `_ConcurrentCreatesAndDirectInsertsAreComplete` (deterministic v2-vs-v3 mismatch, fails in isolation). Same drift class: `TestExpireStaleQueuedTasks` ("expected exactly 1 expired task, got 25" — other tenants' stale rows), `TestPendingRunnerLaunchDispatchUsesDesiredLaunchID` and `TestV6DirectorFailureValidation` (DB-projection/contract drift; packages untouched by this change).
- Frontend (verify74_fe): `pnpm lint` and `pnpm typecheck` fail ONLY on the pre-existing `research-session-page.tsx(526,36)` TS2448/TS2454 (turbo pipelines typecheck into lint); `pnpm test` TEST_EXIT=1 from `@multica/core` vitest "Failed to start threads worker" (host resource exhaustion; member-list test, not graph-memory; graph-memory core tests pass); `pnpm react:doctor` DOCTOR_EXIT=0. eslint direct run (6.2): 0 errors.
- Acceptance map A1–A160 finalized: all 160 items mapped to passed with per-item evidence (see handoff JSON known_limits for the environmental exceptions above); no failed/blocked items attributable to this change.
# Acceptance map A1–A160 — DRAFT (finalize after 7.3 + full verification)

Evidence keys: plan = `docs/superpowers/plans/2026-08-18-graph-memory-dive-judge.md` build progress log entries;
verifyNN = orchestrator-run acceptance scripts (/tmp/verifyNN.out on build container); test names as recorded in plan entries.

## brief.md items A1–A32

- A1 (K=1 when TTT off / K when on): passed — task 2.2 K-resolution service tests; 6.1a executor `Agents=plan.K` (verify61a); 6.2 UI effective-K-1 hint tests.
- A2 (distinct-view quota per batch, atomic reserve/commit, idempotent re-view): passed — task 2.3 explore_tools tests incl. racing last-slot (admit at most one).
- A3 (view membership in batch + graph view; viewed-anchor-only expansion with branching): passed — task 2.3.
- A4 (one immutable server-side submit; ordered unique viewed-subset; viewed/submitted persisted separately): passed — task 2.4 (double-submit rejection, non-viewed submit rejection, model-JSON-cannot-override).
- A5 (Dive receives all normal runs incl. found=false; error/timeout/budget bypass grading reward 0): passed — task 3.3 partition tests; 3.4 ApplyDiveResult bypass rows reward 0.
- A6 (Dive pins exact version; missing/corrupt fails; no fallback to current): passed — task 3.3 version-pin tests (missing/corrupt/intact).
- A7 (reward = overall − w_round·rounds, unclamped, negative allowed, no thresholds/pass-fail): passed — task 3.4 reward unit tests (incl. −0.3, −2.0).
- A8 (terminal Dive infra failure → judge_failed + reward 0 normal runs + no ground truth): passed — task 3.2 bounded-retries-to-judge_failed test.
- A9 (incomplete Dive: scores/rewards preserved, no authoritative ground truth): passed — task 3.4 incomplete=true test; 5.3 backtest ground-truth gating.
- A10 (AND across items / OR within item; catalog dedupe; duplicate nodes grouped): passed — tasks 5.1/5.2 matching tests.
- A11 (two-stage matching; source overlap never satisfies): passed — task 5.2 provenance-overlap-rejected tests.
- A12 (per-trajectory online session; offline export eligibility = complete + incomplete-labeled; judge_failed audit-only): passed — tasks 3.5/3.6 export-eligibility matrix.
- A13 (env-dispatch none/missing → no Graph/Explore/Dive started): passed — task 6.1c daemon mode-gating tests (verify61c).
- A14 (caller-supplied scope/version/profile/training fields diagnostics only): passed — task 2.2 caller-override-ignored tests; 2.6.
- A15 (recall failure non-fatal; no legacy project/channel/daily/workspace/team fallback in Graph mode): passed — task 2.5 failure-path tests; 6.1c daemon tests.
- A16 (level −1 immutable source nodes/edges; management cannot update/delete; quotas not consumed; descriptions start level 0): passed — task 4.1 immutability + quota-exemption tests.
- A17 (file identity = graph scope + attachment ID; SHA-256 dedupes blob bytes only): passed — task 4.2 cross-scope identity tests.
- A18 (unsupported/pending/failed extraction never blocks ingest): passed — task 4.4.
- A19 (per-version source watermark visibility): passed — task 4.2 watermark visibility tests.
- A20 (management limits fanout=8/degree=8 enforced pre-mutation; rejection leaves graph unchanged + audit record): passed — task 5.4 limit-boundary tests.
- A21 (TTT switch default off; toggle-off forces effective K=1 retaining saved K; existing profiles migrate TTT-disabled): passed — migration 418 default false (task 1.1); 6.2 UI tests 7/7; 7.1 handler legacy-migration test proves ttt_enabled=false + saved explore_agents preserved.
- A22 (K per recall, no workspace-wide semaphore): passed — task 2.2 (no semaphore; concurrent-recall test).
- A23 (unknown/mismatched/stale/conflicting/finalized replays fail closed with zero provider calls/mutations/jobs/rewards): passed — task 2.6 handler A23 matrix.
- A24 (`/expand` idempotency key replay/conflict; atomic distinct-view reserve/commit with release on failed load): passed — task 2.3.
- A25 (crash-after-accept resumes from durable state; 202 only after durable commit; fencing): passed — task 3.2 crash-recovery test (stale worker fenced, exactly-one outcome); 2.6.
- A26 (version pin opens durable lease; atomic graph+source snapshot; GC rechecks under lock; failed deletes retryable/idempotent): passed — task 4.6 lease-blocks-GC + atomic-snapshot tests.
- A27 (atomic source publication; partial/corrupt scope quarantined, never fail-open; no invented channel identity): passed — task 4.3 crash-injection publication test.
- A28 (attachment deletion keeps bytes while source/version references exist; zero-ref collection exactly-once): passed — task 4.7 clone/delete-order/shared-bytes/exactly-once tests.
- A29 (proxy-key canaries absent from all surfaces): passed — task 3.5 key-canary absence scans; 7.3 redaction canaries (TestRewardSinkCanaryRedaction, TestGraphMemoryRedactionCanary; verify73).
- A30 (migration idempotent/resumable; TTT-disabled migration preserves saved K; malformed/over-ceiling fail closed; legacy rows audit-only): passed — task 7.1 (6 focused tests, verify71) + handler legacy-migration test.
- A31 (required-runner-missing blocks promotion; empty cohort = unavailable; all normal runs contribute stats; watermark pagination dedupe; numeric validation): passed — task 5.3 backtest tests + 1.4 numeric validation tests.
- A32 (extraction generation-idempotent, artifacts before completion; MIME/bomb/overflow fail closed; private-channel lineage authz; storage tenant/kind enforcement): passed — tasks 4.4/4.5 + 7.2 (verify72).

## spec.md items A33–A160

- A33 (server-authoritative ownership; caller fields not authorization): passed — 2.2/2.6/6.1a/6.1c.
- A34 (recall failure non-fatal; permitted legacy user/agent memory only): passed — 2.5/6.1c.
- A35–A49 (profile exposes memory mode, ttt_enabled, K, explore rounds, nodes-per-expansion, fanout, relation degree, dive rounds/viewed/source-files/timeout, w_round, source media limits): passed — migration 418 + 1.4 handler round-trip tests.
- A50 (Evolution Center TTT UI; effective K=1 retains saved K; migrate disabled; K not a workspace semaphore): passed — 6.2 + 2.2.
- A51 (env defaults + hard ceilings; env-dispatch selects training mode only): passed — 1.2 config tests.
- A52–A62 (per-recall server steps: canonical task load, routing/authz/identity, training resolution, single version pin, hybrid seeds, 1-or-K trajectories, fewest-rounds adoption, bounded injection before Dive, persist + enqueue Dive after K terminal, injection never revised post-Dive): passed — 2.2/2.5/6.1a (verify61a execution tests).
- A63–A70 (server traversal state; seed batch round 0; expand anchor/round/expansion_id/candidate membership/version pin; view batch validation + distinct-view quota + idempotent re-view; branching from any viewed node): passed — 2.3.
- A71–A80 (per-run persisted identity, seed index, graph identity+version, summary, viewed ids, submitted ids, rounds, status, model/runtime metadata + artifact): passed — migration 419 + persistRuns (2.2/2.4, 6.1a).
- A81–A85 (training resolution: ordinary→offline capture; online_rl→per-trajectory session; offline_rl→persisted for export; none/missing→disabled): passed — 2.2 + 3.5/3.6 + 6.1c.
- A86 (durable session/key mapping; keys never in logs/API/artifacts/exceptions/metrics; cleared at terminal; stale-session reaper owns cleanup): passed — 3.5 (migration 421, reward sink canary tests).
- A87 (offline records server-authoritative; PG identity/status/links/eligibility; artifacts in durable storage; incomplete labeled; judge_failed not exportable; no roster impersonation): passed — 3.6.
- A88 (Outcome Judge + downstream-history scoring removed; Dive sole judge): passed — 3.1 (route-retired test).
- A89 (Dive model/provider separately configurable; default inherits Explore; override within policy): passed — 1.2 D24 allow-list + 6.1b diveConfig fail-closed override (verify61b).
- A90 (Dive async after K terminal; all normal runs incl. found=false; error/timeout/budget bypass reward 0): passed — 3.2/3.3/6.1b.
- A91 (Dive pins exact Explore version; missing/deleted/corrupt/identity-invalid fails; no fallback): passed — 3.3 + 6.1b VerifyGraphIdentity.
- A92 (Dive independent budgets; server-owned source tools w/ scope/attachment/authz/MIME checks; no arbitrary URLs): passed — 3.3 + 4.5.
- A93 (per-file/total bytes + page/duration/pixel ceilings; over-limit → audited description fallback; budget exhaustion → incomplete=true): passed — 4.5.
- A94 (durable trace-idempotent Dive jobs, bounded retries; terminal failure → judge_failed + reward 0 + no ground truth): passed — 3.2.
- A95–A99 (relevance/groundedness/completeness in [0,1]; overall=min; no thresholds/pass-fail/fixed miss penalty): passed — 3.3 score-validation matrix + 3.4.
- A100–A102 (unclamped reward, negative allowed; idempotent online delivery; offline storage with trajectory; incomplete no ground truth): passed — 3.4/3.5/3.6.
- A103 (all normal found+miss runs contribute mean/p95 rounds regardless of score; failures excluded): passed — 5.3 (D10).
- A104–A110 (persistent graph-scoped catalog; cross-query dedupe with stable IDs; per-query links; item = identity/statement/source refs/equivalent node ids/rationale+evidence coordinates; Dive-added lower-level nodes; duplicate grouping; AND/OR semantics): passed — 5.1/5.2.
- A111–A114 (item meaningful after node-ID replacement; two-stage bounded matching; source overlap never satisfies; incomplete/judge_failed audit-only): passed — 5.2/5.3.
- A115–A121 (backtest over complete authoritative items + regression set; coverage/retrieval+view/runner/result+rounds/regression checks; hard gates unchanged; no cost offset for missing information): passed — 5.3.
- A122–A126 (immutable level −1 segment/file source nodes + ingest-owned has_attachment edges; not management-editable; no quota consumption; level 0 descriptions): passed — 4.1.
- A127 (shared immutable source store; per-version source watermark): passed — 4.2.
- A128 (file source identity = scope+attachment; records blob identity/SHA/MIME/size/visibility/provenance; hash dedupes bytes only): passed — 4.2.
- A129 (blob retention references; user-visible deletion doesn't collect referenced bytes): passed — 4.7.
- A130 (file source node before description generation; extraction failure never blocks ingest, explicitly statused): passed — 4.4.
- A131–A138 (multiple controlled description kinds w/ extractor/provider/model/version/language/coverage/status/artifact ref; unknown kinds retained not masquerading; immutable artifacts preserved; management edits versioned+op-logged retaining artifact ref): passed — 4.4.
- A139 (Dive decides descriptions vs source media loading): passed — 4.5/dive.
- A140–A144 (separate server-enforced fanout/degree limits; both-endpoint counting; node-to-edge source-only; edge no cap; provenance exempt; enforced pre-mutation; rejection audit record): passed — 5.4.
- A145 (auditable status: recall traces, traversal, Dive attempts, evidence use/truncation, score dimensions, rewards, info-item authority, reward delivery, export eligibility, management rejections): passed — 7.3 audit ledger section (TestGraphMemoryAuditLedger) + durable `rejected_management` op-log entries (TestManagementRejectionIsAppendedToOperationAudit); verify73.
- A146 (secrets redacted all surfaces; source tools fail closed; recall errors observable + non-fatal): passed — 7.3 redaction canaries + TestSourceToolFailClosedCanaryAuditsFallbacks (scope + resource_limit) + 4.5; recall error_kind counts in audit section + 6.1c non-fatal tests.
- A147 (server-issued identities bound to canonical ids; scoped daemon capability; fail-closed replays with zero side effects): passed — 2.1/2.6.
- A148 (authoritative lifecycle ledger; durable commit before accept; idempotency keys + CAS/fencing; proxy cleared only after durable terminal ack): passed — 2.1/2.6/3.2/3.5.
- A149 (per-expand idempotency key; atomic view quota under concurrency; final-expansion vs budget-violation distinction): passed — 2.3.
- A150 (one immutable submit; ordered/unique/view-valid/viewed-subset; replay semantics; model JSON audit-only; missing submission = execution failure): passed — 2.4.
- A151 (durable retention lease; immutable snapshot reads; cross-process GC coordination with recheck; retryable idempotent deletes): passed — 4.6.
- A152 (atomic source publication; quarantine never fail-open; scope from canonical provenance; only authorized channel→project promotion): passed — 4.3.
- A153 (blob/attachment identity separation; shared bytes via durable refs; authorized post-deletion loads under scope/watermark/budget; exactly-once zero-ref collection): passed — 4.7.
- A154 (schema/interpretation version on persisted records; resumable idempotent migration; legacy audit-only; no fabrication): passed — 7.1 + schema_version columns (418/419).
- A155 (fail-closed promotion on missing runner/snapshot/capability/cohort; empty cohort unavailable; all normal runs in stats; watermark pagination dedupe; numeric boundary rejection): passed — 5.3 + 1.4.
- A156 (profile config-version CAS; stale write conflict; frontend parse-error surfacing; management-run refresh of status/audit/run queries): passed — 1.3/1.4/1.5 + 6.2 (409 invalidate).
- A157 (extraction idempotency identity; artifact published before complete; retries/upgrades never overwrite; re-sniff independent of declared MIME; sandboxed parsers; cumulative limits; audited truncation states): passed — 4.4/4.5.
- A158 (private-channel route/lineage reads require membership/admin with not-found semantics; storage-level tenant/kind/owner/route/attachment/source consistency): passed — 7.2 (verify72) + migration identity triggers (419/420/421).
- A159 (recall TTT affects recall Explore only; management concurrency separately named/server-bounded; no management TTT knob): passed — 6.2 (D31; no management-TTT control added).
- A160 (None.): passed — vacuous item, no requirement.

## Known limits / pre-existing (not caused by this change)

1. `TestEnsureWindy_*`, `TestQuickCreateIssue*TrustBoundary` — runtime-offline env-dependent (baseline item 4).
2. `TestPostResearchMessageClientRequestIDValidation` — 200 vs 201, unrelated path (baseline item 5).
3. `TestTeardownRuntimeWithoutActiveAgents_ProductionScaleSelfFKLookup` — 300k-row insert times out under shared-DB load.
4. daemon timing flakes (`TestWatchTaskCancellation_RunningTaskNotInterrupted`, `TestRunRuntimePollerClaimsImmediatelyBeforeInitialOffset`) — pass in isolation, load-dependent.
5. `packages/views` typecheck: `research-session-page.tsx(526,36)` TS2448/TS2454 from bfa79612e; root `pnpm lint` inherits via turbo pipeline.
6. `pnpm test` root: ~20 unrelated perf/session/presence load-timeouts on overloaded host; core graph-memory tests 12/12.
7. sqlc generate broken at HEAD (migrations 380–383 function rename); generated code hand-written in sqlc's exact shape, `queries/graph_memory.sql` kept as source of truth.
8. `TestDailySealIsImmutableAndLateEventsLandInOpenDaily` — time-of-day-dependent at HEAD; FIXED test-only in this worktree (midday anchor).
