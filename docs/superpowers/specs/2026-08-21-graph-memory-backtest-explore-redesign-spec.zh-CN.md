# Graph Memory 回测与 Explore 工具协议重设计规格

- 日期：2026-08-21
- 状态：设计已确认；待实现（四轮 grilling 共识，见 `multica/handoff.md`）
- 范围：仅 Graph Memory 的**回测（backtest）**与 **Explore 工具协议**。不改动节点 scope/visibility、hybrid retrieval、judge/reward、版本化与 GC 等既有原语
- 对照：`docs/superpowers/specs/2026-08-17-graph-memory-scope-design.md`（scope 模型保持不变；本规格只触碰其 backtest 与 explore 两处）
- 配套计划：`docs/superpowers/plans/2026-08-21-graph-memory-backtest-explore-redesign-plan.zh-CN.md`

## 1. 问题陈述

当前 Explore 回测存在四类成本与信号问题：

1. **Explore 开销不可控**。回测对窗口内每条 query 都可能跑 Explore agent（多轮 LLM 调用），候选图数 `TTVTrajectories` 默认 4，总开销随 query 数线性放大，且没有预算分配机制。
2. **轮数信号与工具耦合**。`/view` 免费、`/expand` 计轮，agent 能免费读大量节点、只把轮数花在「展开」上。轮数（Rounds）因此衡量的是「展开次数」，不是「访问节点量」，与 agent 真实成本不对齐。
3. **回归集只增不减**。`regression_set.jsonl` 每次切版追加回归 query，无毕业机制，回测成本单调上涨；且其基线轮数绑定旧工具协议，协议一变即失真。
4. **跳过 Explore 时冒充实测**。无 runner 时 `AcceptedWithoutExplore` 直接把历史轮数 `n` 填进 `Rounds` 并计入统计，等于把「没测」说成「测过且一样好」，污染选版 Cost。

## 2. 目标与非目标

**目标**

- 给 Explore 回测加一个**严格封顶**的预算分配机制，把 Explore 花在「最可能回归」的 query 上。
- 让轮数信号与 agent 真实成本对齐（按访问节点数计轮）。
- 删除回归集与向后兼容包袱，冷启动从零积累，消除只增不减与协议失真。
- 跳过 Explore 时**诚实**记录，不冒充实测。

**非目标**

- 不改节点 scope/visibility、hybrid retrieval 排序、judge/reward 逻辑。
- 不引入向后兼容或旧图迁移（用户明确删除、不兼容）。
- 不重建版本化、GC、consolidation 的轨迹生成机制。

## 3. 现码事实（对照基线）

| 项 | 现码 |
| --- | --- |
| 回测样本 | 相邻版本窗口 `(prev, current]` + 永久回归集 |
| 轮数预算 | `ExploreConfig.MaxRounds=3`；每次 `/expand` 消耗 1 轮，`/view` 免费 |
| 邻居候选 | `/expand` 返回 ≤ `MaxExpandPerRound=5` 条，含 id/via/level/snippet（snippet ≤ `expandSnippetChars=200`） |
| 节点正文 | `/view` 返回正文，≤ `MaxNodeChars=2000`，超则 `truncated` |
| 覆盖检查 | `ComputeBaselineCoverage` 用 `k = adoptedRounds`（缺省 `DefaultBacktestBaselineRounds=2`）的 k-hop 邻域判 GT 是否可达 |
| 硬门槛 | gate1 图校验；gate2 staging 覆盖；gate3 聚合 recall ≥ baseline − `RecallTolerance=0.02`；gate4 回归集单 query 零容忍否决 |
| 轮数回归 | `rounds > baseline + RoundsTolerance(1)` 判 `Regressed` |
| Mean/P95 | 窗口里 judge 通过（`JudgeScore ≥ JudgeThreshold=0.6`）且 `Found` 的轮数；回归集 query 不进 |
| Cost | `CostWeights{Round:1.0, Tail:0.5, Embed:0.2, Node:0.1, Graph:0.05}`；embed/node/graph 归一化后加权 |
| 选版 | 硬门槛幸存者进 `SelectWinner`；current 不进；全灭留原图 |

## 4. Explore 工具协议重设计

### 4.1 合并 `/view` 与 `/expand` 为 `/explore`

`/explore` 一次调用返回**目标节点正文 + 内嵌邻居边信息**：

