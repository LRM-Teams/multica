# Research V6 Dispute · Deliberation · Escalation Visualization (LRM-1472 / UI-04)

Date: 2026-08-06
Owner: UI 设计·组件系统 agent (be7afa73-…)
Parent goal: LRM-1444 · Backend contract: `docs/superpowers/plans/2026-08-05-autonomous-research-system.md` @ `2f72056`
Sibling specs:
- `docs/superpowers/specs/2026-07-29-research-session-canvas-ui-design.md` (canvas/chat base)
- UI-01 (30 类节点卡片视觉注册表) · UI-02 (Insight 组合树) · UI-03 (Transition 动画)
- UI-05 (团队/Agent 覆盖层) · UI-06 (Git 多轨迹探索器)

> This is a **UI design handoff spec**. It does not implement nodes or state; it fixes the display
> contract a front-end agent must implement. All facts about dispute state come from the canonical
> V6 model, never from summaries, chat text, or animation.

---

## 0. Scope & boundaries

Design the on-canvas presentation of the **dispute subgraph** and its **right-side detail panel**:

- `dispute` node, its `dispute_position` fan, each position's `supports`/`contradicts` evidence;
- the `deliberation` spine with `deliberation_turn` rows;
- the `decision` node (verdict) and the `escalated_to` trajectory to the Research Director
  (罗纳尔多) via a `lead_adjudication` task;
- **canvas card ↔ detail panel mutual positioning** (bidirectional focus);
- status encoding for **未解决 / 升级中 / 已裁决 / 被新证据重开**.

Hard boundaries (from the issue and the canonical model):

1. **No red-dot or text-log reduction of conflict.** A conflict is a `dispute` node reached through
   typed `contradicts`/`challenged_by` edges, with its own detail panel.
2. **Relationship source is always a typed edge.** Never infer support/contradiction from message
   text, same-position proximity, or animation state.
3. **No canonical mutation from the UI.** Focus/dim/grouping are client display state only
   (Zustand), never written back to the graph, never turned into a fake Insight.
4. **No production fixture.** The contract fixture (§9) lives under dev/tests only.
5. **Layout is client-computed** (existing canvas convention). Node facts, edges, hierarchy are all
   server-provided and immutable by the client.

---

## 1. Node visual registry extensions

Extend `packages/views/research/lib/node-visuals.ts`. Semantics first — no hardcoded hex, no
`palette-*-500`. All tokens come from the existing Multica semantic set already used by the canvas.

| `node_type` | ring | accent bar | label tone | `emphasizeType` | glyph | corner modifier |
| --- | --- | --- | --- | --- | --- | --- |
| `dispute` | `ring-2 ring-warning/60` | `bg-warning` | `warning` | true | `⚖` | hexagon tip (subtle) |
| `dispute_position` | `ring-1 ring-warning/35` | stance-tinted (`bg-success/15`·`bg-destructive/15`·`bg-warning/15`) | per stance | true | `◆` | plain |
| `deliberation` | `ring-1 ring-brand/40` | `bg-brand` (progress-tinted) | `info` | true | `↻` | pill |
| `deliberation_turn` | (inline row, not a full card) | — | per marker | — | `·` | — |
| `decision` | `ring-1 ring-success/50` (resolved) else warning/muted | `bg-success`/`bg-warning`/`bg-muted-foreground` | per verdict | true | `●`/`✓` | notched top |

**Stance tint for positions** (secondary, always paired with a label so it is never color-only):
`supports→success`, `contradicts→destructive`, `conditional→warning`. The stance is read from the
incoming `supports`/`contradicts` edge **role**, never from position text.

**Status uses the existing `normalizeNodeStatusKey`** (i18n `node.status.*`); debate-specific
lifecycle labels live in the new `dispute.*` namespace (§7).

**Generic-node fallback:** any unknown future `node_type` renders via the existing `DEFAULT_VISUAL`
(muted border, default tone) with `emphasizeType` — old clients never crash.

### 1.1 Lane placement (extend `logic-lanes.ts laneForNode`)

Map dispute-domain nodes onto the existing `validate` lane (already home of `conflict`/`refuted`):
`dispute`, `dispute_position`, `decision`, `deliberation`, `deliberation_turn` → `validate`.

This keeps contention visually co-located with the existing conflict band and avoids a new lane in v1.

