# Research 图与 Graph Memory 统一规格

- 日期：2026-08-31
- 状态：设计共识已确认（grilling 会话产出）；Phase 1 实施计划见 `docs/superpowers/plans/2026-08-31-research-graph-memory-unification-phase1-plan.zh-CN.md`
- 配套修订：`2026-08-17-graph-memory-scope-design.md`（2026-08-31 revision，research 为第四种命名 scope）
- 范围：把 research 调研图的知识沉淀进 workspace 级 graph memory（新增 research 图 scope），实现频道联邦读取与调研 Director 召回，并为记忆图 consolidation 增加 merge_node 去重能力
- 前置规格：
  - `2026-08-14-graph-memory-reviewer-design.zh-CN.md`（图记忆基础模型）
  - `2026-08-17-graph-memory-scope-design.md`（scope 拓扑，本规格修订其 workspace 级图禁令）
  - `2026-08-27-universal-interaction-dag-graph-memory-pipeline-spec.zh-CN.md`（staging/atom 演进方向，本规格的 merge_node 与其问题 #6/#7 相关但不依赖）

## 1. Problem Statement

Research 调研图与 graph memory 是两套完全独立的系统，代码与数据零交叉：

1. **调研图**：会话级知识星图，存 Postgres（V5 直接存表 `research_graph_node/edge/cluster`；V6 存规范化账本，图是按需派生的投影）。节点类型含 goal/subquestion/probe/finding/conflict/dead_end/refuted/pivot/conclusion/insight 等，带状态与血缘边。由服务端调研引擎（Director 规划 + fleet agents 执行）构建。
2. **graph memory**：workspace 级记忆管线，存文件系统版本化 DAG（`memory_graph/{projects|channels}/<owner>`）。节点为单条知识声明（markdown，一个节点一条陈述，认识论状态 proposed/supported/accepted/contested/rejected/superseded）。从频道消息/agent 运行摄取 staging，小时级 LLM 整合成新图版本。聊天时 inject/agent 两种模式消费。
3. 两者唯一联系是 handoff（调研结束建频道发纯文本摘要）——不进图、不回流。
4. **整合管线结构性无法去重**：consolidation 为单发 JSON 清单调用（`consolidate.go:502` `runTrajectory`），提示词仅有操作清单、预算、图统计数字与 staging 段摘要，**不含任何已有节点内容或 ID**。设计文档 §5.1 承诺的「可修改已有 graph 节点」在实现上不可达（LLM 无从知晓已有节点 ID），导致：无语义去重、无跨版本重组、`update_node`/`delete_node`/跨版本 `add_relation_edge` 对已有节点实际不可用。

团队需要：调研知识进入图记忆供频道与后续调研复用（双向），且沉淀过程可控重复。

## 2. 目标与非目标

### 2.1 目标

- 新增 workspace 级 **research 图** scope：每工作区一张，所有调研会话知识累积写入，会话身份降为节点出处。
- 调研知识**增量流式**写入：知识节点进入稳定状态（结论/洞察产生、finding 定论、dead_end/refuted 确认）即写入；后续状态变化（supersede/invalidated）同步为记忆图的边与状态更新。
- agent 枢纽节点（supernode）**溶解为来源标识**：不成为记忆节点，其产出节点的 `SourceAgentIDs` 携带 agent 身份。
- 认识论**终态直入**：按调研终态映射写入，跳过记忆系统 LLM 初判；血缘边同名词直译映射。
- 频道召回**联邦读取**：召回目标 = 项目图 + standalone 频道图 + workspace research 图。
- 记忆图 consolidation 新增 **merge_node 操作**与**整理工作集**（使用/探索/监督三路信号 + 1-hop 邻域 + staging 相似注入），提供原子去重与热点区域定向维护能力。
- Phase 2：调研 Director 每规划周期从 research 图 +（若有）绑定项目图召回背景知识。

### 2.2 非目标

