# 笔记 ↔ Agent 工作打通 — 实现 Todo

## 给实现 AI 的读法（先读这段）

1. **严格按 Slice 顺序做**：完成 Slice N 的全部 checkbox 并满足「完成标准」后，再开 Slice N+1。
2. **一次只做一个 `- [ ]` 任务**：开干前把该条改成 `- [ ]` → 进行中说明写在任务下；做完勾成 `- [x]`。
3. **每个任务块都自包含**：含目标 / 要做 / 不要做 / 依赖 / 完成标准。缺信息时先查「代码锚点」，再查仓库 `CLAUDE.md`，不要臆造合同。
4. **已定决策是硬约束**，不可在实现时改口径；若代码做不到，停下来问人，不要静默放宽。
5. **测试跟代码走**：共享逻辑测 `packages/core` 或 `packages/views`；Go 测 `server/`。改前端后按仓库要求跑相关检查（用户要求时再跑全量 `make check`）。
6. **产品笔记 ≠ Agent 本地 notes**：本 todo 只打通 `note_page`（产品笔记）与平台工作对象。禁止把 `~/.multica/.../notes/*.md` 与 `note_page` 合并存储。

---

## 目标（一句话）

让产品笔记成为 Agent 工作图里的一等公民：**可引用、可作 brief、可触发工作、可待审写回**。  
「日/周回顾」等合成能力是管子上的应用（Slice 4），不是本项目的定义。

## 禁止做（永久非目标）

- 合并 Agent 本地 `notes/*.md` 与产品 `note_page` 存储
- OS 级桌面行为采集（键鼠、全盘文件监控等，称机器源 C2）
- 无引用、无证据的「万能总结」当独立产品
- 跨 workspace 读写笔记或工作对象

## 已定决策（硬约束）

| ID | 决定 |
|----|------|
| D1 | 所有写回笔记默认 **待审**；人确认后才写入 `note_page.content` |
| D2 | MVP 工作面 = **Issue + Agent run 摘要**；Research、机器源放到 Slice 3/4 |
| D3 | 「按笔记做事」主入口 = **笔记内统一意图入口**；创建/执行 Issue 时可挂笔记 |
| D4 | Slice 1 只做 **笔记 → Issue** 引用；**Issue → 笔记** 反向发现放 Slice 3 |
| D5 | **首期 = Slice 1 + Slice 2**；Slice 4 合成/回顾不进首期 |

## 名词

| 词 | 含义 |
|----|------|
| 产品笔记 / Note | 表 `note_page`，UI 在 `packages/views/notes/` |
| Agent 本地 notes | Agent 根目录下的记忆文件，**本 todo 不改造其存储** |
| 工作对象 | Issue、Agent run/task 等平台实体 |
| Editor | 改当前笔记正文（已有 `note_ai_job`） |
| Worker | 以笔记为 brief 去执行平台工作（新建，禁止复用 Editor 合同） |
| 待审写回 | 生成 patch/append 提案，用户确认后才落库 |
| Evidence | 写回或综述中的可点击来源链接（issue/run/note） |

## 代码锚点（实现前先打开看）

| 区域 | 路径 |
|------|------|
| 笔记 API / AI job | `server/internal/handler/notes.go` |
| 笔记 migration | `server/migrations/300_notes.up.sql`、`315_note_ai_job.up.sql` |
| 笔记类型 | `packages/core/types/note.ts` |
| 笔记 UI | `packages/views/notes/notes-page.tsx` |
| 笔记 AI 应用/diff | `packages/views/editor/utils/note-ai-apply.ts`、`note-ai-diff.tsx` |
| Issue 路由/handler | `server/cmd/server/router.go`、`server/internal/handler/` 下 issue 相关 |
| Agent 记忆（勿与产品笔记合并） | `docs/agent-memory-model.md` |

---

## Slice 1 — 关联闭环（首期 · 先做完）

**本 Slice 完成标准（全部满足才算 Slice 1 完成）：**

1. 笔记正文能插入 Issue 引用，能跳转，无权限不泄露。
2. 可从笔记创建 Issue，并自动带上笔记→Issue 引用。
3. Issue（或 run）完成后可生成「写回该笔记」的 **待审** 提案，带 evidence；确认后才改笔记正文。
4. 所有查询带 `workspace_id` + 笔记 ACL（owner / share）。

### 1.1 引用：笔记 → Issue

