# Graph Memory PAST-Bench 恢复规格

- 日期：2026-09-01
- 状态：设计已确认（grilling 会话逐条裁决）；实施待排期
- 北极星：**提高 PAST-Bench 官方对齐准确率**（on 0.536→≥0.60、info gathering 0.267→≥0.50、strong 归因破零），**附带修复生产 recall 404**（~3.2 万次/天）
- 上游证据：
  - 根因分析：`areal/.agents/skills/graph-memory-regression/references/graph-memory-deficiency-analysis-20260901.md`（六不足点 + 代码行号）
  - 22 族基线报告：`areal/.agents/skills/graph-memory-regression/references/official-22-family-report-20260901.md`
  - Handoff：`areal/.agents/skills/graph-memory-regression/references/handoff-20260901-session.md`
- 被修订的上位规格：`docs/superpowers/specs/2026-08-27-universal-interaction-dag-graph-memory-pipeline-spec.zh-CN.md`（worktree `feat/universal-interaction-dag-20260827`，下称 universal spec）
- 落点策略（裁决 Q8-c）：**merge-independent 项现在落 main；pipeline 相关项作为 worktree amendment 随 merge 评审；bench 复测等 worktree 部署**

## 0. 裁决记录（grilling 会话，Q1-Q11）

| # | 裁决 |
|---|---|
| Q1 | A+B：bench 准确率为北极星，生产 404 修复捆绑 |
| Q2 | 范围 = P0+P1+P2+P4；P3（procedural/skill 域）另立 spec；P2-10 kind 分类保留为共同前置 |
| Q3 | 原裁"统一回合段 + channel_message 直读"，**经 Q8 修订**：被 universal pipeline 取代，降级为验证项（见 §C1） |
| Q4 | 空内容防线：consolidation 输入过滤（仅 pre-merge 场景）+ harness gate 断言重试≤2 次（保留，双形态断言） |
| Q5 | recall 404：服务端双形状身份解析 + mode 门禁后移；**严格分离**（graph workspace 不回退 legacy 记忆） |
| Q6 | start 响应补三件套召回元信息；重查=幂等 key 派生规则改 prompt 级 + 服务端 start≤3/run 上限；策略引导 + 行为标记，不硬闸 |
| Q7 | 双通道：业务 agent 图检索工具（走 v2 external Search API）+ memory agent 可见回复携带引用；ablation 口径显式化 |
| Q8 | 混合落点：merge-independent 落 main，pipeline 项为 worktree amendment |
| Q9 | eval 保持同 channel；**主对外指标 = on/off ablation Δ**；P4-15 改硬归因信号；消息隐藏特性挂 follow-up |
| Q10 | 两段式复测（4 族冒烟门 → 26 族全量）+ §D 阈值表 |
| Q11 | 本文档位置：multica 主仓库 docs/superpowers/specs/ |

## 1. 问题摘要

六不足点及其根因详见根因分析。与 universal pipeline 对账后的关键修正：

1. **学习进图率低（不足 1，P0）**：learn 证据经双路径入 staging——primary agent task 段（`23db5dfae`，trajectory 依赖 daemon 异步上传的 task_messages，存在竞态，产 no-content 空段与占位节点）+ memory agent run 段（`192a16110`，仅含触发消息）。universal pipeline 的 generation 模型（§6.2）、durable publisher（§7）、derivative 段不产 Atom（§8.2）、zero-Atom 语义（§8.1）**在结构上解决全部四个子问题**（竞态、空段污染、双路径重复、一回合一段）。
2. **检索行为缺失（不足 2，P1）**：start 无召回元信息（agent 无法判断"信息是否足够"）、幂等锁禁止换角度重查、策略引导缺位、业务 agent 工具真空。**universal spec 未覆盖**，由本规格 B1-B3、B8 补齐。
3. **生产 recall 404（不足 3，P1）**：`Begin` 按 `agent_inbox_event.id` 硬查，channel resident 路径传 `channel_message id` 必 miss；身份校验先于 mode 门禁。#2295 后频道对话不产 inbox event，**修复只能在服务端**。merge-independent。
4. **procedural/skill 无处安放（不足 4，P3）**：另立 spec，本规格只经 B6（Atom kind 分类）铺路。
5. **fact 更新链断裂（不足 5，P2）**：universal spec §10 未设计 working set/supersede 指引，由 B5 补。
6. **图杂讯与评测口径偏松（不足 6，P2/P4）**：zero-Atom + §9.1 认知状态过滤覆盖大半；去重（B7）、历史占位清理（B9）、评测硬归因（A2）补齐。

