# Research Session Canvas UI Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the Research session three-column shell with a canvas-first infinite graph (primary observation) plus a hideable card chat drawer and group-chat-parity agent config.

**Architecture:** `@xyflow/react` canvas renders `research_graph_*` nodes/edges with client-side dagre layout; WS updaters drive incremental patches + motion. Chat is a right drawer toggled via Zustand + FAB. Agent avatars open `ResolvedAgentSidePanel` via `AgentPanelProvider`.

**Tech Stack:** React 19, `@xyflow/react`, `@dagrejs/dagre` (or xyflow dagre helper), TanStack Query, Zustand, existing Research APIs/WS, `@multica/views` channel agent panel primitives.

**Spec:** `docs/superpowers/specs/2026-07-29-research-session-canvas-ui-design.md`

## Global Constraints

- Small PRs: one task group ≈ one PR to `dev` (do not ship all slices in one PR).
- `packages/views` — no `next/*` / `react-router-dom`.
- New deps: add to `pnpm-workspace.yaml` `catalog:` then `"pkg": "catalog:"` in `@multica/views`.
- Presence language: Online/Offline only on dots (LRM-248); working = pulse + activity projection.
- Web + Desktop both use shared `ResearchSessionPage`.
- i18n: update `research` locales (en + zh-Hans required).
- After FE changes: `pnpm react:doctor`.

## File map

| Path | Role |
| --- | --- |
| `packages/views/research/components/research-session-page.tsx` | Page shell: chrome + canvas + drawer slot |
| `packages/views/research/components/research-canvas.tsx` | React Flow host, layout, motion hooks |
| `packages/views/research/components/research-graph-node.tsx` | Custom node renderer |
| `packages/views/research/components/research-graph-edge.tsx` | Edge style mapper (optional) |
| `packages/views/research/components/research-chat-drawer.tsx` | Card timeline + composer |
| `packages/views/research/components/research-chat-fab.tsx` | FAB when drawer closed |
| `packages/views/research/components/research-session-chrome.tsx` | Title/stage/confirm/handoff/zoom |
| `packages/views/research/components/research-node-detail.tsx` | Selection detail popover |
| `packages/views/research/lib/layout-graph.ts` | dagre layout → RF nodes/edges |
| `packages/views/research/lib/node-visuals.ts` | Type → accent classes |
| `packages/core/research/ui-store.ts` | Drawer open persisted preference |
| `packages/views/research/components/exploration-map.tsx` | Remove after canvas lands (or thin re-export) |
| Locales `*/research.json` | New strings |

---

### Task 1: Deps + canvas shell PR

**PR title:** `feat(research): canvas shell with React Flow`

**Files:**
- Modify: `pnpm-workspace.yaml` (catalog `@xyflow/react`, `@dagrejs/dagre`)
- Modify: `packages/views/package.json`
- Create: `packages/views/research/lib/layout-graph.ts`
- Create: `packages/views/research/components/research-canvas.tsx`
- Create: `packages/views/research/components/research-graph-node.tsx`
- Create: `packages/views/research/components/research-session-chrome.tsx`
- Modify: `packages/views/research/components/research-session-page.tsx`
- Test: `packages/views/research/lib/layout-graph.test.ts`

**Interfaces:**
- Produces: `layoutResearchGraph(nodes, edges) => { nodes: Node[]; edges: Edge[] }`
- Produces: `<ResearchCanvas nodes edges selectedId onSelect />`

- [ ] **Step 1:** Add catalog deps + `pnpm install`
- [ ] **Step 2:** Write failing test for `layoutResearchGraph` (goal→probe edge positions differ)
- [ ] **Step 3:** Implement layout helper + minimal custom node + `ResearchCanvas`
- [ ] **Step 4:** Rewrite `ResearchSessionPage` to full-bleed canvas + chrome (keep chat as temporary slim column OK, or hide until Task 4)
- [ ] **Step 5:** Unit test + typecheck views research paths; `pnpm react:doctor`
- [ ] **Step 6:** Commit + PR to `dev` + merge when green

---

### Task 2: Visual language + motion PR

**PR title:** `feat(research): graph accents, dashed edges, enter motion`

**Files:**
- Create: `packages/views/research/lib/node-visuals.ts`
- Modify: `research-graph-node.tsx`, `research-canvas.tsx`
- Modify: locales for node labels if needed
- Test: `node-visuals.test.ts`

- [ ] Map node_type / edge_type → classes + stroke dash
- [ ] Stagger enter + active pulse (`prefers-reduced-motion` off)
- [ ] Selection detail popover (`research-node-detail.tsx`)
- [ ] Tests + PR

---

### Task 3: Agent activity + side panel PR

**PR title:** `feat(research): canvas agent activity and config panel`

**Files:**
- Modify: `research-session-page.tsx` — `AgentPanelProvider` + side panel slot
- Modify: `research-graph-node.tsx` — `ActorAvatar` + open panel
- Wire: `useAgentActivityProjection` for pulse
- Test: session page test with mocked panel open

- [ ] Click avatar opens `ResolvedAgentSidePanel`
- [ ] Busy agents pulse on owned active nodes
- [ ] PR

---

### Task 4: Hideable chat drawer + FAB PR

**PR title:** `feat(research): hideable card chat drawer`

**Files:**
- Create: `packages/core/research/ui-store.ts`
- Create: `research-chat-drawer.tsx`, `research-chat-fab.tsx`
- Modify: session page layout
- Locales: drawer / fab / composer copy
- Test: ui-store + drawer render

- [ ] Persist open/closed in Zustand
- [ ] Card message list + composer → lead
- [ ] FAB when closed; Esc closes drawer
- [ ] PR

---

### Task 5: Sources/report on canvas + chrome exits PR

**PR title:** `feat(research): delivery drawer and source badges on graph`

**Files:**
- Modify: chrome for confirm/handoff popover
- Wire sources into node detail / badges; report markdown drawer
- Delete obsolete empty middle panes / old `exploration-map` if unused

- [ ] PR

---

### Task 6: Polish gate

- [ ] `pnpm --filter @multica/views exec vitest run research`
- [ ] `pnpm react:doctor`
- [ ] Spot-check Web + Desktop routes still mount `ResearchSessionPage`

---

## Verification (each PR)

```bash
pnpm --filter @multica/views exec vitest run research
pnpm react:doctor
```
