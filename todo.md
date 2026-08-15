# 笔记 ↔ 多 Agent 协作场 — 实现 Todo

## 给实现 AI 的读法（先读这段）

1. **严格按 Slice 顺序做**：完成 Slice N 的全部 checkbox 并满足「完成标准」后，再开 Slice N+1。
2. **一次只做一个 `- [ ]` 任务**：开干前在任务下写「进行中」说明；做完勾成 `- [x]`。
3. **每个任务块都自包含**：含目标 / 要做 / 不要做 / 依赖 / 完成标准。缺信息时先查「代码锚点」与合同文档，再查 `CLAUDE.md`，不要臆造合同。
4. **已定决策是硬约束**，不可在实现时改口径；若代码做不到，停下来问人，不要静默放宽。
5. **测试跟代码走**：共享逻辑测 `packages/core` 或 `packages/views`；Go 测 `server/`。改前端后按仓库要求跑相关检查（用户要求时再跑全量 `make check`）。
6. **复用现有多 Agent 能力，禁止另起编排引擎**：频道协作、临时协调群、Wendy 招聘、Issue 指派、协作回合等已经够用；本 todo 只做「笔记接缝」。

---

## 目标（一句话）

把产品笔记接到现有协作场上：**笔记 = brief + 最终落点；频道 = 协作现场；Issue = 可派发的活；Agent = 执行者**。  
不重做多 Agent 协作，只补齐入口剧本、双向锚点、汇合待审写回。

## 核心模型

```text
笔记（brief / SoT）
  →「用这篇…」Worker：协调 Agent + 频道
    → 该 Agent 用已有能力：建临时协调群 / @Wendy 招聘 / 建 Issue 指派
      → 协作发生在 Messages
        → 结果：插入笔记 / 子页 / Issue 完成 → 待审写回
```

| 角色 | 含义 |
|------|------|
| 产品笔记 / Note | `note_page`；目标描述与人确认后的落点 |
| 协作场 | Messages 频道（含临时协调群）、Issue、Wendy 招聘提案 |
| Editor | `note_ai_job` — 只改当前页，禁止顺便派活 |
| Worker | `note_worker_job` — 以笔记为 brief 驱动平台工作，禁止静默改正文 |
| 待审写回 | `note_page_writeback` — 人 Accept 后才改 `note_page.content`（D1） |
| 确定性回顾 | `POST /api/notes/retrospectives` — 平台 Facts 聚合进 `回顾/`（与多智能体整理并存，合同不同） |

## 禁止做（永久非目标）

- 另起一套「笔记内多 Agent 编排引擎」或把 Research Fleet 硬绑成通用周报编排
- 合并 Agent 本地 `notes/*.md` / `memory/daily` 与产品 `note_page` 存储
- OS 级桌面行为采集（键鼠、全盘文件监控等，机器源 C2）
- 无引用、无证据的「万能总结」当独立产品
- 跨 workspace 读写笔记或工作对象
- Worker 走 Editor/`replace_page` 静默改页；Editor 顺便建 Issue / 招聘 / 开频道

## 已定决策（硬约束）

| ID | 决定 |
|----|------|
| D1 | 多智能体整理写回默认 **待审**；人确认后才写入 `note_page.content` |
| D2 | 协作执行面 = **现有** 频道 / Issue 指派 / Wendy / 临时协调群；不新造 fan-out 状态机除非 P2 证明不够 |
| D3 | 「按笔记做事」主入口 = 笔记内 **「用这篇…」**；Worker 选 Agent + 目的地频道 |
| D4 | 确定性回顾与多智能体整理 **并存**：回顾可直接写 `回顾/`；协作整理走频道 + 待审写回 |
| D5 | Wendy 招聘权限边界不变：仅 Onboarding Agent；普通 Agent 不得获得招聘权 |
| D6 | 本机工作进合成仅允许 **C1**（Binding/Agent 工作摘要或 opt-in 白名单源），默认关 |

## 代码锚点（实现前先打开看）

