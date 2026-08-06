# Multica 长程探索与 Auto Research 核心规格

> 状态：Draft，供产品与研发评审
>
> 日期：2026-08-06
>
> 本文只定义最小核心，不授权修改数据库、API、权限或生产行为。

## 1. 一句话决定

Multica 在现有 Goal、Issue、Agent Runtime、Work Graph 和 Research Run 之上补齐一个服务端拥有的长程执行内核：目标被拆成可持续推进的工作单元，Agent 分轮执行并提交 Artifact、Observation 和 Evidence，服务端据此决定继续、等待、重试、请求人工处理或停止。

普通问答和一次性任务继续走现有快路径，不创建 Goal、Graph 或额外验证流程。

### 1.1 复用决定

第一阶段不新建第二套 Goal、Work Graph 或 Research scheduler。现有能力继续作为执行骨架：

- Channel Goal 继续承载已有目标产品语义；
- `work_node` 和 `waits_on` 继续承载 Issue 图及依赖；
- Research Run 的 Task、Attempt、reconcile lease、Gate 和 event ledger 继续承载首个 research profile；
- `agent_inbox_event` 和 delivery lease 继续作为实际执行与 fencing 边界。

Kernel 第一阶段只补一层服务端完成裁决：区分受管与不受管 Node，读取 Evidence 和 Gate 计算 `effective_completion`，并控制受管依赖何时释放。只有现有 identity 或事务边界无法满足验收时，才增加新的领域对象或表。

## 2. 核心模型

```text
Goal
  → Work Graph
    → Node ↔ Issue（仅可协作工作单元）
      → Agent Job
        → Turn / Attempt
      → Artifact / Observation / Evidence
      → Decision / Gate
  → 下一轮探索或停止
```

核心含义：

- **Goal**：目标、范围、验收条件、预算、权限和停止条件；
- **Work Graph**：带版本的工作单元及真实依赖；
- **Issue**：人和 Agent 用于讨论、指派、阻塞和审核的产品对象；
- **Agent Job**：对一个工作单元的持续承接，不绑定某次 provider session 或 runtime；
- **Turn / Attempt**：一次有界执行；
- **Artifact / Observation / Evidence**：执行产物、观察和经核验的支持材料；
- **Decision / Gate**：由服务端决定是否推进、等待、重试、停止或请求人工处理。

聊天历史和 provider session 不是长程状态真相。

## 3. 四条硬边界

### 3.1 只有一个控制面

Goal completion、Graph frontier、Node completion 和依赖释放只能有一个 canonical writer。Issue、看板、报告、结果列表和可选排行榜都是投影或操作入口，不自行推进状态。

现有 Channel Goal 和 Research Run 不得与新 Kernel 双写同一事实。正式实现前必须通过 ADR 锁定：

- 每项 canonical state 的唯一 owner；
- Research Run 向 Kernel 提交 command/committed event 的边界；
- writer epoch、cutover、回滚和旧 writer fail-closed 行为。

### 3.2 执行成功不等于完成

Agent 自述完成、命令退出码为零或文件存在，只能说明一次执行结束。Node completion 必须读取当前 revision 的验收条件、有效 Evidence 和必要 Gate。

状态至少区分：

```text
execution: pending | running | succeeded | failed | cancelled
validity:  unknown | valid | stale | revoked
review:    unreviewed | accepted | rejected | blocked
effective_completion: satisfied | pending | stale | revoked
```

下游依赖、Goal completion 和交付只读取 `effective_completion`。

### 3.3 长任务由有界 Turn 组成

Job identity 跨 comment wake、continue、session rollover、runtime 迁移和可恢复故障保持稳定。每个 Turn 都以结构化 writeback 结束，至少绑定：

```text
job_id / turn_id
anchor_id / anchor_revision
lease_or_fence
reported_outcome
artifact_refs / observations
requested_action
usage
idempotency_key / payload_digest
```

服务端校验后生成 Decision。迟到 owner、旧 revision 或冲突 payload 不能推进 canonical state。

### 3.4 自动化不能扩大权限，也必须能停止

Memory、Skill、历史成功或 Agent 输出不能授予新权限。部署、生产写入、不可逆操作和权限扩大继续使用当前 policy 或人工 Gate。

任何自动循环都必须受预算、时间、节点数、并发、重试和无进展上限约束。

## 4. Issue 与 Work Graph

Issue 继续是人和 Agent 的主要协作面，但不要求每个内部 attempt 都创建 Issue。

第一阶段规则：

