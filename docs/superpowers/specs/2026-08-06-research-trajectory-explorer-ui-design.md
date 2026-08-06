# Research Git 多轨迹探索器 UI 设计 (UI-06 / LRM-1474, 2026-08-06)

> 状态：**设计交接稿**（design handoff）。已核对 dev@59b2c15f 现存 trajectory 基建。
> 前置文档：
> - `docs/superpowers/specs/2026-08-04-lrm-1394-git-trajectory-visual-qa.md`（视觉门合同）
> - `docs/superpowers/specs/2026-07-29-research-session-canvas-ui-design.md`（同会话画布）
> - 后端 `2026-08-05-autonomous-research-system.md` §7.1/7.2（Projection Slice / transition_kind）
>
> 本文不重复 1394 合同（布局/token/截图矩阵沿用），只补 1394 未覆盖的**独立功能视图组合层**：筛选、排序、缩放/迷你图、聚焦与跳转画布、动画可中断、10k 虚拟化、泳道稳定重排、性能与交互预算。实现 owner 由本群贝克汉姆分配前端。

---

## 1. 问题诊断（现状 gap）

**现状（dev@59b2c15f）**：
- 「轨迹」aux 面板仍渲染 `ExplorationRail`（LRM-1287 的方向→结果叙事列表），**不是 Git 图**。
- `ResearchGitList` 只被 `research-canvas.tsx` 在窄屏当画布回退用，不是独立视图。
- 基建已齐、未组合：
  - `packages/core/research/trajectory-lane-layout.ts`（lane/segment/junction/issue + `sliceTrajectoryLaneLayout`）
  - `components/trajectory-virtual-window.tsx` + `trajectory-performance-fixture.ts`（500/2000 commit 性能门）
  - `lib/trajectory-motion-{intents,controller,animator}.ts` + `hooks/use-trajectory-motion.ts`（可取消/合并/reduced-motion/隐藏 tab 丢弃）
  - `lib/git-topology.ts` + `lib/layout-graph.ts`（画布拓扑）
- 尚无 `packages/views/research/trajectory-explorer/`（本 issue 指定所有权目录）。
- LRM-1394 是**验收门**（截图矩阵、五秒扫读、缺陷路由），非实现规格；它缺「独立视图如何从会话壳进入、筛选/排序/缩放/迷你图/跳画布如何接线」。

**本 issue 要补的 gap**：把底部「轨」从叙事列表升为**独立 Git 多泳道探索器功能视图**——组合上述基建，补齐 1394 未定义的视图层交互。**不**复制整张 canonical 图、不塞进无限画布、不改 lane/DAG 算法、不把显示分组写回为真实 Insight。

---

## 2. 范围与非目标

**范围**：
- 独立视图入口：会话页「轨」模块从 `ExplorationRail` 升级为 `TrajectoryExplorer`。
- 展示派生：trajectory 由 **typed edges + Projection Slice 消费端**派生；允许显示层压缩长直线段，**不推演** canonical research 状态。
- 交互：卡片点击聚焦/展开操作/跳转画布对应节点；筛选（分支/Agent/关系）；时间或逻辑排序；缩放；迷你图；长轨迹虚拟化（10k 不加载全图）。

**非目标（strict 边界，对齐 parallel 约束）**：
- 不进无限画布，不复制 canonical 图。
- 不改 `trajectory-lane-layout` / `git-topology` 的 lane 分配与交叉算法——只用其输出与 `slice`。
- 显示分组（折叠长直线段、聚合分支）只属前端显示状态，**不得**写回为真实 Insight / 节点 fusion；真正的融合只能由后端验收的 `insight_derivation | integrates` 边表达。
- 不做无限画布式自由平移缩放；本视图用「看板内滚动 + 显式缩放」，不得产生 document 级横向滚动。
- 接口未落地部分一律用**严格遵循后端文档的 contract fixture**（`TrajectoryCommit`：`id / branchKey / parentIds / status / label / actorAgentId`），生产路径禁止伪数据。

---

## 3. 信息架构与进入路径

