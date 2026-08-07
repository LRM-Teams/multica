# Goal Gate：人机协作主路径与 Work Graph 简化规格

> 状态：Proposed，等待产品与研发评审
>
> 日期：2026-08-07
>
> 本规格收敛 `Chat / Issue / Goal / Work Graph / Research Run / Memory / Skill`
> 的产品边界。它替代“普通 Issue 也可直接创建 Work Graph”的方向；在迁移完成前，
> 现有实现仅作为兼容事实，不代表目标产品合同。

## 1. 一句话决定

Multica 默认是人和 Agent 围绕 Chat 与 Issue 协作的平台；只有用户显式开启 Goal，
或明确批准把现有工作升级为 Goal 后，服务端才允许创建和运行 Work Graph。

Work Graph 是 Goal 的内部执行计划，不是独立产品中心，也不是普通 Issue 的默认能力。
Auto Research 和 Self Evolution 是 Goal 的 execution profile，复用同一套有界长程控制面，
不各自建立第二套 scheduler、任务真相源或递归运行时。

对于探索型 Goal，“有界”不等于只能运行固定轮数。Goal 可以表达一个逻辑上持续、没有预设总轮数的
Continuous Loop；系统通过一系列物理上有界的 Epoch 推进它。每个 Epoch 都必须有预算、lease、
评价和停止检查，只有评价结果证明下一轮仍有价值时才续租。由此支持长期乃至持续运行，
同时避免把“无限 Loop”实现成无预算、不可停止的递归任务。

## 2. 为什么要改

当前实现同时允许 `channel_goal`、根 `issue` 和 `research_run` 作为 Graph anchor，
并把 Graph 创建、Node 状态、Artifact、Verification 和 Revision 暴露给 Agent。
这使简单的人机协作过早承担长程自治的复杂度，同时仍未自然解决以下核心体验：

- 子任务结果怎样可靠回到原聊天或母 Issue；
- 下游 Agent 怎样获得上游的结论、证据和产物；
- 谁负责最终汇总，何时重新唤醒；
- Issue `done`、Agent task success 和 Evidence Gate 谁拥有完成权；
- 普通协作何时值得付出预算、重规划和验证成本。

本规格通过一个显式的 **Goal Gate** 切开两条路径：日常协作保持简单，长程目标才进入受控循环。

## 3. 用户模型

产品主模型只保留四个用户概念：

| 概念 | 用户问题 | 产品职责 |
| --- | --- | --- |
| Chat | “我们在讨论什么？” | 发起、澄清、决策和返回结果 |
| Issue | “谁负责这次可交付工作？” | 指派、讨论、状态、评论、PR 和验收 |
| Goal | “什么需要持续推进，何时应该停止？” | 目标合同、预算、权限、Gate、进度和停止条件 |
| Memory / Skill | “哪些知识或做法值得以后复用？” | 经来源追踪和评测后沉淀稳定能力 |

Graph、Node、Edge、Turn、Artifact Revision、Evidence Receipt 和 Trial 是 Goal 的内部或高级审计概念。
普通用户无需理解这些对象才能分配工作。

## 4. 两条执行路径

### 4.1 默认轻量路径

```text
Chat 或 Issue
  → 分配给一个人或 Agent
  → 必要时创建普通子 Issue
  → 执行并在 Issue 写回结果
  → 评论 / PR / 验收 / 状态
  → 结果返回来源 Chat 或母 Issue
```

默认路径遵守以下规则：

1. 不创建 Goal、Graph、Node、Trial 或 Evidence Receipt。
2. 子 Issue 只表达组织和独立交付，不自动升级成长程控制面。
3. 普通 Issue 可以使用既有 `blocked_by / depends_on` 关系，但只承担轻量解锁，
   不获得 Graph revision、失效传播或递归重规划语义。
4. Agent 可从单聊、群聊或 Issue 创建、分配和推进 Issue；来源 conversation 必须保留为 return target。
5. 一次执行的证据默认使用结构化 Issue writeback：结论、做了什么、来源或测试、风险、产物链接。
6. 简单任务不得仅因“使用多个 Agent”而自动创建 Goal。

### 4.2 Goal 长程路径

