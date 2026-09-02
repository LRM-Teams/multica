# Research Graph Memory Unification Phase 1 实施计划

> 本计划直接执行，不使用子智能体。每个 slice 严格 red → green → refactor；不创建 commit，除非用户另行明确要求。

- 日期：2026-08-31
- Spec：`docs/superpowers/specs/2026-08-31-research-graph-memory-unification-spec.zh-CN.md`（决策 1-14）
- 依赖：`docs/superpowers/specs/2026-08-17-graph-memory-scope-design.md` 已完成 2026-08-31 修订（research 为第四种命名 scope）
- Goal：交付统一方案 Phase 1——research 图 scope、research→memory 导出器（增量流式 + 终态直入 + 高阈值确定性查重）、记忆图 `merge_node` 操作与整理工作集、research 图维护轮、频道联邦读取、Evolution 最小治理。Phase 2（Director 召回）不在本计划内。
- Architecture：`memorygraph` 包保持图存储/操作/门控的唯一边界；导出器是 `service` 层新组件，以 cursor 轮询调研表（V5 `research_graph_command` / V6 `research_run_event`）实现增量流式，与调研引擎事务解耦；联邦读取在 recall service 扩展目标列表，agent 网关从单目录改多目录；所有写路径过 `GraphMutationCoordinator`。

## 全局约束

- 不修改调研星图前端、投影契约与 V6 director 契约。
- memorygraph 既有不变量不得削弱：provenance 单调、scope 不可变、身份不匹配 fail closed、DAG 约束、预算上限。
- `merge_node` 仅同一物理图内有效；跨图合并结构化拒绝。
- 工作集与导出器参数全部走 `GraphMemoryLimits` 式配置（env 默认 + 上限），不硬编码。
- 导出器与维护轮挂 workspace Graph 门控（`memory_type=graph`，参照 `GetGraphMemoryScopedGate`）；legacy 工作区完全惰性。
- 新增渐进开关 `MULTICA_RESEARCH_GRAPH_EXPORT_ENABLED`（默认关）：关闭时导出器/维护轮不写 research 图；research 图可整目录删除后重放重建（幂等保证）。
- 数据库迁移双向；当前最新 migration 是 463，新迁移从 464 开始；运行 `make sqlc` 更新 generated code，禁止手改。
- API wire 保持 snake_case；TS 内部 camelCase；代码注释只用英文；不新增 dependency。

## Phase 1：存储层与 merge_node（地基）

### Slice 1.1 — research 图 scope（layout / 身份 / 目录解析）

**Test first**

1. `server/internal/memorygraph/layout_test.go` 新增：`DirForScope` 支持 `kind=research, owner=workspace-id`，路径 `memory_graph/research/<workspace-id>`；`EnsureScopedDir` 写入并校验 `.graph_identity.json`（kind=research）。
2. 身份不匹配（kind/owner 错）读写 fail closed；UUID 校验路径不得逃逸 workspace graph root。
3. `graph_memory_dirs_test.go`：scope 解析对 research 分支返回正确根目录；legacy 工作区不解析 research 图。

**Implement**

- `server/internal/memorygraph/layout.go`：`DirForScope`、`EnsureScopedDir` 增加 research kind。
- `server/internal/service/graph_memory_dirs.go`：research scope 解析分支（workspace 级，无 channel route 复杂度）。

**Validate**

```bash
cd server && go test ./internal/memorygraph -run 'Test.*(ResearchScope|Layout|Identity)'
go test ./internal/service -run 'Test.*GraphMemory.*Dir'
```

### Slice 1.2 — merge_node 操作

**Test first**

1. `server/internal/memorygraph/consolidate_test.go` 新增 merge 语义：N 输入 → 1 结果；provenance 并集（SegmentRefs/SourceAgentIDs/source 引用单调联合，不丢失）；`merged_from` 边（input→result）写入；输入节点 Epistemic 置 superseded 且不物理删除；op-log 记录 actor/输入/结果/幂等键（输入集合排序哈希 + 结果 content hash）；同幂等键重复应用无副作用。
2. 拒绝路径：输入不存在、输入含其他图节点（跨图）、scope 不一致（visibility 混合）、超预算。
3. merge 计入 changed_nodes 与 edge_churn（门控成本项）。

**Implement**

- `consolidate.go`：`OpMergeNode` 常量、`applyOne` 分支、`opLogDetail` 扩展、应用器校验。

**Validate**

```bash
cd server && go test ./internal/memorygraph -run 'Test.*MergeNode'
```

### Slice 1.3 — 整理工作集构建器（三路信号 + 邻域 + 相似注入）

**Test first**

1. 新建 `server/internal/memorygraph/workingset_test.go`：三路信号聚合——query_log 条目 `NodeIDs` + judge `RelevantNodes`、explore 轨迹 `ViewedNodeIDs`（PG 账本）、dive `ViewedNodeIDs`/`SubmittedNodeIDs` 及判定；去重后按信号权重截断（query_log 引用优先），总上限 64。
2. 增量游标：水位记 op-log，重复运行不重读；条数兜底 256。
3. 邻域扩展：1-hop 父/子/关系边，受 MaxFanout 约束。
4. staging 相似注入：每段 top-K=3（mock embedder）。
5. 每节点摘要截 400 runes。

