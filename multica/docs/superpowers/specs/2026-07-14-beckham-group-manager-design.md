# 贝克汉姆（Beckham）群管理 Agent 设计

日期：2026-07-14
状态：草案 — 待确认关键决策（见 §3）后进入实施计划

## 1. 目标

把"主动发现 / 主动交流 / agent 灵动性"这类**群内协调**能力，从 Wendy 拆分给一个新的**群管理 agent**（默认名 **贝克汉姆 / Beckham**）：

- 建立群聊时，默认加入贝克汉姆。
- 贝克汉姆负责：监控群聊、在需要协调时主动 @agent / @人、换手播报（unlock / start_work / 受阻 / 返工）、群内引导（facilitator）——即当前 Wendy 在群里做的一切主动行为。
- **Wendy 回归 HR 职责**：私聊组队、招聘、人事，不再监控群、不再在群里主动协调。
- 所有"体现 agent 灵动性"的主动功能都归属贝克汉姆。

验收（行为级）：
1. 新建 group → 贝克汉姆自动成为成员且可运行。
2. 群里有新消息 → 由**贝克汉姆**（而非 Wendy）在 debounce 后做主动发现；「主动发现」徽章归贝克汉姆。
3. issue 依赖解锁 / 新可开工 → 由贝克汉姆在群里 @ 对应 agent。
4. Wendy 在群里不再产生主动消息；私聊里仍做组队/招聘。
5. 现有工作区回填：已存在的 group 也加入贝克汉姆，监督绑定切到贝克汉姆。

非目标：贝克汉姆自己写代码/关 issue；固定 workflow；外部 IM 回写；改动 §当前 PR #570 已修的 ambient 可靠性机制（本设计直接复用）。

## 2. 现状（与本设计相关）

- **监督绑定**：`workspace_radar_state.supervisor_agent_id` 目前指向 Wendy。`workspace_radar_task_is_authorized` 用它给 ambient(event) 和 scheduled radar 任务放行。
- **主动发现**：`wendy_channel_ambient` + `DispatchDueWendyAmbientReviews` + `BuildAmbientChannelPrompt`（"You are Wendy, the workspace supervisor…"）+ `resolveWendyAmbientAgentForChannel`（PR #570 后：优先 supervisor，否则按名字确定性回退）。
- **换手**：`pending_handoff` + `DispatchDueWendyHandoffs` + workgraph detect_unlock / detect_start_work，目标 actor 是 waiter 的 owner，发言者是 supervisor。
- **触发点**：`ingestWendyHumanGroupMessage` / `ingestWendyAgentGroupMessage` 在群消息时 touch ambient；建群/加人在 channel 创建与成员处理。
- **agent 身份**：agent 表**无角色列**；Wendy 靠名字集合识别（"Wendy"/"Windy"/"Joe"）。这是 #3 脆弱性的根因。
- **Wendy 私聊人设**：`windyInstructions`。

一句话：Wendy 现在**同时**是 HR + 群督导。本设计把"群督导"整体搬到贝克汉姆，并用**角色标记**取代名字识别。

## 3. 关键决策（已确认 2026-07-14）

**D1. 粒度 = 每群一个、且仅一个（已确认）。**
每个 group 拥有自己专属的贝克汉姆 agent（**各群独立实例**，不是全工作区共享一个）。建群时创建，随群存在；一个群里有且只有一个群管理 agent。

**D2. 用"角色标记"识别，与名字解耦（已确认，实现细节由我定）。**
给 `agent` 增加受管角色标记 `managed_role`（值 `group_manager`）。某个群的贝克汉姆 = 该频道里 `managed_role='group_manager'` 的成员（一群一个，天然唯一）。识别与显示名无关——**改名不影响功能**，也彻底不再用名字集合（根治旧 #3）。

**D3. 主动/协调能力的"负责人"切成贝克汉姆（已确认）。**
ambient 复盘、workgraph 换手、facilitator、群内主动 @ 的发言主体，全部解析成"该频道的贝克汉姆"。ambient 的 event 授权本就按"频道成员"校验（#563），贝克汉姆作为频道成员天然被授权；handoff 派发的发言者解析改为"按频道取贝克汉姆"。

**D4. 贝克汉姆是全新 agent，与 Wendy 无关（已确认）。**
不复用 Wendy 的名字集合 / `EnsureWindy` / 供给逻辑；独立的 `EnsureGroupManagerForChannel`。默认显示名「贝克汉姆」，可改名。Wendy 保留私聊 HR（组队/招聘）与被动被 @ 回复，**不再有任何群内主动行为**。

**D5. 存量群回填 = 待你拍板（见 §8）。**
新建群一定加贝克汉姆；对功能上线前"已存在"的群是否补加，见 §8 唯一待决项。

## 3b. "搬迁"与"回填"是什么（术语澄清）

