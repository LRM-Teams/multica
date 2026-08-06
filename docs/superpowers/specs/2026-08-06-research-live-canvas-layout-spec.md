# Research Live Canvas V6 — 页面布局与模块位置

> 本文固定每个模块的位置、尺寸优先级和断点重排，避免不同 Agent 各自堆浮层。

## 1. 桌面布局（内容区 ≥1200px）

```text
┌──────────────── Session Chrome · 56px ───────────────────────────┐
│ 标题/阶段/同步状态                         Run actions / delivery │
├──────────────── Live Agent Deck · 88px（可折叠到 36px）─────────┤
│ [阻塞] [运行] [运行] [排队] [stale] [idle…]                     │
├──────────────── Lens Bar · 44px ─────────────────────────────────┤
│ 执行  探索  证据  Insight  争议     loaded 86 / hidden 412      │
├───────────────────────────────────────────────┬──────────────────┤
│                                               │ Node Inspector   │
│ Infinite Canvas                               │ 360–400px        │
│                                               │                  │
│ Breadcrumb（左上）                            │ sticky header    │
│ Sync badge（右上）                            │ scroll body      │
│                                               │ sticky actions   │
│                    Minimap（右下，Inspector 左）│                  │
│       Canvas Dock（底部居中）                  │                  │
└───────────────────────────────────────────────┴──────────────────┘
```

### 固定位置

- Session Chrome：页面顶部，不覆盖画布。
- Agent Deck：Chrome 下方；默认展开，用户折叠后保留异常数量、running 数和总人数。
- Lens Bar：Deck 下方，sticky；不放进浮动 Dock。
- Breadcrumb：画布左上安全区内，最多 3 级；更深用“…”菜单。
- Node Inspector：右侧独立列；选中节点才打开，不用覆盖式浮卡压住图。
- Minimap：Inspector 左侧画布右下；Inspector 开关变化时更新 camera insets。
- Canvas Dock：底部居中，包含 fit、zoom、层级、折叠、轨迹入口；不放节点业务操作。

## 2. 中宽布局（768–1199px）

- Agent Deck 折成 2 行：第一行异常/running，第二行可横向滚动 roster。
- Lens Bar 保持单行；次要计数收进“视图信息”。
- Inspector 默认右侧 320px overlay sheet，打开后 camera safe region 扣除其宽度；Esc 关闭。
- Minimap 缩为 132×88，不能和 Inspector、Dock 重叠。
- 每次最多显示 72 张 canonical 卡，硬上限 96。

## 3. 窄屏布局（<768px）

```text
Session Chrome · 52px
Compact Stage + sync · 40px
Canvas / focused list（剩余高度）
Bottom Dock · safe-area
Agent Deck / Inspector / Lens → 互斥 bottom sheet
```

- 默认显示聚焦节点及上下各 1 层，不显示全自由画布。
- Agent Deck、Inspector、Lens 共享一个 bottom sheet，一次只开一个。
- Minimap 改为“当前位置：已加载 24 / 总后代 318”的位置摘要；不画不可点的小图。
- 触控目标 ≥44×44；底部使用 `env(safe-area-inset-bottom)`。
- 不允许 document 级横向滚动。

## 4. Z 轴与覆盖规则

从低到高：canvas grid → edge → card → selected relation → breadcrumb/minimap/dock → Inspector/sheet → modal/toast。

- Edge/particle 不能穿过卡片正文、badge、Agent 名和操作菜单。
- Tooltip 不承载必须信息；状态、目标和负责人常驻。
- Toast 只报告命令结果，不重复 Inspector 错误。
- 打开 modal 时 canvas 与 sheet 使用 inert，关闭后焦点回触发按钮。

## 5. 信息优先级

首屏同时可见的信息按此顺序分配空间：

1. blocking/failed/cancelling；
2. 当前 running Agent 与 task；
3. selected node 的目标/动作/进展；
4. 最近形成的 Insight/Dispute；
5. 其他 loaded path；
6. idle roster 与历史终态。

空间不足时从 6 向 1 折叠，不能先隐藏异常和当前执行。

## 6. Inspector 分区

```text
Header：类型、标题、状态、负责人、关闭
Section 1 · Why：目标、进入条件、上游依赖
Section 2 · Now：当前动作、Attempt、时长、lease
Section 3 · Progress：解决项、最新进展、未解决问题
Section 4 · Evidence：来源、Claim、关系
Section 5 · History：attempt/decision/revision 时间线
Footer：允许的 NodeCommand
```

空分区不渲染空壳；缺字段用一条中性说明。Footer sticky，但不能盖住最后一行内容。

## 7. 视觉密度

- 一屏只允许 1 个主视觉时刻；其他更新使用静态 badge 或轻淡入。
- 同时常驻 glow ≤3 处：selected、最高优先异常、当前 transition。
- 同屏 badge 每卡最多 3 个，其余进入 Inspector。
- 标题最多 2 行，卡面 summary 最多 2 行，Agent/task 名必须各自可辨，不能拼成一段 prose。
