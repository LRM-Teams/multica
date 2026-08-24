# 模型供应商凭证域调度与智能体参与门规格

> 状态：Draft，供产品与研发评审
> 与 Goal / Issue / Run、Goal Execution Graph 和 Research Genealogy 的整合修订以
> `2026-08-24-multi-agent-reliability-control-plane-integration-spec.zh-CN.md` 为准。
>
> 日期：2026-08-24
>
> 本文定义 Provider 凭证域自适应调度器和参与门（Participation Gate）的目标合同。
> 本规格不授权直接修改生产数据库、协议或运行行为；实施前需要拆成 issue，并由实现 owner
> 把对应约束升级为类型、单一入口、事务或测试。

## 1. 一句话决定

Multica 在现有 Message、Attention Round、Ambient Gate、Issue、Goal、Work Graph、Agent Runtime
和 Computer 之上增加两个互补但职责独立的控制点：

1. **参与门**在正式模型调用前决定哪些智能体需要参与，以及它应当公开回答、内部贡献、组织协调还是保持静默；
2. **凭证域调度器**对已经获准执行的模型调用按共享 Provider 凭证域排队、限并发和自适应降速，避免一批智能体同时唤醒时击穿同一账号的配额。

二者不得建立第二套消息、任务或 Work Graph 真相源。参与门只决定“是否以及以什么角色进入执行”，
凭证域调度器只决定“何时获得调用 Provider 的许可”。任务是否完成、消息是否已覆盖、Graph 是否可交付，
继续由既有 canonical owner 裁决。

## 2. 为什么要改

当前能力分别解决了部分问题，但缺少一个端到端的统一边界：

- Attention Round 能处理多个智能体都想公开回答时的选择与收敛；
- Ambient Gate 用时间窗和数量上限抑制群聊环境触发；
- 消息链路用 canonical Message、Pending、Context Boundary 和 content-free Notice 保证投递；
- 同一智能体聊天 lane 已有排他 lease 和单槽；
- Agent process cap 限制本机常驻 Provider 进程数量；
- Work Graph、Issue、work owner lease 表达可执行工作和责任归属。

这些能力尚未统一回答两个问题：

1. 一条消息或一次 Graph Delta 到来时，哪些智能体真的需要启动正式模型？
2. 多个必要智能体共享同一个 Claude、Codex 或 OpenAI 账号时，谁先调用、同时允许多少个调用、遇到 429 后怎样恢复？

缺少第一层会造成重复回答、无关智能体 full turn、纯 Agent 对话循环和 token 浪费；缺少第二层会造成并发突发、
Provider 429、集中重试和队尾延迟。单纯降低智能体数量或写死全局并发不能区分任务必要性、账号边界和配额变化。

## 3. 目标与非目标

### 3.1 目标

本规格目标是：

- 明确每个唤醒候选的参与决策及理由；
- 让确定性业务状态优先于模型判断；
- 复用现有 Attention Probe，不再增加一套平行 triage 模型；
- 同一 Provider 凭证域中的所有本机调用共享一个启动节流和总并发上限；
- 区分正式 turn、受限 probe、验证和后台压缩等 lane，同时遵守共享总配额；
- 在 429、超载和临时 Provider 故障后保留 canonical 工作，不把资源等待误判为业务失败；
- 保证同一智能体的现有串行化、Message Context Boundary 和 Work Graph 完成权不变；
- 为真实多智能体 benchmark 提供可审计的决策、排队、限流和成本数据。

### 3.2 非目标

本规格不做以下事情：

- 不替换 canonical Message、Pending、Delivery 或 Context Boundary；
- 不让参与门消费消息正文或推进已读边界；
- 不替换 Goal、Work Graph、Issue、Task 或 work owner lease；
- 不用聊天轮次 claim 表达真实工作所有权；
- 不允许调度器改写 Node、Issue 或 Goal 的完成状态；
- 不跨机器集中存储或复制 Provider 密钥；
- 首版不承诺对同一账号在多台 Computer 上实现全局精确限流；
- 不在本规格中修改 `--anyway` freshness escape hatch；该能力应由独立 Hold Capability 规格处理；
- 不通过增加场景化 Prompt 规则替代服务端状态判断。

## 4. 总体架构

