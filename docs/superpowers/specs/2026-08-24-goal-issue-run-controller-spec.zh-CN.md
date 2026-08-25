# Goal / Issue / Run 长程协作收口规格

> 状态：Proposed；第 14 节决定 1–3 已于 2026-08-24 确认，其余方案待实施评审。
> 本文本身不授权修改生产行为。
> 与 Provider scheduler、Participation Gate、Goal Execution Graph 和 Research Genealogy 的整合修订以
> `2026-08-24-multi-agent-reliability-control-plane-integration-spec.zh-CN.md` 为准。
>
> 日期：2026-08-24
>
> 代码基线：`lrm/dev@55b1974bb8b0ea39295972f2c2fe17c35960d5db`
>
> 目的：解决长程任务依赖聊天回合、Issue 重开不重新执行、离线运行时仍被当成可执行、
> 多个 Issue 看似并行但实际由同一 Agent 串行、群内重复确认和群管无法持续推进的问题。

## 1. 结论

标准长程协作只保留三个 canonical 对象：

1. **Goal**：保存人类长期目标、成功条件和当前 checkpoint；
2. **Issue**：保存可交付工作、负责人、父子关系、依赖和验收条件；
3. **Run**：保存一次真实执行尝试、运行时、claim、心跳和终态。

群管不是第四套任务系统。群管是 Goal 的 coordinator，由服务端持续触发，负责更新 Goal、拆 Issue、
分配负责人、处理异常、安排复核和汇总。服务端负责可靠唤醒和恢复，模型负责判断与计划。

标准路径不再要求创建 Subgoal 或 Work Graph。Issue 的 `parent_issue_id` 和 `issue_dependency` 是唯一任务拓扑；
图只是它们的只读投影。现有 `channel_goal_subgoal` 和通用 Work Graph 暂不立即删表，但退出标准 Goal 的写路径。

```text
人类长期目标
  → Goal（持续更新，不按阶段替换）
    → 阶段 Parent Issue
      → 可并行的子 Issue + 依赖
        → 每个 Agent Issue 的 Run
      → 复核 / 集成 Issue
    → checkpoint
    → 下一阶段，或满足成功条件后完成 Goal
```

## 2. 为什么要改

### 2.1 线上案例暴露的问题

2026-08-24 对"网页游戏开发"频道的检查显示：

- 人类多次需要发送"继续""怎么断了"才能恢复推进；
- 有一次在人类询问"实现怎么样？有更新 goal 么"后，主时间线约 109 小时没有有效推进；
- LRM-1589、LRM-1590、LRM-1593 从 `cancelled` 重开后，没有创建新的 Issue Run；
- 三个声称并行的开放 Issue 全部分配给 Tess，但没有三个并发的新 Run；分配和 `todo` 状态本身不等于并行；
- Kai、Tess、John、Man 绑定同一个运行时，该运行时离线会让整个团队同时停止；
- 约三分之一的智能体消息以"收到 / 对齐 / 确认"开头，说明群聊软提示没有真正抑制重复发言；
- active Goal 被改写成"Phase-4"，长期目标退化成了当前阶段标题；
- Goal admission 显示 `ready`，但没有检查运行时在线、Run 是否存在、Issue 是否真并行、验收条件是否完整。

### 2.2 当前代码的直接原因

代码基线中的关键缺口：

- `server/internal/handler/issue.go` 只在负责人变化或 `backlog → active` 时入队，
  没有覆盖 `cancelled / done / blocked → todo`；
- `isAgentAssigneeReady` 只检查 Agent 有 `runtime_id` 且未归档，不检查运行时是否在线、连接是否健康或是否有容量；
- Issue 更新后再调用入队，入队错误没有成为同一事务的失败，Issue 状态和 Run 可以分叉；
- `channelGoalExecutionAdmission` 只看成员、项目、Git 和 Issue 数量，不看真实执行条件；
- `DecomposeIssue` 已能原子创建 Issue DAG，但把 `acceptance_criteria` 固定写成空数组；
- Goal manager、并行拆分、复核和续跑主要靠 Prompt 约束，没有服务端 invariant；
- `goal_execution_epoch` 只提供 Agent 调用的 start / finish 记录，没有服务端自治 controller；
- active Goal 频道仍会 ambient 唤醒多个 Agent，是否沉默依赖模型自觉。