```
会话页 ResearchSessionPage
 ├─ 轨 module → ResearchAuxDrawer("trajectory")
 │     └─ TrajectoryExplorer              ← 本视图（替代 ExplorationRail）
 │           ├─ TrajectoryToolbar（筛选/排序/缩放重置/迷你图开关）
 │           ├─ TrajectoryGraph（虚拟化 Git 图主体）
 │           ├─ TrajectoryMinimap
 │           └─ TrajectoryDetail（展开操作 / 跳转画布）
```

- 保留「一次一个 aux 面板」（LRM-1061）。进入面板仍按用户名/权限判断 `trajectory` 是否可渲染（1394 T10）。
- **桌面（≥1024）**：视图占满抽屉/面板，主图优先；**中宽 768**：详情降为下方或可关闭 overlay（不挤压主图至不可读）；**窄屏 <768**：详情用底部 sheet，不盖主图。

---

## 4. 数据契约与派生（对接后端 §7.1/7.2）

**消费端派生（只读）**：
- 输入：`ResearchGraphNode[]` + `ResearchGraphEdge[]`（React Query `researchKeys.snapshot`）或 contract fixture。
- 本视图私有 adapter 映射到 `TrajectoryCommit`（产出与 `trajectory-lane-layout` 同形）：
  - `id = node.id`
  - `branchKey = 后端 branch id ?? lane(edge) 派生`（未落地时用 fixture 提供的 `branchKey`）
  - `parentIds =` 按 `leads_to | derives_from | integrates` 等 typed 边
  - `status = node.status` 的显示语义（ok/run/fail/wait/mute，沿用 `logic-lanes.resolveLogicStatus`）
  - `actorAgentId = node.actor_agent_id`（负责人）
  - `label.text = node.title`，`label.status`
- 布局：`buildTrajectoryLaneLayout(commits)` → 只消费 `commits/lanes/segments/junctions/issues`。
- 虚拟化：`sliceTrajectoryLaneLayout(layout,{startRow,endRow,overscan})` 只渲染可视窗。**10k 节点**：首屏只渲染窗口 + overscan，其余不建 DOM（复用 `trajectory-virtual-window` 窗口/越界模式与性能门）。

**派生铁律**：
- 不从摘要、聊天文本、动画状态推断 canonical research 状态。
- `missing_parent` issue 一律按 1394 T07 降级为「关系不完整」，**不画伪连线**（lane-layout 已把缺父进 `issues`，前端不得兜底补边）。
- 显示分组/折叠/动画状态属客户端显示，不写回真实 Insight。

---

## 5. 组件与文件归属（target `packages/views/research/trajectory-explorer/`）

组件命名与 1394 §10 优先级一致，落在 `trajectory-explorer/` 独占目录，不另建平行组件。

| 组件 | 职责 | 状态 |
| --- | --- | --- |
| `TrajectoryExplorer` | 视图根：组装工具栏/主图/迷你图/详情；协调筛选排序与选中态 | 新增 |
| `TrajectoryToolbar` | 筛选（分支/Agent/关系）、排序（时间/逻辑）、缩放重置、迷你图开关 | 新增 |
| `TrajectoryGraph` | 虚拟化 Git 图主体：lane 线 + 可点击 commit 卡 + segments/junctions | 新增（消费 `sliceTrajectoryLaneLayout` + `trajectory-virtual-window` 模式） |
| `TrajectoryCommitCard` | 每张 commit 卡（标题/状态双编码/负责人/分支 chip） | 新增 |
| `TrajectoryMinimap` | 主图一致性缩略 + viewport 框 | 新增 |
| `TrajectoryDetail` | 选中卡展开操作 + 「跳转画布」 | 新增 |

**复用（不重写）**：`lib/trajectory-motion-*`、`hooks/use-trajectory-motion.ts`、`core/research/trajectory-lane-layout.ts`、`components/trajectory-virtual-window.tsx` 的窗口/性能模式。

---

## 6. 布局与层级

### 6.1 桌面（1440）

