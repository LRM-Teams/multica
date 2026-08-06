# Wendy 工作关系图督导（换手播报）设计

日期：2026-07-14  
状态：已确认；Phase A 实施计划见 `docs/superpowers/plans/2026-07-14-wendy-work-graph-phase-a.md`

## 1. 目标与验收

Wendy 是工作区总管：掌握全局任务与协作关系，**自己不处理具体工作**，只在「该谁开始 / 该谁停下 / 该谁返工」时，在用户可见位置 `@人` 或 `@agent`。

验收以行为为准（对应已确认场景）：

1. a、b 并行开工，c 等 a+b，d 等 c，且状态正确时，Wendy **不说话**。
2. a 完成、b 仍在进行时，Wendy **不说话**。
3. b 也完成后，Wendy **及时 @c** 开始（阻塞解除）。
4. c 受阻且根因在 a/b 时，Wendy **@a/@b 修复**，标明 c 被堵住；**不催 c 硬推，不叫 d**。
5. a/b 修好后，Wendy **@c 继续**；c 完成后 **@d 开始**。
6. 人在群里指出 c 需修改且 d 已在做时，Wendy **尽快 @d 停止并等待，@c 按指出点修改**。
7. 无新信号、关系图无「需要换手」的边时，**不调用模型**（省 token）。
8. 催促可重复，但必须先看进展：有进展则带着进展催；无进展则问原因。
9. 正式 Issue 与群内口头承诺都进入关系图；监控范围以 Wendy 作为总管的工作区全局为准，发言落在相关群或 Issue 评论。

非目标（本版不做）：

- Wendy 自己改代码、关 Issue、建完整 workflow 引擎并强制执行。
- 固定预设 handoff 链（A→B→C 不可改写）作为主路径。
- 全员 agent 定时 Radar（已由 #527 否决；本设计继续 Wendy-only）。
- 外部系统（飞书/Slack/GitHub）写回。

## 2. 与现状差距

| 能力 | 现有 #527 Wendy Radar | 本设计 |
|------|----------------------|--------|
| 角色 | 工作区定时督导 | 事件驱动的换手播报总管 |
| 状态模型 | 变更日志 + 整仓上下文 | **工作关系图**（事项、依赖、阻塞、返工） |
| 何时跑 LLM | 预算内有变更则跑 | 仅当出现「需要换手」或慢车道卡住 |
| 发言对象 | 仅 `@agent` | `@人` 与 `@agent` |
| 正确等待 | 可能误催 | **明确不说话** |
| 中途返工打断 | 无 | 快车道打断在飞依赖方 |
| 私聊人设 | HR 组队，不知督导 | 需同步说明总管能力（另改 instructions） |

未合入的「可见自主 / 可靠 handoff」草稿：可见同事务发布仍可复用；**固定 handoff 状态机不作为主模型**，最多作为关系图上的一种可选边来源。

## 3. 核心模型：工作关系图

### 3.1 实体

**WorkNode（事项）**

- `id`, `workspace_id`
- `kind`: `issue` | `chat_commitment` | `agent_task`（首版至少支持 issue + chat_commitment）
- `title`, `description`（短）
- `owner_type`: `member` | `agent` | `unassigned`
- `owner_id`（可空）
- `status`: `active` | `waiting` | `blocked` | `done` | `needs_rework` | `cancelled`
- `primary_channel_id`（发言落点；可空则回退 Issue 评论）
- `linked_issue_id`（口头事项后来绑到 Issue 时填充）
- `last_progress_at`, `last_progress_summary`
- `last_wendy_nudge_at`, `last_wendy_nudge_kind`
- `created_at`, `updated_at`

**WorkEdge（关系）**

- `from_node_id` → `to_node_id`
- `kind`: `waits_on` | `blocked_by` | `rework_of`
- `status`: `open` | `resolved`
- `evidence`（消息/评论/任务 id）
- `updated_at`

**WatchSignal（廉价信号，不调模型）**

- 来源：群消息、Issue 状态/评论、agent task 终态、成员/@ 提及
- 写入：更新相关 node 的 `last_progress_at` / 候选边；或标记 `graph_dirty`

