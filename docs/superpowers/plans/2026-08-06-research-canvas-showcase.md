# Research Live Canvas V6 — 并行实现计划

> **Status: historical slice plan.** Most listed slices have landed as
> independent modules. Production integration and remaining work are tracked
> by `docs/research/constellation-d5-product-contract.md`; unchecked items in
> this file must not be interpreted as the current implementation status.

> **设计合同**：`docs/superpowers/specs/2026-08-06-research-canvas-showcase-design.md`
> **基线**：最新 `origin/dev`，所有 PR base=`dev`。
> **执行方式**：10 个 Agent 各领 1 个独立 Issue、独立 worktree/branch/PR；完成后再领下一项。
> **视觉分工**：编码 Agent 只读文字规格、源码、DOM、类型和测试；主多模态 Agent 检查截图/录屏并开小修复 Issue。

## 1. 目标与顺序

先让真实数据可用，再做卡片、组合、状态和动画。任何视觉实现都不得绕过统一 Canvas adapter。

```text
V5/V6 Adapter ─┬─ Live Projection ─┬─ Node Registry ─┬─ Insight Tree
               │                   ├─ Agent Overlay  ├─ Dispute UI
               │                   └─ Plugin Shell   └─ Trajectory Explorer
               └──────────────────────── Performance / a11y / visual gates
```

当前 10 个已建 Issue 与本计划一一对应，不再创建重复 Issue：

| Issue | Slice | Owner 目录 |
| --- | --- | --- |
| LRM-1484 | V5/V6 Session Adapter | `packages/views/research/v6-session-adapter/**` |
| LRM-1483 | Projection Snapshot/Delta/Resync Hook | `packages/core/hooks/research-v6-live/**` |
| LRM-1475 | 30-kind Node Renderer Registry | `packages/views/research/node-renderers/**` |
| LRM-1476 | Recursive Insight Tree | `packages/views/research/insight-tree/components/**` |
| LRM-1477 | Semantic Transition Runtime | `packages/views/research/motion/**` |
| LRM-1478 | Dispute/Deliberation/Decision UI | `packages/views/research/dispute/**` |
| LRM-1479 | Agent/Attempt Live Overlay | `packages/views/research/execution-overlay/**` |
| LRM-1480 | Git Trajectory Explorer | `packages/views/research/trajectory-explorer/**` |
| LRM-1486 | Canvas Plugin Shell | `packages/views/research/canvas-plugins/**` |
| LRM-1485 | 10k Performance & Regression Gate | `packages/views/research/performance/**` |

### Issue 必读子规格

| Issue | 必读文档 |
| --- | --- |
| LRM-1483/1484 | data-contract、viewport-performance |
| LRM-1475 | node-card、route-topology、layout、data-contract |
| LRM-1476 | node-card、route-topology、viewport-performance、现有 insight-tree design |
| LRM-1477 | motion-direction、route-topology、viewport-performance |
| LRM-1478 | layout、node-card、motion-direction、现有 dispute design |
| LRM-1479 | agent-execution、layout、node-card、data-contract |
| LRM-1480 | route-topology、viewport-performance、motion-direction、现有 trajectory design |
| LRM-1486 | route-topology、layout、viewport-performance、全部组件公开接口 |
| LRM-1485 | 7 份子规格的预算与验收章节 |

## 2. 全体开发合同

### 数据边界

- Renderer 只读 `CanvasSnapshot/Node/Edge/Delta/Slice`；V5/V6 wire shape 只出现在 adapter/gateway。
- React Query 管 Snapshot、Delta、Slice、Presence、detail；Zustand 只管 lens、selection、fold、viewport、filters、panel。
- WS 只 invalidate/query 或交给投影客户端按序应用，不直接写 Zustand。
- 未知 kind/edge/transition 降级；不得用 `as` 跳过 API 解析。
- Fixture 只进入 test/demo，不得被生产 import。

### 并行边界

- 每个 Slice 只修改表中独占目录。
- 公共接线集中到 `canvas-plugins`；其他 Slice 导出组件、hook 或 adapter，不争抢 `research-canvas.tsx`。
- 必须修改共享文件时，只增加 export/i18n/注册项，单独提交，PR 描述列出重叠风险。
- 不进入其他 Agent worktree；从最新 `origin/dev` 建自己的 branch。

### 完成定义

- 生产代码、相关测试、非 Draft PR；PR base=`dev`。
- `pnpm --filter @multica/views exec vitest run research` 或对应 core 测试。
- `pnpm typecheck`、`pnpm react:doctor`。
- 视觉 Slice 生成截图/录屏 artifact，只报告路径、尺寸、命令和 URL；多模态 Agent 查看内容。
- 没有 PR、只有设计说明或只有 demo，均不算完成。

## 3. Slice 1 — V5/V6 Session Adapter（LRM-1484）

**类型**：AFK，可立即开始。

### What to build

建立单一 session gateway：探测 V6 能力，可用时读取 `ResearchV6Snapshot`，不可用时读取现有 V5 session snapshot；两条路径都输出 `CanvasSnapshot`。V6 route 404/501 是“能力不可用”，解析失败是“接口错误”，不能静默降级掩盖坏数据。

