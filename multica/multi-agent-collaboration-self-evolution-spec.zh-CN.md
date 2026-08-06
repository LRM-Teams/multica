# Multica 多 Agent 协作与自进化改造 Spec

> 状态：实施中（PR-1、PR-2 已创建草稿 PR）
> 目标分支：`dev`
> 基线提交：`b0c86d88b28cb12f4435e335fd405eae2e5d78b0`
> 文档语言：中文
> 适用范围：群聊消息调度、贝克汉姆群管理、协作会话与轮次控制、受控主动贡献、Agent Inbox、消息发送并发控制、团队协作策略、轻量模型训练与 Evolution Center

> 本次修订：取消“Top K Agent 召回”和“无 `@mention` 消息统一交给贝克汉姆快速分诊”的中心化入口；改为“人类无 `@` 消息触发全员大模型轻量注意力判断 + 自主举手 + Agent 间收敛 + 贝克汉姆复杂冲突兜底”。首版显式 `@Agent` 只让目标 Agent 进入完整执行，其余 Agent 仅持久 `observe`，不启动实时 Probe；`@all`/“大家”按群体命令处理。保留动态协作会话与 Work Graph/DAG 解决报数、依赖和并行协作，并以 `response_grant`、`turn_grant` 与 Freshness CAS 控制最终公开输出。训练、团队策略闭环和 Evolution Center 页面仍在范围内，但训练不作为第一阶段前置条件。实现拆为 6 个 PR。


## 0. 实施状态更新（2026-07-19）

本文档仍是 PR-1～PR-6 的唯一设计基准。本节只记录实施进度，不改变后续章节的设计要求。

### 0.1 已提交

