# 摩擦门控记忆规格（Friction-Gated Memory）

> 状态：Phase 0–1 已补齐 action_rejected（工具权限/用户拒绝）与 Issue rework
> （触发句式 + 关联 Issue）；Phase 2 Goal / Epoch 聚合仍未开始
>
> 日期：2026-08-20
>
> 本规格约束"什么内容有资格进入长期记忆"，不修改记忆检索、注入排序或
> graph memory 召回行为。基线为 `docs/agent-memory-model.md`（2026-07-21 口径）
> 与 `server/internal/memorysignal` 现有漏写兜底管道。
>
> 外部经验来源：Tencent teamai-cli 的 Stop-hook 摩擦评分机制
> （用户打断 / 拒绝工具调用 / 失败工具反复重试 → 才提示沉淀经验；
> 又长又顺的 session 不触发；每 session 最多提示一次）。
> 本规格只吸收其触发哲学，不引入其 Git 分发或 CLI 形态。

## 1. 一句话决定

顺滑完成的工作最多进 Daily 短索引，随 Daily 的"历史不加载"语义自然过期；
只有出现**摩擦**（人工纠正、动作被拒、失败重试环、评审返工）的工作，其教训
才有资格进入正式长期记忆和共享候选，且摩擦证据作为条目的优先级与可信度信号
随条目保留。

一句话口诀，追加到记忆模型判断口诀之后：

```text
顺滑干完的活                -> 最多 Daily，短索引，到期即忘
较劲过才干完的活            -> 教训必须落正式记忆，摩擦证据随行
```

## 2. 为什么要改

当前口径（`docs/agent-memory-model.md` §3.1 #9/#10/#11）已经写了正确方向：
一次性噪音不进长期记忆、实质收工进 Daily、可复用修法收工自记。但三处依赖
无证据的自觉判断，长程任务下会系统性失效：

1. **#11 靠 agent 自觉。**"是否可复用由 agent 判断"没有任何输入信号。
   实践中出现两种失败模式：顺滑流水被当成"经验"写进项目 `MEMORY.md`
   （膨胀，curation 一直在对抗）；真正较劲 8 轮才修好的问题反而没写
   （agent 修完如释重负，直接收工）。
2. **L2 self-review 提升无优先级依据。**冷路径从 Daily 提升"稳定内容"，
   但 Daily 条目之间没有任何区分度：一条"重试 8 次才定位到 fixture 冲突"
   和一条"改了个 typo"在提升排序里等权。
3. **长程 Goal 跨 Epoch 重复踩坑。**第 3 个 Epoch 的摩擦教训如果没有
   当场落盘，到 milestone 提取 evolution candidate 时已经稀释；
   下一个 Epoch 换了 session 的 agent 会原样重试。

摩擦是廉价、客观、daemon 已可观测的"这里有值得记的东西"信号。
本规格把它接进现有热路径规则、missed-write 兜底和 L1/L2/L3 冷路径，
不新建管道。

## 3. 摩擦信号定义

摩擦信号是**可数事件**，不是 LLM 打分。每个信号必须能指回具体事件来源。

| 信号 | 含义 | 检测来源 | 边界 |
| --- | --- | --- | --- |
| `human_correction` | 运行中的 turn 被人类新消息打断，或收工后紧邻的人类回复命中纠正句式（"不对 / 停 / 别这样 / 重来 / 换个方法"） | daemon message runtime 的 wake/interrupt 路径 | 追问、补充需求不算纠正；只数明确否定 |
| `action_rejected` | 工具调用被权限 / 审批 / 用户显式拒绝 | provider 事件流 rejection | 因超时未批不算 |
| `retry_loop` | 同一工具在同一 turn 内连续失败 ≥ 3 次 | daemon tool activity 流 | 参数实质不同的探索性调用不算；阈值可配 |
| `rework` | Issue 被评审驳回或 reopen | server 侧 `issue_review_stats` / issue 状态流 | 只在 Issue 关联任务上聚合 |
| `self_error_streak` | provider 自身报错后的连续自动重试 ≥ 3 次 | daemon provider 事件 | 单次瞬时错误不算 |

聚合形态是**原始计数向量**，不加权、不折算总分：

```json
{"human_correction": 2, "action_rejected": 0, "retry_loop": 1, "rework": 0, "self_error_streak": 0}
```

呈现时只列非零项（对齐 teamai"提示列出实际触发的非零信号"）。
全零即**顺滑（zero-friction）**。