### Acceptance

- V5/V6 fixture 输出相同统一模型语义。
- 未知 V6 kind 变 `generic`，保留 raw kind/detailRef。
- malformed response 通过 schema 进入明确错误，不白屏。
- gateway 暴露 `source: v5 | v6` 与 capability diagnostics。

### Blocked by

None。生产 V6 成功路径受服务端路由落地约束，但 adapter 可先合并。

## 4. Slice 2 — Projection Live Hook（LRM-1483）

**类型**：AFK，可立即开始。

### What to build

在 React Query 上封装 Snapshot 首载、Delta 页面、WS resume、乱序 buffer、8s gap、单次 resync 与取消过期请求。复用现有 `ResearchV6ProjectionClient`，不再写第二个 reducer。

### Acceptance

- duplicate/out-of-order/straddle/gap/resume fixture 终态正确。
- 同时出现多个 resync 原因只发 1 次 Snapshot 请求。
- runId 变化取消旧请求；旧响应不能覆盖新 Run。
- 暴露 `lastConfirmedSequence/syncPhase/awaitingSequence` 给 Chrome。

### Blocked by

None。与 Slice 1 通过公开 gateway 接口组合。

## 5. Slice 3 — 30-kind Node Renderer（LRM-1475）

**类型**：AFK，可立即开始。

### What to build

实现 6 个卡片族、`NodeCardShell`、30-kind registry、Generic fallback、八态与 Landmark/Waypoint/Trail Dot 三档语义 LOD。这里只输出节点渲染形态，不实现曲线路径布局。所有事实来自 `CanvasNode` 和显式 detail，不解析 summary。

### Acceptance

- 30 kinds 每类至少 1 个测试；未知 kind 不崩溃。
- default/selected/loading/running/failed/stale/unknown/terminal 八态。
- Landmark/Waypoint/Trail Dot 共享稳定 node id 和 anchor；25% zoom 不把正文卡缩成白色小块。
- 长标题、空 summary、RTL/中英文、200% zoom 不溢出。
- icon button、focus-visible、非颜色状态编码通过。

### Blocked by

None。用统一 Canvas fixture 开发；接入依赖 Slice 10。

## 6. Slice 4 — Recursive Insight Tree（LRM-1476）

**类型**：AFK，可立即开始。

### What to build

实现 canonical Insight/Derivation 逐层展开、Route Bundle/Display Group 折叠、stale 祖先路径、最小重新整合入口和 Slice load-more。Display Group、Route Bundle 与 Insight 必须有完全不同的结构和 accessible name。

### Acceptance

- 4 Claim → 2 L1 → 1 L2 fixture 可摘要/展开。
- 折叠显著减少 DOM；选中、viewport anchor、pinned path 不丢。
- 第 4 层及更深转为 Route Bundle；展开后仍服从总量预算，不能恢复为全量小卡树。
- stale 路径不只靠颜色；权限撤销不泄露正文。
- `canExpand=false` 不显示展开；分页不产生重复节点。

### Blocked by

None。通过 props 接收 `CanvasSlice`；接入依赖 Slice 10。

## 7. Slice 5 — Semantic Transition Runtime（LRM-1477）

**类型**：AFK，可立即开始。

### What to build

实现 10 个 `transition_kind` 到 motion directive 的确定性映射，并补齐 Route Sprout、Detour Trace、Dead-end Settle、Retry Hairpin、Convergence、Semantic LOD Morph、Ambient Probe；支持可中断队列、coalesce/backpressure、Reduced Motion、低性能和后台/resync 终态策略。

### Acceptance

- 10 kinds 均有确定性 directive；unknown/null 无危险动画。
- 100 delta 队列峰值 ≤64，终态与无动画重放一致。
- 只动画 transform/opacity，不使用 `transition: all`。
- background/resync 不补播历史；新 delta 能中断旧动画。
- 同屏 active route probe ≤2；失败不抖动，retry 保留旧失败路径，LOD 不连续缩放文字。

### Blocked by

None。只输出 directive，不修改 canvas 布局。

## 8. Slice 6 — Dispute/Deliberation/Decision（LRM-1478）

**类型**：AFK，可立即开始。

### What to build

实现争议卡、立场扇面、typed evidence relations、讨论时间线、Director escalation、Decision history 与 reopen。详情面板提供双向节点定位；不能从文本推断立场。

### Acceptance

- 3 positions、多 turn、deadlock → escalation → decision → reopen fixture 完整浏览。
- supports/contradicts/refines 与 4 个生命周期状态均有非颜色编码。
- 历史 Decision 在 reopen 后保留并标 superseded/invalidated。
- 键盘可从 dispute 到 position/evidence/decision 并返回。

### Blocked by

None。接入依赖 Slice 10。

## 9. Slice 7 — Agent/Attempt Live Overlay（LRM-1479）

**类型**：AFK，可立即开始。

### What to build

消费 Presence v2 与 task/attempt node detail，形成 LiveAgentDeck、Attempt detail 和 Agent↔节点双向定位。Presence 是 roster/派生视图，Attempt ledger 是执行真相。

