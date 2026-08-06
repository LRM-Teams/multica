# Research Canvas Showcase Design (2026-08-06)

> **性质**：对现有调研画布的**增量设计文档**（非从零重建）。
> **基线**：最新 `origin/dev` 的研究画布体系已完整（聚合树、Git 拓扑、reorg 动效、轨迹动效）。本文只定义在这套成熟体系之上，还能做出"令人惊叹、有增量"的三个展示增强。**不改后端，不引入平行抽象。**
> **关联**：`2026-07-29-research-fleet-design.md`、`2026-07-29-research-session-canvas-ui-design.md`。

## Summary

调研会话画布（`/research/session/:id`）当前已是一套高度成熟的产品：

- **聚合/聚类**（LRM-1278/1295）：后端投影 `parent_id`/`child_ids`/`descendant_count`/`theme_key`/`assessment`，前端 `aggregate-tree.ts` 消费成 root→branches→leaves 三栏聚合视图（`layoutAggregateTreeShell`）。
- **Git 拓扑**（LRM-1091/1116）：无聚合投影时回退到分支泳道式画布（`git-topology.ts` + git gutter）。
- **动效**（LRM-1335）：`canvas-reorg-motion.ts` 实现 P0–P5 分阶段重组动画（`data-reorg` 协议）、`semantic-aggregation-motion`（split/merge/regroup）、`node-enter-motion`、`node-enter` 入场。
- **键盘 / 无障碍**：全键盘导航、`live region`、safe-centre 相机、`prefers-reduced-motion` 全程尊重。
- **画布选择器**：`research-canvas-layout.ts` 的 `layoutResearchCanvas(nodes, edges)` 自动决定 `aggregate` 或 `git`。

本文定义的三个增量，全部建立在这一体系之上，**复用现有动效协议，不重写核心**：

1. **视图切换器（View Mode Switch）** —— 聚合 ⇄ Git 双向手动切换，复用 `data-reorg` 过渡动画。
2. **拖拽归簇预览边（Drag-to-cluster Preview Edge）** —— desktop 画布节点可拖，拖到目标节点显示临时"合并预览边"（非持久化）。
3. **聚合入场脉冲（Merge-Entry Pulse）** —— 新被 `supports` 汇入的 finding 节点入场时短暂高亮。

## Goals

1. **给用户"掌控感"**：聚合模式与 Git 模式是两种同等的、可选的观察视角，而不是后端数据决定的隐藏实现细节。
2. **让"信息正在汇聚"变得可见**：当一个 finding 开始被多个来源 `supports` 汇入时，用户第一眼就能看到"这里正在形成结论"。
3. **为"探索→结论"增加仪式感**：节点归簇的交互具有"把散点收拢成一个结论"的隐喻，拖动预览边是它的轻量前戏。
4. **严格零后端、零 API 改动**：全部是前端 `packages/views` + `packages/core`(仅纯函数) 的增量。
5. **不让现有系统回退**：reorg/entry/motion 全部保持现状；新交互在 `prefers-reduced-motion` 下自动降级。

## Non-goals

- 不重写 `layoutResearchGraph` / `layoutAggregateTreeShell` 的几何。
- 不改变 `research-canvas-layout.ts` 的 `auto` 决策逻辑（除新增 `forceMode` 覆盖）。
- 不持久化任何拖拽结果（后端图是唯一事实源，前端只画临别预览）。
- 不新增节点图标库 / 插画资产。
- 不做移动端画布拖拽（移动端走 `ResearchGitList`，非画布）。

## 三个增量

### 1. 视图切换器（聚合 ⇄ Git）

**问题**：当前 `layoutResearchCanvas` 全自动选模式（有完整 LRM-1278 投影 → `aggregate`，否则 → `git`）。用户没有任何办法在两种视角间切换，也无法在"后端数据完整但想看重分支流"时切到 Git，反之亦然。

**设计**：