## 2. Part A — merge-independent 项（现在落 main）

### A1. recall 404 修复（Q5；对应不足 3）

改动点（`server/internal/service/graph_memory_recall.go`）：

1. `Begin` 身份解析改双形状：先按 `agent_inbox_event.id` 查，miss 则按 `channel_message.id` 反查（message → channel → workspace 归属校验）。ledger 按实际解析出的形状记账。
2. mode 门禁后移到形状解析之后：agent-mode workspace 的 resident 路径请求返回 200/disabled 而非 404（消除告警噪音），graph+inject 组合的频道召回真实生效。
3. **严格分离**：graph workspace 不回退 legacy 记忆（graph miss = 合法空结果：零注入 + ledger 记录）。legacy→graph 知识迁移走显式工具，挂 follow-up（§E2），不进运行时。
4. 观测：按天监控 `graph_memory_recall` reason 分布（identity/disabled/empty/conflict）；上线后生产 404 速率应降为 ~0，disabled 应成为 agent-mode workspace 的主导 reason。

验收：生产 404 ~3.2 万/天 → ~0；graph+inject 频道场景注入恢复。

### A2. driver 侧改造（areal skill `graph-memory-regression/scripts/gm_official2.py`）

1. **harness gate（P0-4，双形态断言，Q4b-ii）**：learn barrier 后断言本 episode 学习证据落库——
   - pre-merge 形态：staging 段非空且不含 no-content/empty-segment tag，图版本推进；
   - post-merge 形态：episode 产出 ≥1 个 published active Atom 且 class-aware search 可见。
   - 不满足 → 以新 `client_message_id`（attempt 后缀）重发该 episode，最多 2 次；仍失败标 family BLOCKED 并大声失败。**残败 = 管线 bug，不进入评分。**
2. **P4-15 硬归因信号**：废弃"图非空"代理（`memory_injection_count`）。改为：比对 learn episodes 写入的 atoms/nodes（typed ref，post-merge 需处理 `graph_node|atom` 两类）与 eval 轮 memory agent start 返回的 seed refs——命中才记 `memory_injection` 信用。
3. **P4-16 口径（Q9-iii）**：eval 保持同 channel。报告主指标 = on/off ablation Δ（两臂 channel 历史可见性相同，Δ=图净贡献）；绝对分照报并标注同 channel 协议偏差（memory 域 outcome 系统性偏高的可能）。
4. **capability 记录**：episode_trace 记录每族协商的 memory capability（v1 node-only vs `memory_explore_v2`）；报告混用时按 capability 分列。
5. **零 explore submit 标记读取**：B3 的服务端行为标记进入 episode_trace，供归因分层。

### A3. 复测协议（Q10；详见 §D）

两段式：4 族冒烟门（不烧全量预算）→ 26 族全量复测。

## 3. Part B — worktree amendments（随 merge 评审）

> 全部叠加在 `feat/universal-interaction-dag-20260827` 未提交工作之上。逐节标注对 universal spec 的修订关系。

### B1. start/Explore plan 补召回元信息（Q6a；修订 §9.4）

- v1 `start` 响应（`memorygraph/explore_tools.go`）additive 增加：`total_nodes`（当前 pinned 版本图总节点数）、`hit_count`（top-4 截断**前**的命中总数）、每 seed 的 `score`。v1 客户端忽略未知字段，兼容。
- v2 Explore plan（`memory_explore_plan.go`，已存在 trajectory_id/graphs/watermarks/budgets，**取证确认无召回元信息**）加入同三字段。
- 目的：给 agent 的"信息是否足够"判断提供依据（不足 2 第 2 类原因：区分"相关的就这 4 个"与"检索只召回 4 个"）。

**B1b. seed 多样性（MMR）**（来源：WikiSkill 论文调研会话裁决）：`handleStart` 的 top-4 截断现为纯 top-k + `AllowsNodeID` 过滤（`explore_tools.go:620-632`），同簇节点可全占 4 席，info gathering 覆盖率天然受限。截断处增加 MMR 式簇内去重（相似簇只留最高分），与 B1 同落点，直接服务复测。

### B2. 换角度重查（Q6b）