- Node 必须显式区分 `issue_status` 与 `kernel_evidence` 两种 completion authority，或提供语义等价的 managed policy；
- 可被人工讨论、指派、阻塞、接管或审核的 Node 必须绑定 Issue；
- 机械验证、内部重试和 scheduler maintenance 可以只作为 Job/Attempt 存在，并投影到所属 Issue；
- `parent_issue_id` 只表达组织关系；Graph Edge 才表达执行依赖；
- Graph → Issue 是默认投影方向；
- 人或旧 API 把 Graph-managed Issue 写为 `done` 时，只能形成完成请求，不能直接释放依赖。

当前仓库已有的 `Issue done/cancelled → work node terminal → waits_on resolved` 路径必须在新 Kernel 接管前加 fence。

对于不受管 Node，现有 Issue 状态驱动行为保持不变。对于受管 Node，Issue `done` 只是 completion request；只有当前 revision 的 `effective_completion = satisfied` 才能把 Node 投影为完成并释放依赖。该判断必须落在共享的 terminal/resolve 边界，不能只封锁某一个 HTTP handler。

## 5. Evidence 与失效

最小 Evidence 合同：

```text
EvidenceReceipt
  receipt_id
  subject_ref
  verifier_kind / verifier_policy_digest
  input_and_artifact_digests
  verdict
  covered_checks[] / uncovered_checks[]
  validity: active | stale | revoked | superseded
```

Evidence 必须可追溯到固定输入和 Artifact。输入、Artifact、关键环境或 verifier policy 变化时，旧 receipt 不能自动满足新 revision。

第一阶段只要求：

1. immutable Artifact reference；
2. verifier receipt；
3. stale/revoke；
4. 失效后停止下游准入。

复杂的全图影响分析和自动最小重算可以后置。

第一阶段的 revoke 采用直接依赖保守语义：前置 Node 立即变为 `revoked`；尚未启动的直接下游恢复等待；已运行或已完成的直接下游标为 `stale`，不删除历史产物；Goal completion 重新计算。递归影响分析和自动取消运行中工作后置。

## 6. Exploration / Auto Research Profile

Auto Research 是统一 Kernel 上的 execution profile，不建立独立 scheduler、任务系统或结果真相。它适用于资料研究、代码实现与 benchmark、反证与复现、产品原型、数据处理、工作流优化、模型实验等受控探索。

现有 Research Run 保留其 questions、claims、sources、research tasks、synthesis 和 research-specific evidence 语义；通用 Kernel 只拥有跨领域的 Goal、预算、权限、Graph frontier、Gate 和 effective completion。

### 6.1 Decision Contract

为避免与现有 `researchrun.ResearchContract` 冲突，Kernel 使用通用 `DecisionContract`：

```text
DecisionContract
  contract_id / immutable_revision
  objective_scope
  evaluation_questions[]
  acceptance_rules[]
  reference_set[]              # 可选
  input_snapshot_digest
  method_policy
  verifier_policy_digest
  guardrails[]
  uncertainty_policy
```

`reference_set[]` 可以是既有方案、控制组、事实快照、benchmark、需求约束或绝对验收标准，也可以为空。metric、dataset、split、seed、AUC 等只属于具体 domain adapter。

需要相对比较时，只有合同、reference、输入和执行环境相容的结果才能形成正式比较。合同关键字段改变时创建新 revision，不得事后更换成功标准。

### 6.2 Trial

每个受控探索使用 immutable Trial Spec：

```text
TrialSpec
  trial_id / trial_kind
  decision_contract_digest
  parent_or_claim_ref
  changed_axes[]
  method
  assumptions[]
  hypothesis_or_question
  expected_information
  required_tools / authority
  cost_estimate / stop_condition
  adapter_kind / adapter_schema_version / adapter_payload_digest
```

单轴 Trial 可以支持局部归因；多轴 Trial 只能形成组合结论，除非另有消融 Evidence。Kernel 不强制所有研究伪装成单变量实验。

执行者提交结果，verifier 决定能否 committed：

```text
TrialResult
  trial_id / decision_contract_digest
  attempt_status: succeeded | failed | cancelled | lost
  validity: valid | invalid | stale | revoked
  outcome: supported | rejected | mixed | inconclusive
  observations[] / artifact_refs[]
  measures[] / comparisons[] / uncertainty[]
  execution_cost
  failure_class
```

`measures[]` 可以是数值，也可以是覆盖、约束满足、复现结果或结构化定性 Evidence。

失败只保留三个核心分类：

- `HYPOTHESIS_REJECTED`：有效 Trial，形成负向 Evidence；
- `TRIAL_INVALID`：输入、方法、实现或验证合同不足，不能评价假设；
- `OPERATIONAL_FAILURE`：资源、超时、基础设施或 provider 故障，不产生研究结论。

### 6.3 Trial、候选和部署分开

通用 Trial 只有执行与验证生命周期：