```text
Canonical event
  Message / Graph Delta / Issue change / lease change
        |
        v
Participation Gate（服务端）
  deterministic rules
        |
        +-- 已确定 --> SILENT / ANSWER / CONTRIBUTE / COORDINATE
        |
        +-- 不确定 --> 既有 Attention Probe（受限 profile）
                              |
                              v
                        Attention Round
                              |
                              v
                    execution candidate
                              |
                              v
Provider Permit RPC（Runner -> Computer）
                              |
                              v
Credential Domain Scheduler（Computer）
  fairness + concurrency + adaptive pacing
                              |
                              v
                      Provider runtime call
                              |
                              v
              outcome / usage / rate-limit feedback
```

职责边界：

| 组件 | 拥有什么 | 不拥有什么 |
| --- | --- | --- |
| 服务端参与门 | 参与资格、参与角色、决策理由和唤醒候选集合 | Message 已读状态、Work Graph 完成状态、Provider 配额 |
| Attention Probe | 规则无法确定时的受限语义判断 | 工具、持久 session、公开回复和 canonical 状态 |
| Attention Round | 多个公开回答候选的收敛和 manager fallback | Provider 排队和工作完成 |
| Computer 调度器 | 本机凭证域队列、permit、并发、pacing 和 rate-limit 反馈 | Agent 工作目录、消息正文、Issue/Graph 状态 |
| Workspace Runner | 持有 Provider runtime，并在调用前申请和结束 permit | 机器级 sibling 调度策略 |
| Work Graph / Issue | 工作、责任、依赖、产物和完成裁决 | Provider 账号吞吐控制 |

## 5. 参与门

### 5.1 决策时机

以下事件可以产生参与候选，但不能直接等同于启动正式模型：

- 人类或智能体的新 canonical Message；
- DM、明确 mention、thread follower 或 ambient observation；
- Work Graph frontier、artifact revision、verification 或 Graph Delta；
- Issue 指派、状态、依赖或 completion request 变化；
- work owner lease 获得、失效、释放或 handoff；
- Goal 需要规划、重规划、汇总或人工 Gate；
- 既有 busy Notice 提示有新的 Pending Message。

参与门不读取 Provider 私有 session 作为业务真相，也不因为运行时当前空闲就推断“应该参与”。

### 5.2 统一输入

服务端为每个候选智能体构造有界、结构化的 `ParticipationContext`。目标字段如下：

```text
ParticipationContext
  workspace_id
  agent_id
  trigger_id
  trigger_kind
  source_actor_type
  source_actor_id
  channel_id / thread_id / message_id
  directed: none | dm | mention | assignment | owner_instruction
  human_attention: active | waiting | absent | unknown
  recent_participant_count
  recent_agent_only_depth
  public_answer_state: none | candidate_exists | granted | already_posted
  goal_id / graph_id / graph_version
  node_id / node_role / node_execution_status
  owns_active_work
  dependency_became_ready
  verification_required
  coordinator_required
  pending_target_count
  ambient_window_counts
  current_runtime_state
  policy_version
```

约束：

- 输入只包含服务端已经验证的身份和 canonical 状态；
- 不从消息 prose 猜测 Node、Issue、Goal 或 lease 身份；
- Message 正文只在确需 Attention Probe 时，以现有受限上下文预算提供；
- 参与判断本身不得推进 Context Boundary；
- 相同 canonical 输入和 policy version 必须得到相同的确定性规则结果。

### 5.3 统一输出

```text
ParticipationDecision
  decision_id
  decision: SILENT | ANSWER | CONTRIBUTE | COORDINATE
  reason_code
  reason_summary
  urgency: background | normal | high
  work_ref
  needs_attention_probe
  policy_version
  decided_at
```

语义固定为：

- `SILENT`：本次不启动正式模型，也不产生公开回复；
- `ANSWER`：候选智能体希望对来源 conversation 公开回答，仍需经过 Attention Round 的冲突收敛；
- `CONTRIBUTE`：执行明确的内部工作或提交产物，不自动获得公开回答权；
- `COORDINATE`：负责分解、分配、重规划、汇总或请求人工处理，不重复实现已委派交付物。

`CONTRIBUTE` 和 `COORDINATE` 必须携带可解析的 `work_ref`；普通聊天中确实没有 durable work 时，
可绑定来源 Message，但不能伪造 Issue、Goal 或 Node。

### 5.4 确定性规则优先

参与门先执行确定性规则，只有无法确定时才调用 Attention Probe。首版规则至少覆盖：

#### 必须保留的参与下限

