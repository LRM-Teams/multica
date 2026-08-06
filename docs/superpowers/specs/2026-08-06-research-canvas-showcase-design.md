# Research Live Canvas V6 设计规格（2026-08-06）

> **性质**：调研会话主工作台的目标设计与前端执行合同，不是 3 个孤立特效的展示稿。
> **后端依据**：`2026-08-05-autonomous-research-system.md` §7.1/7.2/9.1/10/12、`2026-08-03-research-run-backend-design.md`、`presence-contract-v2.md`。
> **前端依据**：`packages/core/adapters/**`、`packages/core/types/research-v6.ts`、`packages/core/hooks/research-v6*/**`、`packages/views/research/graph-model/**`、现有 camera/motion/trajectory 基建。
> **实现计划**：`docs/superpowers/plans/2026-08-06-research-canvas-showcase.md`。

### 子规格索引

文本编码 Agent 不得只读本总览后自行补设计。每个实现 Issue 必须同时读取对应子规格：

| 子规格 | 负责回答 |
| --- | --- |
| `2026-08-06-research-live-canvas-data-contract.md` | 每个后端字段能显示什么、不能推断什么 |
| `2026-08-06-research-live-canvas-layout-spec.md` | 每个模块放哪里、各断点怎样重排 |
| `2026-08-06-research-live-canvas-node-card-spec.md` | 节点卡具体显示负责人、目标、动作、解决项、进展的方法 |
| `2026-08-06-research-live-canvas-viewport-performance-spec.md` | 点击居中、2/3 层展开、节点预算、Slice 与虚拟化 |
| `2026-08-06-research-live-canvas-route-topology-spec.md` | 主节点卡、路标、微圆点、弯路/失败/回流、曲线布局与路径束 |
| `2026-08-06-research-live-canvas-agent-execution-spec.md` | Agent/Task/Attempt 的真实状态和双向定位 |
| `2026-08-06-research-live-canvas-motion-direction-spec.md` | 10 类 transition 的镜头、动画、光效和降级 |

## 1. 结论

调研页要让用户在 5 秒内回答 5 个问题：

1. 现在研究到哪一步？
2. 哪些 Agent 正在做什么，实际启动了吗，多久没有新信号？
3. 哪些证据、Claim、Insight 和争议改变了当前结论？
4. 哪些路径正在扩散、融合、失败、失效或等待裁决？
5. 用户能从哪个节点继续、分叉、重试、改派或调整研究目标？

视觉目标不是堆叠 glow、粒子和大面积渐变。视觉效果来自**真实状态实时改变空间结构**：任务派发时分支长出，结果接纳时节点结晶，Insight 形成时多条路径汇聚，争议打开时关系断裂，Director 升级时出现明确的升级路径。每个动画都必须由已提交的 `transition_kind` 驱动，终态由 Snapshot/Delta 决定。

## 2. 现稿必须废止的判断

原稿有 6 个会直接导致实现偏离后端的问题：

1. **“纯前端、零后端、零 API”错误。** Live Canvas 必须消费 V6 Snapshot、Delta、Projection Slice、Presence 和 NodeCommand；否则只能演示假进度。
2. **只做视图切换、拖拽预览、入场脉冲，范围过小。** 它没有 Agent/Attempt 状态、递归 Insight、争议、Slice、重连和大图性能。
3. **把旧 V5 `ResearchGraphNode/Edge` 当长期合同。** 新渲染层必须只读统一 `CanvasNode/CanvasEdge/CanvasDelta/CanvasSlice`，V5/V6 差异由 adapter 消化。
4. **拖拽“归簇”容易暗示真实合并。** 真正融合只能来自后端 `Insight Derivation` 和 `derived_from | integrates`；前端拖拽最多是操作预览，不能改变 canonical graph。
5. **`supports` 入边不等于形成 Insight。** `supports` 是语义关系；Insight 形成只认 `integration_formed` 与后端 Insight/Derivation 节点。
6. **默认加载整张图不可接受。** 目标 fixture 是 10k 节点；浏览器必须按 Slice 展开、分页和虚拟化。

## 3. 产品结构

### 3.1 页面分区

