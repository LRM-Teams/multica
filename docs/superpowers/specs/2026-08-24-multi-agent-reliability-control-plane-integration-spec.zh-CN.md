# 多 Agent 长程可靠性控制面整合规格

> 状态：Accepted；Goal Execution Graph / Research Genealogy 分层及第 17 节实现决定已确认，开始分 PR 实施。
>
> 日期：2026-08-24
>
> 代码审查基线：`lrm/dev@a0eb37702df9a29beb033893b3d30016b207c3ce`
>
> 覆盖文档：
>
> - `2026-08-24-goal-issue-run-controller-spec.zh-CN.md`
> - `2026-08-24-provider-credential-scheduler-participation-gate-spec.zh-CN.md`
>
> 目的：把 Goal / Issue / Run controller、Participation Gate 和 Provider 凭证域调度器收口成
> 一条可恢复、可限流、可审计的执行链，提高多 Agent 稳定性和长程复杂任务完成率，同时避免重新建立
> 第二套消息、任务或执行图控制面。

## 1. 一句话结论

Multica 的标准长程协作使用：

```text
Message / state event
  -> 服务端确定性路由与 Participation Gate
  -> Goal controller / IssueExecutionReconciler
  -> durable Run intent
  -> Server lane admission
  -> Computer credential-domain permit
  -> provider turn
  -> completion report
  -> review
  -> Goal checkpoint / next frontier
```

其中：

- Goal 保存长期 intent、成功条件、权限、预算和 checkpoint；
- Issue parent/dependency 是标准 Goal 唯一可写任务拓扑；
- Run 使用现有 inbox / execution ledger，不再新增产品级 Task 或 Run 表；
- Participation Gate 只决定是否以及以什么角色参与，不决定工作是否完成；
- Provider scheduler 只决定 provider turn 何时启动，不决定 Issue、Goal 或 Message 状态；
- 每个 Goal 都有统一的 Goal Execution Graph 视图，但它不建立第二套调度真相；可调度工作仍只有 Issue，
  执行尝试仍只有 Run；
- Research / Self Evolution 在 Research Issue 下保留专用 Research Genealogy，用来表达 candidate、hypothesis、
  evidence、score、lineage、reference、crossover 和 pruning，不直接认领 Agent 或完成 Goal；
- 现有 Work Graph 暂作为 Research Genealogy 的 legacy storage / controller 实现，通过 adapter 接入 Issue / Run；
  本规格不迁移其数据，也不允许它与 Issue 路径对同一工作双重派发。

## 2. 对最新 `dev` 的直接结论

### 2.1 已有能力可以复用

最新 `dev` 已有以下可靠地基：

- `agent_inbox_event` 是 durable wake / run intent；
- `agent_event_delivery` 持有 claim / delivery lease；
- `agent_execution` 在真实 provider start 前创建，是不可变归属和 usage ledger；
- `CreateAgentInboxExecution` 以 `source_event_id` 把真实 execution 绑定到 inbox intent；同一 intent 在 reclaim / restart
  后可以产生多个 execution attempt，二者不是同一 ID；
- 同 Agent 的不同 Issue lane 已能按 `max_concurrent_tasks` 并发，同一 Issue lane 串行；
- Agent conversation lane 与 Issue lane 相互独立；
- daemon 以 `taskWakeSerializationKey(agent, issue)` 和 per-Issue execution root 隔离并发 session；
- Computer 已有 machine-wide process capacity 和带 `daemonInstanceId + PID` 的 child fence；
- Attention Probe 的 restricted profile、严格 parser、Attention Round 纯函数和持久表已经存在；
- transient capacity 429 与 sticky quota / billing lock 已有不同失败分类；
- `DecomposeIssue` 已经原子创建普通 Issue DAG，而且明确不创建 Work Graph。

这些能力应扩展，不应复制。

### 2.2 两份原 spec 需要修正的代码事实

1. **产品 Run identity 由 `agent_inbox_event` 承担，但不能与 provider execution 混为一谈。**
   最新代码已经明确区分 durable intent、delivery lease 和真实 execution。`agent_inbox_event.id` 是逻辑
   `run_id`；每次真实 provider start 使用独立 `execution_id`，并通过 `source_event_id=run_id` 形成一对多关系。
   delivery reclaim 不创建第二个逻辑 Run，但 provider restart 可以留下新的 execution attempt。

2. **标准 Goal 不能直接复用现有 `goal_execution_epoch`。**
   当前表要求 `graph_id NOT NULL`，decision enum 也属于 Work Graph continuous loop。强行复用会迫使标准 Goal
   为 controller tick 创建空 Graph，正好恢复被禁止的第二套 DAG。

3. **当前 comment 没有 typed parts。**
   Completion report 若只写普通 comment JSON/prose，服务端无法建立 criterion、Run、reviewer 和 evidence 的约束。
   首版应使用内部 sidecar record，并同时生成普通可见 comment。

4. **Completion command 不能在 Agent turn 内提前终结 `agent_execution`。**
   Agent 调用 completion API 后 provider turn 仍可能继续或失败。API 只提交 report 并把 Issue 推进到
   `in_review`；execution 终态仍由现有 daemon completion/failure 回调写入。

5. **Provider permit 的可执行 seam 是 provider turn，不是每个上游 HTTP 请求。**
   `Backend.Execute`、resident message turn / Pi run admission 是 Multica 能可靠控制的边界。一个 CLI turn 内部
   可能发出多次模型 API 请求，首版不能声称逐 HTTP request 精确调度。

6. **运行时在线不能只看 `agent_runtime.status`。**
   最新工程合同以 current Workspace Runner socket / Computer liveness 为权威；runtime 行用于 provider binding
   和 usability，不能重新成为在线真相。

7. **Attention Round 还不是完整生产编排。**
   现有 schema、restricted probe 和 resolver 可复用，但目前没有一条把候选、probe、round、grant 和正式执行
   全部串起来的生产 owner。上线计划不能把“已有基础组件”描述成“已有完整 Gate”。

8. **现有 `work_owner_lease` 仍在 Issue enqueue 热路径。**
   标准 Issue path 只有在 active Run 唯一 claim 和恢复装置上线后才能移除它。Participation Gate 不得继续把它
   写成标准路径的长期前提。