- [x] **S1-R1 定义引用数据模型**
  - **目标**：笔记与 Issue 之间有可查询的稳定关联（不要只靠 Markdown 纯文本碰运气）。
  - **要做**：选定一种持久化方式（推荐：独立关联表，或解析正文后的规范化 ref 表；需能按 `note_page_id` / `issue_id` 查询）。Slice 1 只要求笔记→Issue 方向写关联。
  - **不要做**：Issue→笔记反向索引（Slice 3）；引用 Agent/Run（Slice 2）；跨 workspace。
  - **依赖**：无
  - **完成标准**：migration + 读写 API 或内部 loader；单测覆盖创建/列表/删除关联；无权限 issue 不出现在结果里。
  - **已落地（2026-08-12）**：
    - 表 `note_page_issue_ref`（migration `336_note_page_issue_ref`）
    - API：`GET/POST /api/notes/pages/{id}/issue-refs`，`DELETE .../issue-refs/{issueId}`
    - 核心类型/客户端：`NotePageIssueRef*` in `@multica/core`
    - 测试：`TestNotePageIssueRef*`（create/list/delete、跨 workspace 拒绝、列表省略不可达 issue、无笔记权限 404）

- [x] **S1-R2 编辑器插入与渲染 Issue 引用**
  - **目标**：人在笔记里能插入 Issue 引用，看到标题/标识，点击跳到 Issue。
  - **要做**：FE 插入 UI + 渲染；跳转走现有 `NavigationAdapter` / workspace paths；保存时同步 S1-R1 关联。
  - **不要做**：在共享包里 `import next/*` 或 `react-router-dom`。
  - **依赖**：S1-R1
  - **完成标准**：手测或组件测：插入 → 保存 → 刷新仍在；点击进入正确 Issue；无权限显示降级（如「不可见」）且不泄露标题。
  - **已落地（2026-08-12）**：
    - Notes 启用 `enableIssueReferences`：输入 `#` 搜索/插入 Issue chip
    - 已有 `IssueReferenceExtension` 渲染 + `useNavigation` 跳转
    - Autosave 后 `syncNotePageIssueRefsFromContent` 同步关联表
    - 无权限 chip 显示「不可见/Unavailable」，不展示 title
    - 测试：`issue-refs.test.ts`、`issue-reference-suggestion.test.tsx`、`issue-chip.test.tsx`

- [x] **S1-R3 Agent/服务端可读结构化 refs**
  - **目标**：后端或 agent 工具拿到的是结构化 ref 列表，不是只能正则扫正文。
  - **要做**：在笔记详情或专用 endpoint 返回 `refs: [{ type, id, label?, accessible }]`；ACL 过滤。
  - **不要做**：把整篇私有笔记无差别塞进任意 agent 上下文（那是 Slice 2 + 授权）。
  - **依赖**：S1-R1
  - **完成标准**：API 契约 + 测试：有权/无权两种 fixture。
  - **已落地（2026-08-12）**：
    - `GET .../issue-refs` 与 `GET .../pages/{id}` 均返回结构化 refs
    - `accessible=true` 带 `label`/详情；`accessible=false` 仅 `type`+`id`，不泄露 title
    - 测试：`TestNotePageIssueRefListMarksInaccessibleWithoutLeaking`、`TestGetNotePageIncludesStructuredRefs`、schema 测

### 1.2 动作：笔记 → 创建 Issue

- [x] **S1-A1 从笔记创建 Issue**
  - **目标**：整页或选区一键/一命令变成 Issue，并自动建立笔记→Issue 引用。
  - **要做**：API + UI（选区优先，无选区用标题+摘要）；创建成功后写 S1-R1 关联；可选把 issue 引用插回笔记（待审或直接插——若改正文，私有页可直接插引用标记，但若重写大段内容仍建议待审；**最小要求是关联表一定有记录**）。
  - **不要做**：自动指派 Agent 执行（Slice 2 的 Worker）。
  - **依赖**：S1-R1、S1-R2
  - **完成标准**：创建后 Issue 存在；关联可查；无 workspace 成员不能对他人私有笔记操作。
  - **已落地（2026-08-12）**：
    - `POST /api/notes/pages/{id}/issues`：建 Issue + 写 `note_page_issue_ref`（不指派 agent）
    - Notes 顶栏「创建 Issue」：有选区用选区，否则用标题+正文摘要；成功后插入 issue chip
    - 测试：`TestCreateNotePageIssue*`

### 1.3 动作：工作 → 待审写回笔记

