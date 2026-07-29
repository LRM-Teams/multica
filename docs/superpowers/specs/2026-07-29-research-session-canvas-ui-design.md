# Research Session Canvas UI Design (2026-07-29)

## Summary

Replace the current three-column Research session shell with a **canvas-first command surface**: an infinite exploration graph is the primary observation channel (who is working, progress, forks, dead ends, findings, pitfalls). A **card-style fleet chat** sits in a **hideable right drawer** for watching agents talk among themselves; the user rarely interrupts and only messages **罗纳尔多**. Closed chat restores full-bleed canvas; a **bottom-right FAB** reopens it.

This spec covers **session page UI/UX + FE wiring to existing Research APIs/WS**. Backend fleet/runtime/tools remain as shipped in Research Fleet v1 unless a gap is called out under *Backend deltas*.

Related: `docs/superpowers/specs/2026-07-29-research-fleet-design.md`.

---

## Goals

1. **Graph is the truth surface.** User understands agents, progress, wrong turns, preliminary conclusions, errors, and dead ends **from the canvas**, not from a text roster.
2. **Instant “team at work” feeling** after creating a session: nodes appear with motion, agent avatars attach to work, active nodes pulse; idle agents stay quiet.
3. **Cool but product-grade.** Strong color language for node kinds / severity; dashed vs solid edges; no generic purple-glow AI aesthetic; respect existing Multica semantic tokens where possible, with a small Research accent palette.
4. **Chat is optional observation.** Card timeline of fleet dialogue; hideable; FAB to restore.
5. **Agent config parity with group chat.** Click avatar → same `ResolvedAgentSidePanel` (Runtime / Files / Activity / …).

## Non-goals

- Replacing stage-eval / handoff / hire APIs in this UI slice (keep existing confirm/handoff entry points, relocated into canvas chrome).
- Building a full Figma-like editor (no user free-draw of arbitrary shapes).
- Showing Working/Resting as presence-dot language (keep LRM-248: avatar dot = Online/Offline; “working” = activity projection + canvas pulse).
- Forcing the user to participate in fleet chatter.

---

## Layout

```
┌─────────────────────────────────────────────────────────────┐
│ Top chrome: title · stage · status · confirm/handoff · zoom │
├───────────────────────────────────────────────┬─────────────┤
│                                               │ Chat drawer │
│         Infinite canvas (primary)             │ (optional)  │
│         graph + agents + edges                │ card feed   │
│                                               │ + composer  │
│                                               │ → 罗纳尔多   │
├───────────────────────────────────────────────┴─────────────┤
│ FAB (only when drawer closed) ◎ bottom-right                │
└─────────────────────────────────────────────────────────────┘
```

| Region | Behavior |
| --- | --- |
| **Canvas** | Default ~100% width when chat closed; ~68–75% when open. Pan (drag), zoom (wheel / buttons), fit-to-content. |
| **Chat drawer** | Width ~320–400px. Persist open/closed per workspace in client store (Zustand). |
| **FAB** | Shown only when drawer closed. Opens drawer. Icon: messages / fleet. |
| **Top chrome** | Compact; never competes with canvas. Stage chip, status badge, confirm + handoff actions. |

No persistent left roster column.

---

## Canvas (exploration graph)

### Interaction model

- **Viewport:** infinite pan/zoom; min/max zoom clamps; “Fit” resets to content bounds.
- **Selection:** click node → selection ring + detail popover/panel (summary, sources snippet, actor).
- **Agent open:** click avatar on node (or detail “Open agent”) → `AgentPanelProvider` / `useOpenAgentPanel` → `ResolvedAgentSidePanel` (same as channels).
- **Non-destructive history:** dead_end / refuted / pivot nodes stay visible; never auto-delete.

### Node types (map to `research_graph_node.node_type`)