```text
Goal Contract
  → Work Graph revision
    → Issue-backed collaborative nodes
    → bounded Agent Jobs / Turns
    → Artifact / Evidence / Gate
  → Decision: continue / wait / ask human / replan / stop
  → checkpoint 与最终结果返回来源 Chat
```

Goal 路径承担：

- 多轮或跨天持续推进；
- 并行工作与真实依赖；
- runtime/session 更换后的恢复；
- 预算、并发、重试和无进展限制；
- Evidence、人工 Gate 和失效；
- 有界重规划及明确停止。

## 5. Goal Gate

### 5.1 开启方式

进入 Goal 路径只能通过以下两种方式：

1. 用户显式创建 Goal，或明确说“作为长期目标推进”“开启 Auto Research”等；
2. Agent 提议升级，用户明确批准。

Graph 创建 API 必须要求有效且 active 的 `goal_id`。批准事实必须由服务端可审计地绑定 Goal，
不能只依赖 Prompt 声称“用户同意了”。

### 5.2 Agent 何时可以建议升级

满足下列任一条件时，Agent可以建议升级为 Goal：

- 预计跨多个有界执行 Turn 或多个自然日；
- 需要持续监控外部状态；
- 存在多个可独立验收的工作单元及最终综合；
- 需要反复提出假设、实验、验证和重规划；
- 中途可能等待人、换 Agent、换 runtime 或跨设备恢复；
- 需要明确预算、停止条件、独立验证或高风险人工 Gate；
- 上游产物变化需要使旧结论失效并重算。

以下情况不得建议升级：问候、一次工具调用、小型低风险修改、单次问答，
或虽然耗时但共享上下文强、可由一个持久 Agent Job 连续完成的任务。

### 5.3 升级交互

Agent 的升级提案必须先展示：

- Goal objective 与成功条件；
- 预计工作单元和参与角色；
- 预算、时间或并发上限；
- 需要的新权限或人工 Gate；
- 停止条件；
- 原 Chat / Issue 的结果返回位置。

用户批准后，服务端原子创建 Goal 与首个 Graph revision；拒绝后继续普通 Issue 路径，
不得换一个幂等键偷偷创建。

## 6. Goal 与 Work Graph 权责

### 6.1 唯一归属

目标态强制以下结构：

- 一张 Work Graph 只能属于一个 Goal；
- 一个 Goal 同一时刻只有一个 active Graph revision；
- Research Run 必须属于一个 Goal；
- 新 Graph 不再支持根 Issue 或独立 Research Run 作为 canonical anchor；
- 普通 Issue 若需 Graph，先升级为 Goal，原 Issue 作为 `source_issue_id` 或首个工作项；
- Graph 不得脱离 Goal 单独继续运行。

### 6.2 唯一完成权

Goal 拥有“目标是否完成”的唯一裁决权；Graph 拥有“当前执行计划是否可交付”的唯一裁决权；
Issue 只拥有协作状态和 completion request。

对于 Goal-managed Issue：

- `in_review`、`done`、Agent task success 或 provider exit 0 不得直接释放 Graph 依赖；
- 它们只能记录执行结束或 completion request；
- 下游只读取当前 revision 的 `effective_completion=satisfied`；
- `cancelled`、`failed`、`stale`、`revoked` 不得被当成成功交付；
- 所有入口必须汇入同一个服务端 completion boundary，不能只封锁某个 HTTP handler。

普通非 Goal Issue 保持现有轻量状态行为。

### 6.3 Graph 的产品位置

Graph 默认以 Goal 进度摘要展示：

- 当前阶段；
- 正在运行、等待和阻塞的 Issue；
- 已用预算；
- 下一步及原因；
- 需要谁处理的 Gate；
- 当前为何尚未完成。

拓扑、revision、Artifact digest 和 Evidence Receipt 放在高级详情和审计视图。
用户不直接拖拽 Node 来推进 canonical 状态。

## 7. Issue 是人机协作投影

需要被人或 Agent 讨论、指派、接管、阻塞或审核的 Graph Node 必须绑定 Issue。
内部重试、机械验证和 scheduler maintenance 可以只作为 Job/Attempt 存在，并投影到所属 Issue。

每个 Goal-managed Issue 必须携带以下服务端上下文：

```text
goal_id
graph_id / graph_revision
node_id / node_role
objective / completion_contract
source_conversation / return_target
upstream_result_refs[]
artifact_refs[] / evidence_refs[]
budget_snapshot
```