- [x] **S1-W1 待审写回数据模型与 API**
  - **目标**：落实 D1——写回先提案、人确认再落库。
  - **要做**：存储提案（目标 `page_id`、patch/append 内容、evidence 列表、status=pending/applied/rejected、actor）；list / accept / reject API；accept 时更新 `note_page` 并记录 `updated_by`。
  - **不要做**：自动静默改 `content`；与 `note_ai_job` 混成同一个 job 类型（可复用 diff UI 模式，但合同分开）。
  - **依赖**：无（可与 1.1 并行，但 1.4 依赖本项）
  - **完成标准**：Go 测：accept 后 content 变、reject 不变；evidence 字段必填校验。
  - **已落地（2026-08-12）**：
    - 表 `note_page_writeback`（migration `337_note_page_writeback`）
    - `POST/GET .../pages/{id}/writebacks`；`POST .../writebacks/{id}/accept|reject`
    - accept 才改 `note_page.content` + `updated_by`；创建时 evidence 必填
    - 测试：`TestCreateNotePageWritebackRequiresEvidence`、`TestAccept*`、`TestReject*`

- [x] **S1-W2 写回必须带 evidence + FE 确认 UI**
  - **目标**：用户能看懂「这段话从哪来的」，确认或拒绝。
  - **要做**：复用或对齐现有 note AI diff 交互（见代码锚点）；每条提案至少含一个可打开的 issue 或 run 链接。
  - **不要做**：无 evidence 的提案允许 accept。
  - **依赖**：S1-W1
  - **完成标准**：UI 可确认/拒绝；无 evidence 的提案 API 返回 400。
  - **已落地（2026-08-12）**：
    - Notes 页在有 pending 写回时展示确认卡片（diff / append 预览 + evidence 链接）
    - Accept / Reject 调 S1-W1 API；accept 后本地更新笔记正文
    - 测试：`note-writeback-review.test.tsx`、`writeback-preview.test.ts`

- [x] **S1-W3 Issue/Run 完成时生成写回提案**
  - **目标**：工作结束 → 自动（或一键）生成针对关联笔记的待审写回。
  - **要做**：当 Issue 完成（及/或关联 run 成功结束）且存在笔记→Issue 关联时，生成摘要提案指向该笔记；摘要含结果要点 + evidence。
  - **不要做**：Slice 1 做全量 Activity/thinking 回放；不做 Research；不做机器源。
  - **依赖**：S1-R1、S1-W1、S1-W2
  - **完成标准**：集成测或 handler 测：完成 Issue → 出现 pending 提案 → accept 后笔记含 evidence 链接。
  - **已落地（2026-08-12）**：
    - Issue `status` 转入 `done` 时（UpdateIssue / BatchUpdateIssues）为每个关联笔记创建 pending `append` 写回
    - 摘要含 issue mention chip markdown；evidence 含 issue（若有则附最新 completed run）
    - 同笔记同 issue 的 pending 提案幂等跳过
    - 测试：`TestIssueDoneCreatesPendingNoteWritebackAndAcceptApplies`

### 1.4 权限（Slice 1 横切，每项实现时遵守）

- [x] **S1-P1 权限与多租户检查清单落地**
  - **目标**：私有笔记仅 owner；分享页按 `note_page_share`；一切按 `workspace_id` 过滤；写回记录 actor（user/agent）。
  - **要做**：为 S1 新增的每个 endpoint 补成员校验 + 笔记 ACL；共享笔记的写回提案创建者与确认者可分离但都需有权。
  - **不要做**：用「知道 UUID」绕过分享模型。
  - **依赖**：与 S1 其他项同步完成（最后做一次总复核也可）
  - **完成标准**：无权用户访问关联/写回 API 得 403/404（与项目现有风格一致）；有测例。
  - **已落地（2026-08-12）**：
    - 复核：issue-refs / create-issue / writebacks 均经 `notesWorkspaceAndUser` + `loadAccessibleNote`（私有→404，分享协作可读写回）
    - 测例：`TestNoteWritebackRequiresNoteAccessForOutsider`、`TestNoteWritebackAllowsSharedCollaborator`、`TestNoteWritebackRecordsMemberActor`、`TestNotePageIssueRefDeleteRequiresNoteAccess`、`TestSharedCollaboratorCanCreateIssueFromNote`（既有 outsider 测例仍覆盖 refs/create-issue）

---

## Slice 2 — Agent 按笔记干活（首期 · Slice 1 完成后）

**本 Slice 完成标准：**

1. 执行 Issue/Task 时可挂 `note_page_id`（或 refs）作为 brief。
2. Agent 在 ACL 内能读该笔记；正文当 untrusted。
3. 从笔记用 **Worker**（非 Editor）触发 Agent；不会误走 `replace_page`。
4. 引用模型扩展到 Agent/Run（展示与关联，仍非 Issue→笔记反向）。

