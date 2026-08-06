# Research Live Canvas V6 — 有机探索路径与语义层级规格

> 本文替代“横平竖直的全量节点树”。目标是让用户同时看见主结论、Agent 走过的弯路、失败与回退，同时把实际渲染量控制在预算内。

## 1. 视觉结论

主画布采用“研究路线图”，不是组织结构图：

- 主节点是可读卡片，构成一条松弛的叙事主脊；
- 次要步骤是带 glyph 的小路标，不再缩成不可读的小卡；
- 低重要度步骤是语义微圆点，串成弯曲探索路径；
- 失败、取消、失效、争议、重试和重新汇入必须留下可追溯的路形；
- 默认只展开选中点相邻 2 层，用户显式钻取时最多 3 层；
- 更深、更密的路径折叠成 Route Bundle 或 Display Group，不能把全部节点铺上画布。

最终画面应像一张正在生长的探索地图：能看出主路、岔路、死路和回流，但每一个形状都能回到真实 node、edge、status 或 transition。

### 目标画面骨架

```text
                         ● query ─ ● source ─ × failed
                       ╭─                              ╲
[研究目标 Card] ──●───╯              ╭─ ● retry ── [Claim Card]
      ╲                              ╯                   ╲
       ●─● cancelled          ●─●─● accepted              ╲
        ╲                                                   [L1 Insight Card]
         ╰── [Question Waypoint] ─ ● disputed ─◇ stale    ╱
                                      ╲                  ╱
                                       ╰─●─● accepted ──╯
```

- Card 是需要扫读的主目标、主结论、阻塞和汇入点；
- Waypoint 是仍需显示标题/Agent 的中间步骤；
- `●` 是可以点击的 canonical 微节点；
- `×`、`◇` 等端点表达失败/失效，颜色是第二重编码；
- 路径不要求几何对称，但节点、曲率和上下支路顺序必须稳定。

## 2. 三档语义节点

节点大小由研究意义、当前状态和用户上下文决定，不由 node kind 单独决定。

### 2.1 Landmark Card · 地标卡

使用 `NodeCardShell` 的完整或紧凑卡面。以下节点必须晋升为地标卡：

1. selected、pinned、当前 breadcrumb 上的节点；
2. root、当前 active task、blocking/failed/cancelling/stale；
3. branch 的主要分叉点；
4. canonical Insight、Dispute、Decision、Report Revision；
5. importance 排序后进入当前视口保留预算的节点；
6. 最近一次 `transition_kind` 的 affected root，但只临时晋升一个节拍。

卡片承担“谁负责、目标、当前动作、已解决、最新进展、风险”。卡片之间不能排成等距行列；它们沿主脊和支路形成有节奏的疏密变化。

### 2.2 Waypoint · 路标

路标是 28–40px 高的胶囊或圆角短签，只显示：

- kind glyph；
- 12–24 字标题；
- 状态形状；
- Agent 头像或未分配 glyph；
- `+n` 未加载/折叠数量（存在时）。

中等 importance 的 Question、Hypothesis、Task、Claim、Observation、Attempt 使用路标。点击路标后，它在原锚点处临时晋升为紧凑地标卡，Inspector 同步打开，镜头执行 safe-centre 定位。

### 2.3 Trail Dot · 探索微圆点

微圆点代表仍可定位到 canonical node 的低密度步骤，直径 6–10px；它不是装饰粒子。典型对象：重复 query execution、source candidate、screening decision、低 importance observation、已结束的中间 attempt。

微圆点必须保留稳定 node id、keyboard target 的代理入口和 Inspector 跳转。视觉上只显示状态和路径归属；hover/focus 显示一行 popover，点击后晋升为路标或紧凑卡。不能把多个真实节点伪装成一个可点击圆点；聚合必须使用 Route Bundle。

## 3. 路径语义：颜色、线型、端点三重编码

颜色使用 semantic tokens，不写裸 hex；每种状态同时使用线型和端点，保证灰阶、色弱与截图仍可区分。

