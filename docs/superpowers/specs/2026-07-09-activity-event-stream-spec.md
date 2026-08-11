# Spec：Activity 事件流（对齐 raft，#351）

日期：2026-07-09 · 作者：Parker（产品）· 状态：口径已定（Frank 拍"Activity 改成和 raft 一样"）
来源：raft-daemon 源码实测（`@botiverse/raft-daemon@0.72.2`，Frank 让研究的）+ Frank 三张截图（矛盾聚合卡 / 时间排序错 / 运行时通知）+ 团队讨论
关联：#351（Activity 收敛）/ #301/#302（agent_activity_event 事件写点+visibility）/ #346 观测北极星段5 / #349 Activity 入口

## 0. 一句话

> **multica 的 Activity = 1:1 抄 raft 的时间线事件流**：一条按时间戳排的事件流，每条是人话叙事，所有事件类型都进、不筛不藏。**砍掉** `Last30dSection`（29 runs/成功率）和 `RecentWorkSection`（"nothing finished"）那两张自相矛盾的聚合卡。

## 1. 病根（为什么现在乱）

Activity 被拆成**多个面、多套数据模型、各有各的 bug**，没一个对齐 raft：
- **详情页 ActivityTab**：聚合卡自相矛盾（"29 runs 97%成功" vs "hasn't completed anything"——run 层 vs work 层硬凑一屏）；RecentWork 按 `chat_session_id` 过滤掉 chat 类 run，导致 directed-channel 的 no-reply run 根本不显示。
- **hover 卡（actor-profile-popover）RECENT ACTIVITY**：FE 没排序、按接口顺序渲染（"5m ago"排在"4m ago"前=反了）；handle 显示占位符"@actor"不是真名。
**修法不是逐个打地鼠，是把所有 Activity 面统一到 raft 的事件流模型 + 归并数据源。**

## 2. 事件类型（照 raft-daemon 源码，别自己发明）

**ActivityKind（事件种类）**：`thinking` / `tool_call` / `tool_output` / `turn_end` / `session_init` / `compaction_started` / `compaction_finished` / `compaction_stale` / `wake_attempt` / `review_finished` / `error` / `text` / `system` / `transport` / `telemetry` / `blocked` / `custom`

Compaction 对齐 `raft-computer@1.0.15`：开始、完成进入 Timeline；5 分钟未见完成追加一次 stale，后续 heartbeat 只更新 Snapshot；恢复输出可推断遗漏的完成。完成仍是 Working，Idle 只由 turn end 产生，compaction 不触发进程重启。

**人话标签（用户看到的行）**：
- Thinking · Output · Working · Message received
- Send held by freshness check · Runtime Profile notice
- `tool_call` 按操作显示 → Reading file / Editing file / Writing file / Viewing file / Searching files / Uploading file / **Running command**

**可见性分级**（raft 源码里就有，对应 #302 visibility 字段）：`user_facing` / `diagnostic` / `internal_progress` / `per_turn` / `persistent`
→ Frank 要"全显示" = 对齐 raft 的 **ACTIVITY DIAGNOSTICS** 视图（全级别都摆）。multica 照搬这套 visibility 分级。

## 3. 数据源（两个互补源归并，不是漂移）

**关键区分**：这次是"两个互补源合并展示"（各存不同事件），**不是**"同一事实存两处"（channel_read/conversation_member 那种漂移）——所以放心归并。

| 层 | 事件 | 源 | 状态 |
|---|---|---|---|
| **run 级** | thinking / output / tool_call（Reading/Running command…）/ working | `task_message`（现成 `buildTimeline`） | 现成，FE 直接用 |
| **agent 级** | Send held by freshness / 运行时通知 / wake_attempt / session_init / system | `agent_activity_event`（#301/#302） | #301 需 emit 这些非-task 事件（带 kind+timestamp+visibility） |
| ~~聚合/状态~~ | ~~成功率/run count~~ | ~~聚合查询~~ | **砍**（矛盾卡，以后作 trust-signal 单独干净做） |

**FE 把 task_message + agent_activity_event 两路按 timestamp 归并成一条时间线** = 完整 raft 事件流。时间排序 bug 在归并里天然修（统一按 timestamp 排）。

## 4. 呈现

- **结构**：raft 式时间线（每条：时间戳 + 类型点/图标 + 人话标签 + 可选详情）；
- **内容叙事化**（"事件流"≠"raw log 灌"，raft 每条本来就是人话）：Thinking 折叠、Output 预览+展开、freshness 显 target+原因；
- **Thinking 事件**：summary 行（agent 自 emit 优先、FE 兜底截首句）+ full（点开展）；
- **零歧义照抄目标** = Iris 的 pixel HTML mockup（Felix/Wren 照 mockup 做、不自解读 raft 截图）。

## 5. 所有 Activity 面统一到这一条链

- **详情页 Activity tab**：事件流重构 + 砍两卡（Wren 主，activity-tab.tsx）；
- **hover 卡 RECENT ACTIVITY**：**不单独维护**——收敛成"点了直接开 #349 的面板"（别修两套 Activity），顺带解掉时间排序错 + "@actor"占位符（Felix，建议收敛不各修）；
- **权限**：owner = Activity/Profile/Files；**非 owner = 仅 Profile**（Activity 是观测自己 agent 的面，只给 owner）。

## 6. 分工

- **Wren**：activity-tab.tsx 事件流重构（归并两源 + 砍两卡 + Thinking→Activity 就是流里 thinking 一类 + 时间排序修）；
- **Felix**：side-panel 壳 + tab 权限（非 owner 仅 Profile）+ @mention 点击开 profile + hover 卡收敛到新链路；
- **Iris**：pixel mockup（零歧义照抄目标）+ 折叠/展开 UX + 真机对 raft 验；
- **Barry/Ronan（#301）**：agent 级事件 emit 进 agent_activity_event（freshness-held / 运行时通知 / wake / system，带 kind+timestamp+visibility）——Wren agent 级那半的硬前置。

## 7. 待拍/确认

1. @Barry 确认 agent_activity_event 现在已 emit 哪些 kind（task_complete/failed 等已有？）、freshness-held/thinking/runtime-notice 还需补哪些；
2. hover 卡收敛到 #349 面板（我倾向 yes，别维护两套）——Frank 拍；
3. visibility 分级默认视图给用户看哪些级（全给=raft diagnostics 视图 / 还是默认 user_facing、diagnostic 折叠）——Frank 说"全显示"倾向前者，确认。