## 3. 范围与非目标

### 3.1 本规格要解决

- Goal 跨聊天回合、进程重启、运行时离线后仍能恢复；
- Issue 一旦成为可执行状态，服务端保证"有 Run，或有明确等待原因"；
- 独立 Issue 能按真实执行槽并行，而不是只在列表里同时变成 `todo`；
- 群管持续维护 Goal 和 Issue 计划，不依赖人类反复说"继续"；
- Worker 通过 Issue / Run 接活，不靠群聊口头分工；
- 完成、复核、集成和 Goal checkpoint 有可核对证据；
- 普通短任务仍保持简单，不被强制升级成 Goal。

### 3.2 本规格不做

- 不设计无限 token、永不退出的单次 Agent Run；
- 不让系统自动扩大权限、自动发布生产或绕过人工 Gate；
- 不要求每个聊天回答、内部重试或机械检查都创建 Issue；
- 不在 v1 引入新的通用 Graph、Node、Subgoal、Todo 或 Handoff 实体；
- 不把 LoopX 的本地文件结构原样搬进 Multica；
- 不在第一阶段删除 Research / Self Evolution 的高级 Work Graph 能力。

## 4. 产品模型

### 4.1 普通任务

一次性问答、单次工具调用、小改动继续走：

```text
Chat → 一个 Issue（需要持久执行时）→ 一个负责人 → Run → 写回
```

不创建 Goal、Subgoal 或 Work Graph。

### 4.2 长程任务

满足任一条件时，进入 Goal 模式：跨多个自然日、需要多个独立交付、需要持续监控、需要多轮重规划，
或人类明确要求长期推进。

Goal 模式使用：

```text
Goal → 一个或多个阶段 Parent Issue → 子 Issue DAG → 复核 / 集成 → checkpoint
```

阶段不是新 Goal。比如"做一个持续扩展的网页游戏"是 Goal；"Phase-4 附魔 / 酿造 / 红石"是 Parent Issue。

### 4.3 Goal 的语义

Goal 保存：

- 人类原始目标和允许系统改写后的当前 objective；
- 可验证的成功条件；
- 权限、预算、停止条件和人工 Gate；
- 当前阶段、进展摘要、阻塞、下一步和证据引用；
- 来源频道和当前 coordinator；
- optimistic `version`。

规则：

- 一个频道同一时刻最多一个 `active` 或 `paused` Goal；
- Goal 的 objective 可以版本化澄清，但不能被阶段标题覆盖；
- 每完成一个阶段，群管更新 checkpoint，并创建或激活下一阶段 Parent Issue；
- 只有成功条件全部满足且有证据，Goal 才能 `completed`；
- 普通 Run 完成、Issue `done` 或 provider exit 0 都不能直接完成 Goal；
- Goal 完成后保留历史，后续新目标创建新 Goal，不复用旧 Goal 身份。

### 4.4 Issue 的语义

Issue 是唯一可分配、可讨论的工作单元：

- `parent_issue_id` 表示阶段或交付拆分；
- `issue_dependency` 表示真实执行依赖；
- `assignee_type / assignee_id` 表示唯一当前负责人；
- `acceptance_criteria` 表示 Definition of Done；
- `channel_goal_id` 表示该 Issue 属于哪个 Goal；
- `goal_required` 表示该 Issue 是否仍是当前 Goal 计划的 required 工作；
- 评论、附件、PR、测试结果和 completion report 保存共享证据。

v1 给 `issue` 增加可空的 `channel_goal_id` 和 `goal_required`，而不是新增 Goal-Issue 多对多表。
一个 Issue 同一时刻只能属于一个 Goal。Goal Issue 默认 required；群管只能在一次带 reason 的 Goal version 更新中
把取消或被替代的 Issue 改为非 required，历史归属仍保留。
通过 Goal 分解创建的子 Issue 必须继承 `channel_goal_id`、项目和来源频道。

标准 Goal 不再写 `channel_goal_subgoal`。需要"子目标"的地方创建 Parent Issue。

