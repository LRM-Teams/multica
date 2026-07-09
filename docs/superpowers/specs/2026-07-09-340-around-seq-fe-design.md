# #340 前端接入：`around_seq` 锚点分页（FE 方案）

- Date: 2026-07-09
- Author: Wren (FE)
- Status: 草案，待 Felix / Parker / Iris 过目
- 上游: BE 设计 `docs/superpowers/specs/2026-07-08-anchor-centered-message-pagination-design.md`（Barry 实现，PR #377）；UX 口径 Iris；替代 #348 老 settle 主路径（settle 保留为兜底）

## 0. 目标一句话
冷开有未读的会话时，首屏**一步落在未读分隔线**（mount prop 定位），不再"先加载最新页 → 再 scrollToIndex 追 → settle 重试"。从架构上消灭 #348 那类时序竞争。

## 1. BE 契约（已读 Barry PR #377 代码，锁定）
`GET /api/channels/{channelId}/messages?around_seq={N}&limit={L}`
- `around_seq`（int64, ≥1）与 `before_*` 游标互斥。
- 响应在现有 `ChannelMessagesPageResponse` 上新增（仅 around 模式）：
  - `anchor_index`：合并后 ASC 数组里**最后一条 `seq <= around_seq`（即最后一条已读）**的下标；窗口内一条已读都没有 → `-1`。
  - `has_more_after` / `after_cursor`：窗口**更新方向**还有更多 + 向新翻页游标。
  - 现有 `has_more` / `next_cursor` 语义不变 = 窗口**更旧方向**。
- 消息数组 `messages` 为 `seq ASC`。

### ⚠️ 给 Barry 的两个联调问题（BE 侧，醒来看）
1. **`anchor_index,omitempty`**：`AnchorIndex int json:"anchor_index,omitempty"` —— `omitempty` 会让 `anchor_index=0`（首条即锚点）从 JSON 里被**省略**。FE 端 `resp.anchor_index ?? 0` 恰好能兜（缺失→0、-1→-1），但这是巧合、脆弱。建议 BE 去掉该字段的 `omitempty`，让 0 正常序列化。
2. **对称回填只做了一半**：`limitBefore=limit/2` 先取、`remainingAfter=limit-actualBefore` 后取——**before 短→after 补** 有；但**after 短→before 补没有**（before 已被 cap 在 limitBefore）。设计文档 §2 明确要求"未读只有 3 条、锚点贴最新"时 before 也要补足到 `limit-实际after`。现状小未读会话窗口只有 `limit/2 + 少量`，上滑立刻二次加载。要不要这轮一起补？（不阻塞 FE 结构，但影响小未读 UX。）

## 2. FE 定位数学（核心）
**UX 修正（Iris + Felix 2026-07-09，已定）**：divider 渲染在**第一条未读之上**。若 pin 到第一条未读 + align:start，divider 会被顶出视口顶、裁掉不可见（昨晚 settle 靠 96px `ANCHOR_TOP_BAND` 下压 anchor 行给 divider 留空间；mount-prop 没这个 band）。故定位目标 = **最后一条已读**：
- **未读冷开**：`initialTopMostItemIndex = firstItemIndex + anchor_index`（= 最后一条已读行）+ `align="start"`。视口顶=最后一条已读、其下紧跟 divider + 第一条未读，divider 可见、贴 Frank 认可的"已读在上 / divider / 未读在下"视觉。
  - 空边界 `anchor_index === -1`（全窗皆未读、无已读）→ `firstItemIndex + 0`（钉最早一条；divider 在最顶、其下即未读）。
- **深链 / 搜索 / highlight（#343 同一数据路径）**：`initialTopMostItemIndex = firstItemIndex + anchor_index`（around_seq 传目标消息 seq、锚点即目标）+ `align="center"` + 高亮。
- **实现落地注意**：现有组件用 `unreadAnchorIndex = messages.findIndex(第一条未读)` = `anchor_index + 1`。未读定位目标 = `unreadAnchorIndex - 1`（= 最后一条已读 = BE anchor_index）。写清楚、别反向再踩一次 off-by-one。
- 验收硬指标（Iris）：① **divider 可见不裁** ② isNearBottom 时序（settle 到位后新消息别拽回底）。

