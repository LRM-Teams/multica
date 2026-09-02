# Research Graph Memory Unification Phase 2 实施计划（调研召回）

- Spec：`docs/superpowers/specs/2026-08-31-research-graph-memory-unification-spec.zh-CN.md` §5（Phase 2 设计）、§8 验收 8-10。
- 前置：Phase 1 已完成（导出器/维护轮/联邦读取/网关多图/状态 UI，Review Checklist 全绿）。
- 执行方式：与 Phase 1 相同——每个 slice 严格 red → green → refactor；不创建 commit，除非用户另行明确要求。
- 依赖方向：`researchrun` 定义 provider 接口与条目类型；`service` 实现并注入（`researchrun` 不 import `service`，避免环）。

## 全局约束

- **决策 7**：只有 Director 每个规划周期召回（含会话创建首个 cycle）；fleet agents 不召回，work prompt 不改。
- **决策 8**：workspace research 图 + 会话绑定项目的项目图；未绑定会话只读 research 图。
- **偏差控制**：rejected/superseded 默认过滤；背景知识块自带「仅供规划参考、不作为证据」guidance；传给 fleet agents 的内容由 Director 融入 work item 描述（不自动注入 work prompt）。
- **读取失败降级**：背景知识检索任何失败 → 该图为空条目并记日志，绝不阻塞 Director cycle（与 Phase 1 联邦读取同策略：联邦是增量，不是依赖）。
- **有界性**：每 cycle 一次检索；query = run goal（spec 允许「当前 goal / 分支 objective」，取 goal 以使成本与分支数解耦）；每图 top-K 有界、条目摘要 rune 有界，Brief 体积增量与图规模无关。
- **成本/确定性**：每 cycle 一次检索；条目确定性 = 同一 goal + 同一图版本 → 相同条目集与相同渲染（检索排序确定、`observed_at` 只按日期渲染，不混入编译时刻）；页 hash 本身每 cycle 随全新 `brief_id`/`created_at` 变化，属既有行为，不要求跨 cycle 稳定。

## 勘察结论（实现依据）

- Director 周期上下文 = 冻结分页 Brief：`researchrun.LoadDirectorBriefFacts`（`postgres_director_brief.go`，纯 Postgres）→ `contextCompilerModule.CompileDirectorBrief`（`context_compiler.go`）→ 页 + hash。触发链在 store 内部（`event_trigger_v6.go` 直接构造 `directorBriefModule`），因此注入缝是 `PostgresStore` 的 setter（参照 Phase 1 `SetSubmittedRunSink` 模式）。
- 会话-项目绑定：`research_session.project_id`（facts 加载 SQL 目前未 select，需补）。
- Brief 合同：顶层无 `additionalProperties:false`（`research`/`control` 子对象是 strict 的）→ 背景块放**顶层** `background_knowledge` 字段，与 `goal` 同级同页处理；同时在 `research_run_v6_director.schema.json` 的 `director_brief` 定义中补文档化属性（旧页不重校验，安全）。
- 接线点：`internal/handler/handler.go:440`（`researchrun.NewPostgresStore(pool)` 之后）。
- schema 校验走 `DecodeV6Contract(V6ContractDirectorBrief)`，compiler 每页都过校验。

## Slice P2.1 — 背景知识提供者（service 层检索）

**Test first**（`server/internal/service/research_background_knowledge_test.go`，DB+tempdir）：

1. `TestResearchBackgroundKnowledgeResearchGraph`：research 图含 accepted/proposed/superseded/rejected 各一节点 → 条目含 accepted+proposed，**不含** superseded/rejected；每条带 `node_id`、`graph:"research"`、epistemic、`observed_at`（RFC3339 日期）、有界摘要。
2. `TestResearchBackgroundKnowledgeBoundProject`：会话绑定 project → project 图（project-visibility 节点）与 research 图条目都返回，`graph` 字段区分；project 图中 research-visibility 节点不泄漏。
3. `TestResearchBackgroundKnowledgeUnboundSessionOnlyResearch`：`project_id` 为 NULL → 只查 research 图（project 图存在也不读）。
4. `TestResearchBackgroundKnowledgeFailsDegraded`：research 图目录不存在 / 身份不符 → 空条目、无错误（cycle 不被阻塞）；goal 为空 → 空条目。
5. `TestResearchBackgroundKnowledgeBounded`：节点 body 超长 → 摘要截断到上限 rune；每图返回数 ≤ top-K。
6. `TestResearchBackgroundKnowledgeLegacyWorkspace`：`memory_type='legacy'` 工作区 → 空条目（与导出器/联邦读取同一门控语义；research READ 不受导出开关限制，但 legacy 模式不读图）。

**Implement**

