# Universal Interaction DAG 与 Graph Memory Pipeline Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将所有 Workspace 的所有 Task 统一记录为 Workspace-scoped Interaction DAG，并为 Graph Workspace 交付安全的 Atom Search、DAG Explore、原子 Consolidation、迁移/删除和可审计训练 reward pipeline。

**Architecture:** 以 `UniversalInteractionDAG` 深模块作为 Task lifecycle 与所有下游投影的唯一 seam；PostgreSQL 保存 canonical Segment/Edge/cursor/outbox/watermark，Mixed-RL frozen DAG、Graph Atom、Search index、Graph version、training manifest 都是可验证投影。读路径按顺序开放：Universal DAG shadow → published Atom → typed Explore v2 → atom-aware Consolidation → migration/retraction → reward/training。

**Tech Stack:** Go 1.26、PostgreSQL 17、sqlc、Chi、现有 memorygraph file store、Pi/AReaL、TypeScript strict、TanStack Query、Vitest、pnpm/Turborepo。

**修订记录：**
- 2026-08-31（维护者批准）：合并 lrm/dev 后上游已占用迁移号 455–463（并存在两个上游 454）。已实现的 `454_universal_interaction_dag` 与 `464_universal_dag_edge_only_linkage` 保留原号；本计划全部未实施 migration 按原顺延规则整体 +10，由 455–463 改为 465–473（455→465 … 463→473）。spec 未引用迁移号，无需修订。后续实施时若上游再占号，按同一规则再次顺延并更新本文。
- 2026-09-01（维护者批准 D1 方案 a，规范适配）：合并带入两条上游过渡 seam 写 legacy-shaped segment（`192a16110` memory-agent run、`23db5dfae` LRM-1079 channel conversation），在 canonical 454 下 fail-closed（3 个 service 测试红）。维护者批准 canonical adapter 方案：新增 **Task 3B**（见下），不新增 migration、不改 spec 验收标准；其测试断言按 canonical 语义重写。Task 3B 完成前暂缓 Task 4，以避免与 projection 设计冲突。

## Global Constraints

- Spec：`docs/superpowers/specs/2026-08-27-universal-interaction-dag-graph-memory-pipeline-spec.zh-CN.md`，Acceptance Criteria 1–68 是本计划唯一产品验收标准。
- 当前 checkout 最新 migration 是 464；本计划未实施 migration 使用 465–473（2026-08-31 由 455–463 整体顺延，见修订记录），执行前若上游占号必须再次整体顺延并更新本文；已实现的 454/464 保留原号。
- `interaction_dag_segment/edge` 演进为唯一 live DAG SoT；`interaction_dag_run_segment/causal_edge` 只保留 frozen Mixed-RL projection，不建立长期 dual writer。
- Canonical Segment range 只使用 `task_messages.seq`；Channel/provider/stream event 不直接推进 cursor。
- Legacy→Graph 不自动回填；Graph eligibility 使用 `memory_type_at_event`。
- 原始 Channel Atom exact-channel；Project promotion只能生成派生node。
- Agent native v2必须双向协商`memory_explore_v2`；同一run不得混用v1/v2。
- Shadow hard caps：6 rounds、8 neighbors/expand、32 distinct segments、8 atoms/segment response、32 tool calls、32000 trajectory tokens、600秒wall clock，并取Workspace ceiling更严格值。
- Shadow retention：trajectory热存90天、加密归档1年、full query/Explore trace 30天；生产调整必须版本化。
- 新增/修改Go注释只用英文；运行`gofmt`；SQL query改动后运行`make sqlc`，禁止手改generated SQL。
- 不新增dependency；如实施证明必须新增，先修改spec/plan并获得用户确认，版本必须精确锁定。
- 当前工作区脏且分支落后上游；执行时先用`superpowers:using-git-worktrees`从目标集成基线创建干净worktree，不覆盖当前用户修改。
- 本计划不授权commit。若用户在执行时明确要求commit，每个Task通过review后仅stage该Task文件并创建独立commit；否则跳过所有commit步骤。
- `docs/engineering-principles.md` 当前缺失。开始 Task 1 前，仓库维护者必须恢复该文件或书面指定现有替代 SSOT；实施者不得自行创作全局工程原则。
- 已批准 spec/AC 不得在执行过程中由实施者修改。发现合同冲突时停止该 Task，另起显式 spec change 并获得批准后再更新本计划。

---

## Phase 0：Implementation readiness gate

### Task 0: Canonical rules、migration preflight 与 clean worktree

**Files:**
- Read only: `AGENTS.md`
- Read only: maintainer-restored `docs/engineering-principles.md` or the maintainer-designated replacement
- Create: `server/cmd/migrate/universal_dag_preflight.go`
- Create: `server/cmd/migrate/universal_dag_preflight_test.go`

**Interfaces:**
- Produces: signed-off implementation baseline and a read-only preflight report; no schema/data mutation.
- Blocks: every later Task until the rules SSOT exists and the preflight result is clean or has an explicitly approved quarantine manifest.

- [ ] **Step 1: Obtain maintainer rule decision**

Do not create `docs/engineering-principles.md` in this plan. Record the restored/designated path in the execution evidence and re-read repository rules before editing code.

- [ ] **Step 2: Create a clean worktree**

Use `superpowers:using-git-worktrees` from the maintainer-selected integration baseline. Do not copy unrelated dirty changes from `merge/graph-memory-agent-dev-20260825`.

- [ ] **Step 3: Write RED preflight tests**

Fixtures must include malformed/orphan `project_id`, duplicate run/range rows, invalid sequence ranges, missing task messages and legacy readable trajectory payload. Assert the command exits non-zero and emits counts/hashes only, never trajectory body.

- [ ] **Step 4: Implement read-only preflight**

The report classifies every legacy row as `mappable`, `resanitize_required`, `duplicate_conflict` or `unmappable`. There is no automatic “best effort” repair. Duplicate generation/range mapping is deterministic only when canonical task message ranges prove order; otherwise migration is blocked.

- [ ] **Step 5: Validate**

```bash
cd server && go test ./cmd/migrate -run TestUniversalDAGPreflight -count=1
go run ./cmd/migrate universal-dag-preflight --check-only
```

Expected: test fixtures fail closed; target DB proceeds only with zero unresolved conflicts or a separately approved quarantine manifest.

---

## Phase 1：Universal Interaction DAG canonical store

### Task 1: Workspace identity、generation cursor、outbox 与 canonical association schema

**Files:**
- Create: `server/migrations/454_universal_interaction_dag.up.sql`
- Create: `server/migrations/454_universal_interaction_dag.down.sql`
- Modify: `server/pkg/db/queries/interaction_dag.sql`
- Regenerate: `server/pkg/db/generated/interaction_dag.sql.go`
- Regenerate: `server/pkg/db/generated/models.go`
- Test: `server/internal/service/interaction_dag_universal_schema_test.go`
- Test: `server/cmd/migrate/universal_dag_migration_test.go`

**Interfaces:**
- Produces: Workspace-scoped Segment/Edge/cursor, durable publish outbox and canonical Segment→provider-call associations used by Tasks 2–22.
- Consumes: Task 0 clean preflight plus existing Project/Task/run/provider-call identities.

- [ ] **Step 1: Write failing schema and dirty-data migration tests**

Prove: Workspace required; Project/Channel nullable; `(workspace_id,agent_run_id,generation)` unique; range valid; visible action unique; cross-Workspace edge rejected; Segment close and outbox share a transaction; provider-call roles/ordinal are constrained. Run up/down against clean DB and fixtures containing orphan project, duplicate run/range, malformed legacy ID and legacy readable trajectory.

```go
func TestUniversalDAGSchemaAllowsUnscopedTaskAndRejectsCrossWorkspaceEdge(t *testing.T) {
    wsA, wsB := createWorkspace(t), createWorkspace(t)
    segA := insertUniversalSegment(t, wsA, pgtype.UUID{}, pgtype.UUID{}, 1)
    segB := insertUniversalSegment(t, wsB, pgtype.UUID{}, pgtype.UUID{}, 1)
    _, err := testPool.Exec(context.Background(), `
        INSERT INTO interaction_dag_edge(workspace_id,src_segment_id,dst_segment_id,type,edge_seq)
        VALUES($1,$2,$3,'continues',1)`, wsA, segA, segB)
    require.Error(t, err)
}
```

- [ ] **Step 2: Run the test and verify RED**

```bash
cd server && go test ./internal/service ./cmd/migrate -run 'TestUniversalDAGSchema|TestUniversalDAGMigration' -count=1
```

Expected: FAIL because Workspace/generation/cursor/outbox/association constraints do not exist.

- [ ] **Step 3: Implement migration 454**

Migration must:

