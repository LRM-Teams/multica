# 锚点居中消息分页（`around_seq`）设计

- Date: 2026-07-08
- Status: 草案，待 Parker/Felix/Frank 过目
- 上游: task #340（Frank 提出，Barry 验证行业先例）；跟 #348（未读冷开滚动）、#343（highlight/deep-link 定位）共用
- 本文角色：后端接口设计。前端接入（`initialTopMostItemIndex` 对接）由 Felix 定

## 0. 问题

现在 `GET /api/channels/{id}/messages` 只支持"从最新往前翻页"（`before_seq` / `before_created_at`+`before_id` 游标）。冷开一个有未读的会话时，流程是：

1. 加载最新一页（默认落底）
2. 前端算出未读锚点在哪一行
3. 用 `scrollToIndex` 手动滚过去
4. 等它 "settle"（多帧重试直到几何真到位）

第 3-4 步是今天全天 #348 反复踩坑的根：锚点算不出来、settle 只试一次就放弃、settle 压根没被触发……每一个坑本质上都是"先加载错误位置、再补救式滚动"这套架构带来的时序竞争。而且未读足够多时（锚点在几百条之前），锚点消息可能根本不在"最新一页"的加载范围内，连滚动目标行都不存在。

## 1. 方案

抄成熟先例：**Discord** 的 `GET /channels/{id}/messages?around={message_id}` 、**Slack** 的 `conversations.history` 配 `latest`/`oldest`/`inclusive`。核心思路一致：给一个锚点 + limit，服务端一次把锚点前后的消息都吐出来，前端首次渲染就落在正确位置，不需要"加载完再滚"。

## 2. 接口

```
GET /api/channels/{channelId}/messages?around_seq={N}&limit={L}
```

- `around_seq`（新增，int64，可选）：与现有 `before_seq` / `before_created_at`+`before_id` 互斥。三选一，都不传时维持现状（默认最新页）。
- `limit`：语义不变（默认/上限沿用 `channelMessagesDefaultLimit`/`channelMessagesMaxLimit`）。

### 取数逻辑

- `limitBefore = limit / 2`，`limitAfter = limit - limitBefore`
- 查询 A：`seq <= around_seq` 的消息，按 `seq DESC` 取 `limitBefore` 条，取到后反转成 `seq ASC`
- 查询 B：`seq > around_seq` 的消息，按 `seq ASC` 取 `limitAfter` 条
- 两段拼接成一个连续的 `seq ASC` 数组
- WHERE 子句复用现有 `ListChannelMessages` 的过滤条件（`main_timeline_visible`、tombstone 可见性规则），只是分成两个方向各查一次，不是新写一套过滤逻辑

### 边界

- 锚点在频道最早消息附近（`limitBefore` 不够）→ 用 `limitAfter` 补足（`limit - 实际取到的 before 条数`），保持总数尽量贴近 `limit`
- 锚点在频道最新消息附近或超过 max seq（如竞态导致 `last_read_seq` 暂时大于当前已知最新 seq）→ `limitAfter` 可能为 0，`HasMoreAfter=false`
- `around_seq` 对应的消息本身已删除（tombstone）→ 不影响取数，`seq` 只是排序/过滤基准，不要求该 seq 存在真实可见消息

## 3. 响应

现有 `ChannelMessagesPageResponse` 只有单方向游标（`NextCursor` = 继续往旧的翻页）。`around_seq` 模式下窗口两端都可能有更多消息，需要双向游标 + 前端定位锚点用的 index：

```go
type ChannelMessagesPageResponse struct {
    Messages    []ChannelMessageResponse       `json:"messages"`
    Limit       int                            `json:"limit"`
    HasMore     bool                           `json:"has_more"`      // 沿用：向旧方向还有更多（before 模式下的含义不变）
    NextCursor  *ChannelMessagesCursorResponse `json:"next_cursor,omitempty"`

    // 新增，仅 around_seq 模式下返回：
    AnchorIndex   *int                           `json:"anchor_index,omitempty"`    // Messages 数组中，seq <= around_seq 的最后一条的下标；FE 用它算 initialTopMostItemIndex
    HasMoreAfter  bool                           `json:"has_more_after,omitempty"`  // 向新方向还有更多
    AfterCursor   *ChannelMessagesCursorResponse `json:"after_cursor,omitempty"`    // 继续向新方向翻页用（seq > 该值）
}
```

`HasMore`/`NextCursor` 在 `around_seq` 模式下语义不变（描述窗口更早一侧），新增的 `HasMoreAfter`/`AfterCursor` 描述窗口更新一侧——FE 后续双向 infinite scroll 直接复用两套游标，不用改现有"向旧翻页"那条代码路径。

## 4. 前端接入点（Felix 定，这里只列已知的对齐点）

- `initialTopMostItemIndex = response.anchor_index`，配合 Virtuoso 挂载
- **align 按用途区分**（Iris UX 口径）：未读冷开 = `start`（divider 贴视口顶），深链/highlight/搜索命中 = `center` + 高亮
- divider 位置沿用现有 `payloadSnap` 快照语义（mark-read 推进游标后，本次渲染看到的 divider 不因为游标变化而跳动）——`around_seq` 请求应该用触发那一刻的 `last_read_seq` 快照值，不是请求发出时刻查询到的最新值（避免和现有 #303 race 语义冲突）
- #348 现有的 `scrollToIndexUntilSettled` retry/settle 机制**保留作为兜底**（不删），处理 `around_seq` 之后 Virtuoso 未能精确定位等残余情况；不再是主路径

## 5. 复用点

- `#343`（highlight/deep-link 定位、搜索命中跳转）与本方案是同一套数据加载，`around_seq` 参数化后天然覆盖，不需要给 #343 单独出一套接口
- SQL 索引复用现有 `idx_message_main`（`channel_message(channel_id, seq DESC)` 一类），两个方向查询都命中已有索引，不需要新索引

## 6. 不在本次范围

- DM 消息接口（如果 DM 走独立 handler 而非复用 `ListChannelMessages`）需要单独确认是否要同步加 `around_seq`——先确认 #348/#343 的真实调用路径是不是都过这一个 handler
- Thread 内消息分页（`ListChannelMessageThread`）不在本次范围，thread 场景消息量小，暂不需要锚定加载
- 排在 #348 完全稳定收口之后实现，不影响当前 hotfix 节奏