9. **“Git 仓库已绑定”不等于 canonical PR 链路可用。**
   真实案例中，Goal 页面因存在 `project_resource(resource_type='github_repo')` 显示“Git 仓库：已绑定”，但 Workspace
   的 GitHub App installation 实际未配置；PR #65 的标题、正文和分支都包含 `LRM-1621`，`edited` webhook 重扫后
   `issue_pull_request` 仍为空。当前 webhook handler 在找不到 installation→Workspace 映射时静默返回，既不补偿扫描，
   也不产生可见等待原因。这不是引用正则失败，而是连接健康状态混用和静默丢事件。执行图必须把“资源地址存在”、
   “事件源已连接”和“canonical link 已收敛”分成三个事实，不能以 metadata 中的 `pr_url/pr_number` 代替 canonical link。

## 3. Canonical 对象与非 canonical 对象

### 3.1 Canonical 产品状态

| 对象 | 唯一职责 |
| --- | --- |
| Message | 人与 Agent 的可见通信事实 |
| Goal | 长期 objective、success criteria、权限、预算、Gate、checkpoint 和生命周期 |
| Issue | 可分配交付物、parent、dependency、acceptance criteria、负责人和 review 状态 |
| Run | 某个 Agent 对某个 Issue 的一次真实执行尝试 |
| Completion report / review verdict | Run 产出怎样满足 criteria，以及谁接受或拒绝 |
| Research Genealogy | Research Issue 内 candidate / evidence 的演化、引用、评分、失败和剪枝事实；不拥有通用任务状态 |

### 3.2 非 canonical 控制状态

以下对象有持久化或审计价值，但不能变成第二套工作真相：

- Participation decision；
- controller event / tick；
- dispatch outbox；
- `agent_event_delivery` lease；
- Provider permit / queue / pacer；
- UI admission projection；
- Reminder / safety scan；
- Goal Execution Graph read model；
- Research Genealogy 到 Issue / Run 的调度 adapter。

## 4. Goal Execution Graph 与 Research Genealogy 分层

### 4.1 所有 Goal 共享一个执行控制面

每个 Goal 都对外提供 Goal Execution Graph。它是以下 canonical 状态的版本化投影：

```text
Goal
  -> Parent Issue / Issue / dependency
  -> Run intent / execution attempt
  -> completion report / review / decision / Gate
  -> artifact / evidence / metric outcome
  -> superseded / failed / pruned / waiting reason
```

该图用于回答“计划如何演化、当前谁在做什么、为什么等待、哪些路径已失败、下一步是什么”。其中：

- Issue parent/dependency 是唯一可写任务拓扑；
- Issue 是唯一可被分配的工作单元；
- Run 是唯一 provider 执行尝试；
- decision、review、artifact、evidence 和 failure 可以成为图中的可见节点或注解，但不能独立派工；
- read model 可以重建和重放，不拥有与 canonical 对象分离的 `done`、assignee、lease 或 retry 状态；
- 不为普通 Goal 创建 Work Graph node / edge 来镜像 Issue DAG。

### 4.2 Research Genealogy 是嵌套领域图，不是第二套 Goal 执行图

Research Issue 可以拥有 Research Genealogy：

| 能力 | Goal Execution Graph | Research Genealogy |
| --- | --- | --- |
| 主要问题 | Goal 如何完成、当前做什么 | 候选方案如何产生、继承、组合和淘汰 |
| 节点 | Issue、Run、decision、review、artifact 投影 | candidate、hypothesis、experiment、evidence、score |
| 边 | parent、dependency、causality、supersede | parent-child、mechanism/reference transfer、crossover |
| 调度权 | Issue assignee + active Run claim | 无；需要 Agent 工作时创建或绑定 Issue / Run |
| 完成条件 | required Issue accepted，Goal criteria 满足 | champion / conclusion 达到研究策略阈值并通过验证 |
| 失败含义 | Run / Issue 需要恢复、重试或重规划 | candidate 可永久 failed / pruned，但证据和谱系保留 |

Research Genealogy 的 candidate 不是 Issue。一个 candidate 可以没有独立派工，也可以由多个 Run 产生；crossover
可以有多个 parent，reference edge 也不代表执行依赖。反过来，一个 Research Issue 可以探索多个 candidate。

当 Research controller 需要新的 Agent 工作时，只能通过 adapter 执行以下一种操作：

1. 顺序探索时复用现有 Research Issue 并创建新 Run；或
2. 独立 candidate 需要并行探索时创建带 acceptance criteria 的 child Issue，再由标准 reconciler 创建 Run。

adapter 必须记录 `candidate_id -> issue_id / run_id` provenance，但 candidate 状态不能反向充当 Run lease；Research
结论只能提交为 completion report，使 Research Issue 进入 `in_review`，不能直接把 Issue 或 Goal 置为 `done`。

### 4.3 现有 Work Graph 的过渡边界

现有 Work Graph、artifact revision、verification、invalidation 和 epoch 在本规格中暂不迁移，作为
`research_legacy` 内部实现保留。它不是用户可选择的 Goal execution mode，也不能用于普通 Goal。

过渡期规则：

- 已有 Research session 继续读写原 Work Graph 数据；
- 新增 provider work 必须经唯一 adapter 绑定 Issue / Run，禁止 Work Graph wake 与 Issue wake 各派发一次；
- Work Graph 的 candidate / artifact / verification 状态是 Research provenance 事实，不是通用 Issue status；
- 不做 Issue DAG ↔ Work Graph 的节点级双写或自动迁移；
- 是否将 legacy schema 演进为独立 Research Genealogy model，由后续 Research 专项 spec 决定。

### 4.4 `advance graph` 的正式语义

`advance graph` 不再表示“执行一个独立 Work Graph frontier”。它是 Goal controller 的幂等推进操作，建议产品和
API 命名收敛为 `AdvanceGoal` 或 `ReconcileGoalExecutionGraph`：

1. 消费截至指定 Goal version 的 controller event；
2. 重算 runnable / waiting / review frontier；
3. 必要时创建、拆分、重新打开或 supersede Issue，并更新 dependency；
4. 需要判断时为群管创建有界 `COORDINATE` Run；
5. 写 checkpoint 和 Goal Execution Graph projection revision。

它不能直接启动 provider、不能绕过 Issue 创建执行、不能接受自己的 completion report，也不能用 projection state
覆盖 canonical Issue / Run 状态。

### 4.5 动态可视化与节点审计