```text
ResearchSessionChrome
├─ StageRail                 当前 S1–S4、运行状态、同步/缺口状态
├─ LiveAgentDeck             Agent roster、Attempt 状态、当前任务、时长
├─ ResearchLensBar           执行 / 探索 / 证据 / Insight / 争议
└─ ResearchWorkspace
   ├─ InfiniteCanvas         地标卡 + 路标 + 微圆点组成的有机语义路线图
   ├─ ContextBreadcrumb      当前节点 → 上游 → Run
   ├─ NodeInspector          详情、证据、历史、允许的 NodeCommand
   ├─ Minimap                当前 Slice 与已加载范围，不伪装全图
   └─ TrajectoryEntry        进入独立 Git 多轨迹探索器
```

`TrajectoryExplorer` 是独立功能视图，不叠在无限画布上。无限画布回答“当前研究结构与状态”，轨迹探索器回答“多 Agent 的执行路径怎样分叉、交叉、合并和终止”。

### 3.2 Lens 不是不同数据源

Lens 只改变强调与筛选，不复制 canonical graph：

| Lens | 首要节点 | 首要关系 | 默认侧栏 |
| --- | --- | --- | --- |
| 执行 | task、attempt、team_membership | depends_on、triggered、staffed_by | Agent/Attempt 详情 |
| 探索 | question、hypothesis、branch、query_execution | decomposes、tests、created_for | 研究方法与下一步 |
| 证据 | source_candidate、source_snapshot、observation、claim | produced、derived_from、supports、contradicts | 证据护照 |
| Insight | insight、insight_derivation、integration_round | integrates、derived_from、invalidates | 组合输入与 freshness |
| 争议 | dispute、dispute_position、deliberation、decision | challenged_by、discussed_by、escalated_to、resolved_by | 争议与裁决历史 |

Lens、选中节点和独立视图写入 URL 查询参数；画布坐标、临时折叠和动画队列不写 URL。

## 4. 后端接口到 UI 的唯一映射

### 4.1 数据面

| 后端/客户端合同 | UI 用途 | 禁止行为 |
| --- | --- | --- |
| `ResearchV6Snapshot` | 首屏或 resync 后建立 canonical 节点/边集合 | 不从多页不同 `snapshot_id` 拼接 |
| `ResearchV6Delta` | 幂等 upsert/tombstone、局部布局、语义动画 | 不用动画推进研究状态 |
| `ResearchV6ProjectionSlice` / `CanvasSlice` | 按 root、方向、关系、深度、状态、importance 分页展开 | 不一次下载整个 Run |
| `GET /api/research/sessions/:id/presence` | 完整 Fleet roster 与 Agent phase | 不用群消息或头像绿点推断 running |
| task/attempt Projection node `detail` | 执行真相、失败、lease、取消、最近信号 | 不从标题/summary 猜进度 |
| `POST .../nodes/:nodeId/commands` | `continue | fork | retry | reassign` | 不在前端伪造新节点或改派结果 |
| `POST .../steer` | 用户改变目标、范围、语言、来源策略和运行预算 | 不直接改画布节点内容 |

### 4.2 渲染层只认统一模型

所有生产组件消费：

- `CanvasSnapshot`：`snapshotId`、`throughEventSequence`、`graphContentHash`、nodes、edges；
- `CanvasNode`：`id/kind/subtype/title/summary/status/importance/freshness/detailRef/actor/planVersion/sequence/payload/timestamps`；
- `CanvasEdge`：稳定 id、from、to、relation、createdAt；
- `CanvasDelta`：序列边界、upsert、tombstone、`affectedRootIds`、`transitionKind`；
- `CanvasSlice`：root、direction、relation filter、depth/status/importance、未加载数量与 `canExpand`。

生产 renderer 不直接读取 V5/V6 wire shape。未知 node kind 降级为 `GenericNodeCard`；未知 edge/transition 显示中性关系或无动画，不能崩溃。

### 4.3 Agent 与 Attempt 的显示合同

Presence 展示 Fleet 全员：

| `phase` | 显示 | 判断依据 |
| --- | --- | --- |
| idle | 待命 | roster 存在且无当前执行事实 |
| queued | 排队 | task/attempt 已建立但 runtime 未实际启动 |
| running | 执行中 + 活动时长 | `runtime_started_at`/有效执行事实 |
| done | 已完成 + 最近产出 | terminal attempt/result |
| failed | 失败 + 原因 + 可操作入口 | terminal failure |
| stale | 信号过期 + stale reason | `expires_at` 或 presence 15m 规则 |

