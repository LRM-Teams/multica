# Engineering Principles — 隐性知识入基建（living doc）

> 来源：Frank 2026-07-16 指令「把隐性知识写进基建里……但是尽量避免隐性知识」。
> 本文是团队规矩/契约的**索引与"为什么"**；能进代码的必须进代码，文档只放代码装不下的判断。
> 维护规则：**每次拍板/定契约，当天写进本文或其指向的文档——聊天记录不算存放地。被否掉的设计更要记原因。**
> （活反例一：2026-06-29 commit `8fa3f2f90` "keep issues out of mention picker"——一个被亲手拆掉的设计，commit body 为空、无理由无任务号；17 天后同一个设计差点被原样重建，因为"为什么拆"只存在于记忆里。）
> （活反例二：`79fbca91f`/PR #183 给 editor Link 加 `Link.extend({inclusive:false})`——一个改变每次按键 mark 边界行为的开关，零注释零理由；URL P0（#531）排查中团队为"它为什么在、能不能动"考古一小时。两个反例同一天各烧一小时，都源于"当天没把理由写下来"。）
> （活反例三：task #537 描述里的 "fix direction: include CJK unified ideograph ranges"——写下来的方向被评审否决两次后仍挂在任务卡的权威位置，执行人动手前读的正是它；实测证明照它做会把一条现在绿的契约打红。**没写让人去问；写了但过期让人照做**——被推翻的记录必须当场改掉，"我认错了"≠"记录已修正"。三个反例合起来才完整：两个"没写"、一个"写了但过期"。）

---

> ## 「外观一致不靠两边小心写得一样，靠结构上没法不一样。」
> — Felix, 2026-07-16
>
> 每条规矩先问：**"能不能让它在结构上没法违反？"** 能 → 它该活在代码里，不该活在文档里。

## 0. 标签制（本文的使用规则）

每条规矩**强制标注**其中之一：

- **`可执行`** — 已经或必须变成 lint / test / 门禁 / 类型 / API 形状 / 共享组件。文档只留"为什么"+ 指针。**三要件缺一 = `仅文档`：**
  1. **指认到具体的物**（哪条 lint、哪个 test、哪个类型、哪个 API 形状）；
  2. **owner 签字**：由**要实现它的工程师**认领；没 owner 签 = `仅文档`。
     （2026-07-16 实证：设计侧三条提案两条边界画错——`IssueChip` 全局禁会误伤编辑器操作态；"客户端不能排序"会把合法的本地数组扫进去。边界只有 owner 知道。）
  3. **owner 见过它红——对每一条已知入口**：**每个验证装置必须先被证伪一次**——"没人见过它失败的 lint，等于'我希望它存在'"（Felix）。
     **「见过它红」≠「见过它对每一条入口都红」**：装置可以在你测的那条路上完美工作、对另一条路完全不设防（实例：`IssueChip` 直接 import 被 lint 拦、barrel re-export 绕过）。**没穷举入口 = 没资格标 `可执行`。** 优先让入口只剩一条（结构唯一）而非枚举禁止所有入口——**允许清单发给会增长的一侧 = 保证漂移**。
     **验证装置本身会静默空转**，四个实例同一个毛病（全都"看起来在工作"——绿的、跑了、有断言，但什么都没测）：
     fixture 先改后写→快照天然等于真值（#622）；`data-ref-source` 若 `AppLink` 不透传 props 就是静默 no-op（#638 差点）；测试 mock 吞掉属性→断言变成测 mock（#638 差点）；量到 wrapper 元素→"token 不是蓝的"假结论。
     **第二亚种——"装置忠实地守着一个已不存在的世界"**（#530/#648）：函数没坏、测试全绿，但它理解的输入语义已被后来的迁移换掉（`formatMessagePartsPreview` 写于 #463 之前，只认 text/sticker；#463 后 parts=[reference]，它照常"正常工作"着返回空）。**这类 bug 没有任何测试会红——它的测试测的是它自己那套已过时的世界。** 换语义的迁移（如 #463）要 grep 所有读该结构的函数，不只改写端。
     **观察（未处置）："同一段文本被不同层各处理一遍"的地方是虫的栖息地**——今天两只 autolink 虫都长在写侧/读侧双处理的缝里；已知另一处：readonly 读路径复跑写侧 `preprocessMarkdown`（幂等已验、暂不动）。盘点这类双处理点，值得进未来的审计清单。
     **样板流程（hover 验收三步法）**：先把实体改成"错的"值→hover 必须显示错值（见红=证明真现查）→再改回→这时的 PASS 才是真 PASS。只做最后一步=空转。
     **「见红」还需配「对照组」**（#644 补全）：「见过它红」管"它会失败"；**对照组管"它是为了对的理由绿的"**——一条"必须没有 X"的断言，在"什么都没有"时也是绿的（hook 什么都不弹→三条抑制测试全绿）。**每条「必须没有 X」都要配一条「Y 必须还在」**，否则"什么都不渲染"是一个通过态。双向都验过（先撤修复见红+对照组在）才算完整装置。