1. 人类 DM、明确 mention 或 owner instruction 不能被 ambient 数量上限静默丢弃；目标智能体至少进入 `ANSWER`、`CONTRIBUTE` 或 `COORDINATE` 候选。
2. 智能体持有 active work owner lease，且对应 Node/Issue 已 ready 或依赖刚满足时，进入 `CONTRIBUTE`。
3. 当前唯一 verifier 的验证节点 ready 时，进入 `CONTRIBUTE`。
4. active Goal 缺少可执行 Graph，且当前智能体是 coordinator/manager 时，进入 `COORDINATE`。
5. Graph Delta 明确要求汇总、重规划或人工 Gate 时，只唤醒对应 coordinator，不广播给全队。

#### 必须保留的抑制上限

1. 已有其他智能体获得公开回答权时，无新事实或明确工作责任的智能体不得再进入 `ANSWER`。
2. 已完成且没有返工、验证或下游责任的智能体默认 `SILENT`。
3. 纯确认、感谢、LGTM 和无语义 reaction 不得唤醒无关智能体 full turn。
4. 只有 Agent-to-Agent chatter、没有人类关注、没有 active work、没有依赖变化的线程，应在有界深度后 `SILENT`。
5. Ambient 数量限制继续作为故障保险丝，但不再作为唯一相关性判断。

### 5.5 Attention Probe 的位置

规则无法判断以下问题时，复用现有 `execution_profile=attention_probe`：

- 一条普通群消息是否真的需要该领域智能体补充；
- 多个候选中谁拥有更直接的知识或责任；
- 当前讨论是在继续推进，还是已经形成重复意见；
- 需要直接回答，还是应升级给 coordinator。

Attention Probe 必须继续遵守既有隔离合同：临时 session、空工具、无 MCP/skill/transport、严格 JSON、
有界上下文、no-public-output。参与门不得创建另一个 small-brain profile。

### 5.6 失败策略

失败策略按触发来源区分：

- 人类 DM、mention、owner instruction：Probe 失败不能静默吞掉；回退到确定性目标参与，并记录降级原因；
- 持有 ready/running work：Probe 失败不能释放责任；保持 `CONTRIBUTE`；
- 纯 ambient Agent chatter：Probe 失败时 fail closed 为 `SILENT`；
- 多个公开回答候选：单个 Probe 失败不自动压掉其他可用候选，继续使用既有 Attention Round 规则；
- canonical 状态读取失败：不得基于空状态猜测无工作，保留原 Pending/任务并重试。

### 5.7 与现有组件的迁移关系

参与门不是第四套 gate，而是现有能力的统一入口：

1. Attention Round 保留，负责多个 `ANSWER` 候选收敛；
2. Ambient Gate 的时间窗和数量上限转为 Participation Gate 的输入与最后保险丝；
3. noise suppression 的结构化 kind 和确定性 lexicon 进入统一规则层；
4. trigger depth 转为 `recent_agent_only_depth`，只作为风险信号和硬保险丝，不能单独代表任务完成；
5. Work Graph scheduler 继续计算 frontier，参与门只决定应唤醒哪个既有 owner/coordinator；
6. busy Notice 继续保持 content-free，只可携带服务端生成的 attention hint，不传 Message body。

迁移完成后，生产唤醒入口必须只调用一个 `EvaluateParticipation` 边界，禁止 caller 自己组合 ambient、attention、
mention 和 work-state 规则。

## 6. Provider 凭证域自适应调度器

### 6.1 调度范围

首版调度范围是单台 Computer。原因是：

- 同一 OS 用户的多个 Workspace Runner 可能共享同一个 Provider 登录账号；
- Provider 的认证、全局配置和 session 属于继承的 Provider home，不属于某个智能体工作目录；
- Computer 已拥有 sibling coordination、进程身份 fencing 和 child lifecycle；
- 每个 Workspace Runner 各自限流无法看见同机其他 Runner 的并发，会继续发生突发。

因此，Computer 持有唯一的机器级 `CredentialDomainScheduler`。Workspace Runner 在每次真正开始 Provider 请求前，
通过既有 Computer↔Runner 本地 RPC 申请 permit；不得使用 HTTP wrapper，也不得把调度状态写进 Agent Root。

Cloud runtime 后续使用等价的集中调度 adapter，但不得让本机 Computer 依赖 Cloud Server 才能调用本地 Provider。