Goal Execution Graph 必须是动态产品视图，而不是一次性生成的 Mermaid 或模型总结。服务端从 canonical mutation
outbox 幂等维护 per-Goal projection revision，前端通过现有 Workspace event transport 接收
`goal_execution_graph.changed(goal_id, projection_revision)`，再获取 snapshot 或 delta。事件丢失、revision 跳号、
客户端重连或 projection schema 升级时必须重取 snapshot，不能由客户端猜测缺失边或自行推进状态。

首版节点类型至少包括：

| 节点 | 默认展示内容 | 展开后的审计内容 |
| --- | --- | --- |
| Goal | objective、状态、总体 criteria、下一步 | checkpoint、Gate、预算、版本历史 |
| Parent Issue / Issue | 标题、负责人、状态、required children 进度 | description、acceptance criteria、dependency、等待原因 |
| Run | Agent、attempt、queued/running/terminal、耗时 | 输入 revision、执行摘要、provider outcome、usage、artifact / commit |
| Completion report | 满足 criteria 的摘要、提交人 | criterion-by-criterion result、evidence、source Run |
| Review | reviewer、pending/accepted/rejected | 每条 criterion verdict、拒绝原因、权限和时间 |
| Decision / Gate | 决定、状态、owner | 输入事实、policy/version、解除条件 |
| Artifact / Evidence | 类型、摘要、有效/失效 | URI/ref、producer Run、校验和、verification / invalidation reason |

边必须有类型，不能只画无语义连线：

```text
contains        Parent Issue -> child Issue
depends_on      Issue -> prerequisite Issue
attempted_by    Issue -> Run
produces_report Run -> Completion report
reviewed_by     Completion report -> Review
produced        Run -> Artifact / Evidence
supports        Evidence -> criterion / decision
rejected_to     rejected Review -> successor Run / reopened Issue
supersedes      new Issue / decision -> obsolete branch
caused_by       state transition -> source event
```

图默认折叠 Run、report、review 和 evidence，只展示 Goal → Parent Issue → child Issue 的工作结构；用户展开某个
Issue 后才显示完整执行链，避免长程 Goal 变成不可读的“意大利面图”。至少提供三种 view：

1. **Plan**：Issue hierarchy、dependency、owner 和 frontier；
2. **Live**：queued / running / waiting / in_review、Agent 和实时变化；
3. **History**：Run、report、review、返工、superseded / failed 分支和 evidence。

例如游戏开发 Goal 的玩法分支应能表达：

```text
制作游戏
  -> [contains] 游戏玩法（Parent Issue）
       -> [contains] 移动系统
       -> [contains] 战斗系统
            -> [attempted_by] Run #1
            -> [produces_report] Completion Report #1
            -> [reviewed_by] Review #1: rejected（碰撞判定不满足 criterion）
            -> [rejected_to] Run #2
            -> [produces_report] Completion Report #2
            -> [reviewed_by] Review #2: accepted
       -> [depends_on] 关卡平衡（依赖战斗系统 accepted）
```

Parent Issue 的进度由 required child Issue 的 accepted verdict 派生，不允许客户端或 Agent直接写百分比。点击节点的
任何修改操作都调用原 Goal / Issue / completion / review API；projection 只读。每个图节点必须保存 canonical
`source_kind + source_id + source_version`，保证 UI 中的每一条结论都能回到原始事实。

### 4.6 只读画布交互合同

Goal Execution Graph 是可以探索的无限画布，但拓扑严格只读：

- 按住画布拖动可向上、下、左、右平移，不把手势传给页面滚动容器；
- 鼠标滚轮围绕指针位置连续缩放，触控板支持平移和 pinch zoom；缩放范围、步进和惯性必须有界；
- 提供 `适应屏幕`、`100%`、`聚焦当前 Run` 和 `返回选中节点`；首次进入只自动 fit 一次，实时 delta 不得反复抢走视口；
- 用户不能在画布上创建、删除、重连或拖动 canonical 节点和边；节点位置只属于本地/瞬时布局，不回写服务端关系；
- 点击节点只打开审计详情，展示它做了什么、输入/输出、Agent、Run/execution attempt、review、evidence、PR 和等待原因；
- 大图使用稳定分层布局、Parent Issue 折叠、分支聚焦和状态过滤；新增 delta 尽量保持既有节点位置，避免整图跳动；
- 边必须按 `contains`、`depends_on`、`attempted_by`、`reviewed_by`、`produced`、`supersedes` 等类型提供图例，
  方向、虚实线和颜色不能只靠单一视觉编码；
- 键盘用户可以依次聚焦节点、打开详情、缩放和 fit；`prefers-reduced-motion` 下取消持续位移动效但功能不降级。

服务端 projection API 不提供通用 `PATCH node/edge`。产品中的拆分、依赖、reopen、review、Gate 和 supersede 操作
只能调用对应 canonical API，成功提交后再由 projection delta 反映到图上。这样“人不能改图”不等于“人不能管理任务”，
而是所有改变都经过有权限、有校验、有审计的领域命令。

### 4.7 真实“网页游戏开发”验收图

首个产品验收 fixture 不使用抽象 A/B/C 节点，直接复刻真实 Goal 的结构与异常：

```text
恢复原版视觉基线与世界连续性（Goal）
  -> 世界地形连续性（Parent Issue）
       -> 旧世界/新世界地形连续
       -> 森林生态与默认 HUD 恢复
  -> 游戏玩法与交互（Parent Issue）
       -> 移动/碰撞/交互
       -> 玩法验收
  -> 部署与真实视觉门禁（Parent Issue）
       -> LRM-1621 修复
            -> Run / execution attempt
            -> Completion report
            -> Review: accepted
            -> PR #65: merged
            -> canonical PR link: missing
                 -> waiting_on: github_installation_binding
       -> LRM-1624 man A-F 独立截图验收
```

该 fixture 必须同时验证正常链和异常链。PR #65 已 merged、Issue metadata 含 PR 信息，只能作为发现线索；只要
canonical `issue_pull_request` 仍为空，图就显示独立的红/橙色 `integration_gap` 节点或状态，不能把该 Issue/Goal
渲染成完整可审计。详情至少显示：repo resource 已绑定、GitHub App event source 未连接、最近 webhook 未建立 link、
建议动作（安装/绑定 GitHub App 后补偿扫描）。修复后由 canonical link delta 原地收敛，不创建第二个 PR 节点。

连接健康状态冻结为三个正交字段：