- **`可执行` 是梯子，不是一格——标注时必须写明档位，能上一档就上一档：**
  **① 不存在**（字段/export 删掉——做错的事没有对象，不需要执行者）→ **② 类型系统**（编译错误，不可绕过）→ **③ 单一结构**（只有一个组件/入口）→ **④ lint**（可绕过、有 scope 边界）→ **⑤ 测试**（只测你想到的路）→ **⑥ review 红线**（人记得 = 其实是散文）。
  **同一个动作的三个实例**（2026-07-16）：#507 删 `part.ref_status` 字段（读不到过期快照，因为快照不存在）；#638 只留 `IssueRefLink` 一个组件（长不出第二副外观，因为没有第二个组件）；#639 删 barrel re-export（走不了那条 import，因为入口不存在）。**共同点：不是"禁止做错的事"，是"让做错的事没有对象"。禁止需要执行者，执行者有边界会漂移；不存在不需要执行者。**
  **scope 必须按"半"写清**（部分保护+完整信心比没保护更危险）：如 IssueChip 收口=两行——barrel 入口全仓不存在（编译期，含 apps/*）；深路径 lint 仅 `packages/views`（apps/* 深 import 为已知边界、非已知洞）。
- **`仅文档`** — 只有真判断题（权衡、口径、依据分级）才够格。
  - **每条 `仅文档` 都是欠债，不是终点。哪天能变 `可执行`，就该搬走。**

**稿里的"顺手写法"会被当成规范读**（#MUL-123 笔误案）：identifier 等形式要么全稿一致、要么明说哪个是规范形式——歧义不会停在稿里，它会变成三个月后的"为什么没做"（Frank 就是照着笔误来问的）。

**同类问题修到第二次，就该变成规则**（Frank）：不再让 agent/人一次次修实例，写成 lint/CI 一劳永逸。

**两个标杆案例**（新规矩照这个标准落地）：
1. **"把规矩变成一个不存在的字段"**：「hover 卡不许拿过期快照兜底」的可执行形态 = #507/#625 把 `part.ref_status`/`ref_title` 整个删掉——**没人能读到过期快照，因为快照不存在了**。配套 write-then-mutate 回归（#622）。
2. **"把具体 bug 锁进回归让它回不来"**：#521 的漏锚（`LRM-126。LRM-126` 相邻重复被 RE2 吞标点）——parser 结构修复 + N>2/相邻/UTF-16 span 回归，而不是在文档里写"要全部锚定"。

---

## 0.1 产品表面：Members Directory — `仅文档`

- **口径（2026-08-11）**：Workspace 主花名册是 **Members Directory**（侧栏/路由 `members`），不是独立 Agents 页；Settings「成员」Tab 删除，人的邀请/改角色/移除只在 Directory。Agent 仍是执行域实体，仅作为目录中的一类条目出现。
- **指针**：决策表 `docs/members-directory-decisions.md`；ADR `docs/adr/0013-members-directory-replaces-agents-page.md`；术语 `CONTEXT.md` → Members Directory。
- **欠债**：实现落地后把 product docs（`members-roles` 等）与 path helpers 改到与上表一致；旧 `/api/agents` 别名退场条件另立。

### 0.2 Agent start dispatch 双身份协议 — `仅文档`（待可执行化）

- **口径（2026-08-12）**：`launchId` 是 Agent 运行生命周期身份，`startDispatchId` 是一次 `agent:start` 命令的幂等身份；两者由服务端独立生成并持久化，协议与 APM 接口均必填，禁止互相 fallback。
- **ACK 语义**：`agent:start:ack` 只证明 Computer 的 APM 已接受或排队，不证明进程、Provider、session 或消息消费；重连重投必须复用原 dispatch，重复 dispatch 只复用原 ACK。
- **切换顺序**：Runtime replacement 必须 `stop old → matching inactive → start new`，不得同批 stop/start；setup、reconnect、Computer restart、Agent restart 与 Runtime update 统一经过一个 desired-state reconciliation module。
- **命名**：领域统一 `computer_id`；既有 `daemon_id` 仅可作为旧存储 adapter 细节，不得进入新增协议、schema、日志或生命周期 module interface。
- **指针**：完整协议、禁止项、已验证范围与升级为 `可执行` 所需回归见 `docs/agent-start-dispatch-contract.md`。
- **当前状态**：实现与迁移尚未完成，不能标 `可执行`；完成后以协议类型、数据库约束、APM/reconcile tests 和日志断言升级本条。

### 0.3 Agent Message ACK 重投协议 — `可执行`（①②⑤，owner: @Codex）

- **口径（2026-08-13）**：普通消息只走 `agent:deliver → Computer 仍负责任 → agent:deliver:ack`。负责任包括 Pending、已覆盖、start/queued、terminal、idle snapshot 自拉起、spawn cooldown；**禁止**仅因 APM `Snapshot` 为 false 就 NACK。Server 持久化未 ACK responsibility，Workspace Runner ready 后按原始 `seq` 重投。ACK 不是 read、进程活着、Provider turn completion 或 Context Boundary advancement。
- **双层安全**：Server `acked_at` 负责跨重连/重启“不丢”；Computer 进程内的 `target + seq` ledger 负责 Provider 去重与真实 context coverage。daemon 重启后 ledger 为空并由 Server 重投重建，允许保守重放。`deliveryId` 与 `seq` 禁止合并或互相替代。
- **不存在**：普通消息的 `agent:recovery:request/page` 协议、payload、handler 与 coordinator 状态机已删除；Reminder snapshot 不受影响。
- **命名**：新增接口和日志使用 Computer；既有 `daemon_id` 只视为旧存储/auth adapter。拒绝码按格子拆：`rejected_no_process` / `rejected_no_inbox` / `rejected_inbox_runtime_mismatch` / `idle_restore_failed` / `provider_rejected`，禁止收回 `has not been accepted by APM`。
- **物**：migration `340_agent_message_delivery_ack`；`docs/agent-message-delivery-contract.md` 的 1.0.16 accept table；`requiredDeliveryRouteTests`；`TestAcceptMessageDeliveryForbidsUnmanagedEarlyNack`；`make test-agent-delivery-route`。改 `acceptMessageDelivery` 必跑该 target。

### 0.4 Daemon App Storage 身份路径 — `可执行`

- **口径（2026-08-20）**：本地 App state 的身份层级固定为 `MachineID → WorkspaceID → AppID → owner`。Multica `WorkspaceID` 在此模型中等价于 Raft `serverId`；禁止新增一层 `ServerScope` / `ServerID`，也禁止拿 `DaemonID` 代替。Agent-owned canonical path 为 `<BindingsRoot>/app-storage/v1/<MachineID>/<WorkspaceID>/<AppID>/agents/<AgentID>/state.json`；Computer-owned state 为同 scope 下的 `<AppID>/computer/state.json`。
- **边界**：路径从 machine-wide `BindingsRoot` 起算；`BindingStateRoot` 已包含 workspace binding scope，不得再用于拼该 canonical path，否则会重复嵌套 `WorkspaceID`。Reminder pending-fire receipts 的 `AppID` 是 `system.reminder`；aggregate Agent App Inbox 的 `AppID` 是 `system.agent-inbox`，两者是独立 state file，禁止混成同一 envelope。
- **依据**：Raft published bundle 的 authenticated machine context 使用 `machineId + serverId`，再按 `appId + agentId/computer` 打开 scoped storage；Multica 已拍板以 `WorkspaceID` 承担该 `serverId` 语义。
- **物**：`builtInAppStorageAgentsRoot` 是唯一 path builder；Reminder receipts 与 Agent App Inbox 均按 Agent 写入 `state.json`；`TestBindingChildrenUseIsolatedDurableExecutionState` 固定 machine/workspace/App 隔离。不读取旧路径，不提供迁移或兼容 fallback。

## 1. 消息写入管道（BE）

### 1.1 destination-first 统一 finalizer — `可执行`（已落地）
- **契约**：一切 agent 输出（普通 ChatDone、targeted send、thread、DM、transport send/send_draft、Radar/Wendy 内部写手）**必须经 `finalizeAgentChannelMessage` / `finalizedAgentTransportInsertInput` 再落库**；禁止直接 `insertChannelMessageWithParts*`。
- **顺序**：先解析唯一最终 destination → 按**目标频道**成员/可见性解析 mention/issue-ref → 落库/通知/wake。来源频道只用于权限/上下文。
- **caller 传来的 reference span 一律不可信**——finalizer 只保留按目标重验证的锚。
- **物**：#613（chat-done）/ #624（Radar/Wendy 三路）+ 结构约束测试（防新写手绕管道）+ destination 矩阵回归（默认群/同群 thread/跨群/跨群 thread/DM/无效 target/成员只在 B/同名歧义）。
- **已知例外（欠债）**：`service/quick_create_return.go` 仍直插且写 legacy `mention://`（task #510，#463 前置）。
- **身份边界**：handoff 内部 actor identity（`member.id`）≠ 频道 human mention identity（`user.id`）；回归必须显式构造两者不等（#624 教训）。

### 1.2 所有可见 occurrence 都要锚 — `可执行`（owner: @Barry ✅ 已签）
- 同一 actor/issue 在正文出现 N 次 → N 个独立、server-verified span 锚；解析器不许吞边界字符（#521：regex 只匹配 identifier、边界独立校验）。这不是展示层兜底的职责：没有 part 的 occurrence 就是写入契约破损。
- **#521 已见失败与修复**：真实消息 `a9d90909` 的第三个 `LRM-126` 紧邻前一个引用（`LRM-126。LRM-126`）而未锚定；旧 RE2 模式把 `。` 一并消费，非重叠匹配无法把它再用作下一个引用的左边界。#637 改为 identifier-only match + 独立 boundary 校验，保留精确 UTF-16 span。
- **物**：#624 mention 契约回归；#637（已合并）`TestFindBareIssueIdentifiersFindsAdjacentRepeatedOccurrences`（N>2、相邻）与 `TestChannelBareIssueReferencesBecomeStructuredMessageParts`（真实写入链、顺序和精确 UTF-16 span）；FE `data-ref-source` 断言（§2.4）。

### 1.2.1 Conversation handle 语法（`#channel:shortId`）— `可执行`（⑤）
- **口径（2026-08-13）**：CLI / Activity / 频道正文共用一套 handle 语法，对齐 Raft：`#channel`、`#channel:<6–8 hex>`、`dm:@peer`、`dm:@peer:<6–8 hex>`。hex 是 message UUID 去掉连字符后的前缀（Raft `shortMessageId = id.slice(0, 8)`）。禁止从 raw short id 猜 href。
- **写路径**：`#channel:shortId` 必须先于裸 `#name` 占住完整 span，并在前缀唯一时把 `message_id` / `thread_id` 写进 `channel-ref.params`；前缀未知或不唯一则退回 `#name` chip。
- **读路径**：Activity / 任意前端深链走 `GET /api/conversations/lookup`（成员可见才返回 href）。频道 chip 只消费已验证的 part params。
- **物**：`packages/core/conversations/handle.ts` 与 `server/pkg/conversationhandle`；`TestChannelBareConversationHandleBecomesStructuredMessagePart`；`ConversationHandleLookupSchema` 畸形响应 fail-closed。

### 1.3 写读不拆部署 / reader-first 删除 — `仅文档`（门禁尚未落地）
- 格式迁移三端（写边界/读渲染/输入端）同批上；删字段先停读后删写（#622→#507 顺序），**永不反向**。
- 迁移 forward-only、不留兼容层（precedent：migration 178/179）；**兼容的是路径，不是外观**（§2.3）。
- **候选物**：PR 模板"生效层：server / daemon / both"检查项（task #320，待落）。在模板/CI 真能拦截且见过它红之前，不能把“流程门禁”当作已经存在。

### 1.4 Agent 加群引导是 membership generation 协议 — `可执行`（⑤，owner: @Barry ✅ 已签；task #651）
- **唯一事实边界**：未来每次 active group channel 的 agent membership generation 都在同一 DB 事务里创建 membership、结构化 `channel_member_added` system row 与 durable onboarding record；system general 使用 system invariant actor/source，普通手动加群保留真实 actor/source。DM、archived channel、human membership 不创建 onboarding；迁移既有 roster 不回填、不唤醒。
- **先让人看见，再让 agent 决策**：system row 必须先以其 message UUID 作为稳定 realtime event id 发布成功，publication fence 才允许生成 target-only onboarding inbox lease。system row 本身不唤醒 agent；除新加入的目标 agent 外，其他 agent inbox 必须为 0。在线路径的可见顺序固定为 `joined system event → Agent intro`。
- **generation 是隔离单位**：remove 立即令旧 generation 过期，re-add 创建新 generation；drain、send、complete 三处都重验 membership + generation。失效 generation 只能 terminal `expired`，可见 agent message 为 0。
- **回合终态**：active generation 的正常 provider turn 完成即 terminal `completed`，无论 agent 通过普通 Credential Proxy 发出至多一条消息，还是选择静默完成；不得要求 agent 回显特殊 receipt，也不得把 event-derived client id 暴露给 agent。消息幂等只走普通 transport 的 `client_message_id`。remove/archive 后的失效 generation 仍只能 terminal `expired`；真正的 provider/transport failure 继续按普通失败语义处理，不能靠 409 lease retry 制造重复 onboarding turn。
- **物**：migration `207_channel_agent_onboarding` 的 generation/trigger/check constraints与 `328_channel_onboarding_turn_completion` 的 `completed` 终态；`server/internal/handler/channel_onboarding.go` 的 publication fence、target-only materialization、generation revalidation 与 terminal transaction；`channel_onboarding_publisher.go` 的 crash-replay publisher；`channel_onboarding_test.go`、agent transport/daemon/realtime listener regressions。
- **已见红**：测试环境 alpha.7 daemon 的普通消息使用随机 `client_message_id`，server 随后以“requires an explicit send or typed skip receipt”返回 409，使同一 onboarding event 反复 lease，最终出现 Aria 3 次、Orion 5 次重复自我介绍；receipt-free completion 回归在旧实现上也稳定返回 409。此前首轮 crash retry 回归还暴露 duplicate transport audit（2，修成复用原 audit 后为 1）；publication gate 在 system row 尚未发布时拒绝 lease；旧 handler tests 因把 channel 当成“只有业务消息”而被新增 canonical membership row 打红，消费者改为按目标消息/语义断言；draft freshness 回归也证明 joined row 会进入真实 recent context，不能继续沿用旧消息数量假设。

### 1.5 Standalone chat 与 channel transport 不得混路由 — `可执行`（⑤，owner: @AIhpJ ✅ 已签）
- **判据**：`chat_session` 有 `channel_agent_session` / claim `channel_id` → 可见回复只走 task-scoped `multica message send|react`；没有 `channel_id` → final assistant output 自动写回当前 standalone session。DM 页面上的 isolated agent bubble 仍属于后者，页面位置不构成 DM transport target。
- **禁止**：standalone reply 不得枚举 workspace members、猜 DM handle、找 channel 或试 transport token；需要贴纸时以结构化 final sticker envelope 落 `parts[]`。只有用户明确要求联系另一个 destination，才允许显式 target 的主动发送。
- **为什么**：#1167 为隔离 group wake 刻意不建 `channel_agent_session`；若 runtime brief 仍统一宣称“final output 不可见，只能 CLI send”，简单 greeting 会进入无解 target 探测循环。
- **物**：`buildChatPrompt` + `renderChatRuntimeBrief` 按 `channel_id` 分流；Raft idle wake 对 `chat:` 走独立 `formatStandaloneChatTurnPrompt`（聊天回合，不是 Canonical Message JSON / CLI `--target`）；channel/DM 仍走 `formatChannelResidentMessageBatch`；混批拒绝；失败写回 `writebackStandaloneChatTurn`；Notes FAB redelivery 重建 `buildNoteChatWakePrefix`；`chat:` 投递仅在 `provider_accepted`（或 boundary 已消费的 dedupe）时 ACK——禁止 `pending_buffered` 写 `agent_chat_delivery.acked_at`；`multica-notes-assistant` / template 禁止 `message send --target chat:`；`TestFormatResidentMessageBatchStandaloneChatUsesAutomaticDelivery`、`TestWorkspaceRunnerStandaloneChatDefersAckWhileStarting`、`TestStandaloneAssistantFailureReplySurfacesProviderError` 与 sticker 双路径回归。
- **已见红**：修复前 standalone prompt/runtime brief 均缺自动回传合同且继续注入 CLI-only 规则；Notes FAB 再犯：idle-batch 一律注入 channel Message 仪式 → agent 对 `chat:` 发 send → 400；provider timeout 无 assistant 行 → UI 一直 pending；redelivery 丢 `<note_chat_context>`；`pending_buffered` 仍 ACK → Computer 卡在 Starting / desired_running_mismatch 时 ledger 已 acked、气泡永久排队。

### 1.5.1 产品表面只认 `channel_id`；禁止新增 `chat_session` 硬门 — `可执行`（⑤；owner: @阿泰；LRM-1079 / LRM-1080 / LRM-1081）
- **产品口径**（Frank #LRM2.0）：业务只记 `channel_id`（钉句再加 `message_id`）。`chat_session_id` 是底层消息流内部号，**不是** Agent/提醒/发言 API 的必填业务概念。Agent 自己的 runtime/agent session（恢复上下文）是第三条线，与本条无关。
- **冻结**：新代码路径不得再以「缺 `chat_session_id` → 403/拒」作为频道级发言、inbox drain、ambient gate、completion 归一化的硬条件；已有 `channel_id`（+ 成员表面权限）时必须可纯 channel 运行。#1909 / LRM-1055 已开先例。
- **P2 已迁**：ordinary mention / DM / ambient enqueue 为 channel-only（`agent_inbox_event.context` 存 `channel_wake` prompt）；claim 与 complete 不依赖 `chat_message` prompt。可见回复仍只走 task-scoped transport。`channel_agent_session` / `chat_session` **仅**留给 env-dispatch / onboarding / standalone Agent Chat 等遗留桥。
- **P3 废弃（未 DROP）**：表与列标记 deprecated；env-dispatch / onboarding / standalone chat 仍读它们时禁止 hard-drop。完整 DROP 另开单，须先迁完遗留读者。
- **物**：`chatOutputOriginForTask` channel 回退；`isChannelAgentTask` / ambient gate stats / `channelInitiatorForTask`（LRM-1080）；`enqueueChannelAgentPrompt*` / ambient channel-only（LRM-1081）；`lrm_1080_channel_session_fallback_test.go`；`lrm_1081_channel_only_wake_test.go`。
- **已见红**：缺 session 的 ambient/GM 运输 403（#1909）；channel-only wake 被 `isChannelAgentTask` 误判为非频道任务、ambient gate 漏计仅带 `channel_id` 的 priority-1 行。

### 1.6 Agent-to-agent DM 是受 owner 监督的有界协议 — `可执行`（①持久状态 + ⑤并发/权限回归；owner: @Barry ✅ 已签）
- `dm:@<handle>` 在 workspace 内同时解析 active human 与 active agent handle；缺失、跨类型重名、自发给自己都 fail closed。Agent pair DM 固定为两个 agent member，owner 只通过 read-only supervision projection 查看，不成为第三个 channel member，也不能发送、改写、删除或 reaction。
- 每个 matter 默认最多 3 轮（6 条可见消息），严格交替；第 6 条必须先原子持久化再暂停，且不再唤醒对方。pair 另有跨 matter 的 5 分钟 12 条频率门；频率与轮次都在 visible message 同一事务内预留，防并发越界。对方唤醒复用 canonical `agent_inbox_event` directed must-reply 路径，不能另造不受终态约束的执行通道。
- owner 可暂停/恢复单 pair、暂停/恢复其所拥有 agent 的全部 A2A DM、或给指定 exchange 增加轮次；Agent 只有在当前 task 明确由自己的 owner 发起时才能代执行同一 control API，peer wake 不能自增预算。暂停/恢复以结构化 system event 写入 A2A DM；自动暂停另给相关 owner 私有 inbox item，source group 不写公开旁路消息。
- **隐私**：只有任一参与 agent 的 owner 能发现并读取该 DM；普通 workspace member 连会话存在性都不可见。A2A 可见消息和 system row 的 realtime event 仅定向给 agent participants/相关 owner。
- **物**：migration 230 的 `agent_dm_pair_control` / `agent_dm_owner_control` / `agent_dm_exchange` 与 inbox exchange/turn 锚；`agent_dm_a2a.go` 状态机和 owner control；`TestAgentTransportAgentDMThreeRoundBudgetAndMustReplyChain`、`TestAgentDMConcurrentFinalTurnCannotOverrunBudget`、`TestAgentDMFrequencyGateSpansMatters`、`TestAgentDMSupervisionListReadOnlyAndOwnerControls`、handle 歧义与 speech-control 权限回归。
- **已见红**：完整 CLI package 首轮回归抓到 target 帮助仍把 DM 限定为 human handle；owner-control 回归在补齐全局 pause/resume 对称 system row 前无法观察恢复确认；并发终轮与跨 matter 频率测试分别锁住“只成功 1 条最终消息”和“第 12 条即暂停”。

### 1.7 Channel chat 不得再走 legacy dual-write / residual inbox — `仅文档`（欠债至 #2296）
- **现状**：#2295 已硬切 channel dual-write；residual reason（`mention` / `channel_message` / `thread_reply` / `ambient` / `dm`）只 suppress、不执行。普通频道/DM/thread 聊天的唯一交付路径是 **canonical Message → MessageCoordinator → `agent:deliver`**（ADR 0010）。#2596 起 hold 为 Raft one-path、禁止 batch cmid、turn 结束不自动 body-handoff。
- **禁止新代码**：为普通 channel 聊天再 dual-write `agent_inbox_event`；新增 residual reason；让 residual 重新可执行；把 server “seen cursor” 当 Agent 已读真相；再引入 turn 坐标合成的 batch `client_message_id`；hold 后自动重发 draft。
- **仍允许（产品 surface，不是 residual channel dual-write）**：`chat_session`（FAB bubble）、`voice_call`、`issue_thread_backflow`、`collaboration_turn`、`channel_onboarding` 等显式 product reason 继续用 inbox/drain——**#2296 未完成前不要硬删这些表/路由**，也不要把新 channel 功能伪装成这些 reason。
- **完整文档（路径白名单 / 禁区 / 代码指针 / 退出条件）**：`docs/legacy/agent-chat-inbox-path.md`。
- **退出**：#2296 full-delete 落地后，当天改写或删除该 legacy 文，并更新本条。

### 1.8 Cursor ACP 模型选择必须 fail closed — `可执行`（⑤）
- 产品模型 `auto` 必须按 live ACP catalog 中 display name 为 `Auto` 的条目解析成当次真实 `modelId`（当前实测为 `default[]`）；省略 `session/set_model` 只会沿用 Cursor 当前模型，不等于 Auto。
- 显式模型不在 live catalog、ACP 不支持 `session/set_model`、或 Cursor 拒绝该值时，本次 turn 必须在 `session/prompt` 前失败；禁止静默沿用当前模型或降级到其他模型。
- resident Message 在 native acceptance 前失败时也必须发布 `error/runtime_error` Activity，把原因显示给用户，不能只留 daemon warning。
- **物**：`TestCursorACPBackendMapsAutoToLiveACPDefault`、`TestCursorACPBackendFailsWhenConfiguredModelIsMissingFromLiveCatalog`、`TestCursorACPBackendFailsOnInvalidParamsSetModel`、`TestIdleMessageAcceptanceFailurePublishesVisibleErrorActivity`。

## 2. 引用与渲染（FE）

（详细规范与验收清单见 Iris 设计稿；此处为契约要点。）

### 2.1 服务端给 entity，前端不搜文本 — `可执行`（地基）
- 渲染按 `parts[]` 的 span 投影（`projectInlineReferences`），never 正则、never 读 URI；无 part → 纯可读文本降级，**绝不露 `mention://` 等内部 URI**。
- **物**：#600/#601 投影器 + 测试；#520 兜底并线。

### 2.2 正文不可变 / 实体状态一律现查 — `可执行`（已落地）
- `parts[]` = 锚 + 身份（ref_id/label/span），仅此而已；status/assignee/priority/project 一律 `useResolvedIssue(ref_id)` 现查；快照不是渲染源也不是 fallback。
- **物**：#507/#625 字段已删（类型里没有=读不到）；#622 write-then-mutate 回归。

### 2.3 一处概念一个长相；兼容的是路径不是外观 — `可执行`
- 正文（阅读面）：引用=零装饰纯链接+hover 卡；编辑器（操作面）：chip 正确（原子 token 的功能信号）。两语境是两个概念，不许互相"统一"。
- 老数据兜底路径可留，**渲染必须与主路径共用同一组件**（`IssueRefLink`）——结构上没法长出第二副面孔。
- **物（owner: @Felix ✅ 已签，已落 dev）**：①`IssueRefLink` 唯一组件（档位③；4 单测见红：no-chip/带卡/provenance/死链保护）②`IssueChip` 阅读面禁入=files-scoped `no-restricted-imports`（deny-by-default，档位④）+ **barrel re-export 已删（档位①②，编译错误）**；入口已穷举：唯一受控入口 `./issue-chip`、编辑器为显式例外。**已知边界（非洞）**：lint 仅覆盖 `packages/views`；`apps/*` 直接深 import 不在覆盖内（barrel 入口则全仓不存在）。拆迁日：#521 落地后兜底与 `data-ref-source` 按 #463/#510 整体删。

### 2.4 「猜」与「认」必须可分辨（对用户不撒谎；对自己必须说实话）— `可执行`
- 外观并线后漏锚会隐形 → token 带 `data-ref-source="anchor"|"fallback"`（用户不可见、测试可见）。
- jsdom 断言：N 次出现全 `anchor`（fallback 出现=漏锚 FAIL）——**仅对 #637 部署后写入的消息成立；历史消息的 fallback=正常降级不判 FAIL**（#637 不回填历史）；代码块/行内 code/URL 内不升级；兜底解析失败→纯文本（fallback+失败=正则误报，同一属性抓两类错）。
- **物（owner: @Felix ✅ 已签）**：#520 测试套件；`data-ref-source` **是脚手架不是家具**——兜底路径死掉时一起删。

### 2.5 「别让界面撒谎」家族 — 分项标档，不能整族冒充 `可执行`
**宁可不显示，也不显示看起来对的假东西。** 变体：不假渲 / 不露 URI / 不弹空卡 / 不拿旧快照兜底 / 不显示"看着完整其实残缺"的列表 / 不按别处开关瞒本处事实（§3.2）/ 未知枚举值白名单降级（#632）。其中 #635 已用 `TestListGroupedIssuesProjectPaginatesPerGroup` 锁住服务端 status 跨页顺序；**“客户端不得对 server-paginated 响应重排/重分组”仍只是 `仅文档`，直到 consumer/query 回归能让该行为见红。** 详细分档见 `docs/issue-display-contract.md` §5.3。

**任何显示消息文本的面必须走 parts[] 投影；`?? content` 兜底签名=泄漏指纹**（#530/#648）：凡"投影失败退回裸 content"处都是内部 ID 泄漏点——修法=grep 指纹逐点接投影。**精确指纹（Felix 校准）= `formatMessagePartsPreview`/`formatMessagePartsCopyText` 的全部调用点**（`?? content` 只命中一处）；全仓恰四处：列表预览/quote/compact body/复制——inbox/搜索/系统行/推送不走此链。**搜源头（会返 null 的投影函数的调用点），不搜出口（各式兜底写法会漏）。**
**"以后照这个查"的方法必须先证明能重现你这次的发现**（Iris 案：实际用函数名 grep 找到四处，写进规矩的却是 `?? content`——照写下的执行=命中一个已修的、得出"全清"假结论）。**重现不了=你写的不是你做的。**
  > **装置：见过它红，才知道它会红。方法：能重现你的发现，才知道它是你的方法。**
  三位一体（"看起来在工作"是最贵的假象——装置是"绿着但没测"，方法是"搜着但没找"，**问题是"答对了却问错了"**）：
  > **第三条（Iris）：你的问题本身是不是问的那个洞。** 一个跑得完美、答得正确、但问错的验证，照样什么都没测出来（实例：查"字符类有没有混入 ASCII"——证伪、离开；真洞是"少了一整类"）。
  且后者能逮住前者盖不住的东西：**一个方法可以从头到尾没机会红**。方法选错是能力问题，下次学会就好；**"写下来的不是做过的"是转录问题——与做得多好无关，产出的规矩会在别人手里安静地失败**。**复制=把我看到的拿走**：剪贴板必须与屏幕一致（`@小雅` 非 `@actor_14`），round-trip 归 mention picker 不归贴文本。

**降级路径不许改身份**（#451 裁定）：名字解析成功=我们知道是谁 → 头像拿不到图只准退"该人首字母/色块"，永不退"通用 bot"（那是错误陈述不是占位）。**重试解决"有时加载不到"，不解决"加载不到时撒谎"——只做前者=概率变低的谎话。** 正常路径的真话人人在管；**坏掉的路径最容易撒谎，因为没人看**。

**档位可以是借来的，要写明借自谁**（#644 实例）：`data-ref-source`/`data-issue-ref` 这类属性约定本身只是档④；它们现在不漂移，唯一原因是 #520 让 `IssueRefLink` 成了正文渲染 issue 链接的独苗（档③）。**依赖链：lint 禁 IssueChip（④）→ 保住独苗（③）→ 属性才完整。** 正文冒出第二个渲染点，属性当场退回④、双卡回来——"新增 issue 链接渲染点必须走 `IssueRefLink`"是硬依赖不是风格建议。
**为什么不能更强（被否的替代案要留档）**：按 href 抑制（任何内部 issue URL 都不弹 URL 卡）看似真③，**但手打的 markdown issue 链接没有 peek 卡，按 href 抑制会让它一个 hover 都不剩**——判据必须是"我自带卡"（组件属性），不是"我指向 issue"（href）。**档位停在哪，有时是语义正确性决定的，不是偷懒。**
**「天花板」和「欠债」要分开标**：能升未升=欠债（照"能上一档就上一档"推进）；**再升会误伤=天花板**（记下被排除的更强方案和它误伤什么）——否则后人照着升档规则改它，正好踩进已排除过的坑。

### 2.6 Runner Activity 完整投影实时写 Query cache，异常恢复才 REST — `可执行`（③⑤，owner: @Codex）
- 服务端持久化后，`agent:activity` 事件会携带完整 `RunnerActivityResponse`（现有 wire field 为 `payload.activity`）；正常事件由 `useRealtimeSync` 的唯一订阅直接写对应 React Query cache，禁止再落入 `agent` 通用前缀失效，也禁止写后立即 invalidate。`useRunnerActivity` 只读 Query cache，不各自订阅 WS。
- 断线恢复由中央 reconnect 对 `runnerActivityKeys.root(wsId)` 做一次前缀失效；TanStack Query 只立即 refetch active queries，未打开页面只标 stale。非法 payload 不覆盖已有 cache。
- **为什么**：旧路径每条事件同时触发 agents、fleet rankings、runner-activity 三路 REST，并按页面/标签数量放大；WS 已携带同一服务端完整投影，正常事件再次 REST 没有新增权威性。
- **物**：`runner-activity-updaters.ts`、`use-realtime-sync.ts` 的单一订阅/reconnect 前缀、`use-runner-activity.test.ts` 与 `use-realtime-sync-ws-instance.test.tsx`。两条回归均已先见红：一条捕获写后 invalidate，另一条捕获缺少专用 handler 导致的 `agent` 前缀失效。

### 2.7 Message 只持有作者身份，外观永远现查 Profile — `可执行`（①②③⑤，owner: @Codex）
- `channel_message` 只持久化 `author_type/author_id` 等通信事实；头像属于 `user.avatar_url` / `agent.avatar_url`，历史 Message 不保存、回填或粘住头像 URL。作者换头像后，全部历史消息随 Identity Profile 当前值更新。
- 服务端 Message/list/reply/thread/search/quote/transport 响应不返回 `author_avatar_url`；对应查询也不得 JOIN Profile 头像。Web/desktop 的 `ChannelMessage` 类型中不存在该字段（①②）；HTTP schema 与 realtime cache seam 会剥离旧服务端输入，乐观消息也不复制登录用户头像。所有消息头像只走共享 `ActorAvatar → useResolvedActorIdentity`（③）。
- **物**：`ChannelMessageResponse`/reply/search/quote snapshot 与 TS 对应类型；`stripLegacyMessageAvatar`；`withoutLegacyMessageAvatar`；`normalizeChannelMessages`；server/HTTP/WS/optimistic/bubble 回归（均已先见红）。

### 2.8 Agent Presence 只有一个服务端真相源 — `可执行`（①②③⑤，owner: @Codex）
- Agent Presence 只有 `online|offline`：服务端由当前 ready Workspace Runner 连接与该实例持有的 active managed launch 共同投影。Runtime heartbeat、Task workload、Runner Activity、Health、provider quota、crash reason 都不能覆盖这个结果；加载或坏响应不造第三态，也不冒充 Online。
- Web 只读一个 Workspace 级 `agentPresenceKeys.workspace(wsId)` Query。`GET /api/agents/presence` 提供全量快照，中央 `agent:presence` handler 只 patch 该 cache；正常事件不 invalidate，WS 重连只做一次 reconcile。大型列表在页面边界读 Map 并传给行/头像，不挂 N 个 Presence Query 或订阅。
- Avatar/Profile/列表/筛选只显示绿色 Online、灰色或空心 Offline；加载时省略。Working/Thinking/Error 属于独立 Activity 槽，不能把头像点染黄或加 pulse。Disconnected 是 Computer 词汇；Stopped/Blocked/Crashed 只进诊断、Timeline 或 recovery，不进紧凑 Agent Presence。
- **物**：服务端 `internal/handler/agent_presence.go`、`daemonws.Hub` current-Runner disconnect fence、`agent_presence_test.go`；Web `agents/agent-presence.ts`、`agent-presence-updaters.ts`、`use-realtime-sync.ts`；`actor-avatar.tsx` 与 `agents/presence-contract.test.ts`。结构守卫会阻止 Runtime/Task/Health 推导、per-avatar Activity 染色和旧 hook 回流。

### 2.9 Messages 左两列不得同色 — `可执行`（⑤）
- **契约**：workspace 导航 = `bg-sidebar`；会话列表 = `bg-background`。LRM-551 lock A（两列同走 sidebar chrome）已否——同色会把导航和会话列表糊成一块。
- **物**：`conversation-sidebar-styles.ts` 的 `CONVERSATION_LIST_PANE_BG` / 行选中 `bg-muted` / hover `bg-accent`；`channels-page-list-bg.test.ts` + `conversation-sidebar-styles.test.ts`。规范源：`docs/design-product-surfaces.md`。

### 2.10 群聊 @ 候选不含发送者本人 — `可执行`（⑤）
- **契约**：群聊 composer 的 @ picker 不列出当前登录用户。@自己不是有效的 notify/wake 目标，排在列表里是噪声。Issue 评论、agent 候选、以及 DM 的 peer-only allowlist 不走这条。
- **物**：`buildGroupMentionAllowedActorIds({ viewerUserId })` 从本地 allowlist 删除 viewer；`ListChannelMentionCandidates` 从 in-channel 页删除 viewer（服务端列表会盖过本地 sync）；`channels-page` fetch 映射再滤一层；`mention-scope.test.ts` + `mention-suggestion.test.tsx` + `channel_mention_candidates_test.go` 锁住。

## 3. 属性显示（跨面）

### 3.1 本家属性语法 — `可执行`（task #518）
- 一个属性=一种语法=`[标记]+[文字]`（`ActorAvatar`/`ProjectIcon`+title/`PriorityIcon`，出处=已运行数月的 `list-row.tsx`）；assignee 必带头像；属性值不套 chip/pill/底色。
- **物**：#518 共享组件（组件不提供"裸文字 assignee"选项）；mobile 已有先例（`attribute-chip/attribute-row`）。

### 3.2 共享语法 ≠ 共享显示策略 — `仅文档`（待 #518 落地升档，Felix 诚实降档）
- 共享组件只接受"一项属性长什么样"，**不接受 `storeProperties` 等策略参数**；"哪些属性显示"归调用方。hover 卡永远"设了就显、没设不占位"。
- **今天靠的是"写对了+注释"，没有物拦下一个人**；#518 的 API 形状（组件签名里没有策略参数）落地才是 `可执行`。同理："server-paginated 响应不得客户端重排"目前也只有 review（BE 侧跨页 fixture 已有 #635，FE 侧禁令无物）。

### 3.3 Agent avatar 是持久身份事实，不是读取时派生值 — `可执行`（①②⑤，owner: @Barry；task #599）
- **契约**：`agent.avatar_url` 与 `user.avatar_url` 一样是唯一显示事实。Agent source 只允许 `assigned|picked|uploaded`；不存在 `legacy/unknown` 第四态。迁移逐字保留全部既有非空 URL 并标 `assigned`，只为 NULL/blank 一次性写入 concrete pool URL。写入只接受可验证的 `avatar_selection`：picker 提交 canonical preset，upload 提交归当前用户、同 workspace、未绑定的 image attachment id；服务端从可信事实原子派生 URL/source/attachment，拒绝 raw `avatar_url`。读路径不得根据 URL 形状或可变 pool 重算 provenance。
- **为什么**：render-time hash 会让 pool 扩容/重排悄悄改掉所有未自选头像，同一个成员身份在不同时间与不同 surface 漂移。`assigned` 表示系统当前持有的基线头像（包括迁移保留值），不是声称迁移重新生成了 URL。
- **物**：migration 203 的 `avatar_url NOT NULL` + 三态 CHECK + insert trigger（①②）；`Agent.avatar_source` API/type；`defaultAgentAvatarPath`/`stableHash` render-time exports 已删除（①）；create/pick/upload/direct-insert/concurrent-update handler tests，以及迁移 ledger 门（总数不变、非空 100%、既有非空 URL changed=0、source 分布）（⑤）。缺值只准走成员首字母占位，不再从 pool 派生第二真相。

### 3.4 荣誉位图的主加载路径是版本化 OSS 目录 — `可执行`（③⑤，owner: @Codex）
- **契约**：80 张用户等级图、30 张 Agent 等级图与荣誉中心背景优先从 `https://cdn.leagent.me/honor-assets/v1` 加载；前端通过 `honor-assets.ts` 生成稳定 URL，不再静态 import 位图。`apps/web/public/honor-assets/v1` 保留逐字节相同的故障回退副本，只有 CDN 请求失败时才改用主站。内联 SVG 徽章不产生独立请求，继续由组件渲染。
- **发布顺序**：资源嵌入后端发布器，部署必须先将每个对象以 `public,max-age=31536000,immutable` 上传并逐字节回读验证，再启动引用这些 URL 的前端。公开 CDN 首尾样本与背景探测失败时停止部署。修改既有 URL 的图片字节必须升级目录版本。
- **为什么**：旧实现把约 3.9 MB 荣誉位图打进 Next.js 静态资源并从主站出口下载；在主站带宽拥塞时，群消息中的等级徽记也与 API/HTML 争用同一出口。
- **物**：`server/internal/honorassets` 的嵌入目录与完整数量/命名/WebP 回归；一次性 `publish_honor_assets` 的 OSS 上传与回读验证；Web 回退副本逐对象字节一致性检查；`selfhost-config.test.sh` 的发布先于迁移/重启门；用户与 Agent 的 CDN URL 和失败回退回归。完整 OSS 边界见 `docs/research/nextjs-static-assets-on-aliyun-oss.md`。

## 4. Provider / 环境（daemon）

### 4.0 Adaptive Channel Goal 是按需状态机，不是所有任务的默认模式 — `可执行`（⑤，owner: @AIhpJ ✅ 已签）
- **启用边界**：问候、问答和一次性小任务不创建 Goal；只有明确的持续、多步骤或多 agent 协作目标，才由有权限的人或 group-manager agent 建立。无 Goal 的 channel claim 保持原行为。
- **每轮重注入**：active Goal 的最新 `version`、目标、验收标准、进度、下一步和 blocker 必须进入每次 claim 的 current-state overlay；paused/completed/cancelled 不注入，不能只靠首次 prompt 让 agent 记忆。
- **权限与完成门槛**：普通 executor 只能 checkpoint；manager 可维护 agent 自建目标，但不能改写 human-authored intent。完成必须同时满足全部 criteria 已确认且至少有一条 evidence；所有写入用 `expected_version` CAS，过期写返回 409。
- **物**：完整产品/API/UI 契约见 [`docs/superpowers/specs/2026-07-31-adaptive-channel-goal-mode.md`](superpowers/specs/2026-07-31-adaptive-channel-goal-mode.md)；数据库约束 `257_adaptive_channel_goal`；handler/runtime/UI/locale parity 回归覆盖创建权限、每轮注入、checkpoint、证据完成门槛和 stale write。

### 4.0.a Goal × Work Graph 权责边界 — `可执行`
- **分层真相源**：Goal 管 objective、success criteria、人工范围和生命周期；Work Graph 管当前执行方案、节点、依赖、角色、预算、调度、产物有效性和核验。Issue/Run Node 是可审计工作单元，Agent Job/Runtime 是执行容器。Goal version 与 graph revision 不得混用。新 graph revision 必须重新准入保留节点并以新 completion contract、依赖和 review 状态调度；Artifact、Verification 和失效传播必须与同一 workspace/graph 内的节点绑定，禁止跨图引用。
- **按需准入**：有 Goal 不等于必须建图。一个上下文能连贯完成的工作保持 `DIRECT`；存在独立交付边界、并行收益、独立权限/等待/核验需求才用 `GRAPH`；明显扩大成本、权限或范围时用 `PROPOSE_GRAPH` 请求确认。
- **完成关系**：Graph 可交付只产生 Goal 完成候选，不得绕过 Goal 的 criteria、Evidence、开放工作和人工/发布 Gate。Goal intent 变化应使受影响节点和旧 PASS 失效，而不是覆盖历史。
- **Subgoal 收敛方向**：现有 `channel_goal_subgoal` 不发展成第二套 DAG；通用图内核落地后，Goal 子目标 UI 应成为 Work Graph Node 投影或被明确迁移，禁止长期双向同步两套执行真相。
- **当前物**：assignment runtime 的 `Work Decomposition Gate` 及回归测试；完整分期见 [`docs/superpowers/plans/2026-08-06-goal-work-graph-runtime.md`](superpowers/plans/2026-08-06-goal-work-graph-runtime.md)。原子建图、ready scheduler、Graph Delta、Artifact/verification 和失效传播尚未实现，不得由 Prompt 冒充。

### 4.0.b 独立工作默认并行，拆分决策必须早于实现派发 — `可执行`（⑤，owner: @Codex）

- **准入时机**：活跃 multi-Agent Goal 的 manager 在 chat 控制面创建或派发实现 Issue 前，必须先识别可独立验收的工作单元；不能等一个大 Issue 已经唤醒执行者后才第一次询问是否拆分。
- **默认并行**：两个及以上无需互等、可独立验收的调研、来源/数据采集、实现、测试或 review，默认成为同一真实父 Issue 下的并行 Issue DAG roots。只把真实前置依赖写成 `depends_on`；不得把独立 root 放进 backlog，也不得创建多个顶层 Issue 后仅在描述里称其中一个为“父卡”。
- **授权边界**：在当前 Goal/Issue 已授权 scope、permissions 和 committed budget 内做并行拆分，不额外请求确认；只有拆分会实质扩大任一边界时才提案。小任务、紧耦合交付或拆分成本高于 wall-clock 收益时保持 DIRECT。
- **物**：`channelGoalStateSlot` 的 manager-only `Parallel admission`（新增热路径文案有 600-byte 上限）；assignment prompt/runtime 的同义 `Work Decomposition Gate`；`multica issue decompose` 的原子父子/依赖写入。回归为 `TestBuildPromptChannelManagerDefaultsIndependentGoalWorkToIssueDAG`、`TestBuildPromptWithoutChannelGoalKeepsOrdinaryChatUnchanged` 和 `TestAssignmentBriefIncludesWorkDecompositionGate`。

### 4.0 服务环境决定连接目标与 Computer 包源 — `可执行`（⑤，owner: @Barry；Web origin 门：`scripts/assert-baked-web-public-origins.sh` + `deploy-test.yml`）
- **服务环境**：`production` 是 leagent.me 正式服务，browser/API 的 canonical origin 分别是 `https://www.leagent.me` 与 `https://api.leagent.me`；`test` 是腾讯云测试服务，首版以 `https://82.157.184.89` 同时承担 app/API，之后可只改部署配置切到 `https://test.leagent.me`。服务端用 `APP_ENV=production|test` 声明身份，并通过公共 `/api/config.environment` 明确告诉页面，禁止根据域名或 IP 猜环境。旧服务缺字段或字段非法时，前端保守降级为 production。
- **Web 公开 origin 是构建期常量**：`NEXT_PUBLIC_API_URL` / `NEXT_PUBLIC_APP_URL` / `NEXT_PUBLIC_ENVIRONMENT` 烤进浏览器 JS，容器启动后再改环境变量无效。test 镜像必须烤 `https://82.157.184.89`（或当时的 test origin），不得出现 `api.leagent.me` / `www.leagent.me`。`/health` 探活发现不了这件事。门禁是 `scripts/assert-baked-web-public-origins.sh`，由 `deploy-test.yml` 在 s89 验收登录页 layout chunk；`scripts/assert-baked-web-public-origins.test.sh` 先见红再生产包。test web 构建禁用 Buildx cache，Dockerfile 设 `TURBO_FORCE=1`，避免复用生产烤过的 `.next`。
- **Computer 本机模型**：同一 OS 用户只有一个 Computer root/identity/resident；每个环境分别保存登录和 Workspace connection，本地键为 `(environment, workspace_id)`。两边连接可以同时保留，但单个 resident process identity 同一时刻只服务当前环境；切换必须 drain、重启、验收，不能并发连接 production/test。
- **Computer 生命周期所有权**：升级、重启和其他整机 mutation 只由 Computer owner 发起；Workspace owner/admin 只管理自己的 Workspace，不因页面中可见一台 Computer 就取得别人电脑的控制权。Workspace 页面只是发起入口和状态投影；一次 Computer 升级对该环境下全部 active Workspace connections 生效，所有连接必须看到同一 operation、版本和结果，禁止按发起 Workspace 分叉升级 lineage。
- **Computer 升级只有一个 CLI 入口**：`multica computer upgrade` 先探测本机 resident；live owner 可控时只通过 owner-only Unix socket / Windows named pipe 提交 canonical Machine Upgrade，且仅 Computer service 执行 sibling drain、stage、verify、swap、journal 与 service restart。只有确认没有 resident owner 时才允许在 machine mutation lock 下离线下载、校验并提交 Active；resident ownership 存在但 IPC 不可达时必须返回 `upgrade_service_unreachable`，不能离线绕过。
- **Computer 就是 PATH 那一份**：`$HOME/.local/bin/multica` 既是 CLI 也是 Computer。`computer upgrade` 把它换成新文件并留下 `.prev`；start / supervise 跑的也是这份，没有 VersionStore 子进程。
- **Computer restart 与升级恢复共享单 owner 栅栏**：`computer restart` 只有在旧 resident 的 local control endpoint 已证明释放后才能启动 successor；仅成功发送 shutdown 不构成 stop proof。Machine Upgrade 由独立 `computer __upgrade` coordinator 拥有 successor spawn 和后续生命周期：incumbent 写 journal 后退出，coordinator 等 `sourceServicePid` 和全部 `oldRunnerPids` 死亡后再起 target，并继续 wait 该 child。journal 只在外部 attestation 之后由 coordinator 删除。公开 start 看见 pending handoff 必须拒绝。target 失败进入 `rolling_back` 再用 `fromVersion` 恢复。不存在 cloud generation CAS、receipt 或双写状态机。
- **Computer CLI 单测不得触碰机器级 resident**：测试必须使用临时 service endpoint 或显式 loopback HTTP test adapter；production Computer control 不绑定 TCP 端口。
- **包源随环境固定**：production 只用 `metainfo.json.environments.production` 的稳定版本；test 只用 `metainfo.json.environments.test` 的预发布版本。没有独立 `release_channel` 让用户制造“test 连稳定包”或“production 连预发布包”的组合。带版本 archive/checksum/manifest 不可变；不发布根目录 channel JSON，也不做隐式 fallback。
- **页面引导**：Computer 页面用 `/api/config.environment` 决定命令类型，用 `daemon_server_url` 和 `daemon_app_url` 分别填 test API/Web origin。production 显示 `multica setup /<workspace>`；test 显示 `multica setup --environment test --server-url <api-origin> --app-url <app-origin> /<workspace>`。两个值当前可以相同，但协议不强制同源。页面不能读取本机 `~/.multica`，所以首次连接必须把目标写进命令；完成后本机配置保存 active environment。连接卡只保留平台选择、安装命令、setup 命令和等待状态；一行说明 setup 会激活目标环境、连接 Workspace 并启动 Computer。若 setup 将切换已有 active environment，CLI 必须在任何写入或登录前询问，除非自动化显式传 `--yes`。
- **部署拓扑**：workflow 结构固定为 `dev → GitHub Environment test → Tencent s89`、`main → legacy-named GitHub Environment aliyun-dev → Aliyun/leagent.me production`。`aliyun-dev` 只因现有 secrets 无法导出而保留旧名字，不代表 dev。部署验收仍必须分别证明 workflow、目标执行通道、served origin、镜像 SHA 与数据库迁移；workflow 合并本身不等于已上线。
- **客户端产物**：CLI/Computer 使用同一份签名二进制，环境在运行时选择，不为 test/production 各编一份。Desktop 不在 Computer v1 交付范围。所有 tag 串行更新同一份 `metainfo.json` 中对应的 environment。Test 页面在浏览器运行时直接读取有 CORS 的 canonical `metainfo.json`，校验后显示 `environments.test.tag` 的精确版本；应用构建和 CD 不得联网读取可变包源，也不能通过 `NEXT_PUBLIC_*` 固定 Computer 版本。

### 4.0.b Computer 删除只有一个产品语义 — `可执行`（②③⑤，Computer v1）
- **唯一入口**：产品只暴露 `Delete Computer`，canonical API 是 `DELETE /api/computers/{computerId}`。已安装客户端使用的 `DELETE /api/runtimes/by-daemon/{computerId}` 只能是同一 handler 的兼容别名；旧 `runtime_mode` query 不得缩小 Computer 删除范围。禁止再暴露独立 Workspace connection revoke 或 Computer 级批量删除 Agent 的旁路。
- **前置条件**：只要当前工作区内该 Computer 仍绑定 active Agent，就以 `409 computer_has_active_agents` 整体拒绝，runtime、connection、credential 和本机数据都不变。用户必须通过正常 Agent 删除流程清空后重试；Computer 删除不替用户级联删除 Agent。
- **事务语义**：成功删除会在一个事务内清除当前工作区的 runtime 投影和已归档 Agent 服务端数据、撤销当前 Workspace connection/credential，并写 registration tombstone；零 runtime 的 binding-only Computer 也能删除。其他工作区和全部本机文件均不在操作范围内。

### 4.0.c Computer → runtime 的层级，以及「显示」与「选择」分属两个接口 — `可执行`（⑤，owner: @Frank 定裁 2026-08-21）
- **层级先钉死**：Computer（一台机器 / 一个 daemon core）→ 它托管的若干 runtime（一个 provider 一个）。**`visibility`（private/public）只存在于 runtime 级**（migration 083 在 `agent_runtime` 上）；**Computer 没有可见性**——`GET /api/computers` 按 workspace membership 返回全部 active binding 的机器，这是设计不是遗漏。**owner 则相反，是 Computer 级**（LRM-1570 / migration 429 删掉 `agent_runtime.owner_id`）。说「private 的电脑」是把两条不同的授权线混成一条。
- **liveness 是 Computer 级的事实**：由 daemon 的 Workspace Runner WS socket 决定（`computerConnectedByRunner` → `DaemonHub.HasWorkspaceRunner`），HTTP 心跳只是 legacy fallback。**不得再用 runtime 的 `status` / `last_seen_at` 推在线**——那条路径随 runtime 全面走 WS 已经过时。
- **前端不做跨列表拼装**（同 §2.1「服务端给 entity」）：agent 的运行配置由 `GET /api/agents/{id}/runtime-config` 一次给全——computer（名字已解析好 + connected + cli_version）、runtime（provider）、model、thinking。曾经的做法是拿 `agent.runtime_id` 去 `GET /api/runtimes` 里 `.find()`，而那个列表按 runtime visibility 过滤，于是别人机器上 private runtime 的 agent 对所有其他人都显示「无电脑」——连被允许在那台机器上建 agent 的 admin 也一样。
- **「显示」与「选择」是两个问题、两个接口**：显示走 `runtime-config`（能看到）；选择走 `GET /api/computers` 携带的 `runtimes[]`（选不到）——按层级先选机器再选 provider，且**该数组已由服务端按 visibility 过滤**，客户端不得再自己判定可绑定性（`isRuntimeUsableForUser` 只在老服务器不返回该字段时兜底，它读的 `owner_id` 是机器 owner 投影到 runtime 行上的值，不能作为长期规则）。
- **`GET /api/runtimes` 的语义不得为显示需求放宽**：它只回答「我能管哪些电脑」，是 computers 管理页的数据源。
- **不得把这类状态反规范化到 agent 响应上**：在线状态的刷新链路挂在 `runtimeKeys` 上（`daemon:*` / `computer:*` WS 事件 invalidate 它），agent 响应走 `workspaceKeys.agents`。挂到 agent 上就脱离这条链路，绿点会停在上次拉取的值——**滞后的「已连接」比不显示更糟**。`runtimeKeys.agentConfig` 挂在 `runtimeKeys.all(wsId)` 之下正是为此。

### 4.0.a 当前 Aliyun deployment ownership — `可执行`（③单一部署链 + ⑤ workflow/host gate；owner: @Barry）
- **当前事实**：`dev` 经 test Environment 部署到腾讯 s89；`main` 经 legacy-named `aliyun-dev` Environment 部署到 Aliyun `101.200.210.144` production。公开 Web/API origin 由部署环境变量提供，production 默认是 `https://www.leagent.me` / `https://api.leagent.me`；`:8090` 只保留 Computer 兼容与部署探针。不要把 Environment 的旧名字 `aliyun-dev` 误报为 dev 服务。
- **构建与部署分界**：镜像和最小 deploy bundle 在 GitHub-hosted runner 生成；production deploy job 下载同一 `github.sha` 的 immutable artifact，经 pinned host key 的 SSH 复制到 Aliyun 临时目录，再在主机本地拉固定 SHA 镜像并执行 Compose/Caddy/readyz。deploy job 禁止 `actions/checkout`/git fetch，避免大陆网络 partial clone 失败，也避免生产机拥有 Actions Git worktree。
- **SSH 与执行 ownership**：SSH 账号和专用私钥只来自受保护 Environment Secrets，账号是 `dev`；remote temp 与 `/data/multica` 均归 `dev`，终局删除 remote temp。root 只用于首次安装公钥，不参与日常发布。禁止把账号或私钥写进 workflow 文件或日志，禁止关闭 host-key 校验，禁止让 production deploy 依赖 Aliyun 到 GitHub Actions task endpoint 的持续出站连接。
- **runtime owner 与 secrets**：`/data/multica` 由 deploy owner `dev` 管理，Caddyfile 从 immutable artifact 先校验，再通过 sibling + atomic rename 安装；deploy script 不 sudo、不拥有 Git worktree。runtime secrets 只在 host-owned `/data/multica/.env`；受保护 GitHub Environment `aliyun-dev` 的必要 ambient secrets 只经 SSH 加密 stdin 的显式 allowlist 转发，不写入远端 dotenv。Compose dotenv 永远只作为数据解析，禁止 `source`/`eval`，不能覆盖 workflow 控制变量。每次 restart 前必须用同一份 host 目标 user/database/password 三元组做真实 PostgreSQL TCP `SELECT 1`，不把“文件有值”或旧容器 identity 当 credential 有效。
- **迁移先于 runtime roll**：Aliyun 必须用目标 backend image 的 one-off container 独立执行 `migrate up`；失败直接以 database migration 失败终止，并发生在 Caddyfile 安装和任何 runtime `compose up` 之前，因此既有服务保持不动。backend entrypoint 的第二次迁移是当前 Compose/Helm 活合同，版本已最新时只做 ledger 空跑，不能当兼容 shim 删除。
- **终局证据**：连续两次 cumulative Deploy（clean + reuse）成功；served backend/frontend SHA 对应 cumulative `dev`；host-local `/readyz` 与外部 `leagent.me` HTTPS 都通过。CI/merge 或公网 `:8090/health` 单独都不能宣称部署完成。
- **事故依据**：2026-07-23 腾讯 s89 checkout 的 6 个 root-owned refs/logs 来自宿主机 root Git 复用 Actions worktree；同日 Aliyun 首次 cutover run `29979928059` 又因 `actions/checkout` 的 GnuTLS/partial-clone promisor fetch 失败；2026-08-20 Aliyun Runner 持续被 GitHub Actions task endpoint TLS reset，生产任务无法取件。三起事故共同证明：生产机不应拥有 Actions Git worktree，也不应让发布可用性依赖自托管 Runner 的持续反向连接。
- **branch source-of-truth 元教训**：workflow 可以只存在于特定 branch。产品代码看 `dev`；“什么部署 main”看 `main`；线上跑什么看部署 SHA/容器。不要把“永远读某一 branch”写成无上下文常量。

### 4.0.1 Schema rollback contract — `仅文档`（人工设计/评审门；无可靠自动 SQL compatibility 判定）
- 每个可能随镜像回滚的 schema 迁移必须保持旧一版应用仍可运行：优先 expand → 双读/双写或 backfill → contract，删除/重命名/收紧约束不得与首次使用新 schema 的应用版本同轮落地。
- 部署回滚只切换 image tag，不自动回滚 schema；因此 migration reviewer 必须逐项确认目标旧 image 对迁移后 schema 的读写兼容性。自动测试可以覆盖具体旧/新二进制组合，但不能把通用 SQL 文本扫描冒充兼容性证明。

### 4.1 子进程环境合同 — `可执行`（task #512，进行中）
- 全局宿主私有变量禁继承（Raft SEA 标记等）+ per-provider 声明允许/禁止清单 + 显式 `custom_env` 最后叠加最优先 + 全 provider 矩阵回归。首刀已落：中央 provider-aware sanitizer（#627，Pi 声明 `PI_PACKAGE_DIR` 宿主私有）。
- provider 可执行路径/版本/模型目录须可审计解析（不依赖 PATH 顺序碰巧正确）——#512 范围，含 restart/PATH-drift 测试。

### 4.2 并发/投递 — `可执行`（已落地）
- 同 agent 聊天 lane 全局一条活 run，服务端 lease 排他（agent 行锁+active-delivery exclusion），daemon per-agent 单槽兜底；lease 永久拒绝→取消旧 executor 丢弃结果。聊天 lane 不读 `max_concurrent_tasks`（那是 issue/task 调度的公开配置，别混）。
- 待执行 wake 按 `priority DESC`，同优先级按 `created_at/id` FIFO；高优先级人类消息可越过尚未开始的低优先级后台 wake，但不能打断活 run，也不能越过更早的同优先级人类消息。
- **物**：#611 + 双 wake/异 agent/失租回归；跨 runtime 优先级与同优先级并发 FIFO 回归。

### 4.3 定向请求先反馈；用户偏好必须“写入 + 取回” — `可执行`（⑤ prompt contract tests；owner: @jianghp3 ✅）
- **先反馈再干活**：人在 DM、群聊 @mention 或直接提问里交代了需要查资料、跑工具、改代码等非即时工作时，agent 必须先发一条简短确认，复述理解与下一步，再开始第一个实质性工具调用；能当场回答的简单问题只答一次，attention-management 操作仍走成功后 `✅` 的专用合同。
- **“收到”不等于记住**：用户明确说出的稳定 profile 事实和协作偏好采用高强度自动写入，必须落到当前 agent 的 `users/<member-id>/USER.md`；后续定向请求在实质工作前先读该文件。`<member-id>` 只能来自当前请求的可信身份元数据，不能由显示名猜测。若用户明确说“所有 agent / 全团队都这样”，当前 agent 还需写带用户归属的 workspace/shared review candidate，交由 governed team curation，禁止把一个人的私有偏好直接散拷给所有人。
- **身份不用 memory 猜**：当前 agent 身份以 `## Agent Identity` 为准，当前说话人以 `## Task Initiator` 的 attested identity 为准；memory 只补充“这个人的偏好”，不能把邻近聊天里的名字当身份 oracle。runtime owner 的 profile 中明确写出的协作偏好是跨 agent standing defaults，但当前任务/更新的现场指令优先。
- **物**：`server/internal/daemon/execenv/runtime_config.go`；`TestChatRuntimeBriefRendersReplyRequirementForDirectedRun`、`TestMemoryOperatingGuidePrioritizesExplicitUserPreferences`、`TestBuildMetaSkillContentEmitsRequestingUser`、`TestBuildMetaSkillContentEmitsTaskInitiatorMember`。已见红：旧 brief 只要求结束前可见回复、memory 为 medium-strength 且 lazy-read、profile preference 被降成 background。

### 4.4 轻量认知档位不能静默退化为完整执行 — `可执行`（② profile enum + ⑤ daemon/Pi contract tests）
- `execution_profile=attention_probe|protocol_turn` 是运行隔离合同，不是 Prompt 风格：必须使用执行后删除且不返回 session ID 的独立临时会话、不可被 custom args 覆盖的空工具 allowlist、无 MCP/Skill/CLI transport、无仓库/附件/完整本地 Memory；不得复用 Agent 的持久 chat runtime。
- 当前只有 Pi 完成 provider 级空工具注册表约束；其他 provider 收到受限 profile 必须 fail closed，直到各自实现并见过隔离测试红。未知 profile 同样 fail closed，不能按 `full` 执行。
- 受限 profile 的成功、失败、超时和 provider/schema 拒绝都必须走 no-public-output：成功回调归一成 `no_reply`，失败回调禁止创建 issue/chat 可见消息，并始终清空 session/workdir 指针；探针结构化结果只能走 Attention Round 的内部结果合同，不能借聊天完成输出旁路发布。
- Attention Probe 只接受无额外字段/尾随文本的严格 JSON 对象；Pi 受限执行在 provider 请求前把模型输出预算限制为 96 Token，运行后再次按 usage fail closed。最近上下文最多 8 条，当前消息、身份和 Memory 都按 UTF-8 字节硬截断，其中私有 Memory/State 摘要总预算 4 KiB。
- `execution_profile` 缺失只兼容历史已入队执行，解释为 `full`；新任务必须在创建时快照明确 profile。
- **物**：`TaskExecutionConfig.ExecutionProfile` 类型/解析；`restrictTaskForExecutionProfile`；Pi `--tools ""` 参数合同；`usesPersistentPiChatRuntime` 受限档位排除；对应 service/daemon/agent tests。

### 4.5 Provider 工具事件先归一成独立事实 — `可执行`（②统一类型 + ③单一 adapter 边界 + ⑤合同/状态机测试；owner: @Barry）
- **契约**：所有用户可见工具 Activity 必须来自 provider 原生事件或已验证 runtime hook，先归一成 `RuntimeToolEvent v1`（event_id/source/protocol shape/session/call_id/phase/tool/input/output/time），再进入既有 daemon Message/Activity 管道；最终回答 prose 永远不是工具事实来源。
- **Raft 真实基线（`raft-computer 1.0.7`）**：内置 runtime 主链也是 provider 原生输出流 → driver `parseLine` → 统一 `tool_call/tool_output` → Activity 投影，不是统一 hook。Cursor driver 直接启动 `cursor-agent --print --output-format stream-json`，当前只认旧 `assistant.message.content[].type=tool_use`，尚不认顶层 `tool_call started/completed`，所以 Raft 值得复用的是统一事实边界与下游投影，不是它的 Cursor shape 覆盖。
- **Provider 边界**：外部协议版本通过显式 shape registry + 真实 raw fixture 支持，不在执行循环里散落字段猜测/fallback。未知/非法 shape 只记结构化 diagnostic（accepted by shape / dropped by reason），不猜成命令、不静默消失。
- **生命周期**：`call_id` 状态机按一次 `Backend.Execute` 隔离，started/completed 不跨 turn 配对；duplicate/out-of-order/tool mismatch 有稳定拒绝原因；TTL + capacity 限制长 turn 内存；turn 结束统计 missing completion。
- **物**：`server/pkg/agent/runtime_tool_event.go`；Cursor `cursorToolEventDecoders`；`TestRuntimeToolEventTracker*`、current/legacy shape contract tests、真实 execute lifecycle fixture。新增 provider/shape 必须先让对应 raw fixture 在旧 decoder 上见红，再加 registry entry。

### 4.6 Durable async hook 是目标权威源，不是已完成能力 — `仅文档`（outbox/cutover 尚未落地）
- **终态（Frank 2026-07-21 拍定）**：runtime hook 先极快写本地 durable outbox 再返回，后台重试上传 `RuntimeToolEvent v1`；event id/call_id 幂等、daemon 崩溃后续传，Activity 失败不阻塞 Agent 主流程。
- **不要把目标态归因给 Raft**：`raft-computer 1.0.7` 的 daemon WebSocket 断线时只在内存 `pendingActivityByAgent` 中按 agent 覆盖保留最后一条 Activity；重连时清空该 map 后 replay，没有磁盘 outbox、逐事件 ACK 或崩溃续传。另有 self-hosted `raft agent bridge` 可从 runtime plugin 的 `/activity/drain` 拉取 `raft-activity-drain.v1` 并转发，但它不是内置 Cursor driver 的采集链，且 daemon 侧是先 drain 再 POST，失败路径没有 requeue/ACK 合同；不能据此宣称 Raft 已有 durable hook。Multica 的 durable hook/outbox 是我们要补强的目标架构。
- **现实边界**：Cursor 已定义通用 `preToolUse/postToolUse/postToolUseFailure`，但 Multica 使用的是 local `cursor-agent --print` 路径；不得拿 IDE/Cloud 的支持面推定某个已装 CLI 版本。每个目标版本必须用 Shell/Read/Write/MCP × success/failure/rejection 探针证明实际覆盖。Cursor 不替 Multica 保证本地持久化/重试/幂等/崩溃恢复；官方 `stream-json` 当前明确提供通用 `tool_call started/completed`，所以在 hook 探针未全绿前它仍是交付权威。
- **切换门**：相关工具覆盖率完整 + 本地落盘/重试 + 幂等去重 + crash resume + shadow 无丢失五门全部通过，才把 hook 切为单一权威；随后删除 native 权威。Shadow 永不写第二份用户事实，不允许永久双真相。
- **待升档的物**：provider-neutral hook source contract、原子 outbox/spool、ACK/checkpoint、重启恢复、source-selection/cutover 配置与双源差异指标。上述代码和见红测试未落前，本节不得改标 `可执行`。

### 4.7 语音供应商只在 server 边界出现 — `可执行`（③单一 transport + ⑤协议/真实往返测试；owner: @Codex）
- 豆包 Speech API Key 只进入 backend 环境和 WebSocket header，不进入前端配置、API 响应、日志、fixture 或 Git。ASR/TTS 失败返回明确错误，不换浏览器语音、旧版资源或其他供应商制造假成功。
- 当前已验证合同固定为 TTS 2.0 `seed-tts-2.0` 和 ASR 2.0 小时版 `volc.seedasr.sauc.duration`；ASR 输入固定为 16 kHz、单声道、signed 16-bit little-endian PCM。切换资源版本或输入格式必须改显式合同和测试，不能在 transport 里试探。
- HTTP 面必须同时经过登录、workspace membership 和 human-actor guard。语音额度不能由 task token、agent credential 或 cloud PAT 消耗。
- **物**：`server/internal/integrations/doubaospeech`；`POST /api/voice/asr`、`POST /api/voice/tts`；协议 frame、header、错误脱敏、handler 输入边界测试；可选 live test 用 TTS 生成 PCM 再送 ASR，已用实际账号见过完整往返成功。

### 4.7.1 消息引用附件资源，不占有附件 — `可执行`（①PostgreSQL 关联事实 + ②parts 类型 + ⑤跨会话/迁移回归；owner: @Barry）
- `attachment` 是 workspace 内一次上传得到的文件资源；`channel_message_attachment` 是消息对该资源的引用。一个合法且归发送者所有的 attachment id 可以同时被群聊、DM、thread 和多条消息引用，文件字节只上传/存储一次。`attachment.channel_id` 只记录上传来源，不是后续复用范围，也不能重新充当 message ownership。
- `parts[]` 决定某条消息展示哪些 attachment；server 在同一发送事务中验证 workspace/uploader 后创建关联行，读模型只从关联表 hydration。禁止用同一 attachment 的旧单值 `channel_message_id`、同频道猜测、re-upload 或“发消息成功但留下 unavailable part”的兜底替代正式多引用路径。
- 历史修复只允许从 canonical message parts 中提取合法 UUID，并关联同 workspace 已存在的 attachment 资源；缺失、已删或非法 id 保持 unavailable，不从正文/文件名猜元数据。migration 必须保留旧单值绑定、补出同资源的全部合法多消息引用，然后删除单值列。
- **物**：migration 224 `channel_message_attachment` + parts backfill；shared owner-authorized link helper；channel/DM/thread readers、voice/Radar/Wendy/quick-create/runtime-cleanup 全部走关联表；人类跨频道复用、Agent 群聊→DM 复用和可回滚数据迁移回归。

### 4.8 消息语音形态由结构化 part 决定 — `可执行`（②协议类型 + ③共享 Composer/播放组件 + ⑤消息链回归；owner: @Codex）
- 人类录音先以空正文 + `{type:"voice", duration_ms, attachment_id, transcription_status:"pending"}` 原子落库，附件是 16 kHz 单声道 PCM WAV。server 在持久队列中完成 ASR 后，把同一消息更新为 transcript text + `transcription_status:"completed"`，再触发 Agent；永久失败标为 `failed`，原声仍可播放。客户端不得在发送前调用 ASR，避免 provider 短暂故障使录音本身无法发送。Agent 语音回复先落库为完整 transcript text + `{type:"voice", synthesis_status:"pending"}`，server 生成一次 24 kHz PCM WAV 并作为同一消息的私有附件持久化，再把 part 更新为 `synthesis_status:"completed"`；重启由持久队列恢复，永久失败标为 `failed`。除 pending 人类录音这一种形状外，`voice` part 没有非空 text part 时服务端拒绝；消息形态不能靠正文关键词猜。
- 人类语音消息要求 Agent 通过现有 `multica message send --voice` 输出；普通文字仍走文字，文字明确要求语音时由 Agent 语义判断后加 `--voice`。前端不维护“语音回复”关键词表。
- 所有人类发言的频道执行提示都必须保留用户请求的交付形式，不得另外注入 `text only` / `plain-text reply` 之类相互冲突的规则。普通文字消息只注入语义意图与能力说明，具体 CLI 语法继续以 runtime brief 为单一权威源；结构化语音输入可以在当轮强化已有的 `--voice` 路径。
- 语音回复依赖支持 `message send --voice` 的运行时版本。服务端与前端已支持语音不代表旧 daemon 自动获得该命令；发布后必须确认目标智能体重新注册的 `cli_version` 已包含语音合同。
- 自动播放资格只来自本机当前会话的一次发送手势，并由第一条新 Agent 回复消费；文字回复也会消费，防止稍后的无关语音突然播放。所有 Agent 语音消息始终提供手动播放/停止/重试。
- Agent 语音回复与新的人类录音默认只显示同一种语音气泡；canonical transcript 仍保留在消息中用于 Agent 上下文、可访问性、复制和 Agent TTS，但正文需由气泡旁的显式“转文字”操作展开。展开区必须与对应气泡同组并有可感知的“语音转写”标识。历史人类语音 part 没有 `attachment_id` 时继续显示不可播放标记，不能用 TTS 冒充用户原声。
- 人类录音在浏览器解码、重采样并上传 WAV 后立即发送；server 只从已经绑定到该消息、频道和工作区的私有附件读取音频并异步 ASR。录音附件沿用频道附件的 membership、写权限、消息绑定、下载和删除合同，不另造公开音频地址。这里保存的是重采样后的真实语音波形，不是浏览器原始 WebM/MP4 容器；实时通话仍需另立媒体传输合同。
- Agent 语音的播放源只能是 server 根据 canonical transcript 生成并绑定到消息的 TTS 附件；每条消息使用固定队列和附件 ID，provider 或进程失败只能重试同一产物，不能让每个浏览器重新合成。旧运行时曾把“文本 + 单个自有音频附件”当语音回复发送；agent transport 将这个精确边界形状补成 `voice` part，但 server 会丢弃运行时提供的音频元数据并重新生成可信 WAV。普通用户音频、多个附件和混合文件消息不参与此规则；当前运行时明确禁止自行合成/上传语音附件。
- **物**：`protocol.MessagePartTypeVoice`、`messageparts.Normalize`、CLI `message send --voice`、共享 `VoiceInputButton`/`VoiceMessageAudio`、channel/DM/thread 发送与渲染回归；语音基础实现见 `docs/superpowers/plans/2026-07-22-beckham-voice-poc.md`，人类原声附件实现见 `docs/superpowers/plans/2026-07-23-human-voice-recording.md`。

### 4.9 实时通话与语音消息使用不同媒体合同 — `仅文档`（实现尚未落地）
- 第一版是贝克汉姆 DM 内的一对一应用内语音通话，不包含群聊会议、PSTN、摄像头或数字人视频。数字人以后只能作为同一通话的展示参与者，不能持有 ASR、会话状态、工具或 Memory。
- 首版媒体面选火山 RTC AI 音视频互动；它负责 RTC、流式 ASR/TTS、VAD、打断和字幕。Multica server 负责鉴权、房间短 Token、贝克汉姆上下文、工具权限、持久状态和回调幂等。供应商密钥不得进入前端。
- 通话不能把现有“录完上传”的语音消息队列改成高频媒体通道。语音消息继续执行 4.7/4.8；通话只在结束后写最终 turn 和一张 DM 通话记录。
- 贝克汉姆的实时口语模型只负责低延迟对话和获准的 Function Calling；开发执行仍进入现有 Agent task queue 和守护进程。实时模型不能声称未执行的代码、GitHub 或服务器操作已经完成。
- 通话上下文必须抽取并复用现有 channel/DM/project assembler，不得另建 voice-only prompt 或 Memory 真相源。部分 ASR 不持久化、不触发模型和工具；被打断的 Agent turn 只保存用户实际听到的前缀。
- **目标物**：`voice_call_session` / `voice_call_turn`、provider-neutral call service、火山 RTC transport、typed realtime events、共享 call UI、回调与工具幂等测试。实施与 PR 顺序见 `docs/superpowers/plans/2026-07-23-beckham-realtime-voice-call.md`。

### 4.10 Staged daemon update 先停 claim、再 drain — `可执行`（① PostgreSQL lifecycle + ⑤ barrier/持久化回归；owner: @Nash）
- 新二进制校验成功后，server 的更新生命周期必须先持久化为 `ready_to_apply`，再进入重启等待；生产单一真相是 PostgreSQL，不能因 API 进程替换、未配置 Redis 或内存实例切换丢失。`ready_to_apply` 的 2xx ACK 必须表示数据库已处于同态；若 lazy timeout/并发 winner 已把状态变成 `timeout|failed|completed`，冲突 report 必须 non-2xx，让 daemon fail closed。每个 runtime 同时最多一个 `pending|running|ready_to_apply` 更新。
- 前 10 分钟是 opportunistic idle window：daemon 继续接受新 ClaimTask，只要某个 tick 同时看到 `claims_in_flight=0` 且 `active_tasks=0`，就原子设 claim barrier 并走既有 graceful restart。
- 10 分钟 deadline 到达时，无论当前是否繁忙，都必须在 `claimMu` 下原子设置 claim barrier；从这一刻起拒绝所有新 ClaimTask，只等待 barrier 前已经进入 claim/handoff/active 的工作全部归零。只有 `claims_in_flight=0 && active_tasks=0` 后才能调用 `triggerRestart`；禁止靠提前 cancel root context 强杀活跃 Agent。函数入口、deadline/ticker 分支和 durable ACK 后的 immediate-idle 分支都必须在 restart 前 fail-closed 复查 root context；等待上下文取消只终止等待并释放已持有 barrier，不触发 restart。
- **物**：migration 217 的 `daemon_runtime_update` 表与 active partial unique index（①②）；`PostgresUpdateStore` 的 create/exclusion/atomic pop/ready/complete/fail/timeout/latest 与 pool replacement 回归；`waitForSafeRestartWithWindow` 的 deadline stop-claim、claim-handoff drain、active-task drain、cancel-no-restart、deadline 前 idle opportunity 回归（⑤）。旧实现“只等全 idle、期间永久继续 claim”会使这些 deadline 回归见红。

### 4.11 Computer 只有一份 PATH 二进制，升级按 Raft 换文件 — `可执行`（③单一 PATH + ⑤ swap/rollback 回归；owner: @Barry）
- 产品是 `$HOME/.local/bin/multica`（Windows 为 `%USERPROFILE%\AppData\Local\multica\multica.exe`）。start、supervise、OS service 和 `computer upgrade` 都跑/改这一份文件，没有 `versions/<tag>` catalog，也没有 `activation.json` Active 指针。
- 升级先把校验过的字节写到 ephemeral scratch，再 `SwapExecutable`：当前文件 rename 成 `.prev`，新文件落到同一 PATH。失败必须把 `.prev` 换回去。回滚只认 `.prev`，不再走 Previous generation CAS。
- verify / recovery 只能 exec 那个 scratch 文件的 `--version`。`runStageUpdate` 的 status 字符串不是路径；journal `staged_path` 也必须是普通文件，缺失就 restage，不能拿 status 当可执行文件。
- live resident 是唯一 mutation owner；无 resident 才允许离线 swap。Homebrew prefix 不自替换。
- live CLI 先用保存的 human session 创建 canonical server operation，再把同一 operation ID 通过 service IPC 交给 Computer；server 的 `computer:upgrade` WS 派发由 runner 转发到 service，service 按 operation ID 去重。Workspace execution credential 只接受、推进和 attest 已有 operation，不能反调 human route 创建 operation。
- **物**：`server/internal/cli/exec_swap.go`、`stage_release.go`、`lifecycle_upgrade.go`；`TestSwapExecutable*`、`TestCommitStagedActivationSwapsInstallPathAndKeepsPrev`、`TestVerifyStagedBinaryUsesScratchPathFromRunStageUpdateStatus`、`TestRecoverStagedJournal*`、offline `computer upgrade` subprocess 证明 PATH + `.prev`。

### 4.12 Daemon 更新观测必须是单调、持久、可降级的事实 — `可执行`（① PostgreSQL daemon scope + ② typed envelope + ③ daemon 单一 coordinator + ⑤重启/CAS 回归；owner: @Barry）
- daemon 内只有一个 update-observation coordinator；自动轮询和 server 下发更新都必须通过它写入。每次语义变化先把完整 snapshot 原子持久化到本机，再唤醒 current Workspace Runner control heartbeat；持久化失败时拒绝开始更新、重启或对外宣称新状态。进程重启后创建新 `session_id`，`revision` 从 1 开始；未终结的 `checking|updating` 归一为 `interrupted`，已成功的 `restart_pending` 结果必须重放。
- server 以 `(workspace_id, daemon_id)` 保存 daemon-scope 最新事实。register 采用新 session，同 session 只接受更高 revision；完全相同的重复 revision 是零写入幂等，旧 session/较低 revision 忽略，同 revision 不同 payload fail closed 并保留 daemon liveness。旧 daemon 在 register 时没有 envelope 必须清空旧投影，不能继续显示历史“健康”状态。
- 配置来源、运行资格、当前 phase、最近 outcome 是四个独立 typed 轴，禁止用一个字符串混写。`observed_at` 只在 daemon 语义变化时更新，server 另记 `received_at`；错误只允许有限枚举 code 与长度受限、去空白的安全摘要，不得上传原始命令输出、环境变量或凭据。runtime API 对新 daemon 返回 typed `auto_update`，对旧/未知/非法未来枚举返回 `null`，不能因此丢掉 runtime 行。
- **物**：migration 231 `daemon_update_status`、register/heartbeat session-revision CAS、`daemon-update-status.json` atomic snapshot、Runner control change wake、runtime typed projection、old-daemon clear、duplicate-zero-write / stale-session / conflicting-revision / interrupted-replay / malformed-future-enum regressions。此阶段只增加观测，不改变 host default、claim barrier、supervisor 激活或 UI。

### 4.13 Daemon 每个 profile 只能有一个 supervisor 拥有 worker generation — `可执行`（② typed state + ③单一 supervisor + ⑤真实进程回归；owner: @Barry）
- supervisor 必须在整个生命周期持有每个 profile 唯一的 OS advisory lock，并且是唯一调用 worker `Start`、`Wait`、终止和重启的进程。锁冲突必须在 worker 启动前 fail closed；不能依赖 stale PID 文件判断唯一性，也不能把 child `Process.Release` 后失去退出事实。
- worker 正常退出是 terminal clean stop，绝不自动拉起；启动失败或异常退出按 typed exit kind 记录并使用有上限的指数 backoff，稳定运行窗口后重置 backoff。停止必须把终止信号转发给整组 worker 并等待，超时才强杀；停止期间或 backoff 期间收到 context cancel 都不得再启动一代。显式 restart 立即终止当前 worker、清零 crash backoff 并只推进一个 generation；并发重复请求可合并但不能并发启动 worker。
- Phase B 只提供 dormant supervisor foundation，不接 `daemon start`、setup、updater 或 claim barrier。后续 cutover 前必须先在 Phase C 加入跨 generation 的 claim-disabled barrier，以及 health + exact version + register grace 的 commit/rollback；当前 public foreground worker 和 self-successor 路径不得提前双写或并行启用。
- **物**：`server/internal/daemon/supervisor` 的 `Run/RequestRestart/Snapshot`、Unix `flock + process-group SIGTERM/SIGKILL`、Windows `LockFileEx + CREATE_NEW_PROCESS_GROUP/CTRL_BREAK` adapter；真实 subprocess 回归覆盖 clean-no-restart、real start failure、crash-backoff-cap + stable-run reset、cancel-no-resurrection、explicit generation restart、stopping/backoff duplicate-request coalescing、failed-lock/terminal request rejection、second-instance fail-closed，以及 Unix descendant process-group graceful/forced termination。
- **已见红**：Phase B 测试在实现前因 supervisor state/API 全部不存在而 compile-fail；实现后 focused/race suite、Go vet、Windows amd64 cross-compile 与 diff-check 必须通过。

### 4.14 Daemon 出站代理必须复用标准语义并保住本机控制面 — `可执行`（③单一 env 入口 + ⑤双向路由回归；owner: @Ronan）
- daemon HTTP、GitHub 下载、WebSocket 与继承环境的子进程必须共享标准 `HTTP_PROXY` / `HTTPS_PROXY` / `NO_PROXY` 语义；不能给某个 caller 自造一套 proxy matcher。环境变量优先于 per-profile `proxy.http|https`，大小写两套变量归一到同一值；`NO_PROXY`/`no_proxy`/profile 配置取并集，并强制保留 Raft runtime 的 `127.0.0.1,localhost` 本机边界。
- 代理 URL 可能携带凭据：持久配置仍走原子 `0600` config；CLI show/set receipt 只能显示 presence，禁止回显原值。没有真实 caller 的“image pull”等能力不得虚报覆盖；新增 subprocess caller 只有继承 canonical daemon env 才自动纳入。
- **物**：`applyProxyConfig` 的 env-over-config、大小写归一、NO_PROXY 去重+loopback 回归；`taskWakeupDialer` 必须使用 `http.ProxyFromEnvironment`。双向 mutation gate 固定为：配置 `HTTP_PROXY` 时 proxy decision 必须非空（删 Proxy hook 即红）；目标命中 `NO_PROXY` 时 decision 必须为空（强制走 proxy 即红）。

### 4.15 Agent lifecycle 离散命令 — `可执行`（⑤三模式与 Start/Stop 回归；owner: @Codex）
- Product/API 暴露 `startAgent(agentId)`、`stopAgent(agentId)` 与 `resetAgent(agentId, mode)`；Reset mode 固定为 `restart | session | full`，没有 public `action_kind`、`execution_mode`、幂等键或 `after_current_run`。显式 Start 每次生成新的 `launchId + startDispatchId`；manual Stop 只投递当前 launch 的 Computer 命令，不写 durable Stop intent。Agent Panel 用 Runner process Presence 在同一按钮切换 Start/Stop，不读 work/activity status。
- 一场 restart 只活在当前 server 进程的内存里，步骤为 `stopping → resetting_workspace? → starting`。没有 `agent_restart_operation` 账本；server 进程重启后不会恢复。
- Computer wire 只有 `agent:stop`、`agent:reset-workspace`、`agent:start`，没有 composite `agent:lifecycle`。Restart 的 start 使用 `config.sessionId=<canonical provider session>`；Session/Full 的 start 使用 `config:{}`。Raft 将 truthy sessionId 视为 resume、absent/empty 视为 fresh，Multica 不再自造 nil/empty/omitted 三态。session 在发 stop 前从当前连接观察抄到这场内存 restart；之后 start 只读这场状态，不再查 live observation。
- Stop 先记住旧 `launchId`，只有该 launch 的 inactive status 才推进；Runner 必须先撤销 APM admission、fence 迟到 startup，并等待 startup owner、provider lease 与 resident process 全部 quiescent 后才能发布 inactive。Start 在当前 socket 上发出新 `launchId`。Full 必须在上述 stop 证据后 reset AgentRoot，并且 Server 收到同场 restart 的 terminal reset result 后才允许 start。
- 当前 Runner socket 拥有整场 stop → start。Runner Ready 不按 step 恢复，desired/observed reconcile 跳过任何仍在内存中的 restart。relay publish 不等于 accepted/completed；中断的 restart 留在进程内存里，直到当前 socket 走完或人再点一次。Activity 只消费正常 `Stopped → Starting → Idle/Working` 事实；Agent Restart 不新增 toast stream。
- **物**：离散 `WorkspaceRunnerAgentStopPayload` / `WorkspaceRunnerAgentResetWorkspacePayload+Result` / `WorkspaceRunnerAgentStartPayload.Config.SessionID`、内存 `agentRestartStore`、三模式成功链、stale launch fence、reset completion proof；旧 composite payload、heartbeat pending queue、delivery lease、Ready/reconcile redrive、持久幂等账本和本地 `.multica/lifecycle-commands` ledger 不得恢复。

### 4.16 V1–V5 调研进度只认服务端账本，Agent prose 不得推进状态 — `可执行`（① canonical ledger + ② strict envelope + ③单一调度器 + ⑤迁移/幂等回归；owner: @Codex ✅ 已签）
- Research Run Module 拥有目标/计划版本、问题前沿、任务及尝试、来源快照、Observation、Claim/Evidence、Decision、事件序列和交付门槛。Agent 只实现有界 Research Task Interface；聊天、画布节点和旧版报告接口都是 projection 或兼容入口，不能成为 canonical progress。普通群聊仍按消息驱动，不能宣称具备 Research Run 的恢复、证据和交付语义。
- 分派以 `(session, task, attempt)` 生成唯一 dispatch key；结果必须由对应 inbox task 的 task-scoped credential 提交 strict JSON envelope。重复的 request ID + 相同 payload 幂等回放；相同 ID + 不同 payload、错 Agent、错 inbox task、跨 workspace、未知字段、循环依赖、snapshot 中不存在的 quote 全部 fail closed。
- 调度器只有一个 Store Interface：PostgreSQL lease 决定多副本单 owner，状态机决定并发、能力路由、超时、重试、stale result、replan 和恢复。Agent 不得用 `graph-append/source-upsert/report-patch/stage-eval/product-round judgment` 绕过它；initialized run 在 handler guard 直接 409。
- 证据方向固定为 `Source Snapshot → Observation → Claim ↔ Evidence → Report Claim`。`research-run-v2` 把“问题已有答案”和“答案进入最终报告”分开检查：每个 required question 必须保存明确的 answer Claim，最新报告必须包含这些 Claim；每个 Report Claim 必须有指向报告原文的 exact anchor，所在 section 引用的 `research_source.id` 必须能沿 verified Evidence/Observation/Snapshot 支持该 Claim。`research-run-v3` 继承这些规则，并把 decision question、analysis methods、evidence requirements、纳入/排除标准、来源与反证策略、停止条件、未知项和风险作为 goal/plan 版本化的 Research Method Decision；所有后续任务从 Run Snapshot 继承，节点不能静默换方法。只有问题、范围、方法或任务图失效才创建 `replan`；未回答问题、Claim 证据不足、反证缺失和报告缺陷分别创建带目标的 `discover`、`verify`、`counter_search` 或 `synthesize`，不得为局部缺口增加 plan version 并废弃有效证据。报告保存 task/attempt/author，quality 与 citation reviewer 的 Agent ID 必须不同于作者，并且审核必须逐项覆盖最新报告的全部 Claim 和 section。篇幅下限只拦占位文本，不能替代方法遵循、证据、覆盖率和独立审核。
- `research-run-v4` 把 Method 的证据要求变成 Claim 引用的可执行 `EvidenceStandard`：用途、所需来源特征、独立来源数、最低 strength/directness/method_fit 和反证要求。Source Snapshot 记录 `evidence_traits`，Evidence Link 记录 directness/method_fit；Gate 只按当前 goal/plan 中每个必答或报告 Claim 的标准验收，禁止从 depth tier、`source_class` 或全局来源数量推导充分性。要求反证时，成功任务必须针对该 Claim；任意无关 `counter_search` 不能代替。v1–v3 的结果、Prompt 和 Gate 行为继续按 orchestrator version 固定。
- 最终交付必须同时通过当前 goal/plan 版本、required question answer/report coverage、独立 verified source、claim support、矛盾 resolution、major claim inclusion、完整报告结构、独立 quality evaluation、独立 citation audit 和连续低边际收益检查。质量失败创建新的 synthesis revision，不能复用已经 succeeded 的旧任务。预算耗尽必须写 Decision；只有其余 hard gate 均通过时才能交给用户确认，否则 run 明确失败并保留已有证据。
- Gate 的每次系统补救选择必须原子写入 `research_decision(decision_kind='remediation_routing')`，保存 finding、选择的 task kind/capability、目标 question 和理由。`required_questions_unanswered` 必须给出当前最高优先级问题的持久 ID；Control Task 必须写 `research_task.question_id` 并按 kind+objective+target 幂等复用，禁止只靠 Prompt 文本“指向”问题。Gate 读取与任务创建之间目标已被回答时返回可重试的 target-changed，不得把并发进展误判为 run 失败。
- 失败的 quality/citation Decision 不能在 Gate 中退化成一个分数。Gate 必须把 evaluation/report/reviewer ID、失败维度及理由、明确 findings、已审 Claim/section keys 做确定性、有界投影，并沿现有 finding → objective → acceptance criteria 路径交给新的 synthesis revision；Reporter 必须逐项修订对应 Claim/section，不能用泛化重写覆盖评审意见或丢弃已验收证据。
- `research-run-v5` 把评审意见变成可寻址的 `EvaluationDefect`：稳定 key、质量维度、blocking/advisory severity、问题、必需修改、目标 Claim keys 和 section IDs。失败评审至少有一个 blocking defect；每个低于当前 depth score floor 的维度必须有同维度 blocking defect，且所有目标必须属于最新报告。兼容 `findings` 只能由 defect problem 有序派生；两者同时提交时必须逐项相同。V1–V4 继续按已存 orchestrator version 解析，不能接受 V5 字段。
- `research-run-v4` 计划必须为每个 required Question 提供 question-bound `verify`，并至少有一个 synthesis 传递依赖全部 discover/deep-read/verify/counter-search；quality/citation 必须直接依赖这条可交付 synthesis。证据结果新增的 evidence task 必须依赖产出它的任务，并原子加入尚未交付的 synthesis 依赖；新增 required Question 必须同时给出 question-bound verify，动态 replan 必须阻塞旧交付。Gate 在报告之后出现 `canonical_changed=true` 的 Information Gain Decision 时要求重写报告，重复零变化探测不使报告过期。边际收益只按当前 goal/plan canonical graph 的 before/after 计算：verified answer coverage、answer transition、独立 verified evidence、Evidence Link、反证、resolution、verified Claim adjudication 和随图规模递减的 source/observation/Claim/question novelty；Agent 自报 `coverage_delta` 和重复 key 不直接满足停止条件。每批 evidence 的输入状态、输出状态、分项和阈值写 `information_gain` Decision；非 evidence 结果不得把 `last_measured_gain` 清零。
- **物**：migration `274_research_run_backend` + `276_research_report_quality`；`server/internal/researchrun`；`research_run_adapter.go` / `research_run_http.go` / `research_run_guard.go`；scheduler `research_run_reconcile`；CLI `multica research task-result`；builtin skill `multica-research-fleet`；前端 typed Run snapshot 和 steer API。
- **已见红**：全新 PostgreSQL 逐迁移到 274 通过；274 down/up 直接回放通过；真实 Store 集成回归首次执行在 `run_stats` 参数推断处失败，显式 SQL casts 修复后锁住 initialize→attempt→result materialize→dependency activation→idempotent replay。v2 回归先因不支持 `research-run-v2`、质量失败错误进入 replan、placeholder report 被接受而失败；实现后覆盖 required-answer 报告遗漏、作者自审、报告结构、精确引用支持、独立审核范围、质量失败新修订任务和 v1 prompt/contract 固定。后续回归继续覆盖证据去重计数、证据只读视图、依赖失败传播、重试退避、terminal projection recovery；strict envelope、exact quote、DAG cycle、capability routing 和 durable-only prompt 另有单测。v4 PostgreSQL 回归先让 deep run 的单一控制记录被旧全局来源配额错误阻塞，再锁定 Claim 标准通过；无关反证任务的假通过改为 Claim 定向检查；迁移后 v1–v3 来源写入曾因 nil traits 触发 `NOT NULL`，统一规范为空数组后旧版回归恢复。控制环回归先证明旧路由会把边际收益、未回答问题和 Claim 缺口都送进 replan；现锁定 finding precedence、question 绑定、plan version 不变、routing Decision、目标幂等和跨 session/stale target 拒绝。信息增益回归锁定重复 batch 为零、canonical verification upgrade、Claim 降置信度与裁决更新、动态 follow-up 交付依赖、无验证路径 required Question 拒绝、replan 阻塞旧交付、零变化探测不使报告过期及真实图变化强制报告修订。

### 4.18 旧版主动调研目标 — `已废弃历史记录`（不得实施）

本节以下条目记录从未生产启用的旧 V6 设计，已由 ADR-0017 和 §4.19 废弃。保留它们只为说明旧迁移、测试和代码的来源；任何与新规格冲突的固定 Fleet、固定角色、递归 stale、独立评审或交付规则都不是目标行为。
- 目标协议见 `docs/superpowers/plans/2026-08-05-autonomous-research-system.md`。新能力必须把 Hypothesis、Branch、Insight、Search/Corpus 谱系、Integration Round、Dispute、Research Monitor、Capability Observation、Episode 和 Strategy Version 变成服务端可验证事实；增加 Agent 角色或 Prompt 文字不能算完成。
- 每批可信结果都必须能够触发固定输入版本的整合、争议检测、候选工作生成和有分项理由的组合选择。Integrator 只能提议，Evidence、Inquiry、Dispute、Task 和 Gate 状态仍由 Research Run 验证后更新。
- 每个 accepted Task Result 都必须写 Assimilation Check Decision。存在相关同类结果时，原产出 Agent 必须提交有工件引用的 Integration Contribution；存在冲突时转入 Research Deliberation；没有相关结果时记录等待水位，后续结果到达后重检。Agent 不可用必须保留缺席原因，不能伪造 Contribution。
- Insight Derivation 只接受至少两个 accepted/fresh 且来自不同 Task 或不同 Branch 的 Claim/Insight Artifact Version；层级和 input/scope/relation 幂等指纹由服务端计算。没有服务端可观察语义价值时记录 `no_semantic_gain`，任一输入失效必须沿无环 derivation edge 递归 stale 所有下游 Insight。
- Research Run 创建时必须把当前 Research Fleet lead 固定为 `research_director_agent_id`；现有产品中该 Agent 是罗纳尔多。只有该身份能提交 Team Formation 或 lead adjudication。运行中 Fleet lead 变化不能暗改 Director；只能由用户显式改派并写版本化 Decision。
- Research Director 自主创建 Agent 必须经过 Contract 授权、Team Formation Decision、预算/权限/重复检查和 Research Team Membership；失败和退出必须保留事实。没有这些对象的“hire Agent”消息不能改变 Fleet。
- 冲突 Agent 的讨论必须写 Research Deliberation Turn 并由 canonical Position/Evidence/scope delta 衡量进展；deadlock 自动升级给 Research Director。Director 只能按证据解决、拆分范围、创建区分任务或保留未解决状态，不能用身份覆盖 Evidence Standard。
- Research Insight 必须有非破坏性的 Insight Derivation DAG。层级由服务端计算；无语义收益的递归摘要拒绝；任一输入失效必须使祖先 Insight stale 并阻止其进入新 Task Context 和 Report。
- Research Projection 必须为每个 canonical 研究实体提供稳定 typed Node/Edge、完整详情和可重建 Delta，包括组队、Membership、Task/Attempt/Result、Search/Source、Observation/Claim、Question/Hypothesis/Branch、Integration Contribution/Insight Derivation、Dispute/Deliberation、Divergence、Decision、Evaluation 和 Report；前端布局不能回写 canonical 关系。
- Committed Research Event 的投影消费由 `projectionModule` 拥有：只按顺序读取未投影 Event，Projection output 成功后才能确认；失败按 Event 的 durable attempt count 写下一次时间；单次调用最多处理 500 条，避免 Reconcile 长时间占用 lease。Engine 只能请求投影，不得复制 outbox 确认和退避规则。
- Research Result 的接纳前规则由 `resultAcceptanceModule` 拥有：先验证 Attempt 属于目标 Task 且 Inbox 绑定一致，再按 Run 固定的 orchestrator version 解码，并验证新任务所需 capability 当前可用，最后调用 PostgreSQL 原子接纳。PostgreSQL 事务仍必须重新验证 Agent/Inbox、Attempt/Run 状态和 replay hash；预检不能替代事务内授权。Engine 只在已接纳后触发 Reconcile。
- Research Artifact 的 withdrawal 与 supersession 只能经过 `artifactLifecycleModule.Change`；调用方提交稳定 Operation ID、目标、理由及可选 successor/Decision，不得直接拼 Passport、policy mutation、lifecycle event 或 supersession SQL。PostgreSQL Adapter 负责 Run→Passport UUID 锁序、eligibility CAS、policy watermark 和 reciprocal ledger 原子写；同一 Operation 必须可在未知 commit 后幂等重放，历史 Version 和审计行不得删除。
- Inquiry Graph 的生命周期与结构合法性由 `inquiryModule` 独占：Hypothesis/Branch/Insight 只能沿冻结状态机迁移，terminated Branch 必须有理由，多态 Edge 两端必须是同一 workspace/session 的真实实体，`decomposes | depends_on | refines` 必须保持 DAG。Go 预检不能替代 migration 350 的数据库 Trigger；H 章 Dispute 表建立前，`dispute` 只保留为协议词汇且任何 Edge 写入 fail closed。
- Execution Task 对 Inquiry 的责任边界只能来自 append-only 的 canonical Task–Inquiry Target ledger：目标必须是同一 Run 内已解析实体，绑定 Task 与产出 Attempt 必须属于同一 Goal/Plan version，并记录真实 Agent/Attempt provenance。Selective steering 只消费明确的 Branch target；不得从 objective、Prompt、前端坐标或名称相似度猜测 Task 归属，未绑定 Task 在局部 steering 中保持不变。
- Agent Attempt 的冻结工件读取在 normal 或 evaluation grant 撤销后必须返回稳定的 access-denied 领域错误；HTTP Agent surface 映射为不含 Run、Attempt、Agent 或 Passport 身份的 403，不能以 500 暴露内部授权状态，也不能因历史 Manifest 仍存在而继续读取。撤权不得删除冻结历史，旧 `ErrInvalidTransition` 判定在迁移期继续成立。
- Question、Hypothesis、Branch 和 Insight 的证据驱动状态变化必须经过 `UpdateInquiryStatus`：命令携带 expected state version、before/after、实质理由与可解析的 same-Run Evidence refs，服务端在同一事务保存 append-only Transition/Event/Agent/Attempt provenance 并 CAS 更新目标。状态字段本身不是充分审计证据，Agent prose 或前端投影不得直接推进生命周期。
- V6 selective steering 必须携带 current state version 和 canonical Branch UUID；服务端负责扩展 Branch 后代并从 Task–Inquiry Target ledger 计算最小影响集。事务只能 obsolete 该集合内的非终态 Branch/Task，并对其中 active Attempt 发出 durable cancellation；未命中 Task、accepted Evidence 和历史终态工件保持不变。`full_replan` 必须显式声明，不能由空 affected 集合猜测。
- Inquiry Graph 的生产创建只能经 `CreateInquiryGraph` 批命令：调用方必须提供当前 Run state version、真实 assigned Attempt/Agent 与幂等键；领域行、`research-run-v6` production Artifact Passport/Version 和 `inquiry_graph_created` Run Event 必须同事务提交。任何绕过该边界的裸 INSERT 都不构成可接受的生产实现。
- 无限画布必须从同一 `snapshot_id + through_event_sequence` 的分页 Snapshot 开始，随后按连续 event sequence 幂等应用 Delta。重复 Delta 不得产生重复节点；序号缺口或保留期过期必须重新取 Snapshot。大图通过有界 Slice、邻接计数和按需详情读取，不能要求浏览器一次载入全部 Run。语义融合只能来自后端 Insight Derivation；前端视觉聚类不能写回研究结论。
- 每条成功投影的 committed Research Event 必须同时保留 V5 graph event，并发布独立的 `research_projection_v6:delta`；V6 payload 只能是 run-scoped `{run_id, delta}`，sequence frame 必须来自该 committed Event，Node/Edge 身份必须复用 Snapshot mapper。实时路径不得自己发明另一套 ID、丢弃 edge、用前端时钟推进 sequence，或把旧 V5 payload 冒充 V6 Delta。
- A1 已由 `server/internal/researchrun/canonical_state.go` 建立可执行基线：`CanonicalState` 对同一 Run 的 V1–V5 规范表做确定性哈希，排除 lease、调度时间、行维护时间和投影重试字段；`ListRunEvents` 与 `ReplayRunEvents` 按 workspace、连续 sequence 和重复一致性重放 committed Event。新写入的 `v6_run_bootstrapped` / `run_completed` / `source_ingested` 带 `rebuild_schema=research-run-rebuild-v1`；`RebuildCanonicalRunFromEvents` 只能从这些事件重建身份与终态。缺少 schema 的历史事件返回 `ErrIncompleteEventLog`，不能宣称系统已经采用 event sourcing。
- A2a 已由 `orchestrator_golden_test.go` 和 `testdata/golden/orchestrator_contracts.json` 冻结 V1–V5 的完整 Task Prompt 哈希、可接受 Plan Result 哈希和新 schema 拒绝行为。修改旧版本协议必须让 golden 失败；真实语义变化只能新增 orchestrator version，不能更新旧 hash 来掩盖不兼容。
- V1–V5 Research Task Prompt 的版本选择和渲染由 `taskPromptModule` 独占；Engine 与 dispatch 只提交 Run、Task、Attempt、Snapshot 和 Fleet 输入，不能拼接或修改 Prompt。历史 builder 保持不可变，新语义只能新增 orchestrator version，并继续由完整 Prompt hash 验证。
- 旧 V6 合同从未被生产 decoder 接受，也没有生产 Run；ADR-0017 因此允许按 [`2026-08-14-ronaldo-research-director-development-spec.zh-CN.md`](superpowers/specs/2026-08-14-ronaldo-research-director-development-spec.zh-CN.md) 原位替换这一个未发布版本。替换必须同时更新 `docs/contracts/research-run-v6.schema.json`、跨对象合同、Prompt/Result golden、builtin skill 与 source map，禁止把新字段混入 V1–V5 envelope。省略 `orchestrator_version` 的创建仍默认 V5；显式 V6 + Director 创建 V6 Run，且 `research-run-v6` 是 supported runtime version。新 V6 首次冻结后再发生不兼容 hash 变化才代表 V7。
- `AssessV6Activation` 只做剩余证据审计，不能隐式修改省略 version 时的默认值。回滚目标必须是已演练的 `research-run-v5` previous version。用户显式选择 V6 不再需要 `RESEARCH_V6_BOOTSTRAP_ENABLED`。V5 reconciler / V5 Gate 不得处理 V6 Run。
- Research Task 的运行态同步、Ready Task 排序、能力路由、Attempt 创建、Inbox 分派、身份挂接和取消确认由 `executionModule` 独占。已挂接 Attempt 以 Inbox Task ID 为运行身份；未挂接 Attempt 以稳定 dispatch key 查找原执行，禁止因响应丢失直接复制 Task。携带 `research_dispatch_key` 的 Inbox delivery 发生 runtime recovery/timeout 时，通用 TaskService 不得克隆 retry child；Research Work lease/recovery 必须结算原 Attempt 并以新 Attempt、新 dispatch key 重新派发，避免唯一键冲突或同一 Attempt 双执行。取消只有在 Runtime 接受取消或未挂接派发超过 stale 后才能在 Store 确认；派发成功但身份挂接失败时必须撤销刚创建的 Runtime Task。Engine 只能调用该 Module，不能直接组合 Dispatcher 与 Attempt 状态变更。
- Research Run 的确定性 dispatch 故障、预算耗尽和补救终态由 `failureModule` 处理。未知或明确可重试的 dispatch 错误不得修改 Run；能力永久缺失和不可重试 Adapter 错误才失败。Run 失败必须先提交 `MarkFailed`，再取消活动 Attempt，最后投影 committed Event；取消失败时保留待确认状态并停止本次投影。预算耗尽必须先写幂等 Decision，再评估交付 Gate，禁止先看 Gate 后补写预算事实。Engine 不得直接组合 `MarkFailed`、取消和投影。
- Agent Inbox 的 `queued_expired` 只证明原 delivery 在 worker claim 前已终止、不可重投，不证明 Research Task 不可重试。Research policy 必须保留 Task 自己的 attempt budget，结算旧 Attempt 后重新解析健康目标；只有 Task budget 真正耗尽才允许失败 Task/Run。其他 Inbox `retryable=false` 仍按 C2a 收紧 Research disposition，不能被泛化覆盖。
- Research Run 的交付判断和 finding 到补救任务的确定性路由由 `deliveryGateModule` 独占。Gate 通过后只能进入等待用户确认；确认请求必须重新评估最新 canonical graph，不能复用旧 Gate。Gate 未通过时一次只创建最小、可寻址且带完整 observed findings 的控制任务；绑定目标并发变化返回可重算结果，不能失败 Run。Module 不派发任务，任务激活和执行继续由 `executionModule` 负责。Engine 不得解释 finding code、拼补救 objective 或直接执行 Gate 状态转换。
- `researchrun` 禁止重新引入覆盖全部用例的 Store Interface。Engine 的生产实现明确组合具体 `PostgresStore`、Dispatcher 和 Projector；每个内部 Module 只声明自己的窄输入接口，测试替身也按该接口实现。多个窄接口不能再嵌入一个全能组合接口以绕过此约束。PostgreSQL 事务和 SQL 留在 `researchrun` 包内，Handler 只能通过外部 Research Run 用例接口调用，不能获取子实体写接口。
- Research Run 的每个 canonical 写事务必须经过 `beginResearchTx`/`commitResearchTx` 私有 runner，并注册稳定的 `researchTxOperation` 标签。除 `CanonicalState` 与 `ListRunEvents` 的只读 repeatable-read 事务外，生产代码禁止直接 `BeginTx`/`Commit`；`transaction_guard_test.go` 的 AST 扫描与 registry 完整性测试是结构性门禁。每个 registry 标签还必须在 PostgreSQL recovery matrix 中证明 `before_commit` 回滚 + 相同重试，以及 `after_commit` 的 `ErrCommitOutcomeUnknown` + 幂等/reconcile 恢复；`transaction_recovery_coverage_test.go` 保证 registry 与 matrix 一一对应。
- Source Snapshot 的摄入类型必须显式且互斥。`screened_retrieval` 必须绑定完整 Search Plan、Query Execution、Source Candidate 与 accepted Screening Decision（含决定指纹）；`agent_direct_evidence`、`user_attachment`、`workspace_artifact` 和 `api_dataset` 必须保存各自真实 origin，并禁止携带任何伪造 Search/Screening 标识。接纳前合同由 `ValidateSourceIngestionIntent` 执行。`screened_retrieval` 走 `FetchAndIngestScreenedSource`；其余四类走 `PersistSourceIngestion`（migration 426 约束 + origin 列）。Result materialization 继续写 `agent_direct_evidence`。
- Handler 和 scheduler 只依赖固定 `researchrun.ResearchRun` 用例接口：运行创建/读取、Fleet 读取、生命周期命令、Steer、NodeCommand、task-scoped SubmitResult 和批量 reconcile。`NewEngine` 返回该接口；内部 `Start`、单 Run `ReconcileSession`、Module、PostgresStore 和来源/Observation/Claim/Task 写方法不得加入外部接口。接口方法集合由反射回归固定，新增用例必须先说明调用者、授权和不可由现有命令表达的原因。
- A2b 已由 `behavior_golden_test.go` 和 `testdata/golden/research_behaviors.json` 冻结证据接纳、报告物化、评审缺陷传递、有界重试、取消确认和结果幂等恢复的用户可观察语义。跨运行随机 UUID、数据库时间和 scheduler 字段不属于行为 golden；同一 Run 的崩溃前后完整状态比较继续使用 A1 canonical hash。golden 变化必须说明协议或状态机原因，不能直接重录期望值。
- Research Task 自身的 Attempt 永久失败或耗尽预算后必须进入 `failed` 并记录 failure class；`blocked` 只表示任务尚未执行但因依赖终态等外部前置条件无法继续。两者都属于终态，但投影、详情和失败分析不得混用。
- 已发生的 Research 生产故障必须保留脱敏、可执行回归和明确 oracle。当前集合覆盖画布重复节点、合法 task result 被 403、永久 dispatch 失败扩散、报告绕过验证、评审缺陷丢失和重复证据低收益；测试与症状的权威映射记录在自主调研实现计划 A3。只写事故描述或只断言 HTTP/任务数量之一均不能替代端到端状态断言。
- 同一 Run Snapshot 的 canvas Projection 内不得产生重复 node/edge ID，并且重复投影必须保持稳定身份和内容。信息收益只按 canonical graph 的变化计算；相同证据可被幂等接纳为不同 Task 的完成事实，但不得重复增长 Source/Observation/Claim/Evidence，连续零收益必须推进有界饱和计数。
- D-enabled Research Agent 派发必须把持久 Manifest ID/Hash 同时绑定进不可变 DispatchRequest hash、outbox payload/列和真实 Agent Inbox context；只在数据库保留 Manifest 而不给执行侧身份不算端到端上下文证明。两字段必须成对出现，任一变化使幂等请求冲突；outbox 每次 claim 都必须复核 payload 与独立列相等，不能只相信创建时写对。历史无 Manifest 派发保持原哈希与省略字段。执行装置：`researchrun.HashDispatchRequest`、`rebindDispatchPromptForManifestTx`、`PostgresStore.ClaimDispatchIntents`、`handler.encodeResearchDispatchInboxContext` 及对应 outbox/Handler 回归。
- Research 系统评测必须把 Subject 可见的 Task/Environment 与隐藏 Oracle 分离；Executor、Agent Prompt 和生产策略不能读取 Oracle。固定 Corpus 要版本化并覆盖所有研究模式和已声明干扰，多个 seed 的执行错误必须作为失败样本进入分母，不能被 Runner 跳过。事实/冲突、Claim 可追溯性和来源筛选使用独立 grader；删除 grader、缺少 grader 或跨 Corpus version 比较不能得到“无退化”结论。当前可执行装置位于 `server/internal/researcheval`。生产接入只写 `user_confirmed_delivery` Episode 并调用 `EvaluateProductionWindow`；没有 LLM Judge，不得把用户确认伪装成模型评审，也不得在线自改 Strategy。
- Research 来源筛选决定必须通过版本化服务端合同，而不是保存一段自由文本后直接接受。`accepted` 必须命中纳入标准且不命中排除标准；`excluded` 必须命中排除标准；`duplicate` 必须指向另一候选，并用规范 URL 或 SHA-256 content hash 证明同一性。每个决定必须保存可定位事实、审查主体、审查时间、理由和稳定指纹；缺项、未知标准、重复事实或不规范身份事实一律在持久化前失败。当前可执行装置为 `server/internal/researchrun/screening_policy.go`；其接入 Search 谱系持久事务前不得宣称 F 章完成。
- Research 自主行为评测必须验证可观察事实，不能只匹配最终文案：动作至少保留 actor/kind/target/outcome，图至少保留稳定 Node/Edge 和 V6 注册类型，节点详情必须实际提供用途、小目标、进入条件、方法、输入、动作、执行者、结果、证据、决定、失败、恢复及上下游字段，投影必须提供重建 hash、节点身份、分页规模和缺口恢复读数。固定正例 Executor 只验证 grader 和 Corpus 本身；只有真实生产 Adapter 通过同一隐藏 Oracle 才能声明对应能力完成，不得为生产另写放宽版 grader。
- 生产评测进入 Research Run 的唯一可见输入必须先冻结为 `SealedSubjectInput`：只包含 Task、受控 Environment 和 seed，结构上不得出现 Oracle；未知字段、尾随 JSON、schema/hash 漂移一律 fail closed。等价集合先确定性规范化，Document 和 Fault 引用有界且自包含。后续 Passport/Manifest 必须绑定这些原始字节与 subject hash，不能把 Corpus Case 整体、grader 期望或临时拼接 Prompt 交给 Agent。
- 每个交付必须有当前 Contract/Plan 的 Divergence Pass。该 Pass 使用隔离上下文和有界 exploration reserve 提出异质视角 probe；推测只能创建 Question/Hypothesis/Branch/Task，不能直接成为 Claim。
- 生产 Strategy 不得在线自改。Episode 只能产生候选；候选经过固定评测集、历史回放、安全不变量、非退化检查和 Promotion Decision 后，才对新 Run 生效。已有 Run 固定旧版本，且保留 previous version 回退。
- Strategy 升级的版本、候选配置 hash、离线评测快照、批准/拒绝 Decision 和 workspace 当前指针必须是持久事实。版本、评测与 Decision append-only；同一个 request key 只能对应同一组输入。Promotion 在 Serializable 事务中锁定当前指针，只有当前版本仍与评测基线一致且 Decision 已落库时才推进 generation。每个 Research Run 在创建事务中固定当时的 Strategy version，后续升级或回退不得改写既有 Run。
- Research 生产质量/成本监测必须按单一 Strategy Version 的终态 Run 窗口判定，禁止把混合版本或重复 Run 聚合成证据。达到显式最小样本量前只能报告观测、不能宣告安全或越界；达到样本量后必须同时评估质量均值、质量通过率、P95 成本和预算超限率。NaN/Inf、缺失身份/时间和非法预算必须 fail closed。当前确定性判定装置为 `researcheval.EvaluateProductionWindow`。生产 Episode 在用户确认交付时写入 `research_production_episode`；scheduler `research_production_window` 持久化窗口报告。越界只报告，不自动 Promotion。没有 LLM Judge。
- 执行失败的修复决定必须是按目标幂等的持久记录，不能每次重算都开一条新补救路径。`research_target_repair` 以 `UNIQUE (workspace_id, repair_key)` 把"这个 Task 在这个状态版本上对这个确切失败已决定的修复动作"变成唯一行；`repair_key` 含 session、task、goal/plan version、`failure_fingerprint` 和 repair kind，`failure_fingerprint` 含 failure class、source reason 和冻结的 target config fingerprint。相同 canonical failure 只推进 `occurrence_count` 与最近观测；状态版本、目标配置或失败类别改变才允许新记录。允许动作矩阵是不可变数据库判定 `research_repair_action_allowed` 并以 CHECK 约束执行，Go 侧矩阵只做写前拒绝；`research_negative`、`method_invalid` 和 `internal_invariant` 在矩阵中没有条目，因此不可能为研究结果或不变量破坏记录自动修复。修复决定 append-only（Trigger 拒绝原地改写动作、失败身份和状态版本，`occurrence_count` 单调），写入点是唯一失败结算函数 `failAttemptTx`，投影 Event 幂等键即 repair key。
  - **物**：migration `299_research_target_repair`；`server/internal/researchrun/repair.go`、`postgres_repair.go`；`TestFailureDispositionOnlyChoosesAllowedRepairActions`、`TestEveryDurableInboxFailureReasonResolvesToAllowedRepair`、`TestClassesWithoutLicensedRepairRecordNothing`、`TestRepairKeyIsStableAndMovesWithCanonicalIdentity`、`TestTargetRepairIsIdempotentPerCanonicalFailure`、`TestTargetRepairSplitsOnTargetConfigurationChange`、`TestRepairActionMatrixIsEnforcedByDatabaseAndMatchesExecutor`、`TestConcurrentWorkersConvergeOnOneTargetRepair`、`TestMigration299DownUpRestoresTargetRepairSchema`。
- 本条在 schema、状态机、迁移、回放、故障注入和系统评测均见红并通过前保持 `仅文档`；实施 PR 必须逐项把约束升级为类型、唯一约束、事务或测试，并在本条记录具体装置。

### 4.19 罗纳尔多分层调研 V6 — `可执行`（用户可创建 V6；省略 version 仍默认 V5）

本条已从说明性设计升级为可执行合同。装置包括：`research-run-v6.schema.json` 的固定 hash 与九 envelope strict/二次 validator；390–408 migration 的 scoped FK、append-only/version/single-successor/吸收约束；`researchTxOperation` registry 与 recovery matrix；Director Brief/Manifest/Catalog 的有界分页和持久恢复；团队 20 人确认门槛与 50 人硬上限；Projection Snapshot/Delta/Slice、重建 hash 和未知类型降级；每消息 Steering Assessment；Web/Desktop 独立 Report origin、短期 capability、CSP 与精确 iframe sandbox；`AssessV6Activation` 的逐项、带 revision 证据审计。对应实现和测试指针见 builtin `research-fleet-source-map.md`。

- 完整产品语义见 [`docs/superpowers/specs/2026-08-14-ronaldo-research-director-development-spec.zh-CN.md`](superpowers/specs/2026-08-14-ronaldo-research-director-development-spec.zh-CN.md)；机器载荷和跨对象约束见 [`docs/research-run-v6-contract.md`](research-run-v6-contract.md)；表、事务和恢复见 [`docs/research-run-v6-storage-contract.md`](research-run-v6-storage-contract.md)；HTTP、Realtime 和 Report origin 见 [`docs/research-run-v6-http-contract.md`](research-run-v6-http-contract.md)；文件级顺序和退出条件见 [`docs/superpowers/plans/2026-08-14-ronaldo-research-director-implementation-plan.zh-CN.md`](superpowers/plans/2026-08-14-ronaldo-research-director-implementation-plan.zh-CN.md)。实现不得从已废弃 §4.18 选择冲突行为拼成第三套协议。
- 新 Run 由用户选择唯一 Director，初始团队只有罗纳尔多；其他 Agent 全部由 Director 动态创建和管理。Research、Match、Discussion、Integration、Director 与 Report 共用一个持久 Work Item 调度面，Agent 会话不保存 canonical progress。
- `research_run_event.actor_type` 的 V6 Director 决策使用独立的 `director` 值；它表达当前 Director 角色作出的 canonical 决策，不等同于普通 Agent 执行结果。数据库约束必须同时允许 `user`、`agent`、`director`、`system`，否则 Brief ACK 与任意 Director Action 会在事件落账时整体回滚。**物**：migration `427_research_v6_director_event_actor`；`appendEvent` 的 Director 调用点；`TestDirectorRunEventActorPersists`。
- V6 Work lease 不能把 Inbox 排队时间当成执行时间：绑定的 Inbox 仍为 `pending`/可重试 `failed` 时不得回收 Attempt；进入 `draining` 后，超时从 `started_at` 按 Run 的 `task_timeout_seconds` 计算。真正超时并把 Attempt 记为 `lost` 后，对应 Inbox 回合必须经共享 Task cancellation path 终止；只创建新 Attempt 而保留旧回合会占住串行本地运行时，使所有恢复任务永久排队。失联 Inbox 查询以终态为重试凭据，取消失败后下一轮继续，直到 TaskService 将其置为 terminal。**物**：`ListLostV6InboxTaskIDs`、`cancelLostV6InboxTasks`、`TestRecoverV6WorkItemWaitsForQueuedOrActiveInbox`、`TestCancelLostV6InboxTasksUsesSharedCancellationPath`。
- 同一 Agent 的 Inbox 串行门只认父 Inbox Event 仍为 `draining` 的 active Delivery；Event 已进入 `completed`、`suppressed` 等终态后，即使异常遗留或被旧 Computer 继续续租的 Delivery 仍显示 `leased|processing`，也不得阻塞后续 `pending` Work。候选选择与加锁后二次校验必须使用同一判据，避免竞态路径重新引入永久阻塞。**物**：`leaseAgentInboxConversationBatchForRuntime`、`TestAgentInboxDrainIgnoresLeakedDeliveryForTerminalEvent`。
- V6 Inbox Prompt 必须把严格提交边界变成可执行步骤：Director Prompt 明确 Manifest/Brief 字段到 `director_action_proposal` 的逐字段映射，只列出 Manifest 已授权的 credential-proxy endpoint，并要求 action 使用冻结的 `task_specific_schema.payload_schemas` key；不得让 Agent 从旧 fixture、聊天或未授权 catalog endpoint 猜合同。**物**：`BuildV6WorkDispatchPrompt`、`TestBuildV6WorkDispatchPromptMakesDirectorAssignmentExecutable`。
- V6 Submission 是异步持久交接：`work-submit` 返回 `received` 即 Agent 成功完成，后端 reconciler 再推进 `accepted|rejected`；Prompt 不得要求 Agent 占住 Inbox 等待不存在的同步 `accepted` 响应。只有 transport/unknown outcome 才用同一 request ID 和 byte-equivalent envelope 重试。**物**：`BuildV6WorkDispatchPrompt`、builtin `multica-research-fleet`。
- Director Brief 的 `expected_state_version` 固定研究水位，不得被同一 Cycle 自己的创建、派发、恢复、分页 ACK、Submission received 等操作事件判 stale；但该水位之后出现任何其他 material Run Event 必须拒绝 Proposal。Action 的 contract version 始终对照 Brief 水位，服务端执行 CAS 使用当前 live state，并只允许自身前一个 action 的单步事件推进。**物**：`rejectMaterialV6EventsAfterDirectorBrief`、`TestV6DirectorBriefSurvivesItsOwnOperationalEvents`。
- Director Brief 的 Branch Frontier 必须同时读取 S 级 `research_result_node` 与 M–XXL 级 `research_insight_version`；只读 Insight 会让首个原子结果已接受、已入 Frontier，但 Director 永远看到空分支。Brief 中的 `node` 必须严格符合冻结 `node_ref`：S 级 kind 为 `result_s`、高层为 `insight`，不得遗漏 `kind` 或带合同外的 `revision`。`v6_result_node_accepted` 只有在覆盖 Cycle 的持久 Brief 实际含该 `artifact_version_id` 时才算已消费；历史坏 Brief 必须自动建立一次新的修复 Cycle，不要求用户重建 Run 或丢弃已接受结果。**物**：`loadV6BranchFrontierBrief`、`ProcessV6EventTriggers`、`TestV6DirectorBriefIncludesAtomicResultFrontier`、`TestV6EventTriggerRepairsAtomicResultMissingFromCoveredBrief`。
- `atomic_result_submission.task_id` 必须来自服务端创建的 one-to-one backing Task：V6 Work Item 仍是执行权威，Task 只承担冻结合同要求的研究/来源身份；Manifest 必须显式携带该 ID，Result acceptance 只接受绑定到同 Work Item 的 Task。Director 定义的单次 Work mission 必须冻结到 Work payload，不得被成员长期 mission 覆盖。**物**：`ensureV6BackingTaskTx`、`compileV6WorkManifestTx`、`v6WorkPayloadWithMission`、`BuildV6WorkDispatchPrompt`。
- 同一 Director Cycle 的重试 Attempt 复用冻结 Brief；已 ACK 的页面必须仍可按 cursor 读取并标记 `reviewed=true`。省略 cursor 时先返回第一个未读页；全部已读时回到第 0 页，不能用 404 迫使重试 Agent 猜合同。V6 动态成员必须从活跃团队模板复制 runtime，模板排序使用真实的 `membership_generation`；`create_agent` outbox 失败保持 durable pending 并由后续部署重试。**物**：`LoadDirectorBriefPage`、`TestReviewedV6DirectorBriefCanBeReadAgainForRetry`、`researchV6AgentLifecycleAdapter.CreateAgent`、`TestResearchV6AgentLifecycleCreateAgentUsesMembershipGeneration`。
- V6 `agent.create` 的 `tool_config` 是 Research membership 执行策略，不是 Agent runner 的 MCP server 配置，禁止写入 `agent.mcp_config`；新成员的 MCP/runtime 从活跃团队模板继承。Director 给出的空模型或产品语义 `default` 表示继承模板模型，不能把字面量 `default` 交给 provider CLI。**物**：`researchV6AgentLifecycleAdapter.CreateAgent`、`TestResearchV6AgentLifecycleCreateAgentUsesMembershipGeneration`。
- `agent.create`/`agent.archive` Proposal 产生的 runtime outbox 是下一轮 Director Brief 的 material 前置条件。只要同 Run 仍有 `pending|delivering` 的团队生命周期 effect，Event Trigger 不得冻结下一轮 Brief；必须等成员加入/归档事件落账或 effect 明确失败后，按最新 event watermark 创建 Cycle，避免“先建 Brief、后变团队”使新 Brief 出生即 stale。**物**：`ProcessV6EventTriggers`、`TestV6EventTriggerWaitsForMaterialRuntimeEffects`。
- V6 Director Proposal 的 terminal contract error 必须在同一事务把 Submission 置为 `rejected`、结算绑定 Attempt/Work，并落 `v6_work_submission_rejected` 事件以触发最新 Brief；不得保留可重复领取的 `processing` 行。写入 V6 JSONB outcome 的动态 SQL 参数必须显式声明 PostgreSQL 类型，避免 reconciler 在 terminalization 上无限重试。**物**：`rejectV6DirectorProposal`、`isTerminalV6SubmissionError`、`TestRejectedV6DirectorProposalTerminatesAndEmitsTrigger`。
- Director 创建的新 V6 Work Item 初始 `state_version` 必须为 1，与数据库约束和后续 lease CAS 的版本原点一致；禁止写 0 再依赖下一次状态变化修正。**物**：`executeV6CreateWorkAction`、`TestV6DirectorCreatedWorkStartsAtVersionOne`。
- Director `work.create.v1.kind` 表达开放的调研方法语义，不是 `research_work_item.kind` 的调度枚举。服务端必须按 `expected_result_schema_id` 将持久调度类型收敛为 `research|discussion|integration|report`，并把原始语义保存在 `payload.task_kind`；禁止把 `deep_read`、`verify` 或未来方法名直接写进受约束的 Work 列。**物**：`executeV6CreateWorkAction`、`v6WorkPayloadWithMission`、`TestV6DirectorCreatedWorkStartsAtVersionOne`。
- V6 Agent 提交在严格 root schema、二阶段 payload schema 或自哈希校验失败时，HTTP 400 必须返回有界、字段级的合同错误并记录绑定的 Run/Work/Attempt/Agent 身份；禁止把所有错误压成无法行动的通用文案，迫使 Agent 对同一无效载荷盲重试。错误响应不得回显提交正文或凭据。**物**：`SubmitAgentResearchV6Work`、`researchV6InvalidContractMessage`、`TestResearchV6InvalidContractReturnsActionableDetail`。
- Projection 节点详情路由的 `runId` 是 UUID，`nodeId` 是冻结 V6 key（例如 `pv6:goal:<uuid>:1`），不得把 `nodeId` 再按 UUID 解析。浏览器会 percent-encode key 中的冒号，Handler 必须在路由边界只做一次 path unescape，再按 V6 key 合同验证并原样传给投影服务；直接给 handler 塞已解码参数的测试不足以覆盖此合同。**物**：`GetResearchV6ProjectionNodeDetail`、`IsValidV6ProjectionNodeID`、`TestGetResearchV6ProjectionNodeDetailRouteDecodesStableNodeID`。
- Projection 节点详情必须由调用方携带当前画布的 `snapshot_id`，并只从该持久 Snapshot 读取；详情 GET 不得新建 Snapshot，也不得返回另一 Snapshot 的同名节点。过期快照走 resync，缺失或非法 UUID 在 HTTP 边界返回 400。**物**：`ProjectionV6NodeDetail`、`loadPinnedCanonicalV6Projection`、`TestGetResearchV6ProjectionNodeDetailRequiresSnapshotID`、`research-v6-director-client.test.ts`。
- 调研星图摘要的“方向”只能统计 canonical Branch cluster，不能把 Goal、Work 和 Result 节点总数冒充方向数；稳定结果不得包含 Goal，运行/终止数字必须明确标为节点。无 Branch 元数据的 legacy 图继续使用旧节点计数。**物**：`summarizeTypedGraph`、`research-d5-summary.test.ts`、Research `d5.summary` locale。
- V6 RFC 8785 canonicalizer 必须在持久化前拒绝 PostgreSQL `jsonb` 无法表示的 `U+0000` 字符，并保留嵌套字段路径；禁止让已通过 root/二阶段 schema 的载荷到 INSERT 时才以 `SQLSTATE 22P05` 变成不可行动的 500。**物**：`appendV6JSONString`、`TestV6CanonicalJSONRejectsPostgresUnrepresentableNullCharacter`。
- V6 Submission 被异步接受或拒绝并结算 Attempt 后，仍未终止的绑定 Inbox 执行必须经共享 Task cancellation path 收口；不得只依赖模型读懂 `received` 后自行退出。结算行保持可重试资格，直到 Inbox 出现 terminal outcome，避免 Agent 在成功后继续发探针、占用运行时或制造 409 循环。**物**：`ListSettledV6InboxTaskIDs`、`cancelSettledV6InboxTasks`、`TestSettledV6AttemptKeepsInboxEligibleUntilCancellation`。
- V6 原子 Work Manifest 必须把冻结二阶段校验器放在 `task_specific_schema.payload_schemas[<payload_schema_id>]`，让执行 Agent 能从权威 Manifest 复制唯一 schema ID；Prompt 不得让 Agent 猜 `research.*` 名称，并必须明确 `content_layers.catalog_summary` 的 512 字符上限。旧的单 schema Manifest 若收到错误 ID，400 必须给出绑定的正确 ID，不能只说“无校验器”。**物**：`compileV6WorkManifestTx`、`boundV6SecondStage.ValidateV6Payload`、`BuildV6WorkDispatchPrompt`、`TestBoundV6SecondStageNamesAuthorizedSchemaOnMismatch`、`TestAtomicV6WorkManifestGetsOneBackingTaskAndFrozenMission`。
- V6 原子提交根层 `content_layers.uncertainties|conflicts|open_questions` 固定为字符串数组；`task_specific_payload` 内同名字段则服从冻结二阶段 schema，允许是对象数组。执行 Prompt 必须明确区分这两个层级，禁止让 Agent 因同名字段把对象写进根层。**物**：`BuildV6WorkDispatchPrompt`、`TestBuildV6WorkDispatchPromptBindsAtomicTaskIdentity`。
- Director 创建的 V6 Work 以 `research_v6_work_item_branch` 作为方向 scope 的持久权威；每次 Attempt 的 Manifest 必须从这些关联行与当时的 `research_branch.state_version` 冻结非空 `branch_refs`，不得只读取 Work payload 或 Discussion 输入。Agent 只能逐字复制 Manifest 的方向引用，不能用 Run state version 猜 Branch version。**物**：`compileV6WorkManifestTx`、`TestAtomicV6WorkManifestGetsOneBackingTaskAndFrozenMission`。
- 已派发的原子 Attempt 若 Manifest 缺失关联表中确实存在的 Branch scope，属于平台无效派发，不是 Agent 失败：恢复器必须立即记 `platform_invalid_manifest`、取消旧 Inbox、退回 Work 到 `ready`，并返还该次 `attempt_count` 后用重新编译的 Manifest 派发。不得等运行超时，也不得以 `attempt_budget_exhausted` 终止 Run。**物**：`RecoverExpiredV6WorkItems`、`TestRecoverV6WorkItemReplacesPlatformInvalidManifestWithoutSpendingAttempt`。
- V6 的同 request ID、同内容重放必须在 Attempt 已结算后仍返回原 Submission outcome，覆盖“服务端已提交、客户端未收到响应”的标准幂等窗口；终态访问只开放给与精确 Agent、Inbox context、Attempt 和既有 Submission 绑定的 `/submission`，不得因此重新开放 Manifest/Catalog 或允许新 request ID 写入。**物**：`authorizeResearchV6SubmissionAttempt`、`AuthorizeV6Submission`、`RecordV6Submission`、`TestResearchV6SubmissionReplayAllowsExactSettledAttempt`。
- V6 `/submission` 没有 validation-only/dry-run 语义；Prompt 与 builtin skill 必须明确禁止探针、占位符和最小测试载荷，因为任意 HTTP 200 都是可永久结算 Work 的正式持久交接。合同诊断依赖提交前 Manifest 对照与服务端字段级 400，不得通过先提交低质量 schema-valid 结果来试探。**物**：`BuildV6WorkDispatchPrompt`、`TestBuildV6WorkDispatchPromptBindsAtomicTaskIdentity`、builtin `multica-research-fleet`。
- Result/Insight 按 S/M/L/XL/XXL 压缩；promotion 需要至少两个 fresh 同级输入，assimilation 不提升等级。每个输入版本最多一个 canonical successor；已吸收节点不自动恢复，跨 Branch 只能复用 successor。每个 Branch 最多一个当前 XXL，同一 XXL 可以服务多个 Branch。
- Director 上下文每轮从持久 Research Brief 与 Control Brief 重建；终止节点只给聚合总结。Director 不可用时进入 `awaiting_director` 并通知用户，不自动换人。每个 Run active team membership 上限 50，Research token 总量不设产品上限。
- Report 是挂在 Goal 上的不可变 HTML 交付物，不是 graph node。JavaScript 只允许在 `sandbox="allow-scripts"`、无同源/存储/外部网络/主应用桥接的 iframe 中运行；只有 Director 可以发布。独立 HTTPS origin + 短期 capability 仍是首选；未配置第二域名时，已登录成员走 `GET .../reports/{id}/compiled`，前端用 `blob:` 挂载，禁止 `srcdoc`、禁止把 API URL 当 iframe `src`、禁止把 24MB HTML 塞进 JSON detail。不得为过门编造 HTTPS origin。
- 用户显式选择 V6 + Director 即可创建 V6 Run；首页默认 V6 并自动选第一个未归档 Agent。省略 `orchestrator_version` 的旧客户端仍创建 V5。`AssessV6Activation` 继续只做审计，不把省略 version 的默认值改成 V6。独立 Report origin / renderer 未配置时不得挡住创建，也不得挡住用户阅读已编译报告或 Director 审到 published（跳过截图，仍校验 HTML/hash/CSP）。回滚由 `GET/PATCH /api/research/v6/release` 控制：关闭新建并把既有非终态 V6 置 `paused`，禁止用 V5 decoder 读取或删除事实。用户消息路由到当前 Director（`research_director_assignment`），不再唤醒旧 Fleet Lead。首页对 V6 展示 Work Item 进度，不画 S1–S4。Research Monitor 有持久表、scheduler 与 API；没有 Search Plan 的周期如实记为 blocked/incomplete，不伪造检索。

## 5. 验证方法论 — `仅文档`（诚实标注：拦不住人，只能让"猜"显式化）

- **渲染活实体的功能，验收必须含"写入后变更"测例**（fixture 先改后写=永远假绿）。→ 有测试模板后升 `可执行`。
- **"验过了"必须带面的清单**：同一个组件出现在哪几个面（频道消息/issue 详情/评论/系统行/收件箱……），就要在哪几个面各验一次——**验收也是装置，装置只覆盖你想到的入口**（2026-07-16 实例：卡在频道面验 PASS，issue 详情面双卡被 Frank 抓到；与 barrel 单入口错误同构）。
- **验证确切的那个面，不是那一族**：同名概念跨面（Activity 时间线 vs chat transcript vs 模型配置的"思考"）删改前写端+渲染端都要 grep 到；"页面上有个 token"不证明整条链路健康——先问 token 是谁写进去的。
- **先拉 parts 再归因**；两种渲染并存≠写手的锅。
- **"看起来等于原文"≠"是原文"**：渲染碎片拼回去可能正好复原——**那正是它最会骗人的时候**（P0 案：textContent 重拼="干净"→"BE 没问题"的假结论把全队钉死在读路径一上午；真相=写入时已损坏落库）。**要看原文就去查存储原文（DB/API），别拼渲染结果。** 写入面与读取面是两条路，各需自己的回归——渲染侧全绿拦不住写入侧的损坏。**"一个测试可以完全正确地回答一个错误的问题"**（Felix）：绿是真的，只是问错了地方——定位 bug 先定位"哪一半"，再在那一半里问问题。
- **"你测的和用户做的不是同一件事"**（P0 终局）：函数级测试喂完整字符串=「粘贴」，天生绕开逐键触发的 input-rule 路径——**打字会坏、粘贴不会**的 bug，setValue 式测试永远绿。**模拟真实按键的回归才测得到它要防的东西**；触发条件也是影响面的一部分（正常人粘贴网址→数月零真实命中）。
- **装置空转的递归形态**（同案三连）：喂整串=粘贴不是打字；ProseMirror 层 dispatch=框架层的"打字"不是浏览器层的打字（隔着 IME/composition/beforeinput）；在结构上没有第二张卡的面验"双卡"=没得复现不是没复现。**修好一层，下一层还能骗你——"我这层看起来像用户"不算数，解药是沿真实路径走到底（CDP 真按键/真实面）。**
- **"合了≠部署了"**：验收前先核 deploy 终态（readyz），拿旧版验新功能=假 FAIL。
- **merge train 期间不许拿 run id 当权威门**：点名某个 run 的瞬间它就可能被下一个 merge 静默 cancel（不报错、只是永远不会 succeed）——**该盯"dev HEAD 的已部署状态"，不是某个 run**（2026-07-16 实例：两个被点名的"权威 run"先后被挤掉）。
- **数值必须注来源**（`getComputedStyle`/file:line/设计决定）；目测（尤其 2x 截图）不准进稿。量色/量身份取真正绘制的最内层元素。
- **验收分道**：数据/DB/投递→automation；hover/弹卡→自起 `--headless=new` Chrome（有合成器，rAF 正常）；真人只留观感与环境不可用两种情况。点击前先滚进视口。
- **依据分级**（设计稿必备节）：`抄`（注出处）/`定`（注理由）/`实测`（注 file:line）/`目测`（禁止）。别把"我们的选择"说成"Linear 就是这么做的"。
- **React Doctor 不是合并门**（2026-08-19）：`pnpm react:doctor` 不再进入 CI，也不再作为前端 PR 必过项。warning 挡合并会把能用的代码拦在注释位置和 effect 风格上。功能验收以 typecheck / lint / 单测为准。`cursordeadlock` 等真实并发门禁保留。
- **PR CI 只测影响面**（2026-08-20）：合进 `dev` 的门禁不再全仓重跑约 1.2 万条测试。相对 `origin/dev`（push 到 `dev` 则相对 `github.event.before`）只跑变更包及其依赖方；脚本门只在对应文件变更时跑。测试文件不删，本地 `make check` / `pnpm test` / `go test ./...` 仍是全量。改 `ci.yml`、lockfile、`go.mod`/`go.sum` 或分类脚本本身则回到现网 web 范围全量。Job 始终启动，无影响面则空过成功。不再每次重复跑 `make test-agent-delivery-route`。物：`scripts/ci-pr-scope.sh`、`scripts/ci-expand-go-packages.sh`、`scripts/ci-turbo-web.sh`、`.github/workflows/ci.yml`。规格：`docs/superpowers/specs/2026-08-20-pr-ci-affected-packages-design.md`。

## 6. 元规矩：别拿没验证的环节当地基 — `仅文档`（本文立身之本）

> 「当"这条假说解释不了不对称"时，先问"是不是我假设的那个环节本身有 bug"，别拿它当反证。」— Felix

2026-07-16 一天内三人各违反一条**自己写过**的规矩：Felix 拿"服务端不会漏"否掉自己正确的假说；Parker 拿版本假说当结论（差点催错升级）；Iris 目测字号当事实。**三条都写下来过，照样犯。** 当天真正拦住错误的三样全是可执行物：`data-ref-source`、#521 回归 fixture、react:doctor 门禁。

> **"写下来 ≠ 拦得住。" 把"靠人小心"换成"结构上做不到"，才是消灭隐性知识的工程形态。文档是退而求其次。**
> **本文每一条 `仅文档` 都是欠债；哪天能变 `可执行`，就该搬走。**

## 7. 本周教训 → 归层对照表（2026-07-14 ~ 07-16）

| 教训/规矩 | 层 | 物 | 状态 |
|---|---|---|---|
| 快照陈旧 | 类型（删字段）+测试 | #507/#625/#622 | ✅ |
| 写手绕管道 | 结构约束测试 | #613/#624 | ✅（#510 欠） |
| 漏锚/吞边界 | parser+回归 | #637 | ✅ owner @Barry 已签；已合并，仍待 live deploy proof |
| 漏锚可观测 | `data-ref-source` 断言 | #520 | 进行中 |
| chip 第二面孔 | 共享组件+restricted-import lint | #520 | 进行中 |
| 属性语法孤儿 | 共享组件 API | #518/#636 | #636 review 中 |
| 客户端假全局序 | 后端 sort/group+跨页 fixture；前端原序透传回归待补 | #635 / 待立 | 后端✅ owner @Ronan 已签；前端⛔仅文档 |
| 未知枚举崩卡 | 白名单降级 | #632 | ✅ |
| env 泄漏/PATH 漂移 | 中央 sanitizer+合同 | #627/#512 | 首刀✅/合同进行中 |
| 同 agent 并发分身 | lease 排他+单槽 | #611 | ✅ |
| 本地 test-DB drift | 迁移 bootstrap+guard | #634 | review 中 |
| 每文件一组件（工具盲区） | 拆件+真 0 验证 | #515 | 进行中 |
| 给决策者的理由，强度必须准确——理由的强度决定他还有没有选择："不可逆/被毁"=关门（只能同意），"成本上升/多一变量"=开着门（可以认成本拍板）。**夸大理由不是更有说服力，是替他做了决定还让他以为是自己做的。结论对≠强度对，强度才是他分配注意力的依据。** 三种污染形态，可检测性递减：①**动机污染结论**→可自查（问"我需要它成立吗"）；②**"看起来在对抗动机"的结论也被污染**（克制像美德）→自查更难；③**替别人主张需求**→**自查无效**——机制：自查审的是"我的动机"，而这条的需求根本不在你身上（"给某人稳定对照面"里想要的人是某人，只是他从没说过），审一百遍都审不出；**解药不是更诚实地自省，只能是去问那个人——"替谁主张，先问谁"是唯一出口**。押中不算问过（无害是运气不是方法）。附则：**别把没验的推断放进通篇硬数据的消息里**——它会穿上数据的衣服，读者没有任何线索能把两者分开（分不开是发的人造成的，不是读的人没看仔细）；尤其当那句恰好是唯一朝某个决定用力的话。机制≠频率：机制铁证只说"这么做会坏"，推优先级的是频率，频率要观测数据不收推断 | ⑥review 红线 | #649 案例：Felix 用"证据被毁"（实为镜像可回放）支持"不合自己的 PR"，被 Ronan 拆；降级成"给 Wren 稳定对照面"又被 Wren 本人拆（"别为我扣着"——替他声明了他不需要的需求且没问过他）；Iris 11:43 P0 播报"每条 URL 都坏"（实为 typed-only、真实命中≈0）——结论对但强度错 | ⛔仅文档 owner @Felix/@iris 合签 |
| legacy `mention://` 写入回流 | CI grep（候选） | 待立 | ⛔ 待 owner 签 |
| 写后变更测例模板 | 测试模板（候选） | 待立 | ⛔ 待 owner 签 |
| 假说靠查证不靠推理——初版"正确输出全来自 grep、错误全来自推理"被 Iris 精确化：**推是必须的，把推当结论才是错的**（纯 grep 不产假说，没有假说就没有值得查的东西；区别不在推还是查，在推完有没有拿去撞数据）。每条错的推都长得很有道理，这正是危险所在。附：**一个错被公开摆出来，才让下一个人认得出自己的同款**——最贵的几条律全是从"某人肯把自己的错摆出来"里长出来的，不是从任何一次成功里 | ⑥review 红线（结构上到顶：无法 lint 一个假说） | URL P0 排查（#531 线程，Felix 一日十六个假说十六次被数据打掉，但其中至少三次给出了"该查哪儿"的方向；七个 PR 零行代码建在错判上——因为每次都有人去查了） | ⛔仅文档 owner @Felix 自签，Iris 精确化 |
| 推理的隐藏前提 = 没人见过它红的装置：发推理链时必须把默认前提摊开（"见过它红"管装置、"能重现发现"管方法，这条管推理本身） | ⑥review 红线 | #531 案例："无空格开火→必走无守卫分支"默认了"mark 是这个插件打的"——没人验过；真机读数的价值正是验这一格 | ⛔仅文档 owner @Felix 自签 |
| 验收测试要**两轴都真**：真实构造入口（factory/组装链，不用手搓近似）+ 真实运行环境（真浏览器，不只 jsdom）——**少任一轴，绿都可能是假的**。近似 factory 漏掉 factory 层回归（难查的那层）；jsdom 漏掉运行时环境（今天 phantom 只在真浏览器现形、真 factory 在 jsdom 照样绿）。同 #463 span 律、原样输入律同族：测真实的那个东西 | ⑤测试 | #657 案例：①构造轴——Felix 两次用手搓近似 createEditor（第一次审+#663 新测），近似拦不住 factory 层，自catch 换 `createEditorExtensions({})`；②环境轴——同一真 factory 在 jsdom 绿、真浏览器出 phantom（Iris 门①=真浏览器终验，jsdom 一路绿也没抓到） | ⛔仅文档 owner @Felix（构造轴）/@iris（环境轴）合签 |
| 规矩的作者不因写过它而免疫——尤其当"违反它"恰好让自己的论点更好听时（不是忘了，是有动机忘）；可操作版：当违反某条规矩恰好让你的结论更好看，就在那一刻停下来重读它 | ⑥review 红线 | #537 工作量估算案例：上午刚裁定"给会增长的那侧发清单=保证漂移"，下午为论证"#537 很小不该挡 P0"顺手估成"三行加 CJK 区间"——用自己刚否掉的做法估自己定了方向的任务；十分钟后 Iris 在"host 必 ASCII"上交了第二个样本 | ⛔仅文档 owner @Felix/@iris 合签 |
| "我认错了"≠"记录已修正"：认错发生在聊天里，记录活在任务卡/文档里，前者不会自动传播到后者，而后者才是下一个人读的。**但当记录根本改不了时（raft 任务卡描述字段 CLI 只能改 status），认错永远传播不到记录——这是工具缺陷不是自律问题**（修复=task #541 字段可编辑+修订留痕；落地前唯一可行=勘误置顶+台账人肉追）。**把结构性缺陷写成个人失职，会让所有人更努力做一件做不到的事，而真正的修复永远排不上号。** 三种病形共一个根：**没写**（低害：人去查）、**写了但过期**（高害：人照做）、**写了但一开始就错**（高害且更难发现）——根 = 坐在权威位置上、没人/没工具负责让它保持真。（本行自身即第四个证人：初版把"改不了"写成了"没去改"，挂在本文档权威位置上直到 Felix 查了一下 `--help`。） | ⑥review 红线 + 工具（#541） | task #537 旧 fix direction 被否两次仍挂任务卡权威位（实测：照做会把契约 3 打红）；Iris `#MUL-123` 笔误挂设计稿权威位被 Frank 当规范读；本行初版误归因 | ⛔仅文档 owner @Felix/@iris 合签 |
| review 要拿代码撞【授权面】（契约签了什么），不是拿代码撞它自己的名字/内部自洽——"名字如实描述代码在做什么"回答不了"代码做的事有没有人签过"；顺手多支持没契约的范围，正是范围漂移进代码的门 | ⑥review 红线 | #650 案例：CJK_LETTER_REGEX 覆盖 Han+假名+谚文，Felix review 判"名字配代码，可以"放过；Barry 抓出真问题=契约只签了 Han（五契约零假名零韩文，扩文字须另立契约），代码动了没人签的两种文字 | ⛔仅文档 owner @Felix 自签（Barry 抓出） |
| "看起来大家都同意"是最便宜的假证据——**N 个人各自没查 ≠ N 次核对，= 0 次核对穿着 N 次的衣服**；复述是把 0 变 N 的那步：用自己的声音重说别人的猜测，它就像被独立佐证过了，一致本身变成证据，没人再去查。能暴露的那格（亲自查一次源头）正是没人跑的那格 | ⑥review 红线 | 时间幻觉案例：14 点全队互道"收工愉快/明天见"，"今晚/明早"在多人消息里连环复述，无人看表，Frank 一句话戳破；同构：textContent 拼回"看起来等于原文"顶替查库（4 人查一上午读路径） | ⛔仅文档 owner @iris 自签（放大器自认） |
| 绿可能是因为错的理由绿的——两个机制在某格给出同一答案时，分歧被掩盖；矩阵缺的永远是"两个机制会不一致"的那一格，设计用例时专找它（oracle 失败+兜底恰好同答 ≠ oracle 验过） | ⑤测试 | #537 案例：`x.中国 ✅` 是因为 oracle 不识别该 TLD、兜底"保留整串"恰好同答——能暴露分歧的 `x.中国吗` 不在矩阵里（Felix 跑出）。二阶案例：随后"oracle 对 IDN TLD 全瞎"的表述又被 Ronan 的 `.рф`/punycode 反例打掉（那两类 fuzzy 认）——准确说法只能是"不识别**所测**的多组 raw-Unicode TLD"，已证列表原样列出不升格全称；三阶（Felix `укр`/`السعودية` 实跑）：`рф` 过而同为西里尔的 `укр` 不过——oracle 表**没有语义只有成员资格**，任何按字符集/语言起的概称都会被表里下一个特例打脸，"已证列表原样"不是保守而是唯一不会变成假话的写法；真机四阶（Iris 拦假 PASS）：验 #650 时屏幕上 `吗` 在链接外看似 PASS——实为 #531 先把 URL 截断在 `https://x`、`吗` 根本没走到 host-overrun 逻辑，"两病同答"掩盖了"#650 未 fire"，靠读 DOM href（text=`https://x`）拆穿——**把 A 的病当成 B 的药是本律最贵的形态**；同形状：fixture ordering 掩盖 #616 | ⛔仅文档 owner @Felix 自签/@Ronan 二阶收窄/@iris 真机四阶；`x.中国吗`（不裁）+`x.рф吗`/punycode（记实际行为）已成钉死测试 |
| 陈旧本地基线两个方向都骗人且不报错（git 忠实按你给的 ref 算，烂的是 ref 的含义）：让没支撑的结论看起来有支撑（假证据）、让干净的改动看起来是灾难（假警报）。**主防线（④候选）：根本不用本地 `dev`——一律 `origin/dev` + 跑前 `git fetch`；worktree 起手式/CI 加 `git merge-base --is-ancestor origin/dev HEAD` 不过就吼**（同 #507 删字段那招：陈旧快照骗不了从不问它的人——让快照不存在，不是让人记得校验）。**兜底（⑥）：实跑证据必须带 base（branch@SHA），无 base 按未验处理**。措辞注意：基线烂收回的是结论的**证据资格**不是结论本身——结论可能仍对，但没支撑就得重跑，支撑烂的结论下次在别处塌 | ④检查（候选，谁顺手谁加）+⑥兜底 | 一小时内三例同一棵落后 504 commit 的树：Felix 假证据（#547 读到 #605 前的序列化器）、Barry 假证据（channel-route 自纠）、Iris 假警报（diff dev...HEAD 得 721 files/108066 行，换 origin/dev 实为 2 文件） | ⛔仅文档 owner @parker 立/Iris 升档设计，④待 Barry/Felix 落 |
| 家族病灶：**代码默认"ASCII/空白分词"的世界观**——中文团队的产品里这不是边缘；审计线索：任何枚举字符类做边界/清洗/分词的正则都是候选面。已知同根四面（一天内）：`CJK_URL_TERMINATOR_REGEX`（#537 吞汉字）、`identity.go [^a-z0-9]+`（中文名塌成 actor、`café→caf`、`qa-bot→qa_bot`）、tiptap suggestion `@[^\s@]*`（中文 query 吞后文）、URL 前贴中文不成链接（#546）。修法共性：**枚举有限侧（成员表/输出契约），永远别枚举"哪些字符终止"**。两条附则（Iris）：①**有界≠正确**——`[a-z0-9]` 白名单有界、可枚举、永不漂移，而且是错的：它从谨慎而非需求推出，顺手的选择长在权威位置没人问凭什么（同 #MUL-123 笔误成规范）；②**不可能失败的对照组=没有对照组**——纯字母名当对照永远绿、`qa-bot→qa_bot` 照样烂，对照格必须选"现在就红/能红"的那格；③一个数据点会被读成例外（`café→caf`=变音符特例？），第二个不同方向的数据点才让通例显形（`qa-bot`） | ⑤审计清单候选（grep 字符类正则盘点） | 四面全为当日实证；第四面（qa_bot 短横）是真机上自己撞出来的；#543 验收因此钉三格（阿策/alice/qa-bot） | ⛔仅文档 owner @iris 归纳，各面 owner 已分（#537 挂/#543 修/#547 观测/#546 批处理） |
| 命名不得宽于已验范围——"名字比证据跑得快"：用比验证语料更宽的词命名契约/实现（只验了 Han 却叫 CJK），下一个人会按名字的范围信任它；扩范围必须另立该范围自己的契约（没有真实语料就别编）。第二种害处（Iris 补）：**说过头还会把有意的取舍说成顺带的结果**——"全产品统一 URL 定义"抹平了"paste 路径故意不动以免扩回归面"这个决定；准确的窄句反而信息量更大 | ⑥review 红线 | #537 案例：五契约全 Han 却叫"CJK"（Ronan 抓/Iris 认领）；#657 文案案例："全产品一个定义"实为仅键入路径统一（Barry 抓/Iris 认领——手上有事实仍顺手说大一号） | ⛔仅文档 owner @Ronan 提出/@iris 认领 |
| 撤回结论时搜【全部】下游——包括自己的 memory/索引/摘要（判据："这句话的任何一个变体，还站在任何一个我会再读到的地方吗"；浓缩版最危险：去掉上下文只剩听起来对的那句）。找到后按三层处置：**能改→去改**（修复）；**改不了→在读者会看到的地方放勘误，并把"改不了"记成工具缺陷 #541**（缓解）；**根修=#541 字段可编辑+修订留痕**。对每天读自己 MEMORY 开工的 agent 这是结构性风险：memory 就是权威位置且没有 reviewer | ⑥review 红线 + 工具（#541） | #537 任务卡（被否三次仍挂卡）；Iris 自己 MEMORY"铁律"节躺着已撤回的 RFC 3986 例（公开宣布"干净"之后发现）；Wren MEMORY 有同句浓缩版（照此law 扫出并划线）；Parker MEMORY 同款（13:40 划线） | ⛔仅文档 owner @iris 自签，三 agent 各自验证 |
| 记录的第五个敌人："没读就动"——**"整理"和"删除"之间只隔一个未经验证的假设**；任何按范围的批量操作（**行号/内容锚点都算**——"两个锚之间只有我以为的那些"同病），先 `grep '^## '` 看范围里有什么（范围假设是今天唯一**不可逆**的假设形态：别的错猜代价是白想，这个的代价是"记录没了且不知道它存在过"）；动权威记录前先留全量副本 | ⑥review 红线 | Iris 整理 MEMORY：假设 96 行"都是 LIVE 块"没读就删，实删四章（当日根因全录/#541/关键律），靠顺手 sed 的 /tmp 副本捡回；Felix 四次锚点式 python 替换幸存=运气非纪律（查证 14 章全在）；该钩子 8 分钟内救场三次（Wren 划废行/Iris 捡回/Felix 查证） | ⛔仅文档 owner @iris/@Felix 合签 |
| 认错认过头也是不准确——把别处犯过的错顺手认领到没犯的事上，是"让证据配合我想说的话"的反面形态，且更隐蔽（穿着诚实的衣服）；变体：**拿假的全错战绩换免责**——"我 N 次全错、别听我的"说完就不必为判断负责，代价是劝团队连对的判断一起丢弃。认错前和下结论前一样要分清事实，对错各自拿证据说 | ⑥review 红线 | Iris 案例：把早上真犯的错认领到真跑过的 21,554 行扫描上（Ronan 原件反证）；Felix 案例：一路自称"18 次全错"对完账是假的——"该拿真实案例撞修复"那次判断对且挡下修不了原案例的 P0 | ⛔仅文档 owner @iris/@Felix 各自签 |
| "我拿不到"≠"拿不到"——把【自己的】权限/工具边界当成【证据的】边界，然后停在那儿等别人；正确动作：说清"谁能拿到"并点名，或者升级请求权限，不把断言降级成"没法验" | ⑥review 红线 | #537 案例：Felix"我没 DB 访问拿不到原件"就停；Ronan 直接只读查出 canonical row e6c14d53 定案 path 型——证据一直可达，只是不经他的手 | ⛔仅文档 owner @Felix 自签 |
| 修 bug 的验收必须包含【引发它的那条原样输入】——简化形状不能替：契约、gate、测试矩阵、review 可以全对而全体错过原始报告（简化形状恰好把真实案例排除在外时，六道验收一致=零道验收） | ⑤测试（原样输入=回归矩阵强制首行） | #650 案例：五契约全是 `x.com吗`（host 型），07-03 真实 DM 是 github URL+吗（path 型）——六人验收无一拿原样输入撞修复，合并前 Felix 实跑发现"唯一观测到的真实案例合完照样坏"；host/path 在该修复里是不同机制，简化时被静默换掉了 | ⛔仅文档 owner @parker 记（Felix 发现），#650 起原样输入进钉死测试 |
| 防退化用例的最好形态 = 测那些现在就对的东西——它们标出"哪些行为是资产、不是空白"；只写"修好 X"的验收看不见 Y 正在工作，而误修恰恰从"不知道 Y 存在"开始 | ⑤测试 | #537 五契约中 3/4/5 反向契约实测已绿——旧修法（CJK 扩表）会把契约 3 打红，被这三条当场拦下 | ✅已成用例 owner @iris 签 |
| "扫不到"的前提是"知道要扫什么"——负向扫描（0 命中=干净）依赖猜对全部目标形态，而内部 handle 格式**没有固定也不该固定**（agent.id=UUID；`actor_<N>`/`agent_<hex>` 只是泄漏出的历史外观），**枚举格式=给会增长的一侧发清单、必漏**（本行初版正是这么写的，20 分钟后被 Frank 一个问题打掉——见案例）。正确判据=**正向对照**：取该消息自己的原文 `content`/`parts[].id`，断言屏幕/剪贴板显示 display name 且不等于原文内部串——原文是什么格式根本不用知道，"有限的那一侧"是这条消息的原文，不是全世界的 handle 格式。fixture 类断言（handle 由测试作者给定）不受影响（Felix 切分） | ⑤测试（正向对照进 #537/#531 回归模板） | #649 验收近失：Iris 按截图扫 `/actor_\d+/`，实际泄漏体是 `@agent_4fce5ed5`；本行初版开出"双格式枚举"处方=同病复发（Iris 骂完 CJK 终止符枚举 20 分钟后在自己规矩里重写了它），Frank 问"为啥要固定 agent id 格式"当场揭穿；救场判据自始至终是 fiber 正向对照 | ⛔仅文档 owner @iris 自签（Barry 评审口径已同步更新），正向对照断言待 #537/#531 落地成用例 |
| 台账的诚实性比完整性重要——"看起来有人在做"是最贵的台账状态；意向≠claim，挂名≠在做 | ⑥review 红线 | 同日两例：#531 早晨无主 in_progress（事后发现）；#537 执行人裁定（事前拦：Felix"明早接"明确记为意向不记 claim，直到 Wren 实 claim 才报"有主"） | ⛔仅文档 owner @iris 提出、@parker 执行签 |

### 4.17 跨设备 Memory 以 portable 中心事实 + device-local overlay 同步 — `可执行`（① tombstone/change_seq + ② typed cursor/outbox + ⑤回归；owner: @Kiro ✅）
- portable USER/RELATIONSHIP/MEMORY/project/channel atom 必须先写本机 `memory-sync-outbox.json`，server ACK 后才能移除 batch；daemon 重启和网络失败不得靠已推进的文件 hash 丢写入。每次 agent turn 前按 `change_seq` cursor 增量 pull，不再用一次性 hydrate marker。
- 删除是 `superseded + deleted_at` tombstone，离线旧设备自动上行不得复活；active 更新、conflict 和删除都推进单调 change sequence。冲突内容移出正式文件，只进 `REVIEW.md`，不得作为权威规则注入。
- 绝对本机路径、loopback endpoint、credential-like 内容在 daemon 和 server 双端 fail closed，不进入中心。机器环境事实写 `$MULTICA_AGENT_ROOT/devices/<daemon-id>/STATE.md`；运行时不再暴露独立的 memory/device/skill 目录变量。
- published/bound skill 的跨机事实仍只有 `skill/skill_file/agent_skill`；`skills/enabled` 是可重建镜像，draft/sync_queue/provider-global skill root 不因换机自动搬运。OS/arch/tool capability 需要独立 typed manifest，不从路径猜测。
- Agent 可切换到另一台电脑的 runtime；跨机目标 daemon 必须显式上报 `memory_cross_device_sync_v2`，否则服务端 fail closed。同机 runtime 切换不需要该能力。切换只迁移 portable memory 与服务端任务状态，不声称迁移本地工作目录、provider 登录态或 device-local 状态。
- **物**：migration `278_agent_memory_cross_device_sync`；`memory_center_replication.go`；`memorysync.PortabilityReason`；`memory_center_replication_test.go`、`compare_test.go`、runtime memory-scope contract tests；完整产品模型见 `docs/agent-memory-model.md` §8.1。

### 4.18 Agent workspace 只有一个持久根 — `可执行`（②单一路径 API + ⑤合同测试；owner: @Codex）
- `WorkspacesRoot` 默认且唯一为 `~/.multica/workspaces`；每个 Agent 的根目录、工作目录和 subprocess cwd 都是 `<WorkspacesRoot>/<workspace_id>/agents/<agent_id>/`。路径拼装只通过 `server/internal/agentworkspace`，禁止 caller 自己拼 `agents`、task ID 或 provider/profile 后缀。
- 运行时只暴露 `MULTICA_AGENT_ROOT`。`memory/`、`skills/`、`devices/`、provider 私有配置和 Agent 自己创建的代码/worktree 都位于 AgentRoot 下，以相对路径定位；不得为这些子目录增加平行 context 字段或 `MULTICA_*_DIR` 环境变量。
- 同一 Agent 跨 task、daemon 重启和 provider 切换复用同一目录。硬切不扫描、不迁移、不删除旧 per-task/repo 目录；旧文件留在原处，新运行只认 canonical AgentRoot。
- Multica 不 clone/pull/reset/branch/worktree，也不提供 `multica repo` 命令。Agent 自己把 checkout 放进 AgentRoot；干活时先选 workspace 内的项目目录或 worktree，再跑 git。项目资源仍是用户管理的 metadata：不改变进程 cwd，不写入 daemon claim/register payload，也不把 repository URL 注入 runtime brief（brief 在 resident 复用时不会因资源变更重写）。当前任务绑定了 project 时，Agent 用 `multica workspace info --projects` 读现活的 project 与 resource 绑定。普通 `workspace_role=member` Agent 可读 `workspace info`：Workspace/Agent/Computer/失败摘要只走 `GET /api/agent/workspace-info`，Projects + resources 只走 `GET /api/agent/projects?include_resources=true`；不得借 `OwnerUserID` 命中人类 API 或解锁其余 private Computer，Computer 可见集仅为 public + 当前 Agent 绑定项。
- AgentRoot 不参与 task GC，也没有后台 retention/GC；仅用户明确选择 full reset，或在 Computer 存储页确认删除时，才可删除精确 canonical root。full reset 是硬切语义：先强制中断 runtime，然后直接删除并重建，不等待 quiescence。
- **物**：`server/internal/agentworkspace/path.go`；`agent_runtime_turn.go`；`execenv/agent_workspace.go`；`TestCanonicalAgentWorkspace*`、`TestMulticaAgentRootStableAcrossHarnessSwitch`、`TestMulticaAgentEnvUsesProviderNeutralRoot`、`TestAgentWorkspaceHoldsCodeCheckouts`。

### 4.19 Agent 消息链路硬切到 Raft 风格 coordinator — `可执行`（Workspace Runner 单一 Inbox + 本地 coverage receipt + MessageDraftStore）

- Server canonical `Message` 是唯一通信真相；机器侧 `Delivery/Pending/Context Boundary` 只负责 at-least-once 传输、可重建待处理投影和上下文 freshness，不再引入 Inbox Event、Task、lease 或 `agent_execution` 身份。Notice 无正文、可合并，不推进 boundary，也不产生前端 `Message received` Activity；该文案不是内部 event/enum/trace/wire 名称合同。
- 机器传输事件名和 envelope 对齐 Raft：Server→machine 使用 `agent:deliver`，machine→Server 使用 `agent:deliver:ack`；ACK 固定携带 `agentId/seq/deliveryId`（可选 `traceparent`），只证明 Computer 仍负责任，不证明 Message 已读、进程活着、正文已进入 runtime 或 Context Boundary 已推进。无进程时的 accept table 与禁止项见 0.3 与 [`docs/agent-message-delivery-contract.md`](agent-message-delivery-contract.md)。
- Credential Proxy 拥有内部 `seenUpToSeq` 注入、本地/服务端 held 消费和十分钟本地 Draft；首次发送前生成并随 Draft 保留内部 `idempotencyKey`，显式重放由 Server 返回原 Message 或拒绝不同 payload，不能重复插入。Agent 不控制 cursor/idempotency key，hold 不自动重发。CLI 硬切为 `message send/check/read/search/resolve/react`，参数和行为以 Raft canonical command 为准，不保留 Multica 兼容别名。
- `message check/read` 与 freshness hold 的 Context Boundary 统一使用 two-phase coverage：coordinator 只 prepare 代表本次具体输出的 machine-local receipt，不先删 Pending/写 boundary；CLI 先剔除内部 receipt 并成功写可见输出，再通过 launch-scoped Agent Proxy credential commit。输出失败不 commit，输出后 commit 失败必须显式报告可能重放。hold 虽最多展示 newest three，receipt 必须覆盖完整 represented Pending range；Draft identity 保留且禁止自动 resend。
- 首版不为理论上的同 Agent/target 并发发送增加锁、持久队列或新错误码；Proxy 只在实际重叠时写 `agent_message_send_overlap` 结构化 warning，日志不得包含正文、附件名或 credential。是否串行化必须由真实重叠数据和故障证据驱动。
- target Context Boundary 只保存在 daemon 进程内存中，对齐 Raft `AgentVisibleDeliveryLedger`；Pending 正文始终从 Server canonical Message 重建，禁止本地 durable receive ledger。daemon 重启后 boundary 为空，Server 依靠未 ACK Delivery 重投重建，允许 at-least-once 语义下的保守正文重放。
- busy Notice 在 3 秒窗口内合并，只投影总 Pending 数、changed targets、每 target 数量/短消息 ID/最新发送者/mention flag/可选 attention hint，绝不携带正文或附件；同 session Pending 指纹不变则去重，失败 debt 15 秒后重试。idle 直接交付正文，Notice 成功也不消费 Pending、不推进 boundary、不发 `Message received`。Activity 按 Raft 的阶段语义分别投影：正文交给 runtime 显示 `Message received`，`message check` 显示 `Checking messages`，`message read` 显示 `Reading history`，`message search` 显示 `Searching messages`。
- 附件上传基础设施对齐 Raft Upload Session/direct upload/完成验证；Agent send DTO 提交正文和 attachment IDs，Server 校验后组装 Multica canonical `parts`。Proxy 不理解或构造 Message Parts。
- 完整术语、状态、Activity 时机、bounded held context、Draft 和命令契约见 [`docs/adr/0010-direct-message-delivery-lifecycle.md`](adr/0010-direct-message-delivery-lifecycle.md)。该 ADR 已取代 4.2 等旧 task/lease 消息路径；后续不得恢复第二条 coordinator 或 Task/lease 消息权威。
- Agent Proxy credential 只证明固定 Workspace/Agent/runtime 身份，不是 command allowlist。Agent Command Policy 必须按 [`ADR-0014`](adr/0014-roll-out-agent-command-policy-additively.md) 增量上线：单一 `state` 为 `legacy_passthrough|allowed|denied|unavailable`，缺失/未知项不得解释为 deny，现有 Agent CLI 基线在分类完成前继续原 transport + Server authorization。禁止注入或信任 `MULTICA_AGENT_ACTIVE_CAPABILITIES`；只有完整 command inventory、权威 policy derivation、shadow 无意外 denial 与 mixed-version 回归全部通过后才能 enforcement。

### 4.19.1 Agent Attachment 是机器责任，不是进程状态 — `可执行`（② concrete machine owner + ③单一 apply core + ⑤ generation/restart 回归）
- machine-wide durable Attachment owner 是本机 Agent placement generation、detach tombstone、Runtime lifecycle cursor 与 Runtime-set reconciliation 的唯一实现；它是 concrete internal dependency，不再保留只有单一 adapter 的宽假接口。调用方只消费 `attached|moved|detached|unchanged`，不得读 map 或自行比较 generation。Attachment 不代表 provider process 已启动；detach 不删除 Agent Root、Inbox 或 Message Draft。
- 所有正式操作固定 authenticated Workspace scope，event payload 不携带 `WorkspaceID`。Runtime reconciliation 必须提供该 Workspace 明确允许的 Runtime IDs，只能 detach，禁止猜测或静默 move。Attachment generation 与 per-Runtime lifecycle sequence 是两套身份；状态/tombstone 与 cursor 在一次 `Apply` 中原子持久化，失败共同回滚。
- Agent 本机 admission 只认 current Workspace Runner 接受的 `agent:start`；`agent:stop` 删除该 launch。不存在独立 Attachment registry、attach/detach wire、generation、replay cursor 或本地兼容 ledger。Message coordinator 可从同一 Runner 的 APM start snapshot 修复，不能读取第二套 ownership state。
- Reminder 只消费 registry 的 Workspace-scoped Attachment、Runtime residency 与 recovery cursor value snapshots，再维护自己的 timer/fence/projection state；Reminder cache cleanup 不能反向 detach Agent。缺少 Runtime→Workspace ownership proof 时不得退回 unscoped Agent lookup。
- restart-time Message Delivery 只能从固定 Runner scope 调用 `Resolve(workspaceID, agentID)` 重建 Inbox coordinator；Runtime 与 AgentRoot 必须来自该 Attachment 且 Runtime 仍属于同一 Workspace。detached 或 wrong-Workspace Agent 在 ACK 前拒绝，已有 coordinator 也必须通过 Runner→Runtime Workspace proof。
- **物**：`agent_attachment.go` 的 Workspace-scoped contract、`agent_attachment_registry.go` 的单一 concrete generation/tombstone/cursor/reconcile owner、`agent_attachment_daemon.go` 的 scoped Reminder projection、`message_runtime.go` 的 Workspace-scoped restart resolution，以及 registry/Reminder/Delivery restart 回归与旧 facade/假接口不存在门禁。

### 4.19.2 Workspace Runner identity 不随 Runtime 变化 — `可执行`（② narrow constructor + ③ Runner-owned Message/Activity/Attachment behavior + ⑤ executable ownership guards）
- `WorkspaceRunnerConfig` 固定 `DaemonID + DaemonInstanceID + WorkspaceID`；Runtime set 是可变输入，不进入 Runner identity。缺任一 identity 或 machine-wide dependency 时 constructor fail closed。
- Runner 本地构造并持有 Process Manager、Activity producer 与 `InboxRegistry`；durable Attachment、canonical Runtime pool、diagnostics 只注入同一 machine-wide owner 的引用，不复制状态、不另建 singleton。Credential Proxy 只解析唯一 Runner 并调用其 Message 方法，不取得 coordinator 或 registry。
- Process Manager 与 Activity producer 没有 Daemon-side Workspace map 或 lazy singleton；start/stop/probe/reconnect 都通过同一 Runner-owned instance。socket reconnect 只替换 Activity transport，保留 launch identity 与 Activity sequence；Binding teardown 才释放该 Runner 的 process/Activity/Inbox state。
- `WorkspaceRunner.Run(ctx)` 已唯一持有 Workspace auth、dial/reconnect backoff、ready identity、connection context/cancel、ping/pong 与 serialized frame writer；replacement 先 cancel/close 旧 connection。socket callback 是 private implementation，不引入 `RunnerTransport`。完整隔离决策见 [`ADR-0001`](adr/0001-workspace-runner-isolation.md)。
- 每个 live connection 自己持有 per-Agent Delivery dispatcher 与 current-socket Message callback：同 Agent FIFO、不同 Agent 可并行；callback 只在 exact connection 仍 current 时可写，replacement 后 stale callback fail closed。Daemon 只按 Workspace 找 current Runner，不再保存 transport/generation parallel maps。
- `InboxRegistry` 固定一个 Workspace scope；仅在该 scope 内 shared Attachment owner `Resolve` 成功且 Runtime ownership 匹配时创建 coordinator。Delivery、coverage 与 reconnect 都经过 current Runner 方法；网络 reconnect 保留 registry 和进程内 Context Boundary，Runner close 只关闭自己的 Inboxes。旧 machine-local 仅 Agent lookup 必须唯一，否则 fail closed，不能隐式选另一个 Workspace。
- **物**：`workspace_runner_state.go` 的 narrow constructor/connection ownership 与 current-connection fence；`workspace_runner_message.go`、`workspace_runner_activity.go`、`workspace_runner_attachment.go` 的行为归属；`workspace_runner_delivery.go` 只返回 current Runner、不暴露 coordinator；`workspace_runner_state_test.go` 的 whole-Daemon dependency 与 field reach-through source guard，以及 identity/reconnect/isolation/recovery 回归。

### 4.19.4 Computer host 按 Raft 对账 Binding execution — `可执行`（③ host reconcile + ⑤ crash/degrade/process identity tests）
- **口径（2026-08-18）**：Computer 与每个 managed Binding Runner 是独立 OS process。`internal/computer.Host` 每 5s 对账 desired Binding；crash 后 2s backoff；60s 内 3 次 crash 进入 degraded、不再自动拉起。摘掉 Binding 是 graceful stop，不是 unlinked/degraded。
- **process boundary**：每个 Binding child 持有自己的 `WorkspaceRunner + AgentProcessManager + Inbox/MessageCoordinator + Activity + provider runtimes`。Computer 通过 Unix socket / Windows named pipe 上的 length-prefixed JSON RPC 管理 child；Credential Proxy 仍是独立 loopback HTTP。Supervisor 在 spawn 时只登记 child handle/PID；`daemonInstanceId` 等 child Ready 自报后再记录。
- **process identity fence**：每次 resident 启动生成 UUID `serviceGeneration`。Host 不预发 runner 门票；Binding child 自己生成 `daemonInstanceId`，Ready 时按进程句柄 + PID 收下。之后 Host control、Runtime report、diagnostic、capacity grant 和 exit observation 校验 exact Workspace + `daemonInstanceId` + PID；stale process 必须 no-op / fail closed。当前 wire 与持久化 JSON 只使用 camelCase。
- **package boundary**：`internal/computer` 拥有 resident/health/control、desired set、supervision、crash/backoff、machine capacity、diagnostic aggregation 与 Machine Upgrade journal/stage/activation/successor attestation；`internal/daemon` 只拥有 child execution。Public resident 直接 `computer.NewHost → Host.RunProcess`，禁止构造或依赖 `daemon.Daemon`；反向也禁止 daemon 生产代码持有 `computer.Host`。CLI 只 wiring，不新增 `computerhost` 包。
- **Machine Upgrade**：Host prepare 所有 current children；每个 child drain/terminate 自己的 execution。任一 prepare 失败必须 release 已暂停 siblings；already-current / rollback re-register 也由 child-reported `daemonInstanceId`-fenced child 执行，Host 禁止 provider probing。
- **物**：`computer.Host` / `Host.RunProcess` / `hostMachineUpgrade` / `BindingSupervisor` / `HostControl` / `ProcessCapacity` / `BindingRunner`；真实 bootstrap/Ready/control process protocol；per-Binding child state root；双向 architecture guard；process-identity/crash/sibling/capacity/Machine Upgrade tests。

### 4.19.5 Machine Upgrade 以本机 successor attestation 收敛 — `可执行`（①无 cloud CAS + ⑤ local-proof tests）
- **口径（2026-08-18）**：incumbent 不得自己 spawn successor。独立 `computer __upgrade` coordinator 等旧 service/runners 死后才启动 target，并继续拥有该 child。successor 必须通过 framed IPC 返回 exact target version、target PID、`sourceServicePid`、non-empty `serviceGeneration` 和完整 managed Workspace set/revision；前任未死不得 ready。journal 由 coordinator 在外部 attestation 之后删除；`fromVersion` 只有 `accepted`/`rolling_back` 才可清 journal。公开 `computer start` 看见 pending handoff 必须拒绝。
- **物**：`machine-attestation` local RPC；`computer __upgrade`；包含 `phase` / `sourceServicePid` / `oldRunnerPids` / `acceptedManagedWorkspaceIds` / `acceptedManagedSetRevision` 的单一 handoff journal；`recoverSuccessor` 的前任死亡门与授权回滚恢复。无 loopback HTTP adapter 或 dual-write。

### 4.19.7 过期 Machine Upgrade journal 不得拦住新 PATH Computer — `可执行`（⑤ recovery 回归）
- 对齐 Raft：正在跑的 PATH 二进制就是 Computer。journal 的 source/target 都对不上当前版本时，那是上一次升级留下的诊断记录，启动必须继续，禁止 fail closed，也禁止回滚到旧 source。
- 当前版本仍是该 journal 的 source 或 target 时，按原相恢复：source 上续 accepted/staged，target 上续 successor，`rollback_pending` 只在这一对版本上才换回 `.prev`。
- **物**：`machineUpgradeJournalSupersededByRunningPath`；`recoverInterruptedMachineUpgrade`；`TestInterruptedMachineUpgradeRecoveryDoesNotBlockNewerPATHWithStaleRollbackPending`；`TestInterruptedMachineUpgradeRecoveryDoesNotReplayJournalSupersededByExplicitActivation`。

### 4.19.8 Computer 控制动作只走 current Workspace Runner — `可执行`（② capability + ③单一 carrier + ⑤正反向回归）
- 声明 `workspace_runner_control_plane_v1` 的 current Runner 是该 Computer-Workspace Binding 内 Runtime heartbeat 与 Server pending action 的唯一 carrier。Runner 每次按当前 Workspace Runtime set 发送，Server 每次重新验证 Workspace、Computer 与 generation；ack 只在同一 Runner socket、同一 Workspace Runtime scope 内消费。
- 当前 daemon 不启动 HTTP heartbeat loop，也不在 legacy runtime-multiplexed socket 上发送 heartbeat 或消费 heartbeat ack。旧 socket 只保留 task wake、Reminder、文件 RPC 与独立 `daemon:liveness_probe/ack`；liveness 不得调用 pending-action handler。Server 保留 legacy WS/HTTP adapter 仅用于 rolling-upgrade 中的旧 daemon。
- 控制 heartbeat 在 current Runner Ready 后立即启动，不等待额外 lifecycle replay。Server 对每个 heartbeat 逐个校验 Computer/Workspace ownership，不得要求当前 Runtime set 等于该 Computer 的全部历史 DB Runtime 行。Reminder reconnect 另按 current Runtime set 请求完整 snapshot，在线 mutation 直接发送 version-fenced upsert/cancel。reconnect replacement 先 fence 旧 socket，再由新 current Runner 恢复。多 Workspace 共用一台 Computer 时每个 Binding 独立发送和鉴权，禁止跨 Workspace 或跨 Computer 执行动作。
- Agent Restart 不进入 control heartbeat pending action；它只由 4.15 的进程内 orchestrator 经 current Workspace Runner 投递离散 `agent:stop/reset-workspace/start`。heartbeat 不得恢复 lifecycle envelope 或本机复合 executor，否则会与离散状态机形成两条控制路径。
- **物**：`DaemonCapabilityWorkspaceRunnerControlPlane`；`WorkspaceRunner.runControlPlaneHeartbeats`；`HandleDaemonWSHeartbeat` 的 Workspace/Computer/generation fence；`TestWorkspaceRunnerOwnsCurrentControlPlaneHeartbeat`、`TestWorkspaceRunnerControlHeartbeatUsesExactWorkspaceRuntimeSet`、`TestLegacyRuntimeWakeSocketDoesNotExecuteControlAcknowledgements`、`TestLivenessProbeAcknowledgesWithoutInvokingHeartbeatActions`、`TestWorkspaceRunnerHeartbeatRejectsRuntimeAssignedToAnotherComputer`；现场验收矩阵见 [`docs/daemon-control-plane-validation.md`](daemon-control-plane-validation.md)。

### 4.19.6 Machine Upgrade 后继校对允许退役 provider — `可执行`（⑤ attest 回归）
- Workspace 连接集合仍必须与 accept 快照完全一致。
- Runtime 校对要求后继覆盖**当前仍在产品目录里**的 accepted runtime。目录里已经没有的 provider（例如删掉的 Antigravity）缺号算退役，不能把 Computer 卡在启动失败。
- 后继多出来的 runtime（新 provider）允许。禁止手改 `accepted_runtime_ids` 来过关。
- **物**：`agent.MissingRequiredRuntimeIDs`；`attestComputerMachineUpgrade`；`PostgresMachineUpgradeStore.AttestComputer`；`TestMissingRequiredRuntimeIDsRetiresUnknownCatalog`；`TestAttestComputerMachineUpgradeAllowsRetiredProviderGap`；`TestMachineUpgrade_ComputerAttestationAllowsRetiredProviderGap`。

### 4.20 云端电脑 Docker 宿主机名称必须携带可追踪上下文 — `可执行`（②payload 合同 + ⑤单测；owner: @Codex）
- 通过 Computers → Cloud computer 创建 Docker 容器时，传给宿主机 `docker run --name` 的名称不是 Multica UI 展示名。服务端必须生成并下发 `multica-<部署服务端>-<workspace>-<username>-<container>` 形状的宿主机容器名，所有段需清洗成 Docker 安全 ASCII；UI 展示名只作为 `<container>` 输入。
- **物**：`CreateSandboxInstance` 写入 `metadata.docker_container_name` / job `docker_container_name`；`sandboxd` 使用该字段作为 `--name`，旧 payload 只回退到 instance-id 名称。

### 4.21 Workspace 招聘能力只属于结构化绑定的 Onboarding Agent — `待执行`
- 每个已配置 Workspace 只有一个 `workspace.onboarding_agent_id`；Wendy 只是默认显示名，改名不影响权限，同名不获得权限。普通 Agent 不注入招聘 skill/instructions，并在 Agent API 边界 fail closed。
- Workspace 先创建；唯一 Owner 连接 Computer/Runtime、选择 Runtime/Model 后显式创建 Onboarding Agent。它与普通 Agent 共用同一创建事务原语，`owner_id` 是点击创建的 Owner；结构化 binding、核心招聘 skill 和版本化欢迎消息在同一事务追加。
- Owner/Admin 可编辑 Wendy 配置；只有唯一 Owner 可首次 Setup、归档或恢复。归档保留 binding、立即停用招聘、不自动补建。
- `agent:create` Hiring Proposal 由 Wendy 准备，由 Owner/Admin 最终提交；卡片消费与 Agent 创建同事务且仅能成功一次。structured card 存在时当前 UI 不显示协议 fallback 文本。
- Agent 创建时 `name` 必填，使用 1–32 位小写字母、数字或连字符，并在创建后保持不变；`display_name` 创建时可省略，默认显示 `name`，后续只编辑 `display_name`。Wendy 的 Proposal `name` 与创建弹窗使用同一契约。
- 完整契约、迁移矩阵与负向控制见 `docs/superpowers/specs/2026-08-06-workspace-onboarding-agent-boundary.md` 与 ADR-0012。

### 4.22 Notes Editor 与 Worker 必须分合同 — `可执行`（②分表/分 endpoint + ⑤误用拒绝测；owner: 本 Slice）
- 改页 = **Editor**（`note_ai_job` / `POST .../ai-jobs`）；以笔记为 brief 做平台工作 = **Worker**（`note_worker_job` / `POST .../worker-jobs`）。禁止给 Editor 加「顺便建 Issue / 跑 Agent」副作用，也禁止 Worker 走 `replace_page` 改正文。
- 误用 fail closed：Worker 字段/`intent:"worker"` 打到 Editor → 400；Editor 的 `prompt`/`action` 打到 Worker → 400。
- Worker 派发把 `note_brief={version,page_id,title}` 写入 `agent_inbox_event.context`；prompt 三分区 `<system_contract>` / untrusted `<note>` / `<instruction>`，title/body 转义 `<`/`>`，instruction 防 `</instruction>` 截断（S2-C4）。
- 待审写回（`note_page_writeback`）是第三条管道（D1），不是第三种 job。
- **订阅策略（S3-W1）**：「有关联即订阅」——`note_page_issue_ref` 行即隐式订阅；无独立订阅表。
- **事件白名单（S3-W2）**：仅 issue 状态新进入 `done` / `cancelled` 时出提案；进行中/阻塞/标题编辑/普通评论为零提案。
- **与 Agent Daily 并行（S3-W3）**：产品笔记/待审写回与 agent 私有 `memory/daily/` 两套存储并行、可互链、禁止合并；交叉声明见合同文档 §「Product note writeback ≠ Agent Daily」与 `docs/agent-memory-model.md` §10。
- **聊天提案写笔记（Messages）**：人点按钮才用点击者 ACL 写入。出按钮的条件：Agent 发了 `--note-write`，**或**人上一条在要求插入/写入笔记（含「给我按钮」）且这条回复像待写入正文（不是一句「好的」）。禁止把本地 `notes/*.md` 当成产品笔记。省略 `--note-page-id` → 「新建笔记」；有 UUID / `/notes/<uuid>` / sticky `note_brief` → 「插入笔记下方 / 新建子笔记」。
- **Notes 页助手泡泡**：选中笔记时右下角 FAB 打开绑定 `chat_session.context_note_page_id` 的 standalone 对话（非 Worker→Messages）。工作区级专用 Agent「笔记助手」(`notes-assistant`)；首开 soft-probe 不自动创建，对话里出引导卡，仅点「对齐 Wendy」或「自己选择电脑和运行时」（预填身份的创建智能体对话框）才 Ensure。删除后再开同样出卡。气泡内无 Agent 下拉。桌面右边栏可拖左缘调宽（280–640，`noteBubbleSidebarWidth` 一份 store，正文留白与栏同步变窄/变宽）；打开时 Notes 正文行按栏宽留白（`noteAssistantSidebarReservePx`），编辑器 `mx-auto` 在未覆盖区域居中。扫过泡泡消息：复制后是「插入笔记下面 / 插入子笔记」（写入当前页或子页）。关闭且未进过场时栏不进 DOM（`noteAssistantSidebarPresence` omit）。禁止用 `motion.div` + `initial={false}` 藏关闭态——Motion 会写 `transform: none`，刷新后栏会露在 `right: 0`。Delivery 前缀 `<note_chat_context>` 仅含根页 id/标题，子页正文由 Agent 按需 `notes tree` / `notes get`（技能 `multica-notes-assistant`）。合同见 `docs/notes-editor-worker-contract.md` § Notes assistant bubble。
- **物**：`docs/notes-editor-worker-contract.md`；`docs/agent-memory-model.md` §10；migration `338_note_worker_job` / `420_chat_session_context_note_page`；`note_intent.go` / `note_brief.go` / `note_worker_prompt.go` / `note_writeback_events.go` / `appendAgentNoteWritePart` + 误用、dispatch/ACL、prompt breakout、白名单、`--note-write` 测；`note_chat_context.go` / `ListAgentNoteTree` / Notes FAB。

### 4.24 写汇报：多 runtime 采集 + 笔记助手合成 — `仅文档`

- **口径（2026-08-17，ADR 0019；采集深度 2026-08-18 增补；结算/重采 2026-08-18；汇报叙事 2026-08-18；fresh session / 失败后仍收包 2026-08-18；无 stub 采集包 2026-08-19；Brief 板块 Summary/Technique/Achievements/Research 2026-08-19；严格时间窗 + 自定义起止 2026-08-19；Brief 无证据层/给别人看 2026-08-19；按事项归组非按时间 2026-08-19；采集端 Work groups 初分 2026-08-19；采集包隐式存放 2026-08-19；仅自己电脑 2026-08-19；可选 focus 由笔记助手先派发 2026-08-21；泡泡内点选+输入、文字优先、不跳消息页 2026-08-21；芯片一轮即失效、进度写进当前页会话、确认后插到下发页的子笔记 2026-08-21；空会话开芯片输入框不顶上去、⊕ 新建会话发写汇报不复用旧会话 2026-08-21；芯片卡可取消、发送后取消失效 2026-08-21；采集包/汇报稿折叠展示 + 插入笔记下面/插入子笔记 2026-08-21；笔记助手桌面改为右侧通顶边栏 2026-08-21；泡泡成稿只取本轮 note_write/底稿、禁止上一轮工作介绍/ 成稿 2026-08-21；采集员不静默创建、对话卡+锁定电脑选运行时 2026-08-21）**：入口是笔记助手卫星「写汇报」，或在泡泡里说出写汇报意图（`looksLikePeriodBriefRequest`）——后者先弹出同一套时间/电脑芯片，再点发送才开跑；**不再**用页头按钮或独立对话框。缺/删/绑错的采集员在芯片列表里出可忽略提醒（电脑锁定，只选运行时）；可忽略后继续用其余电脑。`EnsurePeriodBriefCollectors` 无 `runtime_id` 只探测不创建。条件芯片（时间段 + 自己电脑）只作用于 **发出去的那一轮**，与可选文字合成一条普通用户消息后芯片收起；**任务结束前锁输入**；结束后才恢复普通对话。进度（先让谁采、已分派、收到谁的包、材料齐了、问要不要插入）写进 **当前气泡会话**（有 `chat_session_id` 则沿用；⊕ 新建未带 id 则开新会话，禁止复用该笔记最近一条），关掉再开仍在。空会话弹出芯片时中间留白，输入框仍在底部。收到采集包和成稿后在泡泡里用折叠 `note_brief` 展示（采集包不是笔记页）。成稿提供 **插入笔记下面**（接到下发页正文后）和 **插入子笔记**（下发页的子页），不再落到全局「工作介绍/」。人勾选若干 **自己创建的 Computer** 上的采集员（含自己的云端 runtime；**不得**采他人电脑，即使 runtime 可见/`public` 或本人是 workspace admin）。人可附带可选 **采集要求**（路径/主题/方面，也可在文字里改时间/电脑）；**留空则全量采集（与现在相同）**。有要求时 **笔记助手先 collect-plan wake**（`submit-collect-plan`）理解需求并只派给所需采集员，再各自在 **所在 OS** 按范围搜集最近工作痕迹（`SCAN_ROOTS`：整机 HOME ∪ 沙箱 `/workspace` ∪ 可见项目目录，禁止只扫 HOME；允许短 diff、文件摘要、关键片段、窗口内非 git 源文件；denylist 去密钥噪声）。采集员可见该机最全信息，应做 **初步分类（`## Work groups`）**：同工程/同 git 仓默认一组；跨仓/跨文件但同一事项结果的合并为一组并写明 why；不相关工作分列——**完整性优先**，证据层（Repos/Highlights）不可被分组/图替代。采集包经 `submit-pack` 写入 `note_period_brief_run.collectors[].pack_markdown`（**不**建「采集包」笔记页）；**已 submit-pack 的包仍算 ready**，即使随后 inbox 任务记 `failed`；合成 wake 后 **清空** pack_markdown。平台 Facts 仍由服务端确定性拉取。平台 **按状态等到采集 settle**（不得用固定时钟把仍在跑当成 empty）；未交付 / 失败时状态板写明 **「调用采集 Agent 失败了」**，合成 **不得** 把空包当证据。采集 / 重采 / 合成每次 wake **`force_fresh_session=true`**（一次性 prompt，禁止续上一次 Pi 会话）。合成 wake 带 **状态板**（`retryable` / `abandon_why`）。永久失败（无 API key / 模型配置 / 鉴权 / 配额等）放弃；可恢复失败由笔记助手经窄工具重采（每采集员最多 3 次）。全部交给 Workspace 内 **笔记助手**（写汇报 wake，不可改选）做 Period Work Synthesis，产物是 Notes 里的 Period Work Brief。采集/合成 **严格按所选时间窗**（日/周/月或自定义起止，半开区间）；**汇报叙事**：固定板块 `Summary`（必有 `Work Summary` 详述本期工作 + `Next Steps` 可据现状推测）以及按需的 `Technique` / `Achievements` / `Research`（无相关工作则整节省略、禁止空壳）；Work Summary **从采集 `## Work groups` 展开**（一组一主标题，组内再细分；跨采集员仅在同一事项身份时合并；不因时间穿插拆开；不相关工作不因同窗并句）；标题与正文给人看、禁止路径/目录当标题；**禁止证据层**（哈希/diff/snippet/「证据」——Facts/采集包仅作私有素材）；ready 采集包里利于理解的 Mermaid **必须**带入 Brief。**废弃 Host Digest 作为 Brief 的本机源。** 不导出 PPT。
- **仍禁止**：键鼠、截屏、剪贴板、浏览器历史、全仓灌模型、Daily 当源、密钥与 runtime 诊断、回顾 API 跑模型、静默 `replace_page`。
- **指针**：ADR `docs/adr/0019-runtime-agent-collectors-period-brief.md`（取代 0018 Host Digest 路径）；skill `multica-period-work-collect` / `multica-period-work-plan` / `multica-period-work-brief`；术语 `CONTEXT.md` → Period Work。
- **物（可执行回归）**：`TestBuildNotePeriodBriefCollectorPromptEscapesWindowAndForbidsBrief`（`/workspace` + `SCAN_ROOTS`）；`awaitPeriodBriefCollectorPacks` + `classifyPeriodBriefCollectorOutcome`；`formatNotePeriodBriefPacks`（失败文案、无 stub body）；`POST /api/agent/notes/period-briefs/{draft}/submit-pack`；`POST .../submit-collect-plan`；`POST .../retry-collectors`；`multica notes period-brief submit-pack|submit-collect-plan|retry-collectors`；migration `414_note_period_brief_run` / `432_note_period_brief_collect_plan` / `435_note_period_brief_bubble` / `436_note_period_brief_prompt`；`TestCreateNotePeriodBriefPostsBubbleTranscriptAndInsertsUnderSourcePage`；`TestEnsurePeriodBriefBubbleSessionOmittingIDCreatesNewThread`；`TestPeriodBriefPackAndAppendInsert`；`TestPeriodBriefBubbleResultIgnoresPriorFolderWrite`；`applyNotePeriodBriefCollectPlan`；`notePeriodBriefInstruction`（给人看/无证据层 + Summary 板块 + Work groups 展开） / `TestNotePeriodBriefInstructionRequiresReportingShape`；`EnsurePeriodBriefAgent` archives leftover `weekly-report` and returns 笔记助手；`refreshNotesAssistantInstructionsIfStale`（`Period Brief collect-plan wake`）；`persistPeriodBriefNoteBriefContext` `force_fresh_session=true`；`TestCreateNotePeriodBriefIncludesReadyCollectorPack`；`TestCreateNotePeriodBriefHarvestsNoteWriteAfterFailedTask`；`TestCreateNotePeriodBriefEmptyFocusSkipsPlanner`；`TestCreateNotePeriodBriefFocusHonorsCollectPlanSkip`；`TestFormatNotePeriodBriefPacksFailedOmitsBody`；`TestEnsurePeriodBriefCollectors_SkipsOthersPublicComputers`；`TestEnsurePeriodBriefCollectors_RebindsWrongComputer`；`TestCreateNotePeriodBriefRejectsCollectorOnOthersComputer`；daemon `applyForceFreshSession` + `classifyPoisonedError` Pi `input[n].status`。

### 4.23 Context compaction 是可见 Activity，不是 Message acceptance 或进程生命周期 — `可执行`（②统一 lifecycle event + ③单一 gate/投影 + ⑤状态机回归；owner: @Codex）
- Provider 原生事件先归一成 `MessageCompactionStarted` / `MessageCompactionFinished`；resident runtime 的主动压缩必须在独立 `ResidentMessagePreparation` gate 完成，不能共享 20 秒 native Message acceptance timeout，也不能把压缩超时解释成进程重启。
- Activity 必须按 Raft 阶段投影：开始写一次 `Working/compacting_context`，显式或推断完成写一次 `Working/compaction_finished`，5 分钟未见完成只写一次 `Working/compaction_stale`；之后每分钟 heartbeat 只更新 Snapshot、不追加 Timeline。只有 provider turn 完成才投影 `Online/idle`。
- `thinking`、`text`、`tool_use` 与无错误 turn end 可推断遗漏的 finish；runtime error/失败 preparation 中断 active compaction 并向被阻塞的 Message turn 传播。compaction active 时 busy Notice 继续留在 Pending/retry，不得跨上下文重写边界注入。
- **物**：`ResidentMessagePreparation`、`agentActivityCompactionState`、`TestRuntimePoolPreparesResidentInputOutsideNativeAcceptanceTimeout`、`TestResidentCompactionPublishesOneStaleEntryAndFinishesBeforeResumedOutput`、`TestRuntimePoolDefersBusyNoticeAcrossCompactionBoundary`。

### 4.19 活进程只复用，换进程必须显式 stop — `可执行`（⑤ pool/capability 回归）
- Raft 两条：槽里有活进程 → 复用，不换 launch、不报新的 active；要换进程 → 显式 restart/reset，或等进程死后再 start。禁止 acquire 时比对 model/MCP/AGENTS 哈希并隐式 stop+start。
- 产品只注册有 resident adapter 的 runtime。one-shot 寿命不再进入同一槽，因此槽上没有 mode 字段。Grok Build 的 provider id / CLI 是 `grok`。
- 换 model 要立刻生效，走与 Restart 按钮同一条显式 restart。人没点 restart 时，旧进程可以带着旧 bake-in 再跑一轮——接受，与 Raft rebind 同代价。
- **物**：`canonicalAgentRuntimePool.acquire`；`TestCanonicalAgentRuntimePoolReusesLiveProcessEvenIfFactoryWouldFail`；`TestAllKnownProvidersAreCanonicalResident`。权威判断见 `docs/adr/0011-resident-process-reuse-no-hash-restart.md`。

---

### 4.15 Agent 临时协作空间必须保留人类所有权与幂等来源 — `可执行`（①数据库约束 + ③Agent 专用入口 + ⑤合同测试；owner: @Codex）
- Agent 可为顺序、并行、讨论收敛和分阶段工作创建临时协作群，但不能借此成为频道 owner、选择任意人类观察者或调用 human route。人类 owner 只能从当前有效执行的发起人事实推导；创建 Agent 必须是成员，其他成员只能是同 workspace 的 live Agent。
- 创建请求必须携带由调用者稳定生成的 `client_request_id`；同 Agent、同 key、同请求返回原群，不同请求复用 key 必须冲突。重试不得留下重复频道、无 owner 频道、半套成员或孤儿 onboarding。
- 游戏仅是验收面，存储、API、CLI 与内置技能只表达通用 coordination；不得出现狼人、法官、卧底等产品模型。
- **物**：migration 255 的 provenance/temporary/idempotency 约束；`POST /api/agent/channels`；`multica channel create`；`multica-multi-agent-coordination` 技能及 source map；通用设计见 `docs/superpowers/specs/2026-07-31-general-multi-agent-coordination-design.md`。

### 4.19 Device-code CLI login speaks RFC 8628 at the HTTP boundary — `可执行`（⑤合同测试）
- Official public `client_id` is `multica-cli`. Missing `client_id` is `invalid_request`; unknown is `invalid_client`. There is no OAuth client registry.
- `POST /api/device/code` and `POST /api/device/token` accept only `application/x-www-form-urlencoded`. Token polls must send `grant_type=urn:ietf:params:oauth:grant-type:device_code`. There is no JSON `{client_hint}` start or `{token, expires_in_days}` success body.
- Token success is RFC 6749 `{access_token, token_type=Bearer, expires_in}` seconds. `access_token` is the existing user PAT (`mul_…`), still single-claim.
- `/device` must accept a typed `user_code`. Arriving via `verification_uri_complete` must display the code and require a match confirmation before approve/deny.
- **物**：`server/internal/handler/device_auth.go` + `device_auth_test.go`；`server/cmd/multica/cmd_auth.go` + `cmd_device_login_test.go`；`packages/views/device/device-confirm-page.tsx` + test.

### 4.24 Graph Memory 是独立 project/channel 图，不是 legacy 无损替代 — `仅文档`（实现门槛尚未落地）
- Graph 模式只替代 project/channel/daily；user 与 agent memory 继续使用 legacy 文件。Graph miss、故障或空库不得回退 legacy project/channel/daily。首次启用从空图开始，不迁移或 backfill 旧文件。
- 每个 `(workspace_id, project_id)` 一个由获授权参与 Agent 共享的物理图；最初未绑定项目的 Channel 使用永久 standalone 图。Project-bound Channel 改绑后保留 channel-only lineage，旧 Project 的 project-visible 节点绝不可经该 Channel 泄漏。
- Graph 数据面由 Server 统一拥有；daemon 不持有本地图真相。Profile 参数必须进入运行时；Graph writer 验收前 job 保持 inert，验收后才移除第二环境开关；Graph Workspace 不运行现有 legacy L1–L4/self-review/team-curation pipeline。
- Graph 保持 Experimental，且任何用户 Workspace（含 Experimental）启用前必须先通过 P0。P0/P1、错误语义、daily、治理 API、UI 和验收矩阵见 [`docs/superpowers/specs/2026-08-17-graph-memory-scope-design.md`](superpowers/specs/2026-08-17-graph-memory-scope-design.md)。实现与见红测试落地前不得标 `可执行`，也不得把 Graph 设为默认。

维护人：Parker（产品）。规矩变更走 PR；`可执行` 升降档需 owner 签字。