| 路径状态 | 数据条件 | 颜色 token 语义 | 线型 | 微圆点/端点 |
| --- | --- | --- | --- | --- |
| exploring | active branch/query/task/attempt | info/cyan | 1.5px 实线 | 实心圆 + 单个活动探针 |
| accepted | accepted result/claim/insight/decision | success/teal | 2px 实线 | 双环圆或 check 端点 |
| failed | attempt `failed`/`lost` 或明确 terminal failure | danger/coral | 1.5px 断线 | 断口圆 + `×` 端点 |
| cancelling/cancelled | attempt 明确取消状态 | neutral/slate | 长短虚线 | 空心圆 + stop 端点 |
| stale/invalidated | freshness/status 或 `invalidates` | warning/amber | 点划线 | 中空菱形 |
| disputed | `contradicts`/`challenged_by`/Dispute | accent/violet | 双细线 | 分裂圆 |
| neutral/unknown | 无结构化结果 | muted | 1px 实线 | 空心圆或 `?` |

判定规则：

- 建立显式 status registry；未知字符串落入 neutral/unknown；
- 不使用 `title.includes("失败")`、summary 情感或 Agent 文本推断结果；
- edge relation 只描述关系，node/attempt status 描述执行结果；两者冲突时保留两种编码并在 Inspector 解释；
- `supports` 不自动显示 accepted，`produced` 不自动显示成功；
- Display Group 和 Route Bundle 使用中性虚线外壳，不能使用 Insight/accepted 样式。

## 4. 有机曲线路径几何

### 4.1 主脊

每个 Slice 选择一条“当前叙事主脊”：root → selected/active/Insight/Decision 的 canonical 邻接路径。主脊总体按阅读方向前进，但使用宽缓 S 曲线，不使用单一竖轴或整齐 rank 列。

- 桌面默认从左向右推进；窄屏 focused view 从上向下推进；
- 相邻地标卡的主方向间距 280–420px，法向偏移 56–160px；
- 主脊每 2–4 个地标换一次弯曲方向，换向来自 stable id hash，不能每次刷新随机变化；
- Stage 只作为低对比背景区或路径里程标，不把 S1–S4 画成四列流水线；
- 主脊允许穿越背景区，不能穿越卡片正文、Inspector 或 Dock safe region。

### 4.2 支路、弯路和回流

- 新探索从父路径以 24–52° 切向离开，短距离内不能 90° 转折；
- 同一父节点的支路上下交替展开，使用 stable id 决定顺序；
- 失败/丢失路径逐渐向主脊外弯，终端保留 28–56px 的可见尾迹和失败端点；不能删掉失败节点让过程看似一次成功；
- retry 从失败端点外侧绕出一段 hairpin 曲线，再指向新 Attempt；旧失败点继续保留；
- reassign 先经过旧 Agent 的停止/失败节点，再弯向新 Agent 对应 Attempt，不能直接改旧节点负责人；
- Insight/Decision 的汇入边从不同方向收束，但每条 canonical edge 仍可单独命中和高亮；
- stale/invalidated 路径保留原几何，降低非选中对比并添加断续纹理，不能把历史路径从图上抹掉。

### 4.3 曲线生成

每条边使用 cubic Bézier 或等价平滑样条。控制点由端点切线、分支扇区、卡片避让和 stable seed 决定：

```text
P0 = source port
P1 = P0 + source tangent * clamp(distance * 0.32, 48, 160)
P2 = P3 - target tangent * clamp(distance * 0.28, 40, 144)
P3 = target port
```

再应用以下限制：

- 曲率半径不能小于 32px；
- 曲线进入卡片前最后 24px 接近垂直于卡边，箭头/端口不会贴正文；
- 避障只移动控制点和局部 route corridor，不把路径折成直角；
- 同方向路径可共享视觉 corridor，但命中层必须保留独立 edge id；
- 任何随机偏移都必须由 `runId + nodeId/edgeId` 生成，Snapshot 相同则布局相同。

禁止：orthogonal router、固定蛇形折线、每层等距列、所有边相同曲率、渲染时使用 `Math.random()`。

## 5. Route Bundle · 路径束

当相邻微节点或同向路径超过当前 LOD/预算时，使用路径束压缩，不使用几十张缩小卡：

```text
───●─●─●──╮
           ╰━━ [探索路径 · 18 步]
                成功 7 · 失败 3 · 进行中 2 · 其他 6
                4 Agent · 2 条未决分支
```

路径束显示：

- 总节点数、各 outcome 数、Agent 数、未决/异常数；
- 主方向和起止锚点；
- 最多 12 个有代表性的微圆点，其余用密度/长度表达；
- 只使用真实聚合计数，不生成伪结论；
- accessible name 包含“折叠的 n 个节点、失败 m、进行中 k”。

点击路径束：

