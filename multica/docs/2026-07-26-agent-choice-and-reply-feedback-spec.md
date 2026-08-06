# Agent Choice Cards & Reply Feedback Spec

Written: 2026-07-26  
**Updated: 2026-07-27 (LRM-613)** — Reply feedback（消息下方独立 👍/👎）已移除；训练/标注改用消息 emoji reaction，SQL 过滤 `actor_type != 'agent'`。下文 Feature B 仅作历史记录。  
Status: Choice 有效；Reply feedback **superseded**  
Target branch: `LRM-Teams/multica` `dev`  
Owner: Multica Dev 全栈工程师（实现）；产品口径见下方决策

## Summary

两件平台原生能力，共用「结构化消息 + 可点 UI + 回写事件」模式，**不绑死某一家运行时**：

1. **Choice（交互选项卡）**：Agent 需要人类拍板时，由**模型自行判断**弹出是否（横排）或 2–4 项纵向选项；点击后变成用户回复并唤醒后续任务。
2. **Reply feedback（终答点赞/点踩）**：Agent 终答旁提供 👍 / 👎（可选点彩），落库为消息级偏好标签，供 reward / 非参自进化 / 训练取样；**不是**现有群聊社交 reaction 的替代品。

本文定义协议、布局、跨运行时接入、验收与明确不做项。实现以本 spec + 关联 issue 验收为准。

---

## Motivation

### Choice

- Claude / CodeBuddy 已 `--disallowedTools AskUserQuestion`：daemon 非交互 stream **没有渲染位**，原生 AskUserQuestion 会静默「被猜掉」（#2588）。
- 工作区大量「方案 A/B」拍板、设计门、是否开修 / 开 PR，今天全靠散文问答。
- 手机端需要可触控卡片（≥32px），而非只靠打字。

### Reply feedback

- 群聊 / issue 已有 emoji reaction（社交）。
- `/api/feedback` 是产品意见箱，**不是**「某条 agent 回复好不好」。
- evolution / training 已有 feedback event / reward 管线，缺 **人对 agent 终答一键打标** 的入口。

---

## Goals / Non-goals

### Goals

- Multica 统一契约：任意运行时（Cursor / PI / Claude / OpenCode / …）只要能调 Multica 工具或产出同一 `MessagePart`，即可使用。
- 模型决定**是否**弹选项、弹哪种布局；平台不写死业务规则（如「调研完必弹是否」）。
- 选项点击 → 用户可见回复 + 可关联原 choice → 唤醒 agent。
- 终答 👍/👎 → 持久化偏好，可导出到 evolution / reward；负向可选触发反思（后置，不挡 v1）。

### Non-goals (v1)

- 不重新启用各家私有 `AskUserQuestion` UI。
- 不做自由文本表单 / 日期选择器 / 多选矩阵（可后续扩展）。
- 不把点赞点踩做成强制问卷；不默认每次终答自动弹出说明文案刷屏。
- 不在 v1 把负反馈自动改模型权重（只落标签 + 可选反思任务）。
- 群聊里不解决「全员都能点且互相抢答」的复杂权限（见权限一节默认）。

---

## Feature A — Choice cards

### A.1 Wire format (`MessagePart`)

新增 part（名称可实现时微调，语义固定）：

```ts
{
  type: "choice",
  choice_id: string,          // 稳定 id，点击回写携带
  prompt: string,             // 短问题文案
  layout: "binary" | "list",  // binary=横排是否；list=纵向 2–4 项
  options: Array<{
    id: string,               // 稳定 option id
    label: string,            // 按钮文案
    description?: string,     // 可选副文案（list 可用）
  }>,
  allow_dismiss?: boolean,    // 默认 true：可忽略不点
  expires_at?: string,        // 可选；过期后只读
}
```

约束：

