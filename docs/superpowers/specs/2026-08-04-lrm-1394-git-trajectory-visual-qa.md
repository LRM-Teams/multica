# LRM-1394 — Git 轨迹视觉验收合同

> 状态：fixture/视觉门合同已建立；集成像素证据等待 LRM-1388～1393 的可运行 revision。  
> 范围：独立「轨 · 探索轨迹」面板。不得塞进无限画布，不改 LRM-1295 聚合树布局，不修改 DAG 或 lane 算法。

## 1. 验收结论规则

本单只接受真实运行态、无标注、未合成的截图与交互记录。每组证据必须注明 commit SHA、数据 fixture、主题、viewport、浏览器 zoom、motion 设置与选中节点。设计稿和静态拼图可说明意图，不能作为 PASS 证据。

最终结论只有三种：

- **PASS**：第 3～8 节全部通过，且截图矩阵无缺口。
- **RETURN**：实现存在可定位缺陷；按第 9 节回写对应实现单。
- **BLOCKED**：没有可运行集成 revision、真实 lineage 缺字段或运行环境不可用；不能用 mock/fallback 代替。

## 2. 五秒扫读任务

对每个 5 分支、12 分支和多 merge fixture，各让一名未参与实现的观察者只看默认首屏 5 秒，然后隐藏画面并回答：

1. 当前活跃分支是哪一条？
2. 指出至少两条并行 Agent 路径。
3. 最近一次 merge 在哪里，哪两条路径汇入？
4. 指出一条弯路，以及它为何被判为弯路。

四问全对才通过该 fixture。答案必须来自默认表面的可见 label、形状和短原因；悬停、打开详情或只凭颜色猜测均不计。记录答题用时和错误项，不记录主观“感觉清楚”。

## 3. Typed fixture 矩阵

所有 fixture 使用实现定义的真实 typed trajectory 输入，不另造第二套 QA 数据结构。

| ID | 最小结构 | 首屏必须读到 | 主要风险 |
|---|---|---|---|
| T01 线性 | 1 branch、6 commits、1 active | 起点→当前方向、当前节点 | 装饰过重掩盖顺序 |
| T02 五分支 | 5 branches、至少 2 agents、1 active | active、两条并行路径、分支名 | lane 色过近、label 互撞 |
| T03 十二分支 | 12 branches、至少 4 agents | active 与分组层级；非焦点仍可追踪 | 车道压缩、卡片遮线、窄屏横滚 |
| T04 多 merge | 至少 3 次 merge，含连续 merge | 最近 merge 与汇入双方 | 交叉线冒充连接关系 |
| T05 弯路 | 1 abandoned/dead-end branch + 短原因 | 弯路结论与原因 | 只靠灰色/红色表达 |
| T06 失败 | 1 failed commit，后续可有恢复路径 | “失败”文字或等价图标+可访问名 | 与弯路/暂停混淆 |
| T07 关系不完整 | missing parent/merge target | “关系不完整”降级，不画伪连线 | fallback 伪造拓扑 |
| T08 完成 | terminal merge + completed | 最终有效路径与完成态 | 完成色吞掉其他 lane |
| T09 选中 | T03 中选 active/merge/dead-end 各一次 | 选中卡突出，其他车道仍存在 | opacity 导致上下文消失 |
| T10 空/加载/错误/禁用/权限 | 各自独立状态 | 单一明确状态与下一步 | skeleton/error 混态、假数据 |

T10 约束：加载保留面板骨架但不出现伪 commit；空态说明尚无路径；错误态提供重试；禁用态说明原因；无查看权限时不泄露 Agent、结论、证据或拓扑。

## 4. 布局与层级

### 4.1 桌面（1440×900）

- 页面层级：面板标题/当前结论 > 主轨迹 > 选中卡详情 > 小地图/辅助控制。
- 主图拥有剩余可用高度；小地图固定为辅助层，不盖住节点、lane label、缩放控制或详情。
- lane 交叉只能发生在卡片之间的连线区，任何线、箭头或 glow 不得穿过卡片正文、状态 badge、Agent label。
- 最近 merge 使用拓扑形状 + 文本/图标双编码；active 使用位置/轮廓 + 文本双编码。选中态不得把未选 lane 降到不可读。

### 4.2 中宽（768×900）

- 保持完整拓扑与小地图一致性；详情优先改为下方或可关闭 overlay，不挤压主图至不可读。
- 控制项可折叠，但定位、缩放复位、筛选状态必须仍可达。
- 页面壳不得产生横向滚动；主图若采用内部平移，必须有明确裁切边界与键盘等价操作。

### 4.3 窄屏（360×800）

- 默认聚焦 active 附近，同时保留“当前位于全图何处”的小地图/位置摘要。
- 卡片正文单列，最小触控目标 44×44 CSS px；详情用底部 sheet 或块级区域，不做盖住主图的窄浮卡。
- 不允许 document 级横滚、被截断的关闭按钮、贴边 focus ring 或只能 hover 才出现的内容。

### 4.4 200% zoom

- 在 1440 viewport、浏览器 200% zoom 下按约 720 CSS px 宽的响应式规则验收。
- 文本可重排，不裁切/重叠；功能和信息不因 zoom 消失；小地图与详情不得形成双层遮挡。

## 5. 组件与 token