1. 计算展开后总量；
2. 不超 soft limit：在锚点周围展开 1 层；
3. 超 soft limit：进入局部 Spotlight，只显示路径束 + 上下游各 1 层；
4. 超 hard limit：继续分页，不把全部节点挂 DOM；
5. Back 返回原布局、selection 和 camera。

## 6. LOD 与层级裁剪

### 6.1 缩放层级

| zoom | 地标卡 | 路标 | 微圆点/路径束 |
| --- | --- | --- | --- |
| <35% 全局概览 | 只保留 root、selected、active/blocking、顶层 Insight/Decision；≤12 张 | 不显示正文，只保留少量 glyph 锚点 | 主要表达方式，显示路线形态与密度 |
| 35–65% 路线概览 | 紧凑地标卡；≤24 张 | 显示主要 Question/Task/Claim | 次要步骤全部进入微圆点或束 |
| 66–119% 工作视图 | 标准地标卡 | 中等节点路标 | 低重要度步骤为点 |
| ≥120% 局部细看 | 卡片可增加 2 条事实 | 路标可临时晋升 | 只保留远端/重复步骤为点 |

LOD 切换使用两个渲染形态交叉淡入，不能把带文字卡片直接 scale 到 25% 形成截图中的“白色火柴盒”。

### 6.2 关系层级

- 默认：selected/root + 2 hops；
- 显式展开：最多 3 visible levels；
- selected、ancestor path、blocking path 永不因普通缩放消失；
- 第 4 层及更深折叠为路径束/Display Group；
- 视口外只保留布局数据，不保留可交互 DOM；
- Lens 切换只改变晋升/降级与关系强调，不能重新下载另一套图。

### 6.3 总量预算

所有数字都是同一 viewport 的上限，类型上限包含在 total 内，不可相加突破 total。

| viewport | semantic node soft/hard | Landmark Card hard | Waypoint hard | Trail Dot hard | Display Group/Bundle hard | edge hard |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| desktop ≥1200 | 120 / 180 | 48 | 56 | 96 | 16 | 420 |
| 768–1199 | 72 / 96 | 28 | 36 | 52 | 12 | 220 |
| <768 | 32 / 48 | 12 | 16 | 24 | 8 | 96 |

desktop 图形 DOM 总量仍不得超过 220。微圆点优先批量进入一层 SVG/WebGL route layer；每个点不能各建 tooltip、observer 或 timer。交互命中可用单个 spatial index + canvas/SVG event delegation。

## 7. 交互与镜头

### 点击地标卡

- 保持现有 selection → Inspector insets → 260ms safe-centre camera 顺序；
- 只提高该节点的一阶关系和所属路线；其他路线降到 35–45%，不能消失；
- 主动点击允许镜头移动，后台普通 Delta 不抢镜头。

### 点击路标/微圆点

1. 选中真实 node id；
2. 在原锚点晋升为紧凑卡，周围保留 1 层路径；
3. Inspector 打开并展示完整事实；
4. 镜头将晋升后的卡放到 safe centre；
5. 关闭 Inspector 或选择其他节点后，若不再受保护则降回原 LOD。

### 路径追踪

- 点击一条 edge/路径状态后只强调其 canonical 起止和 provenance chain；
- “查看走过的路”从 selected 向上游追到 root、向下游追到 terminal/Insight，默认最多 3 层；
- 更长路径用 continuation marker，用户逐段加载；
- Inspector 的 History 项点击后定位同一 node id，不另建假时间线节点。

## 8. 路径动效

动效展示变化，不替代终态。除 CSS `offset-path` 上的活动探针外，几何路径本身不做高频形变。

### Route Sprout · 新路生长

新分支的 curve 先以 0→1 opacity 出现；一个 6px 探针沿 curve 移动 220–360ms；途中最多 6 个微圆点用 transform/opacity 依次出现；终点地标卡最后显现。后台非聚焦分支只淡入终态。

### Detour Trace · 弯路回放

用户显式点击“查看走过的路”时，一个非循环探针依次经过 selected 路径。失败段到达 `×` 后停 120ms，再沿 retry hairpin 继续。总时长超过 900ms 时分段并允许跳过，不能长时间锁输入。

### Dead-end Settle · 失败落定

失败路径不抖动。活动探针停止，尾段在 140ms 内出现断口，失败端点淡入；关联卡片显示失败原因入口。旧路不收回。

### Retry Hairpin · 重试回钩