## 3. `around_seq` 取值来源（别等 markRead 回声）
- 冷开请求的 `around_seq` = **进会话前 sidebar/列表响应里的 `last_read_seq` 快照**（已在返回，`COALESCE(vcm.last_read_seq, cr.last_read_seq, 0)`）。不等 markRead 回声（省一个串行 RTT）。
- markRead 回声（`previous_last_read_seq`）只喂 divider 的 `payloadSnap` 渲染快照（现有 #303 语义不变、divider 不因游标推进而跳）。
- `last_read_seq <= 0`（无未读 / 全新会话）→ 不走 around 模式，维持现状（默认最新页、落底）。

## 4. 触发时机 = #336 user-visible entry（关键，别消费未读）
around_seq 请求**只在 user-visible entry 时发**：visible + focus + 用户主动导航进入。**后台重挂 / app restore 自动选中上次会话 / WS 重连重渲染 都不发**（这几个正是今天反复清游标的元凶）。复用 / 对齐 #336 的 entry gate。

## 5. 双向分页
- 向旧（上滑到顶）：沿用现有 `has_more`/`next_cursor` + `before_*` 请求路径，**不改**。
- 向新（下滑到底、锚点上方有更多）：新增 `has_more_after`/`after_cursor` → 用 `after_seq`(或 after cursor 三元组) 向新翻页。Virtuoso `endReached` / `followOutput` 交互需处理：向新翻页 append 到底部，不应触发 followOutput 贴底跳动（已有 #376 ownership + isNearBottom 判定）。

## 6. settle 降级为兜底
- `useUnreadAnchorScroll` + `scrollToIndexUntilSettled` + #376 `isAnchorSettling` **全部保留**，但正常路径不再依赖：around_seq 首屏落对后，锚点已在 mount 位置。settle 仅在 around 数据异常 / Virtuoso 未精确定位等残余情况兜底。#341 两条 settle 边界自动降优先级。

## 7. 数据层改动（已摸清现状，方案如下）

### 现状（backward-only）
- `packages/core/channels/queries.ts` `channelMessagesPageOptions(channelId, limit)` = `useInfiniteQuery`，key `["channel-messages-page", channelId]`，`queryFn({pageParam}) => api.listChannelMessagesPage(channelId, {before: pageParam, limit})`，`getNextPageParam` 只向**旧**走（next_cursor）。`staleTime: Infinity`。
- `flattenChannelMessagePages` = `[...pages].reverse().flatMap(p=>p.messages)` → ASC。
- `channelMessagesFirstItemIndex` = `BASE(1_000_000) - olderCount`，**假设 page0=最新页、其后全是更旧页**。
- api `listChannelMessagesPage(channelId, {limit, before})`（client.ts:2146），响应类型 `ChannelMessagesPage`（channel.ts:138）= {messages, limit, has_more, next_cursor}。**无 around_seq、无双向游标**。
- `last_read_seq` 来源：`Channel.last_read_seq` / `DMItem.last_read_seq`（list 响应），现只经 `useEntryReadCursor` 消费。

### ⚠️ 核心难点（要 Felix 一起定）
around_seq 首屏 = 锚点窗口（既非最新、两侧都可能有更多），打破了"page0=最新、其后全更旧"这个不变式：
1. **firstItemIndex** 现算法失效——around 模式下 page0 是中间窗口，向旧 prepend（减）+ 向新 append（加）都存在。需要新算法：`BASE - (page0 起点之前已加载的更旧条数)`，锚点绝对 index = `firstItemIndex + (窗口内 anchor_index + 1)`。
2. **双向分页**：现在只有 `getNextPageParam`(旧)。around 之后向**新**翻页需要 `getPreviousPageParam` + `after_cursor`。RQ `useInfiniteQuery` 原生支持双向。