- refuse to start unless Task 0 preflight has no unresolved orphan/duplicate/range conflict;
- backfill `workspace_id` through verified Project/Task ownership, never by parsing free-form IDs;
- assign deterministic generation from proven per-run sequence order; any ambiguous row aborts migration;
- add event-time scope, run/run-agent identity, generation, memory type/eligibility, close action, canonical action, visible action key, derivative, policy versions and timestamps;
- add `interaction_dag_task_cursor`;
- add `interaction_dag_publish_outbox` with request hash, attempts, lease and terminal status columns now—not in Task 5;
- add `interaction_dag_universal_provider_call(segment_id,provider_call_id,role,ordinal,run_id,run_agent_id,capture_id)` with `role in (owned,shared_producer,audit)`, one `owned` row and stable ordinal constraints;
- add per-Segment `provider_capture_status in (not_expected,pending,finalized,conflict)` and capture identity/version so trusted provider calls may attach after action commit without reopening Segment/outbox;
- make edge Workspace-scoped with monotonic `edge_seq` and types `continues|responds_to|delegates_to|mentions`;
- mark legacy rows `content_status=legacy_unverified` and make every resolver reject their old body. Re-publication must reconstruct from canonical `task_messages` and pass the new sanitizer; migration must never label legacy body `published` merely because bytes exist;
- keep only hash/audit metadata for an explicitly approved unmappable quarantine; no quarantined body is readable or training eligible.

- [ ] **Step 4: Add sqlc queries**

```sql
-- name: LockUniversalDAGTaskCursor :one
-- name: UpsertUniversalDAGTaskCursor :one
-- name: InsertUniversalDAGSegment :one
-- name: InsertUniversalDAGPublishOutbox :one
-- name: InsertUniversalDAGProviderCallLink :one
-- name: FinalizeUniversalDAGProviderCapture :one
-- name: MarkUniversalDAGProviderCaptureConflict :exec
-- name: GetUniversalDAGSegment :one
-- name: InsertUniversalDAGEdge :one
-- name: AllocateUniversalDAGEdgeSeq :one
-- name: ListUniversalDAGSegmentsByRun :many
-- name: ListUniversalDAGEdgesAtWatermark :many
```

- [ ] **Step 5: Regenerate and verify GREEN**

```bash
make sqlc
cd server && go test ./internal/service ./cmd/migrate -run 'TestUniversalDAGSchema|TestUniversalDAGMigration' -count=1
```

Expected: clean and verified legacy fixtures pass; dirty/ambiguous fixtures abort without exposing legacy body; up/down round-trip passes.

- [ ] **Step 6: Review checkpoint**

Inspect generated DDL/query plans and prove no legacy row silently changes Workspace, generation, content trust or provider owner. Do not commit without explicit user request.

### Task 2: `UniversalInteractionDAG` boundary state machine

**Files:**
- Create: `server/internal/service/universal_interaction_dag.go`
- Create: `server/internal/service/universal_interaction_dag_test.go`
- Modify: `server/internal/service/task.go`
- Modify: `server/internal/service/interaction_dag.go`

**Interfaces:**
- Consumes: Task 1 cursor/segment/edge queries.
- Produces:

```go
type DAGBoundaryKind string
const (
    DAGBoundaryInbound DAGBoundaryKind = "inbound"
    DAGBoundaryVisible DAGBoundaryKind = "visible"
    DAGBoundaryTerminal DAGBoundaryKind = "terminal"
)
type DAGCloseActionKind string
const (
    DAGCloseMessage DAGCloseActionKind = "message"
    DAGCloseReaction DAGCloseActionKind = "reaction"
    DAGCloseTerminal DAGCloseActionKind = "terminal"
    DAGCloseMetadataOnly DAGCloseActionKind = "metadata_only"
)

type DAGBoundaryInput struct {
    WorkspaceID pgtype.UUID
    Task db.AgentInboxEvent
    BoundaryKind DAGBoundaryKind
    CloseActionKind DAGCloseActionKind
    EndSeq int32
    ActionID pgtype.UUID
    ActionKey string
    ProjectID pgtype.UUID
    ChannelID pgtype.UUID
    RouteGeneration int64
    MemoryTypeAtEvent string
    RunID pgtype.UUID
    RunAgentID pgtype.UUID
    ProviderCaptureExpected bool
    ProviderCaptureCorrelationKey string
    Derivative bool
}

type DAGLinkageInput struct {
    WorkspaceID pgtype.UUID
    SourceSegmentID string
    TargetRunID pgtype.UUID
    Type string // continues|responds_to|delegates_to|mentions
    DurableEventID pgtype.UUID
}

type DAGBoundaryResult struct {
    SegmentID string
    Generation int64
    Closed bool
    StartSeq int32
    EndSeq int32
}

func (d *UniversalInteractionDAG) RecordBoundaryTx(
    ctx context.Context, q *db.Queries, tx pgx.Tx, in DAGBoundaryInput,
) (DAGBoundaryResult, error)
func (d *UniversalInteractionDAG) RecordLinkageTx(
    ctx context.Context, q *db.Queries, tx pgx.Tx, in DAGLinkageInput,
) error
func (d *UniversalInteractionDAG) AttachProviderCaptureTx(
    ctx context.Context, q *db.Queries, tx pgx.Tx, segmentID, captureID string, calls []ProviderCallAssociation,
) error
```

`RecordBoundaryTx` creates Segment metadata and publish outbox in the visible-action transaction, setting provider capture to `pending` only when trusted capture is expected. `AttachProviderCaptureTx` later verifies the correlation key, idempotently inserts `owned|shared_producer|audit` rows, and finalizes capture; conflicting replay sets `conflict` and freeze fails closed. Delegation/mention are linkage events, never values of `close_action_kind`; their durable action maps to a `message` close when it emits a visible message, otherwise it only creates the edge after both canonical anchors exist.

- [ ] **Step 1: Write table-driven RED tests**

Cover: multiple inbound before output; open-and-close first outbound; consecutive outbound; terminal empty metadata Segment; cancel/fail; duplicate action key; retry child new run; sequence gaps rejected; derivative flag.

```go
func TestUniversalInteractionDAGGenerationStateMachine(t *testing.T) {
    cases := []struct{name string; events []boundaryFixture; wantRanges [][2]int32}{
        {"batched inbound then output", []boundaryFixture{{inbound,1},{inbound,2},{visible,3}}, [][2]int32{{1,3}}},
        {"consecutive visible output", []boundaryFixture{{visible,1},{visible,2}}, [][2]int32{{1,1},{2,2}}},
    }
    // Execute every case through RecordBoundaryTx and assert generation/ranges.
}
```

- [ ] **Step 2: Run RED**

```bash
cd server && go test ./internal/service -run TestUniversalInteractionDAGGeneration -count=1
```

- [ ] **Step 3: Implement row-locked state machine**

Use one DB transaction and cursor row lock. Insert closed Segment, legal close action identity, provider capture expectation/correlation key and publish outbox before commit; any failure rolls all of them back. A later trusted capture attaches only through `AttachProviderCaptureTx` and cannot alter range/action/outbox. Linkage events use `RecordLinkageTx` and never masquerade as close actions. Do not call model/file/network code. Segment ID is opaque and deterministic from Workspace, run and generation; no parser may depend on delimiters.

- [ ] **Step 4: Replace one-task-one-segment guard**

Remove `SegmentIDForAgentRun` as lifecycle authority from `interaction_dag_seams.go`; retain it only for legacy read/backfill until Task 4. `FinalizeTerminalTaskSideEffects` delegates to `UniversalInteractionDAG` and handles unscoped tasks.

- [ ] **Step 5: Run GREEN and race-focused integration tests**

```bash
cd server && go test ./internal/service -run 'TestUniversalInteractionDAG|Test.*Terminal.*DAG' -count=1
```

Expected: PASS; concurrent duplicate boundary returns same generation.

### Task 3: Wire canonical Task message and visible-action seams

**Files:**
- Modify: `server/internal/handler/daemon.go`
- Modify: `server/internal/handler/agent_inbox.go`
- Modify: `server/internal/handler/agent_transport.go`
- Modify: `server/internal/handler/agent_action.go`
- Modify: `server/internal/handler/agent_transport_mixed_rl.go`
- Modify: `server/internal/service/task.go`
- Modify: `server/internal/service/provider_capture.go`
- Modify: `server/internal/service/interaction_dag_seams.go`
- Modify: `server/internal/service/provider_call_ledger.go`
- Modify: `server/pkg/protocol/messages.go`
- Modify: `server/internal/daemon/client.go`
- Modify: `packages/core/types/events.ts`
- Test: `server/internal/handler/universal_interaction_dag_message_test.go`
- Test: `server/internal/handler/universal_interaction_dag_transport_test.go`
- Test: `server/internal/handler/agent_inbox_test.go`
- Test: `server/internal/service/provider_capture_test.go`
- Test: `server/internal/service/interaction_dag_seams_test.go`

**Interfaces:**
- Consumes: `RecordBoundaryTx` from Task 2.
- Produces: canonical persisted inbound/visible boundaries for all task shapes, stable `visibleActionKey`/action association, run/run-agent identity and late trusted-capture `owned|shared_producer|audit` links with stable ordinal.

- [ ] **Step 1: Write RED tests at HTTP/TaskService seams**

Assert `ReportTaskMessages` persists redacted task messages but does not close on diagnostic/tool rows. Add production integration cases for `ReportAgentInboxMessages`, Agent Inbox inbound materialization, resident mid-turn send, transport message, reaction, standalone chat final, terminal/cancel/fail and duplicate action. Each durable visible action must create/identify its canonical `task_message`, close Segment and enqueue outbox in the same business transaction. Provider cases cover capture pending at action commit, later exactly one `owned`, ordered shared producers, audit-only calls, missing capture, identical replay and conflicting replay.

