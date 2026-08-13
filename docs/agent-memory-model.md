# Multica Agent 记忆模型

> 状态：目标行为已实现在 [PR #697](https://github.com/LRM-Teams/multica/pull/697) 和 [PR #698](https://github.com/LRM-Teams/multica/pull/698)。本文描述两项 PR 合并到 `dev` 后的完整行为。
>
> 最后核对：2026-07-21。

## 1. 一句话模型

Multica 的记忆分成两层：

- **agent 私有文件**负责让当前 agent 立即、持久地记住；
- **平台数据库**负责经过审核的跨 agent / 跨 runtime 共享、适用范围、去重和过期治理。

群消息本身也会写入数据库中的 agent inbox，但那是**消息投递队列**，不是团队共享记忆。不要为了给群内 agent 再投递一次消息而创建团队记忆候选。

## 2. 每个 agent 都有隔离的记忆根目录

每个 Multica agent 的持久根目录为：

```text
~/.multica/workspaces/<workspace_id>/agents/<agent_id>/
```

不同 agent 的目录互相隔离。Pi、Codex 等 provider 只是运行同一个 Multica agent 的不同执行工具；只要 Multica agent ID 相同，它们就使用同一套 agent 记忆和平台筛选后的记忆快照。provider 自己的全局目录（例如 `~/.codex/memories`）不属于 Multica agent 记忆。

目录职责如下：

| 内容 | 文件 |
|---|---|
| 当前 agent 跨项目都适用的稳定知识、习惯和规则 | `memory/MEMORY.md` |
| 当前 agent 跨项目的临时状态 | `memory/STATE.md` |
| 不确定、冲突、敏感或等待确认的内容 | `memory/REVIEW.md` |
| 当天活动和整理记录 | `memory/daily/YYYY-MM-DD.md` |
| 某个成员的偏好和个人资料 | `users/<member-id>/USER.md` |
| 当前 agent 与某个成员的关系信息 | `users/<member-id>/RELATIONSHIP.md` |
| 某个项目的长期知识 | `projects/<project-id>/MEMORY.md` |
| 某个项目的当前状态和阻塞 | `projects/<project-id>/STATE.md` |
| 某个项目已经做出的决定 | `projects/<project-id>/DECISIONS.md` |
| 某个群的非敏感协作约定 | `channels/<channel-id>/CONTEXT.md` |
| 等待平台审核或共享的候选 | `sync_queue/memory-candidates.jsonl` |

## 3. 写入时先判断“对谁生效”

说话人只代表来源，不自动决定记忆的作用域。系统要根据内容本身判断它约束的是某个成员、某个 agent、某个项目、某个群，还是整个 workspace。

例如安东说“干活前先反馈一下”：

| 实际表达 | 写入位置 | 适用范围 |
|---|---|---|
| “以后你给我干活前先反馈” | `users/<andong-member-id>/USER.md` | 当前 agent 服务安东时 |
| “agent A 以后干活前都先反馈” | agent A 的 `memory/MEMORY.md` | agent A 的所有工作场景 |
| “这个项目以后都先跑测试” | 当前项目的 `MEMORY.md` 或 `DECISIONS.md` | 当前项目 |
| “这个群统一用中文” | 当前群的 `CONTEXT.md` | 当前群 |

明确的“记住”“写下来”是直接写入请求。只回复“收到”不算记住；对应的持久文件写入必须成功。

### 3.1 常用写入判断

1. 明确的个人偏好、工作习惯或协作默认，立即写当前成员的 `USER.md`。真人干活时的指令默认高信号；只有明显是测试/打招呼/玩笑才跳过。
2. **队友 agent 说的话也能记**：站位规则、对接/交接、可复用修法/清单、纠正、或明确「记住」——同轮写入正式文件，不要因为说话人是 agent 就当闲聊丢掉。协作默认写 `notes/agents.md` / `notes/relationship-map.md`；项目运维写项目目录；跨项目 agent 规则写 `memory/MEMORY.md`。纯打招呼 / 谢谢 / LGTM / 空 ack 不写。不要复制对方的私有用户文件或秘密。
3. 与某个成员的稳定协作关系，以及干活过程中出现的对接/交接/归属安排（谁跟谁对接、谁负责啥），写该成员的 `RELATIONSHIP.md`；与队友 agent 的关系写 `notes/agents.md` / `notes/relationship-map.md`。不要只在欢迎入群时才写。纯社交打招呼、没有可复用事实时不写。
4. 只要求当前 agent 永久记住、且跨项目适用的安全规则，写当前 agent 的 `memory/MEMORY.md`。
5. 项目知识、状态和决定，写当前项目目录。
6. 群目的、语言、路由和协作默认值，写当前群的 `CONTEXT.md`。
7. 当前事件、配额、阻塞和会过期的状态，写对应 `STATE.md`，并记录日期、状态和可用的 TTL / 到期时间。
8. 不确定、冲突、敏感或不知道应该放在哪里的内容，先写 `memory/REVIEW.md`。
9. 猜测、一次性执行噪音、原始聊天记录、秘密以及只对当前回复有用的内容，不写入长期记忆。
10. 实质收工（改代码、定方案、推进 issue、非琐碎排查）时，给当天 `memory/daily/YYYY-MM-DD.md` 追加 **1–3 条**短索引；纯打招呼 / 贴纸 / 测试废话不写 Daily。偏好和对接仍热路径直写正式文件，不要只堆在 Daily。
11. **处理完可复用的问题后，agent 自己记住**：根因/修法/命令若下次还会用到，收工同轮写入项目 `MEMORY.md`/`DECISIONS.md` 或 agent `memory/MEMORY.md`/`notes/*`，不要等任何人再说「记住」；一次性噪音只留 Daily。是否可复用由 agent 判断。

#### 凝练预算

- Daily 是事件索引，不是工作报告：每次实质收工最多 3 条，每条最多 240 个字符，只保留结果、耐久决定、下一风险或动作。
- 正式记忆一条只写一个稳定事实或规则，每条最多 180 个字符；同主题优先合并或替换，不追加近似副本。
- 步骤、命令输出、文件清单、聊天复述和证据原文留在源任务；记忆只保留必要的短引用。
- 文件软上限：Daily、agent `MEMORY/STATE`、`USER`、project `STATE` 为 2 KiB；`RELATIONSHIP` 为 1 KiB；project `MEMORY` 为 4 KiB、`DECISIONS` 为 3 KiB、channel `CONTEXT` 为 1.5 KiB；冲突出口 `REVIEW` 为 8 KiB。
- self-review 改写后必须满足单条和文件上限；失败时整次本地改动回滚。压缩靠去重、合并和引用来源，不能直接截断。

### 3.2 热路径 vs 冷路径（避免短任务卡顿）

| 路径 | 谁做 | 何时 | 写什么 | 不要做什么 |
|---|---|---|---|---|
| **热路径** | 当前任务里的 agent | 干活过程中 / 实质收工 | 偏好→`USER.md`；对接→`RELATIONSHIP.md`/`notes`；队友 agent 耐久教训同写；可复用排障→项目/`MEMORY.md`/`notes`；实质流水→append Daily | 每轮聊天都做全文自审再决定写不写 |
| **冷路径** | L1 Daily / L2 自审 / Team Curator | 后台、按 active agent 定时/轮次 | 补齐 Daily、提升稳定事实、去重合并共享候选 | 挡在用户回消息的延迟路径上 |

一句话：**自己轻量记；自审+Curator 负责整理。**

### 3.3 同轮 memory signal + 漏写兜底（不另跑模型）

主写入路径仍然是 agent 直接改本地 Markdown。平台**不**在每条消息后再同步调用小模型。

可选增强：

1. Agent 写完 `USER.md` / `RELATIONSHIP.md` 等后，可向 `sync_queue/memory-signal.jsonl` 追加一行短信号，例如：
   `{"action":"write","kind":"feedback","scope":"user","topic":"progress_feedback","summary":"长任务开始前确认并持续反馈进度"}`
2. 任务结束后 daemon 上报文件差分；若触发消息命中明确偏好句式（记住/以后都/别再/下次先…），或 signal 要求 write，但本轮没有任何 durable 文件写入（Daily 不算），平台异步写入一条 `agent_memory_curation_candidate`（`metadata.source=missed_write_guard|memory_signal`，`shareable=false`）。
3. 当天 self-review 消费这些 pending 候选：补写正式文件，或放入 `REVIEW.md`。
4. Team curator 跳过 `shareable=false` / `scope=user` 的私有候选。

明确「记住这个」时，agent 仍须先完成 durable 写入再声称已记住；漏写由上述兜底进入自审，而不是再跑一遍同步 LLM。

候选去重优先使用稳定 `topic` / `dedupe_key`（`scope[+subject]+kind+topic`），词法相似只作补充。

## 4. 单 agent 记忆与群体记忆

### 4.1 只让 agent A 永久记住

安东告诉 agent A：“这个只让你永久记住。”

- 写 agent A 的 `memory/MEMORY.md`；
- 不因为说话人是安东就错误写成安东专属偏好；
- 不默认进入团队共享数据库；
- agent A 换群、换项目或切换 Pi / Codex 后仍会加载；
- 其他 agent 不会加载这条私有记忆。

如果内容本身是安东的个人偏好，则写 agent A 隔离根目录中的 `users/<andong-member-id>/USER.md`，并只在 agent A 服务安东时启用。

### 4.2 群里未 `@` 的“都给我记住”

对于群里的顶层人类消息，如果没有 `@` agent：

1. 服务端遍历当前群全部未归档、未静音的 agent 成员；
2. 为每个 agent 创建独立且持久的 `requires_wake=true` inbox event；
3. 离线 runtime 上线后仍可领取该事件；
4. 每个收到消息的 agent 独立判断并写自己的本地记忆。

因此，“都给我记住”“你们记一下”“everyone note this”等集体表达，默认指**当前消息实际覆盖的 agent**。每个接收者各写各的，不再为了 fanout 创建 workspace/team shared candidate。

只有满足以下任一条件才创建经过治理的 workspace/team 候选：

- 用户明确包含当前消息接收者以外的 agent，例如“整个 workspace 所有 agent”；
- 用户明确要求群外 agent 或未来新加入的 agent 也遵守；
- 内容本身被明确声明为 workspace/team 的公共制度或公共知识。

集体措辞本身不能扩大秘密、敏感个人信息、明确的成员专属偏好或群专属规则。

### 4.3 群消息边界

- 被静音的 agent 不会被未 `@` 的群消息唤醒；共享记忆流程不应绕过静音。
- 明确 `@agent` 时，只直接唤醒被 `@` 的 agent；直接 mention 可以穿透该 agent 的群静音。
- thread 内没有 `@` 的回复主要唤醒 thread follower；其他群内 agent 只得到 ambient context，不保证立即运行。
- 如果要求群内所有 agent 立即处理，顶层未 `@` 群消息是当前的全群 agent wake 路径。

## 5. 文件数据库和两种数据库记录

### 5.1 agent 私有文件

文件是 agent 立即写入、下次运行直接召回的持久记忆。它适合单 agent、单成员、单项目和单群的精确作用域。

### 5.2 agent inbox 数据库记录

`agent_inbox_event` 等记录负责可靠投递群消息和 agent 任务：

- 每个目标 agent 有独立事件；
- 未处理事件保持 `pending`；
- runtime 后续领取；
- delivery lease 过期后可以回收重试。

这是消息可靠性机制，不代表内容已经成为团队长期记忆。

### 5.3 审核后的记忆数据库

平台记忆和 team knowledge 用于：

- 跨 agent 或跨 runtime 提供经过审核的知识；
- 保存来源和适用对象；
- 按成员、项目、群、任务类型和到期时间筛选；
- 去重、合并和处理冲突；
- 让不在原始消息受众内、但被明确纳入作用范围的 agent 后续获得公共知识。

共享数据库不是 agent 私有文件的替代品，也不应该被当作普通群消息的二次投递通道。

## 6. 每次运行只加载相关记忆

运行时不会把 agent 的所有历史记忆都塞进上下文。它按当前执行范围选择：

- 当前 agent 的全局记忆；
- 当前成员的 `USER.md` 和关系信息；
- 当前项目的 `MEMORY.md`、`STATE.md` 和 `DECISIONS.md`；
- 当前群的 `CONTEXT.md`；
- Asia/Shanghai 日期下今天的 daily 文件；
- 数据库中与当前成员、项目、群、任务类型和到期时间匹配的审核后记忆。

默认不加载：

- 其他成员的用户文件；
- 其他项目或群的文件；
- 历史 daily 文件；
- notes 索引和未知文件；
- 已过期或作用范围不匹配的数据库记忆。

运行时会对本地文件和数据库记忆去重，并将最终记忆包限制在约 16 KiB。数据库读取同时限制为最近 48 条 agent memory 和最近 24 条 team knowledge，避免记忆挤占当前任务上下文。

Codex 只获得当前相关作用域目录的可写权限；Pi 和其他 provider 通过各自文件工具写同一套 Multica agent 根目录。

## 7. 后台自进化和整理

后台整理是**冷路径**：补齐、提升、去重，不替代热路径里的即时写入，也不应在每一轮聊天结束时由干活 agent 自己再跑一遍。

后台整理遵循以下方向：

1. L1 使用当天证据更新当天的 `memory/daily/YYYY-MM-DD.md`（补 agent 漏记的短索引，不重述过程）。
2. L2 agent self-review 从 Daily 将稳定内容整理进正式文件（含 `USER.md` / `RELATIONSHIP.md` / `MEMORY.md` 等），将不确定或可能共享的内容放进 `REVIEW.md` / proposal。
3. team curation 只消费主动提交的候选，进行去重、合并、冲突判断和公共知识审核。
4. collective wording 不再自动证明 workspace/team 作用域；必须有更广受众或公共制度的明确证据。
5. L4 清理过期的项目 `STATE.md` 内容，并对项目 `MEMORY.md`、`STATE.md`、`DECISIONS.md` 去重。

curator 额外读取的 scoped context 最多 12 个文件、约 16 KiB；不会预加载历史 daily、notes 索引、未知文件或符号链接。

## 7.1 日常运行规则

- 自动日常整理只处理 **active** agent：当天在 `agent_task_queue` 或 `agent_inbox_event` 有活动、且 runtime 在线的 agent 才进入 self-review / promotion / curator 轮次。
- 长时间没有新活动的 agent 默认跳过自动整理；不会为了“补齐”而强行跑一遍空自审。
- 这类 inactive agent 只有在手动 backfill、显式 `--force`，或者重新变为 active 后，才会被补跑。
- team curation 只消费已经产出候选的 active agent；不会把 inactive agent 当成新的共享记忆来源。
- 如果当天没有相关证据，L1/L2/L3/L4 应当尽量不生成噪音文件，只保留必要的审计记录。

## 8. 冲突和安全规则

- 当前任务和实时用户指令始终高于历史记忆。
- 记忆与当前指令冲突时，执行当前指令并将冲突放入 `memory/REVIEW.md`，不能静默改写规则。
- 不能从显示名猜测成员、项目或群的稳定 ID。
- 用户记忆必须带当前成员的稳定 ID，不能读取或写入另一个成员的隔离目录。
- 不因“大家”“都”“所有人”等集体措辞扩大秘密、敏感信息或成员专属行为。

### 8.1 换机 / 双 runtime：可靠增量同步与冲突（策略 A）

同一 `agent_id` 可在多台机器、多个 runtime 上热写本地文件。平台通过本地 outbox/cursor 和 `agent_memory_sync_entry` 做增量同步：

```text
本地 durable 文件变更
  → 先原子写 `.multica/memory-sync-outbox.json`
  → daemon 幂等上行 portable bullet / tombstone
  → 中心以 change_seq 排序并按 identity_key 判定
  → 每次任务前 pull cursor 之后的增量
  → active 投影进正式文件；conflict 只进 REVIEW.md；tombstone 删除旧投影
```

本地 `.multica/memory-sync-state.json` 保存 pull cursor、已观察的本地 atom 和中心 active 投影。只有 server ACK 后 outbox batch 才删除；网络失败或 daemon 重启不会丢写入。删除通过 `status=superseded + deleted_at` 传播，离线旧设备不能自动复活 tombstone。

判定：

| 情况 | 行为 |
|---|---|
| 同 key、内容相同 | 忽略（更新 seen_at） |
| 同 key、新内容更具体 | 更新 active 并推进 `change_seq` |
| 同 key、语义对立或分歧 | **保留先到的 active**；对立内容写入 `status=conflict`，pull 时移出正式文件并进入 `REVIEW.md` |
| portable 内容被删除 | 写 tombstone；其他设备增量 pull 后删除对应本地投影 |
| Daily / REVIEW 原文 | 不上行中心（Daily 仍是本地流水；REVIEW 是冲突出口） |
| 绝对路径、loopback、credential-like 内容 | 留在本机；中心双端拒绝 |

每台 daemon 的环境事实写入：

```text
<agent-root>/devices/<daemon-id>/STATE.md
```

运行时只暴露 `MULTICA_AGENT_ROOT`；设备目录按 `<agent-root>/devices/<daemon-id>/` 相对定位。绝对路径、安装工具、端口、localhost 服务和其他机器特定状态写这里，不写 portable USER/MEMORY/project/channel 文件。

硬规则：

1. 机器 / runtime 只做 provenance，不当优先级（禁止纯 last-write-wins）。
2. `conflict` 条目**不作为权威记忆注入**执行上下文。
3. 用户本轮明确新指令仍可覆盖历史；裁决后可将旧条标为 superseded（后续自审/人工）。
4. pull 是增量并集投影，不是整文件覆盖；已有本地 bullet 不重复追加。
5. 本地 atom 索引必须在 outbox 落盘后才能推进；server ACK 前不得清队列。
6. 删除必须保留 tombstone，不能用“某台机器没看到文件”直接删除其他设备内容。
7. 删除 batch 只有在 server 返回 `protocol_version >= 2` 时才能 ACK；旧 server 忽略新字段时必须保留 outbox 等待升级。

## 9. 记忆互联 Wiki + 显式边（LRM-1000）

产品原则（jianghp3 锁）：**Wiki 编译页 + 轻量显式边**；图引擎（Neo4j / 全量 Graphiti）后置。人也要看见产品面（UI 设计门另单）；Agent 上下文**按需注入**，禁止全量 Wiki 入 prompt（护 KV Cache）。

### 9.1 节点映射

| 产品概念 | 存储 | kind / 文件 |
|---|---|---|
| 频道 CONTEXT | `team_knowledge_item` + 本地 `channels/<id>/CONTEXT.md` | `context` |
| 项目 DECISIONS | `team_knowledge_item` + 本地 `projects/<id>/DECISIONS.md` | `decision` |
| Memory / Skill / policy… | 既有 `agent_memory` / `skill` / team knowledge kinds | 不变 |

### 9.2 最小边集（可查询）

| 边 | 含义 |
|---|---|
| `derived_from` | 页 ← issue / 频道结论（提升必写） |
| `about` | 页 ↔ issue / channel / project |
| `shared_to` | 页 → agent（可见性） |
| `supersedes` | 新决策废止旧页（旧页 archive） |
| `owned_by` | 页 → member / agent |

表：`team_knowledge_edge`。API：`POST /api/workspaces/{id}/knowledge/promote`，`GET .../team-knowledge/{itemId}/neighbors?hops=1\|2`。

### 9.3 写入 / 提升 / lint 纪律

| 动作 | 谁写 | 何时 | 必写边 / 守门 |
|---|---|---|---|
| 热写本地 CONTEXT/DECISIONS/MEMORY | 当前 agent | 干活中 | 无边；scope ID 必须匹配任务 |
| 提升结论 → CONTEXT/DECISION | member/agent API | issue 或频道结论定稿 | **必须** `derived_from`；建议 `about` + `owned_by` |
| 废止旧决策 | 提升时带 `supersedes_id` | 新页取代旧页 | `supersedes`；旧页 `archived`；注入只取赢家 |
| 跨 agent 可见 | promote `shared_to_agent_id` | 需要共享时 | `shared_to`；禁止 user-private 扇出 |
| ingest（页更新） | 策展 / 提升 | 页面内容变更时 | 改页本身；**不要**每轮把全库灌进 prompt |
| lint | L2/L3 策展 | 冷路径 | 孤儿边、矛盾、被 supersede 仍 active |

### 9.4 Agent 注入与 KV Cache

- claim 路径只注入：**任务相关种子页**（当前 channel `context` / project `decision` / `about` 指向当前 issue·channel·project）+ **≤2 跳** `team_knowledge` 邻域；硬顶约 12 页。
- **禁止** `ListActiveTeamKnowledgeForExecution` 式全量 dump 进入 wake。
- 稳定系统前缀（AGENTS / startup digest）**不**含 wiki 正文；页面更新走 ingest/提升，靠下次相关 wake 按需读取，避免无谓打爆 KV Cache。

### 9.5 非目标

不上 Neo4j/全量 Graphiti；不绑 Harness；不替代 LRM-982 自进化证据链 AC。

## 10. 与产品笔记写回并行（S3-W3）

Agent 私有 `memory/daily/` **不是**产品笔记，也 **不是** `note_page_writeback`。

- **产品侧**：workspace 笔记页 + 待审写回（人类审阅后进 `note_page`）——合同见 `docs/notes-editor-worker-contract.md`。
- **记忆侧**：本文件描述的 agent 私有文件与平台审核记忆。

两套存储**并行、可互链、禁止合并**。不要把 Daily 流水账灌进笔记正文，也不要把待审写回当 Daily 的替代表。后续实现若需要「从 Daily 生成回顾笔记」，必须是显式产品功能，并仍走笔记 ACL / 待审合同，而不是静默同步。

## 11. 最终判断口诀

```text
当前 agent 自己要记住        -> agent 私有文件
某个成员的偏好              -> users/<member-id>/USER.md（热路径立刻写）
对接/交接/归属              -> RELATIONSHIP.md / notes（热路径立刻写）
实质收工流水账              -> memory/daily/今天.md（热路径 append；短社交跳过）
可复用问题修法              -> 项目 MEMORY/DECISIONS 或 agent MEMORY/notes（收工自记，不等「记住」）
只属于当前项目或当前群       -> project/channel scoped 文件
群内所有当前接收者都要记住   -> 每个 agent 各写自己的文件
明确覆盖群外/未来 agent       -> workspace/team 候选，经过审核后入库
不确定、冲突或敏感            -> REVIEW.md，不直接扩大范围
整理/提升/去重/共享审核       -> L1/L2/Curator 冷路径，不挡聊天
换机同步                    -> 中心 active hydrate；conflict 进 REVIEW，不静默覆盖
```