| 字段 | 真值来源 | 可以证明 | 不能证明 |
| --- | --- | --- | --- |
| `repository_bound` | project resource | 知道目标 repo 地址 | webhook 可达、installation 有效 |
| `event_source_connected` | current GitHub installation→Workspace binding | 平台能接收并归属事件 | 某个 PR 已完成映射 |
| `canonical_link_state` | `issue_pull_request` / link reconciler | Issue↔PR canonical 关系已收敛 | PR 内容正确、review 已通过 |

任一层缺失都要显示自己的原因和最后检查时间。webhook 遇到未知 installation 不得静默成功：必须写可观测诊断事件，
并由补偿 reconciler 在绑定恢复后按 repo + PR 更新时间游标重扫；重复 webhook/重扫对 canonical link 幂等。

## 5. Run 的精确模型

### 5.1 一个逻辑 Run 对应零到多个 provider execution attempt

Run 不新增一张产品表，复用最新 `dev` 已有路径：

```text
agent_inbox_event(id = run_id)              # durable logical Run intent
  pending / failed-retryable / draining / terminal
        |
        | provider 每次即将真实启动
        v
agent_execution(id = execution_id, source_event_id = run_id)
  running / completed / failed / cancelled
        ... 0..N attempts for one run_id
```

语义：

- inbox event 存在表示 durable Run intent 已建立；
- delivery lease 只表示某个 Runner 暂时负责投递，不是工作 ownership；
- `agent_execution` 不得在排队、等待 runtime 或等待 provider permit 时预造；
- 只有即将启动真实 provider turn 时才物化 execution；
- delivery lease renew / reclaim 保留同一 Run ID；如果真实 provider turn 重新启动，则创建新的 execution ID；
- 业务 retry、review rejection 或 Issue reopen 创建新 Run ID；历史 Run 和 execution 永不 reopen；
- usage、provider outcome 和真实 running 时间来自 `agent_execution`；
- queued / waiting 投影来自 inbox event、outbox 和资源状态。

### 5.2 active Run 唯一性

标准 Agent Issue 同时最多一个 active Run intent。唯一性必须覆盖 provider start 之前的 pending 状态，不能只在
`agent_execution WHERE status='running'` 上建约束。

推荐使用数据库可执行 claim：

```text
active_issue_execution
  workspace_id
  issue_id UNIQUE
  run_id UNIQUE
  agent_id
  issue_execution_revision
  status: dispatching | active | releasing
```

它只是 Issue→Run 唯一 claim，不是新产品 Task。若能在 `agent_inbox_event` 上用稳定 partial unique index 完整表达
相同约束，则不必建该表；实现评审必须先证明所有 retry / follow-up / mention path 都满足约束。

### 5.3 Issue execution revision

Issue 增加单调的 `execution_revision`。以下变化必须递增：

- assignee；
- status；
- acceptance criteria；
- dependency；
- Goal required/scope；
- 人工 Gate；
- Research adapter binding 所依赖的执行事实。

Run intent、ParticipationDecision 和 completion report 都记录 dispatch 时的 revision。旧 revision 的排队 Run 在
provider start 前必须被 cancel / supersede；已经 running 的 Run 由 controller 决定 drain、cancel 或接受 late report。

## 6. IssueExecutionReconciler

### 6.1 单一入口

所有可能改变 Issue 可执行性的写入都调用同一个 transaction-scoped reconciler：

```go
type IssueExecutionReconciler interface {
    ReconcileTx(ctx context.Context, tx pgx.Tx, issueID pgtype.UUID, trigger Trigger) error
}
```

入口包括 create、patch、batch、decompose、dependency unlock、assignee change、Run terminal、runtime/Computer
变化、Goal resume/replan、completion/review 和恢复扫描。

### 6.2 原子 dispatch intent

Issue 状态变化、execution revision、active Run claim 和 dispatch outbox 必须在同一事务提交。事务后 dispatcher
建立或唤醒 inbox event。现有 handler/service 中 best-effort `EnqueueTaskForIssue` 分支必须逐步删除。

`work_owner_lease` 的标准 executor role 只能在以下条件全部满足后退出：

1. active Run 唯一 claim 已经上线；
2. 所有 enqueue 入口已经收口；
3. crash / retry / reassignment 测试证明不会双执行；
4. Research legacy 和其他消费者已经通过唯一 adapter 改用 Issue / Run owner。

### 6.3 runnable 与等待原因

Agent Issue runnable 至少要求：

- status 为 `todo`，或已被当前 active Run 合法推进到 `in_progress`；
- required dependencies 全部 `done`；
- assignee 有效且未归档；
- acceptance criteria 非空；
- 没有人工或策略 Gate；
- execution revision 与 Run intent 一致。

每个 runnable Issue 必须恰好投影为：

```text
queued | running | waiting_runtime | waiting_agent_lane
waiting_provider | waiting_gate | superseded
```

Provider 排队不等于业务 blocked；Run attempt 的 provider failure 也不等于 Issue 已失败。

## 7. Participation Gate 与 Goal routing

### 7.1 Gate 不得吞掉 durable work

Participation Gate 是 relevance / role policy，不是资源 admission。它不得因为 runtime offline、provider queue 满或
Agent busy 而返回 `SILENT`。资源不足时仍保留参与决定和 canonical work，后续投影为 waiting。

确定性 assignment、active Run、dependency unlock 和 controller work 不需要模型判断：

- Issue reconciler 已创建 Run intent时，Gate 只能确认 `CONTRIBUTE`，不能再创建第二个 Run；
- controller deterministic preflight 不经过 Attention Probe；
- Research controller 需要 Agent 工作时只由其 adapter 产生一次 Issue / Run wake；
- Probe 失败不能释放 ownership 或完成工作。

### 7.2 active Goal 的确定性路由优先级

Goal 是否包含 Research Issue 不影响以下路由合同：

| 输入 | 参与结果 |
| --- | --- |
| 人类无 `@`，频道有 active Goal | 只有群管 `COORDINATE`；其他 Agent observe，不进入 Attention Round |
| 人类明确 `@Agent` | 被点名 Agent 直接执行；群管只收 controller event；不与未点名 Agent 竞争 grant |
| 人类 `@all` / “大家” | 只唤醒群管拆解，不全员 full turn |
| Issue Run intent | 只执行 assignee 的 `CONTRIBUTE` |
| completion / review / runtime / Gate 变化 | 只写 controller event，不广播团队 |
| 普通无 Goal 群消息 | 才进入普通 Participation + Attention Round |