- [ ] **Step 2: Run RED**

```bash
cd server && go test ./internal/handler ./internal/service -run 'TestUniversalDAG.*(Message|Boundary|Unscoped)' -count=1
```

- [ ] **Step 3: Add protocol action metadata only where the producer knows it**

Add camelCase daemon/computer fields for new internal reports; preserve existing snake_case TaskMessage HTTP contract. Do not ask daemon to invent channel/comment UUIDs. The Server completion transaction supplies canonical action IDs and closes the Segment after visible action persistence.

- [ ] **Step 4: Wire every task creation, inbound materialization and visible output path**

Cover Agent Inbox, issue comment, channel/resident directed message, standalone chat, transport send, reaction, cancellation, failure, quick-create and system/cron Task. `ReportAgentInboxMessages` must materialize canonical `task_messages` before advancing the cursor. Visible message/reaction handlers call one `TaskService` helper inside their existing business transaction; no post-response hook may emulate atomicity. Repo-wide enumerate every `agent_inbox_event` creation and durable visible-action writer and pin each in the integration matrix.

- [ ] **Step 5: Attach trusted provider capture**

`provider_capture.go`/`agent_transport_mixed_rl.go` resolves the already-closed Segment through the stored action/correlation identity and calls `AttachProviderCaptureTx`. Missing pending capture remains fail-closed for frozen projection; identical replay is a no-op; role/ordinal/body-hash conflict marks the capture conflict. Durable delegation/mention calls `RecordLinkageTx`; it never writes an unsupported close action.

- [ ] **Step 6: Verify GREEN**

```bash
cd server && go test ./internal/handler ./internal/service -run 'TestUniversalDAG|Test.*TaskMessage' -count=1
```

### Task 3B: Canonical adapter for the two post-merge upstream staging seams

（2026-09-01 追加，维护者批准 D1 方案 a「规范适配」。合并带入的两条上游过渡 seam——`192a16110` 的 memory-agent run 记录与 `23db5dfae` 的 LRM-1079 channel conversation 记录——以 legacy-shaped `interaction_dag_segment` 为表征，在 canonical 454 schema 下结构性 fail-closed（`ck_segment_source_valid` 禁止 `memory_agent_run`；`task_owner` CTE 要求真实 Task；channel-scope 伪 project key 无 FK 匹配）。Task 3B 将两者改写为 canonical 表征并恢复其 staging feed，不新增 migration，不改 spec。）

**Files:**
- Modify: `server/internal/service/graph_memory_agent_run_segment.go`
- Modify: `server/internal/service/interaction_dag_seams.go`
- Modify: `server/internal/service/graph_memory_agent_run_segment_test.go`
- Modify: `server/internal/service/interaction_dag_gating_test.go`

**Steps:**

- [ ] **Step 1: RED — canonical 断言重写**

两个 memory-agent-run 测试与 channel conversation 测试的 legacy 断言（`project_id="channel:<id>"`、`trajectory_source="memory_agent_run"`、segment body 携带 NIMBUS）改为 canonical 断言：segment 存在且 `trajectory_source="task_messages"`、range `[1,1]`、`content_status="pending"`；memory-agent run 侧新增 synthetic Task（`agent_inbox_event`，id=runID，`terminal_outcome="completed"`）与 seq-1 `task_message`（evidence 内容）断言；NIMBUS 证据改由 task_message 与 staging 摘要文件承载。channel conversation 侧断言 seam 零 legacy 写入、仅按 canonical segment id 触发 `SegmentIngestHook`。

- [ ] **Step 2: memory-agent run canonical adapter**

`RecordSubmittedRun` 不再调用 legacy writer（删除 `RecordMemoryAgentRunSegment` 及其 clamp/scope 助手）。新增 `recordCanonicalRunSegment`：单事务内 materialize synthetic Task（`agent_inbox_event`，id=runID，reason=`channel_message`，status=`acked`，`terminal_outcome=completed`，agent=channel 托管 Memory Agent，context 标记 run 溯源；幂等 `ON CONFLICT (id) DO NOTHING`）→ 写入 seq-1 `task_message`（用户 evidence，`user_facing`）→ `UniversalInteractionDAG.RecordBoundaryTx` inbound（打开 [1,1]）→ terminal close（`DAGCloseTerminal`，close 后返回 canonical segment id）。触发器校验链（task/channel ownership、range 精确覆盖、`ck_segment_source_valid`）全部满足；`agent_run_id=runID` 使 `GetInteractionDAGSegmentByAgentRun` 与既有 dedup `SegmentIDForAgentRun` 天然兼容。staging feed 保持 direct-export `IngestChannelRun`，仅将 `SegmentExport.SegmentID` 换为 canonical segment id。

- [ ] **Step 3: channel conversation feed-only adapter**

`maybeRecordChannelConversationSegment` 改为写零路径：canonical segment 已由 Task 3 的 in-transaction terminal close 写入，该 seam 仅负责 staging feed——按 `SegmentIDForAgentRun` 解析 canonical segment id，经 seam message store 读取 `task_messages` 并序列化 allowlisted trajectory（同 `RecordLocalSegmentForEvent` 投影；legacy segment body 永不外露），以 canonical segment id 触发 `fireSegmentIngest`（task 路由自动解析 channel）。进程内按 segment id 去重（staging 不可变写入为跨重启 backstop）；无 canonical segment 时 no-op。project-scoped env-dispatch 路径（Task 4 范围）不动。

- [ ] **Step 4: GREEN 与验证**

```bash
cd server && go test ./internal/service -run 'TestGraphMemoryRunSegment|TestInteractionDAG_ChannelConversation' -count=1
cd server && go test ./internal/service -count=1
```

criteria：上述全部通过；full service suite 仅余 3 个 §7 预存豁免；`go vet`/`gofmt`/`git diff --check` 干净；不触碰 public schema。

### Task 4: Deterministic Mixed-RL frozen projection

**Files:**
- Create: `server/migrations/465_universal_dag_projection.up.sql`
- Create: `server/migrations/465_universal_dag_projection.down.sql`
- Modify: `server/pkg/db/queries/interaction_dag.sql`
- Create: `server/internal/service/interaction_dag_frozen_projector.go`
- Modify: `server/internal/service/provider_call_ledger.go`
- Modify: `server/internal/service/mixed_rl_edges.go`
- Modify: `server/internal/service/mixed_rl_freeze.go`
- Test: `server/internal/service/interaction_dag_frozen_projector_test.go`
- Test: `server/internal/service/interaction_dag_freeze_test.go`

**Interfaces:**
- Consumes: Universal Segment close action/provider-call association from Tasks 1–3.
- Produces: `ProjectRunSnapshot(ctx, runID)` and canonical mapping from Universal IDs to `interaction_dag_run_segment/causal_edge`.

- [ ] **Step 1: Write RED projection tests**

Test message/reaction/terminal kind, canonical action ID, finalized provider-call one-`owned`/shared rule, late capture replay identity, missing pending capture, conflicting capture, channel_message/reaction/session_continuation edges and projection mismatch freeze failure.

- [ ] **Step 2: Add mapping migration and sqlc queries**

Migration 465 adds canonical Universal Segment/Edge IDs to frozen projection and unique mapping constraints. Backfill old frozen rows by existing segment ID; mark unmappable rows for explicit audit rather than guessing.

- [ ] **Step 3: Implement projector**

Project only from structured fields; never parse trajectory text. `FreezeRun` requires expected provider capture to be `finalized` before projector count/hash and refuses pending/missing/conflict or inconsistent mappings.

- [ ] **Step 4: Remove long-term independent writer**

Route `ProviderCallLedger.InsertSegment/InsertCausalEdge` through Universal mapping for new runs. Keep old direct path only inside one migration/backfill command, then delete it in this Task.

- [ ] **Step 5: Validate**

```bash
make sqlc
cd server && go test ./internal/service -run 'Test.*(FrozenProjector|MixedRL|Freeze)' -count=1
```

### Task 4A: Server-authoritative provider/region policy

**Files:**
- Create: `server/internal/service/graph_memory_provider_policy.go`
- Create: `server/internal/service/graph_memory_provider_policy_test.go`
- Modify: `server/internal/memorygraph/embed.go`
- Modify: `server/internal/memorygraph/embed_test.go`
- Modify: `server/internal/memorygraph/retriever.go`
- Modify: `server/internal/memorygraph/dive.go`
- Modify: `server/internal/memorygraph/consolidate.go`
- Modify: `server/internal/memorygraph/backtest.go`
- Modify: `server/internal/service/graph_memory_dive_worker.go`
- Modify: `server/internal/service/graph_memory_agent_gateway.go`
- Modify: `server/cmd/server/main.go`

**Interfaces:**

```go
type MemoryProviderPurpose string
const (
    ProviderAtomize MemoryProviderPurpose = "atomize"
    ProviderEmbed MemoryProviderPurpose = "embed"
    ProviderRerank MemoryProviderPurpose = "rerank"
    ProviderDive MemoryProviderPurpose = "dive"
    ProviderConsolidate MemoryProviderPurpose = "consolidate"
)
type ResolvedMemoryProvider struct { Provider, Model, Region, PolicyVersion string }
func (r *MemoryProviderPolicyResolver) Resolve(ctx context.Context, workspaceID pgtype.UUID, purpose MemoryProviderPurpose) (ResolvedMemoryProvider, error)
```

