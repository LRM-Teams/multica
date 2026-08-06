# Research V6 语义 Transition 动效引擎 — 设计规格（LRM-1471 / UI-03）

> 状态：设计定稿，待前端实现验收
> 日期：2026-08-06
> 父目标：LRM-1444 · 后端设计：`docs/superpowers/plans/2026-08-05-autonomous-research-system.md` §7.2
> 承接人：前端（交接见 issue LRM-1471 评论）；验收人：UI 设计·聊天体验（本 Agent）
> 本文是**可直接实现的设计规格**，不是抽象原则。所有参数、组件、目标文件、状态与验收标准均已写清。

---

## 0. 摘要与边界

本规格把投影层提交的 10 类 `transition_kind` 映射为**一套统一语法的动态展示**（出现 / 合并 / 展开 / 重连补帧 / 相机联动），并定义可中断、可合并降级、可在海量 delta 下不失控的**动效引擎**（transition queue）与 **motion token**。

**硬边界（与后端 plan §7.2 / LRM-1444 数据边界一致）：**
- 动效只**表达**状态变化，**永不修改 canonical 数据**（节点事实、层级、关系）。
- 前端**不得**从摘要、聊天文本、动画状态推断 canonical research state；已提交的 `transition_kind` 只表达语义变化与关联实体。
- 显示分组只属于前端显示状态，**不得写回为真实 Insight**。
- 接口未落地的部分（投影 delta 类型、`transition_kind` 枚举、切片）只使用**严格遵循文档的 contract fixture**；生产路径不保留伪数据。

**并行边界（避免与并行 slice 冲突）：**
- 本 slice 是**10 类 transition 的语义映射 + 统一队列 + token + demo 规格**。
- 不重复实现 Git 多轨迹 lane 动效引擎（LRM-1393/1400/1446/1447/1448，含 trajectory-motion-intents / animator / controller / consumer frame hook），也不重复节点卡片视觉注册表（UI-01）、递归 Insight 组合树展开合并 / 失效传播（UI-02）、Dispute/Deliberation/升级可视化（UI-04）。`transition_kind` 与轨迹 lane 事件是同一渲染面的**两层**：本 slice 定义语义→展示语法，轨迹引擎定义 lane 位移本体；二者通过统一的 motion token（§3）对齐，不各自发明一套时长/缓动。
- 目标产物目录 `packages/views/research/motion` 由本 slice 定义接口与 token；并行引擎可引用这些 token，不互相覆盖文件。

---

## 1. 问题诊断

现网 research 画布已有**零散**的动效（`node-enter-motion`、`canvas-reorg-motion`、`semantic-aggregation-motion`、`hero-cta-motion`），每个各自定义时长/缓动/stagger，没有统一 token；也没有覆盖 `transition_kind` 这 10 类语义的区别表达（分支 vs 合并 vs 冲突 vs 升级当前长得几乎一样），且缺少可中断 / 海量 delta 合并 / 重连补帧 / 相机联动机制。

**要解决的问题：**
1. 10 类语义变化缺乏**可区分**的动态表达（AC1）。
2. 连续大量 delta 会让动画队列失控、最终视图与无动画重放不一致（AC2）。
3. 中断、后台恢复、Reduced Motion、低性能模式没有统一验收方案（AC3）。

**设计目标：** `transition_kind` → 语义分类 → 统一动画语法（出现/合并/展开/重连/相机联动）→ 可中断队列 → token。

---

## 2. transition_kind → 统一语法映射

### 2.1 10 类 transition 的语义主分类

把 10 类 `transition_kind` 归并为 4 个语义大类 + 1 个特殊类。**大类决定动效签名（扩散/融合/冲突/升级），具体 kind 决定触发对象与附加标记。**

