# Graph Memory Recall 去重与相邻调用 Continuation 规格

- 日期：2026-08-24
- 状态：设计已确认（grilling 共识 D1–D12）；Phase 1（P0）待实现，Phase 2 以 Phase 1 量测基线为启动门槛
- 范围：graph memory recall 的**重复计算消除**（Phase 1）与**相邻调用证据接续**（Phase 2）。不改动 explore 工具协议、adoption 规则、GraphView 语义、consolidation 与版本化机制
- 对照：`docs/superpowers/specs/2026-08-21-graph-memory-backtest-explore-redesign-spec.zh-CN.md`（工具协议维持其结论）；`docs/superpowers/specs/2026-08-17-graph-memory-scope-design.md`（scope/visibility 模型不变）
- 配套计划：`docs/superpowers/plans/2026-08-24-graph-memory-recall-p0-dedupe-plan.zh-CN.md`（仅 Phase 1；Phase 2 计划在基线数据产出后另立）

## 1. 问题陈述

当前 graph memory recall 存在三类计算浪费，按修复成本从低到高排列：

1. **单次 recall 内部双重 hybrid 检索**。`Begin` 的 seeder（`server/internal/service/graph_memory_recall.go:267-273`）计算 round-0 seeds 并写入 ledger，但这份结果从未传给执行侧；`Explorer.Explore`（`server/internal/memorygraph/explore.go:147-157`）内部重新执行一次 hybrid `Search`，其结果才是真正嵌入轨迹 prompt 的种子。两次检索之间没有数据流连接，前者纯浪费。
2. **batch 内逐消息 recall**。`prepareResidentMessageBatch`（`server/internal/daemon/message_runtime.go:394-430`）对每条消息独立调用 `graphExecutionMemories`；而 recall query 直接取用户消息原文（`server/internal/daemon/graph_memory.go:58-73`），batch 内相同原文的消息会触发**完整重复**的 recall（含 K 条并行 explore 轨迹与 LLM 调用）。每次调用还生成全新 `TraceID`（`graph_memory.go:35`），服务端 replay 幂等不会合并它们。
3. **相邻 agent 调用间零复用**。群聊中相邻两次 agent 调用的 memory 需求高度重叠，但每次都从零 seeds + explore，上一轮 adopted 探索的全部信息（读过哪些节点、跨节点观察、被否定分支）直接丢弃。

## 2. 目标与非目标

**目标**

- Phase 1（P0）：
  - 每次 recall **恰好一次** hybrid 检索（Begin 的 seeder 成为唯一权威计算点，执行侧消费它）。
  - batch 内 normalized query 相同的消息**共享一次** recall 结果。
  - 行为等价：rounds、found、注入内容、ledger 语义不回归。
- Phase 2（Continuation）：
  - 同 channel 相邻 recall 间以**证据接续**（evidence continuation）复用上一轮 adopted 探索，降低净 token 开销；以遥测数据裁决去留。

**非目标**

- 不做 LLM session / KV cache 物理复用（EphemeralSession 架构、逐次变化的前缀与无 provider cache 契约之下不可行，grilling 已排除）。
- 不改 explore 工具协议（`/explore`、`/submit`）、adoption 规则（found 中 rounds 最少者）、GraphView/scope 过滤语义、consolidation 与版本 pinning。
- 不跨 channel/workspace 复用 prior；不做 embedding 语义去重（保留为 Phase 2 之后的升级路径）。
- Phase 1 不触碰 `daemon.go:2505` 的 runTask 单任务路径（单次调用无重复可合并）。

## 3. 现码事实（对照基线）

| 项 | 现码 |
| --- | --- |
| Begin 检索 | `seeder.Seeds(ctx, dir, version, query, view)` -> `[]string` 节点 ID，经 `persistPlan` 写入 ledger（每轨迹一份 seed batch）；plan 结构体不携带 seeds（`graph_memory_recall.go:267-295`） |
| 执行侧检索 | `Execute` 建 retriever 并 `RebuildForVersion` 后调 `explorer.Explore(ctx, plan.Query)`；Explore 内部 `ForkForVersion` + `retr.Search` 再算种子（`graph_memory_recall_execute.go:69-97`、`explore.go:147-157`） |
| 种子到 prompt | `seedSnippets(hits, version)` 从 pinned version 图 hydrate 正文，截断到 `expandSnippetChars`，staging 段走 `ReadStagingSegment`（`explore.go:344-370`） |
| query 来源 | `graphRecallQuery`：`task.ChatMessage` 优先，其后 TriggerCommentContent 等用户原文（`graph_memory.go:58-73`） |
| batch 循环 | `prepareResidentMessageBatch` 每消息构造 messageTask 并独立调用 `graphExecutionMemories`（`message_runtime.go:421-427`） |
| recall 调用 | `POST /api/daemon/graph-memory/recalls`，每次 `TraceID: uuid.NewString()`，失败非致命注入空（`daemon/client.go:167-173`、`graph_memory.go:18-56`） |
| K 轨迹与 adoption | K 条并行 EphemeralSession 轨迹共享同一 seeds；found 中 rounds 最少者被采纳，只有 adopted run 的 Summary/NodeIDs 成为结果（`explore.go:174-222`） |
| 轨迹落盘 | `TraceRecorder` best-effort 写 `trajectories/explore/<trace_id>__<run_id>.jsonl`，白名单清洗（provider key/session id 构造性排除），失败仅记日志，开关由 wiring 决定（`trace_writer.go:22-40`） |
| 检索默认 | `RetrievalConfig{TopK: 10, BM25Weight: 0.5}`（`retriever.go:32`） |
| 轮数上限 | `DefaultExploreConfig().MaxRounds = 6`，服务端权威计轮 |