- [ ] **Step 1: Write RED policy tests** for allowed provider/region, outage, disabled purpose, missing policy, forbidden fallback and two Workspaces with identical plaintext/hash.
- [ ] **Step 2: Implement one Server-authoritative resolver**. Provider outage returns purpose-specific degradation; it never silently switches provider/model/region.
- [ ] **Step 3: Require resolved policy at every external call seam**. Task 7 Atomizer must consume this resolver; Embedding/Reranker/Dive/Consolidation and backtest constructors cannot accept an unscoped network provider.
- [ ] **Step 4: Key caches by Workspace + policy version + provider/model/region + content hash**. Existing disk embedding paths based only on content hash are invalid for cross-Workspace reuse and must be migrated or ignored.
- [ ] **Step 5: Validate**

```bash
cd server && go test ./internal/service ./internal/memorygraph -run 'Test.*ProviderPolicy|Test.*Embed.*Workspace|Test.*NoProviderFallback' -count=1
```

---

## Phase 2：Durable publish、sanitization 与 Atom

### Task 5: Publish outbox、lease 与 terminal states

**Files:**
- Modify: `server/pkg/db/queries/interaction_dag.sql`
- Create: `server/internal/service/interaction_dag_publisher.go`
- Create: `server/internal/service/interaction_dag_publisher_test.go`
- Create: `server/internal/scheduler/jobs_interaction_dag.go`
- Create: `server/internal/scheduler/jobs_interaction_dag_test.go`
- Modify: `server/cmd/server/main.go`

**Interfaces:**
- Consumes: Segment/outbox atomically created in Task 2.
- Produces:

```go
type SegmentPublishStatus string
const (
    SegmentPending SegmentPublishStatus = "pending"
    SegmentProcessing SegmentPublishStatus = "processing"
    SegmentPublished SegmentPublishStatus = "published"
    SegmentRedactionFailed SegmentPublishStatus = "redaction_failed"
    SegmentRejectedScope SegmentPublishStatus = "rejected_scope"
    SegmentDeadLetter SegmentPublishStatus = "dead_letter"
    SegmentRetracted SegmentPublishStatus = "retracted"
)

func (p *InteractionDAGPublisher) PublishClaim(ctx context.Context, limit int) (int, error)
```

- [ ] **Step 1: Write RED tests** for claim/lease, stale reclaim, max 10 transient attempts in 24h, deterministic no-retry, DLQ replay and business terminal independence.
- [ ] **Step 2: Add claim/retry/replay sqlc queries over migration 454 outbox**; do not add a second outbox table or allow Segment-only commits.
- [ ] **Step 3: Implement worker/scheduler** using `FOR UPDATE SKIP LOCKED`; classify errors; never publish partial payload.
- [ ] **Step 4: Wire scheduler and health counters** without enabling Graph read paths.
- [ ] **Step 5: Validate**

```bash
make sqlc
cd server && go test ./internal/service ./internal/scheduler -run 'TestInteractionDAGPublish|Test.*DAG.*Outbox' -count=1
```

### Task 6: Six-field sanitizer 与 artifact externalization

**Files:**
- Create: `server/internal/service/interaction_dag_sanitize.go`
- Create: `server/internal/service/interaction_dag_sanitize_test.go`
- Modify: `server/internal/service/interaction_dag.go`
- Modify: `server/internal/service/interaction_dag_publisher.go`
- Reuse: `server/pkg/redact/`

**Interfaces:**
- Consumes: canonical `task_messages` range.
- Produces:

```go
type SanitizedTrajectory struct {
    Messages []SanitizedTaskMessage `json:"messages"`
    ContentHash string `json:"content_hash"`
    SanitizerVersion string `json:"sanitizer_version"`
    ArtifactRefs []string `json:"artifact_refs,omitempty"`
}

func SanitizeTrajectory(messages []db.TaskMessage, policy SanitizerPolicy) (SanitizedTrajectory, error)
```

- [ ] **Step 1: Write RED tests** with credentials in content/input/output, binary payload, shell/env dump, oversized output, artifact ref, deterministic hash and sanitizer panic/failure.
- [ ] **Step 2: Implement deterministic sanitizer** before any Atomizer/provider call. Never persist unredacted pipeline payload.
- [ ] **Step 3: Persist encrypted/safe payload only in final publish transaction**; metadata-only on redaction failure.
- [ ] **Step 4: Validate**

```bash
cd server && go test ./internal/service -run 'TestSanitizeTrajectory|TestInteractionDAGPublisher.*Redaction' -count=1
```

### Task 7: Stable Memory Atom extraction

**Files:**
- Create: `server/migrations/466_graph_memory_atom_projection.up.sql`
- Create: `server/migrations/466_graph_memory_atom_projection.down.sql`
- Modify: `server/pkg/db/queries/graph_memory.sql`
- Create: `server/internal/memorygraph/atom.go`
- Create: `server/internal/memorygraph/atom_test.go`
- Create: `server/internal/service/graph_memory_atomizer.go`
- Create: `server/internal/service/graph_memory_atomizer_test.go`
- Modify: `server/internal/service/interaction_dag_publisher.go`

**Interfaces:**
- Consumes: `SanitizedTrajectory` and event-time Segment scope.
- Produces:

```go
type Atom struct {
    AtomID string
    SegmentID string
    Body string
    Kind string
    SourceMessageSeqs []int32
    SourceTool string
    ToolTrustClass string
    ContentHash string
    ArtifactRef string
    Visibility string
    ChannelID string
    ProjectID string
    PublishSeq int64
}
```

- [ ] **Step 1: Write RED tests** for multiple facts, zero Atom, stable normalized hash, scope inheritance, invalid seq refs, trusted read-only vs mutation/unknown tool, fallback Atom and max budgets.
- [ ] **Step 2: Implement migration 466** with Atom tables plus `graph_memory_projection_outbox(segment_id,request_hash,route_generation,status,attempts,next_attempt_at,lease_owner,lease_expires_at)` and idempotency constraints.
- [ ] **Step 3: Implement strict Atomizer JSON contract**; LLM proposes body/kind/seq refs, Server stamps identity/scope/trust and obtains provider/model/region only through Task 4A resolver.
- [ ] **Step 4: Add one publish transaction** writing sanitized Segment payload + Atoms + Workspace `publish_seq` + durable Graph projection request atomically. If any write fails, none become readable. Task 8 may only claim this request; it may not infer work by scanning files/Atoms.
- [ ] **Step 5: Validate**

```bash
make sqlc
cd server && go test ./internal/memorygraph ./internal/service -run 'Test.*Atom' -count=1
```

### Task 8: Event-time Graph projection 与移除 best-effort ingest

**Files:**
- Create: `server/internal/service/graph_memory_projection.go`
- Create: `server/internal/service/graph_memory_projection_test.go`
- Modify: `server/internal/service/graph_memory_ingest.go`
- Modify: `server/internal/service/interaction_dag_publisher.go`
- Modify: `server/internal/service/graph_memory_route.go`
- Modify: `server/cmd/server/main.go`
- Test: `server/internal/service/graph_memory_ingest_test.go`

**Interfaces:**
- Consumes: leased `graph_memory_projection_outbox` row containing published Segment/Atoms and event-time route identity.
- Produces: idempotent `ProjectPublishedAtoms(ctx, projectionRequestID)`; no detached goroutine and no file/Atom scan as work discovery.

- [ ] **Step 1: Write eligibility matrix RED tests** for Legacy, Graph channel, Graph project-only, unscoped, derivative, Legacy→Graph switch, explicit backfill.
- [ ] **Step 2: Implement projector consumer** by leasing Task 7 projection rows; use event-time values only, validate route identity, keep channel Atom exact-channel, and mark success/retry/DLQ idempotently.
- [ ] **Step 3: Delete `fireSegmentIngest` as production availability path**. Keep compatibility only for test fixture construction if needed, not live writes. There is no fallback scanner that turns every published Atom into Graph work.
- [ ] **Step 4: Validate**

```bash
cd server && go test ./internal/service -run 'TestGraphMemoryProjection|TestGraphMemoryIngest' -count=1
```

### Task 8A: Synchronous retraction fence、reverse provenance 与 default-off read gate

**Files:**
- Create: `server/migrations/467_memory_retraction_gate.up.sql`
- Create: `server/migrations/467_memory_retraction_gate.down.sql`
- Modify: `server/pkg/db/queries/interaction_dag.sql`
- Modify: `server/pkg/db/queries/graph_memory.sql`
- Create: `server/internal/service/memory_retraction.go`
- Create: `server/internal/service/memory_retraction_test.go`
- Create: `server/internal/service/memory_read_gate.go`
- Create: `server/internal/service/memory_read_gate_test.go`
- Modify: `server/internal/memorygraph/store.go`
- Modify: `server/internal/memorygraph/source.go`
- Modify: `server/internal/service/graph_memory_blob.go`
- Modify: `server/internal/service/interaction_dag_publisher.go`
- Modify: `server/internal/service/graph_memory_offline_export.go`
- Modify: `server/internal/service/offline_trajectory.go`
- Modify: `server/internal/service/offline_trajectory_test.go`
- Modify: `server/internal/handler/offline_trajectory.go`
- Modify: `server/internal/handler/offline_trajectory_test.go`
- Modify: `server/internal/service/mixed_rl_freeze.go`
- Modify: `server/internal/service/task.go`
- Modify: `server/internal/handler/channel.go`
- Modify: `server/internal/handler/comment.go`
- Modify: `server/internal/handler/chat.go`
- Modify: `server/internal/handler/file.go`
- Modify: `server/internal/handler/issue.go`
- Modify: `server/internal/handler/project.go`
- Modify: `server/internal/handler/workspace.go`
- Modify: `server/internal/handler/env_dispatch.go`