### 2.1 Editor vs Worker 分家

- [ ] **S2-C3 合同分离：Editor 与 Worker**
  - **目标**：两种意图两套合同，禁止混用。
  - **要做**：文档化 + 代码分型（例如不同 job/task reason 或 endpoint）。Editor = 现有 `note_ai_job`（改页）。Worker = 新路径（读笔记 → 平台执行）。
  - **不要做**：给 `note_ai_job` 加「顺便去建 issue/跑 agent」的副作用。
  - **依赖**：Slice 1 完成
  - **完成标准**：代码与注释/skill（若有）能一眼区分；误用返回明确错误。

### 2.2 上下文挂载

- [ ] **S2-C1 执行可挂笔记 brief**
  - **目标**：Task/Wake/Issue 执行请求可带 `note_page_id`（或一组 note refs）。
  - **要做**：API/协议字段；服务端校验笔记 ACL 后注入执行上下文；字段进 prompt 时包在明确 untrusted 边界内（对齐现有 note AI「正文 untrusted」口径）。
  - **不要做**：挂载无权限笔记；把分享范围外的子页面整树塞进去（除非显式设计，默认只挂指定页）。
  - **依赖**：S2-C3
  - **完成标准**：带 note 的执行与不带 note 的执行都有测；无权 note_id → 拒绝。

- [ ] **S2-C2 Agent 读笔记工具（ACL 内）**
  - **目标**：Worker 运行中可按需读指定笔记页（可选：同树只读子页需另开任务，默认不做搜索全家桶）。
  - **要做**：CLI 或 runtime 工具：`notes get` 类；强制 workspace + ACL。
  - **不要做**：默认开放「搜索整个 workspace 所有笔记」。
  - **依赖**：S2-C1
  - **完成标准**：有权可读、无权不可读的测例。

- [ ] **S2-C4 指令与正文分离**
  - **目标**：用户指令 / 系统合同 vs 笔记正文分离，防 prompt 注入。
  - **要做**：Worker prompt 模板明确分区（instruction vs `<note>`）；沿用 notes AI 的 escape/合同风格。
  - **依赖**：S2-C1
  - **完成标准**：prompt 构造有单测或快照测，分区标签稳定。

### 2.3 从笔记触发 Worker

- [ ] **S2-A2 笔记内触发 Agent（Worker）**
  - **目标**：落实 D3 的主路径——在笔记里选 Agent + 指令，「按这篇做」。
  - **要做**：UI 入口（可先独立按钮，Slice 3 再收成统一意图路由）；创建 Worker 执行并挂当前 `note_page_id`；状态可感知（排队/跑完）。
  - **不要做**：走 Editor 的 `replace_page` / `note_ai_job` 完成「去干活」。
  - **依赖**：S2-C1、S2-C2、S2-C3、S2-C4
  - **完成标准**：E2E 或集成测：从笔记触发 → task 带 note → 不创建 note_ai_job 编辑合同。

- [ ] **S2-R1 引用扩展到 Agent / Run**
  - **目标**：笔记可引用 Agent、Run（展示+关联），便于写回 evidence。
  - **要做**：扩展 S1 引用模型 `type`；渲染与跳转；写回 evidence 可链 run。
  - **不要做**：Issue 页上的「关联笔记」列表（Slice 3）。
  - **依赖**：S1-R1
  - **完成标准**：可插入/渲染；ACL 降级行为与 Issue 引用一致。

---

## Slice 3 — 活关系（首期之后）

**本 Slice 完成标准：** 关联对象状态变化可产生待审写回；Issue 页能看到关联笔记；笔记内意图入口统一；Research 仍可再往后。

- [ ] **S3-W1 订阅式写回**
  - **目标**：笔记可订阅已关联 Issue 的状态变化；变化 → 待审提案（D1）。
  - **要做**：订阅关系或「有关联即订阅」策略二选一（实现时写进代码注释）；触发 S1 写回管道。
  - **不要做**：thinking / 诊断洪水写入笔记。
  - **依赖**：Slice 1 写回管道、Slice 2（若要含 run 事件）
  - **完成标准**：完成/失败能出提案；噪声事件不出提案（白名单测例）。

- [ ] **S3-W2 回写事件白名单**
  - **目标**：只允许完成、失败、关键评论等。
  - **要做**：明确枚举 + 测试。
  - **依赖**：S3-W1
  - **完成标准**：白名单外事件零提案。