**PendingHandoff（该说话的意图，调模型前的队列）**

- `urgency`: `fast` | `slow`
- `reason_code`: `unlock` | `block_route` | `interrupt_stop` | `stalled_ask_why` | `progress_nudge`
- `target_actor`（人 or agent）
- `related_node_ids`
- `channel_id` / `issue_id`（发言表面）
- `dedupe_key`
- `not_before`（debounce）
- `status`: `pending` | `claimed` | `done` | `cancelled`

### 3.2 不变量

- Wendy **永不**成为 WorkNode 的执行 owner。
- 打开的 `waits_on` 且前置未完成 → 等待方状态应为 `waiting`；Wendy 不得对其发「开始干活」类催促。
- `needs_rework` 打开时，所有以该 node 为前置且已 `active` 的下游，应产生 `interrupt_stop` PendingHandoff。
- 同一 `dedupe_key` 在未出现新进展前不得重复入队 fast 意图。

## 4. 何时说话（决策表）

| 条件 | 是否入队 | urgency | 话术方向 |
|------|----------|---------|----------|
| 依赖方正确 waiting，前置未齐 | 否 | — | — |
| 前置全部完成，等待方仍 waiting | 是 | fast | `@等待方` 可以开始 |
| 执行方 blocked，根因指向他人 | 是 | fast | `@根因方` 修复；说明谁被堵 |
| 人指令：某事项需修改，下游 active | 是 | fast | `@下游` 停止等待；`@责任方` 修改 |
| 该动却无进展超过阈值 | 是 | slow | 有进展→带进展催；无进展→问原因 |
| 仅聊天热闹但图无换手需求 | 否（可只更新证据） | — | — |

慢车道默认 debounce：**约 10 分钟**（自该事项相关信号起算）。  
快车道：完成解锁、明确受阻路由、人下令打断 → **0～2 分钟内**（实现上可「信号后立即入队，调度器尽快领取」）。

## 5. 运行时架构

```text
Domain events
    │
    ▼
SignalIngestor (无 LLM)
    │  更新 WorkNode/Edge，标记 dirty
    ▼
HandoffDetector (规则为主，必要时小范围 LLM 抽边)
    │  写入 PendingHandoff(fast|slow)
    ▼
WendyDispatcher
    │  debounce / budget / dedupe
    ▼
Wendy Composer (LLM)
    │  只吃：PendingHandoff + 相关 node/edge + 少量证据
    ▼
VisiblePublisher
    │  群消息或 Issue 评论，@member 或 @agent
    │  与 inbox wake（仅 agent）同事务
    ▼
回写 last_wendy_nudge_* ，resolve/cancel 意图
```

与 #527 关系：

- **保留** Wendy-only、禁止其它 agent scheduled Radar、可见发言、同事务唤醒 agent。
- **替换**「整仓 Markdown 上下文 + 小时预算扫一次」为「关系图 + PendingHandoff 驱动」。
- 现有 `workspace_radar_change` 可作为 SignalIngestor 的输入源之一，逐步收敛到按 node 粒度。

## 6. 图如何更新（无固定 workflow）

### 6.1 廉价路径（默认）

- Issue 创建/分派/状态变更 → upsert issue 型 WorkNode，同步 owner/status。
- Agent task 终态 → 推进 linked node `last_progress_at`；若 done 则尝试 resolve 指向它的 `waits_on`。
- 群内结构化依赖（若 agent/人类使用明确格式或已有 issue 链接）→ 写 `waits_on`。
- Wendy 自己的催促产物 → 记入 directive artifact，**不**作为「他人进展」。

### 6.2 需要理解自然语言时

触发条件（控制 token）：

- 新群消息后 debounce，且消息命中启发式（依赖词、停止、返工、等待、@ 多人任务分配）；或
- fast 路径上证据不足无法选 target。

动作用途：**只更新图或生成 PendingHandoff 草案**，不是每条消息都全文复述给大模型。

首版可接受「高置信才建 chat_commitment / edge；低置信只挂观察」以免乱 @。