**Interfaces:**

```go
type MemorySourceRef struct { WorkspaceID pgtype.UUID; Kind string; ID pgtype.UUID }
func (s *MemoryRetractionService) RetractSourcesTx(ctx context.Context, tx pgx.Tx, sources []MemorySourceRef, actor, reason string) error
func (g *MemoryReadGate) AuthorizeResolve(ctx context.Context, workspaceID pgtype.UUID, refs []MemorySourceRef) error
```

- [ ] **Step 1: Write RED deletion/property tests** spanning comment/task output, channel cascade, chat session, attachment, issue/project/workspace and env-dispatch cleanup. For each entry point assert business tombstone/delete, retraction registry and all known dependency-node quarantine commit together or all roll back.
- [ ] **Step 2: Implement migration 467** with one `memory_source_guard` row per canonical source, `retraction_registry`, pre-maintained reverse provenance, `quarantined_pending_recompute`, deletion audit and phase gates. Backfill guards before enabling mutation; future Segment/Atom publish upserts guards. All Atom/v2 Search/Explore/citation routes are DB-default `disabled`; only shadow comparison jobs can run before an approved gate transition.
- [ ] **Step 3: Replace direct cascade assumptions with canonical source-delete service calls**. In deterministic sorted source-key order, deletion locks guard rows `FOR UPDATE`, writes fences and quarantines the complete currently published reverse-provenance closure before business deletion. `DeleteChannel`, `DeleteChatSession`, `DeleteAttachment`, `DeleteComment`, task cleanup and parent cascade handlers call this in their existing transaction. Workspace bulk delete uses the same set-based procedure. No HTTP success is returned if fencing/quarantine fails.
- [ ] **Step 4: Fence every reader explicitly**: DB/file/blob/archive/citation/Search/Explore, old Graph version, Graph Memory offline export and Mixed-RL `OfflineTrajectoryService` all call the same registry before resolving body. Frozen NDJSON maps run/provider rows back to Universal Segment/source refs; retracted input returns `content_retracted`, never stored provider payload. Future training selection uses the same check. A helper alone is insufficient unless each resolver test proves invocation.
- [ ] **Step 5: Add fail-closed read gates** requiring retraction-path E2E plus cross-channel/sanitizer canaries. Tasks 9–16 may build and shadow their internals, but external/API/Agent/Consolidation/migration behavior stays unreachable until the required gate row is green.
- [ ] **Step 6: Validate**

```bash
make sqlc
cd server && go test ./internal/service ./internal/memorygraph ./internal/handler -run 'Test.*(Retraction|Delete.*Memory|MemoryReadGate)' -count=1
```

---

## Phase 3：Class-aware Search 与 typed Explore v2

### Task 9: Active Atom ledger 与 class-aware retrieval

**Files:**
- Create: `server/internal/memorygraph/atom_index.go`
- Create: `server/internal/memorygraph/atom_index_test.go`
- Modify: `server/internal/memorygraph/retriever.go`
- Modify: `server/internal/memorygraph/retriever_test.go`
- Modify: `server/internal/memorygraph/types.go`

**Interfaces:**
- Consumes: Graph version + active Atom snapshot at `publish_seq_max` + Task 8A `MemoryReadGate`.
- Produces: shadow-only class-aware retrieval until the `atom_search` gate is green:

```go
type SearchClass string
const (SearchGraphNode SearchClass = "graph_node"; SearchAtom SearchClass = "staging_atom")

type SearchHit struct {
    Ref MemoryRef
    Class SearchClass
    Score float64
    Components SearchScoreComponents
}

func (r *HybridRetriever) SearchAt(ctx context.Context, query string, view GraphView, publishSeqMax int64) ([]SearchHit, error)
```

- [ ] **Step 1: Write RED tests** proving consumed/retracted Atom exclusion, exact-channel filter, current-node-only default, 14-day shadow half-life component, BM25 fallback and deterministic class fusion.
- [ ] **Step 2: Split Graph and Atom candidate channels**; apply Workspace/scope, status and Task 8A retraction fence before scoring. Never treat missing Graph node as automatically visible staging.
- [ ] **Step 3: Implement deterministic fusion and optional bounded reranker seam**; reranker only sees already-authorized candidates and uses Task 4A provider policy.
- [ ] **Step 4: Put production result adoption behind DB `atom_search` gate**; disabled mode may emit aggregate shadow comparison only, never Atom IDs/body/citations to users or Agents.
- [ ] **Step 5: Validate**

```bash
cd server && go test ./internal/memorygraph -run 'TestHybridRetriever|TestAtomIndex|TestSearchAt' -count=1
```

### Task 10: Structured `MemoryRef` 与 Explore plan ledger

**Files:**
- Create: `server/migrations/468_memory_explore_v2.up.sql`
- Create: `server/migrations/468_memory_explore_v2.down.sql`
- Create: `server/internal/memorygraph/reference.go`
- Create: `server/internal/memorygraph/reference_test.go`
- Create: `server/internal/service/memory_explore_plan.go`
- Create: `server/internal/service/memory_explore_plan_test.go`
- Modify: `server/pkg/db/queries/graph_memory.sql`

**Interfaces:**
- Consumes: Task 9 Search, Task 8A read gate and v2 refs from this Task.
- Produces: persisted plan/watermarks only when `memory_explore_v2` gate is green; disabled mode cannot create a user/Agent trajectory.

```go
type MemoryRef struct {
    Kind MemoryRefKind `json:"kind"`
    ID string `json:"id"`
    GraphIdentity *GraphIdentity `json:"graph_identity,omitempty"`
    SegmentID string `json:"segment_id,omitempty"`
}

type MemoryExplorePlan struct {
    TrajectoryID string
    Graphs []PinnedGraph
    SegmentPublishSeqMax int64
    InteractionEdgeSeqMax int64
    Budgets ExploreBudgets
}
```

- [ ] **Step 1: Write RED tests** with IDs containing colons/slashes/unicode, forged graph identity, invalid field combinations, replayed start, rollover and disabled `memory_explore_v2` gate returning unavailable without persisting a plan.
- [ ] **Step 2: Implement strict validation/resolvers**; authorization comes from plan, never ref fields, and every resolve rechecks Task 8A retraction registry.
- [ ] **Step 3: Persist plan/watermarks/budgets** in migration 468; pin all graphs before returning seeds.
- [ ] **Step 4: Validate**

```bash
make sqlc
cd server && go test ./internal/memorygraph ./internal/service -run 'TestMemoryRef|TestMemoryExplorePlan' -count=1
```

### Task 11: Interaction DAG traversal、Evidence 与 hard budgets

**Files:**
- Create: `server/internal/service/memory_explore_v2.go`
- Create: `server/internal/service/memory_explore_v2_test.go`
- Modify: `server/internal/memorygraph/explore_tools.go`
- Modify: `server/internal/memorygraph/explore_endpoint_test.go`
- Modify: `server/internal/service/graph_memory_agent_run.go`
- Modify: `server/internal/service/graph_memory_agent_gateway.go`

**Interfaces:**
- Consumes: Task 9 Search, Task 10 plan/ref, Universal DAG watermarked reader and Task 8A read gate.
- Produces: gated `Start/Explore/Redirect/Submit/Checkpoint/Evidence/History` v2 methods with structured refs.

- [ ] **Step 1: Write RED contract tests** for disabled gate, Graph edges, Atom→Segment, sibling Atom, bidirectional DAG edges, consumed Segment, canonical replacement, restricted/retracted source, archived evidence, invisible endpoint and watermark stability. Every operation—not only Start—must fail closed if the phase gate or source fence changes.
- [ ] **Step 2: Add Server ledger budgets** exactly: 6 rounds, 8 neighbors, 32 distinct segments, 8 atoms/response, 32 tool calls, 32000 tokens, 600 seconds; apply stricter Workspace ceilings.
- [ ] **Step 3: Implement checkpoint rollover** with bounded prior and latest authorized plan; stale/retracted/migrated refs re-resolve.
- [ ] **Step 4: Implement Evidence** summary-first, bounded trajectory chunks, archive authorization and audit hook.
- [ ] **Step 5: Preserve v1 code path** behind explicit protocol generation, without branching authorization rules.
- [ ] **Step 6: Validate**

```bash
cd server && go test ./internal/service ./internal/memorygraph -run 'TestMemoryExploreV2|Test.*Evidence|Test.*Budget' -count=1
```

### Task 12: Agent native capability negotiation 与 daemon rollout

**Files:**
- Modify: `server/internal/service/graph_memory_agent_gateway.go`
- Modify: `server/internal/daemon/message_runtime.go`
- Modify: `server/internal/daemon/client.go`
- Modify: `server/internal/daemon/types.go`
- Modify: `server/pkg/protocol/messages.go`
- Modify: `server/internal/handler/daemon.go`
- Modify: `server/cmd/server/main.go`
- Test: `server/internal/daemon/graph_memory_agent_v2_test.go`
- Test: `server/internal/handler/graph_memory_agent_run_test.go`