- 幂等 key 派生规则改 prompt 级：`graphMemoryAgentToolContext` 中 start 的 key 从 "message + operation" 改为 "message + operation + query"——不同 query=新 key=新 start（同 query 幂等重放），服务端无需协议变更。
- 服务端护栏：每 run 的 start 操作数上限 3，超出返回终态拒绝；start 计入 §9.7 硬预算（max_tool_calls=32、max_rounds=6）。
- run→trajectory 1:1 结构已证实支持同 trajectory 多 start op（ops 按 trajectory+graph+key 记账），无需 schema 变更。

### B3. 策略引导与行为标记（Q6c；并入 Task 12 daemon tool context 重写）

- 重写工具说明书加入"何时判定信息不足"策略：
  - `hit_count==0 且 total_nodes>0` → 图有内容但不匹配此 query → 换角度重查（B2）或如实回答"图中无此知识"；
  - seeds 看起来不完整 → explore 邻居后再 submit；
  - 有实质发现 → 以可见回复携带引用（Q7-ii，一句措辞）。
- 服务端行为标记：seeds 非空但零 explore 即 submit 的 run 打标记（进 A2-5 的归因分析），**不硬阻断**（保持协议保真与 agent 自主性；空图死循环风险也不存在）。

### B4. publisher 空段跳过 atomizer（P0-2 变体；修订 §7）

`content_status=empty` / 空轨迹的 segment：publisher 不调用 atomizer（省一次模型调用），直接标记 zero-atom 终态。metadata-only 段（§6.2）不产生任何模型消费。

### B5. consolidation working set 与 supersede 指引（P2-9；修订 §10）

universal spec §10 定义了 Atom 消费账本但未设计 working set 装配：

- 标准 consolidation 路径装配 working set：现有节点（按预算截断，cap 建议以 token 预算为准）注入 consolidation prompt，附 supersede 指引（"outdated node 用 update_node + supersedes 边"）。
- 装配点下沉为 consolidation 默认行为（覆盖自动调度与手动 consolidate），不再只存在于 research 维护轮。
- 受益指标：SM03/SM04 update_correctness（旧值 500 与新值 800 同屏问题）。

### B6. Atom kind 分类体系（P2-10；精化 §8.1）

Atom contract 已有 `kind` 字段但未定义分类。atomizer prompt 要求输出：

```
kind: preference | rule | fact | procedure
```

- procedure 保留步骤/触发条件/工具约束结构，**不受句数压缩限制**（为 P3 spec 铺路）；
- **procedure kind 的路由（已裁决，P3 蓝图）**：consolidator 一次判断、两种产出——fact/preference/rule 走 graph ops（现有 consolidation 事务，TTT 门控）；**procedure 路由到独立的 skill-proposal 队列**（不落图、不动 skill），由独立门控泳道的 skill proposer（memorycuration L3 改造版）消费。判断点合并、突变通道分离——graph 是 compound 契约（append/supersede、永不回滚），skill 是 gated 契约（验证不过即回滚），两者不可塞进同一事务通道。

### B7. 确定性去重（P2-12；补充 §10）

- consolidation 输入装配时对 active atoms 做相似度预检：同一事实多 episode 学习（多 atoms）→ 合并评估，替代纯 LLM 自觉。
- `add_node` apply 时对 top-k embedding 相似既有节点强制走 merge/update 评估（相似度阈值 shadow 期校准，硬上限先行）。
- 受益指标：SM06 pollution、PG 族信噪比。

### B8. 业务 agent 显式图检索工具（Q7-i；复用 Task 13）

`multica memory search` 接 v2 external Search contract（Task 13），而非直挂 HybridRetriever：

- corpus = current graph nodes + active atoms（class-aware、scope-filtered，复用 §9.1 全部过滤语义）；
- 只读，无幂等/账本复杂度；channel/project 作用域由调用方环境决定；
- **ablation 口径**：off 臂必须同步禁用业务 agent 图检索工具，否则 Δ 虚高（写进 driver 配置项）。

### B9. 历史占位节点一次性清理（补充，无 universal spec 对应节）

- 生产/存量图中 `n_group_empty_*`/`n_leaf_empty_*`/`n_theme_gm_e2e_empty_*` 占位节点的一次性清理 job（retract + 版本推进），作为运维项独立执行，不阻塞复测（复测用新频道新图）。

### B10. 认知状态过滤实现验证（P2-11；验证 §9.1）

- §9.1/acceptance 16 已规定 superseded/invalidated 不作默认 seed——**验证 worktree 实现**（atom_index/search 路径）确实生效，不重复设计。
- explore 侧同步：node 详情带 epistemic 注解且非 current 的默认不出现在种子视图。