- `layout: "binary"`：恰好 2 个 options（常见「是 / 否」）。
- `layout: "list"`：2–4 个 options（实现可硬限制 max 4）。
- 同一条 assistant 消息可带 0..1 个 choice part（v1 禁止一条消息堆多个 choice，避免手机挤爆）。
- choice 可与 `text` 共存：上文说明 + 下方卡片。

### A.2 Agent 如何发出（跨运行时）

**平台工具（推荐）**，例如：

```text
multica chat ask-choice --prompt "…" --layout binary|list --option id=yes,label=是 --option id=no,label=否
```

或等价 MCP tool `ask_choice`，daemon 将其转成带 `choice` part 的可见消息（与 `message send` / chat final parts 同一落库路径）。

运行时接入原则：

| 运行时 | 接入方式 |
|--------|----------|
| Claude / CodeBuddy | 继续禁用原生 AskUserQuestion；挂 Multica MCP/`multica` CLI |
| Cursor / PI / OpenCode / … | 同样挂 Multica 工具；不依赖厂商私有 question UI |
| 无工具能力的后端 | 可解析约定结构化 JSON → 服务端规范化为 `choice` part（后置，非 v1 必须） |

提示词只给**使用指导**（何时该问、勿滥弹），**不**用代码 if/else 强制弹出。

### A.3 点击回写

用户点某一 option：

1. 客户端 POST 选择。v1：**允许改选一次**（`select_count`：1=首选仍可改，2=锁定）；同选项再点幂等不产生新回复。
2. 服务端插入一条 **user** 消息，内容为「选择：…」或「改选：…」，并带 `choice_reply` part：`{ choice_id, option_id, label }`。
3. 原 choice 卡片高亮当前选项；`select_count < 2` 时可点其它项，`>= 2` 后锁定。
4. 按会话类型唤醒 agent（chat pending task / channel mention 同等现有唤醒路径）。

与 freshness：点击产生的用户消息走正常消息管道；若 agent 仍在跑，遵循现有 freshness / 排队语义，不另造并行通道。

### A.4 UI

- **binary**：同一行两个等宽触控按钮，≥32px 高；手机不溢出气泡。
- **list**：纵向堆叠 2–4 卡；主 label + 可选 description。
- 触屏勿仅 hover；气泡内渲染，不撑破手机 sheet（对齐现有 DM 气泡约束）。
- 无障碍：按钮有明确 `aria-label`；选中态可见。

### A.5 权限（v1）

- **Agent DM / 私聊气泡**：会话参与人类可点。
- **群聊**：仅触发该轮的人类（或消息作者）可点；他人只读。复杂「任何人可点」留后置。

---

## Feature B — Reply feedback (👍 / 👎)

### B.1 产品形态

- 出现在 **agent 终答**（assistant 消息，含 chat / DM 气泡；群聊 channel agent 消息 v1 一并支持更佳）。
- 控件：👍、👎；可选第三态「点彩」映射为强正向（或 v1 只用两态，点彩 = 👍 的视觉变体——实现任选其一，验收以两态语义为准）。
- 每人每条消息至多一个有效偏好（再点可取消或切换；需幂等）。

### B.2 数据模型（逻辑）

```text
reply_feedback (
  id,
  workspace_id,
  message_id,          -- chat_message 或 channel_message，用 message_kind 区分
  message_kind,        -- chat | channel
  task_id?,            -- 若有
  agent_id?,
  actor_user_id,
  value,               -- +1 | -1
  created_at, updated_at
)
unique (message_kind, message_id, actor_user_id)
```

事件：写入后发 realtime（便于 UI 同步）；并记入可被 evolution / training 消费的投影（可复用或旁路 `evolution_unit_feedback_event`，metadata 带 `source=reply_feedback`）。

### B.3 与现有 reaction 的关系

| | 社交 reaction | Reply feedback |
|--|--|--|
| 用途 | 表情互动 | 质量标签 / reward |
| Emoji | 任意 | 固定 👍/👎（语义化） |
| 训练 | 不默认进 reward | **默认进** 偏好样本 |