多 Agent 明确 mention 是有意的定向输入，按 mention 集合分别建立定向执行；单回答 grant 只约束普通 ambient
竞争，不得压掉用户明确点名的目标。

### 7.3 ParticipationContext 修订

统一输入必须以 Issue / Run 为一等字段，而不是默认以 Graph 为中心：

```text
ParticipationContext
  workspace_id
  agent_id
  trigger_kind / trigger_id
  message_id / channel_id / thread_id
  directed_kind / directed_agent_ids
  goal_id / goal_version / goal_execution_graph_revision
  issue_id / issue_execution_revision / assignee_id
  run_id / run_state
  dependency_state / gate_state / review_state
  research_session_id / genealogy_revision   # 仅 Research 触发
  candidate_id / evidence_revision           # 仅 Research 触发
  public_answer_state
  recent_agent_only_depth / ambient_window_counts
  policy_version
```

runtime state、provider capacity 和 queue depth 不进入 relevance 结果；它们属于后续 admission projection。

### 7.4 幂等和事务边界

Participation decision 的幂等键至少包含：

```text
(trigger_kind, trigger_id, agent_id, policy_version, work_revision)
```

确定性 decision 与现有 dispatch outbox 在同一事务提交。Attention Probe 是异步受限 Run：先持久化 round /
participant，再进入 provider permit queue，结果通过 round fence 提交，最后由唯一 resolver 建正式 dispatch intent。

审计表不是任务真相，但一旦 decision 控制是否唤醒，就不能只写 best-effort 日志。

## 8. Attention Round 的实际迁移

现有 `channel_attention_round`、participant、response grant、restricted profile、parser 和 resolver 可以复用，
但实施前需要补齐 orchestration owner：

1. canonical Message transaction 创建或合并 round；
2. Participation Gate 先排除确定性 SILENT / direct / manager-first 候选；
3. 只有剩余不确定候选运行 restricted probe；
4. round deadline / participant failure 不影响其他候选；
5. resolver 只创建一个 ambient public response grant；
6. grant consumption 与消息发送使用同一 fence；
7. crash 后 round 可恢复，过期 round 不得重新发布旧回答。

active Goal manager-first 消息不创建全员 Attention Round，从源头避免额外 probe 成本和 manager routing 竞态。

## 9. 并发、容量与容灾

### 9.1 沿用最新 `dev` 的 Agent lane policy

本规格不改变同 Agent 多 session 策略：

- 同 Agent conversation lane 最多一个 active run；
- 同 Agent 的同一 Issue lane 串行；
- 同 Agent 不同 Issue lane 可并发，最多 `max_concurrent_tasks`；
- conversation lane 可与 Issue lanes 并行；
- 不同 Agent session 可以在同一 runtime / Computer 上并行；
- Server claim 和 daemon execution key 必须继续使用同一 lane 划分。

`max_concurrent_tasks` 是当前 scheduler policy 事实，不要求本规格重新暴露 UI 配置。

### 9.2 执行槽与故障域分开

可并行度不能用 runtime 数量计算：

- `claimable_issue_lane_count` 表示 Server 当前可 claim 的 Issue lane；
- `active_run_count` 表示真实 Run；
- `running_overlap` 表示已观察到的真实并行；
- `distinct_computer_count` / `distinct_runtime_count` 只用于 resilience；
- Computer process capacity 控制常驻 provider process，不等于 provider credential concurrency；
- Provider permit queue 满只产生 `waiting_provider`，不虚报 Agent lane 不存在。

单 Computer / runtime 可以启动 Goal 并真并行，但显示 `single_failure_domain`。只有产品承诺“可持续自治 / 可容忍
单机故障”时，才把两个不同 Computer 故障域作为硬 Gate。

### 9.3 在线真相

Goal admission 使用：

- current Workspace Runner socket / Computer liveness；
- Runner 报告的 current runtime set；
- Agent 与 runtime / Workspace binding 有效；
- Server lane 可 claim 状态；
- provider scheduler 的 domain blocked / cooldown 投影。

禁止仅凭 `agent.runtime_id` 或 `agent_runtime.status='online'` 宣称 ready。

## 10. Provider 凭证域调度器

### 10.1 精确控制边界：Provider Turn Permit

首版 permit 改名并定义为 `ProviderTurnPermit`。它覆盖一个顶层 provider turn：

- fresh task 的 `Backend.Execute`；
- resident Message turn 的 input admission；
- Pi run / equivalent resident turn 的 prepare-to-start boundary；
- Attention Probe、verification、compaction 等同样走各自 lane。

Permit 不承诺观察或限制 CLI turn 内部的每个 HTTP 请求。若未来 provider adapter 暴露可信 per-request hook，
可在同一 domain scheduler 下增加更细粒度 token bucket，但不能反向改变首版 Run 语义。

### 10.2 与 process capacity 的顺序

Machine process capacity 和 ProviderTurnPermit 是两个独立约束：

```text
确保/复用 provider process
  -> 即将提交 turn
  -> 获取 ProviderTurnPermit
  -> 创建 agent_execution
  -> 提交 turn
```

不得在持有稀缺 Provider permit 时长时间等待 process spawn capacity。若 provider process 必须冷启动，先取得 process
admission 并完成必要启动，再申请 turn permit；Starting Activity 仍遵守现有唯一生产入口，permit grant 不产生 Starting。

### 10.3 CredentialDomainKey

```text
CredentialDomainKey
  schema_version
  provider_id
  credential_fingerprint | unknown
  endpoint_class
```

规则：

- key 不包含 token、cookie、邮箱、账号名或可逆 ID；
- adapter 只可对高熵 credential material 使用 machine-scoped、domain-separated fingerprint；
- 不能安全稳定识别时使用 `unknown`，按 `provider + endpoint + machine` 保守合并；
- `auth_source_kind` 只能作为诊断属性，不能在无法证明配额独立时拆分 domain；
- model / workspace / Agent / runtime 不得默认拆分共享账号；
- endpoint 或 model 只有在 provider 官方且实测证明配额独立后才可形成子域；
- mixed-version child 缺少受支持的 key schema 时进入 provider-wide unknown domain，不能随机分桶。

### 10.4 Permit identity 与 fence

请求至少包含：

```text
requestId
serviceGeneration
daemonInstanceId
runnerPid
workspaceId
agentId
runId
credentialDomainKey
lane / priority
enqueuedAt / deadlineAt
```