### 3.1 方法摩擦与基础设施摩擦

摩擦教训必须区分两类，不得混写：

- **方法摩擦**：做法错了、理解错了、缺前置检查——教训可复用，
  进项目 `MEMORY.md` / `DECISIONS.md` 或 agent `memory/MEMORY.md`。
- **基础设施摩擦**：磁盘满、网络断、沙箱抽风——不是方法教训，
  机器特定事实写 `devices/<daemon-id>/STATE.md`，或不写。

对齐 Goal Gate 规格中 `HYPOTHESIS_REJECTED` 与 `OPERATIONAL_FAILURE`
不得折叠的原则：基础设施失败不得被当成方法教训写进可复用记忆。

## 4. 写入分层规则

### 4.1 热路径（当前任务内，agent 自己写）

在记忆模型 §3.1 基础上收紧两条：

1. **顺滑收工：流水只写 Daily。**保持现状 1–3 条短索引；**禁止**顺滑
   流水进入正式 `MEMORY.md` / `DECISIONS.md`。既有例外全部保留：显式
   "记住"、明确偏好句式、对接/交接安排、**项目/方案决策（#5）**——
   这些按内容类型路由，与摩擦无关，摩擦为零也立即写正式文件
   （现有 #1–#8 规则不变）。
2. **摩擦收工：教训必须落正式记忆。**#11 从"agent 自行判断"升级为：
   本 turn 摩擦向量非零时，agent 收工前必须写一条"根因 + 修法"到
   对应作用域的正式文件（方法摩擦），或明确判断为基础设施摩擦 / 
   无可复用教训（此判断本身写入 Daily 条目）。

Daily 条目携带摩擦标注，行内前缀，无 schema 变更：

```text
- [friction: retry_loop×8, human_correction×2] make check 连续失败根因是共享 fixture 冲突；修法已写 projects/<id>/MEMORY.md
- [friction: infra] 沙箱磁盘满导致构建失败，非方法问题，已记 devices STATE
- 改掉登录页 typo 并合并（顺滑条目，无标注）
```

### 4.2 兜底（扩展现有 missed-write guard）

复用 `memorysignal.DetectMissedWrite` → `agent_memory_curation_candidate`
管道，新增一个来源：

- 触发条件：本 turn 摩擦向量非零（方法摩擦），但没有任何 durable 文件
  写入（Daily 不算，与现有 missed-write 口径一致）；
- 生成候选：`metadata.source=friction_guard`，`shareable=false`，
  metadata 附摩擦向量与脱敏、单行化的触发上下文（复用
  `compactTrigger` 逻辑）；
- **每 turn 最多一条**（对齐 teamai"每 session 最多提示一次"），
  按现有 `dedupe_key`（scope+subject+kind+topic）去重；
- 顺滑 turn **不生成任何候选**，无论工具调用多少次。

### 4.3 重要内容的非摩擦通道（决策与人述重点）

摩擦门控只作用于**经验 / 教训 / 流水**类内容。以下两类重要内容按
内容类型路由，摩擦为零也必须当场写入，门控不得被用作不写的理由：

1. **人说的重要内容**：偏好 → `USER.md`；项目决定 → `DECISIONS.md`；
   群约定 → `CONTEXT.md`；明确"记住" → 写成功才能声称已记住。
   全部沿用现有热路径规则。
2. **agent 自己做出或发现的重要决策**：方案选择、约定裁决、
   不可逆取舍——无论过程多顺滑，收工同轮写对应作用域
   `DECISIONS.md`（或经 Wiki promote 挂 `derived_from` 边）。
   "顺滑评估三个方案后选了 B"是决策，不是流水。

**决策漏写兜底**：现有 missed-write guard 的触发句式从
"记住 / 以后都 / 别再 / 下次先"扩展到决策定稿措辞
（"就用 / 定了 / 决定 / 统一改成 / 以后一律"等，落在
`memorysignal.LooksLikeDurableFeedback` 同层扩展）。人拍板或 agent
在共享场宣布决定后，本轮无 durable 写入 → 生成
`metadata.source=decision_guard` 候选，管道与去重规则同
`friction_guard`，每 turn 最多一条。

### 4.4 冷路径（L1 / L2 / L4）

1. **L1 Daily 补齐**：只补事实索引，不因补齐而抹掉或伪造摩擦标注；
   agent 漏记的摩擦事件，L1 依据 daemon 侧摩擦向量补一条带标注的索引。
