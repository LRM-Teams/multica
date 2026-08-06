# Multica 长程探索与 Auto Research 最小实施计划

> 状态：Draft，等待架构评审
>
> 日期：2026-08-06
>
> 对应规格：`docs/superpowers/specs/2026-08-06-long-horizon-control-plane-memory-skill-evolution-spec.zh-CN.md`

## 1. 目标

先完成一个可验证的长程探索闭环，不建设完整自进化平台：

```text
Research Run Task / Issue Node
→ stable Agent Job / bounded Turn
→ Trial Result / Artifact
→ Evidence Receipt
→ Kernel Decision
→ effective completion / 下一步或停止
```

普通聊天、不受管 Issue 和现有 Research Run 的专用研究语义保持不变。

第一阶段不重建 Goal、Work Graph、Research scheduler 或 agent inbox。现有 `work_node / waits_on`、Research Task/Attempt、reconcile lease、Gate、event ledger 和 inbox delivery 是默认复用路径；新增范围限定为受管完成裁决、Evidence Receipt、Decision 及必要的 projection fence。

## 2. 开工 Gate

实现前用一个 ADR 锁定：

- Goal、Graph frontier、Node completion、Issue projection 的唯一 owner；
- Research Run 与 Kernel 的 command/committed-event 边界；
- Job identity 复用或演进现有 agent task/inbox 的方式；
- writer epoch、cutover、rollback 和旧 writer fail-closed 行为；
- 首个 Trial 类型、verifier、预算与 authority envelope。

Gate 未完成前不增加生产 writer。

ADR 默认起点如下，只有 writer inventory 给出相反证据时才改变：

```text
Research Task        → 首切片 Node
Research Attempt     → 首切片 Turn
agent inbox delivery → 执行载体与 lease fence
research_run_event   → committed event / decision ledger
```

不把创建通用 `agent_job` 表作为首切片前置条件。

## 3. Phase 0：盘点并锁住现有事故边界

### 3.1 Writer inventory

- [ ] 列出所有写 Issue `done/cancelled` 的入口；
- [ ] 列出所有把 work node 置为 terminal、resolve `waits_on` 的入口；
- [ ] 列出 Channel Goal completion、Research Run claim/replan/complete 的入口；
- [ ] 标注 canonical writer、projection、scheduler bookkeeping 和 UI operation；
- [ ] 记录现有 task/inbox、lease、idempotency、usage 和 checkpoint identity。

### 3.2 先写失败测试

- [ ] Graph-managed Issue 通过旧 API 写 `done` 不得直接释放依赖；
- [ ] 旧 lease/fence 的迟到结果不得覆盖新 owner；
- [ ] 相同 idempotency key + 相同 payload 返回原决定；
- [ ] 相同 key + 不同 payload 被拒绝；
- [ ] Research Run scheduler 与 shadow Kernel 不产生双 claim 或双 terminal write。

验收：能明确回答每项 terminal state 当前由谁写，以及新 Kernel 将封锁哪条旧路径。

### 3.2 Managed/unmanaged fixture

- [ ] 建立一个由 `kernel_evidence` 管完成权的 Issue/Node 依赖链；
- [ ] 建立一个继续由 `issue_status` 管完成权的普通 Issue/Node 依赖链；
- [ ] 固定受管 Issue `done` 不释放下游、普通 Issue `done` 行为不变；
- [ ] fence 必须位于共享 terminal/resolve 边界，不能只覆盖单个 API handler。

### 3.3 ADR 与 shadow 基线

- [ ] ADR 记录 writer ownership、现有对象映射、managed policy、revoke 语义和回滚方式；
- [ ] shadow Kernel 对同一输入计算 Decision，但不写 Node、Issue、依赖或 Goal；
- [ ] 记录旧决定、新决定、contract revision、Evidence 和分歧原因；
- [ ] shadow 不得产生 dispatch、claim、cancel 或其他外部效果。

验收：可重复回放同一输入并得到同一 shadow Decision，且普通生产行为完全不变。

## 4. Phase 1：复用现有 Task/Attempt，稳定 Writeback

首个 Research 切片优先把 Research Task 作为 Node、Research Attempt 作为 Turn，并复用 inbox delivery fence 和 research event 幂等。只有契约测试证明这些 identity 无法满足 anchor revision、重放或 rollover，才增加 Job 对象。

- [ ] 定义最小 `JobAnchor`：kind、id、revision；
- [ ] 定义版本化 `TurnWritebackProposal`；
- [ ] writeback 校验 lease/fence、anchor revision、idempotency key 和 payload digest；
- [ ] 服务端生成 `CONTINUE / WAIT / ASK_HUMAN / RETRY_OPERATION / STOP`；
- [ ] session rollover 和 runtime migration 保留相同 Research Task/Node identity；
- [ ] 未知外部操作结果进入 readback/reconciliation，不盲目重放；
- [ ] 记录实际 usage；首个切片只需 hard turn/run limit，不先建设通用多层预算账本。

验收：response loss、重复提交、session rollover 和旧 owner 回写不会产生重复 canonical effect。

## 5. Phase 2：最小 Completion 与 Evidence