### 4.5 Run 的语义

Run 复用现有 `agent_inbox_event` / Agent task，不新增第二张执行表。本文统一称其为 Run。

一个 Run 表示"某个 Agent 在某个运行时上，对某个 Issue 的一次执行尝试"。它至少包含：

- `issue_id`、`agent_id`、`runtime_id`；
- 创建原因和触发事件；
- queued / claimed / running / terminal 状态；
- claim / lease、开始时间、最后心跳和结束时间；
- terminal outcome、失败分类和可恢复性；
- 对应 completion report / 输出引用。

对 Agent 负责人，Issue `in_progress` 必须有一个 non-terminal Run。没有活 Run 的 Agent Issue 不能长期显示为"执行中"。
人类负责人不受这条 Run invariant 约束。

数据库必须保证同一 Issue 最多一个有效 non-terminal 执行 Run。具体状态集合由现有 task domain 函数统一定义，
不得在多个 handler 里各写一份字符串列表。

## 5. 核心 invariant

下面规则必须通过事务、唯一约束、类型或测试实现，不能只写进 Prompt。

### I1. 可执行 Issue 必须可解释

Agent Issue 满足以下条件时是 runnable：

- 状态是 `todo` 或已被合法 claim 的 `in_progress`；
- 所有 required dependency 均为 `done`；
- 负责人存在且未归档；
- acceptance criteria 非空；
- 没有人工 Gate 或策略阻塞。

runnable Issue 必须恰好处于以下一种执行投影：

```text
queued | running | waiting_runtime | waiting_capacity | waiting_gate
```

不允许出现"状态看起来能做，但没有 Run，也没有等待原因"。

### I2. 所有状态入口统一 reconcile

下列事件必须调用同一个 `IssueExecutionReconciler`：

- Issue 创建；
- assignee、status、acceptance criteria 或 dependency 变化；
- `DecomposeIssue` 提交；
- 上游 Issue 完成并解锁下游；
- Run 进入终态；
- 运行时上线、离线、替换或容量变化；
- Goal resume / replan；
- 服务启动后的恢复扫描和周期安全扫描。

`PATCH issue`、batch update、Graph adapter、CLI 和内部 handler 不得各自决定是否入队。

### I3. Issue 变化和 dispatch intent 原子提交

Issue 更新和"需要创建 Run"的 intent 必须在同一数据库事务提交。事务内写 outbox / durable dispatch intent，
事务后由 dispatcher 创建或唤醒 Run。进程在两步之间崩溃时，outbox 可以重放。

禁止继续使用"先更新 Issue，随后 best-effort 调 `EnqueueTaskForIssue` 并忽略错误"的模式。

### I4. 重开必须是新 Run

`blocked / cancelled / done → todo`、失败后重试、重新分配负责人都必须重新 reconcile。
如果没有 non-terminal Run，则生成新的 Run ID；历史 cancelled / failed Run 不得被改回 running。

若当前 Agent 在自己的 Run 内把同一个 Issue 从 `in_progress` 改为 `todo`，服务端应先终结旧 Run，
再通过 outbox 创建下一次 Run，不能靠跳过 enqueue 来避免自循环。

### I5. Ready 必须包含运行时事实

Agent ready 至少要求：

- Agent 未归档并绑定运行时；
- `agent_runtime.status=online`；
- Computer 连接和最近心跳未过期；
- 运行时仍属于当前工作区并可使用；
- 有可用执行槽。

只有 `runtime_id` 不代表 ready。

### I6. 并行必须是真并行

两个 Issue 只有同时满足以下条件才算可并行：

- 相互没有 dependency；
- 不争用同一个互斥资源或写入范围；
- 有两个可用 session 执行槽；同一运行时上的独立 Agent session 均可计入；
- 已经有两个并发 non-terminal Run，或 dispatcher 能同时 claim 两个 Run。

多个 Issue 的负责人共用同一个运行时不自动等于串行，也不自动等于并行。只有独立 session 被并发 claim，
或者 scheduler 根据明确的 session capacity 能同时 claim，才可以在 Goal UI 中显示"可并行"；只有 Run 的 running
时间窗口真实重叠，才显示"正在并行"。