2. **L2 self-review 提升**：提升排序以摩擦标注为第一优先级证据——
   带 `[friction: …]` 标注的 Daily 条目和 `friction_guard` 候选优先
   整理进正式文件；**无摩擦、无显式记住/偏好命中的 Daily 条目默认
   不提升**，随历史 Daily 不加载的语义自然过期。这是产品预期行为，
   不是遗漏。
3. **L4 清理**：正式文件去重合并时，摩擦证据条目与普通条目冲突的，
   保留摩擦证据条目（有出处的教训优先于无出处的断言）。

### 4.5 团队层（L3 curator 与 evolution）

- `friction_guard` 候选保持 `shareable=false`，直到 L2 自审确认内容
  已脱敏且具跨 agent 复用价值，才可转共享候选；
- team curator 对共享候选排序时，摩擦向量作为优先级信号：
  被评审驳回两轮才通过的教训，优先于一次成型的心得；
- evolution（skill / workflow candidate）提取沿用现有 Goal milestone /
  Epoch commit 时机，但提取器**优先扫描摩擦标注条目**；
  摩擦向量随 candidate 保留为 provenance。

## 5. 数据合同

### 5.1 摩擦向量的落点

```text
daemon 侧（turn 级，事实来源）
  turn writeback metadata: friction: {human_correction, action_rejected,
                                       retry_loop, rework, self_error_streak}

agent 侧（可选自报，不另跑模型）
  sync_queue/memory-signal.jsonl 追加 action:"friction" 或 action:"decision" 行：
  {"action":"friction","kind":"method|infra","scope":"project",
   "topic":"fixture-conflict","summary":"共享 fixture 冲突需先 make db-reset"}
  {"action":"decision","scope":"project",
   "topic":"storage-engine","summary":"评估三方案后定 B：行级锁开销最低"}

候选侧
  agent_memory_curation_candidate.metadata:
    source: friction_guard
    friction: {…计数向量…}
    trigger_compact: "<脱敏单行上下文>"
```

daemon 计数是权威；agent 自报只用于补充语义（topic / kind），
两者冲突时以 daemon 计数为准。

### 5.2 预算不变

摩擦不是免罪牌。摩擦教训条目仍受现有凝练预算约束：正式记忆单条
≤180 字符、一条只写一个稳定事实；Daily 单条 ≤240 字符；文件软上限
不变。debug 全过程、命令输出、堆栈留在源任务，记忆只保留
"根因 + 修法 + 短引用"。

## 6. 与 Goal / Issue 的集成（Phase 2）

- Channel Goal checkpoint 增加摩擦向量聚合（本 checkpoint 周期内
  各 turn 之和），随 progress/evidence/blocker 一起持久化；
- Epoch commit 时，若本 Epoch 聚合摩擦向量非零且存在未落盘教训，
  coordinator 收到一次性提示（列非零信号 + 紧凑任务上下文），
  引导生成 evolution candidate；
- Issue `done` 且 `rework ≥ 1` 时，评审驳回理由与最终通过方案
  作为候选素材投影回 Issue 评论，人确认后进入候选队列
  （对齐协作五原则原则 1：写回证据本身成为知识来源）。

本节不改变 Goal completion、Graph frontier 或任何控制面裁决；
摩擦向量是观测数据，不参与依赖释放。

## 7. 迁移阶段

### Phase 0：信号采集（无行为变更）

- daemon 落 turn 级摩擦向量（打断 / 拒绝 / retry_loop / error_streak）；
- server 侧把 `issue_review_stats` 驳回轮次挂到关联任务；
- 只记录，不触发任何候选或提示；用真实分布校准 retry_loop 等阈值。

### Phase 1：门控生效

- 记忆模型文档 §3.1 #11 更新为摩擦门控口径；Daily 摩擦标注格式落地；
- `friction_guard` 候选来源上线（每 turn 最多一条）；
- L2 提升排序接入摩擦优先级；无摩擦 Daily 条目停止默认提升。

### Phase 2：Goal / Issue / evolution 集成

- checkpoint 聚合、Epoch commit 提示、Issue rework 提取，见 §6。

## 8. 验收场景

1. **顺滑长任务**：agent 200 次工具调用顺利交付。结果：Daily ≤3 条
   无标注短索引；无任何候选；正式文件零增长；次日起该内容不再注入。
2. **较劲任务**：`make check` 同 turn 连续失败 8 次，人纠正 2 次，
   最终定位共享 fixture 冲突。结果：agent 收工同轮在项目 `MEMORY.md`
   写入一条 ≤180 字符的根因修法；Daily 有 `[friction: retry_loop×8,
   human_correction×2]` 标注条目。