### 6.2 凭证域身份

`CredentialDomainKey` 表示一组共享 Provider 配额的调用。它不是密钥，也不得包含 token、cookie、邮箱或可逆账号信息。

目标形状：

```text
CredentialDomainKey
  provider_id
  auth_source_kind
  credential_fingerprint
  endpoint_class
```

规则：

- `credential_fingerprint` 由 Provider adapter 根据非秘密、稳定的认证来源生成单向摘要；
- 无法稳定识别账号时，按 `provider_id + auth_source_kind + machine` 保守合并，宁可少并发，不得把未知账号随机拆散；
- model 名不默认进入凭证域，因为多个 model 常共享账号总配额；Provider 明确证明配额独立时才能由 adapter 增加子域；
- workspace、Agent 和 runtime ID 不得用于拆分共享账号配额；
- 日志、指标和 API 只暴露不可逆的短 fingerprint。

### 6.3 Permit 合同

Runner 申请 permit 的请求目标形状：

```text
ProviderPermitRequest
  requestId
  daemonInstanceId
  workspaceId
  agentId
  credentialDomainKey
  lane
  priority
  enqueuedAt
  deadlineAt
  executionRef
```

`lane` 首版固定为：

```text
full_turn
attention_probe
verification
compaction
background
```

Permit 生命周期：

```text
queued -> granted -> started -> finished
   |         |          |
   +------> cancelled <--+
```

约束：

- `requestId` 是一次申请的幂等身份，重复申请必须返回原 queue/grant 结果；
- grant 绑定 child 的 `daemonInstanceId`，旧 child 或重连前实例不能消费新 permit；
- Runner 只有在即将发出真实 Provider 请求时才把 permit 标为 `started`；
- 正常完成、失败、超时和 429 都必须报告 `finished` outcome；
- child 退出、deadline 到期或运行时取消时，Computer 自动释放未完成 permit；
- permit 只控制资源，不是 Message Delivery ACK、Task lease 或 Work Graph fence；
- 获得 permit 不代表 Provider 接受请求，也不产生 Starting Activity；现有 Starting Activity 单一生产入口保持不变。

### 6.4 两级容量约束

每个凭证域同时执行两级约束：

1. **共享总并发**：所有 lane 的 in-flight 总和不能超过凭证域总上限；
2. **lane 上限**：防止 full turn、probe 或后台工作独占全部槽位。

示例，仅用于说明而非冻结默认值：

```text
domain total: 6
full_turn: 4
attention_probe: 3
verification: 2
compaction/background: 1
```

lane 上限之和可以大于总上限，但实际同时运行仍受总上限约束。配置必须有安全默认值，并允许 Provider adapter
基于已验证能力声明覆盖；不能从 CPU 数量推断远端账号配额。

### 6.5 公平与优先级

队列遵守：

- 同一优先级按 `enqueuedAt + requestId` 稳定 FIFO；
- 人类直接 DM/mention 可高于未开始的后台压缩；
- active Work Graph frontier 和 verifier 高于纯 ambient contribution；
- 已经在运行的请求不因新高优先级请求被抢占；
- 不同 workspace 使用加权轮转，防止一个大群持续占满同一账号；
- 后台 lane 必须有 aging，等待达到上限后获得执行机会；
- 同一智能体原有单槽串行化继续生效，调度器不得放宽它。

优先级只决定资源等待顺序，不得越过 Message seq、同优先级人类消息 FIFO 或 Work Graph 依赖。

### 6.6 启动节流与自适应 pacing

并发上限只能限制“同时有几个请求”，不能阻止 6 个请求在同一毫秒启动。因此每个凭证域还需要一个共享启动 pacer。

状态至少包括：

```text
base_interval
current_interval
max_interval
next_start_at
consecutive_successes
last_rate_limit_at
provider_retry_after
```

行为：

1. 新请求启动时间不得早于 `next_start_at`；
2. 发生明确 429、quota、overload 或 Provider Retry-After 时增加 `current_interval`；
3. Provider 返回 Retry-After 时优先遵守其值，并设置凭证域 cooldown；
4. 连续成功达到阈值后，逐级衰减回 `base_interval`；
5. 普通模型错误、业务拒绝、Agent 主动取消不能伪装成 rate limit；
6. pacing 变化只影响后续启动，不取消已经运行的请求；
7. full turn 和 Attention Probe 若共享账号配额，必须共享同一个 pacer。