- 新建 `server/internal/service/research_background_knowledge.go`：
  - `ResearchBackgroundKnowledgeService{pool, root}`；`Provide(ctx, workspaceID, runID, query, projectID) ([]researchrun.V6BackgroundKnowledgeEntry, error)`（实现 researchrun 的 provider 接口，P2.2 定义）。
  - research 图：`DirForScope(root, ws, research, ws)` + `VerifyGraphIdentity`（不创建）→ `HybridRetriever`（`GraphView{AllowResearch}`，无 embedder）→ Search → 过滤 `Epistemic ∈ {rejected, superseded}` 之外保留…… 即默认过滤 rejected/superseded。
  - 绑定项目图：`DirForScope(root, ws, project, pid)` + 身份校验 → `GraphView{AllowProject}` 检索。
  - 门控：`GetGraphMemoryProfile(memory_type)` 非 `graph` → 直接空返回。
  - 有界：`topKPerGraph=5`、`summaryRunes=400`；条目确定性（同 query 同图同版本同结果）。

**Validate**

```bash
cd server && DATABASE_URL=... go test ./internal/service -run 'TestResearchBackgroundKnowledge'
```

## Slice P2.2 — Director Brief 集成与接线

**Test first**：

1. `server/internal/researchrun/context_compiler_test.go` 增补：`TestCompileDirectorBriefRendersBackgroundKnowledge`——facts 带 3 条背景条目 → 每页顶层 `background_knowledge.entries` 含 `node_id/epistemic/observed_at_date/summary/graph`，`background_knowledge.guidance` 含「仅供规划参考」「不作为证据」；`DecodeV6Contract` 通过（schema 合法）。
2. `server/internal/researchrun/director_brief_background_test.go`：`TestDirectorBriefLoadsBackgroundKnowledge`——stub provider（返回固定条目）经 `SetBackgroundKnowledgeProvider` 注入 → `StartV6DirectorCycle` 产出的持久化页含条目；provider 收到的 query 是 run goal、projectID 来自 `research_session.project_id`；provider 报错 → cycle 正常完成且背景块为空（降级不阻塞）。
3. `server/internal/handler/research_background_wiring_test.go`：`TestResearchRunEngineWiredWithBackgroundKnowledge`——handler 构造后 store 的 provider 非空（接线不遗漏）。

**Implement**

- `researchrun`：
  - `director_brief.go`（或新文件）：`V6BackgroundKnowledgeEntry{NodeID, Graph, Epistemic, ObservedAt time.Time, Summary string}`；`V6BackgroundKnowledgeProvider interface { BackgroundKnowledge(ctx, workspaceID, runID, goal, projectID string) ([]V6BackgroundKnowledgeEntry, error) }`。
  - `PostgresStore` 增 `backgroundKnowledge V6BackgroundKnowledgeProvider` + `SetBackgroundKnowledgeProvider`；`LoadDirectorBriefFacts` 的主 SELECT 增 `s.project_id`，facts 组装完成后（nil provider 安全跳过）调用并填 `facts.BackgroundKnowledge []any`（条目序列化为 map：日期用 `ObservedAt.UTC().Format("2006-01-02")`）。
  - `DirectorBriefFacts` 增 `BackgroundKnowledge []any`；compiler 在每页顶层渲染：
    `"background_knowledge": {"guidance": "<参考性文案>", "entries": [...]}`（guidance 固定中文：背景知识来自工作区记忆图，仅供规划参考，不作为证据；需要传递给 Agent 的内容请融入 work item 描述）。
  - `research_run_v6_director.schema.json`：`director_brief.properties` 增 `background_knowledge`（object：`guidance` string、`entries` array of object，宽松描述即可）。
- `service`：`NewResearchBackgroundKnowledgeService(pool, root)` 实现接口（P2.1）。
- `handler.go:440` 后接线：`researchStore.SetBackgroundKnowledgeProvider(service.NewResearchBackgroundKnowledgeService(pool, ""))`（root 空则 env 解析）。

**Validate**

```bash
cd server && go test ./internal/researchrun -run 'TestCompileDirectorBriefRendersBackgroundKnowledge|TestDirectorBriefLoadsBackgroundKnowledge'
go test ./internal/handler -run 'TestResearchRunEngineWiredWithBackgroundKnowledge'
```

## Slice P2.3 — 递进闭环与验收（spec §8 验收 8-10）

**Test first**（集成测试，DB+tempdir，全链路）：

1. `TestAcceptance8_DirectorBriefContainsResearchBackground`（验收 8）：research 图两个节点（不同 epistemic/日期）→ provider → `StartV6DirectorCycle` → 持久化 brief 页含背景块，条目带认识论与日期标注。
2. `TestAcceptance9_BoundSessionReadsProjectGraph`（验收 9）：绑定会话 → 条目含 project 图与 research 图两源；未绑定会话 → 仅 research 条目。
3. `TestAcceptance10_ProgressiveLoopAcrossSessions`（验收 10，闭环）：会话 A 的 conclusion 节点（V5 `research_graph_node`）→ 跑导出器 → research 图含该结论 → 会话 B（同 workspace 新 session）启动 Director cycle → brief 背景块引用会话 A 的结论节点（node_id = 导出器确定性 id）。
4. 回归：rejected/superseded 过滤在闭环链路中同样生效（会话 A 的 superseded 结论不出现在会话 B 背景）。

**Implement**