从失败端点外侧长出新曲线，新 Attempt 用 180ms 淡入；旧失败圆点保持 danger 语义，新路径初始为 exploring。成功后新路径变 accepted，旧路径仍可见。

### Convergence · 汇入

Insight/Decision 形成时，只提升参与输入路径；探针从各输入向汇入点运行一次，Insight 卡按 `integration_formed` 分镜结晶。没有 canonical `integrates/derived_from` 的邻近路径不得自动汇入。

### Semantic Morph · LOD 变形

卡片、路标、微圆点使用稳定锚点交叉淡入：旧形态 100–140ms 淡出，新形态 140–180ms 淡入；不对文字做连续缩放。连续 wheel 时跳到最近终态，停止 120ms 后再补一次淡入。

### Ambient Probe · 活动探针

只允许 selected route 和最高优先 running route 各 1 个低频探针；同屏最多 2 个。Reduced Motion、低性能、后台 tab、resync 全部关闭。不能让所有边持续流光。

## 9. 模块位置配合

- Lens Bar 下方可以显示一行 route legend：探索/已接纳/失败/失效/争议；legend 可折叠，不浮在路径上；
- Breadcrumb 显示 semantic parent，不显示每个微圆点；路径历史由 Inspector History 负责；
- Inspector 打开后 route layout 不全图重排，只更新 camera safe insets；
- Minimap 使用路径密度和地标锚点，不复制每个微圆点；颜色与主图一致并带非颜色 legend；
- Canvas Dock 新增“2 层/3 层”“路线密度”“只看主路”“查看走过的路”，业务命令仍留在 Inspector；
- Trajectory Explorer 继续是独立 Git 多轨迹视图；主画布的有机路径回答研究语义结构，不复制完整执行提交历史。

## 10. 实现边界

建议拆成以下纯接口，避免 layout、数据和动效互相写状态：

```ts
type SemanticLOD = "landmark" | "waypoint" | "trail-dot" | "route-bundle";
type RouteOutcome =
  | "exploring" | "accepted" | "failed" | "cancelled"
  | "stale" | "disputed" | "neutral";

classifySemanticLOD(node, context, budget): SemanticLOD
classifyRouteOutcome(node, edge, registry): RouteOutcome
buildRouteTopology(slice, protectedIds, stableSeed): RouteTopology
layoutOrganicRoutes(topology, previousLayout, affectedRootIds): RouteLayout
planRouteMotion(delta, previousLayout, nextLayout): MotionDirective[]
```

- `classifyRouteOutcome` 只读显式 registry/字段；
- `layoutOrganicRoutes` 是纯函数、worker-ready，不读 DOM；
- renderer 只消费 RouteLayout，不自行改变 canonical graph；
- Delta 只重算 affected corridor；未影响地标坐标逐像素不变；
- route layer、hit-test spatial index、card DOM 分层，避免每个点一个 React component。

## 11. 验收场景

1. 1 条主路、4 条探索支路，其中 2 条失败、1 条 retry 成功、1 条 stale；一眼能区分且不只靠颜色。
2. 4 Claim → 2 L1 Insight → 1 L2 Insight；汇入路径可追踪，输入节点没有被视觉删除。
3. 25% zoom 不出现几十张不可读白卡；只保留 ≤12 地标卡，其余为路标、微圆点和路径束。
4. 默认 2 层、显式最多 3 层；第 4 层进入路径束，节点数不突破 viewport hard limit。
5. 点击任一微圆点后晋升、打开 Inspector、safe-centre；Back 恢复原 LOD/位置。
6. 10k fixture 首屏不全量下载；desktop semantic node ≤180、图形 DOM ≤220、edge ≤420。
7. Snapshot 相同的两次布局坐标一致；20-node Delta 只移动 affected corridor。
8. Reduced Motion 下没有探针/位移，但失败、成功、失效、争议和路径层级仍完整可读。
9. 亮/暗主题、灰阶、200% zoom、360/768/1440 viewport 均能定位主路、失败端点和 selected path。

## 12. 禁止项

- 不使用横平竖直的 orthogonal tree 作为最终画布；
- 不把所有 node kind 渲染成同尺寸卡片；
- 不把随机曲线当“有机感”，不能刷新后换路形；
- 不用颜色单独表示成功/失败；
- 不把动画粒子当真实节点或执行状态；
- 不因折叠删除失败、stale、blocking、selected 的可追溯入口；
- 不把主画布和独立 Trajectory Explorer 合成一张全量大图。