```
┌─ TrajectoryToolbar（≤48px，可折叠；定位/缩放重置/筛选状态仍可达）───────┐
├──────────────────────────────────────────────┬────────────────────┤
│  主 Git 图（剩余高度，垂直滚动手；            │  TrajectoryMinimap │
│  lane 线在卡片之间交叉，不得穿过卡片正文/      │  （固定辅助层；遮 │
│  状态 badge/Agent label）                    │  住 node/lane/缩放│
│  [lanes] [card][card][card]                  │  label 即缺陷）    │
├──────────────────────────────────────────────┴────────────────────┤
│  TrajectoryDetail（选中卡：摘要/来源/负责人/展开操作/跳转画布）      │
└────────────────────────────────────────────────────────────────────┘
```

- 层级：面板标题/当前结论 > 主轨迹 > 选中卡详情 > 迷你图；主图拥有剩余可用高度。
- lane 交叉只能发生在卡片之间的连线区；任何线/箭头/glow 不得穿过卡片正文、状态 badge、Agent label。
- 最近 merge：拓扑形状 + 文本/图标双编码；active：位置/轮廓 + 文本双编码。
- 选中态不得把未选 lane 降到不可读（opacity 过度即缺陷）。

### 6.2 中宽（768×900）
- 保持完整拓扑与小地图一致性；详情改下方或可关闭 overlay，不挤压主图。
- 控制项可折叠，但定位、缩放复位、筛选状态必须仍可达。
- 页面壳不得产生横向滚动；主图若内部平移必须有明确裁切边界与键盘等价操作。

### 6.3 窄屏（360×800）
- 默认聚焦 active 附近，同时保留「当前在全图何处」的小地图/位置摘要。
- 卡片正文单列；最小触控目标 44×44 CSS px；详情用底部 sheet 或块级区域，不做盖主图的窄浮卡。
- 不允许 document 级横滚、被截断的关闭按钮、贴边 focus ring、只 hover 才出现的内容。
- 复用现有窄屏 GitList 的泳道表达，但升级为可筛选/可聚焦/可跳画的 explore 视图。

### 6.4 200% zoom
- 在 1440 viewport、浏览器 200% zoom 按约 720 CSS px 宽响应式验收；文本可重排不裁切重叠；功能不因 zoom 消失；小地图与详情不双层遮挡。

---

## 7. 组件与 token（对齐 1394 §5）

- 表面：`background → card/popover → muted/secondary`；边界 `border/input`。
- 文字：最多 `foreground`、`muted-foreground`、一个语义色；只用 `text-base/sm/xs`，`font-normal/medium`。
- 状态色：只用 `brand/success/warning/destructive/info` 项目 token 小面积表达；**禁止裸 hex、Tailwind 固定色、大面积高饱和填充**。
- **lane 身份色与状态语义分离**：lane 色只回答「属于哪条路径」（`trajectoryColorSlot(branchKey)`，12 lane 不要求 12 互异语义色，须辅 lane label/线型/可访问名）；badge/图标/文字回答「状态是什么」。
- hover：仅轻微背景/描边变化，不 scale、不加新阴影；selected 比 hover 多一个稳定维度，selected+hover 不降级。
- 键盘 focus：项目统一 `focus-visible` ring，不能被 overflow 裁掉。

**TrajectoryCommitCard 内容（每卡）**：

| 字段 | 规则 |
| --- | --- |
| 标题/结论 | `line-clamp-2`，只需读性，不展开全文 |
| 状态 | 双编码：语义 badge + 文本（ok/run/fail/wait/mute） |
| 负责人 | 头像（小）+ agent 名，`actorAgentId` 展示 |
| 分支 | lane chip / lane label（身份色） |
| merge/弯路/失败 | 双编码（拓扑形状/图标 + 文本），不能只靠颜色（1394 T04/T05/T06/T07） |

**TrajectoryMinimap**：
- 只画窗口派生（同一 `slice`）里的 lane/segments/junctions；`missing_parent` 节点不补画主图没有的边。
- viewport 框与主图可见范围一致；平移不得镜像/反转；筛选隐藏路径两处同时隐藏；selected 只高亮不删除。
- 交互：点击/拖 viewport 框定位主图；缩放同步。

---