- 不迁移调研图的存储到 memorygraph 文件库，也不把 graph memory 迁到 Postgres（存储级统一不做）。
- 不改动调研星图的前端可视化与投影契约。
- 不在本规格实现：节点级 confidence 字段、冲突/争议解决生命周期、失效传播语义、负面知识召回策略、claim/evidence 分层证据链（五项另立记忆图增强 spec）。
- 不让 fleet agents 直接召回记忆（召回注入点仅 Director）。
- 不做调研知识向项目图的写时扩散双写。

## 3. 决策记录（grilling 会话确认）

| # | 决策点 | 结论 | 关键依据 |
|---|--------|------|----------|
| 1 | 统一方向 | 双向流动 | 价值最大：频道复用调研知识 + 调研递进积累 |
| 2 | 流入粒度 | 知识节点全图导入；排除 agent_activity/stage_gate/roster_change 执行簿记 | 保真与信噪比平衡；失败路径保留负面知识价值 |
| 3 | 写入时机 | 增量流式 | 长会话期间记忆即可用；需要幂等与状态同步 |
| 4 | 记忆 scope | 新增 research 图类型，每工作区一张 | 跨会话知识不碎片化；需修订 scope 设计禁令 |
| 5 | supernode | 溶解为 SourceAgentIDs 来源标识 | 与「agent identity is provenance, not a physical partition」一致 |
| 6 | 频道读取 | 联邦读取 | 不双写、不复制；依赖 agent 网关多图目录 |
| 7 | 调研召回时机 | Director 每规划周期；fleet agents 不召回 | 注入点集中可控，偏差风险与成本最小 |
| 8 | 召回范围 | research 图 + 会话绑定项目图 | 完整双向闭环；不违反项目图参与授权 |
| 9 | 信任级别 | 终态直入（认识论映射），后续整合可重组 | 两套审核体系各管各的；「整合可重组」为弱保证（整合看不见已有节点，已知） |
| 10 | 实施顺序 | Phase 1 流入 + 联邦读取；Phase 2 召回 | Phase 1 独立可交付，Phase 2 依赖知识积累 |
| 11 | 借鉴项范围 | 六项借鉴中仅 merge_node 入 Phase 1，其余五项另立项 | 去重是全图导入后的刚需 |
| 12 | 语义重复处理 | merge_node 操作进 Phase 1（含候选注入） | 整合管线结构性无法去重（§1.4），导出器 + merge 是唯一现实入口 |
| 13 | research 图维护模式 | 导出器直写（高阈值确定性查重）+ 周期性 LLM 维护轮（复用整理工作集机制，空 staging 仅维护操作，使用信号驱动） | 跨图合并按 scope 一致性约束不可能；纯确定性维护无法治理模糊重复 |
| 14 | 工作集参数默认 | 窗口 = 自上次成功运行以来的增量游标（条数兜底 256）；工作集上限 64 节点、每节点摘要 ≤400 runes、每 staging 段相似 top-K=3、邻域受 MaxFanout 约束；全部 GraphMemoryLimits 式可配置 | 保守起步可控提示词体积；参数为运行时可调项，验证后再放宽 |

## 4. Phase 1 设计

### 4.1 research 图 scope

**物理拓扑**（扩展 `memorygraph/layout.go` 的 `DirForScope`）：

```text
<workspaces-root>/<workspace-id>/memory_graph/
├── projects/<project-id>/
├── channels/<channel-id>/
└── research/<workspace-id>/          # 新增；owner 即 workspace 自身
```

- 身份元数据 `.graph_identity.json`：`{workspace_id, kind: "research", owner_id: <workspace-id>}`；store 读写前校验身份，不匹配 fail closed（与现有 projects/channels 图一致）。
- workspace 级路由无 channel rebind 复杂度；`graph_memory_dirs.go` 的 scope 解析增加 research 分支。
- **scope 设计修订**：`2026-08-17-graph-memory-scope-design.md` 的「Root-level and workspace-wide graph fallbacks are forbidden」修订为「禁止**隐式** workspace 级 fallback；**显式** research scope（每工作区一张、有身份元数据）例外」。research 图不是 fallback，是命名的第四种 scope。

### 4.2 research→memory 导出器

新组件（类比 `GraphMemoryIngestHook` 的挂载方式，但消费调研图事件而非交互段）。

