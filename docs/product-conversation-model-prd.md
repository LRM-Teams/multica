# Multica 统一 Conversation 模型 PRD v2.4（合稿·终版口径）

- Task: #188（Parker 牵头）
- 作者: Parker - Product Manager
- 日期: 2026-07-03（v2.4 刷新 2026-07-04）
- 状态: v2.4 —— **事故硬化并入**：新增 §6.3 合并唤醒/队列背压（#194，7-3 队列风暴事故根治）；标注已落地基座（#197 seq/conversation schema=migration 144 已 done+产品 PASS；#194 Phase 0 ambient 队列门已 PASS；§4.2 send 幂等 #207 契约已锁）。供 Miles 对最新单一版本拆实施。
- v2.3: 排序/游标改判 per-conversation seq；§4.2 send 幂等（#207）；issue run 进 Activity。
- v2.2: §4.1 Attachment/Files；§6.2 Raft Activity 基准；Run vs Agent Workspace 边界。
- 取代: v1.0/v1.1/v1.2（含 v1.2 §16 补丁）。变更记录见 §14。
- 关联: #157(action 边界) · #190/#191(message security) · #192(reaction realtime) · #72(issue 可见性) · #196(must-reply) · #27(issue 上下文锁死) · **#194(ambient 扇出+队列改造，Phase 0 已落) · #207(send 幂等，契约已锁)**
- 拆分: #197 P1-BE(**schema 已 done**) · #198 P1-FE/UX · #199/#200（改小为 issue 行为对齐，见 §7/§11）· #201 QA
- 配套: Iris 交互稿 `2026-07-03-issue-thread-reaction-interaction-v0.md`（issue 迁移部分随 scope cut 作废）

---

## 0. 背景 / 目标 / Scope 决策
Frank 四条原始需求：① agent 自然回复 issue；② agent↔human DM；③ agent↔agent DM；④ reaction 覆盖 issue。
红线：naming/模型清晰；不过度复杂。用户体感：Multica DM ≈ Raft DM。

### 0.1 原架构与为什么改（考古，v2.4.2 补，代码核实）
**整体架构五块（健康，本 PRD 不动它）**：Next.js 客户端（apps/web + packages/core·views，Next rewrite 代理 API）→ Go 单体后端（handler + sqlc）→ **Postgres 单存储**（业务表 + `agent_task_queue` 队列表）→ 双 WebSocket（`realtime` 服务器→浏览器推送；`daemonws` 服务器⇄各机 daemon 派任务）→ 分布式 daemon 在用户电脑上起 agent runtime 执行 run、产出经 API 回流。

**旧数据语义四层（本 PRD 改的就是这些"血液"）**：
1. **DM 无实体**：DM = `channel` 表一行，靠命名约定 `name="dm:user:<id>|agent:<id>"`（成员对排序拼进名字，借 UNIQUE(workspace_id,name) 做 create-or-find 幂等）。成员关系/1:1 约束/权限藏在字符串里，agent↔agent DM、审计无落点。更老一代 `chat_sessions` 只留历史迁移（三代同堂）。
2. **顺序靠时钟**：`channel_message` 按 `created_at` 排，而 PG `now()`=事务**开始**时刻——并发下"先开始的后提交"，时间序≠可见序，结构性漏消息窗口（→ 改判 per-conversation seq，事务内取号+行锁，seq 序==提交可见序）。
3. **读态三张 timestamp 表、全 user-only**：`channel_read` / `channel_thread_state`(mig139) / `dm_peer_state`(mig130，按 peer 记 pin/隐藏/静音)。**agent 没有已读概念**——每次唤醒喂什么全靠触发消息本身（→ #197 per-member cursor 含 agent）。
4. **触发无游标、无脑扇出**：非 @ 消息 ambient 扇给频道全部 agent、每个各插一行 `agent_task_queue`（N×M 无背压）——7-3 事故 27 万行排队即此。合并唤醒在旧模型**做不出来**，前提是 agent 有已读位置（→ §6.3）。

一句话：旧实现是"够用的 hack 堆叠"（DM 靠名字、顺序靠时钟、已读靠时间戳、触发靠扇出），单层能跑、叠加成灾；本 PRD 每一项改动都对应一个具体结构缺陷，不是重写偏好。

**Scope 决策（Frank/Miles/Parker 一致）：本轮不迁移 issue 数据模型。**
- issue 今天能正常工作（`issue`/`comment`/`issue_reaction`/`comment_reaction` 表齐全）；为统一而迁移的风险与复杂度 > 收益，且是 v1.2 全部模型空洞（讨论引擎挂接、issue reaction 落点、watcher vs follower）的来源。
- 需求①④是**行为层**诉求，在现有 issue 表上即可满足（见 §7），不需要 schema 统一。
- 统一讨论引擎的范围 = **channel + dm（含各自 thread）**。issue 若未来要接入，单独立项（倾向 anchor-message 方案），不在本轮。

## 1. 核心模型
**Conversation.type = { channel, dm }** —— 顶层消息容器只两种。
- `channel`：群容器，成员可见。
- `dm`：1:1 容器，仅双方可见（human↔human / human↔agent / agent↔agent(P3)）。

**统一原语（channel/dm 共用一套引擎）**：`Message(parts[])` + `Reaction` + `reply/thread` + 未读/静音/follow/搜索/权限/审计。

**thread** = 某条 message 下的**扁平**回复链（不嵌套；针对某条回复用 quote）。thread 回复**不进主时间线、不计会话级未读**（对齐现有 migration 139 与 Raft）。

**issue** = 现有一等 work-item 实体（workspace-scoped，本轮 schema 冻结）：7 态 status（backlog/todo/in_progress/in_review/done/blocked/cancelled）、priority、assignee、watchers(subscriber)、labels、dependencies、parent_issue_id（issue↔issue 工作分解树，与 thread 扁平无关）、acceptance_criteria、context_refs、due_date。issue 讨论仍走现有 comment 体系（行为对齐见 §7）。

## 2. 术语表（naming 锁）
| 术语 | 定义 |
|---|---|
| Conversation | 顶层容器，type ∈ {channel, dm} |
| Message | channel/dm 内一条消息，parts[] 正文（#142/#143 契约） |
| thread | 某条 message 下的扁平回复链（root_message_id 标识；不嵌套） |
| Reaction | 挂在任意 Message（含 thread reply）上的 emoji |
| issue | 现有一等 work-item 实体（workspace-scoped）；讨论 = 现有 comment；本轮不并入统一 Message |
| Activity/Event | agent thinking/tool/status/run 事件；**永不落 Message 行 / comment 正文** |
| agent run | 内部执行队列里一次 agent 运行作业（唤醒→执行→输出 send/react/no_reply）。≠ issue。不写「agent task」 |
| runtime session token | 实现层执行/审计凭证，不进用户可见 naming |
| seq | **per-conversation 自增消息序号**（每个 channel/dm 自己一个计数器；同一事务内取号+插入，行锁保证 **seq 顺序 == 提交可见顺序**）。不是 Raft 那种全局 seq。created_at 只作展示 |
| read cursor | 每 participant（human/agent）每 conversation/thread 一条 `last_read_seq`（整数；thread 共享会话 seq 空间，thread 级游标同一机制） |

## 3. 四条能力 → 本轮落点
- **① Agent 回复 issue（行为对齐，§7）**：现有 comment 上做「回复≠执行结果」渲染分离 + 触发策略对齐；不迁 schema。
- **② Agent↔Human DM（P1 主体）**：产品化 `multica dm`——会话列表/未读/静音/搜索/关闭/上下文隔离/权限/审计；双向同一 1:1；体感对齐 Raft。
- **③ Agent↔Agent DM（P3）**：同一套 dm + 治理（§9）。
- **④ Reaction 覆盖 issue（已满足 + 统一 channel/dm）**：issue/comment reaction 用现有两张表（不迁）；channel/dm 统一 `Reaction`；行为口径三处一致（谁能 react、通知、**reaction 不唤醒 agent**）。