Computer 只接受 current child identity；旧 child 的 queued / granted permit 全部失效。重复 requestId 返回原申请，
但 runId 或 identity 不同的重复 requestId 必须拒绝。

### 10.5 公平队列

lane 首版固定为：

```text
human_direct
controller
full_turn
verification
attention_probe
compaction
background
```

调度顺序使用 domain total limit + lane cap + workspace deficit round robin + lane aging。只有 lane 最大值不足以防饿死；
实现必须证明：

- ambient probe burst 不能吃光 full turn；
- 持续 human direct 不能永久饿死 active Goal work；
- verification / controller 能在有界时间内推进长程闭环；
- background 有 aging，但不能越过 hard Gate 或依赖；
- 已启动 turn 不抢占。

具体权重和上限通过 shadow 数据校准，不写死在本规格。

### 10.6 长时间排队

短 permit wait 可以继续续租现有 delivery。等待超过配置阈值或进入长 cooldown 时：

1. 不创建 `agent_execution`；
2. 释放 delivery lease，把同一 inbox Run intent 恢复为 claimable waiting state；
3. 写 `wait_reason=provider_capacity` 和 `next_eligible_at`；
4. Computer / Runner 在 domain 可用时发 content-free wake；
5. 重新 claim 仍使用同一 Run intent ID；
6. execution revision 已变化时由 reconciler supersede，而不是启动旧工作。

Permit deadline 只终止本次资源等待，不得丢失 Message、Issue 或 controller event。

## 11. Provider 失败分类

Provider 反馈至少分三类：

| 类别 | 例子 | 处理 |
| --- | --- | --- |
| transient rate limit / overload | 429、Retry-After、短时容量不足 | domain cooldown + adaptive pacing；Run attempt 可失败，Issue 保持可恢复 |
| sticky quota / billing | 月额度耗尽、billing disabled、明确 reset time | domain hard block 至 reset / 人工修复；禁止指数重试风暴 |
| auth / configuration | token 失效、模型不可用、配置错误 | domain/target unavailable + Needs You；不当作 transient 429 |

现有 per-Agent `provider_blocked_until/detail` 在迁移期作为 UI compatibility projection。共享凭证域 scheduler 上线后，
同一 domain 的 hard block 必须影响 sibling Runner，而不能只阻塞第一个碰到错误的 Agent；Server 只接收不含账号细节的
domain state / next eligible projection。

Provider failure 可以让一个 `agent_execution` 真实终态为 failed，但不能直接把 Issue、Goal 或 Graph 结论标为失败。
Reconciler 根据 failure class 决定 waiting、创建新 Run、换 target 或 ASK_HUMAN。

## 12. Completion report 与 review

### 12.1 存储

推荐增加内部受约束 sidecar：

```text
issue_completion_report
  id
  workspace_id
  issue_id
  run_id UNIQUE
  issue_execution_revision
  submitted_by_agent_id
  summary
  acceptance_results JSONB
  artifact_refs JSONB
  risks JSONB
  visible_comment_id UNIQUE
  review_status: pending | accepted | rejected | superseded
  reviewer_type / reviewer_id / reviewed_at
  created_at
```

它不是新的用户产品对象；UI 仍在 Issue comment / review surface 展示。sidecar 提供 typed validation、FK、唯一性和
Goal completion query，普通 comment 提供人类可读记录。

### 12.2 Agent completion command

```text
POST /api/agent/issues/{issueId}/completion
```

同一事务必须：

1. 只从当前 task credential 解析 canonical Run，拒绝客户端自报或替换 `run_id`；
2. 验证该 Run 的 workspace、Issue、assignee、active claim 和 expected issue execution revision；
3. 校验每条 acceptance criterion 都有结果和 evidence；
4. 插入 completion report；
5. 插入可见 comment；
6. 把 Issue `in_progress -> in_review`；
7. 释放或转换 executor claim；
8. 写 controller event。

该 API **不写 `agent_execution.status=completed`**。daemon 的真实 turn completion / failure callback继续拥有 execution
终态。如果 report 已提交后 turn 又失败，report 仍保留并可 review；审计同时显示“产出已提交，执行终结失败”。

首版 CLI 为 `multica issue complete <issueId> --summary ... --evidence INDEX=KIND:REF`。它先读取当前 Issue 的
criteria / revision，再构造完整 typed request；缺项、重复项、过期 criterion 文本、无 evidence 或不属于当前 task 的
Run 都必须服务端拒绝，不能依赖 CLI 校验兜底。同一 Run 的相同 request hash 幂等返回已有 report且不重复发布
realtime event，不同内容冲突。

### 12.3 Review

- Agent 实现者不能接受自己的 report；
- 低风险 Issue 可由群管接受；
- 发布、集成、权限、安全和 Goal 最终 completion 由独立 reviewer 或人接受；
- accepted verdict 原子推进 Issue `in_review -> done` 并解锁 dependency；
- rejected verdict 记录 reason，推进 `in_review -> todo/blocked`，递增 execution revision，并由 reconciler 创建新 Run；
- rejected successor Run 必须以原 report 的 Run 为 `parent_run_id`；旧 Run、report、review verdict 继续留在 History；
- Agent 通过通用 Issue PATCH 直接进入 `in_review` 或 `done` 必须拒绝，历史 / 人类兼容路径另行显式授权；
- `pull_request` evidence 只有在该 URL 已存在于 canonical `issue_pull_request -> github_pull_request` 关系时才能被
  accepted review 接受；标题、正文、分支、comment 或 Issue metadata 中的 PR 号/URL 都只能作为待修复线索；
- report author 不能 review 自己的 report；Agent reviewer 也必须使用同一 Issue 上独立的 task-scoped Run；
- Issue 删除可以级联删除 report，但不得因 canonical Run 的历史字段与 `ON DELETE SET NULL` 冲突而失败；Run 审计历史保留。
- 永久删除 Agent 不得删除或阻塞 completion/review 审计；report 保留提交者 UUID 作为 tombstone identity，授权时仍由
  task-scoped Run 验证 workspace / Agent 归属，而不是依赖可被删除的 actor 外键。

## 13. Goal controller

### 13.1 不复用 graph-bound epoch

标准 Goal controller 增加内部 ledger：