| 区域 | 路径 |
|------|------|
| Editor / Worker 合同 | `docs/notes-editor-worker-contract.md` |
| 工程原则索引 | `docs/engineering-principles.md` §4.22（及 Wendy/招聘相关条） |
| Worker API / prompt | `server/internal/handler/note_worker.go`、`note_worker_prompt.go` |
| Worker UI | `packages/views/notes/note-worker-run-dialog.tsx`、`note-worker-reply-actions.tsx` |
| 统一意图入口 | `packages/views/notes/`（「用这篇…」）、`note-intent-entry.test.tsx` |
| 待审写回 | `server/internal/handler/note_writeback*.go`、Notes 写回确认 UI |
| 临时协调群 / Agent 建频道 | `server/internal/handler/agent_channels.go`；技能 `multica-multi-agent-coordination` |
| Wendy / Onboarding | `docs/adr/0012-single-workspace-onboarding-agent.md`、`docs/deploy-wendy-onboarding-agent.md` |
| 确定性回顾 | `server/internal/handler/note_retrospective*.go`、`NoteRetrospectiveDialog` |
| 多 Agent 协作原则 | `docs/multi-agent-collaboration-principles.md` |

---

## 已完成基线（勿重做 · 摘要）

> 详细落地说明曾在旧版 Slice 1–4 todo；下列能力视为 **已合入**，本文件只引用。

| 基线 | 状态 | 要点 |
|------|------|------|
| Slice 1 关联闭环 | ✅ | Issue 引用、从笔记建 Issue、待审写回、ACL |
| Slice 2 Worker | ✅ | Editor≠Worker；`note_brief`；`notes get`；「按这篇做」可派到 Agent DM 或群频道 |
| Slice 3 活关系 | ✅ | Issue done/cancelled → 写回；Issue→笔记反向发现；「用这篇…」统一入口 |
| Slice 4 回顾最小集 | ✅ | 日/周/月回顾；亲手/委派/相关；分层合成；agent_runs 短摘要 |
| 多 Agent 协作场 | ✅（平台既有） | 频道 @mention、临时协调群、Wendy 招聘提案、Issue 指派、协作回合等 |

**手测黄金路径（开干前先跑一遍，把断点记到任务下）：**  
笔记 →「用这篇… / 按这篇做」→ 选协调 Agent + 群频道 → Agent 建临时群或 @Wendy / 拆 Issue → 回复「插入下方」或 Issue done 待审写回。

---

## Slice N1 — 剧本入口（P0 · 先做）

**本 Slice 完成标准：**

1. 用户不必手写长 instruction，即可从笔记发动「开协作并推进」类路径。
2. 模板只驱动 **现有** Worker + 协作技能，不新增编排表。
3. 默认目的地偏好群频道（协作），仍允许 Agent DM。

- [x] **N1-T1 Worker 协作剧本模板（FE）**
  - **目标**：在「按这篇做」对话框提供 2～3 个可一键填入的 instruction 剧本。
  - **要做**：至少包含：
    1. **开协作并推进** — 为笔记目标建/使用协调频道，拆活、指派、进度回频道；
    2. **缺人找 Wendy** — 能力缺口时 @Onboarding Agent 走招聘提案（不擅自创建 Agent）；
    3. **整理并准备写回** — 汇总证据，给出可待审落笔记的结构化结论（不直接改页）。
  - 选中模板后写入 instruction 文本框（可再编辑）；文案中英与 `packages/views/locales` 一致。
  - **不要做**：新后端 job 类型；把模板做成不可编辑黑盒；给普通 Agent 注入招聘 skill。
  - **依赖**：现有 `NoteWorkerRunDialog`
  - **完成标准**：组件测：选模板 → instruction 含稳定关键词（建频道 / Wendy / 写回）；仍只打 `createNoteWorkerJob`。
  - **已落地（2026-08-13）**：
    - `note-worker-playbooks.ts` + Dialog 剧本 chips
    - en/zh `worker_playbook_*` locale
    - 测：`note-worker-run-dialog.test.tsx` 三剧本关键词 + 仍只打 Worker API

- [x] **N1-T2 模板默认目的地与文案引导**
  - **目标**：协作类模板默认引导用户选 **群频道**（或提示「先建群再选」），避免只会进 DM 导致协作不可见。
  - **要做**：协作模板选中时：若未选 `channel_id`，UI 提示「协作建议发到群频道」；可选记住上次频道。
  - **不要做**：强制禁止 DM（DM 仍合法，仅降级提示）。
  - **依赖**：N1-T1
  - **完成标准**：手测或组件测覆盖「未选频道时有提示 / 选频道后可派发」。
  - **已落地（2026-08-13）**：
    - 选协作剧本 → `destinationKind=channel` + `worker_playbook_channel_hint`
    - 选中频道后提示消失；测覆盖