- [ ] **S3-R5b Issue → 笔记反向发现**
  - **目标**：落实 D4——Issue 侧列出关联笔记（ACL 过滤）。
  - **要做**：Issue 详情 API/UI 展示关联笔记；无权限笔记不出现或降级。
  - **依赖**：S1-R1
  - **完成标准**：双边都能发现同一关联；无权不可见。

- [ ] **S3-A4 笔记内统一意图入口**
  - **目标**：落实 D3——一个入口路由到 Editor / Worker / 创建 Issue。
  - **要做**：合并 Slice 1/2 分散入口；意图分类明确，失败时让用户选。
  - **不要做**：再堆互不相关的并列按钮而不路由。
  - **依赖**：S1-A1、S2-A2、现有 Editor
  - **完成标准**：三条路径均可达；路由错误有明确提示。

- [ ] **S3-A3 从笔记发起 Research（可选延后）**
  - **目标**：有 Research 能力的工作区可从笔记开题。
  - **要做**：挂 note 为题面；仍走待审写回合同（若写回笔记）。
  - **不要做**：阻塞 Slice 3 其他项；若排期紧可整项推迟并在本条注明 `deferred`。
  - **依赖**：S2-C1 模式可复用
  - **完成标准**：能创建并带 note 上下文；或明确标记延期。

- [ ] **S3-W3 文档声明：与 Agent Daily 并行**
  - **目标**：防止后续 AI/人把产品写回与 `memory/daily` 合并。
  - **要做**：在本 todo 或 `docs/` 短文声明两套存储并行、可互链、禁止合并（若已有 `docs/agent-memory-model.md`，加一小节交叉引用即可）。
  - **依赖**：无
  - **完成标准**：文档可被后续实现引用。

---

## Slice 4 — 合成应用（验证管子 · 不进首期）

**前置：** Slice 1–2 完成；建议 Slice 3 的写回/反向发现已可用。  
**本 Slice 完成标准（S1 最小）：** 按需生成回顾笔记；每条有 evidence；标明数据源；缺源诚实降级；归属分栏（亲手/委派/相关）。

- [ ] **S4-S1 按需日/周回顾笔记**
  - **目标**：用已有 Facts（Issue 变更、touched 笔记、可选 run 摘要）生成一篇私有回顾笔记或待审草稿。
  - **要做**：选时间窗（用户时区）；聚合 → 分层摘要 → 写入笔记树（如 `回顾/YYYY-MM-DD`）；每条带 evidence。
  - **不要做**：无引用长文；直接灌 raw thinking；假装有未接入的数据源。
  - **依赖**：Slice 1 引用与写回；Slice 2 run 摘要更佳
  - **完成标准**：生成结果可点回 issue/note；UI 列出所用源；关闭某源后内容变化符合预期。

- [ ] **S4-S2 归属打标**
  - **目标**：亲手 / 委派 Agent / 仅相关 分开展示。
  - **依赖**：S4-S1
  - **完成标准**：三类不会混成单一「我做了」。

- [ ] **S4-S3 长窗口分层汇总**
  - **目标**：周/月 = 日汇总再汇总，禁止一次性灌一个月原始事件。
  - **依赖**：S4-S1
  - **完成标准**：月回顾输入是日/周摘要而非全量 raw。

- [ ] **S4-S4 定时生成（可选）**
  - **目标**：复用 reminder 的 daily/weekly cadence，产出笔记或待审草稿。
  - **依赖**：S4-S1
  - **不要做**：在聊天里只丢一段无法编辑的总结当唯一产物。

- [ ] **S4-S5 / S4-A8 其他合成（按需开）**
  - standup、交接、多页索引、按笔记缺口补 Issue 等——**必须复用**引用/Worker/待审写回管子，禁止另起无证据管道。

### 机器源（仅 C1 · 晚于 MVP）

- [ ] **S4-M1** 名下机器 Binding/Agent 工作摘要可进入合成（不是 OS 监控）
- [ ] **S4-M2** 默认关闭或明示可关
- [ ] **S4-P4** 密钥与 runtime 诊断默认不进笔记/模型上下文

---

## 进度建议（给人排期 / 给 AI 选下一个）

| 顺序 | 下一步该做什么 |
|------|----------------|
| 1 | 若 Slice 1 未完成：做第一个未勾选的 `S1-*` |
| 2 | Slice 1 全勾且满足完成标准 → 开始 `S2-C3` |
| 3 | Slice 2 完成 → `S3-*` |
| 4 | 管子稳定后 → `S4-S1` 起验证 |

**当前首期范围：** 仅 Slice 1 + Slice 2。
