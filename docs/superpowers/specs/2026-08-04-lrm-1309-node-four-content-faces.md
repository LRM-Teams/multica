# LRM-1309 — 调研画布节点四内容面（实施规格）

**状态：设计门，等待 LRM-1317 字段契约；本规格不定义匹配、废弃视觉或重组动效。**

## 1. 问题与边界

当前 `ResearchGraphNode` 表面只显示 `title / status / owner / phase`（`research-graph-node.tsx`，240 × 68–88），详情仅按已有 run 投影显示 Objective / Method / Outcome（`research-node-detail.tsx`）。用户看不到当前节点「要达成什么、怎么操作、为何这样研究、已得什么结果」，也不能区分“缺字段”与“已经没有结果”。

本规格仅增加**节点本体内容面**：

- `目标`（goal）
- `操作思路`（operation approach）
- `调研思路`（research approach）
- `调研结果`（result）

不做：LRM-1306 侧栏、LRM-1268/1295 的树拓扑/质量态与全局行距、LRM-1311 的重组动效、LRM-1315 的废弃外观、LRM-1317 的 API/BE。`assessment`（trusted/pending_review/detour）与 `status`/abandoned 是两条独立语义，不能互相替代。

## 2. 数据门与单一投影

实现必须等 LRM-1317 给出稳定字段和示例 JSON。它预期四个可空文本字段：`goal`、`operation_approach`、`research_approach`、`result`；最终路径和命名以 LRM-1317 为准。

- 在 core 类型中以**显式、可空的受解析投影**暴露四字段；不能在组件中扫描任意 `payload`、从 `summary` 猜字段、或从 `status`/颜色推结果。
- `research-graph-node.tsx` 与 `research-node-detail.tsx` 必须消费同一只读内容投影/同一 `NodeContentFaces` 组件族，避免两套标签、截断或缺值口径。
- 字段未知、`null`、空白或非法时都等同“未提供”，使用中性文案；绝不补造研究内容。

## 3. 信息层级与桌面布局（≥768）

### 3.1 表面：一眼扫读，非长文卡

保留现有左侧 gutter/port、点击打开详情、状态、质量态、选中和菜单边界。卡片内容自上而下：

1. **上下文标题**：`node.title`，一行、14px/20px、semibold、`line-clamp-1`；标题不是四内容之一。
2. **四内容网格**：2 × 2，行/列 gap 8px。每格始终显示 10–11px label（目标、操作思路、调研思路、调研结果）及一行 12px value；value `line-clamp-1`。这是四面可见的最低保证。
3. **状态行**：既有状态 badge + 质量态（若已有）+ 负责人/阶段的紧凑文本。不得把任一四内容标签挤到 status 里。

尺寸是内容面的局部约束，不改变 LRM-1295 的拓扑或 global layout：Desktop 外框宽 **240–260px**、内容高度 **112–124px**（含 12px padding）；全局节点排布须由 LRM-1295 根据最终高度更新间距，连线和 port 仍只在 gutter，禁止穿过卡片。

每格的呈现形态：

| 面 | 表面 label | 表面 value | 详情 |
|---|---|---|---|
| goal | 目标 | 1 行，最多 12 个 CJK 等宽字符后省略 | 完整段落，保留换行 |
| operation approach | 操作思路 | 1 行，最多 12 个 CJK 等宽字符后省略 | 完整段落，保留换行 |
| research approach | 调研思路 | 1 行，最多 12 个 CJK 等宽字符后省略 | 完整段落，保留换行 |
| result | 调研结果 | 1 行，最多 12 个 CJK 等宽字符后省略 | 完整段落 + 既有证据区 |

不使用 JS 按字符截断作为数据逻辑；使用 CSS clamp，完整值只能在详情内阅读。`title` 和四字段都来自服务端文本，必须按纯文本渲染。

### 3.2 详情：完整四段优先于现有运行投影

在现有 `ResearchNodeDetail` header 之后、已有 Objective/Method/Outcome 运行投影之前放入 `NodeContentFaces density="detail"`：四个连续 section，顺序固定如上，每段 `h3 + p`。已有 Objective/Method/Outcome、运行指标、证据和 artifacts 均保留为“执行上下文/证据”，不得把它们当四内容的 fallback。

当 `status === "abandoned"` 且 LRM-1317 传入 `abandon_reason` 时，LRM-1315 的废弃态模块在四段之后、证据之前显示其原因。LRM-1309 不定义颜色、线型、是否可恢复或废弃操作。

## 4. 状态矩阵