| Type | Visual role | Accent (semantic) |
| --- | --- | --- |
| `goal` | Session root | primary |
| `subquestion` | Decomposition | muted primary |
| `probe` | In-flight exploration | info / cyan-leaning token |
| `finding` | Positive result | success |
| `conflict` | Disagreement | warning |
| `dead_end` | Abandoned path | destructive (muted) |
| `refuted` | Disproven claim | destructive |
| `pivot` | Direction change | orange / warning-strong |
| `stage_gate` | S1–S4 gate | primary outline |
| `roster_change` | Hire/archive | muted |
| `agent_activity` | Live work chip | agent brand tint |

Each node card shows: type label, short title, optional one-line summary, **actor avatar(s)**, status (`active` / `done` / `abandoned`).

### Edges (`research_graph_edge.edge_type`)

| Type | Stroke |
| --- | --- |
| `leads_to` | Solid, default |
| `supports` | Solid, success-tint |
| `contradicts` | Dashed, warning/destructive |
| `supersedes` | Dashed, muted |
| `abandons` | Dashed, destructive muted |

### Layout algorithm (v1)

- **ELK / dagre-style layered layout** (or `@xyflow/react` + dagre) seeded by `created_at` + edge topology.
- Auto-relayout on batch inserts; preserve viewport anchor when possible.
- Manual node drag optional in v1.1; **not required** for first ship if auto-layout is stable.

**Library choice (recommended):** `@xyflow/react` (React Flow) for infinite canvas, edges, minimap optional. Add dependency via pnpm catalog if not present. Fallback only if package boundary forbids — then canvas with CSS transform + SVG edges (higher cost).

### Motion

- Session kickoff: stagger node enter (opacity + slight translate), edges draw after endpoints exist.
- Active work: soft pulse on `active` nodes owned by busy agents (respect `prefers-reduced-motion`).
- WS `graph_updated`: upsert node/edge with short highlight flash, then settle.
- Presence / activity: intensify pulse on nodes tied to `actor_agent_id` when compact activity is non-null.

### “Who is working” on the canvas

- Derive busy agents from existing `useAgentActivityProjection` (same as channels).
- Busy agent → avatar ring pulse + optional floating activity caption near their latest `agent_activity` / `probe` node.
- Idle online agent → avatar may still appear on last completed node without pulse.
- Offline → presence dot only (no fake “resting” label).

### Detail richness (must feel detailed)

Node detail (popover or bottom sheet on narrow width) includes:

- Full summary / excerpt
- Linked sources (title, class, weight) when payload references them
- Stage / timestamps
- Actor + “Open configuration”
- For `finding` / `conflict` / `dead_end` / `pivot`: explicit “why it matters” line from summary

Sources and report are **not** separate empty middle panes. High-weight sources appear as nodes or as badges on findings; latest report is a **Delivery** affordance in top chrome or a pinned `stage_gate` / report revision card on canvas (click opens markdown drawer).

---

## Chat drawer (card timeline)

### Purpose

Watch fleet members talk / report to each other. Aesthetic card chrome. Not the primary status surface.

### Content

- Chronological **cards**: sender avatar + display name + role chip + body + time.
- System / tool-ish research messages can render as compact “event cards” (e.g. “寻源手 upserted source”) when `sender_type` / payload warrants — still in the same stream.
- User messages visually distinct; replies expected from 罗纳尔多 only (product copy reinforces this).

### Composer

- Single composer at drawer bottom.
- Posts via `postResearchMessage` **without** `target_agent_id` (server defaults to lead) unless user explicitly @’s (v1.1; v1 can omit @ picker).
- Placeholder: 纠偏 / 补充约束 only.

### Visibility

- Toggle close → full-bleed canvas + FAB.
- Persist `researchChatDrawerOpen` in Zustand (`packages/core`), workspace-scoped key optional.

---

## Agent configuration

Reuse channel stack — do not invent a Research-only inspector:

- Wrap session page with `AgentPanelProvider`.
- Open via avatar click → `ResolvedAgentSidePanel`.
- Depth (instructions / skills / env) via existing Agent Detail navigation from panel actions if already linked there.