## 4. Phase 1（P0）设计

### 4.1 单一权威 seed 路径

- `Begin` 的 seeder 成为**唯一** seed 计算点：`GraphMemoryRecallPlan` 增加 `Seeds []string` 字段，`Begin` 将 seeder 结果同时写入 plan 与 ledger（现状不变）。
- `Execute` 改调新 API `Explorer.ExploreWithSeeds(ctx, plan.Query, plan.Seeds)`。
- `ExploreWithSeeds` 语义：
  - `seedIDs` 非空 -> `seedsFromIDs(seedIDs, version)`：按 pinned version 从图 hydrate 正文 snippet，staging 段与 `seedSnippets` 同语义；**跳过内部 hybrid Search**。
  - `seedIDs` 为空 -> 回退现有内部 Search（backtest runner 与直连调用方行为不变）。
- `Explore(query)` 重构为 `ExploreWithSeeds(ctx, query, nil)` 的薄包装；`seedSnippets` 重构为「hits -> IDs -> `seedsFromIDs`」的薄包装（DRY，两条路径产出逐字节相同的种子）。
- 一致性依据：Begin 与 Execute 使用同一 pinned `plan.GraphVersion`，seeds 与 Explore 的版本视图天然一致。

### 4.2 batch 内 query 级合并

- `prepareResidentMessageBatch` 循环外建立 per-batch memo：`map[string][]execenv.MemoryContextForEnv`，key = `normalizeGraphRecallKey(graphRecallQuery(task))`（case-fold + 空白序列折叠）。
- 命中直接复用注入结果；未命中执行 `graphExecutionMemories` 并 memo（**nil 结果同样 memo**——recall 失败是非致命数据，near-simultaneous batch 内不重试）。
- 合并的 recall 记账在第一条消息的 TraceID 下（一条 recall 服务 batch 内相同 query 的全部消息）。

## 5. Phase 2（Continuation）设计（grilling 共识 D1–D11）

Phase 2 在 Phase 1 上线并量测出干净基线后启动（D12）。以下为已拍板的设计决定，Phase 2 计划文档据此展开。

### 5.1 prior record（D10）

- 管线自持、**per-channel 单条**：adoption 确定后由 recall 管线持久化，内容 = 白名单清洗后的 adopted run 消息流 + query + graph version + brief 缓存区。
- 生命周期：随 graph version 失效（consolidation 切版即作废）；被同 channel 下一次 found 的 recall 覆盖。`Found=false` 的 recall 无 adopted run，**不产生 prior**。
- 与 `TraceRecorder` 解耦（调试设施 best-effort 的语义不承载特性可用性）。

### 5.2 lazy per-B 压缩（D4 + D5）

- B 的 recall 启动时，压缩调用（A 的 adopted transcript + B 的 query -> query-aware brief）与 fresh hybrid 检索**并行**执行；两者完成后 explore loop 才开跑。
- 取材仅 adopted run transcript（放弃非采纳 runs 的 viewed 并集，换取低噪声）。
- 压缩失败/超时非致命降级为无 prior（与现有 recall 非致命语义一致）。
- B 的 K 条并行轨迹**共享同一份 brief**（一次压缩服务全部 K 条）。

### 5.3 跨 agent 复用与安全边界（D6）

- prior = 同 channel 内最近一次 found 的 recall，**无论哪个 agent 发起**（群聊相邻调用多为不同 agent，这是本设计的核心收益场景）。
- 注入前 prior 节点 ID 必须先过 B 自己的 GraphView/可见性过滤（continuation 层不得成为绕过 channel visibility 的通道）。
- brief 只含图证据（节点 ID、content hash、发现摘要、跨节点观察、被否定分支、未决问题），不含原始 transcript、会话内部信息或凭证。

### 5.4 注入形态与附着策略（D1 + D7）