- 正文：沿用现 `/view` 语义，≤ `MaxNodeChars`，超则 `truncated`。
- 邻居：沿用 `expandCandidates` 的优先级序（hierarchy parents/children → entity_refs → typed relation → embedding）与 `MaxExpandPerRound` 上限；每个邻居含 id/via/level/snippet（snippet ≤ `expandSnippetChars`）。
- agent 看到一个节点时**同时看到其邻居**；要读某邻居全文，需对该邻居再调一次 `/explore`。
- 保留两个独立上限（正文 `MaxNodeChars` + 邻居条数 `MaxExpandPerRound`），不合并成单一上限，防高度节点撑爆 agent 上下文。
- graph view 过滤语义不变：邻居不得暴露 caller 不可见的节点；staging 段不可 expand。

### 4.2 轮数语义改为「访问节点数」

- 每次 `/explore` 调用消耗轮数 = **实际返回的节点数**（rounds += N），服务端权威。
- 「节点数量默认 1」：单次调用默认拉 1 个节点，可调大；批量按返回节点数线性计轮，**不绕预算**。
- `MaxRounds` 保留为硬预算：轮数耗尽后 `/explore` 返回 `budget_exceeded=true`、空结果（HTTP 200），轨迹标记 budget-blown，`/submit` 强制 `Found=false`。
- 超预算轨迹不被采纳，沿用现语义。

### 4.3 覆盖检查

`ComputeBaselineCoverage` 的 k-hop 半径继续使用 `Rounds`（新语义：agent 在 k 次 `/explore` 内可达 k-hop）。半径含义从「展开次数」变为「访问节点数」，但取值来源不变。

## 5. 回测预算分配

### 5.1 子图变化度 `D_q`

所有 query（不再区分窗口/回归）计算变化度：

```text
D_q = L1 + L2 + ε·L3
```

- **L1（seed）**：retrieval 命中节点 id 集合的 Jaccard 距离（候选 vs baseline）。
- **L2（结构邻域）**：`MaxRounds` 闭包内节点正文 hash 差异 + 边 churn 比例。
- **L3（expand 列表）**：expand 候选 id 集合的 Jaccard 距离。
- `ε` 取小（建议 0.1–0.3），因为 L3 是间接影响且计算最贵（需 embedding 邻居）。
- 同分按 query 文本字典序，保证可复现。

### 5.2 每候选独立排序 + top-B + 并集实测

- **每候选图独立**计算 `D_q`（候选 vs baseline），各自取 top-B。理由：不同候选改的子图区域不同，全局排序会测一堆无关 query。
- **实测集合 = 各候选 top-B 的并集**，且**所有候选都在并集上实测**。修复可比性：若候选各测不同 query 集，`MeanRounds`/`P95Rounds` 在不同样本上算，`SelectWinner` 的 Cost 比较失真。并集保证同样本、同分母。
- 成本 = |并集| × T；候选改区重叠多时并集远小于 T·B。
- 窗口 query 数 < B 时 top-B 退化为全量，无跳过。

### 5.3 跳过的诚实语义

- 未入并集的 query **不跑 Explore**：`Rounds=0`、`skipped=true`、记录 `D_q` 值与跳过原因。
- 跳过的 query **不进 Mean/P95**，不冒充实测。
- 覆盖失败的 query 维持现行为：不跑 Explore、`Covered=false`、进 recall 统计（结构不可达，跑了也白跑）。

### 5.4 无 runner 模式

`TTVTrajectories ≤ 1`（无 runner）时维持 `AcceptedWithoutExplore` 现状，新跳过逻辑不适用——该模式无 Explore 可省、无选版可比。

## 6. Recall 与 gate 语义

- **跳过的 query 不进 gate 3 recall**：`Found` 记 `false` 但标 `skipped=true`；recall 只在实测并集上算。baseline 侧同集合、同分母，候选间可比。recall 语义统一为「实测通过」。
- **不加「轮数上界 gate」**：`MaxRounds` 服务端强制，`rounds` 不可能超（超预算轨迹 `Found=false`、走 recall），轮数膨胀已由 gate 3 + Cost 的 Mean/P95 覆盖。
- **保留现有轮数回归 gate**（`rounds > baseline + RoundsTolerance`），靠 query-log 重新积累的**新协议**基线运转；冷启动无基线时随 §7 阈值逻辑跳过。
- **删除 gate 4 与回归集**：`regression_set.jsonl`、`recordRegressions`、gate 4 全部移除。单点慢性回归由 gate 3（聚合 recall，带 tolerance）兜底。原「回归集 query 不进 Mean/P95」分支随之消失，实测 query 统一进统计。

## 7. 冷启动与迁移策略