一个运行时可以同时承载多个 session，因此并行度不能用 distinct runtime 数量计算。可用槽数由运行时 session
并发上限、当前 active session 和 dispatcher claim 结果决定。若当前协议没有上报 session capacity，v1 应补充该字段，
或以 daemon 现有并发策略的可 claim 结果为准，禁止硬编码成"一个运行时一个槽"。

并行度和容灾是两件事：同一运行时上的多个 session 可以真并行，但仍共享一个故障域；该运行时离线时会一起停止。
同一个 Agent 是否允许多个 session 并发由 Agent scheduler policy 另行规定，本次确认不改变该策略。

### I7. 完成必须带证据

Agent 不能只改 Issue 状态宣称完成。它必须原子提交 completion report：

```json
{
  "summary": "完成了什么",
  "acceptance_results": [
    {"criterion_index": 0, "passed": true, "evidence_refs": ["..."]}
  ],
  "artifact_refs": ["..."],
  "risks": [],
  "requested_action": "review"
}
```

v1 把 completion report 保存为现有 Issue 评论中的 typed part，并关联 `run_id`，不新增独立产品实体。
提交和 Issue `in_review` 状态变化必须原子完成。

普通低风险 Issue 可以由群管依据 report 验收；发布、集成和 Goal 最终完成必须由不同于实现 Run 的 reviewer 或人类验收。

### I8. Goal completion 只看 Goal scope

Goal admission 和 completion 只查询 `issue.channel_goal_id = goal.id` 的 Issue，不再用"整个项目所有 Issue"代替 Goal scope。

Goal 完成至少要求：

- 所有 `goal_required=true` 的 Goal Issue 为 `done`；
- 没有 `todo / in_progress / in_review / blocked` Goal Issue；
- `cancelled` Issue 只有在同一次带 reason 的 Goal version 更新中被设为 `goal_required=false` 后才不阻塞，
  不能默认为成功；
- 所有 Goal success criteria 有证据；
- 必要的复核、集成、发布 Gate 已通过；
- 群管写入最终 checkpoint 和来源频道总结。

## 6. 群管 controller

### 6.1 路由规则

普通频道继续使用现有 mention / attention 规则。存在 active Goal 时改为：

| 输入 | 唤醒规则 |
|---|---|
| 人类无 `@` 的频道消息 | 只 execute 群管；其他 Agent 只 observe |
| 人类明确 `@Agent` | execute 被点名 Agent；群管 observe，并在需要更新计划时收到 controller event |
| 人类 `@all` / "大家" | 先 execute 群管，由群管拆 Issue；不直接全员完整执行 |
| Issue assignment / dependency 解锁 | 只唤醒该 Issue 的负责人 |
| Run / runtime / Gate 状态变化 | 唤醒 controller，不在群里广播全员 |

Worker 的"收到 / 对齐 / 确认"不是独立交付，默认不得公开发送。服务端 public response grant 对 Goal 频道同样生效。

### 6.2 controller 触发

controller 是服务端状态机，不是一个永不结束的模型会话。以下事件写入 durable controller event：

- Goal 创建、更新、pause、resume；
- Goal 频道收到人类消息；
- Goal Issue 创建、状态变化、completion report、dependency 解锁；
- Run 排队、开始、成功、失败、超时；
- 相关运行时上线、离线或容量变化；
- reviewer verdict 或人工 Gate；
- 安全定时器发现 Goal 长时间无状态变化。

同一个 Goal 同一时刻最多一个 controller tick。复用现有 `goal_execution_epoch` 作为内部 tick / lease / audit，
不向用户新增 Epoch 概念。

### 6.3 controller 每次做什么

先执行不调用模型的 deterministic preflight：

1. 读取 Goal version、Goal Issue 树、依赖、Run、运行时和 Gate；
2. 修复可确定的不一致，例如 runnable-without-run、终态 Run 仍显示 `in_progress`；
3. 若所有状态正常且没有新事件，记录 `no_change` 后退出；
4. 只有需要拆分、重规划、取舍、复核或总结时，才启动群管 Agent Run；
5. 群管返回 typed decision，服务端校验 version、权限和 invariant 后执行；
6. 写 checkpoint、下一个唤醒条件和最晚复查时间，然后结束本次 tick。