## 4. 动作合约（v2.4.4 定版：两个工具 + 沉默；运输层 = CLI；Frank 拍）
**Agent 只有"手"（工具），没有"协议话"。合同三行：**
1. **`send(target, parts)`** 和 **`react(message_id, emoji)`** —— 仅此两个动作。
2. **不想回 = 什么都不做**。没有 no_reply 动词/工具/JSON——run 正常跑完没出手即自然结束，游标照常推进（"no_reply"只作为**平台观察到的 run 完结状态**存在：结算游标、判 #196 must-reply、喂"为什么没回"注解，见 §6/§6.2——它不是 agent 会"说"的东西）。
3. **运输层 = CLI（Raft 同款，Frank 拍"MCP 太占上下文"）**：daemon 向 agent 工作环境注入 `multica` CLI（PATH + task 凭证），agent 执行 `multica message send --target "#频道" …` / `multica message react …` 直打 server API——**agent 的最终文本永不被解析成协议**（#255 砍掉文本 JSON 信封解析；unwrap 守卫只作历史数据防御）。幂等：send 带 `client_message_id`（复用 §4.2 契约，runtime 生成）。
   **凭证隔离（7-6 raft 对标补，#259 merge 前 gate）**：task 凭证必须 **per-agent、per-run 文件级隔离**——每次 run 生成独立 token 文件（对标 raft `cli-transport/<agent>/<run>/agent-token` 形状：wrapper PATH 前置 + 清除 raw credential env），**禁止共享环境变量、禁止长期 token**；run 结束凭证即出作用域。我们与 raft 独立收敛到同一 CLI carrier 架构，此形状为参考实现。

**target 语法（与 Raft 一字位对齐）**：`#channel`（频道）｜`dm:@name`（Human DM：自动 find-or-create、禁 self-DM、仅同 workspace、错误 non-leaky；`@name` 用 handle #42，UUID 不进模型可见层）｜`#channel:<msgid>`（thread 回复，扁平）。server 单一 target parser，解析后分三支。**回复目标严格等于收到消息的来源 target**：群顶层来的回群顶层、thread 来的回该 thread、DM 来的回该 DM；开新 thread 必须是显式动作，绝不由内容或语气推断。direct/must-reply 语义仍走 durable queue/可见回复路径。agent↔agent DM 仍 P3。落地=task #247（CLI 载体）+ #255（存量迁移）。
规则：thread 回复生成可见行；react 是 message-scoped、不触发 agent run；parts[] 只作 send 正文。issue 侧动作沿用现有 comment/issue_reaction API（§7）。

### 4.2 send/reply 幂等（事故驱动，#207；BE 契约已由 Barry 锁定）
每个 send/reply 带 **client-generated `client_message_id`**（idempotency key）。server 按 `(conversation, sender, client_message_id)` 去重。三支结果分流（FE 必须区分，不能混成一个"失败提示"）：
- **200 OK + existing message**（同 key + 同 payload dedup 命中）= **透明成功**：消息本来就发了、且只发一次。FE 静默 reconcile/upsert 到那条已存在消息、composer 清空、显示一次，**不弹错**（给用户报错反而让他以为没发出去）。
- **409**（同 key 但 payload / attachments / reply·thread 语义变了）= 真冲突，**不写、无副作用** → FE **可见错误 + reset intent**。
- **网络 / 5xx / 权限 / 校验** = 真失败 → FE **可见错误 + 可重试**，不静默 reset intent。
配套 FE：`handleSend` 在 `isPending` 时 in-flight guard（发送中禁 Send/Enter）+ debounce + **send 请求 timeout/abort**（composer 不能被卡死的 send 永久锁住，onSettled 一定触发）。thread reply 的 idempotency payload 需带 `threadRoot.id`（BE dedup key 不含 thread-root，跨 thread 复用会撞 spurious 409）。
与 §5 `channel_message.client_message_id` 列 + `seq` 唯一约束一脉：一条 pending 无论 re-render/重试/连击都只落一次。**这是今天 16,951 条重复消息事故的源头修复**。

### 4.3 陈旧发送保护 / Freshness hold（P2 候选，Frank 已点头方向；源自 Raft dogfood）
**问题**：agent run 读完 unread bundle → 思考 → send，期间会话已前进，agent 照发过时回复（实证：Parker 单日在 Raft 被 hold 5 次、4 次改写——不拦就发出已被新证据证伪的结论）。§4.2 幂等管"重复"，本节管"过时"，正交。
**机制四件套（对齐 Raft，实测有效）**：
1. **send API 前置校验（强制拦截点）**：send 带上本次 run 已读 max seq（context_pack 已有）；服务端比对 `conversation.last_seq > known_seq`（**整数比较，复用 #197 per-conversation seq，零歧义**）→ 超阈值即 hold，不投递。
2. **拒绝时附带有界 delta**：hold 返回体内嵌**新增消息全文（最新 N 条，有界）**——对 LLM agent，tool result 即新上下文，下一步推理自然消化，无需特殊 runtime 能力。
3. **draft 暂存**：原文存服务端 draft（挂目标，复用 §4.2 client_message_id 幂等），不静默丢。
4. **显式二次决定**：agent 改写→正常重发（覆盖 draft）；坚持→显式 send-draft 发出。**系统不静默丢、不静默发，把决定权连同新证据还给 agent。**
**边界**：仅拦 agent 的 send/reply（human 不拦）；阈值 N 可配（DM 建议 1、channel 建议宽些）；hold-重发循环有上限（连续 hold M 次后强制二选一，防活锁）。验收：run 期间会话前进 → agent send 被 hold 且收到 delta；send-draft 可显式发出；human 发送不受影响。

### 4.4 聊天为主工作模式（v2.4.6，Frank 7-6 定调；P1 候选，排 #259/#178 主线后）
**定调（Frank）**："以后聊天为主、issue 为辅——通过聊天下发任务、下发 issue。" **产品上只有一种模式：聊天。** issue 不是第二个模式，是聊天里长出来的**工作产物/工作台**——用户在聊天里下发，agent 开 issue、干活、回到原 thread 交付，全程不离开聊天。

**核心原则（Frank 拍："issue 里写清楚，是不是一个会话都不重要，因为有 context 了"）：上下文进制品、记忆进 agent、session 只是优化。**
1. **活儿的上下文 → 写进 issue（唯一必需机制）**：聊天下发 issue 时，把上下文写全——来源 thread 引用 + 聊天里拍过的决定 + 边界/验收（生成机制复用现有 ChatContextSummary）。写清楚的 issue，任何新 session 拿起即干；**session 续接从"必需品"降级为"性能优化"**，断了不影响正确性。制品可见可审，优于 session 的隐式黑盒状态（与 context_pack 可观测哲学同源）。
2. **交付回流（脐带闭环）**：issue 完成后结果自动回流来源 thread 并 @发起人——下发和交付都在聊天里闭环。
3. **人的记忆 → 一份薄的 agent 级持久记忆**：只装跨活儿的东西（用户偏好、团队约定、拍过的原则——不可能每个 issue 重抄一遍），每个 agent 一份（文件量级），聊天 run 和 issue run 都注入、agent 可自行更新。现状只有 Codex 的 per-(agent,issue) memory，升级为 agent 级。对标 raft（聊天为主的先例）：身份连续性靠持久 MEMORY，不靠 session。
**session 策略（v2.4.7 Frank 定版，取代此前"架构不动"表述）**："聊天用一个 session，issue 按 (agent_id, issue_id) 维度新开 session"：
- **聊天 = 每 agent 一条常驻 session**：所有 DM/频道消息投进同一条 session（raft 同款身份 session）——agent 在聊天里天然连续，DM 里说过的话频道里当然记得。变化点：聊天侧从现状"每 chat_session 一条"并为"每 agent 一条"。
- **issue = (agent_id, issue_id) 新开 session**：干活的脑子按 issue 隔离、天然并行。与现状实现同键（migration 020），零改动。
- **两个配套（raft 已验证）**：①常驻 session 涨满→压缩重启，#273 agent 记忆文件即恢复锚（session 可死、记忆不死）；issue session 新开时同样注入这份记忆——**两个 session 世界靠一份记忆连接**。②聊天串行（忙时消息合并排队）、issue 并行——并行度全在该并行的一侧。
- 其余实现机制保留：runtime/workdir 绑定、中毒轮换、摘要交接照旧。