下游 Job 启动时读取有界 handoff envelope，而不是读取兄弟 Agent 的 provider session、私有 Memory 或私聊历史。

每个 Node 的终态 writeback 至少包含：

```text
summary
reported_outcome
artifact_refs[]
evidence_refs[]
risks / blockers
requested_action
```

这些字段应投影为人能读懂的 Issue 评论，同时保留结构化记录供服务端裁决。

## 8. 人与 Agent 的协调原则

Issue、Goal 和来源群是厚共享场；Agent DM 或临时私群只承载确实不能公开的输入，
不得成为第二套任务分配、进度或 handoff 控制面。

普通协作：

- 人或 Agent 可在单聊、群聊和 Issue 中创建或分配 Issue；
- 普通并行默认复用同一 Agent 的独立 Issue Session；仅在候选实现、盲审、对抗验证、独立复现、
  不兼容模型/工具或实验污染风险下，显式选择 `worker_mode=derived_agent`；
- derived Agent 必须记录 source lineage 和 `clone_reason`，复制 approved config/skills 与只读源 Memory 快照，
  不继承凭据、会话或 Memory 写历史，不自动把经验写回源 Agent；
- derived Agent 只负责一个子 Issue，结果经 Issue/Artifact 回流；`in_review` 期间保留以处理返工，
  Issue 到达 `done` 或 `cancelled` 后自动归档；
- 仅仅“任务很多”或“想更快”不得隐式触发克隆；成本、并发和生命周期必须可见；
- 谁被指派谁负责，调度者不自动成为永久负责人；
- 完成结果必须回写 Issue，并回到来源 conversation；
- 多个 Issue 的结果由明确的汇总 Issue 或发起人整合。

Goal 协作：

- 服务端按 Graph frontier 解锁工作，不靠 Agent 在群里轮询；
- Node terminal、failed、stale、Gate 和 graph deliverable 生成持久 Graph Delta；
- Graph Delta 唤醒当前 coordinator Job 或等待中的下游 Job；
- coordinator 只负责重规划、综合和返回，不重复实现已委派的同一 deliverable。

## 9. Execution Profile

Goal 使用统一内核，并通过 profile 限定方法和额外对象：

### 9.1 `standard`

用于开发、运营、内容、项目推进等长程工作。默认使用 Issue、依赖、checkpoint、预算和人工 Gate；
不强制创建 Trial、Claim 或研究引用模型。

### 9.2 `auto_research`

在 standard 基础上增加 question、claim、source、research task、synthesis、citation audit 和停止判断。
Research Run 是 Goal 的 domain adapter，不拥有第二套 Goal completion 或调度真相。

典型流程：

```text
提出实验 → 实现并运行 → 评价结果 → 学习并提出下一轮实验
   ↑                                             │
   └─────────────────────────────────────────────┘
```

并行调查默认创建多个独立 Issue；综合 Issue 依赖所有 required 调查结果。

这里的“实验”是通用接口，不局限于训练模型：它可以是资料检索、代码修改、benchmark、模拟、
数据分析、原型、用户实验、芯片设计工具或其他能够产生可评价反馈的操作。一个领域只要能够表达
`proposal → execution → measurable evaluation`，就可以通过 domain adapter 接入。

Auto Research 必须把规模化对象从“聊天轮数”切换为“有效实验”：

- 一次提出多个相互区分的候选，而不是让多个 Agent 重复回答同一个问题；
- 在资源允许时并行执行独立实验，共享基础数据和缓存但隔离可变状态；
- 评价同时考虑质量、成本、时间、资源和 guardrail，而不是只优化单一分数；
- 成功、失败、无效和 inconclusive 都形成结构化结果；
- 下一轮 planner 只能读取已经 committed 的评价结果；
- 每轮选择应提高预期信息增益，而不是机械扩大实验数量。

失败实验是一等知识：`HYPOTHESIS_REJECTED` 可以排除候选空间；`TRIAL_INVALID` 只能说明实验合同
不足；`OPERATIONAL_FAILURE` 只能触发修复或重试。三者不得折叠成一个 `failed`，否则 Loop 会反复
重跑已经被证伪的方向，或错误地从基础设施故障中学习研究结论。

### 9.3 `self_evolution`