Fleet members with `pending_prompt_review` show a badge on avatar/node; panel still opens read/config as permitted by ownership rules.

---

## Top chrome actions

Retain product exits without middle-column clutter:

- Status badge + current stage
- **Confirm completion** (existing API)
- **Handoff** checkboxes + action (existing API) in a compact popover/menu
- Zoom controls + Fit
- Optional minimap toggle

---

## Data & realtime

| Source | Use |
| --- | --- |
| `GET` session snapshot | Initial nodes/edges/messages/sources/report/fleet |
| WS `research_session:graph_updated` | Incremental graph patch + enter animation |
| WS `research_session:message` | Append chat card |
| WS `research_session:presence` | Optional caption near agent |
| WS `research_session:sources_updated` / `report_updated` | Attach to nodes or delivery drawer |
| WS `research_session:stage_eval` / `status_changed` | Chrome chips + stage_gate nodes |
| `useAgentActivityProjection` | Working pulse |

Layout positions: **client-computed** in v1 (not persisted). If jitter appears, v1.1 may persist `x,y` in node `payload`.

---

## Backend deltas (only if needed)

Prefer zero API change. Gaps to verify during implementation:

1. Agent↔agent messages already appear in `research_message` stream when fleet posts — if not, lead/members must post via existing message API (CLI `report-to-lead` / `message`).
2. Richer canvas may need optional `payload` fields (`source_ids`, `highlight`) — document convention; no migration if JSON payload suffices.
3. ListAgents still hides `research_fleet`; GetAgent + side panel remain available by id.

---

## Package / platform rules

- UI in `packages/views/research/**` (no `next/*` / `react-router`).
- Stores in `packages/core` (drawer open state).
- Web + Desktop both render `ResearchSessionPage`.
- New shared dep (e.g. `@xyflow/react`): add to pnpm catalog + declare in `@multica/views` `package.json`.
- i18n: extend `research` locale namespace (en + zh-Hans minimum; ja/ko stubs ok if following existing pattern).
- Design tokens: semantic first; Research accent map via CSS variables under session root.

---

## Accessibility & motion

- Keyboard: zoom in/out, close drawer (Esc), focus composer when drawer opens.
- `prefers-reduced-motion`: disable stagger/pulse; keep instant upserts.
- Contrast: destructive/warning accents on cards meet WCAG against canvas background.

---

## Acceptance criteria

1. Create session → within seconds, canvas shows goal + emerging work nodes with agent avatars; motion communicates “many people started.”
2. User can pan/zoom; dead ends and pivots remain visible with distinct styling.
3. Clicking an agent avatar opens the same configuration side panel as group chat (runtime + files at minimum).
4. Chat drawer shows card timeline; closing it maximizes canvas; FAB reopens.
5. User message goes to 罗纳尔多; no requirement for user to chat with every member.
6. Confirm + handoff still reachable from chrome.
7. Web and Desktop parity.
8. No regression of LRM-248 presence language.

---

## Implementation slices (separate PRs)

Per project small-PR rule:

1. **Canvas shell** — React Flow (or chosen lib) + top chrome + fit/zoom; render existing nodes/edges; kill old 3-column layout.
2. **Graph visuals + motion** — type accents, dashed edges, stagger/pulse, selection detail.
3. **Agent activity on canvas** — activity projection + avatar open panel.
4. **Hideable chat drawer + FAB** — card messages + composer + persist toggle.
5. **Sources/report on graph** — delivery drawer / source badges; remove empty panes.
6. **Polish + tests + react:doctor** — layout tests, i18n, reduced-motion.

---

## Open decisions (defaults locked unless overturned)

| Topic | Default |
| --- | --- |
| Canvas library | `@xyflow/react` |
| Manual node drag | Deferred |
| @mention in composer | Deferred |
| Minimap | Optional, off by default |
| Persist node x,y | Deferred (client layout only) |
