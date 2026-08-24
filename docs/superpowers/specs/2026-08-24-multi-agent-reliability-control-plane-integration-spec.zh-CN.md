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

1. 验证当前 task credential、runId、assignee 和 active Issue claim；
2. 验证 expected issue execution revision 和 Goal version；
3. 校验每条 acceptance criterion 都有结果和 evidence；
4. 插入 completion report；
5. 插入可见 comment；
6. 把 Issue `in_progress -> in_review`；
7. 释放或转换 executor claim；
8. 写 controller event。

该 API **不写 `agent_execution.status=completed`**。daemon 的真实 turn completion / failure callback继续拥有 execution
终态。如果 report 已提交后 turn 又失败，report 仍保留并可 review；审计同时显示“产出已提交，执行终结失败”。

### 12.3 Review

- Agent 实现者不能接受自己的 report；
- 低风险 Issue 可由群管接受；
- 发布、集成、权限、安全和 Goal 最终 completion 由独立 reviewer 或人接受；
- accepted verdict 原子推进 Issue `in_review -> done` 并解锁 dependency；
- rejected verdict 记录 reason，推进 `in_review -> todo/blocked`，递增 execution revision，并由 reconciler 创建新 Run；
- 直接 PATCH Agent Issue 到 `done` 必须拒绝，历史 / 人类兼容路径另行显式授权。

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
7. 普通 Goal 停止写 Work Graph / Subgoal；
8. `advance graph` 收敛为幂等 `AdvanceGoal` / `ReconcileGoalExecutionGraph`；
9. Research legacy 通过唯一 adapter 创建或绑定 Issue / Run，消除双重 wake；
10. 最后移除标准 Issue 的 `work_owner_lease`。

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
12. `single-failure-domain-warning`。

指标包括 Goal 完成率、人工“继续”次数、runnable-without-run、重复 Run、重复公开回答、review 返工率、
provider queue P50/P95、429、hard-block 重试次数、runtime 恢复时间、Research duplicate wake、genealogy provenance
完整率、token、成本和真实并行重叠。

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
