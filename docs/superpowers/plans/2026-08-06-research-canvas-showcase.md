# Research Canvas Showcase — implementation plan

> **关联设计文档**：`docs/superpowers/specs/2026-08-06-research-canvas-showcase-design.md`
> **基线**：最新 `origin/dev`。本 plan 是**纯前端、零后端、零 API** 的三个展示增量；每个切片一个独立 PR，base=`dev`。
> **实现者**：外部编码 Agent（一个切片 = 一个 worktree + 一个分支 + 一个 PR）。

## Goal

在已成熟的调研画布（聚合树 + Git 拓扑 + reorg 动效）之上，交付三个令人惊叹、有增量、可独立验收的展示增强：

1. **视图切换器**：聚合 ⇄ Git 双向手动切换，复用 `data-reorg` 过渡动画。
2. **拖拽归簇预览边**：desktop 节点可拖，拖到目标显示临时合并预览边（非持久化）。
3. **聚合入场脉冲**：新被 `supports` 汇入的 finding 一次性高亮。

## Status

- [ ] **切片 1：视图切换器**（PR 1）
- [ ] **切片 2：拖拽归簇预览边**（PR 2）
- [ ] **切片 3：聚合入场脉冲**（PR 3）
- [ ] 文档（本文件 + spec 已就绪；实现后按需更新 Status）

---

## 切片 1：视图切换器

**PR title：** `feat(research): aggregate ⇄ git view mode switch`

### 接口改动

`packages/views/research/lib/research-canvas-layout.ts`

```ts
export type ResearchCanvasViewMode = "auto" | "aggregate" | "git";

export function layoutResearchCanvas(
  nodes: ResearchGraphNode[],
  edges: ResearchGraphEdge[],
  options?: { forceMode?: ResearchCanvasViewMode },
): { mode: "git" | "aggregate"; layout: ...; topology: ... }
```

- `"auto"`（默认）：保持现状（`selectAggregateTree` → `ready` 才 `aggregate`）。
- `"git"`：强制 `layoutResearchGraph(nodes, edges)`。
- `"aggregate"`：强制 `layoutAggregateTreeShell`；若 `selectAggregateTreeColumns` 返回 `null`（投影不完整），**回退 `git` 并 `console.warn`**（不渲染编造树）。

### 组件改动

`packages/views/research/components/research-canvas.tsx`

- 加 `viewMode: ResearchCanvasViewMode` state（默认 `"auto"`）；传给 `layoutResearchCanvas(nodes, edges, { forceMode: viewMode })`。
- 扩展 `startReorg` 触发：除节点 delta 外，当 `viewMode` 变化也拨动 `data-reorg="running"` → 现有 P0–P5 过渡自然播放。**方式**：把 `startReorg` 依赖的 trigger 从"仅 `classifyCanvasDelta.kind === 'reorg'`"扩展为"`delta.kind === 'reorg' || viewMode 变化"（薄改，不动 `canvas-reorg-motion.ts` 纯函数）。
- top-left Panel hint 已读 `canvasLayout.mode`，无需改文案逻辑（`aggregate_hint`/`git_hint` 自动跟随）。

`packages/views/research/components/research-canvas-dock.tsx`

- 新增 props：`viewMode?: ResearchCanvasViewMode`、`onToggleView?: () => void`。
- 桌面分支（`layout === "desktop"`）在 detail-toggle 旁加一个按钮：
  - `aria-label={t(($) => $.dock.toggle_view)}`
  - `aria-pressed={viewMode === "aggregate" || viewMode === "git"}`（任一手动模式即 pressed）——**或用三态**：`auto/aggregate/git`（建议三态循环：`auto → aggregate → git → auto`，`aria-pressed` 随明确手动模式 true）。
  - 图标：聚合树 / Git 分支（lucide 内现有集），icon 包 `aria-hidden`。
  - **mobile 分支（`layout === "mobile"`）不渲染**（只渲 rail）。受 dock 源文件 `sm:` 响应式正则禁令约束，必须用 prop/分支控制，不能用 `sm:` 类。
- props 透传：`research-canvas.tsx` 调用 `<ResearchCanvasDock viewMode={viewMode} onToggleView={cycleViewMode} ... />`。

### i18n

`packages/views/locales/en/research.json` + `zh-Hans/research.json`（**两语必须同加，`locales/parity.test.ts` 强制**）：

```jsonc
// dock
"toggle_view": "Toggle research view",   // 切换调研视图
"view_aggregate": "Aggregate tree",       // 聚合树视图
"view_git": "Git lane view"               // Git 分支视图
```

无 ja/ko（现有即无）。

### 测试

- `lib/research-canvas-layout.test.ts` 增：
  - `forceMode: "git"`（对完整契约）→ `mode === "git"`；
  - `forceMode: "aggregate"`（完整契约）→ `mode === "aggregate"`；
  - `forceMode: "aggregate"` + `child_ids: undefined` 夹具 → 回退 `git` + `console.warn`；
  - 无 options → 保持既有 auto 行为。