---

## 2. Edge encoding (extend `visualForEdgeType`)

Arrow direction is always meaningful: **toward the entity it annotates**
(evidence → position → dispute → deliberation → decision → Director).

| `edge_type` | stroke | dasharray / style | width | role | purpose |
| --- | --- | --- | --- | --- | --- |
| `supports` | `var(--success)` | solid | 2 | source | evidence → position / claim |
| `contradicts` | `var(--destructive)` | double-strike `10 3 2 3` | 1.75 | dashed | refutes a position/claim |
| `refines` | `var(--brand)` | solid thin | 1.5 | solid | scopes a position |
| `supersedes` | `var(--warning)` | `5 5` | 1.5 | dashed | replaces a decision/claim |
| `invalidates` | `var(--destructive)` | `2 4` sparse | 1.5 | dashed | voids a settled claim/dispute |
| `discussed_by` | `var(--muted-foreground)` | solid thin, opacity .4 | 1.25 | recessed | attaches a claim to deliberation |
| `challenged_by` | `var(--warning)` | `6 3` + arrowhead | 1.5 | dashed | position → challenge |
| `escalated_to` | `var(--warning)` | **solid thick + stepped arrowhead** | 2.5 | active | dispute/deliberation → Director lead_adjudication |
| `resolved_by` | `var(--success)` | `5 5` + seal dot | 1.5 | dashed | decision → dispute (settlement) |

**Non-color anchors:**

- **Conflict** is a *double-struck dashed* line (two dashes = clash), visibly different from solid
  `supports` even in grayscale.
- **Escalation** is the *only thick solid stepped* line on the canvas, with an arrowhead aimed at the
  Director avatar — impossible to confuse with thin support lines.
- **Resolution** carries a *seal dot* (endpoint marker) at the dispute end.

Delegated to UI-03 for animated draw-in, but **animation must never advance canonical state** — the
edge type and endpoints are always the source of truth.

---

## 3. Dispute lifecycle → visual grouping matrix (core)

Group the debate lifecycle into **4 display buckets**. These are display groupings; the underlying
node statuses are the canonical input.

| Bucket | Dispute status | Deliberation status | Card treatment | Status chip (i18n `dispute.status.*`) |
| --- | --- | --- | --- | --- |
| **A · 未解决** | `open` / `investigating` | `pending`/`discussing` | warning hexagon, active pulse while an actor is busy | `dispute.status.open` / `investigating` |
| **B · 升级中** | `open`/`investigating` | `deadlocked`/`escalated` | attention ring + `escalated_to` step arrow to Director; amber attention flash (motion from UI-03, never truth) | `dispute.status.deadlocked` / `escalated` |
| **C · 已裁决** | `resolved`/`conditionally_resolved`/`irreducible` | `converged`/`resolved`/`unresolved`/`cancelled` | decision node attached; settled (dims, not removed); `conditionally_resolved` shows 条件 + 残余影响 note; `irreducible` muted “不可判定” | `dispute.status.resolved` / `conditionally_resolved` / `irreducible` |
| **D · 被新证据重开** | back to `investigating` after a resolution | — | dispute returns to live state **with** the history `decision` node still attached, marked `superseded` via `supersedes`/`invalidates` edge; “重开” flag chip | `dispute.status.reopened` |

### 3.1 Deliberation status tones
- `pending` → muted · `discussing` → brand/run · `converged` → success · `deadlocked` → warning
- `escalated` → warning-strong with escalation icon · `resolved` → success · `unresolved` → warning
- `cancelled` → muted, strikethrough title

### 3.2 Blocking gate banner
`open`/`investigating` disputes are **delivery-blocking**. When a dispute detail is open in that
state, show a compact banner: “此争议阻断交付” (`dispute.gate_blocking`), per the product rule that
blocking `open | investigating` disputes forbid report delivery. `conditionally_resolved` /
`irreducible` instead surface their residual-condition note (never block).

---

## 4. Subgraph layout

Client-computed, per existing canvas convention (not persisted).