**触发源**（V5/V6 双路径都要接）：
- V5：`research_graph_node` 变更（`research_graph_command` 事务后 / `graph_version` bump）。
- V6：`research_run_event` 中 insight/result 提交类事件。

**节点过滤**（决策 2）：
- 白名单（V5）：goal、subquestion、probe、finding、conflict、dead_end、refuted、pivot、conclusion、insight。
- 白名单（V6）：observation、claim、insight、result、dispute、branch objective。
- 排除：agent_activity、stage_gate、roster_change、attempt/task 执行记录、reporter 角色。

**节点映射**：

| 记忆字段 | 来源 |
|----------|------|
| NodeID | 调研节点 UUID 确定性派生（防重放重复导入） |
| Body | title + summary 结构化渲染 |
| Level | 0（声明层） |
| Epistemic | 见认识论映射表 |
| SourceAgentIDs | `actor_agent_id`（决策 5：supernode 溶解） |
| SourceKind | 新增 `"research_node"`；附 source_id、source_session_id |
| CreatedBy | `"research-exporter"` |
| SegmentRefs | 空（非 segment 来源） |

**认识论映射**（决策 9 终态直入）：

| 调研状态/类型 | 记忆 Epistemic |
|---------------|----------------|
| conclusion | accepted |
| insight | supported |
| finding（高 confidence） | supported |
| finding（低 confidence） | proposed |
| conflict | contested |
| dead_end / refuted | rejected |
| superseded / invalidated | superseded |

**边映射**（两边词汇同构，同名词直译）：

| 调研边 | 记忆关系边 |
|--------|-----------|
| supersedes / superseded_by | supersedes |
| contradicts | contradicts |
| supports | supports |
| evidence_for / derived_from | evidence_for / derived_from |
| invalidated_by | invalidates |

不映射 `summarizes` 层级边（调研图无摘要层级语义）。

**写入通路**：经 operations manifest 直写新图版本（复用 `consolidate.go` 应用器语义），**不经过 staging、不做 LLM 初判**（决策 9）。幂等键 = 调研节点 UUID + content hash（复用 memorygraph 现有 ContentHash 幂等机制）。

**状态同步**：后续 supersede/invalidate 事件 -> `update_node`（Epistemic -> superseded）+ `add_relation_edge(supersedes)`。

**导入时确定性查重（保守）**：写入前对每个调研节点做相似检索（HybridRetriever），仅**高相似**命中（默认阈值 ≥0.95，可配置）已有节点时直接走 merge_node（近重复直并）；模糊相似（低于阈值）不阻塞导入，留给维护轮治理（见 4.5）--导入时确定性 merge 无 LLM 复核，阈值必须保守，错合并的代价高于暂时重复。

### 4.3 merge_node 操作与整理工作集（记忆图模型改造，本规格唯一动记忆图模型的部分）

**merge_node 操作语义**（参照调研图 `mergeNodesAtomic`，`research_graph_typed.go:593` + `research_graph_command` 幂等审计）：

```json
{"op":"merge_node","input_node_ids":["n1","n2"],"result_node":{...}}
```

- 原子性地将 N 个输入节点合并为 1 个结果节点：
  - 结果节点 provenance = 全部输入的并集（SegmentRefs / SourceAgentIDs / source 引用单调联合，不丢失）；
  - 写 `merged_from` 关系边（input -> result）；
  - 输入节点置为 superseded（保留可追溯性，默认不物理删除）；
  - op-log 记录 actor、输入、结果、幂等键（输入集合排序哈希 + 结果 content hash）；
  - 应用器校验：输入必须存在、scope 一致（禁止跨图/跨可见性合并）、预算计数。
- **门控保护**：merge 纳入现有 TTT 候选/回测门控统计（错合并比重复更有害）；changed_nodes 与 edge_churn 成本项天然计入合并代价。

**整理工作集（可达性前提：没有它 merge/update/delete 都不可达）**：`buildPrompt` 修订--整理 agent 的提示词除 staging 段摘要外，注入一个**有界工作集**，候选来源三路（数据基础全部现成，无需新埋点）：