Attempt 详情直接展示：`attempt_status`、`attempt_number`、`assigned_agent_id`、`inbox_task_id`、`dispatch_key`、`failure_class`、`diagnostics`、`pending_failure_*`、`dispatched_at`、`runtime_started_at`、`runtime_last_observed_at`、`runtime_lease_expires_at`、`cancellation_requested_at`、`cancellation_completed_at`、`result_submitted_at`、`completed_at`。

`dispatching` 与 `running` 必须是两个视觉状态；`cancelling` 占用执行额度且不可显示为空闲。任何缺失字段显示“未知”，不能自动归为成功或运行。

### 4.4 当前落地状态必须诚实显示

截至审阅基线 `origin/dev@ed1bdff3e`：

- 已合并：统一 Canvas adapter、V6 schema/client、Delta reducer、Slice 前端基建、GraphModel、局部 positioner、camera focus、Presence v2、NodeCommand、Attempt lease/cancel 字段。
- 尚未发现服务端 `/api/research/v6/runs/:id/projection/{snapshot,deltas,resume}` 路由；客户端方法存在不等于生产接口已接通。
- Autonomous Research 计划中的完整 Insight Derivation、Dispute Graph 和 Projection N 阶段仍有未完成项。

前端必须有能力探测：V6 可用则走 V6；不可用则使用已验证的 V5 adapter。不能用 fixture 冒充生产 V6。

## 5. 无限画布的动态组合规则

### 5.1 初始视口

- 首屏只请求当前 Run 的入口 Slice：桌面建议 `maxDepth=2`、`limit≤120`，窄屏 `limit≤48`；实际上限服从服务端。
- 默认聚焦最高 importance 的活动节点、阻塞节点或最近 transition 的 affected root。
- Minimap 只表达“已加载 Slice”，必须显示已加载/未加载数量，不能让用户误以为看到完整图。
- 可见节点服从 viewport 与 route-topology 子规格的 soft/hard limit；desktop 图节点 DOM 总量不得超过 220，10k fixture 不允许建立 10k DOM 节点。
- 主图禁止横平竖直的全量树。主节点保留为地标卡，中等节点降为路标，低 importance 的调研步骤降为可定位微圆点；失败、retry、stale 和汇入使用稳定曲线保留过程。

### 5.2 小节点 → 大节点 → 更大节点

只允许两种组合：

1. **Canonical Insight**：后端产生 `insight` + `insight_derivation`，层级、输入、贡献 Agent、freshness 全部来自事实；卡片可逐层展开。
2. **Display Group**：前端为性能折叠的显示单位，必须标“显示分组 · n 节点”，使用虚线/中性表面，不得使用 Insight 徽标，不写回后端。

摘要态只显示顶层 Insight、选中路径、stale/失败路径、活动 task/attempt 与阻塞 dispute。其他过程压缩为 Route Bundle/Display Group。展开时通过 Projection Slice 逐层加载；`canExpand=false` 不显示假展开入口。

### 5.3 拖拽与操作

- 节点位置拖拽只保存用户布局，不改变 graph relation。
- 拖到另一节点可打开“操作预览”，列出后端允许的 `continue/fork/retry/reassign`；没有对应 NodeCommand 时只显示比较，不显示“合并”。
- 真正 Integration/Insight 必须等待后端事件；UI 收到 `integration_formed` 后再播放融合动画。
- 所有 destructive 或改变执行的操作需要确认或可撤销窗口，并携带 `client_request_id` 防重复。

## 6. 卡片系统

### 6.1 6 个卡片族覆盖 30 类节点

| 卡片族 | node kinds | 信息重点 |
| --- | --- | --- |
| Execution | task、attempt、result_artifact | 状态、Agent、Attempt、时长、失败/产出 |
| Inquiry | question、hypothesis、branch、divergence_pass | 小目标、验证方式、不确定性、分支 |
| Corpus | search_plan、query_execution、source_candidate、screening_decision、source_snapshot、observation、claim | 来源、筛选、证据、Claim 状态 |
| Integration | insight、insight_derivation、integration_round、integration_contribution | 层级、输入数、覆盖、stale、贡献者 |
| Dispute | dispute、dispute_position、deliberation、deliberation_turn、decision | 立场、证据关系、升级、裁决历史 |
| System | team_formation、team_membership、capability_observation、report_revision、evaluation_defect、monitoring_cycle、episode | 团队变化、能力、修订、缺陷、周期 |