## 4. Part C — post-merge 验证项（Q3 降级产物）

1. **learn 事实链路**：channel turn → segment → atoms → class-aware search 可见（冒烟门 A3 覆盖）。
2. **derivative 无 Atom 规则生效**：memory agent run 段（Path B）进入 DAG 但不产 Atom——双路径重复学习在结构上消失。
3. **竞态闭环**：daemon 在 task_messages 上传前崩溃 → segment 以 empty 发布 → zero-atom、无污染、无学习（可接受终态）；publisher 重试窗口内上传到达则正常学习。
4. **遗留取证项**：SM03 的 Path B 为何未产出含 freeze date 的段（target_seq 依赖或 submit 未发生）——post-merge 该路径已被 derivative 规则重构，取证转为回归断言（harness gate 兜底）。
5. **eval 污染残项**：primary agent 的 eval Q&A turn **非 derivative、照产 Atom**——eval 回答可能经 atoms 进入图。P4 污染测量保留，冒烟门观察其规模。

## 5. Part D — 复测协议与验收阈值（Q10）

### D1. 两段式复测

> **前置风险（2026-09-01 部署引入的 #2295 回归）**：canonical delivery 频道回合不再铸任务 → `graph_memory_agent_run` 停铸 → 两条 ingest 路径（Path A 任务段 + Path B run 段）同时断源，channel 图摄取归零、版本冻结（WikiSkill Phase 1c 实测 GATE 拦截，详见 SKILL.md「#2295 环境坑」）。复测前必须确认目标部署的频道回合→摄取链路存活；驱动侧已内置 bounce_daemon 缓解（批量建频道 → 重启本机 daemon → reconcile 拉起 → 补投滞留消息）。若服务器侧修复（canonical 回合重建铸任务）先于本 spec 落地，A2 driver 的 harness gate 会直接暴露链路存活与否。

1. **冒烟门（worktree 部署后）**：4 个最差族（PG02/PG03/SM03/PC01）——transport gate 3/3 → 3 条 learn → 断言 atoms published + search 可见（A2-1 post-merge 形态）→ 1 条 recall 验证 start seeds 命中（A2-2）。**不过冒烟不跑全量**。
2. **全量 26 族**（B 节 amendment 合并部署后）：基线 = official2-run2 regraded（26 族终版数字，待 regrade 完成后固化）；driver 记录 capability 并按需分列。

### D2. 阈值表

| 指标 | 基线 | 门槛 | 来源 |
|---|---|---|---|
| info gathering 域 | 0.267 | **≥0.50** | universal pipeline |
| overall on | 0.536 | **≥0.60** | 全部 |
| 显式检索率 | 3.5% | **≥25%** | B1/B2/B3 |
| memory_injection 硬信号 | “图非空”代理 | **≥70% learn episodes** | A2-2 |
| 归因 | 0 strong / 3 medium | **≥1 strong / ≥8 medium** | B1-B3 + 硬信号 |
| memory 域 mech | 0.671 | **≥0.80** | B5/B6/B7 |
| 新族图杂讯占比（类 PG05 63%） | — | **<10%** | zero-atom + B7/B10 |
| procedural 域 | Δ=−0.461 | **不设门**（P3 另立，显式标注） | — |
| 生产 404 | ~3.2 万/天 | **~0**，disabled 为主导 reason | A1 |
| harness gate 残败（2 次重试后） | — | **=0** | A2-1 |

### D3. 报告口径

- 主指标：on/off ablation Δ（对官方可比的图净贡献）；
- 绝对分照报 + 标注同 channel 协议偏差；
- 结果分与机制分分离呈现（SM01 型"高分零图"应现形为零机制分）；
- 官方收敛（on 0.66 / info 0.71）为二期目标，本规格门槛为恢复性门槛。

## 6. Part E — follow-up 提案（不进本规格）