**Implement**

- 新建 `server/internal/memorygraph/workingset.go`：`WorkingSetBuilder`（输入：store、PG 账本读取函数、embedder、配置）。
- `consolidate.go` `buildPrompt`：追加工作集段落（节点 ID/摘要/Epistemic/信号标注）与操作指令（更新/合并/删除仅限工作集内、新知识默认 add_node、重复发 merge_node）。

**Validate**

```bash
cd server && go test ./internal/memorygraph -run 'Test.*WorkingSet'
```

### Slice 1.4 — 工作集参数配置

**Test first**：默认值（窗口游标/64 节点/400 runes/top-K 3/兜底 256）、env 覆盖、上限 clamp。

**Implement**：`server/internal/service/graph_memory_config.go` 扩展 `GraphMemoryLimits` 式结构（新增 `ResearchGraph*` 与 `ConsolidationWorkingSet*` 组）。

**Validate**

```bash
cd server && go test ./internal/service -run 'Test.*GraphMemory.*Config'
```

## Phase 2：导出器与维护轮

### Slice 2.1 — research→memory 导出器核心（含迁移 464）

**Test first**

1. 新建 `server/internal/service/research_graph_export_test.go`（test DB + 临时 graph root）：
   - 节点过滤：白名单通过（goal/subquestion/probe/finding/conflict/dead_end/refuted/pivot/conclusion/insight；V6 observation/claim/insight/result/dispute/branch objective）；`agent_activity`/`stage_gate`/`roster_change`/reporter 排除。
   - 认识论映射表逐条断言（conclusion→accepted … superseded→superseded）。
   - supernode 溶解：节点携带 `SourceAgentIDs=[actor_agent_id]`、`SourceKind=research_node`、`source_session_id`；图中不出现 agent 节点。
   - 边映射同名词直译；不产生 summarizes 层级边。
   - 幂等：同一调研节点重放（同 UUID 同 hash）不产生重复；hash 变化产生更新版本。
   - 状态同步：后续 supersede 事件 → Epistemic 更新 + supersedes 边。
2. 导入时确定性查重：相似度 ≥0.95（mock）直接 merge_node（血缘可追溯）；低于阈值照常 add。

**Implement**

- 新建 `server/internal/service/research_graph_export.go`：`ResearchGraphExporter`（cursor 轮询 + 映射 + manifest 直写版本，经 `GraphMutationCoordinator`）。
- 新建 `server/migrations/464_research_graph_export_state.up.sql/.down.sql`：导出 cursor/state 表（workspace 维度水位 + 幂等键去重），复用 V5 `research_graph_command` 与 V6 `research_run_event` 单调 id 做 cursor；`server/pkg/db/queries/graph_memory.sql` 增查询；`make sqlc`。
- 触发实现选择 cursor 轮询（scheduler job）而非事务内 hook：与调研引擎事务解耦、天然 durable、可重放；spec §4.2 的「订阅事件」语义由此达成。

**Validate**

```bash
make sqlc
cd server && go test ./internal/service -run 'TestResearchGraphExport'
```

### Slice 2.2 — 导出器调度与开关

**Test first**：Graph 门控（legacy 工作区不跑）、`MULTICA_RESEARCH_GRAPH_EXPORT_ENABLED` 关闭时不写、cursor 推进持久化、部分失败重试不跳过。

**Implement**

- `server/internal/scheduler/jobs_research_graph_export.go`（新 job：默认 1min 轮询，注册于 `cmd/server/main.go`，惰性于开关与 Graph 门控）。

**Validate**

```bash
cd server && go test ./internal/scheduler -run 'TestResearchGraphExport'
```

### Slice 2.3 — research 图维护轮

**Test first**

1. `consolidate_test.go` 维护轮变体：无 staging；工作集含最近导入调研节点 + 其相似 top-K 旧节点 + 三路信号节点；LLM 仅可对工作集发 update/merge/delete/边操作；门控与 op-log 照常。
2. 触发：research 图 query_log 条数阈值 + 最小间隔 1h；无信号空转无害（不产版本）。
3. 保真约束：维护轮对认识状态的修改仅允许沿 supersede 方向迁移。

**Implement**

- `consolidate.go` 维护轮入口（复用 `Consolidator`，空 staging 路径）；`server/internal/scheduler/jobs_graph_memory.go` 增 research 维护触发。

**Validate**

```bash
cd server && go test ./internal/memorygraph -run 'Test.*Maintenance' && go test ./internal/scheduler -run 'TestGraphMemory'
```

## Phase 3：联邦读取与治理

### Slice 3.1 — 召回目标联邦（recall 侧）

**Test first**