| transition_kind | 语义大类 | 展示动词 | 关键视觉标记 | 关联实体 |
| --- | --- | --- | --- | --- |
| `branch_spawned` | 扩散 Appear | 出现（新分支） | 从父节点**放射扩散**（+scale/translate） | 父 Branch/Question → 新 Branch |
| `task_dispatched` | 扩散 Appear | 出现（任务被派发） | 从所属分支**下沉出现** + 执行徽标 | Branch → Task |
| `result_accepted` | 出现/推进 Advance | 出现 + 打勾 | 节点状态提升（accepted 勾） + 轻微上浮 | Task/Attempt → 结果 |
| `integration_formed` | 融合 Merge | 合并 | 多输入**向心收敛融合**成一个 Insight 节点 | 多个节点 → Insight |
| `insight_staled` | 失效 Stale | 失效 | 顶层 Intent 链路**由实变灰/打洞**（stale 传播） | Insight 及其派生子树 |
| `dispute_opened` | 冲突 Conflict | 冲突 | 两个对立节点**侧向拔出 + 拉开** + 警示色 | Claim ↔ Claim |
| `deliberation_progressed` | 升级 Escalate | 升级 | 争议卡**上浮 + 进度填充**（轮次推进） | Dispute |
| `lead_escalated` | 升级 Escalate | 升级 | 节点**沿上级方向推进 + 强调描边** | Deliberation → Director |
| `team_membership_changed` | 扩散 Appear/推进 | 人员加入/退出 | 成员齿片**淡入/淡出**（非路径位移） | Agent ↔ Team |
| `report_revised` | 推进/修订 Revise | 修订 | 报告节点**刷新脉冲**（内容更新） | Report |

> **可执行规则 ①（结构约束）**：10 类 kind 是字符串字面量联合类型，映射表用 `Record<SemanticTransitionKind, SemanticDisplayGroup>` 单一结构，新增 kind 若不在表中则不编译（tools 档位②）。不允许出现"无对象告警后悄悄不展示"的旁路。

### 2.2 展示动词统一语法（display verb）

每个大类对应一个「进入/变更/退出」三元组签名，全部只动 `transform` + `opacity` + 静态 `status`/`highlight`（永不改布局值，与 trajectory-motion 硬规则一致）。

| 大类 | verb | 起始帧 transform | 结束帧 | 时长上限 | 说明 |
| --- | --- | --- | --- | --- | --- |
| 扩散 Appear | appear | `translateY(8px) scale(0.96)` + `opacity 0` | `none` + `opacity 1` | ≤300ms | 复用 `node-enter-motion` 主轴 |
| 融合 Merge | merge | 多输入 `translate(x,y)` 向 anchor 收敛 + `opacity 0.4→1` | `none` | 单元素 ≤320ms | 复用 `semantic-aggregation-motion` split/merge 相位 |
| 冲突 Conflict | conflict | 双节点 `translateX(±12px)` 相对拉开 + 警示色描边**先出现后保留** | `none` + 保留 border | 单元素 ≤320ms | 冲突必须留下静态状态标记（不消失） |
| 升级 Escalate | escalate | 节点 `translateY(-8px)` 沿上级方向 + `filter` 强调 | `none`（静态 emphasis 保留） | ≤320ms | 升级后保留强调描边 |
| 失效 Stale | stale | 目标子树 `opacity 1→0.55` + 灰化（不打洞到消失） | 保留灰化态 | 逐节点 ≤300ms | 失效是**进入失效状态**，不是删除 |
| 修订 Revise | revise | 报告节点 `box-shadow 脉冲` + content `opacity 0.6→1` | 保留脉冲结束态 | ≤300ms | 内容刷新，不移动位置 |
| 补帧 Reconnect-backfill | reappear | 重连后落后节点 `opacity 0→1`（无路径位移） | `opacity 1` | ≤260ms | 重连补帧不得重放历史路径 |
| 相机联动 Camera-sync | camera | 目标节点移到视野中心 + scale 微调 | 静态 | ≤400ms | 唯一允许动布局相机的 verb |

> **可执行规则 ②**：`conflict` / `escalate` / `stale` / `revise` 必须**保留静态终态标记**（描边/灰化/轮廓），不能动画结束回到与开始前无差别——否则"冲突/升级/失效"没有持久表达，违反 AC1「动作能区分分支、合并、冲突和升级」。对照物（Y 必须在）：动画结束后 `data-verb` 标记与静态 highlight 类必须仍在 DOM。

