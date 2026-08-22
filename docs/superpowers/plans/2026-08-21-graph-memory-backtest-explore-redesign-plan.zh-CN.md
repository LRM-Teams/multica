# Graph Memory 回测与 Explore 工具协议重设计实施计划

> 状态：参数已拍板（2026-08-21），待评审后开工
>
> 日期：2026-08-21
>
> 对应规格：`docs/superpowers/specs/2026-08-21-graph-memory-backtest-explore-redesign-spec.zh-CN.md`
>
> 共识来源：`multica/handoff.md`（四轮 grilling）

## 1. 目标

给 Explore 回测加严格封顶的预算分配，把 Explore 花在「最可能回归」的 query 上；轮数信号改为按访问节点数计；删除回归集与向后兼容包袱，冷启动从零积累；跳过 Explore 时诚实记录、不冒充实测。

不改动节点 scope/visibility、hybrid retrieval、judge/reward、版本化与 GC。

## 2. 开工 Gate

参数已于 2026-08-21 拍板默认值（用户确认 B 的动态形式；上限、ε、阈值、MaxRounds 按建议定默认，Phase 5 实证校准可改）：

- `B = min(窗口 query 数, 16)`：小窗口全量实测，大窗口封顶 16/候选；T=4 时并集 ≤ 64 次 Explore。
- `ε = 0.2`：L1/L2/L3 归一到 [0,1] 量级后 L3 占 1/5 权重。
- `ColdStartThreshold = 20`：recall tolerance 0.02 下 n≥20 统计才有意义。
- `MaxRounds = 6`（新协议）：1 轮 = 访问 1 节点，典型 GT 深度 2–3 hop 的 2 倍余量；Phase 5 若 miss 率偏高试 8。
- `MaxExpandPerRound=5`、`MaxNodeChars=2000` 保留独立上限不变。
- 删旧图与冷启动的一次性成本已确认可接受（不可回滚，用户拍板）。

Gate 已过，可进 Phase 0。

## 3. Phase 0：盘点并先写失败测试

### 3.1 Writer / reader inventory

- [ ] 列出 `/view`、`/expand`、`/submit` 的所有调用方与测试。
- [ ] 列出 `regression_set.jsonl`、`recordRegressions`、gate 4 的所有读写点。
- [ ] 列出 `AcceptedWithoutExplore`、`Rounds`、`recallRates`、`ComputeBaselineCoverage` 的消费方。
- [ ] 标注 `SelectWinner`/`CostWeights` 的输入依赖，确认只改样本、不改公式。

### 3.2 先写失败测试

- [ ] `/explore` 批量 N 节点消耗 N 轮（当前按 /expand 次数计，会失败）。
- [ ] 轮数耗尽后 `/explore` 返回 `budget_exceeded`、轨迹 `Found=false`。
- [ ] 跳过 query `Rounds=0`、`skipped=true`、不进 Mean/P95、不进 recall（当前 `AcceptedWithoutExplore` 填 n 并计数，会失败）。
- [ ] 每候选 top-B 并集：所有候选在并集上同测、同分母。
- [ ] 回归集删除后无 `regression_set.jsonl` 读写、gate 4 不生效。
- [ ] 冷启动（query-log < 阈值）跳过 gate 3 与轮数回归 gate。

验收：能明确回答每处旧行为由谁写/读，以及新逻辑将替换哪条路径。

## 4. Phase 1：Explore 工具协议合并

- [ ] 新增 `/explore` handler：返回目标节点正文（≤`MaxNodeChars`）+ 内嵌邻居（复用 `expandCandidates` 优先级序与 `MaxExpandPerRound` 上限，snippet ≤ `expandSnippetChars`）。
- [ ] 轮数按实际返回节点数计（rounds += N），服务端权威。
- [ ] 「节点数量」参数默认 1，可调大；批量线性计轮。
- [ ] 保留正文、邻居两个独立上限；graph view 过滤语义不变；staging 段不可 expand。
- [ ] 轮数耗尽返回 `budget_exceeded=true`、空结果（HTTP 200），轨迹标记 budget-blown，`/submit` 强制 `Found=false`。
- [ ] 移除 `/view`、`/expand` 端点（或保留为内部别名直到调用方全切换）。
- [ ] `ComputeBaselineCoverage` 的 k-hop 半径继续用 `Rounds`（取值来源不变，语义变为访问节点数）。

验收：验收测试 1、2 通过；agent 在新协议下能完成一条真实 Explore 轨迹。

## 5. Phase 2：D_q 与预算分配

- [ ] 定义 `BacktestBudget` 配置：`B`、`Epsilon`、`ColdStartThreshold`。
- [ ] 实现 `D_q = L1 + L2 + ε·L3`：L1 seed id Jaccard、L2 闭包正文 hash 差异 + 边 churn、L3 expand 候选 Jaccard。
- [ ] 每候选独立计算 D_q（候选 vs baseline），各自取 top-B；同分按 query 文本字典序。
- [ ] 实测集合 = 各候选 top-B 并集；所有候选在并集上实测。
- [ ] 窗口 query 数 < B 时退化为全量、无跳过。
- [ ] 覆盖失败 query 维持现行为（不跑 Explore、`Covered=false`、进 recall）。