## 4.1 Attachment / Files（发送与查看）
**模型**：Attachment = 一等 file asset（id/uploader/filename/mime/size/storage ref）；进消息 = `parts[]` 的 **attachment part**（image/file 子型），与 sticker 同一契约，跨 channel/dm/thread 一致；**权限跟随所属会话**。不允许把文件路径/JSON 当正文；用户可见的永远是 attachment id/ref，不是本机 path。
**发送（human）**：composer 上传 → pending 托盘（#151/#154 已锁交互：输入区上方、限高内滚、可移除替换、不挡 Send）→ 发送后成为消息的 attachment part。
**发送（agent）——两种分享路径（Frank 修正：workspace 文件应能被授权用户看到，参考 Raft Workspace tab）**：
- **Share as attachment**：`upload → attachment_id → send/reply(parts 含 attachment)`——文件进会话资产，长期可见/可转发，source of truth 在 attachment store。
- **Share workspace file**：发 `workspace_file_ref`——用户点开进入 agent Workspace 文件视图浏览（对齐 Raft：Frank 能直接看 agent 的 workspace 文件）；路径必须是**受管 workspace 内的受控路径**，权限走 agent/workspace 授权 + 审计。
- **三种 file ref 分清（不混为一谈）**：`attachment_ref`（会话资产）｜`workspace_file_ref`（受管 workspace 文件，可浏览可显示路径）｜**raw host 绝对路径**（如 /Users/xxx/...，不可移植、对他人无效）——裸 host path 不能当聊天链接，发出前必须转成前两者之一。context_pack 记录使用/读取的是哪种 ref。
**查看（human）**：图片 = 有界内联预览（点开 viewer/下载）；其他文件 = file row 卡片（图标/文件名/大小 + 下载）；搜索/quote/thread 预览走 CompactSummary 文本；移动端查看体验进 #198 交互稿。
**查看（agent，省 token）**：附件内容**不自动注入**上下文；unread bundle/context_pack 只带元数据（id/名/型/大小）；需要内容时**显式 fetch**（权限限 conversation/thread）；**context_pack 记录本次 run 读了哪些附件/片段**——Activity 里可见，但附件本身不变成 trace/message。
**两个持久存储层（画进架构图）**：
- **Product Attachment Store**：消息/会话文件的 source of truth；
- **Agent Workspace**：`~/.multica/workspaces/<workspace_id>/agents/<agent_id>/`，既是持久私有区也是每次运行的 cwd；Agent 自己决定是否把附件或工具产物保存到这里。
**API 契约（Frank 定）**：查询/读取 attachment 时**必须返回可用 URL**（view/download），经权限校验签发（建议短时效 signed URL，不暴露裸 storage 路径）；消息里存的是 attachment_id/ref，URL 由查询时按当前权限动态给出。
**边界态**：上传中/失败可重试；删除后 tombstone「附件已删除」；**无权限只显示"无权访问的附件"，不泄文件名**；大小/类型白名单；留存跟随消息生命周期。
**Scope**：P1 = channel/dm 发送+查看（复用现有 attachment 关联/托盘/parts 契约）；**issue 附件维持现状 schema**，仅行为对齐。naming：UI 只叫「附件 / Attachment」。

## 5. 数据模型（P1，channel/dm）
**Conversation**: id, workspace_id, type(channel|dm), created_at, archived_at?, **last_seq**（per-conversation 消息序号计数器）｜channel: name/description/visibility(public|private)｜dm: 无名，participants 唯一（同 ws 成员对唯一）。
**ConversationMember**: conversation_id, **member_ref{kind:human|agent, id}**, role(channel: owner/admin/member), joined_at, left_at?, muted_at?, **last_read_seq**, closed_at?（DM 关闭，来新消息自动重开）｜dm 恰好 2 成员。
**Message**: id, conversation_id, **seq**（插入事务内从 conversation.last_seq 取号；unique(conversation_id, seq)）, author_ref{kind,id}, parts[], created_at(展示用)/edited_at?/deleted_at?, root_message_id?（thread 归属；只可指向非回复的顶层 message → 不嵌套）。**排序/分页/游标一律用 seq**。
**ThreadParticipant**（落表，非派生）: root_message_id, **member_ref{kind,id}**, followed_at, **last_read_seq**（共享会话 seq 空间）, role?。thread 聚合（reply_count/last_reply_at）可派生。
**Reaction**: message_id, member_ref, emoji, created_at, unique(message_id, member_ref, emoji)。
**Activity/Event**: 独立流，永不落 Message 行，与 copy/search/save/thread/reply/quote 隔离。
**读态迁移**：`channel_read`/`channel_thread_state`/`dm_peer_state`（timestamp、user-only）→ 统一 per-member（含 agent）cursor；新表并行 + 回填；**agent 首次获得 cursor**。

## 6. Thread → Agent 语义
**事件驱动，非旁观**：消息到达 → 后端按 Wake matrix 判断唤醒 → 入队一次 agent run → prompt 带 unread bundle → 输出 send/react/no_reply。

**Wake matrix（谁被唤醒因 surface 而异；喂法统一）**
| surface | 规则 |
|---|---|
| DM | direct delivery：对方发消息即唤醒 agent member（DM 内 thread 同此规则，不套 participant 门控） |
| Thread(channel) | participant delivery：follow/被@/参与过/被指派的 agent 收到投递；非参与不唤醒 |
| Channel 主流 | directed delivery，默认克制：@agent/@all、指派·认领·需行动、direct question 才唤醒；ambient 是例外策略且必须先过便宜 pre-gate（明显无关不起 run，#196 浪费根因） |
| **reconnect/restart 补偿唤醒（v2.4.9，#304，Frank 07-07 抓）** | **agent 一上线/重启就必须补处理离线期间到达的消息，不能等下一条新消息才醒。** agent offline/断连期间发来的消息照常持久化+分 seq（数据侧本就有），但历史上**没有"上线即扫未读补唤醒"这条路**——resume 不重新 wake → 消息静默漏（比内心戏泄漏更严重=不回错话、而是根本不回、丢工作）。规则：agent transition to online（reconnect/daemon restart/resume）→ 对每个 conversation 扫 `seq > last_read_seq`，有未读就触发补偿唤醒：**direct/@我/DM 必补一次 run；ambient 走 §6.3 合并唤醒（不逐条炸）**。幂等：补唤醒和后续新消息 wake 不重复处理同一 backlog（复用 pending_wake / cursor）。**实现口径（Frank 07-07 拍）=基于事件系统、事件驱动**：补偿唤醒**不是 reconnect 代码里的特例 handler**，而是**订阅 §6.2/#301 生命周期事件**——当 `agent_activity_event` 落地"agent online/reconnected/resumed"事件时，catch-up wake 作为该事件的**消费者**被触发。好处=任何让 agent 回 online 的路径（daemon restart/网络恢复/resume）都自动触发补偿，不用每条路径各自记得调；与"统一 activity 事件流、行为挂事件上"同一哲学。依赖 #301 先落 online/reconnect 生命周期事件。**自观测**：catchup 本身也记一条 `backlog_catchup` activity event（"这次上线补处理了 N 条离线消息"），补偿不是黑箱——连"补偿唤醒"都是可观测事件（visibility 由 Iris §6 清单定）。这是我在 §6.3 raft 对标点过的"重启撞积压不丢"失败路径——识别过但没落地，#304 补齐。落点=Wake matrix 本行 + §8 offline 语义 + §6.2/#301 事件源。 |