- 测试即验收；若链路暴露缝（如 provider 需要导出器先跑）则在测试内直接调用 `ResearchGraphExporter.ExportWorkspace`（Phase 1 组件，无需等调度器）。
- `docs/superpowers/specs/...spec.zh-CN.md` §8 Phase 2 验收 8-10 标注完成与测试名（沿用 §1 的完成标注格式）。

**Validate**

```bash
cd server && DATABASE_URL=... go test ./internal/service ./internal/researchrun -run 'TestAcceptance(8|9|10)'
go build ./... && go vet ./internal/...
```

## 端到端与回归验证

- 全量：`go test ./internal/service ./internal/researchrun ./internal/handler ./internal/scheduler ./internal/memorygraph ./internal/daemon`（预存 5 个 interaction_dag/RunSegment 失败不计）。
- 不改前端（Phase 2 无 UI 需求）。
- `make sqlc` 不适用（无 sqlc 查询改动；延续 Phase 1 直接 pgx 偏差）。

## Review Checklist（Phase 2）

- [x] 仅 Director 周期召回；fleet agent work prompt 零改动。（召回只发生在 `LoadDirectorBriefFacts`；`work_prompt_v6.go` / `RonaldoV6DirectorSystemProtocol` 本阶段零修改。）
- [x] rejected/superseded 默认过滤，闭环链路同样生效。（`TestResearchBackgroundKnowledgeResearchGraph`；闭环内 `TestAcceptance10_ProgressiveLoopAcrossSessions` 断言 superseded 结论不出现。）
- [x] 未绑定会话不读项目图；绑定会话两图都读且 graph 字段可区分。（`TestResearchBackgroundKnowledge(Bound|Unbound)Project*`、`TestAcceptance9_BoundSessionReadsProjectGraph`、`TestDirectorBriefUnboundSessionPassesEmptyProject`。）
- [x] 检索失败降级为空，绝不阻塞 Director cycle（测试覆盖 provider 报错路径）。（`TestResearchBackgroundKnowledgeFailsDegraded`、`TestDirectorBriefBackgroundKnowledgeProviderFailureDegrades`。）
- [x] 背景块内容确定：同一图版本 + 同一 goal → 相同条目与相同渲染；`observed_at` 只按日期渲染，不引入编译时刻时间。（`TestResearchBackgroundKnowledgeResearchGraph` 内两次召回逐条相等断言；渲染为纯 map 变换，日期 `UTC().Format("2006-01-02")`。）
- [x] 背景块体积有界且与图规模无关（top-K × rune 上限）。（`TestResearchBackgroundKnowledgeBounded`：每图 ≤5 条、摘要 ≤400 rune；schema `entries.maxItems=16` 兜底。）
- [x] legacy 工作区零行为（provider 门控）。（`TestResearchBackgroundKnowledgeLegacyWorkspace`：图在磁盘存在仍返回空。）
- [x] schema 文档化 `background_knowledge`，`DecodeV6Contract` 全页通过。（嵌入 schema 与 `docs/contracts/research-run-v6.schema.json` 字节一致——`TestRonaldoV6EmbeddedSchemaMatchesCanonicalDocument`；golden fixtures 与 compiler 全页校验通过。）

### 执行中发现并修复的缝（Phase 1 缺口）

- 导出器建图未写 `.graph_identity.json`（`researchGraphDir` 用 `DirForScope` + `store.Init()`），而联邦读取（Phase 1）与背景召回（本阶段）都身份校验失败即静默降级——生产环境导出器创建的 research 图对所有身份校验读者不可见。修复：`researchGraphDir` 改用 `EnsureScopedDir`（首用盖章、已存在即校验）。回归：`TestResearchGraphExportStampsGraphIdentity` + Phase 1 导出器 10 测全绿。此缺口同时影响 Phase 1 验收 6 的生产路径（联邦读取），已在 spec §8 验收 10 标注中记录。
- 补齐 spec §4.5 信号闭环：维护轮触发「使用信号来自频道联邦读取**与 Phase 2 Director 召回**」，但 P2.1 初版提供者未写 query_log。修复：背景召回命中后向该图自己的 `director` 窗口记 `RecordRecall`（research 与 project 图各自记录；miss 不记；写失败仅丢信号不丢条目）。调度器 `countQueryLogEntriesSince` 跨窗口统计，零调度器改动即接入。回归：`TestResearchBackgroundKnowledgeRecordsQueryLog` + 验收/导出器/调度器电池全绿。

### 测试夹具备注

- `research_session` 受 DEFERRED 护照守卫约束：INSERT/UPDATE 与 `research_artifact_backfill_registered` 必须同事务（验收夹具因此用单事务种子）。
- `research_director_brief_page.content_bytes` 是 bytea：`::text` 会得到十六进制转义，断言须扫原始字节。

## 已知偏差（延续记录）

- query 取 run goal 而非逐分支 objective（成本与分支数解耦；spec 措辞「goal / 分支 objective」二选一）。
- 背景块渲染在每页顶层（与 `goal` 同处理），而非仅首页。
- guidance 放在背景块内，不改 `RonaldoV6DirectorSystemProtocol`（协议文本是冻结合同，最小改动）。