允许的 decision：

```text
DECOMPOSE | DISPATCH | REASSIGN | REQUEST_REVIEW | INTEGRATE
REPLAN | WAIT | ASK_HUMAN | COMPLETE_GOAL | STOP
```

`WAIT` 必须包含 `wait_reason`、`wake_on[]` 和 `review_at`。不能只说"等待中"。

### 6.4 群管的拆分合同

群管收到长期目标或新阶段时：

1. 保留 Goal 的长期 objective；
2. 创建一个阶段 Parent Issue；
3. 拆成可独立验收的叶子 Issue；
4. 给每个叶子写 acceptance criteria；
5. 写 dependency，只让 frontier Issue 进入 `todo`；
6. 按能力分配负责人，并按真实 session capacity dispatch；不为制造并行而强制不同负责人；
7. 创建明确的复核 / 集成 Issue，并依赖所有 required 产出；
8. 原子提交 Issue DAG，再由 reconciler 同时 dispatch frontier。

`DecomposeIssue` 输入必须增加每个 node 的 `acceptance_criteria`，不得继续固定为 `[]`。

群管不实现它已经委派的同一 deliverable。没有合适负责人时，群管应 `ASK_HUMAN` 或明确自己接管并改变 assignee，
不能在聊天里口头接活但让 Issue 仍显示别人负责。

## 7. 断线、失败与恢复

### 7.1 运行时离线

运行时离线后：

- dispatcher 停止向它 claim 新 Run；
- running Run 进入 recoverable waiting / pending，并保留原 Run attempt 记录；
- Issue 投影为 `waiting_runtime`，Goal 不再显示 `ready`；
- 运行时在恢复窗口内重连时，从 durable Run / inbox 恢复；
- 超过恢复窗口后，controller 评估换运行时、换负责人或请求人类；
- 若整个 Goal 只有一个故障域，在线时显示 `single_runtime_failure_domain` resilience warning；
  该运行时离线后进入 waiting / degraded 并显示 `degraded_single_runtime`，不能静默等待。

### 7.2 群管离线

群管运行时离线不能让服务端 controller 消失。deterministic preflight、状态修复、等待原因和告警继续工作。
需要模型判断但群管不能运行时，Goal 进入 `waiting_manager_runtime` 并生成 Needs You；不能伪装成"仍在执行"。

### 7.3 安全扫描

事件触发是主路径，周期扫描只是兜底。建议：

- 每 60 秒扫描 runnable-without-run、in-progress-without-run 和过期 claim；
- active Goal 连续 10 分钟无状态变化时触发一次 controller preflight；
- 连续 `no_change` 使用指数退避，最长不超过 1 小时；
- 有新消息、Run 终态、运行时变化或 Gate 变化时立即取消退避并触发。

定时器是唤醒机制，不是任务真相。真实状态始终来自 Goal、Issue、Run 和运行时。

## 8. Admission

`channelGoalExecutionAdmission` 改成结构化结果，而不是一个过于宽松的字符串：

```json
{
  "status": "ready | planning | waiting | degraded | blocked",
  "reasons": ["runtime_offline", "acceptance_missing"],
  "runnable_issue_count": 3,
  "active_run_count": 2,
  "online_agent_count": 3,
  "online_execution_slot_count": 2,
  "distinct_runtime_count": 2,
  "next_action": "reassign LRM-1590"
}
```

`ready` 至少要求：

- Goal 有明确 scope；
- Goal Issue 拆分和 dependency 合法；
- runnable 叶子均有负责人和 acceptance criteria；
- 每个 runnable Issue 有 Run 或 dispatcher intent；
- 至少一个在线执行槽；
- 声称并行时，在线执行槽数量至少为 2；
- 复核 / 集成路径存在；
- 没有未知等待原因。

`distinct_runtime_count` 只表示故障域数量，不参与并行度计算。同一运行时可以贡献多个在线执行槽；
单运行时 Goal 可以是 `ready` 且正在并行，同时因单故障域标记 resilience warning。

项目和 Git 绑定只在代码交付 Goal 中要求，不应成为所有 Goal 的通用硬编码条件。