- **删除旧版图，不做向后兼容**：旧图版本、旧 query-log Rounds、旧回归集一并清除，新系统从空图/冷启动积累。
- 这消解了「换代时序」问题：没有旧基线要迁移，不需要「重测轮不选版」的过渡轮——冷启动前几轮本来就没历史数据，覆盖半径/轮数回归 gate 用默认值兜底即可。
- **`ColdStartThreshold`**（query-log 条目数，建议初始 10，实现期校准）：条目数低于阈值时跳过 gate 3（recall，baseline 侧为空/除零）与轮数回归 gate，只留 gate 1/2；达到阈值自动恢复全量 gate。必须显式实现，不是自然退化。

## 8. 边缘 case 语义汇总

| Case | 语义 |
| --- | --- |
| D_q 排序同分 | query 文本字典序 |
| 窗口 query 数 < B | top-B 退化为全量，无跳过 |
| 覆盖失败 query | 不跑 Explore、`Covered=false`、进 recall |
| 无 runner 模式 | 维持 `AcceptedWithoutExplore`，新跳过逻辑不适用 |
| judge 失败/未 judge 的实测 query | 不进 Mean/P95（沿用现逻辑），保留 `Found`/`Rounds` 记录 |
| skipped query 的 recall | 不进 recall，`Found=false` + `skipped=true` |
| 冷启动 | `ColdStartThreshold` 阈值降级 gate 3 与轮数回归 gate |
| 高度节点 | 正文、邻居两个上限独立保留 |

## 9. 配置参数

新增/调整（2026-08-21 拍板默认值，Phase 5 实证校准可改）：

- `BacktestBudget.B = min(窗口 query 数, 16)`：小窗口全量实测，大窗口封顶 16/候选；T=4 时并集 ≤ 64 次 Explore。
- `BacktestBudget.Epsilon = 0.2`：L1/L2/L3 归一到 [0,1] 量级后 L3 占 1/5 权重。
- `BacktestBudget.ColdStartThreshold = 20`：recall tolerance 0.02 下 n≥20 统计才有意义。
- `ExploreConfig.MaxRounds = 6`（新协议）：1 轮 = 访问 1 节点，典型 GT 深度 2–3 hop 的 2 倍余量；轮数通胀是协议固有代价，miss 率偏高时试 8。
- `ExploreConfig.MaxExpandPerRound=5`、`MaxNodeChars=2000`：保留两个独立上限。

其余沿用现默认（`RecallTolerance=0.02`、`JudgeThreshold=0.6`、`RoundsTolerance=1`、`CostWeights` 不变）。

## 10. 验收测试

1. `/explore` 单节点调用返回正文 + 邻居，邻居遵守 graph view 过滤。
2. `/explore` 批量 N 节点消耗 N 轮；轮数耗尽返回 `budget_exceeded`、轨迹 `Found=false`。
3. 每候选独立 D_q 排序；实测集合为各候选 top-B 并集；所有候选在并集上同测。
4. 跳过 query 记 `Rounds=0`、`skipped=true`、`D_q`、原因；不进 Mean/P95、不进 recall。
5. 窗口 query 数 < B 时全量实测、无跳过。
6. 回归集/gate 4 移除后，单点回归由 gate 3 兜底；无 `regression_set.jsonl` 读写。
7. 冷启动（query-log < 阈值）跳过 gate 3 与轮数回归 gate；达到阈值恢复。
8. 无 runner 模式维持 `AcceptedWithoutExplore`。
9. 删旧图后冷启动从零积累，无旧基线依赖。
10. Cost 公式与 `SelectWinner` 归一化逻辑不变，仅输入样本改为实测并集。

## 11. 已知残余风险（用户知情选择，勿当 bug 修）

- gate 4 删除后，单 query 慢性回归仅由 gate 3（带 tolerance）兜底，可能被 tolerance 吸收、静默通过选版。
- L1/L2 稳定但 L3 剧变的 query 可能排不进 top-B（ε 小且无硬触发）。
- **冷启动期无历史基线**：前几轮覆盖半径用默认 2、轮数回归 gate 参考缺失，选版能力弱；随 query-log 积累恢复。这是「删旧图」的固有代价。
- **不可回滚**：删旧图 + 不做向后兼容意味着新协议上线后若发现设计缺陷，无法退回旧图/旧协议，只能向前修。
- 轮数通胀：新协议读邻居计轮，同 query 天然比旧协议耗更多轮，miss 率冷启动期偏高，`MaxRounds` 需重调。

备选缓解（未拍板，仅在风险显现时提出）：query-log 记回归次数作观察信号，或给有前科 query 加分项。

## 12. 范围外

- 节点 scope/visibility、hybrid retrieval 排序、judge/reward、版本化、GC、consolidation 轨迹生成。
- 向后兼容、旧图迁移、旧协议数据保留。
- 通用预算 reservation/ledger 平台。
- 整条 TTT 回路主目标（第一轮搁置的原 Q1/Q2）。