```text
proposed → executing → verifying → committed
                         ↘ invalid / inconclusive
```

只有存在“候选方案”时才启用可选成熟度：

```text
screened → provisional → confirmed → guardrail_passed → integration_ready
```

只有存在生产 binding 时才启用部署生命周期：

```text
canary → active → rolled_back | retired
```

资料结论、事实核验和无候选排名的研究不需要进入 promotion、canary 或 active。

### 6.4 下一步与停止

Planner 只能读取 committed Trial Result、有效 Evidence、成本和已测谱系，提出下一轮 Graph revision，不能自行裁决结果或扩大 authority。

第一阶段只支持以下 Decision：

```text
CONTINUE
WAIT
ASK_HUMAN
RETRY_OPERATION
REPAIR_CONTRACT
REPLAN_NEW_AXIS
STOP_CONVERGED
STOP_NO_GAIN
STOP_BUDGET
```

结果充分、连续无信息增益、成本超过预期收益、合同失效、方法无法区分，或下一步需要扩大权限时必须停止或进入 Gate。

## 7. Memory 与 Skill 的边界

Memory 和 Skill 不是第一阶段控制面的前置依赖。

第一阶段只要求 Trial、Evidence 和 Decision 可追溯。后续可以把有效结果提取为带来源和作用域的 Memory；Skill 候选仍需独立评测、人工或 policy Gate、灰度和回滚，不能由一次成功 Trial 自动晋升。

本文不设计新的 Skill marketplace、自动部署系统或无限递归自进化。

## 8. 最小数据对象

概念上只要求：

```text
goal / graph_revision / node
agent_job / turn
artifact / evidence_receipt
decision / gate
decision_contract / trial_spec / trial_result
```

不要求每个概念立即对应新表。优先扩展或复用现有 Channel Goal、work node、agent inbox/task、Research Run 和 decision ledger；只有现有 identity、幂等或生命周期无法表达时才增加对象。

首个 Research 切片默认采用以下映射，除非 ADR 通过代码证据否决：

```text
Research Run        → profile / Goal anchor
Research Task       → Node
Research Attempt    → Turn
agent inbox delivery→ 执行载体与 lease fence
research_run_event  → committed event / decision ledger
```

第一阶段不以创建通用 `agent_job` 表为前置条件；跨 profile 的稳定 Job identity 留待首切片证明现有 identity 不足后再设计。

## 9. 首个端到端切片

第一版只证明一条路径：

```text
现有 Research Run
→ 一个可协作 Research Task 绑定 Issue/Node
→ 复用现有 Agent Task 执行
→ 提交 Trial Result 和 Artifact
→ verifier 产生 Evidence Receipt
→ Kernel 计算 effective_completion
→ 投影 Issue 状态并释放一个下游依赖
```

必须同时证明：

1. 重复 writeback 不产生重复 effect；
2. 旧 lease、旧 revision 和迟到结果不能推进状态；
3. 旧 Issue API 写 `done` 不能绕过 Evidence；
4. Evidence revoke 后，下游和 Goal completion 不再视为 satisfied；
5. 普通聊天和不受管 Issue 行为不变。

## 10. 非目标

第一阶段不做：

- 重写现有 Research Run；
- 把所有内部 Task/Attempt 变成 Issue；
- 通用排行榜或统一数值评分；
- 自动部署、自动扩权或无人值守 Skill 自修改；
- 完整 Memory/Skill Evolution 平台；
- 一次性迁移全部 Channel Goal、Issue 和历史 Research Run；
- 为所有领域预先设计 adapter 字段。

## 11. 开工前必须锁定

1. Goal、Graph、Node completion 和 Issue projection 的唯一 owner；
2. 现有 Research Run 与 Kernel 的 command/event 边界；
3. Job 是新领域对象还是现有 agent task/inbox 的稳定 identity 演进；
4. 首个 verifier、Evidence revoke 和 legacy `done` 策略；
5. 首个允许自动运行的 Trial 类型、工具、预算和 authority envelope。

这些决定必须形成 ADR。锁定前只做 writer inventory、测试夹具和 shadow decision，不增加生产 writer。

这里的交付物含义是：writer inventory 列出所有能够写 terminal/completion/dependency 的入口；测试夹具同时覆盖受管和普通 Issue；失败测试先固定旧 `done`、迟到 writeback、幂等冲突和 revoke 边界；shadow decision 只记录新旧裁决及分歧，不改变用户可见状态。

## 12. 最终判断

Multica 的目标不是让 Agent 永远运行，也不是把所有工作包装成实验，而是让一个复杂 Goal 在多次有界执行、人员协作和环境变化后，仍保持同一业务身份、可核验进度、有限预算、明确权限，并最终继续、等待或停止。