**参与投递 ≠ 必须回复**：
- 定向（@ / 指派 / 直接问题 / DM）⇒ **必须**可见 reply/ack（#196 must-reply，禁 silent no_reply）。
- 参与 agent 收到非 @ 续聊投递 ⇒ 可 no_reply（两个人类在 thread 讨论，agent 不被强制 ack）。
- 多参与 agent ⇒ 投递给所有参与者，各自 cursor 各自决定（对齐 Raft）。
- 退出/降噪：显式 unfollow / 人类可移除 agent；连续 N 条无 @ 自动衰减为不唤醒（仍可读）。
- 参与者定义：authored root / 被@ / 回复过 / 被指派。

**上下文喂法（unread-anchored）**：唤醒时喂 `seq > last_read_seq` 的 unread bundle（+root 锚点）；已读默认不重复注入但可 fetch/search 回查（权限限 conversation/thread，对齐 Raft `message search`/`read --around`）。**cursor 只在 run 成功完成/显式 ack 后推进**；排队/失败不标已读（at-least-once）。
**编辑/删除 vs agent 已读竞态（v2.4.2 锁死，三句）**：① run 在**执行时**读消息最新版（唤醒后被编辑→agent 看到的是编辑后内容）；② context_pack 钉住**实际读到的版本**（审计可查 agent 当时看的是什么）；③ **edit/delete 绝不产生新 wake**——"改错字"不能变成新的放大器（事故同款风险），要 agent 重新处理就显式再 @。
**并发正确性（结构保证，非补丁）**：seq 在插入事务内从 conversation 计数器取号，行锁串行同会话写入直至提交 → **seq 顺序 == 提交可见顺序**，游标不可能越过未可见消息，竞态从结构上消灭（已核码：现 `INSERT` 用 now()=事务开始时间，时间戳方案存在结构性乱序丢投递风险，故弃用 keyset+重扫）。代价=同会话写入串行（聊天负载可接受，Frank 已拍「接受改动」）。验收：并发写入下 agent 不丢投递。
**Token/Context**：token 大头 = 唤醒次数，第一闸是 Wake matrix + ambient pre-gate；**不建平台压缩/摘要**——产品契约 = unread cursor + raw delivery + fetch/search + context_pack 审计；runtime auto-compact 属 runtime 自身能力，非平台承诺。可观测：agent run 记 context_pack_id（raw messages/id-range/cursor advanced_to）。（context pack range 缓存 = 实现建议，不占验收。）

## 6.2 Activity / Trace 面（agent 活动可观测，Frank 硬要求）

**渲染分层（v2.4.8，Frank 7-6 贴 Raft 自身 Activity 佐证——两层都要，非二选一）**：同一条事件流、两种渲染，靠 BE 给每个事件标 **user-facing / diagnostic-only** 高度驱动：
- **默认 = 活动叙事**（Activity 主视图 + hover/profile 卡的 RECENT ACTIVITY 精简子集）：只吃 user-facing 事件——被谁唤醒→做了什么（人话 label+summary）→结果（completed / failed·人话原因 / **no-reply·原因** / **suppressed·原因**）→在线·断开·恢复。**活动行 = 时间 + 人话，无装饰 icon**（波形/圈 glyph 不用，标记至多极简状态点）。
- **诊断视图 = 保留**（现「ACTIVITY DIAGNOSTICS」整页 + Copy Diagnostic 降成显式入口，不删）：raw command/args/output、raw error 原文、thinking、subagent 内部、freshness hold/draft、synced context、target id、内部 task transition。**Raft 自身也保留诊断整页——不是把诊断删掉，是默认叙事 + 诊断一键可达。**
- **单一数据源**：叙事默认视图和诊断视图吃同一份 BE 事件高度标注，不是两套数据源；FE 不从 raw 文本猜高度（BE 标、FE 按标过滤）。落地：task #267（重做）/ #285·#287（聊天流小刀）。

**五个面，各归各位：**
1. **Message**：有意发的话，可 quote/react/reply。
2. **瞬时 presence**：「正在处理」聚合条（#10），短暂、不留史、不可引用。
3. **Activity/Trace（run 级，persistent 可查）**：以 agent run 为单位——何时被唤醒 / 被什么触发（trigger_message/conversation 引用）/ 看到什么（context_pack_id）/ 输出什么动作（send·react·no_reply + result_message 引用）/ 状态（completed·failed+原因）/ 耗时·token。**不是 message row，不可被 react/quote/reply**（守 #155/#157）。
4. **Audit 底层（context_pack 证据）**：raw message ids / 附件 ids / cursor advanced_to / runtime session / 权限来源；排障与审计用，不进普通用户 UI。
5. **Agent runtime lifecycle（v2.4.1 新增，Frank 由 Raft 0.67.0 dogfood 点出）**：**run 之外**的 agent 级生命周期事件，进同一条 Activity 时间线（低噪层，同平台决策层）——
   - **runtime error 显式化**：provider/auth/启动失败（如 API key 无效）必须显式入流，**禁止"看起来 active/idle 实际卡死"的静默态**（此时 run 级 Activity 空白，恰是盲区）；
   - **restart / resume**：重启与恢复事件，含 stopped 期间补投递（replay）了多少条；
   - **runtime / 模型目录版本变更**。
   归属：这些不挂任何 run，锚定 agent 本体；渲染与平台决策同层（低噪、结构化、不可 react/quote）。这是复盘教训「可观测性是止血前提」+「不显示假 active」（§8 agent offline 态）的 Activity 落点。

**现状基础（已核代码，~80% 数据层已存在）**：`agent_task_queue`（run 行：status/时间戳/result/error/initiator）；`task_message`（**step 级 trace 已在记**：seq/type/tool/input/output，经 `task:message` WS 实时推，FE 已有 build-timeline/TimelineView/issue-timeline/agent-activity-hover 渲染面）；`task_usage`（run×provider×model token 账）；`activity_log`（workspace/issue 审计）。

**要改的（P1）**：① run 行补 trigger_message_id/conversation_id + output_action/result_message_id（部分或在 result JSONB，BE 核实）；② 读 API `GET /agents/{id}/runs`（分页 + **按会话权限过滤**）+ run 详情（steps=task_message + usage=task_usage）；③ FE：agent 详情 Activity 时间线 tab（收敛现有 chat/issue/runtime 三套活动渲染为一套组件，避免平行抽象）+ run 详情视图；④ 留存：run 元数据长留（审计/成本），task_message step 明细设 TTL。**审计级 context_pack 记录 + 只读审计查询面是真正的新后端件**。

**权限**：run 记录可见性跟随所属会话权限（看不到该 channel/DM 就看不到 run 详情）；step 级明细默认 agent owner + workspace admin；agent↔agent DM 的 run 走 admin 审计（§9）。issue 侧 `comment.type=status/progress/system` 继续归 Activity 渲染（§7）。
**issue run 也进 Activity（Frank 确认）**：agent 被指派/被@处理 issue = 一次 run，trigger 记 issue_id(+comment id)，与 channel/dm run 同构；两个入口同一份数据——agent 档案页 Activity（跨 surface 时间线，target chip 指向 issue）+ issue 自身 activity 面（现有 comment.type/activity_log，行为对齐不迁 schema）；执行过程不落 comment 正文（#157 红线）。#197 run trigger 需支持 issue_id。

