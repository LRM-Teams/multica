# Research Live Canvas V6 — Agent、Task 与 Attempt 展示规格

> 目标是让用户知道“谁在干活、干什么、是否真的启动、有什么进展、为何停住”。

## 1. Live Agent Deck 单元

```text
┌──────────────────────────────┐
│ 头像  Agent 名       [running]│
│ 任务：核验供应商地区条款      │
│ 当前：读取第 3/8 个来源        │
│ 03:18 · 最近信号 12s · ⚑1     │
└──────────────────────────────┘
```

固定 4 行，不将全部字段拼成一段：

1. identity：头像、姓名、角色、phase；
2. task：当前 task objective/title；
3. action：Attempt 当前动作/阶段；
4. telemetry：时长、最近信号、产出/风险计数。

## 2. 状态真相

| UI state | 必需事实 | 禁止误判 |
| --- | --- | --- |
| queued | task assigned，Attempt 未 runtime start | 不能显示计时中的 running |
| running | `runtime_started_at` 或等价不可变启动事实 | 头像在线不算 running |
| cancelling | cancellation requested，未 completed | 不能显示 idle/failed |
| done | succeeded + terminal/result | 不能因 prose“完成”判 done |
| failed | failed/lost/cancelled terminal | 必须区分 failure class |
| stale | Presence 过期或 lease 过期 | 不自动改 Attempt terminal state |
| idle | roster active 且无执行事实 | 不从无消息推断离线 |

## 3. 排序与分组

Deck 排序：blocking failed → cancelling → running → queued → stale → idle → done。

- 同一状态按 `runtime_started_at/updated_at` 由新到旧。
- Agent stable key 永远是 agent id，排序 FLIP 不重建卡片。
- collapsed Deck 显示：`2 异常 · 5 运行 · 3 排队 · 12 人`；点击展开。
- 不能让 done/idle 占满首屏，超过 4 个收进“其他 7 人”。

## 4. Agent ↔ Canvas 双向定位

### Deck → Canvas

点击 Agent：

1. Lens 切到 execution；
2. 选中其当前 attempt node，若无 attempt 则 task node；
3. 若不在 loaded Slice，按 `node_id` 请求局部 Slice；
4. camera 自动移到 safe centre；
5. Inspector 打开 Agent/Attempt 详情。

### Canvas → Deck

点击 task/attempt：Deck 自动展开并滚动到负责人，单元加 2s 静态定位 highlight；不循环闪烁。

## 5. Attempt Inspector

### 现在发生什么

- attempt number + status；
- assigned Agent；
- dispatched/runtime started/last observed；
- 已运行或已等待时长；
- lease expiry 与是否存在风险；
- inbox task/dispatch key 作为高级诊断，可复制但默认折叠。

### 已产生什么

- 最近 accepted result artifact；
- 产生的 Claim/Observation/Source 数量；
- 解决的 Question/Dispute 引用；
- result submitted/completed 时间。

### 为什么停住

- failure class + diagnostics；
- pending failure 与 retryable；
- cancellation requested/completed；
- blocked dependency/等待用户（若 canonical detail 提供）。

### 下一步

- retry/reassign 只在服务端允许时出现；
- continue/fork 出现在 question/task anchor；
- 每个错误说明可执行下一步，不只写“失败”。

## 6. 计时与刷新

- 页面只建立 1 个共享 clock tick；running 每秒更新，queued/stale 每 10 秒更新，后台 tab 降频。
- 相对时间用 `Intl.RelativeTimeFormat`，持续时间用 tabular numerals。
- WS presence patch 是部分更新；收到后 invalidates/refetch 完整 roster，不删除未出现在 patch 的 Agent。
- offline 时停止“实时”字样，时长仍可从已知时间计算但标“离线估算”。

## 7. 状态视觉

- running：细 activity rail + play glyph；不使用无限 scale pulse。
- queued：空心 queue glyph + 静止虚线轨。
- cancelling：stop-pending glyph + amber rail；占用排序高位。
- failed：destructive 小面积标记 + failure class 文案。
- stale：clock-alert glyph + stale reason，和 failed 分开。
- idle：低对比但文字可读，不降到 opacity<0.5。

## 8. 窄屏

- 底部入口显示 `5 运行 · 2 异常`。
- sheet 首屏先异常和 running；每个单元高度 84–104px。
- 点击 Agent 后 sheet 收起，Canvas 聚焦；保留返回“Agent 列表”的 breadcrumb。