- [x] **N1-T3 协调 Agent 侧技能提示对齐（必要时）**
  - **目标**：被 Worker 唤醒的协调 Agent 知道优先用现有 CLI/技能完成剧本，而不是空聊。
  - **要做**：评估是否仅靠 N1-T1 instruction 足够；若不够，在 Worker `<system_contract>` 或内置 skill 交叉引用中 **短** 增补「note_worker 场景：可 `channel create` / mention / issue 指派；写回交给人闸」。
  - **不要做**：复制整份 multi-agent skill 进 prompt；扩大 Agent 笔记 list/search。
  - **依赖**：N1-T1
  - **完成标准**：文档或 prompt 单测证明契约句子稳定；行为仍走现有 API。
  - **已落地（2026-08-13）**：
    - `buildNoteWorkerPrompt` system_contract 增补 coordination channel / pending writeback 一句
    - 快照测 + 关键词断言：`TestBuildNoteWorkerPromptSnapshotStablePartitions`

---

## Slice N2 — 双向锚点（P1）

**本 Slice 完成标准：** 从笔记能一跳到「这篇正在进行的协作现场」；从该现场能回到笔记。

- [x] **N2-A1 笔记 ↔ 频道锚点数据模型**
  - **目标**：Worker（或后续显式操作）能把「进行中的协作频道」挂到笔记上，可查询。
  - **要做**：选定持久化（推荐：`note_page` 元数据字段或小表 `note_page_channel_ref`，含 `channel_id`、`kind=worker|coordination`、`created_at`）；ACL 与笔记一致；列表 API 或嵌在 `GetNotePage`。
  - **不要做**：把频道消息全文镜像进笔记；跨 workspace。
  - **依赖**：N1 可并行设计，落地建议在 N1-T2 之后以便写入时机清晰
  - **完成标准**：migration + Go 测：创建/列表/删除；无权频道不泄露标题。
  - **已落地（2026-08-13）**：
    - migration `347_note_page_channel_ref`
    - API：`GET/POST/DELETE .../channel-refs`；`GetNotePage.refs` 含 `type=channel`
    - 不可达频道仅 `{type,id,accessible:false}`；DM 不可作锚点
    - 测：`note_channel_refs_test.go`、`note-refs-schema.test.ts`

- [x] **N2-A2 Worker 派发成功后写入锚点**
  - **目标**：`POST .../worker-jobs` 若带 `channel_id`（或 Agent 随后创建的协调群——若一期拿不到则只记派发频道），记下笔记↔频道关联。
  - **要做**：派发成功路径写 N2-A1；幂等（同笔记同频道不重复）。
  - **不要做**：因锚点写入失败而回滚已成功的 Worker 派发（记录警告即可，或事务策略在实现时注明）。
  - **依赖**：N2-A1
  - **完成标准**：handler 测：带 `channel_id` 的 Worker → 笔记可查到该频道 ref。
  - **已落地（2026-08-13）**：
    - 显式群频道派发成功后 `bestEffortUpsertNotePageChannelRef`（失败只 Warn）
    - FE：`onDispatched` 刷新 `noteDetail` 以显示锚点

- [x] **N2-A3 笔记 UI：协作现场入口**
  - **目标**：打开笔记时能看到并跳转关联频道（Messages）。
  - **要做**：笔记顶栏或侧栏「协作中」列表；`NavigationAdapter` 进频道；空态不占版。
  - **不要做**：在笔记里嵌完整聊天 UI。
  - **依赖**：N2-A1、N2-A2
  - **完成标准**：组件测或手测：有 ref 显示可点；无 ref 不报错。
  - **已落地（2026-08-13）**：
    - `NoteChannelAnchors` + locale `collaboration_links`
    - 测：`note-channel-anchors.test.tsx`