### 6.3 人的突发指令

人在群里的修正（如「c 这块不对，d 先别做」）优先走启发式 + 短上下文抽取 → `needs_rework` + `interrupt_stop`，进入快车道。

## 7. 发言与权限

- `@agent`：沿用 #527 可见消息 + inbox/task wake。
- `@member`：频道内可见 mention（并走现有成员通知/未读）；**不**假装给人类派 agent task。
- Wendy 必须是发言频道成员；全局读 Issue/任务不要求她在每个群，但**在某群发言**要求在该群。
- 总管若尚未进关键协作群：可先 Issue 评论 @agent，或（后续）系统提示人把 Wendy 拉进群；本版不自动拉人进群。

## 8. Token 与预算

- 无 PendingHandoff 到期 → **0 LLM**。
- Composer 每次只带相关子图（目标 node、直接前后置、最近 N 条证据），禁止整仓群历史灌入。
- 工作区级安全阀：例如滚动 1 小时最多 N 次 Composer 调用（与 #527 同量级可调）。
- 慢车道合并：同一 channel 窗口内多个 slow 意图可合并为一次 Composer。

## 9. 私聊人设

更新 `windyInstructions`：明确 Wendy 已是工作区总管；后台按关系图换手播报；用户**不必**在私聊配置「每天 10 点规则」；具体执行仍由对应人/agent 完成。  
私聊仍可做组队/招聘，但不得否认已有督导能力。

## 10. 分阶段交付

### Phase A — 骨架（可演示「解锁就 @」）

- WorkNode/Edge/PendingHandoff schema
- Issue + agent task 信号 → 图
- 规则检测 `unlock` / 简单 `waits_on`：优先同步已有 Issue 依赖（如 `depends_on` / parent 关系）到 WorkEdge；若场景依赖尚未落在 Issue 字段上，测试与 agent 可通过写 edge / 声明依赖补齐。**Phase A 不靠全文 NLP 猜依赖**
- fast Composer + 仅 `@agent`
- 场景 1–3、5（无返工）可测

### Phase B — 受阻与返工

- `blocked` / `needs_rework` / `interrupt_stop`
- 群消息启发式 + 受限 LLM 抽边
- `@member`
- 场景 4、6

### Phase C — 口头承诺与慢车道催促

- `chat_commitment` 抽取
- slow debounce 10 分钟、有进展/问原因话术
- 收敛替换旧整仓 Radar prompt 路径
- 更新 Wendy 私聊 instructions 与简单可观测面板（可选：频道侧「Wendy 关注」列表）

### Phase D（非本设计必做）

- 关系图 UI、人工改边、跨工作区、外部 IM。

## 11. 测试场景（必须自动化的核心）

用 fixture 工作区回放事件序列，断言 PendingHandoff 与可见消息：

1. 并行正确等待 → 无 Wendy 消息  
2. a done 仅 → 无 @c  
3. a+b done → 恰好一次 @c unlock  
4. c blocked by a/b → @a/@b，无 @d  
5. a/b fixed → @c resume  
6. c done → @d start  
7. 人下令 c rework while d active → @d stop + @c rework  
8. 无信号 → 无 LLM 调用计数增加  

## 12. 风险与缓解

| 风险 | 缓解 |
|------|------|
| 自然语言抽边误报 | 高置信才写边；低置信只观察；人可在群里纠正 |
| 刷屏 | dedupe_key + 无新进展不重复 fast；慢车道合并 |
| 与旧 Radar 双轨 | Phase C 关掉整仓 scheduled prompt，仅留 WendyDispatcher |
| 人不在群 / Wendy 不在群 | 发言表面回退 Issue；文档说明拉 Wendy 进协作群 |

## 13. 决策记录（已确认）

- 事项范围：正式 Issue + 口头承诺（C）。
- 重复催促：允许，但先看进展再决定话术。
- Wendy = 总管，全局信息，不干具体事，只找人。
- 场景中「说/不说」判断表已获产品认可。
- 不要固定预设 workflow；要事件驱动、可改写的关系图。
