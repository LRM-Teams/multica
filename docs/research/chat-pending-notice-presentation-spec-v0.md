# 聊天流三态呈现规格：Pending vs content-free Notice vs 已投递（LRM-1532）

> 状态：**规格（仅文档 / 设计交付）**，非功能实现。实现由其认可的 FE 角色按本规格执行，
> 并 @bei-ke-han-mu-15 记录 owner；本单**不产代码**。
>
> 约束：只消费 LRM-1453（in_review）/ LRM-1454（in_review）**已定稿的语义**；不对后端字段做任何
> 假设或新契约，不触碰后端 / transport / 鉴权，不与阿泰 T1–T6 冲突。**LRM-1453/1454 定稿后才可进入
> 实现**；本规格先行冻结产品口径与 DOM/CSS/aria 靶向。

---

## 0. 问题与目标

用户当前无法在聊天流中分辨三件事（句式「发了却看不到 / 被静默」）：

| 语义 | 权威含义（LRM-1454 已定稿） | 用户误读风险 |
| --- | --- | --- |
| **Pending** | 消息已被 runtime **接受、进入队列、尚未投递**（runtime busy，后续会投） | 以为「发丢了 / 被吞」 |
| **content-free Notice** | 本轮只有**纯确认被抑制、无实质内容**；ack 是**传输级**（不证明 read / handoff / boundary 推进） | 以为「已读 / 已处理 / 回执=投递成功」 |
| **已投递** | 实质内容已进入聊天流（canonical chat 投影，LRM-1453） | — |

目标是让**视觉、交互、读屏三层**都能区分这三态，桌面 float 与窄屏 sheet 一致；同时**不要**把传输级回执加工成「已读/已处理」的暗示。

**产品口径（本规格拍板，需 @bei-ke-han-mu-15 复核）**：

- **UI 侧必须呈现 Pending 与 Notice 显式状态**，理由：`message check`（T4）drain 会延迟投递，且 Notice 是被抑制的确认，二者在用户侧都表现为「看不见新东西」；若不显式标注，用户会把「没看到」误判为「失败/丢失/被忽略」。呈现出它们不等于暗示「已读/已处理」——见 §3 读屏防线。
- **Pending 是「进行中」而非「错误」**：不触发 error / destructive 语义，用信息性 + 弱强调的 token，可搭配「稍后投递」文案。
- **Notice 是「回执被抑制」而非「新消息」**：弱化、可 fold，**不入 boundary**（不推进任何已读/处理记录），用系统级样式（如既有 `process`/`bg-muted` 家族）区别于实质消息。
- **是否展示 Pending/Notice 的开关**：**默认展示**，不设隐藏开关（避免「被静默」问题复发）。如果后续产品要求降噪，以全局单一开关收口，不做每个会话的散开关。

---

## 1. 消费边界（字段语义 — 只读，不新增契约）

下述为 UI 派生状态，**由已定稿语义推导**（不假设特定的后端 JSON 字段名，除非该字段已在 core 类型中存在）。

### 1.1 已存在的、可直接消费的字段（core 已有类型，本次不新增）

- `ResearchMessage.created_at`、`body`、`sender_id`、`sender_type`、`card_kind`、`meta` —— 判定「**已投递**」的实体内核：`body` 非空（非 content-free）即视为实质内容已入流。
- `has_mention?: boolean`（已在 `packages/core/conversations/types.ts`、`packages/core/dm/types.ts`）—— 用于给 changed-targets / Pending 行做「有 @ 提到我」的强提示（复用既有字段名，**不新增**）。
- `aria-live` / `aria-busy` / `useIsMobile` / `data-placement` / `motion-safe:|motion-reduce:` —— 现成 UI 基建。

### 1.2 LRM-1454 定稿的 Notice 语义（字段名待阿泰落地后绑定，本规格只冻结**含义**）

| 语义（LRM-1454 验收原文） | UI 用途 | 绑定方式 |
| --- | --- | --- |
| total Pending count | 全局 Pending 徽标/行「N 条待投递」；读屏汇总 | **占位符**，LRM-1454 落地后在 FE 数据层绑定；未落地前该字段为 `undefined` → 该区块整体不渲染 |
| changed targets | 「目标已变更」提示；目标列表 | 同上，占位 |
| per-target Pending count | 每个 target 自己的待投递数（桌面 float 需要） | 同上，占位 |
| 可选首/末短 ID | 定位具体待投消息 | 可选，未落地不渲染 |
| 最新 sender / mention flag / server attention hint | 「谁在待投 / 是否 @ 我 / 是否该注意」 | 可选；`mention flag` 若落地可映射到既有 `has_mention` 语义，但**不臆造字段** |