### 2.3 出现（Appear）细分
- 新节点无可见 anchor（新增根）：走普通 enter 路径（`node-enter-motion`）。
- 新节点有锚（分支/任务）：走 `appear` verb 从锚扩散。
- `branch_spawned` 用放射扩散（scale），`task_dispatched` 用下沉（translateY(8) + 执行徽标）。

### 2.4 合并（Merge）
- 输入节点 count>1 才构成 `merge`；单输入不合并，走 `result_accepted` 推进。
- 合并目标 = 新 Insight 节点；使用 `integration_formed`。
- 合并输入中的"移除的旧节点"走退出（淡出），新 Insight 走进入（收敛）。

### 2.5 展开（Expand）
- 展开是用户触发的**手动操作**，不是 `transition_kind`；但它与 `deliberation_progressed` 的轮次推进共用「上浮 + 进度填充」节奏。
- 展开采用 `react-spring` / CSS 高度+opacity 动画（≤240ms），内容懒加载（对接 FE-06 接力组合节点容器）。

### 2.6 重连补帧（Reconnect-backfill）
- WebSocket 重连后，缺口 delta 补齐（对接 FE-02 LRM-1464）。
- 缺失的历史节点**补帧出现为终态**（无路径重放），用 `reappear` verb（opacity 0→1，无 translate）。
- **可执行规则 ③**：重连补帧的节点**不播放**其历史 transition 动画（否则 100 条历史会级联重放），只补终态终帧 + 一个统一淡入。这是 AC2「最终视图与无动画重放一致」的关键。

### 2.7 相机联动（Camera-sync）
- 当新节点/争议/升级成为焦点时，画布相机平滑平移到该节点 + 微 scale（≤400ms，缓动见 §3）。
- 相机联动是**唯一允许动布局相机**的 verb；其余 verb 不得改变用户已保存的布局坐标。
- 用户手动拖拽/缩放画布时**立即取消**相机联动（中断优先）。

---

## 3. Motion Token（统一 token 表）

> 目标：新增 `packages/views/research/motion/tokens.ts`，作为整个 research 动效的唯一 token 源。并行轨迹引擎（LRM-1446 animator）应引用这些 token，不另发明时长。**已存在**的 `node-enter-motion` / `canvas-reorg-motion` / `semantic-aggregation-motion` / `hero-cta-motion` 常量逐步迁移到共享 token（过渡期向下兼容，见 §7）。

| Token | 值 | 用途 |
| --- | --- | --- |
| `MOTION_SINGLE_MAX_MS` | 320 | 单元素动画时长硬上限 |
| `MOTION_TOTAL_BUDGET_MS` | 900 | 一批次动画总预算（超出截断到 0 位移） |
| `MOTION_EASING` | `cubic-bezier(0.22, 1, 0.36, 1)` | 默认进入/合并/升级缓动（出快入缓） |
| `MOTION_STAGGER_MS` | 40 | 同批相邻节点 stagger |
| `MOTION_STAGGER_CAP` | 6 | stagger 最大步数（防大批次拖尾） |
| `MOTION_START_MS` | 80 | 批次起始延迟 |
| `MOTION_APPEAR_MS` | 300 | appear verb 时长 |
| `MOTION_MERGE_MS` | 320 | merge verb 单元素时长 |
| `MOTION_CONFLICT_MS` | 320 | conflict verb 时长 |
| `MOTION_ESCALATE_MS` | 320 | escalate verb 时长 |
| `MOTION_STALE_MS` | 300 | stale verb 单节点时长 |
| `MOTION_REVISE_MS` | 300 | revise verb 时长 |
| `MOTION_REAPPEAR_MS` | 260 | 重连补帧 verb 时长 |
| `MOTION_CAMERA_MS` | 400 | 相机同步时长 |
| `MOTION_CONFLICT_GAP_PX` | 12 | 冲突相对位移 px |
| `MOTION_ESCALATE_RISE_PX` | 8 | 升级上浮 px |
| `MOTION_APPEAR_RISE_PX` | 8 | 出现上浮 px |
| `MOTION_STALE_OPACITY` | 0.55 | 失效态终端透明度 |
| `MOTION_REDUCED` | 见 §4.1 | Reduced Motion 全部位移归 0 |

