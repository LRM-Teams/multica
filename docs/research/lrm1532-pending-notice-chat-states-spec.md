# UI Spec: Pending vs content-free Notice 聊天流三态呈现

- Written: 2026-08-10
- Status: ready for FE implementation review (spec only, no feature code)
- Owner (spec): UI设计·聊天体验 (6c18f71c)
- Issue: LRM-1532 (parent LRM-1452 / #2282 Raft Direct Agent Message lifecycle)
- Sources: LRM-1453 (T1 空闲投递链), LRM-1454 (T2 忙碌投递 + content-free Notice) — backend semantics draft (阿泰, in_review)
- Target branch: `ui/lrm1532-chat-delivery-state`, base `dev`

> 本单只交付前端可逐条落地的**规格**，不做功能实现。实现由认可的 FE 角色按本规格执行。只消费 LRM-1453/1454 已定稿字段与语义，不对后端字段做任何假设或新契约。

---

## 1. 问题定义（产品裁定采纳）

用户在多 agent 聊天（频道 DM / 调研会话罗纳尔多聊天）中无法分辨三种递送状态，句式表现为「发了却看不到 / 被静默」：

1. **Pending** — agent 消息已被 runtime **接受**（transport ack）但尚未投递到聊天流（runtime 忙，稍后会投，成功后推进 boundary）。
2. **content-free Notice** — 本轮只有**纯确认被抑制**、无实质内容（ack 为传输级、**不**推进 boundary、成功 Notice **不**消费 Pending、不产生 `Message received` Activity）。用户不该误以为「已读 / 已处理 / 有新内容」。
3. **已投递 (delivered)** — 实质内容已进入聊天流，成为一条可见消息（LRM-1453: canonical Message 创建后即从 canonical 源可见，前端投影不依赖 runtime 状态）。

UI 侧需要让这三种状态在聊天流中**显式且可区分**（视觉 + 读屏），并与既有「本地发送状态（Sending… → 已投递 → Failed）」模式区分开（那是用户自己的发送，LRM-271/273；本规格是**远端 agent 消息**的递送状态）。

---

## 2. 消费字段与语义契约（只读，不新增契约）

本规格 UI 只消费 LRM-1454 Notice 已定稿的以下 Capacity 字段；**绝不在 UI 侧发明/拼装 body、Parts、附件元数据**：

| 字段（LRM-1454 Notice） | UI 消费方式 |
|---|---|
| `total_pending`（总 Pending 计数） | 可展示聚合数，如「N 条消息等待送达」 |
| `changed_targets` + 每 target `pending_count` | 可展示逐频道/逐会话的 Pending 计数（当 changed_targets.length ≤ 3 时） |
| `latest_sender` | 可展示「最新来自 {sender}」；不解析 body |
| `mention_flag` | 当 true 时提升注意级（attention hint），例如强调该 Pending 链含 @ 我 |
| `attention_hint`（server-derived） | 仅用于强调/排序，不作为正文展示 |
| 首/末短 ID（optional） | 可折叠展示为 link 依据；不展示原始长 ID |
| body / Parts / 附件元数据 | **一律不展示**（Notice 明确不含） |

**边界（铁律）**：UI 不新增任何后端字段、不做本地推测（如不猜「这条 notice 对应哪条具体 body」）。Pending / Notice 呈现完全由上述字段驱动；字段缺失时整体不渲染该状态（fail-closed，不占位）。

---

## 3. 三态可视化

### 3.1 Pending（等待送达）

语义：消息已被接受、会在稍后送达，**不要**让用户以为已读/失败。

- **位置**：聊天流顶部 sticky 状态条（desktop float 与窄屏 sheet 各自复用自己的容器），不逐条插入占位气泡。
- **视觉**：弱化、居中、低饱和。`text-muted-foreground` 文案 + `animate-pulse`（≤2 处，`motion-reduce:animate-none`）。
- **文案**（i18n，进入 `$.chat.*`）：
  - 单 target：「{n} 条消息等待送达」
  - 多 target（≤3，按 changed_targets）：「{targetA} {n} 条 · {targetB} {m} 条…」
  - 含 @ 我 时前缀强调：「含 @你的消息」
- **示例 DOM**：

```tsx
<div
  role="status"
  data-testid="chat-pending-banner"
  className="mx-auto my-1 flex max-w-[92%] items-center justify-center gap-2 rounded-full border border-border/60 bg-muted/40 px-3 py-1 text-[11px] text-muted-foreground"
>
  <span aria-hidden className="size-1.5 rounded-full bg-brand motion-reduce:animate-none animate-pulse" />
  <span>{pendingCopy}</span>
</div>
```

### 3.2 content-free Notice（纯确认被抑制）

语义：本轮只有确认被抑制，**无新内容**。用户最常见困惑是「我看不到那条确认」。方案是**折叠显示 + 明确标注无实质内容**，避免误以为已处理/已读/有新消息。

- **位置**：聊天流顶部（可叠加于 Pending 条下），或列表最末 **fold 行**；**不**插入完整消息气泡（无 body 可渲染）。
- **视觉**：比 Pending 更弱（它不会推进 boundary）：`border-dashed border-border/40 bg-transparent`、`text-muted-foreground/70`、`italic` 不可用（避免难读）→ 用常规低对比。
- **文案**：「{n} 条确认已折叠（无新内容）」；`mention_flag` 时不折叠、降级为 Pending 强调（因为它可能含关键 @）。
- **默认折叠**：默认只显示一行摘要，可展开查看 target/sender 明细（`<details>`/chevron），避免刷屏（呼应 LRM-1523 回声治理——UI 不重新制造刷屏）。
- **举例 DOM**：

```tsx
<details
  data-testid="notice-fold"
  className="mx-auto my-0.5 max-w-[92%] rounded-md border border-dashed border-border/40 px-3 py-1 text-[11px] text-muted-foreground/80"
>
  <summary className="cursor-pointer list-none text-muted-foreground/80">
    {t(($) => $.chat.notice_folded, { n: notice.total_pending })}
  </summary>
  <ul className="mt-1 flex flex-col gap-0.5">
    {notice.changed_targets.map((t) => (
      <li key={t.target} data-testid="notice-target-row">{t.target} · {t.pending_count}</li>
    ))}
  </ul>
</details>
```

### 3.3 已投递（delivered）

语义：实质内容已在聊天流，**无需新增标志**（LRM-1453 已交付 canonical 消息本体渲染）。本规格只需确认：Pending/Notice 状态条在对应 delivered 消息出现后**正确消失**（去重、不残留），且不重复播报 `Message received`。

- **边界**：已投递即「正常气泡渲染路线」，本规格不改变气泡本体；只要求转场清理（见 §6 状态机）。

---

## 4. 读屏与语义（aria / focus / reduced-motion）

### 4.1 aria-live 播报边界（复用既有「持久 live region」模式）

代码库已有先例：`research-chat-mode-chip` 用一个**常驻** `<output aria-live="polite" aria-atomic>` 承载 loading→running 翻转（避免挂在会被卸载的子树上，LRM-1225）；`channel-message-bubble` 用一条持久 `message-send-status` live region 承载 Sending…→Failed（LRM-271/273）。本规格沿用同一模式，**新增一条常驻 live region** 承载三态播报，避免在状态翻转时节点被卸载导致永不播报。

- **常驻位置**：聊天抽屉/频道页根容器内，一条 `sr-only <output>`（相似 `data-testid="chat-delivery-state-live"`）。
- **播报内容与时机**：
  - **进入 Pending**（0→pending）：`aria-live="polite"`，播报「有 N 条消息等待送达」。
  - **Pending 增加**（count 变化 → 仍 pending）：**不重复播**（避免轰炸）；仅当 `mention_flag` 或 pending_count 跨档（如 1→2）才播。
  - **Pending→已投递**：播「消息已送达」；若 folded 打开则聚焦（见下）。
  - **进入/保持 Notice**（无 body）：播一次「N 条确认已折叠，无新内容」；**不重复**；`aria-live="off"` 于折叠明细展开（用户在主动阅读，不打扰）。
- **`aria-busy`**：pending 状态下设为 `aria-busy=true`；投递完成置 false。reduced motion 下仍可播报（语义不降级）。

### 4.2 focus 还原

- 折叠 Notice 展开/收起：`<summary>` 自身保持 focusable；展开后焦点留在 summary，不额外抢 focus。
- 点击 Pending/Notice 状态条跳转/刷新动作（如有）后，焦点还原到原触发控件（`use-overlay-panel-a11y`/既有 restore 模式），**禁止**落到 `body`。
- 所有可点控件在 pending 中**不设原生 `disabled`**（沿用已冻结模式 LRM-1213/1347/1348：用 `aria-disabled` + handler guard，避免焦点掉 body / 浮层卸载）。

### 4.3 reduced-motion 降级

- Pending 的 pulse/spinner：`motion-reduce:animate-none`，保留静态弱化视觉 + 文本，**不丢语义**（状态条仍可见、仍可读屏）。
- Notice 折叠默认展开态在 reduced-motion 下直接展开静态明细，不依赖动效引导。

---

## 5. 边界状态（loading / empty / error / disabled）

| 状态 | 呈现 |
|---|---|
| **loading**（三态数据未就绪） | 复用抽屉既有 `research-chat-mode-body` loading skeleton；**不**显示 Pending/Notice 条（等数据定稿再播，避免误报）。 |
| **empty**（无 any notice/pending） | 状态条整体不渲染；聊天流保持现有 empty body（`chat.empty_title/body`）。 |
| **error**（Notice 拉取/读取失败） | 不显示 Pending/Notice（fail-closed）；复用既有 `chat.error_*` + retry，保持既有错误体，不新增状态。 |
| **disabled**（无权限 / 归档 / 会话不可写） | 状态条不渲染可交互控件；只读展示（若数据允许）或整体隐藏；**不占位、不伪造**。 |

> 边界原则：三态状态条是**提示性**，非**功能性必达**。任何字段缺失/失败 → 隐藏而非占位/猜测（呼应工程原则「不用假数据、无意义 fallback、制造表面成功」）。

---

## 6. 状态机与转场

```
(无) --notice/total_pending>0--> pending[+notice折叠]
pending --对应消息delivered--> pending(去重) --pending_count=0--> (无)
notice --mention_flag--> 强调态(pending高亮) --delivered--> (无)
```

- **去重/不残留**：已投递的 message id 计入 consumed 集合，对应 Pending/Notice 计数随之递减；计数归零整条状态条卸载。
- **刷新一致性**：状态条由 Notice 驱动、随 React Query canonical 消息刷新合并，不维护与消息流冲突的本地缓存。
- **不重复 `Message received`**：状态条仅反映 Pending/Notice，不产生/不复制 `Message received` Activity 或文案（该 Activity 归 T1 的 `Checking messages…` 职责，见 LRM-1454 AC8）。

---

## 7. 组件与 token 建议（落地到 packages）

### 7.1 建议分层（Web/Desktop 共享，不复制两套）

- **共享通用组件** `packages/views/common/`（或 `channels/` 与 `research/` 各自薄封装引用同一布局 + token）：
  - `DeliveryStateBanner`（receives normalized `{kind: 'pending' | 'notice', total, targets, mention}`）— 纯展示，只吃字段。
  - `ChatDeliveryStateLiveRegion`（常驻 sr-only `<output>`，对外暴露 `announce(msg)`）。
- **channels 表面**：`packages/views/channels/components/channel-message-list.tsx` 顶部挂 `DeliveryStateBanner` + 根容器挂 `ChatDeliveryStateLiveRegion`；与现有 `channel-message-bubble` 本地发送 live region 并存。
- **research 表面**：`packages/views/research/components/research-chat-drawer.tsx`（desktop `<aside>` 与窄屏 `<Sheet>` 各自容器顶层）+ 复用 `research-chat-mode-chip` 的常驻 output 模式扩展三态播报。

### 7.2 token（对齐现有语义 token，不新增裸色值）

| 用途 | token |
|---|---|
| Pending 状态文案/点 | `text-muted-foreground` + `bg-brand`（点） |
| Notice 折叠行 | `border-dashed border-border/40`、`text-muted-foreground/70`、`bg-transparent`（无底） |
| 强调（mention/attention） | `text-brand`、`bg-brand/10`、`border-brand/35`（对齐 modeChip.brand 系） |
| 错误 | 复用 `destructive` 系（仅 error 态） |
| a11y 边框 | 复用 `focus-visible:ring-ring`、`ring-offset-background` |
| 密度 | 状态条 `text-[11px]`、`p-1/2`（不占满、不加气泡壳） |

---

## 8. 桌面与窄屏一致性

- **desktop float**（`research-chat-drawer` `<aside>`，宽 ≤380px）：状态条在抽屉内容顶部。
- **窄屏 sheet**（`<Sheet side=bottom>` 全屏）：状态条置于 sheet 内容顶部、紧贴 header 下方；尺寸/文案一致，仅容器宽度随 sheet。
- **channels**：desktop 与移动端消息列表均复用同一 `DeliveryStateBanner`（窄屏仍居中弱化条，不溢出）。
- **阈值**：`useIsMobile()` 仅切换容器（float vs sheet），**不改**三态视觉/文案/读屏语义。

---

## 9. 验收清单（FE 实现据此逐条验证）

- [ ] 三态（pending / notice / 已投递）各有可区分的可视化 + 读屏方案，desktop 与窄屏 sheet 一致。
- [ ] Notice 默认折叠、可展开 target/sender 明细；`mention_flag` 时强调不折叠。
- [ ] 常驻 `sr-only` live region 承载三态播报；进入 pending 播 1 次、计数跨档/mention 才再播；Notice 只播 1 次。
- [ ] 不设原生 `disabled`（`aria-disabled` + guard）；焦点不落 body；折叠 summary 自身可聚焦。
- [ ] `motion-reduce` 下 pulse 停、静态 + 文本保语义；状态条不丢读屏。
- [ ] loading/empty/error/disabled：三态条 fail-closed 隐藏/不伪造；loading 用既有 skeleton、error 用既有错误体。
- [ ] 投递后状态条去重消除、计数归零即卸载；不产生重复 `Message received`。
- [ ] 仅消费 §2 契约字段，不新增后端假字段、不拼装 body/Parts/附件元数据。
- [ ] Web 与 Desktop 复用同一共享组件 + token，不复制两套。

---

## 10. 实现依赖与边界

- 依赖：LRM-1453/LRM-1454 后端语义**定稿**后方可进实现（本单只交付规格）。
- 边界：不触碰后端 / transport / 鉴权；不与 T8「前端投影」（canonical 事件→React Query 失效 + stage 文案）冲突——本规格只管聊天流三态可视化 + 读屏区分。
- 不与既有本地发送状态（`channel-message-bubble` Sending…) / Presence Working chrome（`channel-agents-live-cue`）冲突：它们描述「用户自己发的」与「agent 正在跑的任务」，本规格描述 agent 消息的**递送状态**。