**硬边界（LRM-1454 验收 2/4/5）**：
- Notice **绝不含** body / Parts / attachment 元数据 / 字节 → UI **不得**从 Notice 中渲染任何正文、附件、图片、链接预览；Notice 行只渲染「这是一条被抑制的确认 + 可选计数/目标」。
- ack 只证明 transport 接受 → UI **不得**把 ack / Pending 翻译成「已读」「已处理」「对方看到」。任何读屏、任何 tooltip、任何 icon 文案都不得出现此类含义。
- 同会话未变 Pending fingerprint 抑制重复 Notice → UI 对**未变的 Pending 集合不重复播报**（见 §3）。

---

## 2. 三态可视化（desktop 与窄屏一致）

### 2.1 核心模型：三个可区分但**共享骨架**的呈现

三态共用**同一张卡片骨架**（视觉差异只在 token 与辅助元素），从结构上保证 float/sheet 一致、读屏一致，
也避免「不同状态长出不同外观组件」的漂移（工程原则 §0）。

建议落到既有骨架 `ResearchChatCard`（`packages/views/research/components/research-chat-card.tsx`）之上，
新增一个轻量线性「状态行」/「状态胎记」，而**不是平行造新卡组件**。

| 态 | 视觉要点 | token 建议 | 交互 |
| --- | --- | --- | --- |
| **Pending** | 弱强调：非错误、非灰死。卡片边框用 `border-brand/30`（复用 `loading/running` 的 `brand` 语义），配一个小的 `animate-pulse` 圆点 + 文案「待投递」。**不**用 destructive/warning。 | `border-brand/30`、`bg-brand/[0.04]`、圆点 `bg-brand`、文案 `text-muted-foreground` | 无阻塞交互；hover 可给 tooltip「已在队列，稍后投递」；不自动聚焦 |
| **content-free Notice** | 弱化、系统级、可折叠。归入 `process`/`bg-muted` 家族（`border-dashed bg-muted/40 + text-muted-foreground`，与既有 `wasStopped`/process 卡同级）。正文区**只渲染说明与计数**，绝不渲染任何实质 body。默认折叠为一行。 | `border-dashed`、`bg-muted/40`、`text-muted-foreground`、`text-[10px]` 行 | 可展开显示计数/目标；无操作；不推进 boundary |
| **已投递** | 现状 `ResearchChatCard` 完整渲染（`bg-card` 实心可视 + StreamingMarkdown / process / clarification） | 沿用现状 | 现状 |

**「已投递」是默认基线**：只有当一条 message 处于 Pending（有 `pending` 标记/派生态）或是一条 content-free Notice 时，
才进入以上两种降级呈现；其余全部按已投递渲染，**不做任何额外状态装饰**（避免把正常消息也「贴标签」）。

### 2.2 Pending 状态行（聚合，非逐条）

Pending 建议**不止在单卡**，还要在会话/通道层面给一个聚合行，因为 T4 drain 可能推迟多目标：

- **全局行**（feed 顶部 or 底部 sticky）：`aria-live="polite"` 的 `output`（复用 `research-chat-mode-body.tsx` 的 standing-live-region 模式）+ 可见徽标「N 条待投递」。
- **计数绑定**：`total Pending count`（§1.2）→ N；`per-target Pending count` → 若 `data-placement="float"`（桌面）展示按 target 分组，窄屏 sheet 收进可展开明细。**未落地时该行整体不渲染**。
- **changed targets**：当 Notice 报告目标变更时，在全局行追加「目标已变更」chip（弱 info），不打断读屏主流程。

### 2.3 desktop float 与窄屏 sheet 一致性（复用 `data-placement`）

- 复用 `ResearchChatDrawer`（`research-chat-drawer.tsx`）现状：desktop 用 `<aside data-placement="float">`，窄屏用 Radix `Sheet data-placement="sheet"`。
- Pending 行 / Notice 卡 / 已投递卡**同一套组件**在两个 placement 下复用，只有**布局差异**（float 侧栏宽 380px 内可平铺 per-target；sheet 走折叠明细），无组件分叉。
- `useIsMobile()` 只用于「是否折叠 per-target 明细」，不用于「是否渲染三态」。

---

## 3. 读屏与语义（aria 方案）