1. **P3 procedural/skill 域 spec**（蓝图已定，待独立 grilling 细化）：
   - **架构**：co-evolution 三层（WikiSkill 论文 2608.27454 同构）——graph = Wiki 层（compound），SKILL.md = Skill 层（gated），consolidator = 共享判断点（B6 kind 路由），memorycuration L3 = 独立门控泳道的 skill proposer（去掉 `jobs_memory_curation.go:104-108` 的 NOT EXISTS 排除，输入源改接 procedure-atom 队列）；
   - **skill 的图内表达（已取证裁决）**：skill 本体 = 文件系统 artifact（执行真相，Anthropic skill 目录格式）；图内建**注册影子节点**（导航/溯源真相）。现有边白名单（`types.go` 13 种）对单 skill 的溯源/版本/知识耦合**已够用**（组合关系另见 skill 链条目，那是唯一 schema 增量）：`derived_from`/`evidence_for`（procedure atom → skill 影子的溯源）、`supersedes`（skill 版本链，复用 B5 同一 supersede 机器）、`refines`（skill 演化）、`invalidates`/`supports`（事实修正与 skill 假设的知识耦合——源撤回经 procedure atom 传播到 skill 影子）；has_step 类过程性关系**不需要**（步骤结构是 SKILL.md 内部组织）；影子节点以 `content_hash` 绑定 SKILL.md（复用 atom 的 `artifact_ref`/`content_hash` 机制，漂移可检测）；
   - **skill 影子节点进图后**：gateway start 可检索到 skill → PC 族 procedural 检索路径打通 → driver `skill_read_count` 硬编码 0 的问题随机制信号闭环；
   - **skill 链（复合组合）的图内表达（已裁决）**：链分三层——观察到的链（Interaction DAG 轨迹 + procedure atom body，已可表达）/ 声明的链（复合 SKILL.md 本体，含顺序，执行真相）/ 可导航的链（**`composed_of` 边：唯一 schema 增量**，复合 skill 影子 → 成员 skill 影子，无序 BOM）。顺序不进图（留 artifact + 影子 body 派生摘要，`content_hash` 绑定）；成员间语义依赖用现有 `enables`；变更传播沿 composed_of 逆向遍历；声明链 vs 观察链（DAG traversal）构成审计配对；`composed_of` 仅由 skill proposer 在注册/派生时写，consolidator 永不写；Edge 加 ordinal 为过早优化，将来确需再议。
   - **skill 验证门三级**：per-agent 直接上（爆炸半径小）；跨 agent 共享走 shadow-then-promote（复用 universal spec §19 纪律）；owner 显式确认对齐 §11 promotion 语义；
   - **提案审计账本**（skill-impact.md 模式）：diff + 验证结果 + 接受/拒绝追加式账本，proposer 下一轮必读，禁止重复被拒方案。
2. **legacy→graph 知识迁移工具**：一次性、owner 授权、限速、标记 `legacy_backfill`（对齐 universal spec §8.2 backfill 语义）。
3. **channel 消息 agent 可见性控制**（Q9-iv）：eval 轮对 agent 隐藏 learn 消息——产品功能提案，使绝对分可对齐官方新会话语义。
4. **PR #3919 凭据报告**、**graph-x 跨频道臂**、**legacy per-family 重跑**（handoff §6 延后项）。

## 7. 修复 → 指标映射（预期收益）

| 修复 | 受益域/族 | 指标 |
|---|---|---|
| universal pipeline + A1-A3 | info gathering（PG×6）、SM03/SM06、PC 全族 | info on 0.267→≥0.5；write_precision、recall_accuracy |
| A1 recall 404 | 生产频道记忆（bench 外） | 404 ~32k/天→~0 |
| B1/B2/B3 + A2-2/B8 | 归因升级 | retrieval_before_success↑；strong 破零 |
| B5/B6/B7 | SM03/SM04/SM06、EP02 | update_correctness；1−pollution |
| （P3，另立 spec） | procedural（PC×7） | Δ −0.461→转正 |

## 8. 硬约束（沿用，仍然有效）

- 生产零打扰：driver 默认 `--environment test`，拒绝配置不匹配；不写生产 DB。
- 不修改 memory_bench 评测逻辑；driver 只组合。
- 隔离频道/agent 用完归档；state 目录 0700/文件 0600；不输出凭据。
- 结果声明上限 `system-comparison-only`。
- 测试服务器容器周期性重建窗口（批量后 15-27 分钟观测）：重跑前 `curl` 确认恢复，`--retries` 缓解。
- 本规格不授权 commit、部署、production migration 或 daemon restart；部署窗口由团队排期。

## 9. Out of Scope

- P3 procedural/skill 域的全部实现决策（§E1）。
- 图作用域从 channel 提升到 workspace（被 universal spec §12 迁移语义反证）。
- 运行时 legacy fallback（Q5b 已否决）。
- 训练/reward 治理（universal spec Phase 6 自带）。