```text
goal_controller_event
  goal_id / event_kind / source_id / source_version / idempotency_key / created_at

goal_controller_tick
  goal_id / goal_version / through_event_id / lease / decision / wait_reason
  wake_on / review_at / status / created_at / completed_at
```

它们不是用户可见的新任务对象。现有 Research epoch / graph controller 暂时负责 Research Genealogy 内部演化，
但需要 Agent 工作时必须调用唯一 adapter，由 Goal controller / Issue reconciler 建立 Issue / Run；两者不能直接
竞争 provider execution ownership。

### 13.2 每个 tick

1. 获取单 Goal lease；
2. 读取 Goal version、scoped Issue、Run、review、Gate、Computer 和 provider state；
3. 先运行 deterministic reconciler；
4. 没有判断工作时记录 `no_change` 并退出；
5. 需要拆解、重规划、取舍、review 或总结时，为群管建立 `COORDINATE` Run intent；
6. 群管返回 version-fenced typed decision；
7. 服务端校验并应用；
8. 写 checkpoint、`wake_on[]`、`review_at` 后结束。

Controller tick 结束不等于 Goal 停止。Issue terminal、review、runtime、provider、Gate、人类消息和 safety timer 都会
再次写 controller event。

## 14. Goal admission 与状态投影

不要再用一个 `ready` 字符串混合业务、容量和容灾。API 至少返回：

```json
{
  "execution_status": "planning | active | waiting | blocked | completed",
  "wait_reasons": ["provider_capacity"],
  "runnable_issue_count": 3,
  "queued_run_count": 1,
  "running_run_count": 2,
  "in_review_issue_count": 1,
  "claimable_issue_lane_count": 2,
  "provider_waiting_count": 1,
  "distinct_computer_count": 1,
  "resilience_warnings": ["single_failure_domain"],
  "next_action": "review LRM-1590"
}
```

规则：

- provider queue 是正常 waiting，不是 Goal blocked；
- 单故障域是 warning，不降低真实并行度；
- 整个 Goal 只有无效 runtime / hard credential block 且无人处理时才进入 Needs You；
- Goal completion 只查同 Goal scope 的 required Issue、accepted report/verdict 和 success-criteria evidence；
- `cancelled` required Issue 不算成功；
- unrelated project Issue 不阻塞 Goal。

## 15. 实施与上线顺序

### Slice A：先修 durable Issue Run

1. 增加 Goal-Issue scope、Goal Execution Graph projection revision 和 Issue execution revision；
2. 抽出 `IssueExecutionReconciler`；
3. 建 active Run claim + dispatch outbox；
4. Run intent 使用 inbox event ID；provider execution 使用独立 ID 并以 `source_event_id` 绑定 Run；
5. 覆盖 reopen、dependency、batch、runtime move 和 crash recovery；
6. 保留 `work_owner_lease` 直到替代约束变绿。

### Slice B：完成与 review 收口

1. completion report sidecar + command；
2. Agent 不能直接 done；
3. review verdict + rejection rerun；
4. Goal scoped completion query；
5. `DecomposeIssue` 强制 acceptance criteria。

### Slice C：Provider scheduler shadow → enforce

1. Computer passthrough permit 和 domain fingerprint shadow；
2. 只管 Attention Probe 和新 full turn；
3. 启用 pacer、domain total、lane cap、公平和 crash fence；
4. 接 transient 429 / Retry-After；
5. 接 sticky quota / auth domain block；
6. resident turn、verification、compaction 最后接入。

### Slice D：Participation Gate

1. 先上线 active Goal manager-first deterministic routing；
2. 接 Issue Run / controller 的确定性 work routing；
3. 接普通 ambient candidate filtering；
4. 串起现有 Attention Round orchestration；
5. 删除重复 ambient / attention production entry。

### Slice E：自治 controller 与执行图收口

1. Goal controller event/tick；
2. deterministic preflight + bounded manager Run；
3. waiting / Needs You / safety scan；
4. 建 Goal Execution Graph projection，覆盖 Issue、Run、decision、review、artifact 和 failure；
5. 增加 per-Goal snapshot / delta、projection revision 和 existing Workspace event push；
6. 上线 Plan / Live / History 三种折叠视图和 canonical source drill-down；
7. 上线只读无限画布：二维平移、指针中心缩放、fit/100%/当前 Run、稳定增量布局和键盘可达；
8. 将 repo resource、GitHub event source 和 canonical PR link 健康状态分层，并为未知 installation 增加诊断与补偿重扫；
9. 普通 Goal 停止写 Work Graph / Subgoal；
10. `advance graph` 收敛为幂等 `AdvanceGoal` / `ReconcileGoalExecutionGraph`；
11. Research legacy 通过唯一 adapter 创建或绑定 Issue / Run，消除双重 wake；
12. 最后移除标准 Issue 的 `work_owner_lease`。

Provider scheduler 在 Participation Gate enforce 前上线，避免 Gate / Probe rollout 自身制造新的 provider burst。

## 16. 必须见红的验收与 benchmark

### 16.1 事务和恢复

- Issue update 提交后进程崩溃，outbox 重放只产生一个 Run intent；
- cancelled / blocked / done 重开生成新 Run ID；
- stale execution revision 的 queued Run 不启动 provider；
- delivery lease 过期只重投同一 Run intent，不制造第二个 active claim；
- runtime move / reconnect 后无需人说“继续”；
- report 提交成功但 provider terminal callback 失败时，report、Issue review 和 execution history均真实可解释。

### 16.2 路由和 Gate

- active Goal 无 `@` 消息只创建 manager decision，不创建全员 round；
- 明确 mention 不受 ambient cap / probe failure 静默影响；
- assigned Run 永不被 capacity 状态判成 SILENT；
- ordinary ambient 多候选最多一个 public grant；
- Agent-only confirmation loop 有界停止；
- deterministic manager / issue routing 不调用 Attention Probe。

Research 场景还必须证明：

- candidate parent-child / reference / crossover 不会被误当作 Issue dependency；
- 两个独立 candidate 通过不同 child Issue 并行，且同一 candidate binding 不产生重复 Run；
- failed / pruned candidate 保留 score、evidence 和 lineage，但不会阻塞 Research Issue 的 champion review；
- Research conclusion 只能把 Research Issue 推进到 `in_review`，不能直接完成 Issue / Goal。

### 16.3 并发和 Provider