- `components/research-canvas-shell-a11y.test.tsx`（mock `use-t` 字典加 `dock.toggle_view` 等）增：
  - 桌面 dock 出现切换按钮；点击调 `onToggleView`；`aria-pressed` 翻转。
  - mobile dock 不出现该按钮（`queryByLabelText` null）。
- `components/research-module-rail.test.tsx` 增：同上的按钮最小覆盖（可选）。

### 验证

```bash
pnpm --filter @multica/views exec vitest run research
pnpm --filter @multica/views exec tsc --noEmit
pnpm react:doctor
```

---

## 切片 2：拖拽归簇预览边

**PR title：** `feat(research): drag-to-cluster merge preview edge`

### 纯函数（新）

`packages/views/research/lib/drag-merge-preview.ts`

```ts
export function resolveDropTarget(
  draggedId: string,
  position: { x: number; y: number },
  nodes: ResearchGraphNode[],
): string | null
```

- 返回 `position` 命中（最近、距离 < 阈值、非自身）的节点 id，否则 `null`。
- 与 React Flow 解耦，便于单测。

### 组件改动

`packages/views/research/components/research-canvas.tsx`（desktop 分支）

- `rfNodes` 的 `research` 节点改 `draggable: true`（保留 `gitGutter` 不可拖）；`nodesDraggable` 保持全局但以节点级覆盖。
- 新增 state：`dragState: { sourceId: string; targetId: string | null } | null`。
- React Flow 事件：
  - `onNodeDragStart`：记 `sourceId`，清 `targetId`。
  - `onNodeDrag`：`resolveDropTarget(sourceId, node.position, nodes)` → 更新 `targetId`；目标变化时更新。
  - `onNodeDragStop`：清 `dragState`。
- 绘制：当 `dragState.targetId` 非空时，渲染一条**虚线临时预览边**（source→target）+ target 节点一圈品牌高亮环（`outline`/`ring`，语义 token）。**不进 `rfEdges`**（那是持久化数据模型）——用绝对定位 SVG/Polyline overlay 或 React Flow 的临时 edge state，**跳过快照**。
- **零持久化**：释放即清，不写任何数据。

### 测试

- `lib/drag-merge-preview.test.ts`：
  - 拖到自身 → `null`；
  - 多节点取最近 → 目标 id；
  - 无命中 → `null`；空数组 → `null`；距离过远 → `null`。
- 组件级（可在 `research-canvas` a11y 测试旁加）：断言拖拽态存在时出现 `data-drag-merge-preview` 元素，`onNodeDragStop` 后消失（mock React Flow 事件）。

### 验证

同上（vitest run research / tsc / react:doctor）。

---

## 切片 3：聚合入场脉冲

**PR title：** `feat(research): merge-entry pulse on supported findings`

### 纯函数（新）

`packages/views/research/lib/aggregation-pulse.ts`

```ts
export function newlySupportedFindingIds(
  prevNodes: ResearchGraphNode[],
  nextNodes: ResearchGraphNode[],
  edges: ResearchGraphEdge[],
): string[]
```

- 返回"`next` 中新增、且存在入边 `edge_type === "supports"` 指向它 的 finding node id"。
- 参考：来源入库自动投影 finding 时，`research_ops.go` 会建 `finding` 节点 + `supports` 边。前端消费它即可，无需后端改动。

### 组件改动

`packages/views/research/components/research-canvas.tsx`

- 在现有 `startReorg/effect` 旁边，对 `newlySupportedFindingIds(...)` 返回的节点，挂一次性脉冲 class + `data-entered-pulse`。
- 复用 **remount-keyed CSS sweep** 模式（借鉴 `research-evidence-pulse.tsx` 的 `key={sweepMountKey}`）：给这批节点一个 `data-pulse-key`，remount 触发 CSS one-shot；播完不再触发。
- `prefers-reduced-motion`：不挂脉冲 class（保普通 `NODE_ENTER_CLASS` 入场）。
- CSS keyframes（放 `canvas-reorg-motion` 同族或组件内 `<style>`，用语义 token）：短促品牌高亮 → 正常。

### 测试

- `lib/aggregation-pulse.test.ts`：
  - 新增 finding + `supports` 入边 → 在列表；
  - 新增 finding 无 `supports` → 不在列表；
  - 既有 finding（prev 已有）→ 不在列表；
  - edges 过滤：非 supports 边不算。

### 验证

同上。

---

## Verification（每个 PR 后）

```bash
pnpm --filter @multica/views exec vitest run research
pnpm --filter @multica/views exec tsc --noEmit
pnpm react:doctor
# conditions permitting:
make check
```

## Boundaries / 不做

- 不新增 npm 依赖（React Flow 已在）。
- 不打开移动端画布拖拽。
- 不持久化拖拽结果 / 不自造 parent/child（LRM-1278 由后端投影管理）。
- 不重写 `layoutResearchGraph` / `layoutAggregateTreeShell` 几何。
- 不新增插画/图标资产。