**低性能模式 token：** `MOTION_LOW_PERF_MAX_MS`（600ms 动画抽帧至 30fps 目标）、关闭 glow/blur（`filter` 降级为纯 `opacity`）。

---

## 4. 可中断与降级（AC3）

### 4.1 Reduced Motion（`prefers-reduced-motion: reduce`）
- 所有位移类 verb（appear/merge/conflict/escalate/revise/camera）**位移归 0**，仅保留：
  - 静态 highlight / status 标记（conflict 描边、escalate 强调、stale 灰化）仍在；
  - 统一淡入（opacity 0→1，≤200ms）与**即时布局**。
- **实现**：复用 `packages/views/common/use-prefers-reduced-motion.ts` + CSS `@media (prefers-reduced-motion: reduce)`（沿设备 side-effect）。
- **可执行规则 ④**：动效必须在 CSS / hook 两层都降级——一层用 `motion-reduce:`（Tailwind），一层用 `usePrefersReducedMotion()` 门禁 class 的**是否发射**（`base.css` 后导入、单 class 在真实 Chromium reduce 下失效的教训，LRM-1362）。每条位移动画必须二者选一覆盖，不能只盖一层。

### 4.2 低性能模式
- 进入条件：`navigator.hardwareConcurrency <= 2` 或 `deviceMemory <= 4`，或一次队列达低性能阈值（§5.3）时自动降级。
- 降级动作：动画目标帧率降为 30fps（RAF 节流）；`filter: blur/glow` 全部关闭（只留 opacity）；位移幅度减半或归 0；总预算缩短至 `MOTION_TOTAL_BUDGET_MS` 的一半——**最终终态必须与无动画重放一致**（这是 AC2 底线，降级只改变过程，不改变终态）。
- 可执行规则 ⑤：降级只影响**动画的呈现过程**，绝不能影响 layout / canonical / 状态机。用一条回归锁定「降级开启 → 终态 DOM 与无动画一致」。

### 4.3 中断（Interrupt）
- 任意新的 `transition_kind` 到达时，可**取消**同 lane/同 kind 队列中尚未开始或正在进行的旧动画，从当前状态继续（不回到起点）。
- 中断遵循并行轨迹引擎已确立的语义（LRM-1400：同 lane+kind 预算窗内合并；cancel 后新事件从当前状态续跑）。
- 相机联动被用户手动拖拽/缩放画布时**无条件立即取消**。

### 4.4 后台恢复（Background restore）
- 文档 `visibilitychange` 为 hidden 后，新排队事件**只记录不入队动画**（不调度 RAF）。
- 回到 visible 时，隐藏期间积压的事件按 §5 的合并规则坍缩（同 lane+同 kind 合并、超出预算丢弃多余），**逐个以终态补帧出现**，不级联重放。
- 可执行规则 ⑥：恢复后最多一次性播放 `MOTION_STAGGER_CAP` 次动画，其余节点直接落终态；保证 100 条后台 delta 恢复时不失控（AC2）。

---

## 5. Transition Queue（动效队列与背压）

> 目标：新增 `packages/views/research/motion/transition-queue.ts`（纯函数状态层，无 DOM/RAF/React，可单测）。并行引擎的 frame hook 可消费它的输出。

### 5.1 输入 contract（对接 FE-01 投影 delta）
```ts
type SemanticTransitionKind =
  | "branch_spawned" | "task_dispatched" | "result_accepted"
  | "integration_formed" | "insight_staled" | "dispute_opened"
  | "deliberation_progressed" | "lead_escalated"
  | "team_membership_changed" | "report_revised";

// 契约 fixture（接口未落地时使用，严格遵循 plan §7.2）
interface ProjectionTransitionEvent {
  transition_kind: SemanticTransitionKind;
  related_ids: string[];       // 关联实体稳定 ID
  anchor_id?: string | null;   // 出现/合并/升级锚点（父/目标）
  status?: string | null;      // 静态状态标签（reduced-motion 也保留）
}
```