### 6.2 统一卡片结构

```text
┌─ kind glyph / title / state badge / action menu ─┐
│ bounded summary（最多 2 行）                      │
│ semantic facts：Agent、attempt、证据、层级等       │
│ progress rail / freshness / updated time          │
└─ typed-edge ports + unloaded count ───────────────┘
```

- <35%：只保留 root、selected、active/blocking、顶层 Insight/Decision 地标卡，最多 12 张；其他节点切换成路标、微圆点和路径束。
- 35–65%：紧凑地标卡 + 主要路标；不把标准卡整体缩小成不可读白块。
- 66–119%：显示标准卡的标题、负责人、目标、当前动作和进展计数。
- ≥120%：显示扩展事实与操作入口，不把完整详情塞进卡片。
- 文字容器必须 `min-w-0` + clamp/break；数值和时长用 tabular numerals。
- 状态不能只靠颜色；同时使用 badge 文案、glyph、线型或表面结构。

### 6.3 视觉语言

风格为“高精度研究控制台”：低噪声底图、清晰卡片层级、局部光能反馈。

- 基础层只用现有 semantic tokens；禁止裸 hex、Tailwind 固定 palette、大面积饱和渐变。
- Glow 只用于 selected、active transition、blocking dispute 和 integration moment，静止后收回。
- 卡片 hover 只改变描边/表面，不 scale；selected 使用稳定的双层 ring 和定位锚点。
- Edge 默认低对比；选中节点的一阶 typed relations 提升，其他边降到仍可读的 35–45%。
- 粒子不能承担状态含义，不能常驻，低性能模式全部关闭。

## 7. 4 个应当让用户记住的动态时刻

1. **Branch Bloom**：`branch_spawned` 后新分支从父节点端口长出，镜头安全居中，节点依次显现。
2. **Insight Crystallization**：`integration_formed` 后输入路径收束到 Insight 卡，卡片显示层级、输入数与贡献 Agent；终态仍保留输入关系。
3. **Conflict Fracture**：`dispute_opened` 后 contradicts/challenged_by 关系显形，争议卡展开立场扇面；不使用循环抖动。
4. **Director Escalation**：`lead_escalated` 后从 deliberation 到 Director/lead task 出现唯一的粗阶梯线，同时 Agent Deck 显示接手者。

路径层还有 4 个明确分镜：新路线用 Route Sprout 长出；失败用 Dead-end Settle 留下断口；retry 用 Retry Hairpin 绕出新 Attempt；用户显式追踪时用 Detour Trace 沿弯路、失败点和回流路径播放一次。完整规则见 route-topology 与 motion-direction 子规格。

其他 transition：`task_dispatched` 下沉到 Agent、`result_accepted` 结晶、`insight_staled` 沿祖先路径传播、`deliberation_progressed` 增加 turn、`team_membership_changed` 更新 roster、`report_revised` 显示版本替换。

Motion 约束：只动画 `transform/opacity`；单元素 ≤320ms，总编排 ≤900ms，stagger 上限 6；新 delta 可中断旧动画；队列峰值 ≤64；后台恢复或 resync 只淡入终态，不补播历史。Reduced Motion 直接落终态并保留静态语义。

## 8. Camera 与导航

- 点击卡片：该卡移动到 safe centre，避开右侧 Inspector、顶部 Chrome、底部 Dock。
- 展开/折叠：锚点保持在原屏幕位置；新节点从锚点周围显现，不把用户甩走。
- Delta：只有 affected root 在视口外且属于 blocking/selected context 时才自动聚焦；普通后台更新不抢镜头。
- 支持 Back/Forward 恢复 lens、node、view；Esc 返回触发元素。
- 键盘：方向键按图邻接移动，Enter 展开/打开，Space 固定，`F` 聚焦，`[`/`]` 上下层。

## 9. Agent Live Deck