在 standard 基础上产生 Memory/Skill/Workflow 候选，并执行独立评测、风险检查和晋升 Gate。
一次任务成功不得直接修改已启用 Skill 或将经验扩散到所有 Agent。

典型流程：

```text
观察问题
  → evolution candidate
  → 有界实验 / 回归评测
  → verifier
  → human 或 policy Gate
  → promote / reject / rollback
```

## 10. Continuous Loop 与递归自进化边界

### 10.1 逻辑无限，执行有界

Auto Research 和 Self Evolution Goal 可以没有预设的总迭代次数，并在外部环境持续变化时长期运行。
但平台不执行一个永不返回的 run，而是把 Loop 切成连续 Epoch：

```text
Goal（durable, potentially continuous）
  → Epoch N contract
  → Graph revision / parallel Trials
  → committed Results + aggregate Evaluation
  → Continue Decision + Epoch N+1 lease
```

每个 Epoch 必须：

- 从上一个 committed checkpoint 恢复，而不是依赖 provider session；
- 声明该轮问题、候选空间、实验组合、资源预算和 deadline；
- 只提交不可变 Trial Result 和 Evidence；
- 计算该轮信息增益、成本、风险和剩余不确定性；
- 产生 `CONTINUE | WAIT | ASK_HUMAN | STOP_*` 决策；
- 在获得下一轮 lease 前释放本轮 runtime 和临时资源。

“无限”因此是 Goal 生命周期语义，不是单次 Agent run、递归调用深度或无限 token budget。

### 10.2 自进化循环

递归发生在 Goal 的“提出候选—评测—选择—再评测”循环，不是无限创建 Agent、Issue 或 Goal。

必须满足：

1. 每轮有 immutable contract、预算和停止条件；
2. 新候选不能扩大当前 authority；
3. 连续无信息增益、预算耗尽或方法无法区分时停止；
4. 修改成功标准、扩大权限、生产部署或开启新 Goal 必须请求人工批准；
5. Memory/Skill 晋升与绑定分开，保留版本、评测结果和回滚路径；
6. 自进化结果只影响批准后的后续 Turn，不回写篡改历史 Evidence。

### 10.3 探索与利用

连续 Loop 不能只复制当前最好结果。每个 Epoch 的 planner 应显式分配：

- exploitation：复现、优化或扩展当前高价值方向；
- exploration：测试尚未覆盖、具有高不确定性或高潜在收益的方向；
- verification：独立复核关键结果，防止错误评价污染后续数百轮；
- synthesis：压缩已知结果，更新候选空间和下一轮决策输入。

比例可以由 profile policy 调整，但任何单个 Agent 的自信陈述都不能直接淘汰探索空间或晋升能力。

### 10.4 大规模实验的系统要求

当 Loop 从数个实验扩大到数千实验时，主要瓶颈将从模型推理转为调度、算力、存储、数据版本和评价吞吐。
Multica 应复用 Goal/Graph 控制面并补齐以下能力，而不是增加聊天 Agent 数量来模拟规模：

- immutable Experiment Spec 与内容寻址的输入、代码、环境和产物；
- 基于 Trial identity 的幂等提交、重试、去重和结果缓存；
- 每资源池、Goal、Agent、实验族的并发与配额；
- 早停、失败隔离、straggler 处理和可回收 lease；
- 批量创建、claim 和结果提交，避免每个实验都依赖一次聊天往返；
- 分层保留策略：原始日志、Artifact、Evaluation、聚合知识分别设生命周期；
- evaluator version 和 policy digest 固定，防止跨轮成功标准暗中漂移。

Issue 不应与每个微型实验一一对应。只有需要人机讨论、指派、接管或审核的研究工作单元才投影为 Issue；
大规模机械 Trial 留在 Research adapter 内，并聚合到所属 Issue 和 Goal。

## 11. Memory 与 Skill 边界

Memory 和 Skill 不参与 Graph frontier、lease、completion、budget 或权限裁决。

- Memory 保存带来源和作用域的稳定知识、偏好、决策和可复用经验；
- Skill 保存经过评测的可复用工作方法；
- 进行中的 blocker、下一节点和本轮 checkpoint 属于 Goal/Job 服务端状态；
- provider session 和本地 `STATE.md` 不得成为长程任务唯一恢复点；
- 只有 Epoch commit、Goal milestone 或 Goal completion 后，才从该边界内的有效 Evidence 提取
  evolution candidate；持续 Goal 不必为了沉淀知识而伪装完成，提取也不等于晋升。