- [x] **N2-A4 频道侧回链笔记（最小）**
  - **目标**：带 `note_brief` 的 Worker 消息 / 频道头能打开源笔记。
  - **要做**：复用或加强已有 `note_brief` 时间线卡片的跳转；若已有则补测并勾选。
  - **不要做**：在每个频道消息下重复堆笔记全文。
  - **依赖**：现有 note_brief 卡片
  - **完成标准**：从 Worker 触发消息可回到 `noteDetail`；无权笔记降级。
  - **已落地（2026-08-13）**：
    - `NoteBriefPart` 展开后「打开笔记」→ `paths.noteDetail(ref_id)`
    - 测：`message-parts-renderer.test.tsx`

---

## Slice N3 — 汇合待审写回（P2）

**本 Slice 完成标准：** 协作收工后，人能在一处 Accept「汇总提案」，而不是只靠手工「插入下方」或零散 Issue 写回。

- [ ] **N3-W1 协作收工 → 单条待审汇总提案**
  - **目标**：协调 Agent（或用户一键）针对源笔记创建 **一条** pending writeback，evidence 含频道和/或相关 Issue。
  - **要做**：定义触发方式（优先：Agent 工具/API 显式 `propose note writeback`；或笔记 UI「从关联频道生成待审稿」读近期结论——实现时二选一写进本条注释）。内容为 append/patch 提案；**必须**走 `note_page_writeback` + evidence。
  - **不要做**：自动 Accept；把频道整个 transcript 无裁剪灌入；绕过 D1。
  - **依赖**：N2 锚点（便于定位频道）；现有 writeback API
  - **完成标准**：Go/FE 测：提案 pending → Accept 后笔记更新且含可点 evidence。

- [ ] **N3-W2 多 Issue 完成时的写回噪声控制**
  - **目标**：一篇笔记挂多个协作 Issue 时，避免每个 done 都炸一条重复长文。
  - **要做**：策略显式化（例如：同笔记短窗口合并提案 / 或仅「史诗」Issue 触发 / 或协作剧本默认关掉自动 Issue 写回改走 N3-W1）。写进代码注释与合同文档。
  - **不要做**：默默丢弃写回且无文档。
  - **依赖**：S3 既有 Issue→writeback；N3-W1
  - **完成标准**：白名单/合并策略有测；合同文档更新 `docs/notes-editor-worker-contract.md`。

- [ ] **N3-W3 与确定性回顾的产品文案分界**
  - **目标**：用户分清「生成回顾」vs「用这篇开协作整理」。
  - **要做**：Notes UI / 空态短文案；可选链到剧本模板。
  - **不要做**：把回顾 API 改成必须待审（除非单独产品决策）；混成一个按钮两套合同。
  - **依赖**：N1-T1
  - **完成标准**：文案过中英 locale；无行为回归。

---

## Slice N4 — 可选增强（按需开 · 勿阻塞 N1–N3）

- [ ] **N4-S4 回顾定时生成（可选）**
  - **目标**：daily/weekly cadence 产出回顾笔记或待审草稿。
  - **不要做**：挂在 `agent_reminder` fire 上凑合；聊天里只丢不可编辑总结当唯一产物。
  - **依赖**：已有回顾 API

- [ ] **N4-M1 机器源 C1：Binding/Agent 工作摘要进合成**
  - **目标**：名下 Computer 上的 Agent 工作摘要可进入回顾或写回 evidence。
  - **不要做**：OS 监控（C2）；密钥与 runtime 诊断进模型上下文（N4-P4）。
  - **依赖**：N1–N3 稳定后

- [ ] **N4-M2** 机器源默认关闭或明示可关

- [ ] **N4-P4** 密钥与 runtime 诊断默认不进笔记/模型上下文

- [ ] **N4-S5** 其他合成（standup / 交接模板等）——**必须**复用 Worker + 频道 + 待审写回，禁止新管道

- [ ] **N4-R1** 从笔记发起 Research（前 S3-A3 deferred）——有产品排期再开

---

## 进度建议（给人排期 / 给 AI 选下一个）

| 顺序 | 下一步 |
|------|--------|
| 1 | ~~N1 剧本~~ / ~~N2 锚点~~（已完成） |
| 2 | **N3-W1** 协作收工 → 单条待审汇总提案 |
| 3 | N3-W2 → N3-W3 |
| 4 | 仅当产品需要时再开 N4 |

**当前焦点：** Slice N2 已完成。下一个应做的 checkbox：**N3-W1**。