首版算法可以采用有界倍增/减半，但倍数、成功阈值和上下限必须配置化并通过故障注入验证，不能直接复制其他项目常量。

### 6.7 429 和临时故障语义

Provider 资源故障不能直接制造业务失败：

- 429、明确 overload、可重试网络错误进入现有 retry/backoff 路径；
- canonical Message 保持 Pending 或已覆盖的真实状态，不因 permit 重排而改变；
- Issue、Task、Node 保持其既有 active/waiting 语义，只有自身 retry budget 耗尽后才按当前合同失败；
- work owner lease 不得因为单次排队自动释放；长 cooldown 需要按当前 ownership policy 续租或显式 handoff；
- 用户可见 Activity 可以表达“等待模型容量”，但不得伪装成 Agent 正在调用工具；
- 同一 failure fingerprint 的密集重试应合并，不能每次 wake 都创建新请求；
- rate-limit cooldown 结束后重新进入公平队列，不得绕过其他已排队工作。

### 6.8 崩溃与恢复

首版调度状态以 Computer 进程内状态为准，不成为跨重启 canonical 任务状态。恢复规则：

- Runner 崩溃时，Computer 根据 child identity 释放它持有的 permit；
- Computer 重启后所有本机 permit 失效，Runner 通过现有任务/消息恢复路径重新申请；
- Provider 请求 commit outcome unknown 时，仍由各业务现有幂等和 reconcile 合同处理，调度器不能假设“permit 丢失等于请求没发生”；
- 不持久化 Message body、Prompt、credential 或 Provider response；
- 后续如果持久化 pacing 学习，只能保存非秘密聚合状态，且不能冒充 canonical execution ledger。

## 7. 两个能力怎样协作

### 7.1 普通群聊问答

```text
人类在 8 个智能体的群里提问
  -> 参与门先排除 5 个无关智能体
  -> 2 个候选进入 ANSWER，1 个进入 CONTRIBUTE
  -> Attention Round 选出 1 个公开回答者
  -> 必要的 2-3 个模型调用进入同一凭证域队列
  -> 贡献者先交内部结果，回答者最后公开回复
```

### 7.2 Goal 并行执行

```text
Work Graph 解锁 3 个独立 worker node
  -> 参与门按 node owner 产生 3 个 CONTRIBUTE
  -> 三个请求进入 Provider 凭证域调度器
  -> 调度器按账号承受能力并行或排队
  -> artifact 提交后 verifier node ready
  -> 参与门只唤醒 verifier
  -> 验证通过后只唤醒 coordinator 汇总
```

### 7.3 Provider 限流

```text
3 个 worker 中第 2 个收到 429
  -> Scheduler 对凭证域降速并记录 Retry-After
  -> 第 2 个工作保持可恢复，第 3 个不立刻撞同一限流窗
  -> cooldown 后按公平队列恢复
  -> Work Graph 只看到真实结果，不把一次 Provider 限流当作结论失败
```

## 8. 观察性与审计

### 8.1 参与决策

每次参与判断至少记录：

```text
decision_id
trigger_kind
directed_kind
decision
reason_code
used_attention_probe
probe_status
work_ref_kind
policy_version
decision_latency_ms
```

不得记录完整 Message body、Prompt、私有 Memory 或 credential。审计记录是决策证据，不是新的任务状态。

建议指标：

```text
participation_decisions_total{decision,reason_code}
participation_probe_total{status}
participation_full_turn_avoided_total
attention_answer_collision_total
agent_only_loop_suppressed_total
```

### 8.2 凭证域调度

每个 permit 至少记录：

```text
request_id
credential_domain_fingerprint
provider_id
lane
priority
queue_wait_ms
run_ms
outcome
rate_limited
pacer_interval_ms
queue_depth_at_grant
```

建议指标：

```text
provider_permit_queue_depth{provider,lane,domain}
provider_permit_wait_seconds{provider,lane,domain}
provider_calls_in_flight{provider,lane,domain}
provider_rate_limit_total{provider,domain}
provider_pacer_interval_ms{provider,domain}
provider_permit_cancelled_total{reason}
```

domain 标签必须是有界、不可逆的短 fingerprint，禁止把 workspace、Agent 或账号邮箱作为高基数标签。

## 9. 实施阶段

### 9.1 阶段 A：只观察，不改变行为