**Interfaces:**
- Consumes: gated v2 methods from Task 11 and Task 8A Server gate state.
- Produces: bidirectional `memory_explore_v2` capability and run-level protocol generation fence; capability alone never authorizes a disabled Server path.

- [ ] **Step 1: Write RED tests**: both sides v2 + green Server gate→structured payload; either capability missing→v1; gate disabled→v2 unavailable rather than fallback exposure; active run cannot switch; old daemon remains functional.
- [ ] **Step 2: Add camelCase daemon/computer capability fields** and persist selected generation in Agent run claim.
- [ ] **Step 3: Update five native tool schemas/prompts** without changing operation names.
- [ ] **Step 4: Run daemon/service tests**

```bash
cd server && go test ./internal/daemon ./internal/handler ./internal/service -run 'Test.*Memory.*V2|TestGraphMemoryAgent' -count=1
```

### Task 13: Inject recall、external v2 API 与 core client

**Files:**
- Modify: `server/internal/service/graph_memory_recall.go`
- Modify: `server/internal/service/graph_memory_recall_execute.go`
- Create: `server/internal/handler/memory_explore_v2.go`
- Modify: `server/cmd/server/router.go`
- Modify: `packages/core/api/schemas.ts`
- Modify: `packages/core/api/client.ts`
- Modify: `packages/core/api/graph-memory-schemas.test.ts`

**Interfaces:**
- Consumes: SearchAt and v2 service.
- Produces: existing bounded inject response backed by Graph+active Atom; versioned external v2 Search/Evidence/History API.

- [ ] **Step 1: Write RED tests** proving disabled gates preserve old inject/v1 behavior and make v2 routes unavailable; after green gate, inject can use active Atom without interactive DAG API, v1 response remains unchanged and v2 structured refs parse strictly.
- [ ] **Step 2: Replace recall seed retriever** with class-aware SearchAt behind `atom_search` adoption gate while preserving failure-nonfatal/adoption/continuation semantics. Shadow-disabled mode records only aggregate comparison.
- [ ] **Step 3: Add authenticated v2 routes and TS schemas/client**; route registration alone does not enable access, and every request checks `memory_explore_v2` gate plus current fence. No API field can carry unvalidated raw ref maps.
- [ ] **Step 4: Validate**

```bash
cd server && go test ./internal/service ./internal/handler -run 'TestGraphMemoryRecall|TestMemoryExploreV2API' -count=1
pnpm --filter @multica/core exec vitest run api/graph-memory-schemas.test.ts
```

---

## Phase 4：Atom-aware Consolidation 与 Promotion

### Task 14: Atom coverage ledger 与原子 winner publish

**Files:**
- Create: `server/migrations/469_graph_memory_publication_coverage.up.sql`
- Create: `server/migrations/469_graph_memory_publication_coverage.down.sql`
- Modify: `server/internal/memorygraph/consolidate.go`
- Modify: `server/internal/memorygraph/consolidate_test.go`
- Modify: `server/internal/memorygraph/backtest.go`
- Modify: `server/internal/memorygraph/store.go`
- Modify: `server/internal/memorygraph/retriever.go`
- Modify: `server/internal/memorygraph/explore_tools.go`
- Modify: `server/internal/service/graph_memory_route.go`
- Modify: `server/internal/service/graph_memory_recall_execute.go`
- Modify: `server/internal/service/graph_memory_agent_gateway.go`
- Modify: `server/internal/scheduler/jobs_graph_memory.go`
- Create: `server/internal/service/graph_memory_publication.go`
- Create: `server/internal/service/graph_memory_publication_test.go`
- Create: `server/internal/service/graph_memory_consolidation_publish.go`
- Create: `server/internal/service/graph_memory_consolidation_publish_test.go`

**Interfaces:**
- Consumes: active Atom manifest at fixed watermark and green `atom_consolidation` gate.
- Produces: one DB-authoritative publication transaction containing Graph generation pointer, immutable file manifest hash, node→Atom provenance, coverage ledger, active index generation and outcome. File-store `current` is only a recoverable cache/projection and never reader authority.

- [ ] **Step 1: Write RED tests** for partial Segment coverage, loser/no-switch/failure non-consumption, disabled gate, immutable candidate preparation, crash before file fsync, after file prepare/before DB commit, after DB commit/before cache pointer, replay, concurrent multi-process readers, and source deletion exactly between file prepare and publication commit. Assert either publication aborts or subsequent deletion atomically quarantines the newly published node; deleted body is never visible.
- [ ] **Step 2: Implement migration 469** with DB-authoritative `graph_memory_publication` current-generation CAS plus coverage/provenance/index/outcome tables.
- [ ] **Step 3: Change Consolidator input** from all staging files to active Atom manifest; prompt requires explicit `atom_refs`; even non-TTT flow always writes a new immutable candidate version rather than mutating current files in place.
- [ ] **Step 4: Implement publication coordinator**: write/fsync complete immutable version/index first. In deterministic source-key order, the PostgreSQL publication transaction locks every Atom/source/evidence `memory_source_guard` `FOR KEY SHARE`, rechecks current ACL/scope/retraction and candidate manifest, inserts complete reverse provenance, then CAS-publishes generation + file manifest hash + all ledgers. Deletion takes `FOR UPDATE` on the same keys/order: delete-first makes publication abort; publish-first makes deletion wait and then quarantine the new closure. Search the repository for every `CurrentVersion`, `LoadCurrent`, file `current` pointer and Graph resolver; migrate each production reader and pin it in a test. File-store `current` updates only after commit and is rebuildable.
- [ ] **Step 5: Change triggers** to new active Atom/query/age counts; no historical staging replay. Scheduler cannot claim when `atom_consolidation` gate is disabled.
- [ ] **Step 6: Validate**

```bash
make sqlc
cd server && go test ./internal/memorygraph ./internal/service ./internal/scheduler -run 'Test.*(AtomCoverage|Consolidat)' -count=1
```

### Task 15: Durable-evidence Project promotion 与 corrections

**Files:**
- Create: `server/internal/service/graph_memory_promotion.go`
- Create: `server/internal/service/graph_memory_promotion_test.go`
- Modify: `server/internal/memorygraph/consolidate.go`
- Modify: `server/internal/memorygraph/types.go`
- Modify: `server/internal/service/graph_memory_publication.go`
- Modify: `server/internal/service/graph_memory_consolidation_publish.go`
- Create: `server/internal/handler/graph_memory_corrections.go`
- Modify: `server/cmd/server/router.go`

**Interfaces:**

```go
type PromotionEvidence struct { Kind, RefID, PolicyVersion string }
type PromotionDecision struct { Allowed bool; Reason string; DerivedNode *memorygraph.Node }
func (p *GraphMemoryPromotionPolicy) Evaluate(ctx context.Context, req PromotionRequest) (PromotionDecision, error)
```

- [ ] **Step 1: Write RED matrix** for human confirmation, formal decision, trusted read-only result, completed non-rolled-back outcome, multi-source evidence, prompt injection, secret, low-privilege author, deleted source, cross-lineage ref and deletion after LLM proposal but before commit.
- [ ] **Step 2: Implement policy**; LLM proposes only. The derived Project node is published exclusively through Task 14 coordinator, which locks all evidence source guards and revalidates event-time/current ACL, durable evidence, scope and retraction in the commit transaction before stamping project scope.
- [ ] **Step 3: Add retract/correct/supersede handlers** with owner/admin direct retract and candidate flow for others.
- [ ] **Step 4: Validate**

```bash
cd server && go test ./internal/service ./internal/memorygraph ./internal/handler -run 'TestGraphMemory(Promotion|Correction|Retraction)' -count=1
```

---

## Phase 5：Channel migration 与 source retraction

### Task 16: Online channel-owned Graph migration

**Files:**
- Create: `server/migrations/470_graph_memory_channel_migration.up.sql`
- Create: `server/migrations/470_graph_memory_channel_migration.down.sql`
- Modify: `server/pkg/db/queries/graph_memory.sql`
- Create: `server/internal/service/graph_memory_channel_migration.go`
- Create: `server/internal/service/graph_memory_channel_migration_test.go`
- Modify: `server/internal/service/graph_memory_route.go`
- Create: `server/internal/service/channel_project_binding.go`
- Create: `server/internal/service/channel_project_binding_test.go`
- Modify: `server/internal/handler/channel_project.go`
- Modify: `server/internal/handler/channel_project_test.go`
- Modify: `server/internal/handler/agent_goal_bootstrap.go`
- Create: `server/internal/handler/agent_goal_bootstrap_test.go`
- Modify: `server/internal/handler/graph_memory_lineage.go`
- Modify: `server/internal/scheduler/jobs_graph_memory.go`

**Interfaces:**
- Consumes: channel binding transaction and canonical Graph projection refs.
- Produces: migration generation, single write owner, source watermark, redirect ledger and safe dual-read plan.