- `layoutResearchCanvas(nodes, edges, options?: { forceMode?: "auto" | "aggregate" | "git" })`
  - `"auto"`：保持现状（纯契约驱动）。
  - `"git"`：强制 `layoutResearchGraph`。
  - `"aggregate"`：强制 `layoutAggregateTreeShell`；当投影不完整（`selectAggregateTree` 返回 `incomplete`）时，用父/子集合近似近似构建，仍缺关键父时**回退 `git` 并 `console.warn`**（不允许渲染一个编造的树）。
- 画布 UI 增加 `viewMode` state（默认 `"auto"`）；Dock 上放一个 **视角切换按钮**（桌面 dock 右侧、fit 旁边），图标在"聚合树 / Git 分支"间切换，`aria-pressed` 表达当前手动模式。
- **切换动画**：模式切换会改变 `laid` 的节点集合/位置 → 现有 `classifyCanvasDelta` 会把它判为 `reorg` → 现有 `data-reorg` P0–P5 过渡自动播放。**不新写动画管线**。唯一需要做的是让 `startReorg` 除了"节点 delta"外，也能在"mode 值变化"时被触发（一个很薄的扩展）。
- **i18n**：仅 `en` + `zh-Hans`（`locales/parity.test.ts` 强制两语 key 一致）。新增 `dock.toggle_view`、`dock.view_aggregate`、`dock.view_git`。

**Acceptance criteria**：
- 用户点击切换按钮，画布在"聚合三栏"与"Git 分支"两种形态间过渡，伴随一次已有的 reorg 动效。
- 切换按钮 `aria-pressed` 正确表达手动模式；`auto` 退化为"契约决定"。
- `prefers-reduced-motion` 下切换仍即时完成（无位移动效）。
- `paragraph` 级：`mode` 变化后 top-left Panel hint（`aggregate_hint`/`git_hint`）同步更新。

### 2. 拖拽归簇预览边（Drag-to-cluster Preview Edge）

**问题**：画布目前完全只读（`draggable:false`）。用户想表达"这几点其实是一簇"时，没有任何手段。

**设计**：

- **仅 desktop 画布**节点设 `draggable:true`（`gitGutter`/band 保持不可拖）。移动端不动（走 Git list）。
- 拖拽过程中（React Flow `onNodeDragStart`/`onNodeDrag`/`onNodeDragStop`）：
  - 记录拖动 source 节点；
  - 每帧做**命中检测**（新纯函数 `resolveDropTarget(draggedId, position, nodes)` → 命中"除自身外最近的节点"或 `null`）；
  - 命中时画一条**临时虚线预览边**（source → target），并对 target 加一圈高亮环；未命中不画。
- **释放即清**：`onNodeDragStop` 清空预览。**不持久化、不进数据模型、不写快照**——后端图仍是唯一事实源（LRM-1278 字段由后端管理，前端绝不自造 parent/child）。
- **样式**：预览边用语义 token（虚线 `var(--brand)`）；target 高亮环用品牌半透明 `outline`/`ring`；全程 `prefers-reduced-motion` 下不做位移动效。
- **纯函数可测**：`resolveDropTarget` 独立于 React Flow，便于单测。

**Acceptance criteria**：
- desktop 画布节点可拖；拖到另一节点上时出现虚线预览边 + target 高亮。
- 拖出/松开后预览消失，无任何数据写入。
- 命中检测：拖到自身返回 `null`；多节点时取最近的；空画布返回 `null`。
- 键盘导航 / a11y 不回归（拖拽是可选的鼠标增强；普通选中仍走 click）。

### 3. 聚合入场脉冲（Merge-Entry Pulse）

**问题**：当新的 `foundings` 被 `supports` 边汇入时（来源入库自动投影 `finding`），现有入场只是普通 `NODE_ENTER_CLASS` 淡入，没有表达"这条正在形成结论"。