- [ ] 为受管 Node 增加或计算 `execution / validity / review / effective_completion`；
- [ ] worker succeeded 只进入待核验，不直接 resolve dependency；
- [ ] 实现 immutable Artifact reference；
- [ ] 实现最小 Evidence Receipt：subject、policy digest、input/artifact digests、verdict、covered/uncovered checks；
- [ ] 支持 active、stale、revoked、superseded；
- [ ] Graph → Issue 使用唯一 projection writer；
- [ ] 旧 Issue `done` 对受管 Node 变为完成请求或被拒绝；
- [ ] Evidence revoke 后停止下游准入和 Goal completion。

受管与不受管行为必须显式分离：普通 Issue 保持当前 `done → terminal → resolve waits_on`；受管 Node 只有 `effective_completion = satisfied` 才能释放依赖。

第一阶段不实现完整全图影响分析。Evidence revoke 后，前置 Node 立即 revoked，未启动的直接下游恢复等待，已运行或已完成的直接下游标 stale，Goal completion 重新计算；不自动删除产物，也不递归取消运行中工作。

验收：只有当前 revision 的有效 Evidence 和必要 Gate 才能使 Node satisfied。

## 6. Phase 3：一个 Auto Research 垂直切片

### 6.1 合同与 Trial

- [ ] 实现 immutable `DecisionContract`，避免与现有 `researchrun.ResearchContract` 同名冲突；
- [ ] reference set 可选，不强制 metric、dataset、split 或排行榜；
- [ ] 实现最小 Trial Spec：contract、question/hypothesis、changed axes、method、authority、cost、stop condition；
- [ ] domain-specific 内容放入 versioned adapter envelope；
- [ ] 实现 Trial Result 的 attempt status、validity、outcome、observations、artifacts 和 failure class；
- [ ] 支持 `supported / rejected / mixed / inconclusive`；
- [ ] 区分 hypothesis rejected、trial invalid 和 operational failure。

### 6.2 接入现有 Research Run

- [ ] 选择一个现有 Research Task 作为首个 profile adapter；
- [ ] 仅可讨论、指派或审核的工作单元绑定 Issue，内部 attempt 不创建 Issue；
- [ ] Research Run 保留 question、claim、source、synthesis 等专用数据；
- [ ] Research Run 通过 command 提交 Trial/Node proposal，通过 committed event 消费 Kernel Decision；
- [ ] 结果列表只读取 committed Trial Result；
- [ ] shadow 比较旧 Research Run 决策和 Kernel 决策，不双写。

### 6.3 下一步与停止

- [ ] 首版 Planner 只生成 `CONTINUE / REPLAN_NEW_AXIS / ASK_HUMAN / STOP_*` proposal；
- [ ] Planner 只读取 committed result、有效 Evidence、成本和谱系；
- [ ] 无信息增益、预算耗尽、合同失效或需要扩权时停止；
- [ ] 不实现通用 candidate promotion、canary 或 deployment。

端到端验收：

```text
Research Task 绑定 Issue/Node
→ Agent 执行
→ Trial Result + Artifact
→ verifier receipt
→ effective completion
→ Issue 投影
→ 一个下游 Node 被释放
```

并通过 duplicate、late owner、old revision、legacy done 和 evidence revoke 测试。

## 7. Rollout

1. 本地契约测试；
2. 单 workspace shadow decision；
3. 内部 workspace writer-epoch canary；
4. 一种低风险 Research Trial；
5. 稳定后再评审是否扩展到其他 Trial 和普通 Graph-managed Issue。

立即停止扩量的条件：

- 出现双 writer 或双 claim；
- 旧 lease 或旧 revision 推进新状态；
- stale/revoked Evidence 释放下游；
- 非幂等外部效果被盲目重放；
- rollback 无法恢复单 writer；
- 无法从 Decision 追溯 Goal、Node、Job、Turn、contract 和 Evidence。

## 8. 明确后置

以下内容不进入本计划：

- 全量重写 Research Run；
- 通用 budget reservation/ledger 平台；
- 完整 transactional outbox 平台；
- 全图自动最小重算；
- Memory 自动晋升；
- Skill Candidate、Skill canary 和自动部署；
- 通用排行榜；
- 多领域 adapter 一次性设计；
- 无人值守递归自进化。

若首个切片证明需要，再分别立项，不提前塞入 Kernel。

## 9. 提交切片

1. ADR + writer inventory + managed/unmanaged fixtures + failing contract tests；
2. Research Task/Attempt anchor + writeback + idempotency/fence；
3. effective completion + Issue `done` fence；
4. Artifact + Evidence Receipt + revoke；
5. Decision Contract + Trial Spec/Result；
6. 一个 Research Task adapter + shadow decision；
7. writer-epoch canary + rollback proof。

每个切片独立评审，包含迁移边界、回滚方法和定向测试。

## 10. 完成定义

- 一个 Research Task 在 crash、rollover 和迟到结果下保持同一业务身份；
- Agent 自述、Issue `done` 和旧 scheduler 不能提前释放依赖；
- 重复 writeback 不产生重复 canonical effect；
- Trial Result 可追溯到固定合同、输入、Artifact 和 verifier；
- Evidence stale/revoke 后 completion 和下游准入立即失效；
- 自动循环在无增益、预算、合同或权限边界明确停止；
- 普通聊天和不受管 Issue 没有行为变化。
- 首切片没有重复创建 Goal、Work Graph、Research scheduler、Job 或 inbox 执行体系。