1. **使用信号**：最近查询窗口 `query_log` 条目的 `NodeIDs`（被采纳路径的引用节点）与 judge 回写的 `RelevantNodes` + `JudgeScore`（`types.go:271`）；
2. **探索信号**：explore 轨迹的 `ViewedNodeIDs`（`explore.go:348`，已持久化于召回账本）；
3. **监督信号**：dive 任务的 `ViewedNodeIDs`/`SubmittedNodeIDs` 及验证结论（`dive.go:62`，`graph_memory_dive_job`）。

工作集 = 上述节点 ∪ 其 1-hop 邻居（父/子/关系边，受 MaxFanout 预算约束）∪ 每个 staging 段的混合检索 top-K 相似节点。提示词给出各节点的 ID、摘要、认识论状态与信号标注（如「最近被引用」「被 dive 判定过期」），并指示：更新/合并/删除仅发生在工作集内，新知识默认 add_node，发现重复发 merge_node。

- **成本不变式保持**：工作集大小 = O(检索活动) + O(staging·K)，与图规模无关。
- **同时修复** `update_node`/`delete_node`/跨版本 `add_relation_edge` 的现状不可达问题（§1.4：提示词不含任何已有节点 ID）。
- **整理策略升级**：从「盲折叠 staging」到「热点区域定向维护」。不做全图整理（任何已批准设计中都不存在，成本不可行）。整合触发本就含 query 阈值（N_q），检索活动驱动整理节奏的管道已存在，本设计补上的是把检索**结果**喂给整理 agent。
- **已知偏差**：热点区域优先维护（富者愈富）--冷区域节点不被检索就不被维护；兜底为回测回归集（门控挡退化）与 staging 相似注入（抓冷节点与新证据的重复）。残余风险可接受（现状是什么都不维护）。

**参数默认（决策 14，全部 GraphMemoryLimits 式配置可调）**：

| 参数 | 默认值 | 说明 |
|------|--------|------|
| 信号时间窗口 | 自上次成功运行以来的增量游标 | 水位记入 op-log；条数兜底 256（超过截最近）；整合轮与维护轮共用同一游标机制 |
| 工作集节点总上限 | 64 | 三路信号去重后的节点池，超出按信号权重截断（query_log 引用优先） |
| 每节点摘要上限 | 400 runes | 对齐 dive 的 `diveNodeBodyMaxRunes` |
| 每 staging 段相似 top-K | 3 | 相似检索候选注入 |
| 邻域扩展 | 受现有 MaxFanout 约束 | 1-hop 父/子/关系边 |

### 4.4 频道联邦读取

- 召回目标解析（`ResolveChannelRoute` 及召回侧）扩展：项目图 + standalone 频道图 + **workspace research 图**。
- Memory Agent 网关需支持多图目录（现状一次只打开一个图目录；`2026-08-27-universal-pipeline-spec` 问题 #9 已将其列为已知缺口，方向一致）。
- 引用徽章照常工作（`graph_memory_agent_citation` 已按图区分）。
- Evolution 页治理卡片：research 图纳入 status/audit 展示（最小范围：status）。

### 4.5 research 图维护轮

调研知识只在 research 图内部去重；跨图（research 节点 vs 频道衍生节点）按 scope 一致性约束**不合并**，重复由频道联邦读取的排序自然呈现（读取侧天然去重）。

research 图自身的治理分两层（决策 13）：