## 8. 状态矩阵（默认/加载/空/错误/禁用/权限 — 对齐 1394 T10）

| 状态 | 行为 | 备注 |
| --- | --- | --- |
| 加载 | 保留面板**骨架**（工具栏占位 + 泳道骨架 + 迷你图占位），**不出现伪 commit** | skeleton/error 不得混态 |
| 空 | 单一明确空态：「尚无探索轨迹」，说明下一步（开始研究/等待结果） | 不伪造路径 |
| 错误 | 单一错误态 + 重试；显示 load 失败原因 | 用现有连接/错误基建 |
| 禁用 | 说明原因（如会话已结束/不可用轨迹） | 不给可点但无效卡 |
| 无权限 | 不泄露 Agent、结论、证据或拓扑 | 打码/隐藏 |

---

## 9. 交互与键盘（含跳转画布、聚焦、排序、筛选）

**选中与展开**：
- 卡片点击 → 聚焦（选中 ring）+ 详情（TrajectoryDetail 摘要/来源/负责人）。
- 详情内「展开操作」：直接二次展开摘要/来源/节点明细（复用以 `ResearchNodeDetail` 能力）。
- 「跳转画布」（AC2 核心）：`onJumpToCanvas(nodeId)` → 会话页切回画布视图并**聚焦/选中对应节点**；本视图保持打开时才用同面板内 `onSelect`（画布模式选中同一 `node.id`）。跳转后滚动/平移画布使该节点进入可视区。
- 双向：画布侧选中某节点且打开「轨」时，本视图滚动对齐同一 `node.id`（依赖 `onSyncSelection` 契约，未落地先 fixture）。

**筛选（AC3 泳道稳定重排）**：
- 维度：分支、Agent、关系（`main/branch/merge/abandoned`）。
- 筛选改变 → 重新 `buildTrajectoryLaneLayout`（基于**筛选后的 commit 集**）→ 泳道重排。**必须稳定**：相同筛选参数下 lane 分配确定性、顺序稳定（`trajectory-lane-layout` 已满足按序追加）；重排不得造成选中卡跳失或 lane 抖动。
- 筛选隐藏的路径在迷你图同步隐藏（§7）。

**排序（AC2 时间或逻辑）**：
- 时间：按 `created_at`（逻辑顺序内稳定）。
- 逻辑：按拓扑/`transition_kind` 前后关系（branch_spawned / result_accepted / merge 等）。
- 切换排序 → 重排泳道，选中节点仍在（按 `id` 保持定位）。

**缩放**：
- 显式缩放（如 50%–200%，步进按钮 + 快捷键 Ctrl/Cmd+±），重置回 100%。
- 缩放不得改变布局（1394 §7：不得以 scale 改变布局）；只改变渲染密度/压缩长直线段。
- `prefers-reduced-motion: reduce` 下取消路径绘制/脉冲/自动平移；状态直接落终点，功能与 ARIA 保留。

**键盘/方向键导航**：
- Tab 只进卡片/控制/迷你图/详情关闭按钮；不逐一陷入纯装饰边。
- 复用 `git-topology.neighborByRow / neighborByLane` 的方向键语义（上下行、左右 lane）；焦点若在泳道间应能抵达卡片、控制、迷你图与详情关闭按钮。
- focus 后自动滚入/平移到可见区，但不得跳失当前选择；Esc 关闭详情并把焦点还给触发卡。

**动画可中断（AC3）**：
- 复用 motion 基建：`applyTrajectoryEvent` 生成可取消 intent；`cancelTrajectoryIntent(id)` 由新事件打断旧 intent 并从当前状态续跑；同 lane+kind 在预算窗内 coalesce；`checkout-focus` 单例替换；document hidden 时丢弃不入队。
- 分支生长/merge 汇流只表达状态变化，不循环抢注意力；不得以 scale 改变布局；reduced-motion 下路径 displacement=0、保留静态高亮/status。

---

## 10. 性能与交互预算（AC2/AC3）

复用 `trajectory-performance-fixture` 的门并扩展到 10k：