- [ ] **Step 1: Write RED tests** for settings A→B rebind, agent-goal bootstrap bind, unbind, standalone→Project, concurrent CAS rebind, direct SQL writer rejection, disabled migration gate, new writes during copy, worker crash/replay, no old Project/other channel copy, daily nodes, cross-scope edge drop, query/backtest non-copy, old citation redirect and deletion through both old/new refs.
- [ ] **Step 2: Implement migration 470** with state/redirect/ref tables, CAS generation and a DB trigger/constraint that rejects `channel.project_id` changes unless the same transaction has inserted/updated the matching binding generation + migration state row. This protects against accidental future direct writers.
- [ ] **Step 3: Move every production binding writer into `ChannelProjectBindingService`**. `SetChannelProject` and `agent_goal_bootstrap.go` both call it. In one PostgreSQL transaction lock channel row, verify expected old binding, capture source watermark, CAS route generation, create migration generation, switch single write owner and update `channel.project_id`; rollback all on conflict. Repo-wide search `project_id` channel UPDATEs and pin the zero-bypass result in a structural test.
- [ ] **Step 4: Copy channel-owned artifacts by watermark** behind green migration gate; add blob refs rather than bytes; tombstone old searchable projection. Safe dual-read still calls Task 8A fence and deduplicates by canonical ID.
- [ ] **Step 5: Validate**

```bash
make sqlc
cd server && go test ./internal/service ./internal/handler ./internal/scheduler -run 'TestGraphMemory.*Migration|Test.*Lineage' -count=1
```

### Task 17: Versioned retention、encrypted archive 与 sweep

**Files:**
- Create: `server/migrations/471_memory_retention_archive.up.sql`
- Create: `server/migrations/471_memory_retention_archive.down.sql`
- Modify: `server/pkg/db/queries/graph_memory.sql`
- Create: `server/internal/service/memory_retention.go`
- Create: `server/internal/service/memory_retention_test.go`
- Create: `server/internal/service/memory_archive.go`
- Create: `server/internal/service/memory_archive_test.go`
- Create: `server/internal/handler/memory_retention.go`
- Create: `server/internal/handler/memory_retention_test.go`
- Modify: `server/internal/service/graph_memory_blob.go`
- Modify: `server/internal/service/memory_explore_v2.go`
- Modify: `server/internal/scheduler/jobs_interaction_dag.go`
- Modify: `server/internal/scheduler/jobs_graph_memory.go`
- Modify: `server/cmd/server/router.go`

**Interfaces:**

```go
type MemoryRetentionPolicy struct {
    Version int64
    TrajectoryHotDays int // default/max 90 in shadow
    ArchiveDays int       // default/max 365 in shadow
    TraceHotDays int      // default/max 30 in shadow
}
type ArchiveCipher interface {
    EncryptForWorkspace(ctx context.Context, workspaceID pgtype.UUID, plaintext io.Reader) (ciphertext io.Reader, keyEnvelope, sha256 string, err error)
    DecryptForLease(ctx context.Context, lease RestoreLease) (io.ReadCloser, error)
}
func (s *MemoryArchiveService) ArchiveDue(ctx context.Context, limit int) (int, error)
func (s *MemoryArchiveService) RestoreEvidence(ctx context.Context, req RestoreRequest) (RestoreLease, error)
func (s *MemoryRetentionService) SweepDue(ctx context.Context, limit int) (int, error)
```

- [ ] **Step 1: Write RED policy/migration tests** for bootstrap 90d/365d/30d, Workspace shortening, attempted platform-cap extension, CAS policy version, pre-policy data semantics and policy rollback.
- [ ] **Step 2: Implement migration 471** with versioned Workspace policy, archive manifest/key envelope, restore lease/audit and sweep cursor. Existing rows bind to explicit bootstrap version; no default silently lengthens retention.
- [ ] **Step 3: Implement encrypted archive creation** before hot trajectory expiry, verify ciphertext/hash, then retire hot body. Archive bytes share Task 8A fence and Workspace-scoped key envelope; no plaintext cache crosses Workspace.
- [ ] **Step 4: Implement restore** requiring current ACL, explicit reason, short TTL and object-scoped audit; restored bytes are stream/lease-only and never written back to Search/index.
- [ ] **Step 5: Implement idempotent sweeps** for 90-day hot trajectory, one-year archive and 30-day full trace; release blob refs and cryptographically erase only after zero legal refs. A shortened Workspace policy creates an audited migration job that recomputes `due_at` for existing eligible rows and new rows; it never extends any row beyond its originally bound platform cap.
- [ ] **Step 6: Add owner/admin API** with platform upper-bound validation and no extension above shadow caps without a separately versioned approved profile.
- [ ] **Step 7: Validate**

```bash
make sqlc
cd server && go test ./internal/service ./internal/handler ./internal/scheduler -run 'TestMemory(Retention|Archive)|Test.*RetentionSweep' -count=1
```

---

## Phase 6：Training governance 与 delayed reward

### Task 18: Training grants、selection manifest 与 deletion ledger

**Files:**
- Create: `server/migrations/472_interaction_dag_training_governance.up.sql`
- Create: `server/migrations/472_interaction_dag_training_governance.down.sql`
- Modify: `server/pkg/db/queries/interaction_dag.sql`
- Create: `server/internal/service/interaction_dag_training.go`
- Create: `server/internal/service/interaction_dag_training_test.go`
- Create: `server/internal/handler/interaction_dag_training.go`
- Modify: `server/internal/service/training.go`
- Modify: `server/internal/service/task.go`
- Modify: `server/internal/service/graph_memory_offline_export.go`
- Modify: `server/internal/service/offline_trajectory.go`
- Modify: `server/internal/service/offline_trajectory_test.go`
- Modify: `server/internal/service/graph_memory_rl_session.go`
- Modify: `server/internal/handler/evolution_training.go`
- Modify: `server/internal/handler/graph_memory_offline_export_test.go`
- Modify: `server/internal/handler/offline_trajectory.go`
- Modify: `server/internal/handler/offline_trajectory_test.go`
- Modify: `server/internal/arealrl/reward_sink.go`
- Modify: `server/cmd/server/main.go`
- Modify: `server/cmd/server/router.go`

**Interfaces:**
- Pre-run capture: ordinary Task always writes Universal DAG under data-processing policy; it does not call AReaL and needs no selection manifest.
- Post-publish selection: consumes published non-derivative Segment, current reward revision, Task 8A fence and Workspace grant.
- Training execution: consumes an immutable selected/exported manifest and creates a distinct replay/training Task carrying `training_manifest_id`.
- Produces grant-bound manifests and CAS states `eligible→selected→exported→execution_started→consumed`, plus `retracted|revoked`; pooled and tenant purposes remain separate.

- [ ] **Step 1: Write RED integration tests**: ordinary source Task never opens AReaL; source publishes and receives reward before selection; existing Workspace pending_owner_ack, new Workspace tenant default, pooled opt-in, global kill switch, CAS conflict, revoke before execution, delete after consume, derivative/retracted/unavailable exclusion, duplicate/Workspace cap and replay. Exercise Graph Memory export, Mixed-RL `OfflineTrajectoryService`, evolution training handler and AReaL execution; raw NDJSON routes return disabled/manifest_required without a valid manifest.
- [ ] **Step 2: Implement migration 472** with grant, policy, manifest, manifest item, execution identity/transitions and deletion/unlearning ledger. Backfill existing Workspace as pending_owner_ack; direct legacy export/session rows are closed until reselected.
- [ ] **Step 3: Implement post-publish selection/delivery service**. Manifest fixes source Segment/hash/sanitizer/scope/reward/grant; every transition rechecks grant, global switch, current fence and reward status with exactly-once CAS.
- [ ] **Step 4: Convert online training to manifest-backed delayed execution**. `MaybeOpenTrainingSession` is a no-op for ordinary source Tasks and may call `StartSession` only for a distinct training execution Task whose immutable manifest/purpose is in context. `RouteTerminalTrainingTask`/`SetReward` applies only to that execution identity. Before reward calibration enables the global switch, no replay task is created and no model update occurs.
- [ ] **Step 5: Route all exporters/consumers through the service**. Graph Memory and Mixed-RL offline NDJSON can serialize only a manifest export, never select rows directly; AReaL and pooled/tenant consumers require the same manifest. Revoked/retracted export is rejected or enters deletion/unlearning handling.
- [ ] **Step 6: Add owner/admin APIs** and keep global shadow/calibration kill switch off by default.
- [ ] **Step 7: Validate**

```bash
make sqlc
cd server && go test ./internal/service ./internal/handler ./internal/arealrl -run 'TestInteractionDAGTraining|Test.*TrainingGrant|Test.*Manifest.*Export|Test.*Manifest.*Consume' -count=1
```

### Task 19: Dive unavailable reward、Consolidation delta reward 与 revisions

**Files:**
- Create: `server/migrations/473_graph_memory_reward_revision.up.sql`
- Create: `server/migrations/473_graph_memory_reward_revision.down.sql`
- Modify: `server/internal/memorygraph/reward.go`
- Modify: `server/internal/memorygraph/reward_test.go`
- Modify: `server/internal/service/graph_memory_dive.go`
- Modify: `server/internal/service/graph_memory_dive_reward.go`
- Modify: `server/internal/service/graph_memory_rl_session.go`
- Modify: `server/internal/memorygraph/backtest.go`
- Modify: `server/internal/memorygraph/consolidate.go`
- Modify: `server/internal/arealrl/reward_sink.go`
- Modify: `server/internal/arealrl/reward_sink_test.go`
- Test: `server/internal/service/graph_memory_reward_policy_test.go`