| 情形 | 表面四格 | 详情 | 禁止 |
|---|---|---|---|
| 默认/有结果 | 四个 label + 已有短值；result 使用真实摘要 | 四段完整文本 + 既有证据 | 用质量颜色表示内容是否完整 |
| 进行中（running/active）且 result 缺失 | 前三格依数据；result 为“结果整理中”并在卡片根元素保留既有 `aria-busy` | “调研结果”显示“正在整理，暂未形成可展示结果。” | 显示假结论、持续动效（LRM-1311 所有） |
| 空/新节点 | 四 label 均在；缺值为“尚未形成” | 对应段落“暂无此项内容” | 隐藏 label 造成四面不可辨 |
| 单字段缺失/契约未到 | 只该格中性“未提供”；其余真实显示 | 同样逐段中性 | 用 `summary`、title、status 填补 |
| failed/unknown | 已持久内容照常读；result 缺失为“本轮未产出可展示结果” | 安全解释 + 现有详情/菜单可达 | 原始 error、任务码、猜测 retry |
| abandoned | 保留四内容读态；废弃 badge/reason 由 LRM-1315 | 理由只显示 LRM-1317 的安全 `abandon_reason` | 当作 `detour` 或删除历史 |
| 无详情动作/无权限 | 卡片仍可读、状态语义仍可见；无 `onViewDetail` 时主按钮 `aria-disabled=true` 且不假装可打开 | 不渲染未授权控制；按现有宿主权限处理 | 伪造权限文案、显示空菜单 |

## 5. 窄屏（<768）

`ResearchGitList` 是窄屏真实表面，非桌面自由画布的缩放版：

- 仍保留左侧 Git gutter 和 44px 菜单触达目标；卡片宽度为可用宽度，不可横向滚动。
- 表面改为单列四行紧凑 stack：每行 `label（80px 固定） + 1 行 value`，外框最小高度 148px；不要把 2 × 2 网格压到小于可读宽度。
- 点击卡片仍打开既有底部 `Sheet`；Sheet 保留 sr-only title/description、最大 90vh 和纵向滚动，四段完整文本不得截断或横滚。
- 360、700、767px 均应完整显示四个 label、状态和菜单；768px 才回桌面画布。不得添加横向滚动或把完整段落塞回 canvas node。

## 6. 组件、token 与无障碍

- 复用 `bg-card`、`text-foreground`、`text-muted-foreground`、`border`、`muted`、`brand/success/warning/destructive`；不新建裸 hex、不把 branch lane 色用于内容面的语义。
- `目标/操作思路/调研思路/调研结果` label 使用 `text-muted-foreground`，正文 `text-foreground`；文本对比度至少 4.5:1，边框/状态非文本至少 3:1。disabled 不能用祖先 opacity 稀释文本。
- 保留单一原生主 button、roving tabindex、Enter、方向键与 `ResearchCardMenu`。不得在卡内嵌套 button/role=button。
- 更新 `buildNodeAccessibleName`：标题 + 当前状态 + 四个 label/value（缺值读“未提供”）+ lane/低置信；label 不得仅靠视觉位置传达。详情四段用语义 `section > h3`。
- 复用现有 focus outline；新的内容面没有 hover-only 信息、没有自动朗读四项变更。点击时既有 live 区只报“已打开详情”。
- 不依赖颜色判断 ready/running/failed/abandoned；状态文字始终可见。`prefers-reduced-motion` 本刀不引入任何动画。

## 7. FE 文件面与测试

**LRM-1317 专属（不得由本 FE 刀抢改）**：`packages/core/types/research.ts` 与 API schema/序列化，提供稳定四字段/abandon reason。

**建议 FE 子刀（字段门通过后，20–60 分钟）**：

- `packages/views/research/components/research-graph-node.tsx`
- `packages/views/research/components/research-git-list.tsx`
- `packages/views/research/components/research-node-detail.tsx`
- 新增唯一共享的 `research-node-content-faces.tsx`（表面/详情两种 density，统一 label、缺值和顺序）
- `packages/views/research/lib/canvas-keyboard-nav.ts`
- `packages/views/locales/{en,zh-Hans,ja,ko}/research.json`
- 定向 tests：`research-graph-node.test.tsx`、`research-node-detail.test.tsx`，新增/扩充 Git list 和 content-face tests。

不可修改：`layout-graph.ts` 拓扑、edge visual/animation、LRM-1306 侧栏、LRM-1311 motion、LRM-1315 abandoned style；若最终 112–124px 节点高度需要全局行距更改，应先由 LRM-1295 owner 明确接手。

## 8. 可执行验收

1. 四字段全量 fixture：桌面表面和窄屏列表均能同时看到四个精确 label，四个短值按 CSS 单行省略；详情展示四段完整文。
2. 字段缺失 fixture：四个 label 永不消失，缺失仅显示中性文案；断言没有读取 `summary` 作为 fallback。
3. running + result 缺失：只 result 使用“整理中”，`aria-busy=true`；reduced-motion 下无新增 animation。
4. failed/unknown/abandoned fixture：无 raw error/task code；abandoned 原因只在 LRM-1317 给出安全文本时显示，并与 `assessment=detour` 可同时存在、各自有名称。
5. a11y：保留一个主 button 和菜单 button；选中 node `tabindex=0`，其他 `-1`；accessible name 含四个 labels，详情有四个 heading；键盘和 Escape/menu 回归全绿。
6. 视觉证据：真实 `/research/:id`，亮/暗、1440 与 360/700/767/768px、Playwright `fullPage:true` 截图。每组各一张「四字段齐」「running result 空」「单字段缺失」「abandoned + detour 同屏」；窄屏必须是 Sheet 打开态。附 computed style/Playwright 断言：无 `overflow-x`、四 label 可见、port/right edge 不进入卡片内容 box。
7. 相关 Vitest、Views typecheck、eslint、React Doctor 全绿后再开 PR；本设计 spec 本身不声称已实现或已取得真实路由截图。