1. 抽出纯函数 `EvaluateParticipation`，复现现有 Attention、Ambient、mention 和 work-state 决策；
2. 影子计算新旧决策差异，但继续执行旧路径；
3. Computer 增加 permit 队列和指标，但以 passthrough 模式立即 grant；
4. 建立凭证域 fingerprint，验证同账号 sibling Runner 能稳定归入同一域；
5. 用真实多智能体 benchmark 建立基线。

退出条件：连续观测窗口能解释新旧差异；不存在敏感信息进入日志；permit passthrough 不改变 Starting、Message ACK 或 Task 终态。

### 9.2 阶段 B：启用调度器

1. 先只控制 `attention_probe` 和新启动的 `full_turn`；
2. 启用共享启动 pacer，再启用总并发和 lane 上限；
3. 接入 429/Retry-After 反馈和自适应衰减；
4. 接入 child crash 自动释放、deadline 和公平队列；
5. compaction/background 最后接入，避免首批改动扩大上下文生命周期风险。

退出条件：限流故障注入不丢任务、不制造重复 provider start；同账号并发受控；不同账号互不阻塞。

### 9.3 阶段 C：启用参与门

1. 先接管纯 ambient Agent chatter；
2. 再接管已有 Work Graph owner、verifier 和 coordinator 的确定性唤醒；
3. 再统一普通群聊 Answer/Contribute/Coordinate；
4. 人类 DM、mention 和 owner instruction 最后切换，并保留 fail-safe 对照；
5. 删除已经没有生产 caller 的重复 ambient/attention 判断入口。

退出条件：所有生产唤醒入口都经过唯一参与边界；人类定向消息无静默丢失；复杂任务不因固定 trigger depth 提前终止。

## 10. 验收与可执行装置

本规格当前没有实现 owner，也没有“见过它红”，因此以下要求在落地前都保持 `仅文档`。
实施者必须为每条已知入口建立失败对照，再把对应条目升级为 `可执行`。

### 10.1 参与门矩阵

| 场景 | 预期 |
| --- | --- |
| 人类 DM 一个智能体 | 目标智能体参与，其他智能体不启动 |
| 群里明确 mention 一个智能体 | mention 目标参与，不受 ambient cap 静默影响 |
| 普通问题有两个 ANSWER 候选 | Attention Round 最终只授予一个公开回答者 |
| worker 持有 ready node | 进入 CONTRIBUTE，不需要公开抢答 |
| verifier node ready | 只唤醒 verifier，不重新唤醒已完成 worker |
| artifact revision 更新 | 唤醒受影响 verifier/下游 owner，不广播全队 |
| 纯 Agent 感谢/确认链 | 无 active work 时有界停止 |
| Probe 失败且来源是人类 DM | 不静默，按定向规则降级处理 |
| Probe 失败且是纯 ambient chatter | 不启动 full turn |
| canonical work-state 查询失败 | 不把空状态当无工作，不释放责任 |

每条“不得唤醒”的断言都必须配一个“相关场景仍会唤醒”的正向对照，防止整个入口被关闭后测试仍然通过。

### 10.2 调度器矩阵

| 场景 | 预期 |
| --- | --- |
| 同凭证域 10 个同时申请 | 受总并发和启动间隔约束 |
| 两个不同凭证域同时申请 | 独立调度，互不串行 |
| full turn 与 probe 共享账号 | lane 分开但共享总并发和 pacer |
| 某调用返回 429 | domain 降速，后续请求尊重 cooldown |
| 连续成功达到阈值 | pacing 有界衰减，不低于 base |
| Provider 返回 Retry-After | 不早于指定时间启动后续请求 |
| 等待中 Runner 退出 | queued permit 被取消，不泄漏容量 |
| 已 grant Runner 崩溃 | permit 被 fencing 回收 |
| 重复 requestId | 返回同一申请，不增加队列项 |
| 高优先级持续到来 | 后台工作通过 aging 最终获得执行 |
| Computer 重启 | canonical 工作通过现有恢复路径重新申请 |
| permit outcome unknown | 不据此断言 Provider 请求未发生 |

### 10.3 真实多智能体 benchmark

至少加入以下定时场景：