3. **较劲但漏写**：同上但 agent 未写任何 durable 文件。结果：产生
   exactly 一条 `source=friction_guard` 候选，`shareable=false`，
   metadata 含摩擦向量；次日 L2 消费并补写。
4. **显式记住**：摩擦为零，用户说"记住以后先跑 lint"。结果：现有
   路径不变，立即写正式文件；不因摩擦门控被降级到 Daily。
5. **基础设施摩擦**：沙箱磁盘满导致构建失败 5 次。结果：不产生
   方法教训候选；机器事实写 `devices/<daemon-id>/STATE.md`；
   Daily 标注 `[friction: infra]`。
6. **评审返工**：Issue 被驳回 2 轮后通过。结果（Phase 2）：Issue
   评论出现候选素材投影；候选 metadata 含 `rework: 2`。
7. **顺滑决策**：agent 零摩擦评估三个方案后选定 B。结果：收工同轮
   写项目 `DECISIONS.md` 一条；Daily 无 friction 标注；不产生候选。
8. **人随口拍板漏写**：人在群里说"行，就用 B 方案"，agent 本轮未写
   任何 durable 文件。结果：产生 exactly 一条 `source=decision_guard`
   候选；次日 L2 补写 `DECISIONS.md`。

### 不得发生

- 顺滑 turn 因工具调用次数多而生成候选或写正式记忆；
- 摩擦教训绕过凝练预算，把 debug 过程灌进 `MEMORY.md`；
- 基础设施失败被写成项目方法教训；
- 同一 turn 生成多条 friction_guard 候选；
- `friction_guard` 候选未经自审确认直接进入团队共享；
- 摩擦计数被用作 agent 绩效或排名口径；
- 以"过程顺滑"为由跳过决策、偏好或显式记住的当场写入
  （门控只挡流水与经验提升，不挡按内容类型路由的重要内容）；
- 摩擦向量参与 Goal completion、依赖释放或任何控制面裁决；
- L1 冷路径为 inactive agent 强行补跑摩擦扫描（沿用现有 active-only 规则）。

## 9. 成功指标

- 正式记忆文件（agent / project `MEMORY.md`、`DECISIONS.md`）的
  月增长率下降，同时 L2 提升条目的"摩擦证据占比"上升；
- friction_guard 候选的次日 L2 消化率接近 100%；
- 同一项目内，相同 `dedupe_key` 的摩擦教训重复产生率下降
  （同一坑不再反复踩后反复记）；
- 长程 Goal 中，跨 Epoch 重复出现相同 retry_loop 签名
  （同工具 + 同失败模式）的次数下降；
- Daily 文件保持在软上限内，顺滑内容零提升引发的用户投诉为零
  （显式记住路径未受影响的旁证）。

## 10. 非目标

- 不修改记忆检索、注入排序、graph memory recall 或任何召回行为；
- 不引入 LLM 摩擦打分、情绪分析或"任务难度"主观评估；
- 不新建第二套候选 / curation 管道（复用 memorysignal 与
  `agent_memory_curation_candidate`）；
- 不把摩擦数据用于 agent 考核、模型对比或计费；
- 不照搬 teamai-cli 的 Git 分发、MR 评审或 CLI 交互形态；
- 不要求 inactive agent 补跑历史摩擦分析。

## 11. 与相关文档的边界

| 文档 | 关系 |
| --- | --- |
| `docs/agent-memory-model.md` | 基线。本规格收紧其 §3.1 #9/#10/#11 与 §7 提升规则；落地时更新该文档判断口诀。 |
| `docs/superpowers/specs/2026-08-07-goal-gated-work-graph-simplification-spec.zh-CN.md` | Goal / Epoch / evidence 边界以其为准；本规格只向 checkpoint 附加观测数据。 |
| `docs/superpowers/specs/2026-08-06-long-horizon-control-plane-memory-skill-evolution-spec.zh-CN.md` | evolution candidate 提取时机以其为准；本规格提供提取优先级信号。 |
| `docs/multi-agent-collaboration-principles.md` | 原则 1"写回证据才算做完"是 §6 Issue rework 提取的产品依据。 |
| `docs/superpowers/specs/2026-08-17-graph-memory-scope-design.md` | graph memory 存储与路由不受本规格影响；graph 模式下摩擦门控同样约束"什么内容值得写入"。 |