### 3.1 播报边界：什么播、什么时候播、怎么避免轰炸

核心原则：**Pending 变更播一帧、Notice 只在「有新增内容」时播、已投递正文不额外播**。所有播报走**常驻 live region**，
不挂在会卸载的子树上（沿用 `research-chat-mode-body.tsx` LRM-1225 的 standing-region 教训）。

| 事件 | 是否播报 | 时机 | live region / 文案 |
| --- | --- | --- | --- |
| 新 Pending 出现 | **播一帧**（聚合，不逐条） | 集合从 0→N 或计数变化 | `output aria-live="polite" aria-atomic="true"`：「N 条消息待投递」 |
| Pending 数字未变（fingerprint 不变，LRM-1454 抑制） | **不播** | — | 依赖后端不重复发 Notice；FE 对「未变化的 N」只更新可见数字、**不触发播报** |
| Pending → 已投递 | 播一帧（这条内容已投递） | drain 完成 | 复用已投递实体的自然读屏；可选 `polite`：「消息已投递」 |
| content-free Notice 到达且有新增计数 | 播一帧摘要 | 3s 合并后 | `output aria-live="polite"`：「确认仅被抑制，无新内容；N 条待投递」 |
| Notice 无实质变化（被抑制的重复） | **不播** | — | 仅视觉更新，不打扰 |
| 已投递实质正文 | 走正文自然读屏（StreamingMarkdown 现状），**不额外 live 播报** | 现状 | 现状 live-stream `aria-busy` 机制 |
| @ 我的待投变化 | 优先播报（`has_mention` / attention hint） | 有 mention 时 | 同一条 pending 播报里前置「有 @ 提醒」 |

**anti-bombing 防线**：
- **只用一个常驻 live region**（drawer 级），不逐卡创建 `aria-live`；避免多 region 竞速。
- Pending/Notice 播报**是状态句**而非逐行复述；数字变化才促发，图标/脉动动画 `aria-hidden`。
- 卡片本体的 Pending/Notice 修饰元素全部 `aria-hidden`，**只让 live region 说一次**，防止「可见标签+live 播报」双重朗读（沿用 LRM-1225：可见 chip `aria-hidden` + sr-only `<output>` 措辞）。

### 3.2 focus 还原

- Pending/Notice 卡**不抢焦点、不自动聚焦**（避免打断正在读/输入的用户）。
- 若用户通过键盘进入全局 Pending 行并按展开（per-target 明细），焦点按 Radix `Sheet`/`Disclosure` 约定管理：折叠头持有 `aria-expanded`，展开后焦点回到触发元素（默认还原）。
- 现成 `useOverlayPanelA11y` 的 focus 还原路径在 float 分支沿用，不因三态新增而改变。

### 3.3 semantic 标注

- **已投递** = 现状 `<article>`（`ResearchChatCard`），语义不变。
- **content-free Notice** = `<aside role="note">`（或列表内 `<li>`），`aria-label` 明确「确认仅被抑制，无新内容」；**不得** `role="alert"`（它不是错误、不打断用户）。
- **Pending 行** = 状态信息：`<output aria-live="polite">`（常驻 region 的唯一播报载体）承载正在生成中的数字文本；卡片本体修饰 `aria-hidden`。
- **内联状态胎记**（单卡上的 pending/notice 标记）统一 `aria-hidden`，避免与 live region 重复朗读。
- **reduced-motion 降级**：沿用 `motion-safe:`/`motion-reduce:` 前缀（repo 已普遍使用，如 `research-chat-card.tsx` 的 `motion-safe:animate-in`）。
  - `animate-pulse`（Pending 圆点）与「胎记淡入」全部以 `motion-safe:` 前缀包裹；在 prefers-reduced-motion 下**静态呈现**（圆点保持常亮色、无脉动），语义与读屏不变。
  - 聚合行的数字变化不做位移/滚动动画的强制依赖；颜色/文字即是信息，动画仅增强。

---

## 4. 边界状态