桌面为顶部可折叠横带，窄屏为底部 sheet 入口。每个 Agent 单元显示：

- 头像、姓名、角色；
- phase 文案与非颜色 glyph；
- 当前 task 标题、Attempt #、已运行/等待时长；
- 最近产出或失败原因；
- `runtime_last_observed_at` 与 lease 风险；
- 点击后同时聚焦 Agent 对应 task/attempt 节点；从节点点 Agent 反向高亮 Deck。

Deck 排序：blocking/failed → running → queued → stale → idle → done。排序变化用 FLIP 位移且不改变焦点 DOM；同一 Agent 在更新前后保持稳定 key。

## 10. 状态、错误与恢复

| 状态 | UI |
| --- | --- |
| 首屏加载 | Chrome/Deck/Canvas 分区骨架，不渲染假节点 |
| Slice 加载 | 被展开端口局部进度，不锁整张画布 |
| 空 | 说明尚无 Projection，并提供开始/恢复入口 |
| Delta gap | 显示“同步中”，保留最后确认状态，8s 后 resync |
| resync | 冻结命令写入，重新取 Snapshot，完成后淡入终态 |
| offline | 显示离线横幅，禁止暗示实时；保留只读画布 |
| unknown kind | Generic 卡 + 原始 kind + 诊断，不崩溃 |
| 权限不足 | 不泄露标题、Agent、证据或拓扑；显示受限卡 |

## 11. 性能、可访问性和质量门

- 10k canonical 节点：首屏只取 Slice；desktop 图节点 DOM 预算 ≤220，其他断点服从 viewport 子规格，边按可视窗口裁剪。
- desktop 同屏 semantic node soft/hard=120/180；Landmark Card hard=48，其他步骤使用 Waypoint、Trail Dot 或 Route Bundle，所有类型仍受总量上限约束。
- Delta 只重算 `affectedRootIds`，未受影响节点坐标保持不变。
- 不在 render 做 `getBoundingClientRect`；批量 DOM read/write；不使用 `transition: all`。
- 所有 icon-only button 有 `aria-label`，装饰图标 `aria-hidden`，异步状态用 `aria-live="polite"`。
- focus ring 不被 canvas/overlay 裁切；触控目标 ≥44×44 CSS px。
- 200% zoom、360×800、768×900、1440×900、亮/暗主题都必须验收。
- 每个 PR：相关 Vitest、`pnpm typecheck`、`pnpm react:doctor`；涉及视觉的 PR 生成截图文件，由多模态验收者查看，文本模型不得读取图片。

## 12. 验收场景

1. 30 类节点各至少 1 个，未知第 31 类降级，页面不崩溃。
2. 12 个 Agent：running/queued/idle/failed/stale/cancelling 同屏，节点与 Deck 双向定位。
3. 4 个 Claim → 2 个 L1 Insight → 1 个 L2 Insight；一个输入失效后祖先 stale，重新整合后恢复。
4. dispute → 3 positions → deliberation deadlock → Director escalation → decision → 新证据 reopen。
5. 连续 100 条含重复/乱序 Delta，最终 hash 与无动画重放一致，缺口触发一次 resync。
6. 10k 节点 Run 首屏不全量下载、不创建全量 DOM；多次 Slice 展开后仍可操作。
7. 8 条 Agent 轨迹在独立 TrajectoryExplorer 中可追踪分叉、交叉、合并、终止和负责人。
8. Reduced Motion、低性能、后台恢复、断网重连、200% zoom 均保持完整功能。
9. 25% zoom 只保留 ≤12 张地标卡；用户仍能从弯曲路径、微圆点、失败端点和路径束看出主路、弯路、retry 与汇入。

## 13. 不做

- 不用 Canvas 动画、群消息或 Agent prose 作为研究事实。
- 不把 Display Group、用户拖拽或视觉靠近写回为 Insight/关系。
- 不新增第二套 V6 type/registry/graph store。
- 不让每个组件直接访问 API；server state 进入 React Query，client display state 进入 core Zustand。
- 不在无限画布中复制独立 Git 轨迹探索器。
- 不用 orthogonal tree、等距 rank 列或所有节点同尺寸卡片作为最终主画布。
- 不为“震撼”牺牲可读性、焦点、Reduced Motion、性能或真实数据边界。