**设计基准（Frank 提供 Raft Activity 截图，存档 raft-activity-reference.png）**：
1. 条目分型渲染：Running command（显示实际命令行）/ Working / **Thinking（只显示标记，内容默认不展开）** / Output / 结构化事件（键值：target/decision/原因）；
2. 时间轴左栏 + 状态点，严格时序；
3. target chip 可点跳转到会话/消息（过权限）；
4. **平台决策也入 Activity 流**：唤醒判定（为什么派/没派）、must-reply 拦截、cursor 推进、dormant 触发——#196 类"为什么没回"直接可见；
5. Copy Diagnostic Info 一键复制诊断；
6. 入口 = agent 档案页 Activity tab；
7. **P1 即给 step 级实时流**（复用现有 task_message/`task:message` WS + FE timeline 组件）+ run 级分组——修正早前 run/step 分期：step 数据已在记，不必等 P2；
8. step 明细（含命令/正文）默认 agent owner + workspace admin，普通成员看 run 摘要。
9. **Activity = 只读观察面（Frank 拍定，v2.4.2）**：记录 immutable；唯一动作 = Copy Diagnostic（纯读，基准第 5 条）。**failed run 的 retry 按钮不做**（记 backlog，等"批量重试一排失败 run"的真实需求出现再议）；人类要重试 = 重新 @ agent。

## 6.3 合并唤醒 / 队列背压（#194，7-3 事故根治，产品不变量）
**事故教训（postmortem 规则5）：凡系统自动产生（ambient）的负载，必须有背压/上限，作为架构不变量——系统不该"猜"出无界负载。** 7-3 队列风暴 = 每条非@消息 ambient 无脑扇给全部 agent、每个入一 run、`agent_task_queue` 无入队背压 → 排到 27 万行打挂 DB。以下是把 ambient 从"放大器"改成"有界信号"的产品口径（Phase 0 已落 PASS，后续 Phase 1+ 继续）：

**① 两条投递路径分开（naming 清晰、语义不同）：**
- **direct delivery**（@agent/@all、DM、指派·认领·直接问题）= 进 **durable queue**：可靠、有序、必达、必须产可见结果（#196 must-reply）。这类是"真的要 agent 干活"。
- **ambient delivery**（频道里非定向的普通消息）= **不再物化成 per-message×per-agent 任务行**；改**合并唤醒**：每个 (conversation, agent-member) 维护一个 **pending_wake 标记 + cursor**，agent 醒来一次性消化 `seq > last_read_seq` 的 unread bundle（§6 喂法），**N 条 ambient 消息 → 最多 1 个待处理 run**，而非 N 个 run。这从结构上消灭"一条消息 = 全频道 agent 各起一 run"的乘积爆炸。

**② relevance pre-gate（便宜前置，起 run 前过滤）：** ambient 内容明显无关就不起 run（deterministic skip：空内容 / ≤8 字符纯标点无字母数字；不确定→放行，保守不误伤）。这是 #196 token 浪费与事故放大的共同前置闸。落点见 #194 Phase 0 `channel_ambient_gate`（已 PASS：advisory lock 串行 gate-recheck+enqueue、fail-closed、direct 路径不受门控、每次决策记 metrics/audit reason）。

**③ 熔断（circuit breaker）：** 单频道 wake 速率超阈值 → 自动暂停该频道 ambient 唤醒（direct 仍通），并显式记录/可观测（进 §6.2 Activity 平台决策流："ambient 已因超阈暂停"）；人工/冷却后恢复。防止单频道把整个 agent 队列拖垮。

**④ 队列背压（durable queue 侧）：** 入队深度上限 + 去重（同 (conversation, agent, wake) 不重复堆积）；接近/超限显式降级并可观测，**不静默无界膨胀**。

**⑤ 可观测是止血前提（事故教训）：** 每个 wake/gate 决策记 reason（唤醒/跳过/熔断/降级）；带外 kill-switch（频道级 ambient-off）+ 健康面，让下次"看得见、断得掉"。

**⑥ 成本护栏（observability follow-up，Frank 已点头方向）：** §6.3 熔断是 wake **频率**维度；成本维度缺位——agent 陷入"低频但每 run 巨贵"的低效循环时只能等账单。数据已有（task_usage per run×model token 账），缺产品面：**先视图后护栏**——(a) per-agent / per-channel 成本聚合视图（owner/admin 可见，进 agent 档案页与 admin 面）；(b) 可选预算上限（per-agent 日/周 token budget，超限→dormant + 显式通知 owner，不静默）。同一条不变量：**自动产生的花费也必须有界。**

**⑦ spawn 级削峰（7-6 raft 对标补，daemon 端）：** ④的队列背压是 run 级；真正的宿主机资源尖峰是**同秒多 agent 冷启动**（一条消息→频道内全部 agent 同时拉起进程）。对标 raft `AgentStartCoordinator`（maxConcurrentStarts=5 + 500ms 间隔，slot 只占进程拉起一瞬、不占整个 run）：daemon 端对 agent 启动加**并发上限+出队间隔**，削 spawn 风暴而不限制在跑 run 数。配套参照：忙时通知合并要带 **fingerprint 去重 + already-contributed 抑制**（同一事件不重复打扰已参与的 agent）——这是 wake/cursor"不重复打扰"的工程面。归 #194。

**⑧ server⇄daemon 双向探活（7-6 raft 对标补，daemonws 连接健康）：** 长连接会**无声死掉**（半开连接：网络切换/中间设备重置——不报错、只是永远安静），只靠单向心跳有盲区。对标 raft 双侧机制：**server→daemon 定期 ping**（~30s，漏跳→判机器失联→该机 agent 标 offline）；**daemon 侧沉默看门狗**（收包静默超阈值 ≈2×ping 间隔+余量，raft=70s → 主动发 liveness probe，无回应→判连接死→断开重连自愈）。缺 daemon 侧半边的后果：连接假死后 agent **显示在线但永远收不到消息**——最糟的故障形态（看着在线、喊不应）。分层纪律：**心跳/探针只管连接健康，永不计入"有工作/被唤醒/已读/投递"**（与 deliver/start 业务动词分离）；#178 Runtime Health 的在线判定以此心跳为源，不用"最近发过消息"近似。**产品面（Barry 两层落法）**：连接健康是**四态状态机**——在线 / 疑似断连 / 重连中 / 已恢复——不是布尔；底层 lifecycle 事件（ping received / probe sent / probe timeout→reconnect / reconnected）直接映射这四态，用户看状态、工程看事件时间线，不翻原始 log 猜。定位=runtime 可观测性与自愈的验收锚，**不是新聊天功能、不回头扩 #259**。归 #178/#194 失败路径验收。

**验收锚**：ambient 高频消息下 agent run 数有界（合并唤醒，不随消息数线性爆炸）；direct 必达且 must-reply；relevance pre-gate 不误伤（不确定→放行）；熔断与背压在压测下生效且可观测；direct 与 ambient 路径隔离（ambient 熔断不影响 @/DM）；**同秒 N agent 唤醒时进程拉起被削峰（⑦），且验收按失败路径验（投递失败重试/重启积压不丢/卡死 watchdog 兜底），不只 happy path**。**与 §6 Wake matrix 是同一套：Wake matrix 定"谁被唤醒"，§6.3 定"被唤醒如何不成灾"。**

