# 统一 Conversation / Message 终态表设计

- Date: 2026-07-08
- Status: 已拍(2026-07-08 Frank 批准,§7 待拍点清零)
- 上游: `docs/product-conversation-model-prd.md` v2.4 §5(产品模型已定);migration 144(#197)已落地约 60%
- 本文角色: §5 的**工程终态 schema** — 最终 DDL、与现状的 delta、退役清单、迁移排序。PRD 定"是什么",本文定"表长什么样、旧表怎么死"
- 参照: Slack conversations 模型;raft CLI 命令面(0.71.0,本机实测)——命令面即 schema 验收测试

## 0. 现状审计(2026-07-08 代码核实)

三代模型同堂,五张读态表并行双写——每一步局部合理,叠加成灾(PRD §0.1 同判):

| 代际 | 表 | 状态 |
|---|---|---|
| 一代 | `chat_session` / `chat_message` | 活跃(agent 1:1 chat + channel 桥接 via `channel_agent_session`) |
| 二代 | `channel` / `channel_member` / `channel_message` + `channel_read` / `channel_thread_state` / `dm_peer_state` | 活跃,DM 靠 `name="dm:..."` 编码(mig 126) |
| 三代 | `conversation` / `conversation_member` / `thread_participant`(mig 144) | 活跃,但 conversation 是 channel 的影子(id 复用、`channel_id NOT NULL`) |

具体病灶(file:line 均已核实):

1. **双写**:`channel.go:433/443`(channel_read + conversation_member 各插一遍)、`channel.go:2078/2083`(thread 两套)、`dm.go:341→361`(dm_peer_state + conversation_member)。事务外无一致性保证,drift 只是时间问题。
2. **两套线程机制**:`thread_id TEXT`(mig 119,Lark 语境)与 `thread_root_message_id UUID`(mig 135)并存一张表。
3. **两套内容真相**:`content TEXT` + `parts JSONB`(mig 140),谁 canonical 不在 schema 层可知。
4. **三代分页索引全活着**:mig 139(created_at)、144(seq)、150(projection)逐代新增不删旧,`channel_message` 上约 11 个索引。
5. **词汇表不一致**:`channel_member` 用 `'user'`,`channel_message_reaction` 用 `'member'`,PRD 写 `'human'`——Slack `bot_id`/`user_id` 双轨病的胚胎。
6. **seq 分配藏在 plpgsql 触发器**(mig 144),与 Go 业务逻辑两个真相源;channel/dm 域 4100 行裸 SQL 绕过 sqlc。
7. **`channel_ambient_pending_wake.delivered_to_seq` 与 `conversation_member.last_delivered_seq` 重复**。

## 1. 设计原则(四条)

1. **只有一种东西:conversation。** channel / dm / agent 1:1 都是 `conversation.type`,消息表不知道容器种类。(Slack 2017 年把 channels/groups/im 三套 API 痛苦统一成 conversations.* 的同款教训。)
2. **成员状态一张表。** cursor、mute、pin、close、manual-unread、agent wake 全是"某成员对某会话"的属性。
3. **seq 是唯一排序真相**,应用层事务内取号(行锁保证 seq 序 == 提交可见序),`created_at` 仅展示。所有 unread 都是整数减法。raft 的 `read --around <seq>`、`message check`(ack)全长在这条骨架上。
4. **一套 actor 词汇**:`human | agent | system`(与 PRD、raft 消息头 `type=` 一致)。`lark` 不是 author 类型,是传输渠道(`via`)。

## 2. 终态 DDL

```sql
-- 会话:channel/dm 的唯一容器(PRD §1: type 只有两种;agent 1:1 = dm)
CREATE TABLE conversation (
  id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id  UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  type          TEXT NOT NULL CHECK (type IN ('channel', 'dm')),
  name          TEXT,                 -- 仅 channel;dm 无名(不再用 name 编码成员对)
  description   TEXT,
  visibility    TEXT CHECK (visibility IN ('public', 'private')),   -- 仅 channel(PRD §5 要求,现表缺失)
  dm_key        TEXT,                 -- 仅 dm:成员对规范键,find-or-create 幂等锚点
  last_seq      BIGINT NOT NULL DEFAULT 0,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  archived_at   TIMESTAMPTZ,
  CONSTRAINT conversation_shape CHECK (
    (type = 'channel' AND name IS NOT NULL AND visibility IS NOT NULL AND dm_key IS NULL) OR
    (type = 'dm'      AND name IS NULL     AND dm_key IS NOT NULL)
  )
);
CREATE UNIQUE INDEX uq_conversation_name ON conversation(workspace_id, name)   WHERE type = 'channel';
CREATE UNIQUE INDEX uq_conversation_dm   ON conversation(workspace_id, dm_key) WHERE type = 'dm';

-- 成员:membership + 双游标 + 偏好 + wake,一行放完
CREATE TABLE conversation_member (
  conversation_id     UUID NOT NULL REFERENCES conversation(id) ON DELETE CASCADE,
  workspace_id        UUID NOT NULL,
  member_kind         TEXT NOT NULL CHECK (member_kind IN ('human', 'agent')),
  member_id           UUID NOT NULL,
  role                TEXT NOT NULL DEFAULT 'member',
  joined_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
  left_at             TIMESTAMPTZ,
  last_read_seq       BIGINT NOT NULL DEFAULT 0,   -- run 成功 / 显式 ack 后推进(§6)
  last_delivered_seq  BIGINT NOT NULL DEFAULT 0,   -- 投递游标,全库唯一一份
  pending_wake_to_seq BIGINT NOT NULL DEFAULT 0,   -- ambient 合并唤醒右端(§6.3②);> delivered 即有待醒
  wake_state          TEXT NOT NULL DEFAULT 'active' CHECK (wake_state IN ('active', 'no_wake')),
  muted_at            TIMESTAMPTZ,
  closed_at           TIMESTAMPTZ,                 -- DM 关闭,新消息自动 reopen(§8)
  pinned_at           TIMESTAMPTZ,
  marked_unread_at    TIMESTAMPTZ,
  updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (conversation_id, member_kind, member_id)
);
CREATE INDEX idx_member_sidebar ON conversation_member(workspace_id, member_kind, member_id, closed_at, last_read_seq);

-- 消息:唯一的消息表。身份 = (conversation, seq),对齐 Slack (channel, ts)
CREATE TABLE message (
  id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  conversation_id   UUID NOT NULL REFERENCES conversation(id) ON DELETE CASCADE,
  workspace_id      UUID NOT NULL,               -- 多租户过滤冗余,保留
  seq               BIGINT NOT NULL,             -- 应用层事务内取号,不用触发器
  author_kind       TEXT NOT NULL CHECK (author_kind IN ('human', 'agent', 'system')),
  author_id         UUID,                        -- system 时为 NULL
  via               TEXT NOT NULL DEFAULT 'multica',   -- 'multica' | 'lark':传输渠道,非 author 类型
  external_id       TEXT,                        -- 外部渠道消息 id(Lark 去重)
  root_message_id   UUID REFERENCES message(id) ON DELETE CASCADE,  -- 唯一线程机制;只指顶层(不嵌套)
  show_in_channel   BOOLEAN NOT NULL DEFAULT false,    -- thread 回复投影主线,"一行两面"(#256)
  reply_to_id       UUID REFERENCES message(id) ON DELETE SET NULL, -- quote
  parts             JSONB NOT NULL DEFAULT '[]', -- 正文唯一真相(#142/#143 契约)
  text              TEXT NOT NULL DEFAULT '',    -- 从 parts 派生:trgm 搜索/预览,禁止反向写
  client_message_id TEXT,                        -- §4.2 幂等(#207)
  created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),  -- 仅展示
  edited_at         TIMESTAMPTZ,
  deleted_at        TIMESTAMPTZ,                 -- tombstone;edit/delete 绝不产生 wake
  UNIQUE (conversation_id, seq)
);
CREATE INDEX idx_message_main   ON message(conversation_id, seq DESC) WHERE root_message_id IS NULL OR show_in_channel;
CREATE INDEX idx_message_thread ON message(root_message_id, seq)      WHERE root_message_id IS NOT NULL;
CREATE UNIQUE INDEX uq_message_idem ON message(conversation_id, author_id, client_message_id) WHERE client_message_id IS NOT NULL;
CREATE UNIQUE INDEX uq_message_ext  ON message(conversation_id, external_id)                  WHERE external_id IS NOT NULL;

-- thread 订阅/游标(PRD:落表非派生;thread 共享会话 seq 空间)
CREATE TABLE thread_participant (
  root_message_id UUID NOT NULL REFERENCES message(id) ON DELETE CASCADE,
  member_kind     TEXT NOT NULL CHECK (member_kind IN ('human', 'agent')),
  member_id       UUID NOT NULL,
  followed_at     TIMESTAMPTZ,
  last_read_seq   BIGINT NOT NULL DEFAULT 0,
  PRIMARY KEY (root_message_id, member_kind, member_id)
);

-- 反应:自然键 PK,无代理主键;react --remove = DELETE
CREATE TABLE reaction (
  message_id  UUID NOT NULL REFERENCES message(id) ON DELETE CASCADE,
  member_kind TEXT NOT NULL CHECK (member_kind IN ('human', 'agent')),
  member_id   UUID NOT NULL,
  emoji       TEXT NOT NULL,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (message_id, member_kind, member_id, emoji)
);

-- freshness hold(§4.3):被拦的 agent send 原文,--send-draft / --anyway 重发
CREATE TABLE message_draft (
  id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  conversation_id   UUID NOT NULL REFERENCES conversation(id) ON DELETE CASCADE,
  root_message_id   UUID REFERENCES message(id) ON DELETE CASCADE,
  author_kind       TEXT NOT NULL CHECK (author_kind IN ('agent')),  -- 只拦 agent(PRD 边界)
  author_id         UUID NOT NULL,
  parts             JSONB NOT NULL DEFAULT '[]',
  client_message_id TEXT NOT NULL,      -- 复用 §4.2 幂等键
  based_on_seq      BIGINT NOT NULL,    -- hold 时 agent 已知 max seq
  hold_count        INTEGER NOT NULL DEFAULT 1,   -- 连续 hold 上限防活锁
  created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (conversation_id, author_id, root_message_id)
);

-- mention 物化:directed delivery 判定源 + 发送侧 pending resolution(raft mention pending/notify/add)
CREATE TABLE message_mention (
  message_id  UUID NOT NULL REFERENCES message(id) ON DELETE CASCADE,
  target_kind TEXT NOT NULL CHECK (target_kind IN ('human', 'agent', 'all')),
  target_id   UUID,                     -- 'all' 时为 NULL
  resolution  TEXT NOT NULL DEFAULT 'delivered'
              CHECK (resolution IN ('delivered', 'pending', 'notified', 'added', 'dismissed')),
  PRIMARY KEY (message_id, target_kind, target_id)
);
```

配套(不在本表域展开):attachment 在消息域只需 `message_id` 一列;agent 常驻 runtime session 是 **agent 级**属性(v2.4.7"聊天=每 agent 一条 session"),不挂会话;`task_message`(执行遥测)保持独立,合并是错的。

## 3. 不变量(schema 之外必须守住的)

1. **seq 序 == 提交可见序**:取号 + INSERT 同事务,conversation 行锁串行同会话写入。游标不可能越过未可见消息。
2. **unread = `last_seq - last_read_seq` 纯减法**:发消息事务内同时把作者自己的 `last_read_seq` 推到新 seq,读路径无需"排除自己的消息"。
3. **`text` 只从 `parts` 派生**,任何写路径直写 `text` 都是 bug。
4. **system 行永不唤醒**(#329 + reaction/edit 同 family);人类时间线可见、agent bundle 可见(带 system 标记)。
5. **wake 判定读 `message_mention`**,不再运行时解析正文——#196 must-reply"谁被定向"有审计落点。
6. **所有查询过滤 workspace_id**(多租户既有铁律)。

## 4. 退役清单(终态达成时不存在的东西)

`chat_session`、`chat_message`、`channel_read`、`channel_thread_state`、`dm_peer_state`、
`channel_agent_session`、`channel_ambient_pending_wake`(折入 conversation_member 三游标 +
`agent_task_queue` partial unique)、`channel` 表本体(并入 conversation)、
`channel_message.thread_id TEXT`、`content`/`parts` 双真相、`author_name` 快照列、
`'user'`/`'member'` 词汇、mig 139/144 被 150 取代的分页索引、seq 触发器(收回 Go)。

## 5. 迁移排序(每步独立可发布,顺序即依赖)

1. **锁词汇表**(便宜且越拖越贵):全库 `human|agent(|system)`,消灭 reaction 的 `'member'`。
2. **停双写、drop 旧读态**:`channel_read`/`channel_thread_state`/`dm_peer_state` 回填进
   `conversation_member` 后删除。当前最大正确性风险,先做。
3. **DM 去 name 编码**:加 `dm_key`,回填自 `dm:...` 命名,DM 行 name 置空。
4. **conversation 转正**:name/description 迁入 + 补 `visibility`;`channel` 降级为视图过渡一版后
   drop;`channel_message` 的 channel_id/conversation_id 双列收敛。
5. **chat_session/chat_message 退役**:回填成 dm conversation + message;runtime session 绑定改
   agent 级一行。
6. **pending_wake 折入 conversation_member**;在途 run 约束移到 `agent_task_queue` partial unique。
7. **收尾卫生**:seq 取号收回 Go 事务(顺势把 channel/dm 域裸 SQL 收进 sqlc);drop 旧索引与
   `thread_id`;`content` → `text`(parts 派生)。
8. **增量新表**(与 1-7 无依赖,可并行):`message_draft`(§4.3)、`message_mention`、`reminder`。
9. **principal 表(二期,依赖步骤 1,排在 1-7 完成后)**:建 `principal(id UUID PK,
   kind CHECK(human|agent))`;user/agent 主键改为派生自 principal(创建同事务先插主体行);
   各引用表逐个把 `(member_kind, member_id)` 多态对收敛为 `member_id REFERENCES principal(id)
   ON DELETE CASCADE`(kind 需要时 JOIN 取)。独立系列 PR,一表一 PR,横切 issue 域
   (assignee)时与 issue 侧对齐后再动。

## 6. Slack / raft 坐标对照(设计对齐依据)

| 本设计 | Slack | raft 命令面证据 |
|---|---|---|
| `conversation.type` | conversations API(`is_channel`/`is_im`) | `#channel` / `dm:@peer` target 语法 |
| `(conversation, seq)` | `(channel, ts)` | `read --before/--after/--around <idOrSeq>` |
| `root_message_id` 扁平 | `thread_ts` | `#channel:threadId`、`dm:@peer:threadId` |
| `show_in_channel` | `reply_broadcast` | send options |
| 双游标(read/delivered) | 读游标 + 事件确认 | `inbox check`(只看)vs `message check`(排空+ack) |
| `parts[]` | `blocks[]` | send 正文契约 |
| `message_draft` | — | `send --send-draft --anyway` |
| `message_mention` | — | `mention pending/notify/add` |
| `member_kind` 单词汇 | bot_id/user_id 双轨(反面教材) | 消息头 `type=human|agent|system` |

## 7. 决策记录(2026-07-08 Frank 全部拍定)

1. **步骤 2(停双写)排进当前迭代。** 正确性风险最高、收益最快,优先启动。
2. **`principal` 表确认为长期方案,列为二期正式阶段(步骤 9)。** 行业对齐:主流 IM
   均已统一 actor 命名空间(Slack bot user、Discord bot=user flag、Matrix bot 即用户);
   multica 的 agent 明细远重于 user,故取关系建模的标准 party/actor 超类型形态——薄
   `principal(id, kind)` 主体表 + user/agent 明细表,多态对收敛为真外键,比 Slack 的
   半吊子统一(遗留 bot_id/user_id 双轨)更干净。启动时机:步骤 1-7 完成、消息域稳定后;
   前置依赖 = 步骤 1 词汇表锁定。提前触发条件保留:悬空引用产生用户可见 bug,或需要
   引入第三种主体(webhook/app),命中即提前。
3. **Lark `thread_id TEXT` 退役前置条件确认**:必须先核实 Lark 线程映射已完全走
   `root_message_id`(#255 存量迁移口径),核实通过才执行步骤 7 中的 thread_id drop。
4. **大步骤节奏确认**:步骤 4(channel 转正)、步骤 5(chat 退役)每步独立 PR + 产品 smoke,
   不合并推进。