1. **导入时确定性查重**（见 4.2）：导出器高阈值（≥0.95）近重复直并，保守防错合并。
2. **周期性 LLM 维护轮**：复用整理工作集机制（§4.3），差异是**无 staging**（research 图按决策 3/9 导出器直写版本，不产生 staging 段）--提示词只含工作集：三路信号节点 + 邻域 + **最近导入的调研节点（新证据在场）及其相似 top-K 旧节点（合并候选对）**。LLM 只对工作集内节点发 update_node / merge_node / delete_node / 边操作，不折叠新内容（导入已由导出器完成；维护轮里新节点是「已入场的更新证据」，供 LLM 与旧节点比对合并/更新，不是待折叠对象）。维护轮复用 TTT 候选/回测门控与 op-log 审计。
   - **触发**：复用双阈值机制（research 图自身的 query_log 条数阈值）+ 最小间隔（默认 1h）；使用信号来自频道联邦读取与 Phase 2 Director 召回--信号随采用率自然增长，冷启动期维护轮空转无害。
   - **作用**：治理跨会话模糊重复（两个会话调研出同主题结论但相似度低于导入阈值）、更新被 dive/judge 判定过期或被新证据矛盾的调研节点、修剪 research 图内部冗余边。
   - **保真约束**：维护轮对终态直入节点的重组遵守既有不变量（provenance 单调联合、认识论映射只允许沿 supersede 方向迁移）；merged_from 血缘保证任何合并可追溯、内容不丢失。

### 4.6 前置条件

- 部署实例在 Evolution 页开启 `memory_type=graph`（空启动、无 legacy 回退）。
- ~~scope 设计文档修订先行合入~~ **已完成（2026-08-31）**：`2026-08-17-graph-memory-scope-design.md` 已修订--新增 research 为第四种命名 scope（物理拓扑/身份元数据/visibility/召回矩阵/整合例外/验收测试 23-26），workspace 级图禁令改为「禁止隐式 fallback，显式 research scope 例外」。

## 5. Phase 2 设计：调研召回（记忆 → 调研）

- **时机与主体**（决策 7）：Director 每个规划周期（含会话创建首个 cycle）；fleet agents 不直接召回。
- **范围**（决策 8）：workspace research 图 + 会话绑定项目的项目图（未绑定会话只读 research 图）。
- **召回执行**：以当前 goal / 分支 objective 为 query，HybridRetriever 检索。
- **注入格式**：「背景知识」上下文块，每条带 Epistemic 状态 + observed_at 日期 + 节点引用。
- **偏差控制**：
  - rejected / superseded 默认过滤；
  - 提示词明确背景知识仅供规划参考、不作为证据；
  - 需要传递给 fleet agents 的背景由 Director 融入 work item 描述；
  - 闭环：本会话新结论经导出器回流 research 图，下个 Director 周期可召回。

## 6. 数据流总览

```text
调研引擎（Director + fleet agents）
  -> 调研图（Postgres：V5 typed graph / V6 账本+投影）
  -> [导出器] 知识节点稳定态事件（幂等：UUID+hash）
       -> 导入时确定性查重：高相似（≥0.95）-> merge_node；否则 -> add_node + relation edges
       -> workspace research 图（新版本，终态认识论直入）
            -> 频道召回联邦读取（项目图 + standalone 频道图 + research 图）
                 -> query_log / explore ViewedNodeIDs / dive 判定（使用/探索/监督三路信号）
            -> Phase 2：Director 每周期召回（research 图 + 绑定项目图）
  -> 频道图整合（工作集 = 三路信号节点 + 1-hop 邻域 + staging 相似 top-K；
     staging 折叠为新节点，工作集内节点可被 update/merge/delete）
  -> research 图维护轮（同一工作集机制，无 staging；工作集含最近导入调研节点
     + 相似 top-K 旧节点 + 三路信号节点，治理跨会话模糊重复与过期节点）

注：跨图不合并（scope 一致性约束）；research 节点与频道衍生节点的重复
由联邦读取的排序自然呈现，不属于合并范围。
```

## 7. 风险与已知代价