## 6.4 时间驱动唤醒 / Reminder（P2 候选，Frank 已点头方向；源自 Raft dogfood）
**问题**：Wake matrix（§6）全是消息驱动——agent 无法"到点跟进"（等 CI 绿再看 / 明早催人）。现状两条烂路：死等占 runtime，或干脆忘掉。Raft 的 reminder 是 agent 从"被动应答"到"可靠同事"的关键能力（Parker 日常在用）。
**模型（对齐 Raft）**：Reminder = **author-owned** 的持久唤醒信号——谁建的到点唤醒谁（不转移 wake 所有权；要通知别人 = 醒来后自己 @ 对方）。锚定 message/thread（fire 时该 surface 可见系统提示）；可改期（snooze）/更新/取消/列表可查。
**落点**：Wake matrix 加第四行「**time trigger**」：reminder 到点 → 唤醒 author agent 一次 run（direct 级、durable、不受 ambient 门控/熔断影响；但计入 per-agent 并发与 §6.3④ 背压）。数据：reminder(id, author_ref, anchor_message_id?, fire_at, note, status)。
**边界**：到点时 agent 离线 → 入队待上线（同 §8 offline 语义）；重复 fire 幂等；per-agent reminder 数量上限（防自我放大：agent 给自己排 1w 个闹钟也是无界负载，§6.3 不变量同款）。验收：schedule→到点唤醒 author→run 里可见 reminder 上下文；snooze/cancel 生效；离线补唤醒；数量上限显式报错。

## 7. Issue 线（schema 冻结，行为对齐）
- **可见性维持现状**（#72 已核：issue detail 按 **workspace member** 可见；assignee 是分配/筛选，不是 ACL）。UI 保留「Assigned to me」筛选，详情页不得暗示"没分配就不能看"。本轮不改。
- **回复 ≠ 执行结果（渲染分离，需求①核心）**：现有 `comment.type` 已区分 comment/status_change/progress_update/system——真评论渲染为讨论消息（可 react/quote）；status/progress/system 渲染为 Activity 样式（不可当消息 react/quote 引用）。**不改表，只改渲染与写入纪律**（agent 执行结果走 status/progress 或 Activity，不写成 comment 正文）。
- **触发策略对齐（策略层，不动 schema）**：issue comment 里 @agent/指派 ⇒ must-reply；被指派/评论过的 agent 收到续聊投递可 no_reply；reaction 不唤醒。与 §6 同一套规则口径。
- **reaction（需求④）**：`issue_reaction`/`comment_reaction` 现表已工作，保留；仅对齐行为口径（权限/通知/不唤醒）。
- **兼容修复**：#27（进入 issue 后被锁死、channel 里 @ 不回）按上面触发策略修，不需要新模型。
- **未来**：若要将 issue 接入统一讨论引擎，单独立项评审；倾向 anchor-message 方案（同时解 root 挂接与 issue 本体 reaction 落点），本轮不做。

## 8. DM 状态机
`active`（正常）→ `closed`（closed_at；关闭移出列表；来新消息自动 reopen + 真实未读）｜`muted`（muted_at；静默投递、无红点；human 静音 agent 即此态）｜`agent offline`（入队 + 唤醒投递 + 真实未读；不显示假的「正在回复」；**agent 一上线/重启必走 reconnect 补偿唤醒补处理离线期间消息，见 §6 Wake matrix，#304——离线期间到达的消息不能静默丢**）｜`permission-revoked`（私有 agent 访问被撤 → 不可访问/隐藏、必须重查，对齐 #191）｜`dormant`（P3，见 §9）。

## 9. Agent↔Agent DM 治理（P3）
- `admin_visible`: **默认 TRUE，Frank 已批准**（会话头安静常驻「此对话 admin 可见」，非 banner）。
- `context_isolation`: 强制——不自动带 task/issue 上下文；引用须显式 quote/link。
- `loop_guard`: 连续 N 轮 agent↔agent 无 human 无进展 → `dormant`「已暂停，等待人接入」+ 唤醒 CTA；human 发话/点击恢复即续；阈值可配。**已批准**。

## 10. 权限 / 审计
发送权限 = workspace member + agent permission + conversation membership；执行/审计用 runtime session token（记 actor/workspace/session 来源）。channel 随 visibility；dm 仅双方（agent↔agent 视 admin_visible 增审计）。**对齐 #190（WS broadcast 限授权接收方）/#191（private-agent DM recheck）**。fetch/search 工具受 conversation/thread 权限限制，不越权读全 workspace。

## 11. 边界状态
空会话态｜接收 agent 离线→入队+唤醒投递+真实未读｜权限拒绝显式 reason（不泄无权正文）｜delete/archive/close 三态区分｜未读真实计数（非恒 1）｜human 静音 agent→静默投递｜仅同 workspace｜dormant 显式态｜admin 审计视图｜**仅 archived 锁 thread 只读**（issue done/blocked/cancelled 仍可追评/reopen）｜并发写入不丢 agent 投递（§6 兜底）｜reaction 不唤醒 agent。

## 12. 分期 → 任务
- **已落地基座**：#197 seq/conversation schema（migration 144）**已 done + 产品 PASS**（handler read/unread 口径确认；agent-run cursor 推进遗留 gate 见 §6/§13.2）；#194 **Phase 0 ambient 队列门已 PASS**（§6.3 ①②基础）；§4.2 send 幂等（#207）契约已锁。
- **P1（#197 BE 续 / #198 FE-UX）**：Conversation/Message/ThreadParticipant/Reaction/per-member cursor（含 agent、ack 推进、重扫兜底）+ Human↔Agent DM 产品化 + Wake matrix + **§6.3 合并唤醒/熔断/背压后续阶段**。前置：#190/#191、#192。
- **P2（#199/#200，改小）**：issue 行为对齐——comment.type 渲染分离（回复≠执行结果）+ issue 触发策略对齐 + reaction 行为口径。**无 schema/迁移**。
- **P2 候选（v2.4.2 新增，Frank 已点头方向，实施等排期）**：§4.3 陈旧发送保护（freshness hold）；§6.4 时间驱动唤醒/Reminder；§6.3⑥ 成本视图→护栏（observability follow-up）。
- **P3**：agent↔agent dm + 治理（admin_visible 待拍）。
- **QA（#201）**：§13 验收 + issue「不被本轮改坏」smoke + **§6.3 ambient 有界性压测**。

## 13. 验收标准
1. channel/dm 统一原语：message/reply/react/未读/mute/follow/搜索一套引擎；thread 扁平不嵌套；thread 回复不进主流不计会话未读；回复目标严格复用来源 target，开 thread 必须显式发起。
2. per-member cursor（含 agent）落表 = `last_read_seq`；**per-conversation seq：seq 顺序==提交可见顺序（结构保证）**；run 成功/ack 后推进；并发写入不丢投递；真实未读计数。
3. Wake matrix 生效：DM 直达；thread 参与投递；channel 定向克制 + ambient pre-gate；directed 禁 silent no_reply（#196）；参与投递≠必须回复；unfollow/衰减可用；reaction 不唤醒。
4. DM 体感对齐 Raft；状态机（active/closed/muted/offline/permission-revoked）正确。
5. issue：schema 零变更；回复≠执行结果渲染分离生效；issue/comment reaction 行为口径一致；#27 场景修复；现有功能 smoke 通过。
6. 权限对齐 #190/#191；fetch/search 权限受限；context_pack 可观测。
7.（P3）admin_visible（默认 TRUE，已批准）/ context_isolation / loop_guard→dormant 均为显式状态。
8. **agent 活动可观测（§6.2）**：每个 agent 有 Activity 时间线（P1 含 step 级实时流 + run 级分组）；run 详情可查 steps+usage；平台决策（唤醒判定/must-reply/cursor/dormant）入流；**runtime lifecycle 事件入流（provider/auth/启动失败显式化、无静默卡死态；restart/resume 含 replay 计数）**；可见性按会话权限过滤；activity 永不落 Message 行。
9. **Attachment（§4.1）**：human 发送（托盘→part）/查看（内联预览/file row/viewer）；agent 显式 fetch（元数据默认、内容按需、context_pack 记录 ref 类型）；agent 分享二选一（attachment upload 或 workspace_file_ref），裸 host path 必须先转换；无权限不泄文件名；删除 tombstone；issue 附件现状不动。
10. **send/reply 幂等（§4.2，#207）**：同 client_message_id 高频/重试/连击只落一行、网络只一次 send、后端一次 dispatch；200 dedup 透明成功不弹错、409 真冲突可见错误+reset、网络/5xx/权限/校验真失败可见+可重试；in-flight guard + debounce + send timeout/abort。
11. **合并唤醒 / 有界 ambient（§6.3，#194）**：ambient 高频消息下 agent run 数**有界**（合并唤醒不随消息数线性爆炸）；direct 必达 + must-reply；relevance pre-gate 不误伤（不确定→放行）；熔断 + 队列背压压测下生效且可观测；direct 与 ambient 路径隔离（ambient 熔断/背压不影响 @/DM）。
**待拍点：无。**