### 5.2 队列结构
```
TransitionQueue {
  queued: Map<laneKey, QueuedEntry[]>;   // laneKey = kind 语义 + 关联锚
  seq: number;
  hiddenSinceMs: number | null;
  perfProfile: MotionProfile;            // reducedMotion / lowPerformance
}
QueuedEntry {
  id: string; event: ProjectionTransitionEvent; verb: DisplayVerb;
  updatedAtMs: number; coalesced: boolean; settled: boolean;
}
```

### 5.3 背压与合并（AC2 核心）
- **同 lane 合并**：同一 `laneKey` 在 `MOTION_TOTAL_BUDGET_MS` 预算窗内的连续同类事件**合并为一条**，只播放一次（不重复触发，与 LRM-1400 一致）。
- **队列上限**：`queued` 总条目数上限 **64**；超过后最旧未开始条目直接落终态并出队（不播放）。
- **预算截断**：一批次动画总时长超过 `MOTION_TOTAL_BUDGET_MS`，尾部 stagger 以 0 位移截断（只留 opacity/静态标记）。
- **终态保证**：每个进队列的条目最终必然落终态（要么播放到 settle，要么被合并/截断后直接落终态）；**动画永远不能阻止最终视图与无动画重放一致**。

### 5.4 可执行规则 ⑦（AC2 回归）
提供一个纯函数测试：注入**连续 100 条** contract fixture delta（覆盖 10 类 kind 混合、含同 lane 合并、含后台 hidden 窗口），断言：
1. 队列峰值 `<= 64`，settle 后 `queued` 为空；
2. 每条 enter/merge 都有一条对应入队 → settle 记录；
3. 最终 settled 的**终态节点集合**与「不做动画直接应用 100 条 delta」的纯重放集合**完全一致**（这是「最终视图与无动画重放一致」的可执行版本）。

---

## 6. 组件与文件所有权

### 6.1 目标目录（建议所有权，本 slice 定义）
- `packages/views/research/motion/tokens.ts` — motion token 唯一源（§3）。
- `packages/views/research/motion/transition-queue.ts` — 纯函数队列（§5）。
- `packages/views/research/motion/semantic-mapping.ts` — 10 kind → verb 映射（§2 可执行规则 ①）。
- `packages/views/research/motion/directives.ts` — verb → scoped CSS/class + inline style（对齐 LRM-1446 directive 形状）。
- `packages/views/research/motion/use-semantic-transition.ts` — React hook：消费队列 + frame hook，映射到 DOM class / inline style，处理中断/后台恢复/reduced-motion/low-perf。
- `demo/`（或 storybook）— 10 类 kind 的**可运行 demo**（§8）。

### 6.2 消费组件（提示，不改这些文件内部业务）
- `packages/views/research/components/research-canvas.tsx` — 画布根，接 camera-sync。
- `packages/views/research/components/research-logic-strip.tsx`（窄屏 strip 卡）— 复用 `node-enter-motion` 的窄屏 enter 规则。
- 递归 Insight 组合树（UI-02 / FE-06 接力）— 消费 `integration_formed` / `insight_staled` 的 merge/stale verb。

### 6.3 状态矩阵（组件各状态）
| 状态 | 表现 |
| --- | --- |
| default（有数据） | 按 verb 播放，token 一致 |
| loading（首载） | 骨架/skeleton，不播放历史 transition（`reappear` 淡入终态） |
| empty | 空状态，无动画 |
| error / wake_failed | 连接/缺口错误条；无动画重放、等待重连补帧 |
| disabled（只读/降级） | 低性能/reduced-motion 用 §4 降级 |
| permission（无权查看某节点） | 该节点打码/占位，无动效 |