| 风险/代价 | 说明 | 缓解 |
|-----------|------|------|
| agent 网关多图目录改造 | Phase 1 频道联邦读取的硬依赖，工程量重心 | 与 universal pipeline 规格缺口 #9 合并实施 |
| V5/V6 双事件路径 | 两条触发路径都要接入导出器 | 导出器内部抽象为统一的知识节点事件 |
| 大 session 节点量级 | 单会话知识节点可达数百上千 | rejected/superseded 默认不参与召回；检索过滤 |
| 决策 9「整合可重组」为弱保证 | 整合看不见已有节点（§1.4），系统性重排通道不存在 | merge_node + 候选注入补足最小重组能力；全面重组留待后续 spec |
| 错合并比重复更有害 | merge_node 的 LLM 判断可能失真 | TTT 候选/回测门控 + changed_nodes/churn 成本计入 |
| 导入时确定性 merge 无复核 | 阈值判断错误会即时错合并（无 LLM 审） | 阈值保守（≥0.95）；merged_from 血缘保留全部内容可追溯；维护轮可后续纠正 |
| 热点区域富者愈富 | 工作集只含被检索/探索/监督过的节点，冷区域不被维护 | 回测回归集挡退化；staging 相似注入抓冷节点与新证据的重复；残余风险可接受（现状零维护） |
| 六项借鉴中五项未入列 | 节点置信度、冲突生命周期、失效传播、负面知识召回、证据链分层缺位 | 另立记忆图增强 spec（素材清单见决策 11） |

## 8. 验收标准

### Phase 1

1. 调研会话产出 conclusion/insight 后，research 图出现对应节点：Epistemic 正确、provenance 含 SourceAgentIDs 与 source_session_id。
2. agent_activity/stage_gate 等执行簿记不出现在 research 图。
3. 同一调研节点重复导入不产生重复节点（幂等）。
4. merge_node 可用：语义重复节点可原子合并，merged_from 血缘可追溯，op-log 有审计记录。
5. 整理工作集注入生效：consolidation 提示词含 query_log/explore/dive 三路信号节点及其邻域；LLM 可对工作集内节点发出**可达**的 update_node/merge_node/delete_node 操作（现状这些操作因提示词无节点 ID 而不可达）。
6. 频道 agent 聊天相关主题时可召回调研知识并展示引用徽章（联邦读取生效）。
7. research 图维护轮生效：空 staging 的维护运行可对工作集内调研节点发出 update/merge/delete 并过门控；跨会话同主题调研的模糊重复在若干维护轮后被合并，merged_from 血缘可追溯。

### Phase 2

8. Director cycle 上下文包含来自 research 图的背景知识块（带认识论与日期标注）。✅ 已完成（2026-08-31）：`TestAcceptance8_DirectorBriefContainsResearchBackground`（service）+ `TestCompileDirectorBriefRendersBackgroundKnowledge` / `TestDirectorBriefLoadsBackgroundKnowledge`（researchrun）。
9. 绑定项目的会话可召回项目图知识；未绑定会话只读 research 图。✅ 已完成（2026-08-31）：`TestAcceptance9_BoundSessionReadsProjectGraph`（service）+ `TestDirectorBriefUnboundSessionPassesEmptyProject`（researchrun）+ `TestResearchBackgroundKnowledgeBoundProject` / `TestResearchBackgroundKnowledgeUnboundSessionOnlyResearch`（service provider 层）。
10. 同主题第二次调研的 Director 规划引用第一次调研的结论（递进闭环）。✅ 已完成（2026-08-31）：`TestAcceptance10_ProgressiveLoopAcrossSessions`（service，全链路 V5 conclusion → 导出器 → research 图 → 第二会话 brief 引用，且 superseded 结论被过滤）。闭环联调暴露并修复 Phase 1 缺口：导出器建图未写身份标记，身份校验读者（联邦读取/背景召回）会静默降级——`researchGraphDir` 改用 `EnsureScopedDir`（回归测试 `TestResearchGraphExportStampsGraphIdentity`）。同批补齐 §4.5 信号闭环：Director 背景召回命中即写入该图 query_log 的 `director` 窗口（`TestResearchBackgroundKnowledgeRecordsQueryLog`），维护轮触发统计自动纳入。

## 9. 与既有规格的关系

- **已修订** `2026-08-17-graph-memory-scope-design.md`（2026-08-31 revision）：新增 research scope，workspace 级图禁令改为禁隐式 fallback。
- **衔接** `2026-08-27-universal-interaction-dag-graph-memory-pipeline-spec`：merge_node 与候选注入服务于其问题 #6/#7（拓扑相连、消费边界）；agent 网关多图目录与其缺口 #9 共享实施；本规格不依赖其 atom 层落地。
- **不动** 调研图自身规格（星图 UI、投影契约、V6 director 契约均不变）。