1. `single-answer`：多个智能体收到同一问题，最终只能有一个公开回答；
2. `parallel-graph`：三个 worker 并行、一个 verifier、一个 coordinator；
3. `agent-only-loop`：纯智能体确认链必须有界停止；
4. `provider-burst`：同凭证域同时唤醒，不能形成 429 风暴；
5. `provider-recovery`：注入 429/Retry-After，工作最终恢复且无重复副作用；
6. `human-directed-under-load`：后台队列拥塞时，人类 DM 仍能按优先级进入且不越过已经运行的请求。

统计指标至少包括完成率、重复公开回答、full turn 数、被避免的 full turn、排队时间、429 次数、恢复时间、
token、成本和最终 Graph 状态。真实 LLM benchmark 按多次 trial 统计，不作为每个 PR 的硬门禁。

## 11. 预期改动面

实施时预计涉及以下位置，最终文件名以实际单一职责为准：

- `server/internal/handler/`：参与上下文构造、canonical work-state 读取和唤醒入口收口；
- `server/internal/daemon/attention_round.go` 与 `attention_probe.go`：复用现有决策和受限 probe；
- `server/internal/handler/channel_ambient_gate.go`：从独立主判断迁移为统一参与输入/保险丝；
- `server/internal/daemon/`：Runner 申请 permit、上报 Provider outcome；
- Computer control RPC：机器级凭证域 scheduler 的 typed request/result；
- Provider adapter：凭证域 fingerprint、429/Retry-After 分类；
- 既有 usage/metrics：增加 participation reason、lane、queue wait 和 pacing 维度；
- `benchmarks/multiagent/`：真实多智能体定时评测。

如果 CLI、API、Computer↔Runner 协议或 builtin skill 行为发生变化，实施 PR 必须同步更新对应合同、
`SKILL.md` 和 source map；本 Draft 本身不改变任何既有协议。

## 12. 禁止项

- 禁止每个 Workspace Runner 各自维护同一账号的独立限流器；
- 禁止把 Provider token、cookie、邮箱、完整账号 ID 写入 domain key、日志或指标；
- 禁止用 CPU 数量直接推导远端账号并发额度；
- 禁止 Attention Probe 读取工具、完整 Memory、仓库或 Provider session；
- 禁止参与门推进 Message Context Boundary 或伪造 Message read；
- 禁止把 permit 当成 work owner lease、Task execution identity 或 completion evidence；
- 禁止 429 直接把 Goal、Issue、Node 标记为业务失败；
- 禁止直接注入 busy Message body 来降低响应延迟；继续使用 content-free Notice；
- 禁止长期保留新旧两套唤醒判断并依靠两边“写得一样”；迁移完成后只保留一个生产入口；
- 禁止以“减少调用”为理由静默吞掉人类 DM、明确 mention 或 owner instruction；
- 禁止为了 benchmark 通过而向生产 Prompt 添加场景专用答案或游戏规则。

## 13. 成功标准

满足以下条件才可以宣称本规格完成：

1. 一条普通群消息不会默认让全部智能体进入 full turn；
2. 人类定向消息在 probe 故障和 ambient 高负载下仍不会静默丢失；
3. Work Graph frontier 只唤醒当前需要的 owner、verifier 或 coordinator；
4. 同一 Provider 凭证域的 sibling Runner 共享总并发和启动 pacer；
5. 明确 429 后系统自动降速，连续成功后有界恢复吞吐；
6. Provider 临时限流不会丢 Pending Message、释放错误责任或制造重复 canonical 结果；
7. 不同凭证域互不阻塞；
8. Starting Activity、Message ACK、Context Boundary 和 Work Graph completion 的既有唯一 owner 不变；
9. 单元、并发、崩溃恢复和真实多智能体 benchmark 均覆盖正反对照；
10. 生产指标能回答“谁被判定需要参与”“为何参与”“排队多久”“哪个凭证域限流”“恢复用了多久”。

## 14. 待评审决定

实施前只剩以下需要 owner 拍板的参数性问题，不改变本规格的职责边界：

1. 各 Provider 的首版 total/lane concurrency 安全默认值；
2. base/max pacing、连续成功衰减阈值和无 Retry-After 时的 cooldown；
3. workspace 公平轮转的权重与后台 aging 上限；
4. 参与决策审计复用现有 execution/usage ledger，还是增加独立 append-only decision 表；
5. Cloud runtime 的等价 credential-domain adapter 在首版同步上线，还是本机 Computer 稳定后再接入。

这些参数必须通过影子数据、故障注入和 benchmark 校准；在有观测数据前，不把其他项目的常量当成 Multica 的最终默认值。