```
                 ┌───────────────┐
                 │  Director     │  ← 罗纳尔多 avatar + lead_adjudication task chip
                 └───────▲───────┘     (only when escalated)
            escalated_to │ 2.5px thick stepped
    ┌──────────────┬─────┴─────┬──────────────┐
    │ position P1  │  dispute  │  position P3 │  fan (≥2, typically 3; ≤5/row, wrap + "+N")
    └──────┬───────┴───────────┴──────┬───────┘
   supports│                   supports│contradicts
    evidence nodes ─── contradicts ─── evidence nodes
    ┌──────────────────────────────────────────┐
    │  deliberation spine (below the fan)      │  ← turns expand downward
    │   T1  T2  T3 …                           │
    └──────────────────────────────────────────┘
    { decision node (after settlement, stays for history) }
```

- Position fan: ≥2 positions, 3 typical; more than 5 wrap to a second row, the rest spill into a
  `+N` overflow chip that expands in the detail panel.
- `deliberation_turn` are **not** full canvas cards by default — they are rows inside the
  deliberation spine, paged/lazy-loaded (bounded Projection Slice). They never flood the canvas.
- The `decision` node is a child of the dispute shown after settlement; after a reopen (bucket D) it
  stays attached and is marked `superseded`.

---

## 5. Detail panel ↔ canvas mutual positioning

Extend `ResearchNodeDetail` (existing overlay-card on desktop, bottom sheet on narrow) with
dispute-domain sections keyed by `node_type`. Widened to `min(100%, 380px)` for debate views.

### Canvas → detail
- Click a card → selection ring + detail panel for that node + **focus mode**: unrelated nodes dim
  to ~45%, this node and its typed relations stay full opacity (display-only).
- Esc or close → clear selection and focus dim.

### Detail → canvas (bidirectional)
- In a **dispute** detail, the position list and evidence list each render a `focusNode(id)`
  affordance (chevron button). Selecting it pans the canvas to that node, selects it, dims unrelated
  nodes, and switches the detail to that node.
- In a **deliberation** detail, clicking a turn's evidence refs or a position focuses the same way.
- In a **decision** detail, “查看历史” lists prior decisions; clicking any of them focuses that
  `decision` node (history preserved on canvas).

### Panel section spec

| Node | Detail sections |
| --- | --- |
| `dispute` | 冲突类型 chip (`fact/definition/scope/time/population/method/measurement/interpretation/source_identity`) · 严重度 · 影响范围 · 立场列表 (click→focus) · 相关证据 with `supports`/`contradicts` badges · 当前状态 + 交付义务 (gate banner) · 指派 Director (when escalated) |
| `dispute_position` | 立场说明 · 适用条件 · 提出者 avatars · 证据充分度 · 关联 Claim |
| `deliberation` | 参与 Agent avatars · 结构化轮次 timeline (position/evidence/challenge/concession/scope/progress marker) · 进展水位 · 轮次/预算计数 · deadlock 原因 · 升级原因 |
| `deliberation_turn` | turn body + progress-marker chip (`position_changed/evidence_added/scope_refined/no_change`) |
| `decision` | verdict type · 条件 + 残余影响 · superseded flag + prior-decision history (newest first) |

### Persistence
`focusMode` node id + `panelTab` live in a **Zustand client store** under `packages/core`
(research scope). Never written to the canonical graph.

---

## 6. Multi-encoding table (beyond color)

This design ships **all three** anchors so the system is grayscale-safe and screen-reader-safe:

1. **Label / glyph**: every dispute-domain node carries a stable leading glyph + type chip, always
   visible (`⚖`, `◆`, `↻`, `●`/`✓`, `↺` for reopened).
2. **Line style**: conflict double-strike, escalation thick stepped arrow, resolution seal-dot,
   support solid — distinct in grayscale.
3. **Shape (corner modifier, optional polish)**: dispute hexagon tip, decision notched top via
   `clip-path` utility keyed by `node_type`. Non-blocking; label+linestyle already satisfy the
   criterion even if shape is deferred.
4. **Status text**: debate lifecycle always labeled (`dispute.status.*`), never color-only.

---

## 7. i18n additions

Extend `packages/views/locales/{en,zh-Hans,ja,ko}/research.json`:

- `node.dispute`, `node.dispute_position`, `node.deliberation`, `node.deliberation_turn`,
  `node.decision`
- `dispute.status.open|investigating|deadlocked|escalated|resolved|conditionally_resolved|
  irreducible|reopened|cancelled`