## 9. API 与存储变化

### 9.1 数据库

最小新增：

```sql
ALTER TABLE issue
  ADD COLUMN channel_goal_id uuid NULL REFERENCES channel_goal(id),
  ADD COLUMN goal_required boolean NULL,
  ADD CONSTRAINT issue_goal_scope_consistent CHECK (
    (channel_goal_id IS NULL AND goal_required IS NULL) OR
    (channel_goal_id IS NOT NULL AND goal_required IS NOT NULL)
  );

CREATE INDEX issue_channel_goal_idx
  ON issue(workspace_id, channel_goal_id, status)
  WHERE channel_goal_id IS NOT NULL;
```

另外增加或确认：

- non-terminal Issue Run 的唯一约束；
- durable dispatch outbox / intent 的幂等键；
- completion report typed comment part 必须关联 Issue、Run 和提交者；
- controller event / epoch 的幂等键和 Goal lease；
- 所有新增表或字段均提供 down migration。

如果现有 `agent_inbox_event` 状态语义无法直接建立 partial unique index，应增加一个由 task service 维护的
`active_issue_execution` claim 表；它只做唯一 claim，不成为新产品实体。

### 9.2 内部服务

新增单一 domain service：

```go
type IssueExecutionReconciler interface {
    ReconcileTx(ctx context.Context, tx pgx.Tx, issueID pgtype.UUID, trigger Trigger) error
}
```

所有 Issue 状态、负责人、dependency 和运行时变化经该 service 处理。

现有 `shouldEnqueueAgentTask`、`isAgentAssigneeReady` 和多个 handler 内的特殊 enqueue 分支逐步删除，
避免旧路径和新路径双写。

### 9.3 公共 API

v1 不要求新增普通 Issue 状态 API。现有 create / patch / batch / decompose API 保留，内部改走统一事务服务。

Agent 完成使用语义化命令，避免"发评论"和"改状态"两次调用中途断开：

```text
POST /api/agent/issues/{issueId}/completion
```

请求包含 completion report 和 expected Issue / Goal version；成功后原子写 typed comment、终结 Run、
把 Issue 推进到 `in_review` 并触发 controller。

### 9.4 UI

用户默认只看：

- Goal：长期目标、当前阶段、总体进度、当前阻塞、下一步；
- Issue：负责人、状态、依赖、验收条件、最新 Run；
- 团队：哪些 Agent / 运行时在线，实际并发几个；
- Needs You：必须由人处理的 Gate 或没有可用运行时。

Work Graph、epoch、outbox、lease 和完整事件流只放高级详情 / 审计，不进入普通用户主流程。

## 10. 现有复杂对象如何处理

| 现有对象 | v1 处理 | 原因 |
|---|---|---|
| `channel_goal_subgoal` | 标准 Goal 停止新建；迁移成 Parent Issue 后只读 | 与 Parent Issue 重复 |
| Work Graph | 标准 Goal 不写；UI 从 Issue parent / dependency 派生 | 避免两套 DAG 漂移 |
| Goal Process | 保留为可选审计 / 方法说明，不参与调度 | 不能成为续跑条件 |
| `goal_execution_epoch` | 内部复用为 controller tick / lease | 不新增用户概念 |
| Reminder | 只做兜底唤醒和通知 | 不是长期状态真相 |
| `work_owner_lease` | 标准 Issue 路径由 Run claim 取代，确认无其他消费者后删除 | 同一 Issue 两套所有权重复 |
| `collaboration_session` | 只服务需要发言轮次的会话 | 不参与 Issue 执行 DAG |
| Research Work Graph | 暂保留给 advanced profile | 不阻塞标准路径收口 |

迁移期禁止长期双写。一个 Goal 一旦切到 Issue canonical 模式，Subgoal / Work Graph 只读投影，不得反向改 Issue。

## 11. 实施顺序

### Slice A：先止住"断了"

1. 提取 `IssueExecutionReconciler`；
2. 覆盖所有 non-runnable → runnable transition，特别是 `cancelled / blocked / done → todo`；
3. Issue 变化与 dispatch intent 同事务；
4. readiness 检查运行时在线、Computer 心跳和执行槽；
5. 增加恢复扫描和 runnable-without-run 告警；
6. 修复 batch update、dependency unlock 和 runtime reconnect 的同类路径。