## 14. 变更记录
- v2.4.10（7-20，Raft 回复目标规则对齐）：退役 `show_in_channel`/`also_send_to_channel` 与 thread→主流投影；历史投影记录迁回 thread-only。agent 收到群顶层/DM/thread 的消息时，回复 target 必须原样复用来源 target；开 thread 只能显式动作，禁止按内容或语气推断。
- v2.4.9（7-7 早，Frank 抓离线消息+重启后没回=#304）：§6 Wake matrix 加「reconnect/restart 补偿唤醒」行——agent 上线/重启即扫每 conversation seq>last_read_seq 未读、有就补唤醒（direct/@/DM 必补一次 run、ambient 走 §6.3 合并唤醒、幂等不重复处理 backlog）；§8 agent offline 态引用该补偿唤醒。根因=我在 §6.3 raft 对标点过的「重启撞积压不丢」失败路径识别过但没落地，静默丢消息比内心戏泄漏更严重。归 #304 BE。
- v2.4.7（7-6 午，Frank DM 定版 session 策略）：§4.4 session 从"实现细节不动"升级为定版两句——**聊天=每 agent 一条常驻 session**（所有 DM/频道进同一条，raft 身份 session 同款；聊天侧由"每 chat_session 一条"合并）；**issue=(agent_id, issue_id) 新开 session**（与现状同键，零改动）。配套：常驻 session 压缩重启以 #273 agent 记忆为恢复锚、issue session 注入同一份记忆（两个 session 世界靠一份记忆连接）；聊天串行/issue 并行。
- v2.4.6（7-6 午，Frank DM 定调"聊天为主、issue 为辅"）：新增 **§4.4 聊天为主工作模式**——产品只有一种模式（聊天），issue=聊天里长出的工作产物；核心原则"**上下文进制品、记忆进 agent、session 只是优化**"：①下发 issue 写全上下文（来源 thread+决定+边界，复用 ChatContextSummary），session 续接降级为性能优化；②交付回流原 thread @发起人（脐带闭环）；③薄的 agent 级持久记忆（跨活儿偏好/约定/原则，聊天与 issue run 共用一份，对标 raft 身份 MEMORY）。session 架构（per-issue/per-chat resume）不动。P1 候选，排 #259/#178 主线后，待 Miles 排期。
- v2.4.5（7-6，raft daemon 对标——Frank 贴生产日志两轮，Parker 分析 + Miles 对 raft 源码核实，两条进 gate）：①**§4-3 凭证隔离**：task 凭证 per-agent/per-run 文件级隔离（禁共享 env/长期 token；raft `cli-transport/<agent>/<run>/agent-token` 为参考形状），作为 #259 merge 前 gate——我们与 raft 独立收敛到同一 CLI carrier 架构，验证了 §4 运输层方向；②**§6.3⑦ spawn 级削峰**：daemon 端 agent 启动并发上限+间隔（raft `AgentStartCoordinator` maxConcurrentStarts=5/500ms，slot 只占拉起一瞬），削同秒冷启动风暴，配 fingerprint 去重/already-contributed 抑制参照，归 #194。对标结论（不改道项）：deliver/start 动词分离+忙时降级 content-free 计数=我们 wake/cursor 同思路已在 raft 生产验证；异构 runtime 统一 start/deliver 契约=#178 Runtime Health 建模参照（在线判定用心跳，非"最近发过消息"）；动词级版本号（如 reminder.cancel v2）=#262 capability gate 同族。总原则：**验收按失败路径验（投递失败重试/重启积压不丢/卡死 watchdog），不只 happy path**。（7-6 追补③：**§6.3⑧ server⇄daemon 双向探活**——server 30s ping 判死机器 + daemon 70s 沉默看门狗判死连接自愈重连；心跳只管连接健康、不计业务；防"显示在线但收不到消息"的半开连接假死。归 #178/#194。）
- v2.4.4（7-5 晨，Frank 连拍三刀，§4 定版）：①**动词 3→2 + 沉默**：删除 no_reply 动词（Frank："不调 send 不就是 no_reply 么"）——agent 只有 send/react 两个动作，不回=什么都不做；no_reply 降级为平台观察的 run 完结状态（结算游标/判 must-reply/喂注解），非 agent 表态；②**运输层 = CLI**（Frank："MCP 太占上下文"）——daemon 注入 `multica` CLI（PATH+task 凭证）直打 server API，agent 最终文本永不解析为协议（#255 砍文本 JSON 信封；unwrap 只作历史防御）；幂等复用 §4.2 client_message_id；③**`also_send_to_channel` 更名 `show_in_channel`**（Frank："名字难受"；"send"暗示再发一条=carrier bug 方向）——语义定为同一消息投影主线（一行两面，task #256 退役 carrier 副本），频道侧「↩来自 thread」可点回跳。落地：#247（CLI 载体）/#255（存量迁移）/#256（投影）。
- v2.4.3（7-4 晚，Frank 拍"DM 命令 naming/调用方式对齐 Raft"）：§4 动作合约改 **target-based**——动词 4→3（`send(target,parts,options?)`/`react`/`no_reply`），`reply`/`send-to` 收敛为 target 形态（`#channel:<msgid>` / `dm:@name` find-or-create）；统一 server target parser；`dm:@name` 用 handle 不用 UUID；agent 主动 Human DM=task #247（agent↔agent 仍 P3）。Barry BE schema gate 已 PASS。
- v2.4.8（7-6 晚，Frank 贴 Raft 自身 Activity 整页佐证）：§6.2 加**渲染分层**——同一事件流两种渲染，BE 标 user-facing/diagnostic-only 高度驱动；默认=活动叙事（主视图+profile 卡精简子集，时间+人话、无装饰 icon、no-reply/suppressed 原因保留 user-facing），诊断视图保留（现 DIAGNOSTICS 整页+Copy Diagnostic 降成显式入口不删，对标 Raft 也留）；单一数据源、FE 不猜。落地 #267/#285/#287。
- v2.4.2（7-4，Frank DM 拍定 5 项 proactive gap 裁决）：①§6 加**编辑/删除 vs agent 已读竞态**三句口径（run 读执行时最新版 / context_pack 钉版本 / **edit·delete 绝不产生新 wake** 防新放大器）；②新增 **§4.3 陈旧发送保护 freshness hold**（P2 候选：send API 前置 seq 校验+有界 delta 补投+draft 暂存+显式二次决定）；③新增 **§6.4 时间驱动唤醒/Reminder**（P2 候选：Wake matrix 第四行 time trigger，author-owned，数量上限防自我放大）；④§6.3⑥ **成本护栏**（先视图后预算上限，observability follow-up）；⑤§6.2 基准#9：**Activity=只读观察面**，retry 不做进 backlog，Copy Diagnostic 保留。
- v2.4.1（7-4）：§6.2 四面→**五面**：新增「Agent runtime lifecycle」事件源（runtime error 显式化·禁静默卡死 / restart·resume 含 replay 计数 / 版本变更），run 之外的 agent 级事件入 Activity 低噪层（Frank 由 Raft 0.67.0 dogfood 点出 run 锚定 Activity 的盲区：起不了 run 时什么都看不见）。§13#8 验收随之含 lifecycle 事件。
- v2.4（7-4 刷新）：**并入 7-3 队列风暴事故硬化**——新增 §6.3 合并唤醒/relevance pre-gate/熔断/队列背压（#194，产品不变量：系统自动产生的负载必须有背压/上限）；标注已落地基座（#197 schema=migration 144 已 done+产品 PASS、#194 Phase 0 ambient 门已 PASS、§4.2 #207 契约已锁）；§12 加"已落地基座"段 + §6.3 后续阶段；§13 补验收 #10（send 幂等）/#11（有界 ambient）；**新增 §16 v2.3→v2.4 Delta / Implementation Map**（Miles 排期 + Iris #201 QA reconcile 用：A 已落地基座/B 本次 delta/C P1 继续项/D 未决 gate，每行带 task+验收锚）。目的=给 Miles 单一最新 canonical 版拆实施，消除散在各 thread 的增量。
- v2.3：**排序/游标改判为 per-conversation seq**（Frank「完美方案、可接受改动」；代码核实 now()=事务开始时间使 keyset 存结构性丢投递风险；seq 取号行锁保证 seq 顺序==提交可见顺序，删除重扫窗口方案；cursor=last_read_seq，thread 共享会话 seq 空间；created_at 仅展示）；补「issue run 进 Activity」（trigger 支持 issue_id，双入口同一数据）。
- v2.2：新增 §4.1 Attachment/Files（模型/human·agent 发送查看/三存储层 Product Store·Run Workspace·Agent Workspace/边界态）；§6.2 补 Raft Activity 设计基准 8 条（Frank 截图），P1 即含 step 级流；§13 加验收 #9；§15 补代码事实（Multica 无持久 per-agent workspace，仅 per-run workdir + project 目录 + agent_memory 表）。
- v2.1：admin_visible 默认 TRUE 获 Frank 批准（待拍点清零）；新增 §6.2 Activity/Trace 面（run 级活动记录 + per-agent Activity 时间线 + 权限过滤 + 留存；数据层基于现有 agent_task_queue/task_message/task_usage）；§13 加「agent 活动可观测」验收。
- v2.0：合稿；**scope cut：冻结 issue 数据模型**（A1/A2/A3 随之消失），issue 侧改行为对齐；全文清除 issue=message.kind / seq / 4 态 status / 宿主 channel 可见性 / 参与必回 等旧表述；cursor 统一 keyset 语言；补 at-least-once 并发兜底；also_send_to_channel 进动作合约；context-pack 缓存降为实现建议。
- v1.2：收 Frank 代码级 review 7 条（§16 补丁式，已并入本稿）。
- v1.1：去平台压缩；多参与 thread 对齐 Raft；attachment/可观测；loop_guard 获批。
- v1.0：初稿。