- fresh hybrid seeds **永远保留**；B 的种子集 = fresh Top-K ∪ prior brief 节点（经 GraphView 过滤）。
- prompt 双命名区块：`Fresh retrieval candidates` 与 `Prior exploration evidence — tentative; re-read if needed`。
- **无条件附着**：channel 内存在有效 prior 即注入，不设相似度门。锚定偏差的缓解全部依赖 prompt 指令（prior 仅为起点、本轮 query 优先、可推翻、重读照常消耗 round）——该段指令是承重墙，回测须监控 found rate 有无因锚定恶化。

### 5.5 失效与去重（D8 + D11）

- 失效边界**仅 graph version**，不设年龄上限（陈旧 prior 最坏结果是压缩产出空洞 brief，白付一次压缩而非污染）。
- brief 缓存按（prior record，规范化 query）精确匹配去重；并发相同 query 用 singleflight 合并为一次压缩、共享结果。不做 embedding 语义去重。

### 5.6 预算与验收（D9）

- B 的 `MaxRounds` 不变、不加「可提前 submit」劝导；节约依赖更好种子下的自然收敛与 adoption 的最少-rounds 偏置。
- 验收判据：**净 token 开销**（压缩输入 + explore 全部轮次）相对 Phase 1 基线不降则整体回退；found rate 不得恶化。上线前以 backtest 回放对比。

## 6. 边缘 case 语义汇总

| Case | 语义 |
| --- | --- |
| `plan.Seeds` 为空（seeder 未接线/测试注入） | `ExploreWithSeeds` 回退内部 Search，行为与现状一致 |
| backtest runner / 直连 `Explore(query)` | 薄包装走内部 Search，不受影响 |
| replay（`LoadReplayInjection`） | 不触发 Execute，不受影响 |
| batch 内 query 仅大小写/空白差异 | normalize 后同一 key，合并 |
| batch 内 recall 失败 | nil 结果 memo，同 batch 不重试（非致命语义） |
| Phase 2：并发不同 query 的相邻 recall | 各自附着同一旧 prior 独立执行，互不冲突；先完成者更新 prior 指针 |
| Phase 2：compression 失败/超时 | 非致命降级为无 prior 的独立 explore |
| Phase 2：consolidation 切版 | prior 整体作废，B 走无 prior 路径 |
| Phase 2：staging 节点出现在 prior | 随 GraphView 过滤与版本失效语义处理，不特殊化 |

## 7. 验收测试

**Phase 1**

1. `ExploreWithSeeds` 使用提供的种子并跳过内部检索（提供的种子覆盖内部 Search 的 top hit）。
2. `ExploreWithSeeds(ctx, query, nil)` 与现 `Explore` 行为逐字节等价（含种子 snippet 截断与 staging 段）。
3. `Execute` 将 `plan.Seeds` 传入 explorer（wiring 编译级 + 现有套件回归）。
4. batch 内逐字相同的 query 只触发一次 `POST /api/daemon/graph-memory/recalls`；仅大小写/空白差异同样合并。
5. 不同 query 不合并；nil 结果被 memo。
6. rounds / found / 注入内容与 Phase 1 之前等价（现有 explore、recall、message-runtime 套件全绿）。

**Phase 2（启动时展开为独立验收）**

7. prior record 仅在 found recall 后产生、被覆盖、随版本作废。
8. 注入前 GraphView 过滤生效（channel-only 节点不借 prior 泄漏给不可见 agent）。
9. brief 精确匹配去重 + singleflight：并发相同 query 只压缩一次。
10. 净 token 开销对比与 found rate 监控落盘（回退判据可执行）。

## 8. 已知残余风险（用户知情选择，勿当 bug 修）

- **锚定无保险丝**：无条件附着（D7）× 无年龄上限（D8）之下，话题漂移时 B 可能被 A 的框架带偏；缓解只有 prompt 指令与 found rate 监控。
- **隔夜 prior 纯浪费**：安静 channel 重启对话时对陈旧 prior 白付一次约全量 transcript 的压缩。
- **跨版本结构盲区**：放弃节点级 hash 复验意味着不存在「部分存活」路径——版本切换即全量作废，复用率略降（换取简单与无假阳性）。
- **Phase 2 计量依赖**：`reward.go` 现仅计轮数；净 token 判据需在 Phase 2 动工时补齐 token/latency 遥测。

## 9. 范围外

- KV cache 物理复用、provider session 续接。
- explore 工具协议、judge/reward、consolidation、版本化与 GC。
- 跨 channel/workspace 的 prior 复用；embedding 语义去重。
- `daemon.go:2505` runTask 路径的合并（单任务单调用）。
- 通用 token 计量平台化（Phase 2 仅做判据所需的最小遥测）。