v1 可用独立表；不要把「训练标签」 silently 混进任意 🎉 反应里不可解析。

### B.4 下游使用（契约，非本 issue 必须实现训练环）

- `value=+1` → 正样本 / reward 上修信号。
- `value=-1` → 负样本 / reward 下修；**可选**后置：创建「反思」类 follow-up task（v1 可只落库 + API，反思开关默认关）。
- 导出字段至少：`message_id, agent_id, task_id, value, actor_user_id, created_at`。

### B.5 UI

- 终答下方或气泡角：两个触控目标 ≥32px；选中态清晰。
- 不挡正文、不误触发送。
- 未登录 / 无权限不展示。

---

## Cross-cutting

### 安全与滥用

- Choice：限制 options 数量与 label 长度；防 XSS（纯文本渲染）。
- Feedback：每用户速率限制；禁止 agent 给自己打标。
- 审计：保留 actor、时间、关联 message/task。

### 观测

- Choice 弹出次数、点击率、dismiss 率。
- Feedback +/- 比例、按 agent / 按日聚合（可进 evolution metrics 后置）。

### 文档 / 提示

- Agent runtime brief 增加短节：何时用 ask-choice；勿滥弹。
- 明确：点赞点踩由人类发起，agent 不要乞求评价。

---

## Implementation phases

### Phase 1（本 issue 范围）

1. Spec 评审通过（本文）。
2. `choice` MessagePart + 发送路径（CLI 或 MCP 其一即可先通 chat/DM）。
3. 前端：气泡 / chat 渲染 binary + list；点击回写 + 锁定。
4. `reply_feedback` API + 终答 👍/👎 UI（chat/DM 优先）。
5. 测试 + `pnpm react:doctor`（前端变更）绿；PR → `dev`。

### Phase 2（后置，可拆单）

- 全运行时 MCP 挂载清单与兼容矩阵。
- 群聊 choice 权限扩展。
- 负反馈自动反思 task。
- Feedback 进 training reward 实装（非仅落库）。
- Choice dismiss / 过期（改选一次已在 v1）。

---

## Acceptance criteria (issue 可直接挂载)

1. **协议**：存在文档化的 `choice` part 与 `reply_feedback` 字段约定（本文或实现旁路 OpenAPI/类型），且不依赖 Claude 原生 AskUserQuestion。
2. **跨运行时口径**：至少一条路径（`multica` CLI 或 MCP）能发出 choice；文档写明 Cursor/PI/Claude 均走该路径，而非各家私有 UI。
3. **Choice UI**：binary 横排两键、list 纵排 2–4 项；手机触控 ≥32px；首次点选后可改一次，第二次改选后锁定并产生用户可见回复。
4. **Choice 唤醒**：点选后 agent 能收到选项上下文并继续（chat/DM 至少一端验收）。
5. **模型自判**：平台无「固定场景强制弹窗」硬编码；仅工具 + 提示指导。
6. **Feedback UI**：agent 终答可见 👍/👎；可切换或取消；状态刷新正确。
7. **Feedback 持久化**：落库可按 message 查询；带 `agent_id`/`task_id`（有则写）；不与随意 emoji reaction 混为同一语义。
8. **验证**：单测覆盖 part 解析 / feedback 幂等；前端变更 `pnpm react:doctor` 相对 `origin/dev` 0 issues；PR 合入 `dev`。

---

## Open questions (已拍板 2026-07-26)

1. Choice：**允许改选一次**（`select_count` 1→2 后锁定；同选项再点幂等）。
2. 点赞点踩：👍 / 👎；**点彩 = 👍 皮肤**（仅 +1 / -1）。
3. 群聊 agent 终答：**v1 一并带** `reply_feedback`（与 DM 同控件）。

**实现默认**：上列三条已锁定，不再使用「选定即死」。