- 表面：`background` → `card/popover` → `muted/secondary`；边界使用 `border`/`input`。
- 文字最多 `foreground`、`muted-foreground`、一个当前语义色；只用 `text-base`/`text-sm`/`text-xs`，只用 `font-normal`/`font-medium`。
- 状态色只用 `brand`/`success`/`warning`/`destructive`/`info` 等项目 token，小面积表达；禁止裸 hex、Tailwind 固定色和大面积高饱和填充。
- lane 身份色与状态语义分离：lane 色回答“属于哪条路径”，badge/图标/文字回答“状态是什么”。12 lane 不要求 12 个互异语义色；必须辅以 lane label、线型/标记或可访问名。
- hover 仅轻微背景/描边变化，不 scale、不加新阴影；selected 比 hover 多一个稳定维度，selected+hover 不降级。
- 键盘 focus 使用项目统一 `focus-visible` ring，不能被 overflow 裁掉。

## 6. 小地图与主图一致性

对 T02/T03/T04/T09 分别在初始、平移后、缩放后、checkout 聚焦后取证：

- 小地图节点数、lane 身份、merge 位置和 active/selected 位置与主图一致。
- viewport 框与主图当前可见范围一致；平移方向不得镜像或反转。
- 筛选隐藏的路径在两处同时隐藏；selected 只高亮，不从任一视图删除其他路径。
- 关系不完整节点不得在小地图中补画主图没有的边。

可自动断言节点/边 identity 与 transform；最终位置仍以真实渲染截图和交互为准。

## 7. 可访问性与动效

- 每张 commit 卡的 accessible name 至少包含：结论/短标题、状态、Agent 或路径身份；merge、弯路、失败不能只靠颜色。
- DOM/无障碍顺序遵循可解释的时间或拓扑顺序；Tab 不逐一陷入数百个纯装饰边。方向键/约定图导航必须可发现，并能抵达卡片、控制、小地图和详情关闭按钮。
- focus 后自动滚入/平移到可见区，但不得跳失当前选择；Esc 关闭详情并把焦点还给触发卡。
- 文本与背景达到 WCAG AA：普通文字 4.5:1，大文本 3:1；focus indicator 与相邻颜色至少 3:1。亮暗主题分别实测最内层绘制元素。
- 正常 motion 下，路径生长/merge 汇流只表达状态变化，不循环抢注意力；不得以 scale 改变布局。
- `prefers-reduced-motion: reduce` 下取消路径绘制、粒子、脉冲和自动平移动画；状态直接落终点，功能与 ARIA 语义保留。仅把时长调短不算通过。

## 8. 截图与证据清单

文件名采用 `lrm-1394_<sha>_<fixture>_<theme>_<viewport>_<state>.png`，禁止在图片内加箭头或说明。

### 8.1 强制截图（20 张）

| Theme / viewport | T02 五分支 | T03 十二分支 | T04 多 merge | T09 选中 | T10 状态组 |
|---|---:|---:|---:|---:|---:|
| light / 1440×900 | 1 | 1 | 1 | 1 | 1 |
| dark / 1440×900 | 1 | 1 | 1 | 1 | 1 |
| light / 360×800 | 1 | 1 | 1 | 1 | 1 |
| dark / 360×800 | 1 | 1 | 1 | 1 | 1 |

T10 状态组可用同一尺寸的 contact sheet 仅作索引，但每个原始状态截图仍须单独附上。另附 768×900 的 T03/T04、200% zoom 的 T03、键盘 focus 的 T09、reduced-motion 最终态 T04；这些不替代上表。

### 8.2 Before/after

- Before：父单 LRM-1324 附件，仅用于说明旧胶囊/活动卡问题。
- After：必须来自集成 revision 的运行页面，同一用户路径、相同 viewport/主题与真实 fixture。
- 每张 after 记录 SHA、URL/route、fixture ID、浏览器及 DPR。设计源图、Storybook 静态 mock、注释图不能替代。

## 9. 缺陷路由

发现缺陷时在本单记录 `fixture + theme + viewport + zoom/motion + revision + expected/actual + 原始截图`，并回写唯一实现 owner：

| 缺陷 | 回写 |
|---|---|
| lineage 缺 parent/branch/merge，错误伪连 | LRM-1388 |
| typed DAG/status 映射、降级语义错误 | LRM-1389 |
| lane 交叉、稳定列、merge routing、遮卡 | LRM-1390 |
| 面板响应式、缩放/定位/筛选、小地图不一致 | LRM-1391 |
| 卡片层级、状态双编码、详情、focus/AA | LRM-1392 |
| 生长/汇流/聚焦动效与 reduced-motion | LRM-1393 |
| 500/2000 commit 窗口化导致视觉错位或扫读退化 | LRM-1395 |

同一根因只回写一个实现单；本视觉门不在旁路补算法或生产样式。

## 10. 实现定位提示

预计独占文件遵循 sibling PR 最终命名，优先落在：

- `packages/core/research/**/trajectory-*`：typed projection/fixture（LRM-1389）。
- `packages/views/research/components/trajectory-*`：面板、lane、卡片、小地图、详情（LRM-1390～1393）。
- 同目录 `trajectory-*.test.tsx` 与 e2e/visual fixture：结构、键盘、响应式和 transform 断言。
- `packages/views/locales/{zh-Hans,en,ja,ko}/research.json`：所有可见状态与 a11y 文案四语言同步。

若 sibling PR 采用不同真实路径，以实际单一实现为准；不得为了满足本表再创建平行组件。

## 11. 当前基线记录（2026-08-04）

- QA checkout：`origin/dev@eee9f84ed`。
- 当前 dev 尚无前端 Git 轨迹面板实现；只存在旧「探索轨迹」文案与其他非本面板轨迹代码。
- 父单 Before 附件已通过 `multica attachment view` 获取（340×91 PNG），但本机图像查看器因 sandbox 初始化故障未能渲染；因此本合同不对附件像素作臆测结论。
- 集成视觉结论：**BLOCKED（等待 LRM-1388～1393 可运行 revision）**。fixture audit/验收合同不受阻。