**Interfaces:**
- Produces immutable `RewardRecord{Kind,Status,Revision,Components,Value,PolicyVersion,InputManifestHash}` and exactly-once outbox per revision.

- [ ] **Step 1: Write RED tests**: Dive min dimension-rounds; judge failure unavailable/no outbox value; deterministic budget negative; consolidation baseline quality/efficiency delta; hard-gate negative; loser/winner semantics; revision conflict; holdout split.
- [ ] **Step 2: Implement migration 473** replacing trajectory-unique mutable reward with immutable revisions and delivery identity.
- [ ] **Step 3: Change terminal judge failure** from reward 0 to unavailable. Offline export and training selection must exclude unavailable.
- [ ] **Step 4: Implement consolidation reward components** using absolute costs and stable holdout/safety partition; do not reuse batch-relative min-max cost as RL reward.
- [ ] **Step 5: Preserve exactly-once effect** in RewardSink by `(trajectory,reward_kind,revision)`.
- [ ] **Step 6: Validate**

```bash
cd server && go test ./internal/memorygraph ./internal/service ./internal/arealrl ./internal/handler -run 'Test.*Reward|Test.*Dive|Test.*Backtest' -count=1
```

---

## Phase 7：UI、health、shadow rollout 与 backfill

### Task 20: Citation/status UI 与 Workspace policy controls

**Files:**
- Modify: `packages/core/api/schemas.ts`
- Modify: `packages/core/api/client.ts`
- Modify: `packages/views/channels/components/graph-memory-citation-badge.tsx`
- Modify: `packages/views/channels/components/graph-memory-citation-badge.test.tsx`
- Modify: `packages/views/evolution/components/graph-memory-cards.tsx`
- Modify: `packages/views/evolution/components/graph-memory-cards.test.tsx`
- Modify: `packages/views/channels/components/channel-graph-memory-settings.tsx`
- Modify: `packages/views/locales/en/channels.json`
- Modify: `packages/views/locales/en/evolution.json`
- Modify: `packages/views/locales/zh-Hans/channels.json`
- Modify: `packages/views/locales/zh-Hans/evolution.json`

**Interfaces:**
- Consumes: v2 citation/status/training/retention APIs.
- Produces user-visible classes: consolidated, recent-unreviewed, historical, restricted, retracted; owner controls with loading/error/empty/conflict states.

- [ ] **Step 1: Write RED component/schema tests** for all citation classes, restricted details, retracted evidence, CAS training toggle, pending_owner_ack, retention 90/365/30 bootstrap and shortening/cap conflict, migration/DLQ health.
- [ ] **Step 2: Implement strict schemas and Query-owned server state**; no Zustand duplication.
- [ ] **Step 3: Implement UI/accessibility/i18n** following conventions docs.
- [ ] **Step 4: Validate**

```bash
pnpm --filter @multica/core exec vitest run api/graph-memory-schemas.test.ts
pnpm --filter @multica/views exec vitest run packages/views/channels/components/graph-memory-citation-badge.test.tsx packages/views/evolution/components/graph-memory-cards.test.tsx
pnpm --filter @multica/core typecheck
pnpm --filter @multica/views typecheck
```

### Task 21: Shadow evidence、health metrics 与 gate promotion

**Files:**
- Modify: `server/internal/metrics/business.go`
- Modify: `server/internal/metrics/business_test.go`
- Modify: `server/internal/service/memory_read_gate.go`
- Modify: `server/internal/service/graph_memory_audit.go`
- Modify: `server/internal/service/graph_memory_status.go`
- Modify: `server/internal/handler/graph_memory_status.go`
- Modify: `server/internal/scheduler/jobs_graph_memory.go`
- Test: `server/internal/service/universal_dag_shadow_gate_test.go`

**Interfaces:**
- Consumes: default-off gates created in Task 8A and evidence from Tasks 9–20.
- Produces audited gate promotion/automatic shutdown for Atom adoption, v2 Search/DAG, consolidation, migration, reward shadow, tenant training and pooled training.

- [ ] **Step 1: Write RED canary/gate tests** for sequence gap, outbox loss, cross-channel leak, sanitizer fail-open, retraction visibility, cost/latency budget and automatic read-path shutdown.
- [ ] **Step 2: Implement metrics/health** without memory content labels. Gate failure keeps DAG writes enabled and disables only dependent read/training phase.
- [ ] **Step 3: Implement audited CAS gate promotion**. A gate can move from disabled→shadow→enabled only when named prerequisite tests/canary windows and policy versions are recorded; provider, ACL, sanitizer or deletion failure synchronously returns it to disabled.
- [ ] **Step 4: Verify phase order** in configuration and scheduler; no later phase can activate before prerequisites green. Re-read the maintainer-designated engineering SSOT from Task 0 and verify all documentation links resolve; do not create or rewrite that SSOT here.
- [ ] **Step 5: Validate**

```bash
cd server && go test ./internal/metrics ./internal/service ./internal/handler ./internal/scheduler -run 'Test.*(ShadowGate|Canary|GraphMemoryStatus)' -count=1
```

### Task 22: Approximate historical backfill、compatibility cleanup 与 full validation

**Files:**
- Create: `server/internal/service/universal_interaction_dag_backfill.go`
- Create: `server/internal/service/universal_interaction_dag_backfill_test.go`
- Create: `server/internal/scheduler/jobs_interaction_dag_backfill.go`
- Modify: `server/internal/scheduler/manager.go`
- Modify: `server/cmd/server/main.go`
- Modify: `server/internal/service/interaction_dag.go`
- Modify: `server/internal/service/interaction_dag_seams.go`

The approved spec is read-only during this Task. If implementation evidence conflicts with it, stop and open a separately approved spec change; do not edit AC as part of implementation cleanup.

**Interfaces:**
- Consumes: all prior phase gates.
- Produces: rate-limited 90-day `legacy_backfill` with one approximate Segment per completed Task; no guessed generation/edge; default excluded from training selection.

- [ ] **Step 1: Write RED backfill tests** for 90-day window, one Segment/Task, `boundary_quality=approximate`, existing live Segment skip, sanitizer/scope reuse, no guessed edge, realtime quota priority and replay.
- [ ] **Step 2: Implement backfill worker** behind final shadow gate and separate rate budget.
- [ ] **Step 3: Remove expired internal compatibility paths**: one-task-one-segment live guard, independent Mixed-RL writer, file-modtime staging activity and unconditional all-staging retrieval. Keep external v1 boundary only.
- [ ] **Step 4: Run targeted backend suites**

```bash
cd server && go test ./internal/service ./internal/memorygraph ./internal/handler ./internal/scheduler ./internal/arealrl -count=1
```

Expected: PASS.

- [ ] **Step 5: Run frontend gates**

```bash
pnpm lint
pnpm typecheck
pnpm test
```

Expected: PASS or documented unrelated pre-existing failure with targeted affected packages green.

- [ ] **Step 6: Run repository gates**

```bash
make sqlc
git diff --check
make check
```

Expected: generated SQL clean, no whitespace errors, repository check green. If full gate requires unavailable external services, run the documented local subset and record the exact blocker.

- [ ] **Step 7: Evidence review**

Produce an AC 1–68 evidence table linking each criterion to test names, migration/schema evidence, metrics/canary and UI/API artifact. Do not mark rollout complete from issue status or “tests pass” alone.

---

## Spec Coverage Matrix

| Spec area / AC | Tasks |
| --- | --- |
| Universal DAG identity, generation, edge, all Task shapes (AC 1–7, 56–57, 64–65) | 0–4 |
| Provider/region policy and Workspace-scoped cache (spec §7.3, §16) | 4A, 7, 9, 11, 14, 19 |
| Durable publish, sanitizer, Atom, event-time eligibility (AC 8–14, 58) | 5–8 |
| Retraction fence, source-delete atomicity and default-off gates (AC 38–41, 51–52, 62, 66) | 8A, 16–17, 21 |
| Search, typed refs, v1/v2 paths, DAG Explore, budget (AC 15–25, 59–60, 63, 67) | 9–13 |
| Atom consumption, DB-authoritative Consolidation, promotion/correction (AC 26–33) | 14–15 |
| Channel migration artifact matrix and redirects (AC 34–37, 61) | 16 |
| Retention/archive/blob lifecycle (AC 40–41, 55, 63) | 8A, 17, 20–21 |
| Training grants, controlled delivery, reward, holdout, exactly-once (AC 42–50, 68) | 18–19 |
| UI, compatibility, shadow evidence, backfill and production freeze (AC 51–55) | 20–22 |

## Execution Notes

- Recommended execution is Subagent-Driven Development with a fresh implementer per Task and spec-compliance/code-quality review after each Task.
- Phase 1–2 may ship shadow-only without any Memory read behavior change.
- Phase 3 cannot enable Atom Search until cross-channel canary and retraction prerequisites for its exposed data class are green.
- Phase 4–6 cannot be treated as optional security follow-ups once their corresponding read/training path is enabled.
- Do not execute production migrations, enable rollout flags, backfill production data, or restart production Computer/Agent without separate explicit authorization.