- `dispute.conflict_type.*` for the 9 conflict types
- `dispute.turn_marker.position_changed|evidence_added|scope_refined|no_change`
- `dispute.gate_blocking`, `dispute.residual_note`, `dispute.reopened_note`,
  `dispute.view_history`, `dispute.positions`, `dispute.evidence`, `dispute.turns`,
  `dispute.participants`, `dispute.escalated_to`, `dispute.director_adjudicating`
- `dispute.empty.positions` (“尚无立场”), `dispute.empty.evidence` (“尚无证据”)
- `dispute.overflow_more` (“+N”)

zh-Hans and en are required; ja/ko may be stubs per existing pattern.

---

## 8. Empty / long-content / mobile

### Empty states (real, not fake)
- A session with no dispute renders ordinary canvas — **no** decorative “冲突处理” watermark.
- Dispute detail with no positions yet → `dispute.empty.positions`; no evidence →
  `dispute.empty.evidence`. Never inject placeholder positions or mock evidence.

### Long / overflow content
- Canvas card title: 2-line clamp (existing). Full text lives in the detail panel.
- Positions beyond 5 wrap to a second row; the remainder collapse into a `+N` overflow chip
  (`dispute.overflow_more`) that expands inside the detail panel — the canvas never loads the whole
  fan at once.
- Deliberation turns: lazy-load by page (bounded `Projection Slice`, `limit` + `cursor`). Show the
  first N turns on the spine, then a “加载更多” affordance; the detail panel paginates the rest.
- Long evidence/claim text in detail: `line-clamp-3` with full text expandable; never truncate the
  deliberation turn body.
- Screen reader: each dispute-domain card exposes `aria-label` with glyph name + status + actor
  (matching existing graph-node pattern), so glyph/line encoding is never the only channel.

### Mobile (narrow / bottom sheet)
- Reuse the existing bottom-sheet pattern (`ResearchNodeDetail` placement `sheet`).
- For debate nodes the sheet gains internal tabs: 概览 / 立场 / 讨论 / 裁决 (`dispute.tabs.*`).
  Default tab = 概览.
- Turn timeline: vertically listable with sticky turn-number gutter; no horizontal scroll required.
- `focusNode(id)` from detail works on mobile: it pans the canvas behind the sheet and updates the
  selected node (sheet stays open).
- Director strip collapses to a compact chip row above the dispute, not a thick bandwidth line.

---

## 9. Contract fixture (dev/test only, never production)

A strict-contract fixture lives under `packages/views/research/dispute/__fixtures__` (dev/app only)
and feeds canvas + detail rendering, tests, and screenshot review. Shape must match the V6 registered
`node_kind` and typed edges; **no production path references it**.

Fixture must contain:

1. **3 positions** — P1 `supports` claim X, P2 `contradicts` claim X, P3 conditional/`refines`
   (scope split). Each is a `dispute_position` node.
2. **Evidence fan** — several claim/evidence nodes feeding the positions via `supports` /
   `contradicts` / `refines` typed edges.
3. **≥3 deliberation turns** — a `deliberation` node with `deliberation_turn` rows carrying
   progress markers `position_changed | evidence_added | scope_refined | no_change`.
4. **Escalation** — the dispute/deliberation reaches `deadlocked`, auto-creates a
   `lead_adjudication` task bound to the Research Director (罗纳尔多) and writes an `escalated_to`
   edge; the Director strip and thick stepped arrow render.
5. **Decision + supersede/reopen** — a `decision` node (verdict `resolved` with conditions) and a
   later reopen scenario: new contradicting evidence returns the dispute to `investigating`, the old
   `decision` is marked `superseded` (`supersedes`/`invalidates` edge), and the “重开” flag chip
   shows — history stays visible.

Fixture schema sketch (canonical shape):