| 指标 | 预算 |
| --- | --- |
| 首屏 500/2000 commit 布局 | 沿用 fixture：`initial2000 ≤ 250ms` |
| 增量 20 commit | `incremental20 ≤ 100ms` |
| 滚动窗口化 | `scrollWindow ≤ 50ms` |
| **10k 节点** | 首屏只建可视窗+overscan DOM；全量不建 DOM；滚动同帧率无 jank |
| 泳道重排 | 筛选后布局纯函数 `buildTrajectoryLaneLayout` ≤ bucket；选中卡不跳失 |
| motion | 可中断、coalesce、reduced-motion 落终点 |

- 交互规格：缩放步进、方向键语义、filter=rerun layout 的稳定顺序、jump-to-canvas 焦点语义（§9）。

---

## 11. 可访问性（对齐 1394 §7）

- 每张 commit 卡 accessible name 含：结论/短标题、状态、Agent 或路径身份；merge/弯路/失败不能只靠颜色。
- DOM/无障碍顺序遵循可解释的时间或拓扑顺序；Tab 不逐一陷入数百个装饰边。
- 方向键/约定图导航可发现，能抵达卡片、控制、迷你图和详情关闭按钮。
- 文本/背景 WCAG AA（正文 4.5:1，大文本 3:1）；focus indicator 与相邻 3:1。亮暗主题分别实测。
- `prefers-reduced-motion: reduce`：取消路径绘制/粒子/脉冲/自动平移；仅调短时长不算通过。

---

## 12. 验收映射（AC 📍 → 交证据）

| AC | 本文支撑 | 交证据 |
| --- | --- | --- |
| **AC1** 至少 8 条并行且交叉合并的 fixture，任一路线起点/分叉/合并/终止/负责人可追踪 | §4 contract fixture（≥8 分支 + merge + abandoned）；§7 card 含分支 chip + 负责人；lane-layout segments/junctions | 录屏：8+ 分支 map，逐条追踪起点→分叉→合并→终止→负责人；截图矩阵按 1394 §8 |
| **AC2** 卡片聚焦/展开/跳转画布；长轨迹虚拟化 10k 不加载全图 | §9 交互（聚焦/展开/`onJumpToCanvas`）；§4 虚拟化 + §10 预算 | 录屏：点卡→详情→跳画布；10k fixture 首屏 DOM 计数 + 60fps 滚动；`pnpm typecheck` + `pnpm react:doctor` |
| **AC3** 分支/合并动画可中断；筛选后泳道稳定重排 | §9 动画可中断（motion 基建）+ 筛选稳定重排；§10 预算 | 录屏：动画中途打断续跑；筛选前/后 lane 稳定快照对照；性能数字 + 交互规格 |

---

## 13. 实现切片（独立小 PR，base=dev）

1. **数据适配 + 视图壳**：`TrajectoryExplorer` + `TrajectoryToolbar` + adapter（nodes/edges→commits）+ 桌面布局；接入 aux drawer。
2. **主图（虚拟化）**：`TrajectoryGraph` + `TrajectoryCommitCard`，消费 `sliceTrajectoryLaneLayout`，10k 窗口/性能门。
3. **筛选/排序 + 稳定重排**：`TrajectoryToolbar` 维度；layout 重排稳定性测试。
4. **缩放 + 迷你图**：`TrajectoryMinimap` + 显式缩放 + viewport 同步 + 一致性断言。
5. **详情 + 跳转画布**：`TrajectoryDetail` + `onJumpToCanvas` 接线（画布选中同一 node.id）。
6. **动画消费 + a11y + 打磨**：接 motion 基建、i18n（en/zh-Hans/ja/ko）、reduced-motion、react:doctor、1394 截图矩阵。

---

## 14. 交接与角色

- 本稿由 UI 设计 agent（UI-06）产出，**实现交前端**；本群贝克汉姆指定实现 owner 并监督。
- 实现 PR 附：截图/录屏、相关结构测试、`pnpm typecheck`、`pnpm react:doctor`；base=dev。
- 验收由本设计 agent 逐项（§12）核对后给 PASS/RETURN；1394 缺陷按 §9 回写对应实现单。