验收：验收测试 3、5 通过；给定固定输入，D_q 排序与并集可复现。

## 6. Phase 3：跳过语义、recall 与回归集移除

- [ ] 未入并集 query：`Rounds=0`、`skipped=true`、记 `D_q` 与跳过原因；不进 Mean/P95。
- [ ] recall 只在实测并集上算；skipped query `Found=false` + `skipped=true`、不进 recall；baseline 侧同分母。
- [ ] 保留现有轮数回归 gate（`rounds > baseline + RoundsTolerance`），基线来自 query-log 新协议数据。
- [ ] 删除 gate 4、`regression_set.jsonl` 读写、`recordRegressions`、`RegressionEntry` 消费分支。
- [ ] 实测 query 统一进 Mean/P95（移除「回归集 query 不进」分支）。
- [ ] 无 runner 模式维持 `AcceptedWithoutExplore`，新跳过逻辑不适用。

验收：验收测试 4、6、8 通过；`SelectWinner` 输入样本为实测并集，公式与归一化不变。

## 7. Phase 4：冷启动与删旧图

- [ ] 实现 `ColdStartThreshold` 判断：query-log 条目数低于阈值时跳过 gate 3 与轮数回归 gate，只留 gate 1/2；达到阈值恢复。
- [ ] 迁移/初始化脚本：删除旧图版本、旧 query-log Rounds、旧回归集（一次性、不可回滚）。
- [ ] 冷启动从空图积累，覆盖半径缺省 `DefaultBacktestBaselineRounds=2`。
- [ ] 不保留任何旧协议数据读取路径。

验收：验收测试 7、9 通过；删旧图后首次整理能正常完成且无旧基线依赖。

## 8. Phase 5：接线、参数校准与集成

- [ ] 将 `BacktestBudget` 接入 `jobs_graph_memory.go` 的 runner 装配（仅 `TTVTrajectories > 1`）。
- [ ] 校准 `MaxRounds`（默认 6，miss 率偏高试 8）、`B` 上限（默认 16）、`ε`（默认 0.2）、`ColdStartThreshold`（默认 20），记录取值依据。
- [ ] 度量并记录：D_q 单次计算成本、并集大小分布、新协议 miss 率。
- [ ] 若 D_q 比 Explore 贵，给 L3 加缓存或仅在 L1+L2 接近阈值时算 L3。
- [ ] 跑完整回归套件与端到端 consolidation。

验收：验收测试 10 通过；一次真实 TTT consolidation 在新协议 + 预算分配下完成选版。

## 9. Rollout

1. Phase 0 本地契约测试；
2. Phase 1–3 单 workspace 影子回测（新旧并行，不切版）；
3. 删旧图 + 冷启动在隔离 workspace 验证；
4. 参数校准后单 workspace 正式启用；
5. 稳定后再扩量。

立即停止扩量的条件：

- 跳过 query 被误计入 Mean/P95 或 recall；
- 候选间 Cost 因样本不一致而不可比；
- 冷启动 gate 降级未生效导致除零/误杀；
- 轮数预算被批量请求绕过；
- 删旧图后仍读取旧协议数据。

## 10. 明确后置

以下内容不进入本计划：

- 向后兼容、旧图迁移、旧协议数据保留（用户明确删除）。
- 回归集、gate 4、硬触发、「子图不变复用 Rounds」的重新引入。
- 通用预算 reservation/ledger 平台。
- 整条 TTT 回路主目标（第一轮搁置的原 Q1/Q2）。
- 节点 scope/visibility、hybrid retrieval、judge/reward、版本化、GC 的任何改动。

若实现中证明需要，再分别立项，不提前塞入本计划。

## 11. 提交切片

1. Phase 0 inventory + failing tests；
2. `/explore` 合并 + 轮数按节点数计；
3. `D_q` + 每候选 top-B + 并集实测；
4. 跳过语义 + recall/gate 调整 + 回归集移除；
5. 冷启动阈值 + 删旧图迁移；
6. 接线 + 参数校准 + 集成。

每个切片独立评审，包含迁移边界、回滚方法（删旧图切片需显式标注「不可回滚」）和定向测试。

## 12. 完成定义

- `/explore` 合并生效，轮数按访问节点数计，预算硬封顶不被批量绕过。
- 每候选独立 D_q top-B、并集实测、全候选同测，Cost 可比。
- 跳过 query 诚实记录，不进 Mean/P95、不进 recall。
- 回归集与 gate 4 完全移除，单点回归由 gate 3 兜底。
- 冷启动阈值降级生效，删旧图后从零积累、无旧基线依赖。
- 一次真实 TTT consolidation 在新设计下完成选版，`SelectWinner` 公式不变、仅输入样本改为实测并集。
- 普通非 TTT 模式行为不变。