### Acceptance

- idle/queued/running/done/failed/stale 与 attempt `cancelling` 可区分。
- 无 `runtime_started_at` 或等价事实时不显示 running。
- lease 即将过期、过期、离线、pending failure 有不同说明和下一步。
- 排序不破坏焦点，时长由统一 clock 更新，不每卡各建 interval。

### Blocked by

None。通过 `nodeId/agentId` callback 接入 Slice 10。

## 10. Slice 8 — Git Trajectory Explorer（LRM-1480）

**类型**：AFK，可立即开始。

### What to build

独立多泳道 Git 视图，主节点使用卡片、密集提交使用可命中的微圆点/路径束，支持 Agent/branch/relation filter、时间/逻辑排序、minimap、详情和 jump-to-canvas。路线允许平滑弯曲、分叉、交错与汇入，但不能随机变化。复用 lane layout、window slice 和 motion 基建。

### Acceptance

- ≥8 条交错轨迹可追踪起点、分叉、合并、终止和负责人。
- 10k fixture 首屏只建 window+overscan DOM。
- 筛选前后 lane 稳定；selected id 不丢。
- 点击卡片 → 详情 → 跳画布聚焦同一 node id。
- 失败、retry、cancelled、accepted 具有颜色 + 线型 + 端点三重编码；密集轨迹不会变成等距卡片墙。

### Blocked by

None。通过 callback 与 Slice 10 组合。

## 11. Slice 9 — Canvas Plugin Shell（LRM-1486）

**类型**：AFK；集成时依赖各 Slice 的公开 export，壳本身可先做。

### What to build

建立 lens/plugin registry、lazy boundary、局部 error boundary、Inspector slot、Agent Deck slot、Trajectory entry、URL state adapter，并在 `canvas-plugins/route-map/**` 落地有机路线层：稳定 cubic Bézier、Landmark/Waypoint/Trail Dot/Route Bundle 组合、spatial hit-test、局部布局和 safe-region 避让。`research-canvas.tsx` 只接一次插件壳，禁止后续 6 个并行 PR 都修改它。

### Acceptance

- 单个插件失败不使 Canvas 白屏；可重试并报告插件名。
- lens/node/view 可 deep-link；viewport/fold/motion 不污染 URL。
- 插件 lazy load 有局部 skeleton，不锁画布。
- Web/Desktop 共享，零 `next/*`/`react-router-dom`。
- 最终主图不能使用 orthogonal router、每层等距列或所有节点同尺寸卡；Snapshot 相同则路线坐标一致，Delta 只移动 affected corridor。

### Blocked by

最终接线依赖 Slice 3–8 的 exports；先合并 registry/interface 再逐项注册。

## 12. Slice 10 — 10k Performance & Quality Gate（LRM-1485）

**类型**：AFK，可立即开始；验收随其他 Slice 增量扩充。

### What to build

建立 10k fixture、DOM/布局/Delta/motion budgets、25% 语义 LOD、弯路/失败/retry fixture、200% zoom、keyboard、Reduced Motion、unknown kind、malformed response 和视觉 artifact 生成脚本。这个 Slice 写门，不替其他 Slice 实现功能。

### Acceptance

- 首屏不全量下载；desktop semantic node ≤180、图节点 DOM ≤220、Landmark Card ≤48；25% zoom Landmark Card ≤12；Trajectory 只渲染 window+overscan。
- Delta 仅重算 affected roots；未受影响位置稳定。
- 100 delta 终态 hash 一致；gap/resync 单次。
- 360/768/1440、200% zoom、亮暗主题、Reduced Motion 有自动断言或 artifact。
- route geometry 不含最终 orthogonal tree；失败端点、retry hairpin、stale/争议路径在灰阶下仍可区分。

### Blocked by

None。每个合入组件必须补入对应 gate。

## 13. 合并顺序

1. Slice 1、2、3、5、7、10 可并行。
2. Slice 4、6、8 可并行，通过 props/callback 保持独立。
3. Slice 9 先合 registry/interface，再按已合并 exports 做小接线 PR；不要等全部完成后做一个大集成 PR。
4. 每个 PR 合并后，负责人立即领取下一个未完成接线或视觉修复 Issue。

## 14. 多模态验收流程

1. 编码 Agent 按文字 contract 实现并生成 artifact，不读取图片。
2. 主多模态 Agent 在真实页面检查：层级、动效、状态、长文本、亮暗主题、断点、焦点与视觉噪声。
3. 每个视觉偏差单独建小 Issue，写明截图区域、期望 token/尺寸/时长、目标文件和回归断言。
4. 空闲 Agent 领取 1 个小修复；提交 PR 后继续领取，不等待集中验收。

## 15. 当前后端依赖

以下缺口不能由前端伪造：

- V6 projection snapshot/delta/resume 服务端路由；
- 生产 Projection Slice gateway；
- 完整 Insight Derivation/Dispute/Deliberation canonical 实体落地。

前端可以先完成 adapter、组件、fixture 和能力探测。生产 UI 只能在接口真实可用时显示对应功能；否则显示明确的不可用/旧版能力状态。
