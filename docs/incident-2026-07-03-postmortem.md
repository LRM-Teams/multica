# 事故复盘 — 2026-07-03 s89 两起 P0 + 一次虚惊

- 作者: Parker (Product Manager) · 参与: Frank, Miles(PM), Barry(BE/ops), Felix(FE), Iris(UX), Cindy(HR)
- 状态: 已闭环（s89 恢复 + #211 GitHub 单源部署验证通过，sha-26a7da6）
- 关联任务: #206(containment) · #207(send 幂等) · #194(ambient+队列) · #210(workspace UX) · #211(密码单源 ✅) · #208(登录) · #212(招后端)

## 1. 影响
- s89 服务两次不可用（队列风暴打挂 + 部署 crash-loop），累计约 1.5 小时受影响。
- **无数据丢失、无用户/凭据被改**。含 16,951 条重复消息、270,549 条 queued 任务是脏数据/积压，非丢失。

## 2. 时间线（CST）
- **16:40–16:50** Iris 做 #193 s89 滚动终审，向「内测」（16 个 agent 的活跃群）发测试 marker。她只做了约 2 次显式发送，但前端 send 层 bug 把它放大成 **16,951 条**同一消息落库。
- 每条非@消息 → ambient **无脑扇给全部 16 个 agent** → 每个入队一个 run；`agent_task_queue`（Postgres 表）**无入队背压** → 排到 **270,549** 行；单 daemon 拼命 drain → 压垮 DB/服务。
- **~17:28** Frank 报服务挂。**~17:5x** 止血：移出「内测」16 个 agent 成员 + 取消该频道 queued/running（可逆、限内测）→ 队列 27w→0，backend 恢复。
- **~18:10** Frank 报"群聊数据全没了"（截图 Messages 全空）→ **虚惊**：UI 落在空 workspace `111`，真实数据在 `LRM-team`（内测 17,885 条消息 + 成员都在）。空态只显示 "No channels yet"，分不清"空工作区 vs 数据丢"，放大了惊吓。
- **~18:35** "又挂了" → 部署 crash-loop：s89 `/data/multica/.env` 的 DB 密码 ↔ Postgres role 实际密码漂移，每次部署用错密码重建 backend；一个 PR-merge 自动部署持续复发。止血：冻结 Deploy workflow。
- **~19:20** #211 完成：改为 GitHub Environment secret 注入做单一真值源，fresh workflow_dispatch 验证通过（sha-26a7da6, `/readyz db=ok/migrations=ok`）。闭环。

## 3. 根因（分清，别误诊）
**放大链 = ① × ② × ③，缺一不成灾：**
1. **前端 send 层无幂等/无 in-flight guard**：`useSendChannelMessage` 等是裸 mutation，`handleSend` 不 guard `isPending`；高频 re-render 下同一 pending 被非显式路径反复重发 → 2 次点击变 16,951 条。
2. **ambient 无脑全量扇出**：每条非@人类消息扇给频道全部 agent，无 per-message/per-channel 上限、无 relevance pre-gate。
3. **队列无背压**：`agent_task_queue` 是 Postgres 表，只有执行并发上限（max_concurrent_tasks），**无入队深度上限/去重** → 无界膨胀。

**要澄清的两个误诊：**
- **不是"agent 无限互相回复"**：loop guard 本来就有（`channelRunTriggerLimit=10`），且 ambient 已跳过 agent 消息（`type=="agent" return`）。别去"加 loop guard"（已存在）。
- **不是数据丢失**：是 workspace 选择 + 空态歧义。

**部署事故根因（独立）**：DB 密码有两处（server `.env` / Postgres role）会漂移；Barry 只手动修了运行容器，每次新部署从漂移的 `.env` 重建 → crash-loop。

## 4. 促成因素
- **事故期间自动部署未冻结**：迁移/部署撞上系统脆弱高压窗口。
- **可观测性缺失放大处理成本**：storm 时 SSH/API/DB read 都 timeout，连取证都做不了 → 只能盲操作、等授权。
- **组织单点**：Atlas（CTO）+ Dana（DB）当天离职 → 后端/ops 只剩 Barry 一人扛，且每个可逆止血动作都卡在等 Frank 一人授权（~10 分钟）。

## 5. 纠正措施（已建任务）
| # | 措施 | Owner | 说明 |
|---|---|---|---|
| #207 | send/reply 幂等 | Felix(FE)+Barry(BE) | client idempotency key + server 去重(conversation,sender,key) + in-flight guard + debounce；并进 #188 §4 动作合约 |
| #194 | ambient 扇出上限 + **队列改造** | Barry | ambient 不再物化成任务行,改**合并唤醒**(pending_wake + per-member cursor,复用 #197);direct 进 durable queue;+relevance pre-gate + 熔断(频道 wake 超阈值自动暂停 ambient) |
| #210 | workspace 选择/空态 UX | Felix+Iris | 默认落上次非空 workspace;空态区分"空工作区 vs 数据丢"+一键切回 |
| #211 ✅ | DB 密码单源部署 | Barry+Felix | GitHub Environment secret 注入,URL-safe,不进日志/PR,fresh deploy 验证 |
| #208 | 登录 | Barry | 888888 是 6 个 8(此前打成 5 个 8) |
| #212 | 补后端工程师(agent,偏 ops/infra) | Cindy(HR) | 解组织单点;首任务 = #211/#194 收尾 |

## 6. 常设流程（新增规则）
1. **事故期冻结自动部署 + 暂停 merge**，直到闭环。
2. **Validation 纪律**：绝不往有 agent/ambient 的频道发测试消息;用无-agent 频道 / human↔human DM / 只读 DOM 探针。
3. **凭据单源 + hygiene**：密码/密钥单一真值源(GitHub Environment);URL-safe;绝不进 chat/log/PR/CI 输出。
4. **P0 可逆止血预授权 runbook**：可逆、限范围的止血动作(如频道级 ambient-off/移成员/清队列)应有预授权,别让服务在等一个人点头时持续降级。
5. **系统不该"猜"出无界负载**：凡系统自动产生(ambient)的东西必须有背压/上限,作为不变量。
6. **事故修复类收口必须含一次"事故形状"live 复现验证**（2026-07-04 #223 收口补）：单测 mock 与顺手数据会天然掩盖缺陷——#223 两例：route 单测 mock `https://app.test` 隐藏了 proxied callback-origin 反射（unit 绿、live 空白页）；测试账号 default==last-active 使修复"不可分辨"，差点掩盖邮箱登录路径漏接。收口证据 = 构造与事故同构的数据形状 + 真实环境走查；**unit 绿 ≠ 修好**。auth/routing 类改动默认适用。

## 7. 经验教训
- **先查最朴素解释**（workspace 选错、密码打错）再跳复杂根因——我一度过度假设 migration 144 回填失败,实际是选错工作区。
- **已有防护要核实再下结论**：loop guard 存在,失败是"放大"不是"循环"。
- **可观测性是止血前提**：#188 §6.2 的 Activity/context_pack + #194 的熔断可观测,加带外 kill-switch/健康面,让下次能"看得见、断得掉"。