| 状态 | 处理 |
| --- | --- |
| **loading** | 复用现成 `ResearchChatModeBody` 的 `mode=loading`（骨架 + `aria-busy`）。**不**把 loading 误标为 Pending——loading 是「拉取中」，Pending 是「已接受未投递」，语义不同、样式不同（loading=骨架/`brand/10`；Pending=`border-brand/30`+圆点）。 |
| **空（no messages）** | 复用 `mode=empty`。display 文案不得暗示「无待投递/无 Notice」=「已全部处理」；空态只表达「当前无已投递内容」，不表达回执状态。 |
| **错误（error）** | 复用 `mode=error` 的 `role="alert"` + destructive 卡。**只有 FE 层取数失败**（query reject）才触发；**Pending/Notice 数据缺失（`undefined`）不是错误**——按 §1.2 整体不渲染该区块，不显示 error。Never 用 error 态表达「有 Pending 未投」——那是进行中不是错误。 |
| **禁用（disabled / 无权限 / 会话不可写入）** | 沿用现状禁用态。Pending「待投递数」在不可写会话仍可只读展示（用户只需知道有内容在排队），但**不得**提供任何「催投/手动 drain」交互、不暗示可操作。 |

**错误 vs Pending 的判别红线**：Pending 永不渲染为 error；`role="alert"` 仅用于真实取数失败；任何把「有内容在排队」当成「出错/失败」的渲染都是本规格要杜绝的（§0 产品口径）。

---

## 5. 一致性与响应式小结 / 组件 token 清单

### 5.1 一致性判定
- 三态在 float（`data-placement=float`）与 sheet（`data-placement=sheet`）下**同一组件渲染**，仅 per-target 明细折叠与否差异。
- Pending 全局行、Notice 卡、已投递卡三者的顺序、层级、命名在两种 placement 下一致。

### 5.2 组件 token 清单（供 FE 直接落 token，建议走既有 tailwind 语义词）

| token | 值 | 用途 |
| --- | --- | --- |
| `border-brand/30` + `bg-brand/[0.04]` | Pending 卡边框/底 | 区分「进行中」，非错误 |
| `bg-brand` `animate-pulse`（`motion-safe:`） | Pending 圆点 | 视觉脉动信号 |
| `border-dashed bg-muted/40 text-muted-foreground` | Notice 卡 | 系统级衰减、可折叠 |
| `text-[10px] text-muted-foreground` | 计数/明细行 | 弱化信息 |
| `text-brand` 徽标 | 全局 Pending 计数 chip | 可见但不打扰 |

**文案字典（建议 i18n，禁止出现「已读/已处理/回执成功」类）：**
- Pending：「待投递」「已在队列，稍后投递」「N 条消息待投递」
- Notice：「确认仅被抑制，无新内容」「无新内容」+ 可选「N 条待投递」/「目标已变更」
- 已投递：走现状，不加状态词

---

## 6. 验收清单（实现阶段据此验收）

- [ ] 三态（pending/notice/已投递）各有可区分的可视化 + 读屏方案，`data-placement=float` 与 `data-placement=sheet` 一致。
- [ ] Pending/Notice 播报走**唯一常驻 live region**，未变的 Pending 集合不重复播报；卡片修饰 `aria-hidden`，无双重朗读。
- [ ] Notice 只渲染「说明 + 可选计数/目标」，**绝不**渲染 body/Parts/附件/字节（LRM-1454 硬边界）。
- [ ] 不含「已读/已处理/回执成功」语义的文字或 icon；Pending 永不渲染为 error；`role="alert"` 仅用于真实取数失败。
- [ ] `motion-reduce` 下脉动/动画降级为静态，语义与读屏不变。
- [ ] loading/空/错误/禁用 各态复用现成 `ResearchChatModeBody`/`role=alert` 骨架，不新增平行分支。
- [ ] 未落地的 Notice 字段（LRM-1454 未合入前）一律 `undefined→不渲染`，不臆造字段、不新增后端契约。

---

## 7. 依赖与豁免

- 依赖：LRM-1453（in_review）、LRM-1454（in_review）后端语义定稿（阿泰）。**定稿前不得进入实现**；本单只交付规格。
- 不触碰：后端 / transport / 鉴权；与阿泰 T1–T6 无冲突（本规格只读其已定稿语义）。
- 消费边界已写死：见 §1.2 表格——UI 只消费 total pending / changed targets / per-target pending / 可选 sender·mention·attention，字段名待阿泰合入后绑定占位。
- **建议实现落点**：`packages/views/research/components/`（`research-chat-card.tsx` + 新的状态行组件 + `research-chat-drawer.tsx` 内嵌），token 走既有 tailwind 语义词；组件级 a11y 测试参照仓库现有 `*-a11y.test.tsx` 模式（见 `research-chat-mode-body`、`research-chat-drawer` 对应测试）。

---
*LRM-1532 · UI设计·聊天体验（6c18f71c）规格交付 · 2026-08-10*