## 15. 关联独立方向：Agent Memory / Workspace
Frank 已确认要做：per-agent 持久私有 workspace/memory（跨会话 MEMORY.md/notes/artifacts，像 Raft 一样让 agent 跨时间积累专长）。与本 PRD 的会话层（共享、权限/审计）是两层。
**当前代码事实（2026-08-05）**：每个 Agent 使用 `~/.multica/workspaces/<workspace_id>/agents/<agent_id>/` 作为持久根与 cwd；同一 Agent 跨 task、daemon 重启和 provider 切换复用。旧 per-run 目录不迁移、不扫描、不删除。
单独 PRD 覆盖：存什么/谁能看/审计/容量清理/导出删除/与会话 attachment 的边界（显式 Save to agent workspace 才写入，见 §4.1）。

## 16. v2.3→v2.4 Delta / Implementation Map（供 Miles 排期 + Iris QA reconcile）
**给排期表用：一行一决策/工件，标状态 + 对应 task + 验收锚。已落地不重做，未决 gate 不抢跑。**

### A. 已落地基座（done / PASS，作为底座不重做）
| 工件 | 状态 | task | 验收锚 |
|---|---|---|---|
| seq / conversation schema（migration 144：per-conversation seq、幂等 backfill、member/thread cursor、read-state 迁移） | **done + 产品 PASS** | #197 | §5 / §13.2 |
| handler read/unread 口径（只显式 read/follow 写 cursor；unread=seq>last_read_seq；应用层不手写 seq） | **PASS** | #197 | §13.2 |
| ambient 队列门 Phase 0（advisory-lock 串行 gate+enqueue、fail-closed、direct 不受门控、relevance pre-gate、每决策记 reason） | **PASS（PR#220）** | #194 Ph0 | §6.3 ①② |
| send/reply 幂等契约（client_message_id + 200 dedup/409/失败三分流 + in-flight guard/debounce/timeout） | **契约已锁** | #207 | §4.2 / §13#10 |

### B. v2.4 新增/收敛决策（本次 delta）
| 决策 | 说明 | 落点 | 验收锚 |
|---|---|---|---|
| §6.3 合并唤醒 / 队列背压 | ambient 不物化 per-msg×per-agent 行→pending_wake+cursor 合并唤醒；relevance pre-gate；熔断；背压。产品不变量=自动负载必须有界 | §6.3 | §13#11 |
| direct vs ambient 双路径 naming | direct→durable queue 必达+must-reply；ambient→合并唤醒有界信号 | §6.3① | §13#3/#11 |

### C. P1 实现继续项（Barry 两件前置收口后开）
| 项 | owner | 基线 | 验收锚 |
|---|---|---|---|
| Conversation/Message/ThreadParticipant/Reaction/per-member cursor（含 agent、ack 推进、重扫兜底）+ Wake matrix | BE（#197 续） | migration 144 底座 | §5/§6/§13.2 |
| Human↔Agent DM 产品化（列表/未读/静音/搜索/关闭/隔离/权限/审计；体感对齐 Raft） | BE+FE #198 | Iris #198 交互稿 | §13.4 |
| FE-UX shell（DM/thread/reaction/未读/mute/follow） | FE #198 | Iris #198 交互稿 | §13.1 |
| Activity/Trace 面（run 级分组 + step 级实时流 + 平台决策入流） | BE+FE #198 | 现有 task_message/task:message WS | §6.2/§13#8 |
| Attachment（托盘→part；agent fetch 元数据默认/内容按需；分享二选一；signed URL；无权不泄名） | BE+FE #198 | 现有 attachment 契约 | §4.1/§13#9 |
| §6.3 合并唤醒/熔断/背压完整实现（Phase 0 之后阶段） | BE | #194 Ph0 | §13#11 |

### D. 仍未决 / 后续 gate（不在本轮闭环，标清避免抢跑）
| gate | 触发点 | task |
|---|---|---|
| agent-run cursor 推进（只在 run success / explicit ack / no_reply completion 后推进；排队/失败不推进） | agent delivery/ack 真接上时（Barry 拉 Parker 复核） | #197 后续 |
| issue 行为对齐（comment.type 渲染分离 + 触发策略对齐 + reaction 口径；**无 schema/迁移**） | P2 | #199/#200 |
| agent↔agent DM + 治理（admin_visible 默认 TRUE 已批准 / context_isolation / loop_guard→dormant） | P3 | — |
| Agent Workspace（持久私有区，独立 PRD v0 已出） | 等 Barry BE 可行性 | #204 |
| issue 接入统一讨论引擎（倾向 anchor-message） | 未来单独立项 | — |

**排期原则（呼应 Miles）**：按独立可并行 Phase 切、不做顺序长链；每实现任务带 owner + PR/CI/React Doctor/后端测试/UX-QA 验收锚。**A 段不重做、D 段不抢跑、C 段等 Barry 两件前置。**