### 方案（建议，待评审）
**A. 类型 + api（低风险、先做）**
- `ChannelMessagesPage` 加可选 `anchor_index?`, `has_more_after?`, `after_cursor?`（channel.ts）。
- `listChannelMessagesPage(channelId, {limit, before?, around?})`：`around` 存在时发 `around_seq={around}`（与 before 互斥，BE 已强制）。
- schema 解析新字段。加 client 单测（`?around_seq=N&limit=` + 双游标 round-trip）。

**B. 双向 infinite query（核心）**
- `channelMessagesPageOptions(channelId, {limit, aroundSeq})`：
  - `initialPageParam` = `aroundSeq ? {around: aroundSeq} : null`。
  - `queryFn({pageParam})`：`pageParam.around` → around 请求；`pageParam.before` → 旧翻页；`pageParam.after` → 新翻页。
  - `getNextPageParam(lastPage)` = 旧方向（has_more → next_cursor 包成 `{before}`）。
  - `getPreviousPageParam(firstPage)` = 新方向（has_more_after → after_cursor 包成 `{after}`）。
- `flatten`：RQ 语义 `fetchPreviousPage` prepend 到 pages 前、`fetchNextPage` append 到后。需重定 reverse 逻辑，保证最终 ASC。**注意**：当前 next=旧；若沿用 next=旧，则 previous=新，flatten 时 pages 已是 [新…, page0, …旧]？要仔细核 RQ 的 pages 顺序，写测试锁。
- **firstItemIndex**：改为"page0 窗口起点为基准 + 已加载更旧条数"。around 模式与现有 default 模式并存（无未读时仍走 default backward-only，不动）。

**C. channels-page / dm-conversation wiring**
- entry 时（#336 gate）决定 aroundSeq：`last_read_seq` 快照 >0 且有未读 → 传 aroundSeq；否则不传（default 最新页，现状不变）。
- 深链/搜索：aroundSeq = 目标消息 seq（#343 复用）。
- 向新翻页接 Virtuoso `startReached`/`endReached`（现只有 `startReached`→onLoadOlder；需加向新的触发）。

### 风险控制
- around 模式与 default 模式**并存、default 路径一行不改**（无未读/老会话继续 backward-only）。降低 blast radius。
- firstItemIndex/双向是消息加载核心，出过 #365 事故——小步 + 充分单测 + Felix 互审 + 真机验收再上。

## 8. 产品口径（Parker 2026-07-09 已答，锁定）
1. **divider 文案/空态**：文案不新增，沿用 "N new messages"；**N = 真实未读数（与 sidebar badge 同源），不是窗口内条数**（486 未读、窗口只装 50，divider 仍写 486）。实现注意：`useNewMessagesDivider.count` 要取真实未读数、不是窗口计数（实现时核一遍）。空态：`anchor_index=-1`（全窗皆未读）→ divider 钉窗口最顶、首屏从第一条起（§2 `+0` 正确）；无未读 → 不走 around、无 divider、落底、现状原样。
2. **向新翻页触发距离**：跟旧方向**对称、不发明新参数**——Virtuoso `endReached` + 现有 `increaseViewportBy.bottom`（520px ≈ 1.5 屏预取），跟 `startReached`+top 320px 同思路。真机撞到空洞再调，不预优化。
3. **首屏 limit**：默认 **50（锚点前后各 ~25）不动**；~25 ≈ 桌面 2-4 屏。Barry 补完对称回填后小未读窗口也填满到 50。真机调，不预设特殊值。

产品侧确认：around/default 双模并存、**default 一行不改**（最重要的爆炸半径控制）✓；settle 全套降兜底（#341 降级）✓。架构难点（firstItemIndex / 双向 pages 顺序）等 Felix 早上拍。

## 9. 测试计划
- 数据层单测：around 模式 query key / 响应解析（anchor_index/dual cursor）/ 冷开选 around vs default 分支。
- 定位单测：`firstUnreadIndex = anchor_index+1`、空边界 -1→0、深链用 anchor_index、align 分支。
- 复用现有 use-unread-anchor-scroll 测试（settle 兜底仍绿）。
- 真机验收（新功能验收，非旧 bug 考古）：未读→divider 钉顶 / 深链→居中高亮 / 大量未读锚点不在最新页照样对 / 后台·restore 不消费 / sidebar==消息视图。