1. PR-1（受限执行档位基础）：已创建草稿 PR [#750](https://github.com/LRM-Teams/multica/pull/750)，目标分支为 `dev`。已提供 `attention_probe|protocol_turn` 隔离执行档位、禁工具、临时会话、严格 JSON 和资源边界；未启用生产 Attention Round。
2. PR-2（无明确 Agent @ 的频道 Attention Round）：已创建堆叠草稿 PR [#751](https://github.com/LRM-Teams/multica/pull/751)，基于 PR-1 分支。已实现：
   - 人类群聊无明确 Agent `@mention` 时，为所有可用且未静音 Agent 创建受限 Attention Probe；
   - 显式 `@Agent` 仅让目标 Agent 完整执行，其他 Agent 持久 `observe`；`@all`/“大家”继续走群管理协议；
   - `channel_attention_round`、`channel_attention_participant` 与原子 dispatch outbox；
   - 2～5 秒 debounce、8 秒最大等待、每 Runtime 最大 16 个 Probe、容量与超时追踪；
   - 严格内部决策解析、无公开输出防线、`execution_id` 与 token usage 原子归因；
   - feature flag、`legacy_full` 紧急回滚和低基数指标。
3. PR-2 明确不包含 response grant、多人决策聚合、贡献公开发布、协作 DAG、Freshness CAS、审计 UI、Student 或 Context Filter；这些仍属于 PR-3～PR-6。

### 0.2 验证状态

1. Linux Go 1.26.1 `go build ./...` 已通过。
2. PR-2 相关 handler、daemon、metrics、server 定向测试已通过，包含 16 Agent 单轮、debounce 序列范围、飞书裸 `@Agent`、容量超时、严格结果和无公开输出边界。
3. Linux `go test ./...` 中，除 Windows checkout 将未修改的内置 `SKILL.md` 转为 CRLF 所触发的既有 frontmatter 基线问题外，其余实际执行包通过；同一基线测试在 Linux-native PR-1 clone 中通过。
4. 本机 PostgreSQL 不可达。仓库 CI 仅对目标为 `dev` 的 PR 自动触发；PR-2 当前堆叠在 PR-1 分支，因此在 PR-1 合并并把 PR-2 改指向 `dev` 后，再由 CI 执行数据库集成用例。在此之前 PR-2 保持 Draft。

## 1. 背景

当前群聊中，无明确 Agent `@mention` 的人类消息会进入全员唤醒路径。虽然 Agent Prompt 会要求模型自行判断相关性并在无关时保持沉默，但判断发生在模型已经被唤醒之后，因此仍然会产生模型调用、上下文组装和 Token 消耗。

典型问题包括：

1. “收到、好的、谢谢、你好”等低信息量消息唤醒所有 Agent；
2. 多个 Agent 从相同群消息快照并行推理，产生重复或过期回复；
3. 当前无 `@mention` 消息会让全部 Agent 进入完整执行；每个大模型确实可以独立判断是否回应，具有真实主动性，但代价是为每个 Agent 组装上下文、创建完整任务、启动 Runtime 和消耗大量 Token；
4. Agent Memory、团队知识、Evolution Center 已经存在，但缺少“协作策略生成—检索—使用—归因—淘汰”的闭环；
5. 缺少可用于训练 0.6B 轻量 Attention Student 和 Context Filter 的结构化标签数据；
6. 现有 AReaL 训练能力偏向 9B、OPD、GRPO 等高成本训练，不适合作为轻量分类模型的首个上线依赖；
7. 报数、轮流发言、依赖执行等任务缺少显式顺序，单靠“同时唤醒 + 发送前 freshness 检查”只能拦截旧回复，不能告诉 Agent 现在轮到谁；
8. 如果所有消息永远只由贝克汉姆单点判断，或者先由 owner、thread、Work Graph、角色或 Memory 检索召回少量候选，虽然成本可控，但未被召回的 Agent 根本没有机会形成“我知道这件事”的 `aha moment`；如果继续让全部 Agent 直接进入完整执行，又会产生高成本、刷屏和重复回复；
9. 现有系统缺少介于 `observe` 与 `execute` 之间的 `attention` 层：Agent 不能以低成本只做“是否参与”的判断，也缺少多 Agent 举手后的自动让出、合并与冲突收敛机制。

## 2. 改造目标

### 2.1 业务目标

1. 保证每个大模型都能独立看到群消息并判断自己是否应该参与，不通过中心路由、Top K 召回或贝克汉姆预选剥夺 Agent 的判断机会；
2. 将“全员完整执行”改为“全员轻量注意力判断 + 少数 Agent 完整执行”，显著降低工具加载、上下文组装、Runtime 执行和输出 Token；
3. 让 Agent 自主输出 `SILENT | ANSWER | CONTRIBUTE | COORDINATE`，并在多人举手时先自行让出、合并或请求协调；
4. 贝克汉姆只处理无法自动收敛的冲突、无人认领但疑似明确任务、多人编排、Issue 化、验收和催办，不成为每条无 `@mention` 消息的必经入口；
5. 对报数、轮流发言、前后依赖等任务建立明确参与者、顺序、当前轮次和结束条件；
6. 防止多个 Agent 在报数、抢答等场景发布基于旧快照生成的消息；
7. 保留受控主动性：每个 Agent 都可以发现自己拥有独特信息并内部“举手”，但未经授权不得直接抢占公开发言；
8. 将历史协作经验沉淀为分别供 Agent 注意力判断和贝克汉姆复杂协调使用的团队协作策略；
9. 从线上使用数据中自动生成训练样本，使注意力判断和上下文压缩能力持续迭代；
10. 在 Evolution Center 中量化展示全员观察、主动沉默、自主认领、内部补充、Agent 间自动收敛、贝克汉姆介入、完整执行节省和模型版本演进。

### 2.2 技术目标

1. 建立 `observe/deliver`、`attention/evaluate` 与 `execute/act` 三层消息语义；
2. 建立全员注意力轮、举手窗口、Agent 间收敛和公开发言授权机制；
3. 建立可回放、可归因、可审计的 Attention Decision 数据；
4. 建立通用 `collaboration_session`，支持顺序、依赖、并行、提案汇总和抢答等协作模式，并以 `turn_grant` 明确当前行动者；
5. 将“每个大模型独立判断”“发现自己可能有贡献”“进入完整执行”和“获得公开发言权”四件事分离；
6. 使用 `channel_message.seq` 实现可见消息提交边界的乐观并发控制；
7. 建立协作策略的候选、审核、版本、灰度、反馈和回滚机制；
8. 轻量模型必须支持 Shadow、Canary、自动回退和版本切换。

## 3. 非目标

本期不做以下事项：

1. 不训练 0.6B 模型完整替代大模型的任意多 Agent 判断或替代贝克汉姆进行复杂派工；
2. 不删除原始群消息或永久修改历史上下文；
3. 不让普通 Worker Agent 每次执行都读取完整团队协作策略库；
4. 不使用 GRPO 作为 Attention Student 或 Context Filter 的首个训练方案；
5. 不对所有 Agent 推理过程做全局串行化，只串行最终可见消息提交边界；
6. 不通过同时 `@mention` 所有参与者来模拟顺序协作；
7. 不允许仅因为“可能相关”就让 Agent 直接在群里公开抢答；
8. 不使用 owner、thread、Work Graph、角色标签、关键词或 Memory 相似度作为“哪些 Agent 有资格判断”的前置过滤；这些信息可以作为大模型自己判断时的上下文事实，但不能阻止任何 Agent 参加注意力轮；
9. 不在第一阶段把每个 Agent 的完整 Memory、完整工具集或工作目录加载进注意力判断；注意力上下文只包含该 Agent 自己的稳定身份、紧凑 Memory 摘要、当前工作摘要和有限群消息；
10. 不在本期修改现有 Agent 自身 Memory/Skill 自进化主流程。

## 4. 设计原则

### 4.1 看见、注意与执行是三件事

群消息必须正常写入 `channel_message`。每个 Agent 都获得消息可见性，但消息处理分为三级：

* `observe`：只进入持久 Inbox，等待 Agent 下一次自然执行时读取；
* `attention`：调用该 Agent 自己的大模型，以极短上下文、禁用工具和受限输出判断是否参与；
* `execute`：只有获得任务或发言权的 Agent 才进入完整上下文、工具和 Runtime 执行。

第一阶段的目标不是消除全员模型调用，而是把全员昂贵的 `execute` 降级为全员低成本的 `attention`。

### 4.2 每个大模型自己判断，贝克汉姆只处理例外

第一阶段不使用候选召回器。所有未静音、可运行的群内 Agent 都参加注意力轮，并独立输出：

* `SILENT`：不参与；
* `ANSWER`：愿意承担主回答；
* `CONTRIBUTE`：有独特证据、纠错或关键补充；
* `COORDINATE`：判断需要多人编排、顺序、Issue 化或管理员介入。

贝克汉姆继续负责复杂任务理解、Issue 化、验收、编排和派工，但只在注意力轮无法自动收敛或明确需要协调时介入。

### 4.3 明确指令优先于模型

以下路径不等待无 `@mention` 主回答者选举：

* 明确 `@mention`；
* 服务端已验证的 Issue assignee、work node owner 或 handoff target 所对应的确定性任务启动；
* 系统级安全、权限和人工强制操作。

### 4.4 错误静默比多一次注意力判断更严重

单个 Agent 注意力判断失败，不能替其他 Agent 做出 `SILENT` 决定。注意力轮超时或部分 Agent 不可用时，记录缺席者并允许其后续从 ambient Inbox 形成延迟贡献；若所有可用 Agent 均失败且消息疑似需要处理，则回退到贝克汉姆，而不是直接静默。

### 4.5 训练不能成为第一版上线前置条件

第一阶段先由各 Agent 当前使用的大模型产生真实注意力判断和协作数据。0.6B 学生模型不是首版前置条件，且只能在 Shadow 数据达到门槛后逐步加速高频简单判断；大模型自判断仍是教师和低置信回退路径。

### 4.6 主动发现不等于公开发言

每个 Agent 都可以通过自己的注意力判断提交 `ANSWER` 或 `CONTRIBUTE`，不需要先被中心系统选为候选。注意力判断和内部 offer 默认不可见；只有明确 `@mention`、唯一主回答认领、Agent 间收敛后的 response grant、贝克汉姆派工或当前 `turn_grant` 才获得 `public_response` 权限。

`CONTRIBUTE` 的判断标准不是“我也会回答”，而是“我掌握了当前回答者可能没有、并且会实质改变结果的信息”。重复、附和和泛泛建议必须选择 `SILENT` 或在收敛轮中 `YIELD`。

### 4.7 顺序由协作协议决定，CAS 只负责兜底

报数等强顺序任务必须先建立参与者队列和当前轮次，一次只向当前参与者发放 `turn_grant`。Freshness CAS 负责防止重试、竞态或旧快照导致的重复提交，但不能替代顺序调度。

## 5. 目标架构

### 5.1 群消息处理与全员注意力轮

```text
写入 channel_message
  ├─ 给所有 Agent 写持久消息投递
  ├─ 人类消息有明确 @Agent
  │    ├─ 被 mention 的 Agent 直接获得 primary_responder 权限并进入 execute
  │    └─ 其他 Agent 首版仅 observe，不启动实时 Probe
  ├─ 人类消息为 @all / “大家”群体命令
  │    └─ 进入群体协议/协作编排入口，不按普通单 Agent mention 处理
  ├─ 人类消息无明确 Agent mention
       ├─ 2～5 秒内消息合并为 attention_round
       ├─ 所有可用 Agent 进入 attention/evaluate
       ├─ 0 个 ANSWER：保持沉默；存在 COORDINATE 或全员失败时交给贝克汉姆
       ├─ 1 个 ANSWER：自动授予 public_response
       └─ 多个 ANSWER：进入一次 Agent 收敛轮；仍冲突才交给贝克汉姆
  └─ Agent / system 消息
       └─ 只做持久投递与既有确定性协议推进，不创建新的全员 Attention Round
```

注意力轮不做 Top K 召回，也不根据 owner、thread、Work Graph、角色或 Memory 相似度排除 Agent。上述状态可以进入每个 Agent 自己的注意力上下文，帮助其判断，但不能代替它判断。

所有 Agent 使用自己的身份、指令和紧凑 Memory 摘要判断；未及时完成注意力判断的离线 Agent 仍保留 ambient 消息，可在下一次自然执行时提交 `late_contribution_offer`，形成延迟 `aha`。

### 5.2 协作会话与轮次协议

贝克汉姆识别到需要多人配合时，应创建 `collaboration_session` 并把目标编译为动态 Work Graph/DAG，而不是简单地同时 `@mention` 所有参与者。贝克汉姆负责理解、拆解、选择协作模式、定义依赖/完成条件和异常重规划；服务端负责按图自动推进 ready node、发放 grant 与执行幂等检查。只有图冲突、节点失败、目标变化或无人认领时再次唤醒贝克汉姆。

建议支持以下通用模式：

| 模式           | 适用场景            | 调度方式                                   |
| ------------ | --------------- | -------------------------------------- |
| `sequential` | 报数、轮流发言、逐级审批    | 一次只向当前参与者发放 `turn_grant`               |
| `dependency` | 开发完成后测试、接口完成后联调 | 前置节点完成后唤醒下游                            |
| `parallel`   | 前端与后端可同时开发      | 同时唤醒多个明确子任务 owner                      |
| `proposal`   | 头脑风暴、方案评审       | 多人内部提交提案，先由参与 Agent 自行合并，无法收敛时由贝克汉姆汇总  |
| `race`       | 抢答、紧急故障排查       | 允许并行思考，但通过 response grant 只允许一个提交者公开输出 |

最小数据模型：

```text
collaboration_session
  id
  workspace_id
  channel_id
  mode                  sequential | dependency | parallel | proposal | race
  status                pending | active | completed | suspended | cancelled
  goal
  participant_agent_ids
  current_turn_index
  expected_step
  completion_condition
  version
  created_by_run_id
  created_at / updated_at

collaboration_turn
  session_id
  turn_id
  agent_id
  turn_index
  grant_status          pending | granted | consumed | skipped | expired
  grant_seq
  result_message_id
  deadline_at
```

报数示例：贝克汉姆创建 `sequential` 会话，参与者为 A、B、C，顺序为 `A → B → C → A`，当前数字为 0，结束条件为 7。系统只唤醒 A；A 成功提交 1 后，事务性推进 `current_turn_index` 并唤醒 B。不得一次性唤醒 A、B、C 再依赖模型自行猜测顺序。

轮次推进必须满足：

* 当前 Agent 持有有效 `turn_grant`；
* Agent 的输出通过 Freshness CAS；
* `session.version` 与提交时一致；
* 同一 `turn_id` 只能消费一次；
* 超时可由贝克汉姆决定提醒、跳过、替换参与者或暂停会话；
* 会话完成、取消或暂停后，不再产生新轮次。

### 5.3 全员注意力判断与受控主动贡献协议

系统把“看见消息”“大模型判断”“进入完整执行”和“可以公开发言”进一步分开。建议统一使用：

```text
delivery_mode
  observe
  attention
  execute

attention_decision
  SILENT
  ANSWER
  CONTRIBUTE
  COORDINATE

response_mode
  no_public_output
  contribution_offer
  convergence_vote
  public_response
```

对“人类无明确 Agent mention”的 Attention Round，每个未静音 Agent 都有权提交一次注意力判断。`ANSWER` 和 `CONTRIBUTE` 必须给出价值类型、简短摘要和可选证据引用：

```json
{
  "decision": "CONTRIBUTE",
  "agent_id": "agent-b",
  "confidence": 0.94,
  "reason_code": "direct_answer | unique_evidence | correction | newer_result | task_claim",
  "summary": "该连接池问题已定位为超时配置不一致",
  "evidence_refs": ["message-102", "issue-38"],
  "suggested_action": "grant_public | merge_into_primary | request_coordination | no_public_reply"
}
```

无明确 mention 时，唯一 `ANSWER` 可以自动获得公开发言权；多个 `ANSWER` 进入一次收敛轮，每个举手 Agent 只能输出 `YIELD | KEEP | MERGE | REQUEST_MANAGER`。收敛到唯一 `KEEP` 或明确 `MERGE` 时自动授权；仍冲突才由贝克汉姆裁决。

明确 mention 时，被 mention 的 Agent 始终是主回答者并直接进入完整执行；首版其他 Agent 只接收持久 `observe` 投递，不做实时 Probe，也不参与本轮 offer。未来如要增加“旁路贡献”，必须作为独立灰度能力另开设计，不得借本次无 `@` Attention Round 顺带启用。

### 5.4 Attention Probe 输出协议

```json
{
  "decision": "SILENT | ANSWER | CONTRIBUTE | COORDINATE",
  "confidence": 0.92,
  "value_type": "none | direct_answer | unique_evidence | correction | task_claim | needs_protocol",
  "summary": "我处理过相同连接池超时，问题可能是配置版本不一致",
  "evidence_refs": ["memory:connection-pool-timeout"],
  "model_version": "agent-primary-model",
  "seen_up_to_seq": 102
}
```

约束：

* Attention Probe 使用该 Agent 自己当前配置的大模型，不由中心模型替它判断；
* 禁用工具、代码仓库加载和公开输出，限制输入上下文和最大输出 Token；
* Agent 不得读取其他 Agent 的私有 Memory；
* 输出解析失败只影响该 Agent 本轮判断，不能自动代表全员静默；
* 明确 mention、系统级任务启动和人工强制操作继续由确定性规则保证；
* 第一阶段不使用 Attention Probe 结果训练或驱动小模型，先验证真实成本、主动性和噪声。

### 5.5 上下文过滤输出协议

```json
{
  "label": "MUST_KEEP | KEEP_IF_BUDGET | DROP",
  "confidence": 0.96,
  "reason_code": "task_requirement | decision | tool_result | acknowledgement | duplicate | unrelated_chat"
}
```

Context Filter 必须以“当前任务”为条件，不能仅凭消息文本静态判断。例如“今天天气如何”在普通开发任务中可能无关，但在天气查询任务中是核心要求。

## 6. PR 拆分总览

| PR   | 名称                                       | 核心结果                                                                                             | 是否依赖训练  |            |                                     |   |
| ---- | ---------------------------------------- | ------------------------------------------------------------------------------------------------ | ------- | ---------- | ----------------------------------- | - |
| PR-1 | 受限执行档位基础                                 | 引入 `attention_probe`、`protocol_turn`、`full` 三档；Pi 临时会话、空工具注册表、短上下文和 no-public-output fail closed | 否       |            |                                     |   |
| PR-2 | 人类无 `@` 全员 Attention Round               | 只有人类无明确 Agent `@` 的消息创建全员 Probe；结果写内部 Round/Participant，不直接发布                                    | 否       |            |                                     |   |
| PR-3 | 自主举手、一次收敛与 Response Grant                | 聚合 `SILENT                                                                                       | ANSWER  | CONTRIBUTE | COORDINATE`，完成唯一主答、一次收敛、内部贡献和公开发言授权 | 否 |
| PR-4 | 动态协作 Workflow / Work Graph DAG           | 贝克汉姆把复杂任务编译成动态 DAG；支持 sequential/dependency/parallel/proposal/race、turn grant、异常重规划              | 否       |            |                                     |   |
| PR-5 | Freshness、审计、策略闭环与 Evolution Center      | 闭合 CAS/held continuation；沉淀教师数据和团队策略；改造自进化页面与消息决策链、自动化/协作/模型指标                                   | 否       |            |                                     |   |
| PR-6 | 可选 Attention Student 与 Context Filter 训练 | 在真实教师数据稳定后训练/灰度 0.6B Student 与任务条件化 Context Filter，支持 Shadow/Canary/回退                           | 是，非首版前置 |            | …9375 tokens truncated…生成

使用强教师对“当前任务 + 候选消息”生成三分类标签。可以从以下数据自动抽样：

* 实际被 Agent 引用的消息；
* 最终 ActionPlan evidence；
* Issue 创建时引用的源消息；
* 工具结果和失败消息；
* greetings、acknowledgement、重复状态等高频负样本；
* 人工重新补充到 Prompt 的消息作为漏过滤反例。

### 12.5 训练方案

1. 第一版使用 CE/SFT；
2. 有教师 soft probability 时使用离线软蒸馏；
3. 不使用 GRPO；
4. 不以“压缩率”单一指标训练，必须把关键上下文召回作为约束；
5. 可以与 Attention Student 共享 0.6B 底座，但保持独立 adapter 和发布版本。

### 12.6 预算策略

```text
先放 MUST_KEEP
若超过硬预算：走摘要或报错，不允许直接丢弃
再按相关性和时间放 KEEP_IF_BUDGET
DROP 不进入 prompt
记录每类消息数量和节省 token
```

### 12.7 验收标准

1. 关键上下文召回率不低于 99%；
2. 原始任务和最新用户纠正召回率为 100%；
3. 平均上下文输入 Token 降低 30%～50%；
4. Task success rate 不低于未过滤基线；
5. 任意过滤结果可通过 message ID 审计和回放；
6. 模型不可用时回退为“不过滤”，不能阻塞任务。

---

## 13. 能力包 G：Evolution Center 自动化/协作进化页（PR-5）

### 13.1 目标

在现有 Evolution Center 中展示三层自进化，而不是另建孤立页面：

1. 个体进化：Memory/Skill；
2. 自动化与协作进化：全员注意力判断、自主举手、Agent 间收敛、协作会话与贝克汉姆异常兜底；
3. 参数进化：Attention Student、Context Filter 的数据、版本和灰度效果。

页面的核心叙事不是“管理员派了多少任务”，而是“系统自主观察了多少消息、多少次自己决定不打扰、多少次主动发现并完成工作、多少复杂情况才需要管理者介入”。

### 13.2 后端 API

扩展 evolution metrics response：

```json
{
  "individual_evolution": {},
  "collaboration_evolution": {
    "unmentioned_messages": 0,
    "attention_rounds": 0,
    "attention_probes": 0,
    "attention_silent_rate": 0,
    "autonomous_claims": 0,
    "peer_converged": 0,
    "manager_fallbacks": 0,
    "full_execution_wakes": 0,
    "full_execution_reduction_rate": 0,
    "collaboration_sessions": 0,
    "turn_order_violation_rate": 0,
    "contribution_offers": 0,
    "contribution_offer_adoption_rate": 0,
    "contribution_offer_helpful_rate": 0,
    "unauthorized_public_sends_blocked": 0,
    "policies_retrieved": 0,
    "policies_used": 0,
    "policy_success_rate": 0,
    "attention_tokens": 0,
    "execution_tokens": 0,
    "estimated_tokens_saved": 0
  },
  "model_evolution": {
    "attention_student_version": "",
    "attention_student_mode": "shadow",
    "missed_attention_rate": 0,
    "late_rescue_rate": 0,
    "context_filter_version": "",
    "context_compression_rate": 0,
    "critical_context_recall": 0
  }
}
```

### 13.3 页面结构

在 `packages/views/evolution/components/evolution-center-page.tsx` 增加：

#### Overview

* 累计节省 Token；
* 自动处理率：无需贝克汉姆即可完成收敛并授权的消息比例；
* 有帮助的主动贡献率；
* 贝克汉姆异常兜底率；
* 每条无 mention 消息平均 Attention Probe 数与完整执行数；
* Agent 自主静默、主答、贡献、协调的分布；
* stale output 拦截数；
* 顺序会话成功率和轮次异常数；
* 被拦截的无发言权公开发送数；
* 策略复用带来的任务成功率/成本差异；
* 可展示典型摘要：`16 个 Agent 观察 → 13 静默 → 2 贡献 → 1 主答 → 0 次管理兜底`。

#### Attention / Collaboration

* 单条消息决策链：参与者、四类决策、收敛 vote、response grant、完整执行、最终结果；
* 自动收敛与 Beckham fallback 的原因分布；
* `ANSWER` 冲突如何通过 `YIELD|MERGE` 解决；
* contribution offer 的采用、独特增量、噪声和 late aha；
* collaboration session 模式分布与 turn 时间线；
* 策略生命周期漏斗：candidate → review → canary → active → quarantined；
* 策略列表及版本；
* 使用/忽略/成功/失败/冲突；
* 典型案例：某 Agent 没有被 `@mention`，但根据自己的上下文发现关键证据并通过内部贡献改变了结果。

#### Agents

* 每个 Agent 的 `SILENT|ANSWER|CONTRIBUTE|COORDINATE` 分布；
* 举手采纳率、独特贡献率、噪声率、正确让步率；
* 主动发现任务数、平均 Probe 成本与完整执行成本；
* 仅用于反馈和诊断，不形成剥夺其后续观察权的中心排名。

#### Knowledge / Policies

* 保留现有 Memory、Skill、Team Knowledge 的 review/curation；
* 增加 attention policy 与 collaboration policy 的版本、适用范围和效果；
* 明确策略消费者是 `attention_agent` 还是 `group_manager`。

#### 模型进化页签

* 当前模型版本、模式和灰度比例；
* 金标集 precision/recall/F1/ECE；
* Shadow 分歧样本；
* missed attention、late rescue；
* 上下文压缩率和关键召回；
* 版本升级、回退和数据时间范围。

### 13.4 指标定义

| 指标                               | 定义                                                                    |
| -------------------------------- | --------------------------------------------------------------------- |
| Full execution amplification     | Full execution wake count / no-mention message count                  |
| Token saving rate                | `(legacy estimated tokens - actual tokens) / legacy estimated tokens` |
| Attention silent rate            | `SILENT` decisions / completed attention probes                       |
| Autonomous handling rate         | 无需 Beckham 即完成收敛并授予 response grant / actionable rounds                |
| Manager fallback rate            | 进入 Beckham 复杂协调 / attention rounds                                    |
| Missed attention rate            | 金标/人工确认应参与但该 Agent 被学生模型自动静默的比例                                       |
| Late rescue rate                 | 注意力轮结束后窗口内被人工重新 mention/派工的比例                                         |
| Duplicate reply rate             | 同一触发窗口内语义重复可见回复比例                                                     |
| Turn order violation rate        | 无有效 turn、重复消费 turn 或错误参与者公开提交的比例                                      |
| Contribution offer adoption rate | `(merge_into_primary + request_followup + reassign) / all offers`     |
| Contribution offer helpful rate  | 被采用且最终确认提供独特增量或改善结果的 offer / 被采用 offer                                |
| Proactive noise rate             | 被判定为重复、附和或无关的 offer / all offers                                      |
| Policy adoption rate             | used / injected                                                       |
| Policy success rate              | success / (success + failure)                                         |
| Context compression rate         | removed candidate tokens / total candidate tokens                     |
| Critical context recall          | retained MUST_KEEP / all gold MUST_KEEP                               |

### 13.5 验收标准

1. 指标口径在后端统一计算，前端不自行推导关键业务指标；
2. 页面支持 7/30/90 天时间范围；
3. 支持按模型版本和运行模式筛选；
4. 高基数维度从数据库查询，不写 Prometheus labels；
5. 数据为空、模型未上线和功能关闭时有明确 empty state；
6. 页面可展示回退前后效果，不只展示累计正向数据。
7. 单条消息可展开查看“观察 → 判断 → 收敛 → 授权 → 执行 → 结果”的完整自动化链路。

---

## 14. PR 依赖与推荐合并顺序

```text
PR-1 受限执行档位（dormant foundation）
  └─ PR-2 人类无 @ 全员 Attention Round
       └─ PR-3 自主举手 / 一次收敛 / Contribution / Response Grant
            └─ PR-4 Dynamic Workflow / Work Graph DAG / Turn Grant
                 └─ PR-5 Freshness / 审计 / 策略闭环 / Evolution Center
                      └─ PR-6 Attention Student + Context Filter（可选训练）
```

PR-1 不创建生产 Attention Round；PR-2 不允许 Probe 结果直接发布；PR-3 才闭合谁进入完整执行和谁能公开响应；PR-4 才开启多人动态编排；PR-5 闭合发送安全、数据闭环和页面可观测；PR-6 只能在 PR-5 数据门槛达到后开始 Production 灰度。

推荐合并顺序：

1. PR-1；
2. PR-2；
3. PR-3；
4. PR-4；
5. PR-5；
6. PR-6。

PR-5 较大，但它是同一个“可安全上线且可观测”的交付面：Freshness/audit 为数据真实性提供基础，策略闭环消费这些事件，Evolution Center 展示同一套口径。实现时可以分多个 commit，仍保持一个 PR，避免页面先于可信数据口径上线。

## 15. 分阶段发布

### 阶段一：快速可用

包含：PR-1、PR-2、PR-3、PR-4。

结果：

* 无 mention 消息仍由所有可用 Agent 看见，但只执行轻量大模型 Attention Probe，不再全员完整执行；
* 每个 Agent 自己选择 `SILENT|ANSWER|CONTRIBUTE|COORDINATE`，owner/thread/Work Graph 等只作为判断事实；
* 唯一 `ANSWER` 自动取得完整执行权，多人 `ANSWER` 先通过一轮 `YIELD|KEEP|MERGE|REQUEST_MANAGER` 自主收敛；
* 明确 mention 确定主回答者，其他 Agent 首版只 observe；
* 贝克汉姆只处理收敛失败、明确 `COORDINATE`、强顺序/依赖、Issue 化、验收和催办；
* 贝克汉姆把复杂任务编译为动态 Workflow / Work Graph DAG，正常节点由服务端自动推进，异常时才重规划；
* 报数等强顺序任务创建协作会话和参与者队列，一次只唤醒当前 turn；
* Agent 可以自主内部举手，但没有 response grant 时不能抢群聊发言；
* 报数和抢答场景同时使用 turn grant 与 freshness CAS；
* 不依赖标签和训练资源；
* 能立即看到完整执行 Token、重复回复和顺序冲突下降，同时保留每个大模型的真实主动性。

### 阶段二：协作经验自进化

包含：PR-5。

结果：

* 每个 Agent 的主模型自动生成自身注意力教师数据；
* 历史协作经验形成团队策略；
* 注意力判断、自主让步、轮次安排和主动贡献效果形成可复用策略；
* 策略按消费者分别注入 Agent Attention Prompt 或贝克汉姆复杂协调 Prompt，不能用于前置过滤 Agent；
* 策略使用和业务结果可归因；
* Evolution Center 能展示个体进化、自动化/协作进化、策略复用、成本收益、消息决策链与 Work Graph 运行/重规划情况。

### 阶段三：参数自进化

包含：PR-6。

结果：

* 0.6B Attention Student 逐步接管每个 Agent 的高置信简单静默判断；
* Context Filter 降低输入上下文；
* 分歧和低置信样本持续回流给教师；
* 模型按 Shadow/Canary/Production 演进；
* 越用标签越多、模型越准、Token 越省。

## 16. 风险与回滚

### 16.1 漏掉真实任务或独特贡献

措施：

* 第一阶段所有可用 Agent 都用自己的大模型参加 Attention Probe，不做前置召回；
* 单个 Agent Probe 失败不替其他 Agent 判静默，所有 Probe 均失败时才回退贝克汉姆；
* 学生模型低置信度回退对应 Agent 的主模型；
* 自动 `SILENT` 使用高阈值；
* 监控 late rescue 和人工重新 mention；
* 保留 ambient Inbox，允许错过首轮的 Agent 形成可审计的 late contribution；
* 一键切回“全员主模型 Attention Probe”。

### 16.2 全员 Attention Probe 成本或延迟过高

措施：

* 1～2 秒 debounce 合并同一触发窗口；
* Probe 禁用工具、仓库扫描和写操作，限制最近 4～8 条消息、紧凑个人摘要和 96 Token 输出；
* 复用模型前缀缓存并对同一消息的 Agent Probe 做服务端 batch；
* 设置注意力轮截止时间，超时者不阻塞已形成的唯一主答；
* 数据稳定后优先蒸馏高频 `SILENT` 样本到 Attention Student；
* 贝克汉姆长期巡检与逐消息注意力轮完全分开限流。

### 16.3 团队策略污染普通 Agent

措施：

* 使用 `consumer_roles=attention_agent|group_manager` 明确注入边界；
* 普通完整执行上下文强制过滤注意力/管理策略；
* 不复制到 Agent 本地 Memory/Skill；
* 策略带版本并支持 quarantine/rollback。

### 16.4 上下文过滤误删关键信息

措施：

* 硬保留规则先于模型；
* 模型不可用时不做过滤；
* 保留 message ID 和过滤审计；
* 使用关键上下文召回率作为发布门槛；
* 支持 workspace 级关闭。

### 16.5 训练资源不足

措施：

* 首版使用普通 CE/SFT 或 LoRA；
* Attention Student 和 Context Filter 可共享 0.6B 底座；
* 不把 AReaL GRPO 作为依赖；
* OPD 只在已有稳定离线模型后增量执行；
* 推理使用量化和 batch。

### 16.6 Agent 主动性被中心逻辑削弱

措施：

* 所有可用 Agent 都参加 Attention Probe，owner、近期参与、thread/Issue 和 Memory 只作为各自判断上下文；
* 首版明确 mention 只唤醒目标 Agent 完整执行，其他 Agent 仅 observe；无 `@` 场景的全员主动性不受中心逻辑削弱；
* 每个 Agent 都可以提交一个内部 offer，由主回答者、收敛结果或贝克汉姆吸收；
* 监控“人工后来找回历史 owner”和 contribution helpful rate；
* 不以任何角色/Memory 排名永久降低某个 Agent 的观察资格。

### 16.7 主动举手重新造成噪声

措施：

* 所有 Probe 和 offer 默认无公开发言权；
* 每个 Agent 每轮最多一个精简 offer，并设置整轮字节预算；
* 举手必须包含独特信息理由和 evidence reference；
* 统计 ignored、offer_noise 和 duplicate contribution；
* 高噪声反馈用于优化该 Agent 的判断策略，但不取消其后续观察权。

### 16.8 协作会话卡死或顺序错误

措施：

* turn 使用唯一 ID、状态和版本号；
* 消息提交与 turn 消费在同一事务；
* 超时进入贝克汉姆处理，不自动全员唤醒；
* 支持 skip、replace、suspend、cancel 和人工接管；
* 会话异常时可退化为贝克汉姆逐个明确派工。

### 16.9 注意力轮卡死或收敛振荡

措施：

* 注意力轮使用固定 deadline 和幂等 round ID；
* 多人 `ANSWER` 只允许一轮收敛，不做 Agent 间无限讨论；
* 收敛后仍有多个 `KEEP` 或出现 `REQUEST_MANAGER` 时立即升级贝克汉姆；
* response grant 使用唯一 ID、版本和过期时间，迟到 vote 只能写审计，不能反向覆盖已生效授权；
* 可按 workspace 回退到“唯一 `ANSWER` 自动授权，多 `ANSWER` 直接交贝克汉姆”的简化模式。

## 17. Definition of Done

整个项目完成需同时满足：

1. 对人类无明确 Agent `@` 的消息，100% 可用且未静音 Agent 获得 Attention Probe 投递，失败和超时均显式记录；
2. 明确 mention 投递召回率 100%；
3. 无 mention 完整执行 amplification 小于等于 0.3，或较 legacy 全员完整执行下降至少 70%；
4. 多人举手只进行一轮收敛，冲突无法消除时才进入贝克汉姆；
5. sequential 会话任意时刻最多一个有效 turn，报数 1,000 次顺序正确；
6. 未获得 response grant 的 Agent 公开发送成功数为 0；
7. stale publish 率小于 0.1%；
8. contribution offer 可追溯到 Agent 决策、证据、采用者和最终结果；
9. 第一阶段 Attention Probe + 完整执行总 Token 较 legacy 全员完整执行下降至少 50%；
10. Attention Student 上线前 actionable decision recall 不低于 99%；
11. 线上 late rescue 率不高于 0.5%；
12. Context Filter 关键上下文召回率不低于 99%；
13. 平均输入上下文 Token 下降至少 30%；
14. 团队策略每次使用可归因到具体版本和任务结果，且不能用于跳过 Agent Probe；
15. Evolution Center 可以展示个体、自动化协作、参数三层自进化和单条消息决策链；
16. 所有模型和策略均支持 Shadow、Canary、停用和回滚；
17. 功能关闭或学生模型异常时系统仍可依靠全员主模型 Probe 与贝克汉姆异常兜底安全运行。

## 18. 建议的第一个迭代

第一个迭代按 PR-1 → PR-2 → PR-3 → PR-4 顺序推进，不等待训练：

1. PR-1 先在 Agent Runtime 增加禁工具、临时会话、短上下文、严格 JSON 的 `attention_probe|protocol_turn` 执行档位，不接生产触发；
2. PR-2 只改“人类无明确 Agent `@`”消息投递语义，建立 `channel_attention_round` 和内部 Probe 结果，不做候选召回、不直接发布；
3. 显式 mention 继续直接授予目标 Agent 主回答权，其余 Agent 首版只 observe；Agent/system 消息不得触发全员 Probe 循环；
4. PR-3 在无 `@` 场景聚合决策：唯一 `ANSWER` 自动发 response grant，多 `ANSWER` 只做一次 `YIELD|KEEP|MERGE|REQUEST_MANAGER` 收敛；
5. PR-3 增加 `contribution_offer`、response grant 和公开发送服务端校验；
6. PR-4 建立 `collaboration_session`、Work Graph DAG 和 `turn_grant`，完整支持 sequential，并让 dependency/parallel/proposal/race 走统一可执行图；
7. PR-4 让贝克汉姆只负责编译 DAG 和异常重规划，正常 ready-node 推进由服务执行；
8. PR-5 补齐发送前事务级 Freshness CAS，并与 grant/turn 消费放在同一事务；
9. PR-5 建立不可变审计、教师数据、团队协作策略闭环，以及 Token、收敛、管理兜底、重复回复、主动贡献、DAG 重规划和任务漏接指标；
10. PR-5 改造 Evolution Center，展示个体、自动化/协作、策略和模型三层演进及单条消息完整决策链；
11. 用真实线上数据验证完整执行是否下降、顺序/DAG 是否稳定、主动贡献是否有用以及任务是否漏接；
12. 数据稳定并达到门槛后再启动 PR-6 的 Attention Student 与 Context Filter，训练不作为第一阶段前置条件。

第一阶段的原则是：所有数字员工先观察，并由各自的大模型决定是否参与；系统只控制公开输出和并发边界；贝克汉姆只处理复杂例外。这样既保留未被点名时产生 `aha moment` 的主动性，也能立即降低全员完整执行的成本，并自然积累后续协作策略与参数自进化数据。