**设计**（最小、可靠）：
- 新纯函数 `newlySupportedFindingIds(prevNodes, nextNodes)` → 找出"**新出现的、且有入边 `supports`** 的 finding id"。
- 这批节点挂一个 **one-shot 脉冲类 + `data-entered-pulse`**（CSS keyframes，短促的品牌高亮→正常），**只播一次**（class 一次性，不重复）。
- 复用 `evidence-pulse` 的 **`key`-remount + CSS sweep** 模式：这次"被汇入"的节点 remount 一次 pulse key，播完即静。
- `prefers-reduced-motion`：去掉脉冲类（保普通入场）。
- **注意**：`research-evidence-pulse.tsx` 是**抽屉级证据概览条**，不是节点级——本文的脉冲是"节点级、被汇入时"的新增，不复用它，只借鉴它的 remount-sweep 模式。

**Acceptance criteria**：
- 新增 finding 且带 `supports` 入边 → 入场时带脉冲类 + `data-entered-pulse`。
- 新增 finding 但无 `supports` 入边 → 只走普通入场，无脉冲。
- 既有 finding 不变 → 不触发脉冲。
- reduced-motion 下无脉冲 class。

## 复用关系（不新建平行抽象）

| 复用对象 | 位置 | 被哪个增量怎么复用 |
|---|---|---|
| `layoutResearchGraph` / `layoutAggregateTreeShell` | `lib/layout-graph.ts` | 增量1 的两种目标形态（原样调用） |
| `layoutResearchCanvas` | `lib/research-canvas-layout.ts` | 增量1 加 `forceMode` 覆盖 |
| `classifyCanvasDelta` / `buildNodeSnapshotMap` / `reorgTransitionCss` | `lib/canvas-reorg-motion.ts` | 增量1 的切换动画 / 增量2 的过渡 |
| `startReorg` / `data-reorg` 协议 | `components/research-canvas.tsx` | 增量1 触发源扩展（薄） |
| `NODE_ENTER_CLASS` / `nodeEnterMotionCss` | `lib/node-enter-motion.ts` | 增量3 的入场基底 |
| remount-keyed CSS sweep 模式 | `components/research-evidence-pulse.tsx` | 增量3 借鉴（非复用组件本体） |
| 语义 token | `packages/ui` | 三个增量全程（禁止硬编码 hex/palette-500） |

## Package / platform rules

- 全部 UI 在 `packages/views/research/**`（无 `next/*` / `react-router`）；纯函数可入 `packages/views/research/lib/**`（不涉及 store）。
- 新增纯函数（`drag-merge-preview.ts` / `aggregation-pulse.ts`）放 `lib/`，**不进** `packages/core`（那是共享业务逻辑，本增量是画布专属视图逻辑）。若未来需要跨 app 复用再提升。
- 语义 token 优先；Research accent 沿用现画布 CSS 变量。
- i18n：`en` + `zh-Hans`（`locales/parity.test.ts` 强制一致）。
- 新依赖：不新增任何 npm 包（React Flow 已在）。

## Accessibility & motion

- 键盘：视图切换按钮可 Tab 到、可 Enter 触发；拖拽完全是鼠标增强，键盘路径不变。
- `prefers-reduced-motion`：三个增量全部自动降级为即时切换 / 无动效 / 无脉冲。
- 焦点/aria：切换与预览边都有明确的 `aria-pressed` / `aria-label` / `data-*` 供测试与读屏。

## Acceptance criteria（汇总）

1. 视图切换：点击按钮，画布在聚合⇄Git 间过渡（带 reorg 动效）；aria-pressed 正确；panel hint 同步。
2. 拖拽预览：desktop 节点可拖，拖到目标出现虚线预览边 + 高亮，释放即清，零持久化。
3. 入场脉冲：被 supports 汇入的 finding 有一次性脉冲，普通入场无脉冲，reduced-motion 无脉冲。
4. 全量现有 research 测试保持绿；`pnpm react:doctor` 无新诊断。

## Implementation slices (separate PRs)

1. **视图切换器**（`research-canvas-layout` + `forceMode` + Dock 按钮 + `startReorg` 触发源扩展 + i18n + 测试）
2. **拖拽归簇预览边**（`drag-merge-preview.ts` 纯函数 + canvas 拖拽接线 + 测试）
3. **聚合入场脉冲**（`aggregation-pulse.ts` + canvas 接线 + CSS + 测试）