## 12. 权限模型

Graph API 不得以“workspace 内任意 Agent”作为充分权限。

服务端至少区分：

- Goal owner / authorized coordinator：创建 revision、重规划和请求 Gate；
- Node assignee：读取自己的 handoff、提交自己的执行结果和 Artifact；
- Verifier assignee：只对分配给自己的 verification contract 提交 verdict；
- Human reviewer：按 Goal policy 审批 Gate、接管或取消；
- Observer：只读授权范围内的 Goal 投影。

创建 Node 时必须复用 Issue assignment 的 Agent visibility、private Agent、角色和 workspace 权限检查。
调用方不得通过提交其他 `node_id` 冒充其 assignee 或 verifier。

## 13. API 目标形状

### 13.1 普通协作

保留现有 Issue create/update/assign/comment/dependency API。可增加一个轻量原子分解命令：

```text
multica issue decompose <issue-id> --plan-file <path> --idempotency-key <uuid>
```

它只创建子 Issue、assignee、普通依赖和明确的汇总 Issue；不创建 Work Graph。

### 13.2 Goal 协作

Graph mutation 必须从 Goal 作用域进入：

```text
POST /api/agent/goals/{goalId}/work-graphs
GET  /api/agent/goals/{goalId}/work-graph
POST /api/agent/goals/{goalId}/work-graph/revisions
POST /api/agent/goals/{goalId}/nodes/{nodeId}/turn-results
POST /api/agent/goals/{goalId}/nodes/{nodeId}/artifacts
POST /api/agent/goals/{goalId}/nodes/{nodeId}/verifications
```

服务端从认证 principal、Goal participant 和 Node assignment 推导 actor，客户端不能任意指定 actor identity。

现有 `/api/agent/work-graphs/*` 在迁移期只保留兼容读取或受控内部调用；新的独立 Graph 创建必须关闭。

## 14. 迁移计划

### Phase 0：冻结扩张

- 不再为普通 Issue、Research Run 增加新的 Graph 能力；
- 将现有 Graph 标记为 advanced/experimental；
- 修复 Graph mutation ACL 和 completion bypass 两个安全边界；
- 协调 Skill 改为 Issue/Goal 共享场优先，禁止用私线替代控制面。

### Phase 1：Goal Gate 与来源回流

- Graph create 强制 `goal_id` 和 active Goal；
- Goal 保存 `source_channel_id / source_message_id / source_issue_id`；
- Graph Delta 持久化并唤醒 coordinator；
- 最终结果可靠返回来源 Chat 或 Issue。

### Phase 2：Handoff 与单一完成边界

- 为 Goal-managed task 注入 bounded handoff envelope；
- Agent task success 和 Issue done 改为 completion request；
- Artifact、Evidence 和 Gate 计算 effective completion；
- 下游只由 effective completion 解锁。

### Phase 3：普通 Issue 分解

- 提供轻量 `issue decompose` 原子操作；
- 单聊、群聊和 Issue UI 支持预览、确认并分配子 Issue；
- 普通依赖解锁不创建 Graph；
- 来源 conversation 显示进度与最终汇总。

### Phase 4：Profile 收口

- Research Run 迁为 `auto_research` Goal adapter；
- evolution candidate/评测接入 `self_evolution` profile；
- 删除新的 root Issue / standalone Research Run Graph anchor；
- 对历史 Graph 保留只读投影或一次性归属迁移，不永久双写。

## 15. 验收场景

### 15.1 单聊开发任务

人对 Agent 说“修复登录错误”。系统创建一个普通 Issue 并指派；不创建 Goal 或 Graph。
Agent 提交 PR、测试和 Issue writeback，结果返回原单聊。

### 15.2 群聊并行开发

人要求前端、后端和测试分别处理一个已明确范围的改动。系统创建三个普通子 Issue和一个汇总 Issue，
使用普通依赖解锁汇总；不因参与 Agent 数量自动创建 Goal。

### 15.3 多日产品调研