1. `graph_memory_recall_test.go`：`recall_targets` 含 workspace research 图；引用 graph-qualified `research:<workspace-id>/node:<id>`；research-visible 节点不得以 project/channel 内容暴露；无 scope 任务仍不查任何图（research 非 fallback）。
2. 预算：16 KiB execution-memory 裁剪顺序（historical 先裁，research 在 current 之后）。
3. Director 召回行本计划不实现（Phase 2 spec），但矩阵测试预留断言位。

**Implement**

- `server/internal/service/graph_memory_recall.go` / `graph_memory_recall_execute.go`：目标列表追加 research 图；`ScopedRecallCoordinator` 等价物（现 recall 执行路径）多图过滤检索；explore 每图独立视图。

**Validate**

```bash
cd server && go test ./internal/service -run 'TestGraphMemoryRecall'
```

### Slice 3.2 — agent 网关多图目录

**Test first**：网关打开 channel route 目标 + research 图两个 store；citation 图限定（`graph_memory_agent_citation` 区分图）；scope 校验 fail closed；quota/幂等按图独立。

**Implement**

- `server/internal/service/graph_memory_agent_gateway.go`：单 store 改多 store/多 `ExploreToolServer` 实例；`graph_memory_agent_run.go` ledger 记录图标识。

**Validate**

```bash
cd server && go test ./internal/service -run 'TestGraphMemoryAgentGateway'
```

### Slice 3.3 — Evolution 治理最小 UI

**Test first**：status 端点返回 research 图统计（版本/staging=0/节点数）；`graph-memory-cards.test.tsx` 渲染 research 行；legacy 模式不显示。

**Implement**

- status handler 扩展 research scope；`packages/core/evolution/queries.ts` 与 `packages/views/evolution/components/graph-memory-cards.tsx` 最小改动；locale 同步。

**Validate**

```bash
cd server && go test ./internal/handler -run 'TestGraphMemoryStatus'
pnpm --filter @multica/core test -- graph-memory
pnpm --filter @multica/views exec vitest run packages/views/evolution/components/graph-memory-cards.test.tsx
```

## End-to-End 与回归验证（映射 spec §8 Phase 1 验收 1-7）

1. 调研会话产出 conclusion/insight → research 图节点 Epistemic/provenance 正确（验收 1）。
2. agent_activity/stage_gate 不出现在 research 图（验收 2）。
3. 同节点重复导入幂等（验收 3）。
4. merge_node 语义重复合并 + merged_from 血缘 + op-log 审计（验收 4）。
5. 工作集注入生效：consolidation 对工作集节点发出可达 update/merge（验收 5）。
6. 频道 agent 召回调研知识并出引用徽章（验收 6）。
7. 维护轮治理跨会话模糊重复（验收 7）。

E2E 前置：测试工作区开启 `memory_type=graph`（confirm_empty_start 流程）；开关 `MULTICA_RESEARCH_GRAPH_EXPORT_ENABLED=1`。

## Validation commands

```bash
make sqlc
cd server && go test ./internal/memorygraph ./internal/service ./internal/scheduler ./internal/handler
pnpm --filter @multica/core test
pnpm --filter @multica/views test
```

## Review Checklist

- [x] 所有写路径经 `GraphMutationCoordinator`，research 图身份 fail closed。（导出器 `WithGraphLock(ws,"research",ws)`、维护轮同锁；recall/网关/调度/status 四处读取均 `VerifyGraphIdentity`，身份不符即拒绝。）
- [x] provenance 单调、认识论仅沿 supersede 迁移、跨图 merge 拒绝——不变量测试全绿。（memorygraph 全绿；新增 `TestConsolidateMergeNodeRejectsCrossGraphInputs`：另一物理图中的真实节点 id 在本图整合中按未知输入拒绝，两图均未被改动。）
- [x] 导出器/维护轮在 legacy 工作区与开关关闭时零行为。（`TestResearchGraphExportLegacyFailsClosed`、`TestResearchGraphExportJobGatesLegacyWorkspaces`、`TestGraphMemoryResearchMaintenanceSwitchOff`。）
- [x] 幂等可重放：删除 research 图目录后重放 cursor 重建一致。（新增 `TestResearchGraphExportWipeAndRebuild`；实现补齐两处缺口——skip-unchanged 过滤增加"节点仍在图中"条件，检测到 key 存在而图为空时重置水位让 V5 行重扫。重建后指纹一致且再重放为 no-op。）
- [x] 迁移 464 双向；`make sqlc` 产物无手改。（464/465 均有 up/down；本计划未改任何 sqlc 查询（直接 pgx），`pkg/db/generated` 无 diff；`make sqlc` 因本机未装 sqlc 二进制无法执行，属环境限制。）
- [x] 工作集成本不变式：提示词体积与图规模无关（大图回归测试）。（新增 `TestWorkingSetCostInvariantToGraphSize`：3000 节点/600 条查询日志与大图同受 MaxNodes=64 × NodeBodyRunes=400 上限约束。）
- [x] 无 scope 任务不触及 research 图（fallback 防护，scope 设计验收 23）。（`TestGraphMemoryRecallFederatesResearchGraph`：research 图存在但任务无 scope 时返回 `ErrGraphMemoryRecallNoScope`，不查图。）