```ts
// dispute_position + deliberation + decision nodes follow ResearchGraphNode
{ node_type: "dispute",  status: "open|investigating|resolved|…",
  payload: { conflict_type, severity, impact_scope, blocking } },
{ node_type: "dispute_position", status: "proposed|investigating|settled",
  payload: { stance: "supports|contradicts|conditional", claim_ids, evidence_ids, author } },
{ node_type: "deliberation", status: "discussing|deadlocked|escalated|converged|…",
  payload: { participant_ids, progress_level, turn_count, budget, deadlock_reason,
             escalation_reason } },
{ node_type: "deliberation_turn",
  payload: { position, evidence_ids, challenge, concession, scope_refined,
             marker: "position_changed|evidence_added|scope_refined|no_change" } },
{ node_type: "decision", status: "current|superseded",
  payload: { verdict: "resolved|conditionally_resolved|irreducible|obsolete", conditions,
             residual_impact, decided_by } },
// typed edges:
{ edge_type: "contradicts" | "supports" | "refines" | "challenged_by" | "escalated_to" |
             "resolved_by" | "supersedes" }
```

---

## 10. Acceptance mapping & verification

Map each LRM-1472 acceptance criterion to concrete, screenshot-reproducible checks.

| Acceptance criterion | Verifiable outcome | Screenshot required |
| --- | --- | --- |
| 1. 3 position、多 turn、lead escalation 的 fixture 完整浏览 | Fixture loads on canvas: 3-position fan, ≥3 turns readable on the deliberation spine, escalation arrow to Director visible, decision + reopen preserved | 默认/已裁决/重开 三态 |
| 2. 点击立场/证据聚焦对应图节点，裁决后保留历史并显示 superseded/resolved | Detail `focusNode(id)` pans & selects target; settled dispute keeps decision node with `superseded` flag | focus 前后, 裁决历史 |
| 3. 颜色以外还有形状/标签/线型；空态、超长、移动端齐全 | Glyphs + double-strike conflict + thick escalation arrow present; empty/overflow/mobile specs implemented | 灰阶对比, 窄屏 sheet, 空态 |

Per-project delivery bar (from the issue): `pnpm typecheck`, `pnpm react:doctor`, and supporting
canvas/detail tests must pass before the handoff PR is mergeable. Every implementation must attach
screenshot(s) covering the rows above.

---

## 11. Package / platform / token rules

- UI in `packages/views/research/**`; new dispute module under `packages/views/research/dispute`.
- Stores (focus mode, panel tab) in `packages/core` (Zustand), research-scoped key. Web + Desktop
  both render the same components.
- Design tokens: semantic first via the existing `node-visuals.ts` / CSS-variable accent map; no
  hardcoded hex, no `palette-*-500`.
- i18n: extend the `research` locale namespace (`en` + `zh-Hans` required).
- Reuse `@xyflow/react` as in the base canvas; no new shared dep without pnpm-catalog entry.

### Boundaries with sibling issues
- **UI-01** owns the node-card *registry mechanism* + generic unknown-node fallback; UI-04 supplies
  the dispute/decision/deliberation-specific visual *entries* and debate detail sections. No file
  collision: UI-04 adds entries, does not own the registry dispatch.
- **UI-03** owns *animated* transition draw-in; UI-04 fixes the *static* semantic encodings
  (glyph/linestyle/status). Animation never drives truth.
- **UI-05** owns the team/Agent activity overlay; UI-04 references the Director avatar via that
  overlay resolver rather than duplicating an avatar system.

---

## 12. Implementation slices (separate PRs, non-Draft → dev)

1. **Registry + edges** — `node-visuals.ts` dispute entries; edge table additions; `logic-lanes.ts`
   lane mapping; i18n keys.
2. **Dispute card + fan layout** — position fan, `+N` overflow chip, deliberation spine rows,
   decision/`escalated_to` geometry (client layout).
3. **Detail panel sections** — dispute/position/deliberation/turn/decision sections +
   `focusNode(id)` + bidirectional focus (Zustand).
4. **Fixture + tests + screenshots** — fixture under `dispute/__fixtures__`, canvas/detail tests,
   `pnpm typecheck`, `pnpm react:doctor`, screenshots for the three states.
5. **Polish** — corner modifiers, reduced-motion, mobile sheet tabs, ja/ko stubs.

---

## Open decisions (defaults locked unless overturned)

| Topic | Default |
| --- | --- |
| Corner shape modifiers (hexagon / notch) | Optional polish; label+linestyle already satisfy the non-color criterion |
| Director strip position | Top-right chip row, only when escalated |
| Turn default visible count | First N on spine, rest lazy-loaded (bounded Slice) |
| Panel width for debate views | `min(100%, 380px)` |
| Focus dim opacity | ~45% for unrelated nodes |