- 同 Agent 两个独立 Issue 在 `max_concurrent_tasks=2` 时真实重叠；
- 同 Issue follow-up 仍串行；
- 同 runtime 不同 Agent session 可并发；
- distinct runtime 只影响 resilience warning；
- 同 credential domain burst 受 total + pacer 约束；
- 不同 domain 互不阻塞；
- probe burst 不饿死 Goal full turn；
- 持续 human direct 下 verification / active Goal work 通过 fairness / aging 最终启动；
- Computer / child crash 后 permit 回收，Run intent 可恢复；
- long cooldown 不持有陈旧 delivery lease；
- sticky quota 不进入无限 429 retry；
- permit grant 不产生额外 Starting Activity。

### 16.4 动态执行图

- 创建 Parent Issue、child Issue 和 dependency 后，snapshot 以 typed edge 展示相同 canonical 关系；
- Issue Run 从 queued → running → terminal 时只递增 projection revision，不改变 Issue 拓扑 identity；
- completion report 和 accepted review 展开为可追溯链，Parent Issue 进度从 required child verdict 派生；
- rejected review 展示拒绝原因、reopen 和 successor Run，旧 Run / report 不被覆盖；
- superseded / failed 分支保留在 History view，但不污染当前 Plan frontier；
- projection worker 重放同一事件不会产生重复节点或边；
- 客户端漏掉 delta、乱序收到 event 或断线重连时，通过 revision gap 强制重取 snapshot；
- projection 延迟、损坏或重建不能创建 Run、改变 Issue status 或完成 Goal；
- 任一节点都能通过 `source_kind + source_id + source_version` 打开 canonical 详情。
- 画布可向四向平移，滚轮以指针为中心缩放；画布内手势不误滚整页，fit/100%/当前 Run 可恢复导航；
- 用户无法拖动拓扑、连边或删除节点，节点布局变化不产生服务端 canonical mutation；
- 实时 delta 到达时保留用户视口和既有节点心智地图，只有用户明确请求时才重新 fit；
- Parent Issue 折叠、分支聚焦和状态过滤在大图中不改变 canonical 节点/边数量；
- 真实网页游戏 fixture 能从 Goal 展开到 major Issue、child Issue、Run、report、review、evidence 和 PR；
- PR #65 merged 且 metadata 有 `pr_url`、但 canonical link 为空时，图必须显示 `integration_gap`，不得以 metadata 补齐；
- `repository_bound=true`、`event_source_connected=false` 时 UI 不得只显示“Git 仓库已绑定”的绿色正常态；
- 安装绑定恢复并补偿重扫后，PR #65 的 canonical link 幂等收敛，异常节点消失且历史诊断仍可审计。

### 16.5 长程 benchmark

至少运行：

1. `single-answer`；
2. `goal-manager-first`；
3. `parallel-issue-dag`；
4. `same-agent-multi-issue`；
5. `review-rejection-rerun`；
6. `runtime-disconnect-resume`；
7. `provider-burst`；
8. `provider-recovery`；
9. `sticky-quota-needs-you`；
10. `multi-day-controller-resume`；
11. `research-genealogy-isolation`；
12. `single-failure-domain-warning`；
13. `web-game-goal-execution-graph`；
14. `github-canonical-link-recovery`。

指标包括 Goal 完成率、人工“继续”次数、runnable-without-run、重复 Run、重复公开回答、review 返工率、
provider queue P50/P95、429、hard-block 重试次数、runtime 恢复时间、Research duplicate wake、genealogy provenance
完整率、canonical integration gap 平均发现/恢复时间、视口稳定率、图中不可追溯节点数、token、成本和真实并行重叠。

## 17. 已确认方向与待确认实现决定

以下分层方向已经确认：

- 所有 Goal 使用 Issue / Run 作为唯一执行控制面；
- Goal Execution Graph 是统一执行 read model，不是第二套 scheduler；
- Research Genealogy 嵌套在 Research Issue 下，表达 candidate / evidence 演化；
- 现有 Work Graph 暂作为 `research_legacy` 内部实现保留，不迁移、不作为 Goal 高级模式、不与 Issue 双重派发；
- `advance graph` 收敛为推进 Goal 的 controller operation。

以下三项不是调参问题，会改变实现形状；实施采用以下已确认默认：

1. **Completion report 是否使用 sidecar + visible comment？**
   推荐：是。当前 comment 没有 typed parts；sidecar 能建立 Run / criterion / reviewer / evidence 约束，
   comment 继续承担产品展示。

2. **Provider permit 是否正式定义为顶层 turn，而非每个上游 API request？**
   推荐：是。它符合当前 `Backend.Execute` / resident turn 可控 seam，也避免做出无法观测的精确限流承诺。

3. **标准 Goal controller 是否使用独立 internal event/tick ledger，而不复用 `goal_execution_epoch`？**
   推荐：是。现有 epoch 强依赖 Graph；分开 internal ledger 才能真正让标准 Goal 不创建空 Graph。

上述决定随本 spec 一并 Accepted；实现仍须按 Slice 顺序、feature flag 和验收门分批上线。

## 18. 覆盖关系

本规格已经接受并覆盖以下旧口径：

- 覆盖 Goal spec 中“Run 只复用 `agent_inbox_event`”的模糊口径，明确 inbox event 是逻辑 Run intent，
  provider execution 是通过 `source_event_id` 关联的一对多 attempt ledger；
- 覆盖 Goal spec 中“复用 `goal_execution_epoch`”的建议，标准 Goal 使用独立 internal tick；
- 覆盖 Goal spec 中“completion report 存为 comment typed part”的建议，改为 sidecar + visible comment；
- 覆盖 Provider spec 中默认 Work Graph-centric ParticipationContext，改为 Issue / Run first、Research context optional；
- 覆盖 Provider spec 中“每次真实 Provider 请求”的描述，首版定义为 provider turn；
- 覆盖 Provider spec 把 quota 与 transient 429 一并 adaptive pacing 的描述，明确 sticky hard block；
- 明确 Goal Execution Graph 是 Issue / Run 等 canonical 状态的 read model；Research Genealogy 是 Research 专用
  provenance 图，现有 Work Graph 只作为 `research_legacy` 内部实现过渡；
- 保留 active Goal manager-first、session 并行、Agent completion 先 `in_review`、单故障域只警告等已确认决定。

在本规格被接受并完成 source-map / builtin skill 同步前，代码仍以当前 `dev` 行为为准，文档不得宣称上述新
controller、Gate 或 scheduler 已上线。