这一阶段不需要先改 UI，也不需要先迁移 Work Graph。

### Slice B：让协作真的并行

1. 给 Issue 增加 `channel_goal_id`；
2. `DecomposeIssue` 接受并校验 acceptance criteria；
3. Goal scope 改为显式 Goal Issue，不再扫整个项目；
4. active Goal 频道启用 manager-first routing；
5. admission 展示真实在线槽、active Run 和 distinct runtime；
6. 群管按槽分配 frontier Issue，并创建复核 / 集成 Issue。

### Slice C：让群管持续推进

1. 建 durable controller event 和单 Goal lease；
2. 复用 `goal_execution_epoch` 实现 bounded tick；
3. 接入 Issue / Run / runtime / Gate 事件；
4. 实现 deterministic preflight、typed decision 和退避；
5. 实现 completion command、checkpoint 和来源频道总结；
6. 增加 waiting / degraded / Needs You 状态。

### Slice D：删掉重复主路径

1. 标准 Goal 停止创建 Subgoal / Work Graph；
2. 迁移现有 active Goal 的 Subgoal 为 Parent Issue；
3. Work Graph 改成只读派生或仅 advanced profile 可写；
4. 移除标准 Issue 对 `work_owner_lease` 的依赖；
5. 删除旧 enqueue 分支、双写和只靠 Prompt 的 continuation 规则。

每个 Slice 独立上线并可回滚。禁止先做一次全库大重构再一起切流。

## 12. 必须见红的验收测试

### 12.1 重开与重试

- cancelled Run 的 Issue 从 `cancelled → todo`，必须创建不同 ID 的新 Run；
- blocked Issue 的最后一个 dependency 完成，必须自动产生 dispatch intent；
- batch update 和单 Issue update 行为一致；
- Agent 在当前 Run 内重开同一 Issue，不产生无限自循环，也不丢下一次 Run；
- 事务在 Issue 更新后、dispatcher 前崩溃，重启后 outbox 能补发且只发一次。

### 12.2 运行时

- 只有 `runtime_id`、但 runtime offline 的 Agent 不得通过 ready；
- Computer 心跳过期时 admission 变为 waiting / degraded；
- runtime reconnect 后 runnable Issue 自动恢复，不需要人发送"继续"；
- 一个 session capacity 为 3 的共享运行时可以贡献三个执行槽；
- 同一运行时上的两个独立 Issue session 可以产生时间窗口重叠的 running Run；
- manager 与所有 Worker 共用的运行时离线时，Goal 明确显示 `degraded_single_runtime` 和 Needs You。

### 12.3 并行与依赖

- 两个无依赖 Issue、两个在线槽，提交分解后产生两个并发 Run；
- 两个 Issue 分给同一运行时上的两个 Agent，可以通过两个独立 session 形成两个活跃执行槽；
- 下游 Issue 在所有 required dependency `done` 前不能产生 Run；
- acceptance criteria 为空的叶子 Issue 不能 dispatch；
- controller 不能自己实现已经分配给 Worker 的同一交付。

### 12.4 Goal 与群聊

- active Goal 频道的人类无 `@` 消息只 execute 群管；
- Worker 未获得 public response grant 时不能发送重复确认；
- 群管一个模型 Run 结束后，后续 Issue terminal event 仍能触发新 tick；
- Goal checkpoint 更新不覆盖长期 objective；
- 项目中无关开放 Issue 不阻塞 Goal 完成；
- Goal 内 cancelled required Issue 不得被当成成功；
- 没有 completion report / evidence 的 Agent Issue 不能从执行直接变成 `done`；
- 最终 Goal completion 必须返回来源频道。

每项回归必须先在旧实现上见红，再在新实现上变绿；仅写 happy-path 测试不算完成。

## 13. 监控与成功指标

新增指标或等价可查询事件：