Agent 判断任务需要持续监控、多个来源和独立复核，先提出 Goal objective、预算、停止条件和角色。
用户批准后创建 `auto_research` Goal 与 Graph；并行调查写回各自 Issue，综合节点获得结构化 Evidence，
最终报告返回原群。

### 15.4 自进化

Goal 执行发现某个工作流反复失败，产生 Skill 候选和评测节点。候选通过独立 verifier 及人工 Gate 后晋升，
再在后续 Turn 使用；单次成功不能自动覆盖已启用 Skill。

### 15.5 持续实验 Loop

用户开启一个持续优化 Goal，并允许它在固定日预算内长期运行。系统每天执行若干有界 Epoch，
并行提出、运行和评价实验；负结果用于排除方向，基础设施失败不进入研究结论。每日预算耗尽时 checkpoint
并释放资源，下一预算窗口从 committed state 恢复。只有达到人工 Gate、需要扩大权限、评价合同失效或
长期无信息增益时暂停或请求用户，而不是因为没有预设总轮数就失控运行。

### 15.6 不得发生

- 普通 Issue assignment 自动创建 Graph；
- Agent 未经用户批准把工作升级为高成本 Goal；
- workspace 内任意 Agent 修改不属于自己的 Graph；
- Issue `done` 或 task success 绕过 Evidence Gate 解锁下游；
- Agent 通过 DM 私下传递 canonical handoff，公开 Issue 没有可核对结果；
- cancelled Node 被计入成功交付；
- Auto Research 或 Self Evolution 脱离 Goal 无限递归。
- 用无限 Agent run、无限递归深度或无限 token 模拟 Continuous Loop；
- 大量实验结果未经可靠评价就进入下一轮 proposal；
- 把基础设施失败误当成假设被证伪，或把一次偶然成功自动晋升为稳定能力。

## 16. 成功指标

- 普通 Chat/Issue 请求创建 Goal 或 Graph 的比例接近零；
- 普通任务的首个可见动作和完成路径不因 Graph 增加延迟；
- Goal-managed Node 的下游启动全部可追溯到 effective completion；
- Goal 最终结果能够稳定返回原 conversation；
- Graph mutation 越权测试全入口 fail closed；
- Auto Research 在预算、无进展和人工 Gate 条件下可确定停止；
- Continuous Loop 能跨 Epoch、runtime 和重启恢复，且每一轮均可独立停止和审计；
- 单位时间 committed 有效实验数可以扩展，同时无效重复率和不可复现实验率受控；
- Memory/Skill 晋升均有 Evidence、版本和回滚记录。

## 17. 非目标

- 不把所有项目或多 Agent 工作自动变成 Goal；
- 不要求每个内部 attempt 创建 Issue；
- 不让用户手工维护复杂 DAG 才能协作；
- 不用 Memory、聊天记录或 provider session 代替服务端控制状态；
- 不在本次规格中设计无限自治、自动扩大权限、自动生产部署或无界 Agent 繁殖；
- 不同时维护新的通用 Graph scheduler、Research scheduler 和 Evolution scheduler 三套真相源。

## 18. 外部思想来源与 Multica 取舍

Discovery Loop 在 2026-08-05 公布的方向将科学与工程抽象为持续实验循环：提出实验、实现并运行、
评价结果，再用评价推动下一轮；其公开目标包括并行执行大量实验、缩短迭代时间，并从每次评价中学习。

Multica 吸收的是以下系统思想：

- Scale experiments，而不只是 Scale 模型或 Agent 数量；
- proposal、execution 和 evaluation 形成闭环；
- 结果必须影响下一轮候选选择；
- ML 是首个适合落地的 domain adapter，但循环接口可以扩展到其他科学与工程领域；
- 大规模 Loop 最终是计算、调度、存储和评价基础设施问题。

Multica 不照搬的是“无人介入”或“无限运行”的字面表达。Multica 是人和 Agent 协调工作的平台：
人拥有 Goal、预算和权限边界；Agent 与自动化系统负责在边界内扩大有效实验吞吐；关键 Gate、例外和
高风险动作返回给人。对外产品语言应使用“持续探索 / Continuous Loop”，并明确“逻辑持续、逐轮续租”。

参考：

- `https://www.discoveryloop.com/`
- `https://x.com/JeffDean/status/2085034604172603724`
- `https://x.com/JeffDean/status/2085036253263921218`