---

## 7. 迁移与降级路径

- 已存在的 `node-enter-motion.ts` / `canvas-reorg-motion.ts` / `semantic-aggregation-motion.ts` / `hero-cta-motion.ts` 的常量**迁移到 `motion/tokens.ts`**；旧模块保留 re-export 兼容（过渡期），token 值保持一致（`REORG_SINGLE_ELEMENT_MAX_MS=320`、`REORG_TOTAL_BUDGET_MS=900`、`REORG_EASING=cubic-bezier(0.22,1,0.36,1)` 等与本规格一致，现有模块已符合，无需回退）。
- 并行轨迹引擎（LRM-1400/1446/1447/1448）落地后，`transition-queue` 只负责**语义 kind → verb 输出**，把位移/轨迹需求**交给**轨迹 animator/controller 消费；本 slice 不重复实现 lane 位移。
- 接口（投影 delta 类型）未落地时用 §5.1 的 contract fixture；生产路径落地后删除 fixture 替换为真实类型（不留伪数据）。

---

## 8. Demo 规格与验收截图要求

### 8.1 可运行 demo（AC1）
- Storybook/演示页提供 10 个 kind 各一个场景（含触发按钮），能**独立播放**并观察：出现（分支/任务）、合并（integration）、冲突（dispute）、升级（deliberation/lead escalation）、失效（insight stale 传播）、修订（report revised）、人员变更、补帧、相机联动。
- 演示数据用 contract fixture（§5.1），不接入生产 API。

### 8.2 视觉区分验收（比对截图）
- 同一画面并排：`branch_spawned`（扩散）vs `integration_formed`（向心合并）vs `dispute_opened`（侧向拉开+警示）vs `lead_escalated`（上浮+强调描边）——截图必须能**一眼区分**这 4 类，尤其是分支 vs 冲突 vs 升级。
- 截图在**动画结束后**（静止终态）也要能区分：冲突保留警示描边、升级保留强调、失效保留灰化（§2.2 可执行规则 ②对照）。

### 8.3 AC 验收截图清单
1. 10 个 kind 各自触发瞬间 + 终态截图。
2. 100 条 delta 队列：动画前、中、后三帧，证明终态与无动画重放一致（可加 devtools 对比层）。
3. Reduced Motion 开启：所有位移归 0，淡入 + 即时布局，静态标记仍在。
4. 低性能模式：30fps 抽帧、glow 关闭，终态一致。
5. 重连补帧：缺口补齐后节点以淡入终态出现，无历史重放。
6. 中断：新 delta 打断旧动画，从当前状态续跑（录屏）。

---

## 9. 验收标准（对照 issue AC）

**AC1 — 10 类均有动效参数、时序、可运行 demo，动作区分分支/合并/冲突/升级：**
- 每类在 §3 token 表有参数 + §2 有时序；demo 可运行；§8.2 截图证明 4 大类视觉可区分。

**AC2 — 连续 100 条 delta 不失控，最终视图与无动画重放一致：**
- §5.4 回归（可执行规则 ⑦）通过：队列峰值 ≤64、终态节点集合与纯重放完全一致；§8.3 截图 2 佐证。

**AC3 — 中断/后台恢复/Reduced Motion/低性能有验收方案：**
- §4 每项都有明确行为 + §8.3 截图 3/4/5/6 验收方案。

**交付门槛：** 实现需 `pnpm typecheck` 与 `pnpm react:doctor` 通过，相关单测通过（含 §5.4 背压回归、§2.2 静态终态对照回归、§4 降级回归）。

---

## 10. 交接与验收

- 本规格由 **UI 设计·聊天体验（本 Agent）** @ 前端空闲成员交接实现，并在 issue LRM-1471 评论记录负责人。
- 前端提交实现截图后，本 Agent **逐项对照 §8 截图清单与 §9 验收标准**给出明确通过或返工结论。
- 范围冲突（与并行轨迹引擎、UI-02/UI-04）立即在群内 @bei-ke-han-mu-15 裁定。