- `issue_runnable_without_run_total`：目标恒为 0；
- `issue_in_progress_without_run_total`：Agent Issue 目标恒为 0；
- `issue_dispatch_recovery_total{trigger}`；
- `goal_controller_tick_total{decision,outcome}`；
- `goal_stalled_seconds`；
- `goal_online_execution_slots` 和 `goal_distinct_online_runtimes`；
- `goal_human_continue_prompt_total`：人类靠"继续"恢复任务的次数；
- `channel_ungranted_agent_response_total`；
- 从 runnable 到 Run claimed 的 P50 / P95；
- runtime 断线后自动恢复成功率和恢复耗时。

上线成功标准：

- runnable-without-run 连续 14 天为 0；
- 运行时短暂断线后，无需人类"继续"即可恢复；
- UI 声称并行时，后端能观察到至少两个并发 Run；
- active Goal 无状态变化时始终有明确 wait reason 或下一次检查时间；
- Goal completion 全部可追溯到 scoped Issue 和 evidence；
- Goal 频道重复确认消息显著下降。

## 14. 已确认决定与剩余默认项

### 14.1 已确认（2026-08-24）

1. **active Goal 频道的人类无 `@` 消息只唤醒群管。**
   明确 `@Agent` 仍直达该 Agent，Issue 唤醒不受影响。

2. **多个 Agent 共用一个运行时时，各自的 session 可以算并行。**
   是否并行看 session / Run 是否被并发执行，不看 distinct runtime；distinct runtime 只衡量容灾。
   同一个 Agent 能否多 session 并发不在本次决定范围内。

3. **Agent 做完叶子 Issue 后不能直接 `done`。**
   先提交 completion report 并进入 `in_review`；低风险项由群管快速验收，发布 / 集成由独立 reviewer 或人验收。

### 14.2 未单独确认，实施评审前按默认建议

4. **标准 Goal 是否继续创建 Work Graph？**
   默认建议：不创建。标准路径只写 Issue tree / dependency；高级 Research profile 暂时保留现有 Work Graph。

5. **Goal 是否按 Phase 新建？**
   默认建议：不按 Phase 新建。Phase 使用 Parent Issue；只有人类长期 objective 真正结束或改变为另一个目标时才结束 / 新建 Goal。

6. **单运行时团队是否禁止启动 Goal？**
   默认建议：不禁止；在线时可以是 `ready` 且并行，但显示 resilience warning，并明确无法容忍该运行时故障。
   需要"可持续自治"承诺时，至少要求两个不同故障域。

## 15. 与 LoopX 的关系

本规格借鉴 LoopX 的三条原则：Goal 是 durable object、执行器是可替换的 bounded turn、kernel 持有
claim / gate / evidence / recovery / scheduling 真相。Multica 不复制 LoopX 的本地 Todo 文件模型，
因为 Multica 已有适合人机协作的 Issue、负责人、评论和项目对象。

对应关系：

| LoopX | Multica v1 |
|---|---|
| durable goal | Channel Goal |
| todo | Issue |
| executor turn | Run / `agent_inbox_event` |
| claim / lease | non-terminal Run claim |
| gate / evidence | acceptance criteria + typed completion report + review |
| monitor / recovery | Goal controller + reconciler + runtime events |

真正需要复制的是"状态属于 kernel，不属于聊天会话"的边界，而不是再增加一批名称相似的实体。

## 16. 与既有规格的覆盖关系

本规格若获批准，对标准团队 Goal 覆盖以下既有口径：

- `docs/team-collaboration-self-evolution.zh-CN.md` 中"群管不是每条消息入口"：
  普通频道仍成立；active Goal 频道改为 manager-first；
- `2026-08-07-goal-gated-work-graph-simplification-spec.zh-CN.md` 中标准 Goal 必须拥有 canonical Work Graph：
  改为 Issue tree / dependency canonical，Work Graph 只读派生或限高级 profile；
- `channel_goal_subgoal` 作为 Goal 拆分对象：改由 Parent Issue 承担；
- Reminder / Prompt 负责 continuation：改由服务端 controller event 和安全扫描负责。

在产品 owner 批准前，这些仍是提案，不应把本文描述成已经上线的可执行契约。批准后需当天更新
`docs/engineering-principles.md` 的索引和对应内置 skill source map，避免文档继续教旧路径。