- **搬迁（迁移边界）**：指把"谁在群里主动说话"从 Wendy 换成贝克汉姆。**搬走**给贝克汉姆的能力：群消息监控+主动发现、issue 解锁/可开工时在群里 @ 对应 agent、群内引导、主动 @人/@agent。**留给 Wendy**：私聊里的组队/招聘/人事，以及别人 @Wendy 时的被动回复。搬迁只换"由谁触发/由谁发言"，底层机制（PR #570 硬化过的 ambient 生命周期、workgraph 换手）不变。
- **回填**：功能上线前工作区里**已经建好的群**，不会自动有贝克汉姆（因为它们建群时还没这套逻辑）。"回填"= 跑一次性任务，给这些老群补建并加入贝克汉姆、把发言权切过去。不回填的话，只有上线**之后新建**的群才有贝克汉姆，老群保持现状（Wendy 或无人主动）。

## 4. 目标架构

```
建 group / 群消息 / issue 事件
      │
      ▼
resolveGroupManagerForChannel(channel)   ← 该频道里 managed_role='group_manager' 的成员
      │                                     取代 resolveWendyAmbientAgentForChannel（不再靠名字）
      ├─ ambient 复盘（复用 PR #570 硬化后的 wendy_channel_ambient 生命周期）
      ├─ workgraph 换手（pending_handoff，发言者=该频道贝克汉姆）
      └─ facilitator / 主动 @
      │
      ▼
可见发言（群消息 / issue 评论），@member 或 @agent；同事务唤醒 agent
      │
      ▼
授权：workspace_radar_task_is_authorized 的 ambient event 分支已按"频道成员"校验，
      贝克汉姆作为频道成员天然被授权（无需工作区级监督绑定）
```

Wendy 侧只保留私聊 HR 与被动回复。

## 5. 影响面（代码）

| 区域 | 变更 |
|------|------|
| 迁移 | `agent.managed_role TEXT`（NULL/`group_manager`）；建群回填任务（见 §8） |
| 供给 | 新增 `EnsureGroupManagerForChannel(channel)`：创建该群专属贝克汉姆 agent（独立于 `EnsureWindy`，独立 runtime 供给），加为频道成员，标记 `managed_role='group_manager'` |
| 建群/加人钩子 | group 创建后自动 EnsureGroupManagerForChannel；保证一群一个（唯一性约束/幂等） |
| ambient | `resolveWendyAmbientAgentForChannel` → `resolveGroupManagerForChannel`（按角色，不靠名字）；`ingestWendy*GroupMessage` 改用之；`BuildAmbientChannelPrompt` 人设改贝克汉姆群管理 |
| handoff | dispatch 发言者解析改"按频道取贝克汉姆"（handoff 落在某频道/issue） |
| 授权函数 | ambient event 分支已是频道成员校验，基本无需改；仅确认贝克汉姆非归档、有 runtime |
| 人设 | 新增贝克汉姆 persona；`windyInstructions` 删除"督导群聊"表述、回归 HR；移除 Wendy 群 auto-watch |
| 唯一性 | 需保证一群仅一个 `group_manager` 成员（部分唯一索引：channel_member 上 member 为 group_manager 的行 ≤ 1，或在供给层幂等保证） |

## 6. 分阶段交付（详见 plan 文档）

- **Phase 0 — 角色骨架**：D2 角色标记 + `EnsureGroupManager`（不改行为，仅能创建/识别贝克汉姆）。
- **Phase 1 — 自动加入 + 回填**：建 group 自动加入；回填现有 group；切监督绑定；授权函数改判。
- **Phase 2 — 迁移主动能力**：ambient/handoff/facilitator 的解析与发言主体从 Wendy 切到贝克汉姆；prompt 人设更新。
- **Phase 3 — Wendy 收敛为 HR**：更新 `windyInstructions`；移除 Wendy 群 auto-watch；回归测试确认 Wendy 群内静默、贝克汉姆接管。

每阶段独立可测、可灰度；Phase 2 依赖 PR #570 的 ambient 硬化已在 dev。

## 7. 风险

| 风险 | 缓解 |
|------|------|
| 回填期双督导（Wendy+贝克汉姆同时发言） | Phase 1 切绑定与 Phase 2 迁移同一批次上线；切换后 Wendy 解析立即失效 |
| 授权函数改动影响 claim | 保留 ambient event 分支逻辑，仅换绑定来源；带迁移测试 |
| 存量工作区无 runtime 供贝克汉姆运行 | 复用 Wendy 的供给策略；无 runtime 时 ambient 自然跳过（PR #570 已容错） |
| 用户已把 Wendy 拉进群并习惯 | 保留 Wendy 可被动 @；仅停其"主动"行为 |

## 8. 存量群策略（已确认：只管新群）

- **新群**：创建时自动供给并加入该群专属贝克汉姆（自动化）。
- **老群**：不动、不批量回填。用户可**手动邀请**贝克汉姆进群——即一个按需触发的入口，调用与新群相同的 `EnsureGroupManagerForChannel(channel)`。
- 移除后不强制拉回（已确认）。
- 因此**无回填迁移**；实现 = 新群钩子（自动）+ 手动邀请入口（按需）。
