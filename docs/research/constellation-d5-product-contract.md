# Research Constellation D5 Product Contract

Status: active production target  
Reference prototype: `/Users/xxx/Desktop/multica-research-constellation-v2.html`  
Reference image: the 1920×1024 D5 constellation screenshot supplied on 2026-08-13

This document is the executable frontend product contract for the Research
session workbench. Earlier canvas and V6 documents remain useful technical
references, but they do not define a second competing session UI.

## 1. Product statement

The Research session is a constellation-first command surface. The canvas is
the primary explanation of what the fleet is researching, how results relate,
which paths failed, and how evidence is being combined. Chat and node details
are contextual inspectors on the right, not the primary status surface.

The supplied static prototype is the target composition and interaction model,
not merely a mood board. Production code must use canonical backend facts,
shared Web/Desktop components, semantic tokens, accessible interaction, and
bounded rendering rather than copying the prototype's hard-coded fixtures.

## 2. Canonical composition

1. **Top command bar** — product identity, current goal/version/round, canvas
   filters and lenses, then session actions.
2. **Constellation canvas** — dotted research field, summary badge, organic
   relation graph, cluster boundaries, map key, and zoom/fit controls.
3. **Context rail** — switchable fleet chat and selected-node detail. It may be
   hidden to restore a full-width canvas.
4. **Agent inspector** — opens from an Agent work node and shows the current
   task/attempt facts plus the shared Agent configuration entry.
5. **Node report** — opens from result nodes and explains the local objective,
   findings, evidence, review history, contributors, and lineage. It is not a
   replacement for the final session delivery.

Desktop targets a 360 px context rail. Tablet uses 320 px. Mobile uses a bottom
sheet. The canvas camera must account for the visible rail's safe region.

## 3. Five-level node language

| Tier | Product meaning | Required visible facts |
| --- | --- | --- |
| XXL | master synthesis / final umbrella | tier, title, summary when present, metrics |
| XL | fused or stable result | tier, title, summary when present, metrics |
| L | important result | tier, title, summary when present, metrics |
| M | intermediate result or scoped direction | tier, title, summary when present |
| S | active Agent work node | short task title, Agent badge, execution state |

Tier comes from the typed projection. The kind classifier is only a documented
fallback; unknown kinds degrade to M. Title, summary, metrics, Agent identity,
state, cluster, and lineage must come from projection facts. The frontend must
never manufacture a summary, confidence, source count, conclusion count, or
relationship to make the canvas resemble the prototype.

States use shape/glyph/text as well as colour: default, selected, running,
stable, pending review, conflict, failed/abandoned, and restarting. Historical
dead ends, superseded results, and absorbed inputs remain inspectable.

## 4. Relation and grouping language

The primary visual relation families are:

| Family | Examples | Visual treatment |
| --- | --- | --- |
| Decomposition | `leads_to`, `refines`, `derived_from` | cyan solid route |
| Support | `supports`, `resolved_by` | green solid route |
| Challenge | `challenged_by`, `contradicts`, `invalidates` | amber/red dashed route |
| New direction | `restart_of`, branch/frontier spawn | violet dotted route |
| Fusion | `merged_from`, integration derivation | emphasized convergence route |

The real edge type is retained even when it maps to a visual family. Unknown
relations remain visible with a neutral treatment and diagnostic detail; they
must not be silently reinterpreted as supporting evidence.

Cluster boundaries are display groupings backed by projection cluster facts.
They are not canonical Insights and are never written back as research facts.

## 5. Conversation-driven graph changes

User messages go to the Research Director. A response may produce committed
canonical changes such as:

- abandon a direction and release its Agents;
- revise the Research goal and label affected old nodes with their goal version;
- restart a weak result with new Attempts or Agents;
- create a new frontier;
- integrate results into a higher-tier node.

The UI renders changes only after backend facts are committed. Each change must
be explainable in chat as a compact change receipt and on the canvas through a
bounded semantic transition. Chat text and animation never mutate canonical
research state. Refresh must reconstruct the same terminal graph.

## 6. Data and state ownership

- React Query owns snapshots, typed graph pages, presence, node details,
  reports, messages, and mutations.
- Zustand owns lens, filters, selection, camera, folded display groups, rail
  mode/open state, and transient inspector state.
- WebSocket events invalidate queries or feed the ordered projection client.
  They do not copy server facts into Zustand.
- The current D5 typed-graph path remains the production shell. V6 snapshot,
  delta, node-kind, Insight, Dispute, and trajectory work is integrated into
  this shell incrementally; it does not create a second Research session page.
- V6 404/501 means capability unavailable and may fall back to V5/D5. A 200
  response that fails schema parsing is an interface error, not a fallback.

## 7. Interaction contract

- Click/focus a node: select it, move it into the rail-safe camera centre, and
  open detail.
- Open an S node: show Agent inspector; Agent configuration uses the shared
  `ResolvedAgentSidePanel`.
- Open an L/XL/XXL result: show its local node report.
- Pan, wheel zoom, buttons, fit-to-content, keyboard adjacency navigation, and
  Escape focus restoration must work.
- Closing the context rail expands the canvas. Reopening restores its previous
  mode.
- Selected first-order relations are emphasized while unrelated content is
  reduced but remains legible.

## 8. Quality gates

- Web and Desktop render the same shared page.
- Unknown node/edge/status kinds degrade without a white screen.
- Desktop graph-node DOM hard limit is 220; semantic-node hard limit is 180;
  Landmark cards hard limit is 48. At 25% zoom, at most 12 Landmark cards show.
- 360×800, 768×900, 1440×900, 200% browser zoom, dark/light themes, keyboard,
  reduced motion, offline, gap/resync, malformed responses, and long Chinese /
  English text are covered by tests or visual artifacts.
- Motion animates transform/opacity, is interruptible, and settles immediately
  for reduced motion, background resume, and resync.
- Each frontend change runs relevant Vitest, typecheck, and `pnpm react:doctor`.

## 9. Implementation status at `dev@1617b8662`

Status in this table means code already present on `dev`. Open pull requests are
tracked separately in §11 and are not counted as integrated until merged.

| Area | Status | Next production work |
| --- | --- | --- |
| Shared Web/Desktop routes | Integrated | Keep parity gate |
| Top command bar and goal history | Integrated | Visual polish against target |
| Five-tier star graph, camera, clusters, relations | Integrated | Improve fact density and edge degradation |
| Context rail, chat, node detail | Integrated | Add canonical change receipts |
| Agent inspector and shared Agent panel | Integrated | Complete Attempt/lease fact coverage |
| Local node report | Integrated | Align structured evidence/lineage sections |
| Typed graph pagination/filter/lens/DOM budget | Integrated | Add viewport slice gateway when backend exists |
| V6 schema, API, adapter, ordered live client | Built, not wired to session page | Capability-gated production integration |
| 30-kind cards, Insight, Dispute, trajectory | Built as isolated modules | Register in D5 detail/lens surfaces |
| V6 server snapshot/delta/resume | Not present on current dev | Backend dependency; do not fake in frontend |

## 10. Delivery sequence

1. Close visible D5 parity gaps using current typed projection facts: node
   summaries, compact metrics, relation fallback, selected-neighbour emphasis,
   rail composition, and target breakpoints.
2. Add canonical conversation-change receipts and terminal-state transitions
   for abandon, regoal, restart, frontier, and integration.
3. Integrate Insight, Dispute, execution, and trajectory modules into existing
   D5 lenses/details through stable props and callbacks.
4. Wire the V6 adapter/live client behind capability detection once the server
   routes are real, retaining the existing D5 shell and explicit fallback.
5. Run the full visual/performance/accessibility matrix and remove obsolete
   documents or mark them superseded.

## 11. Delivery ledger

This ledger is the merge checklist for the production target. A linked open PR
is implementation evidence, not completion evidence. After a PR merges, update
the corresponding §9 row and replace the PR evidence with the merge SHA.

| Contract surface | Current evidence | Delivery state |
| --- | --- | --- |
| Target D5 shell and shared Web/Desktop composition | `dev@ec2082afc`, PR #2907 | Integrated |
| Canonical conversation change receipts | PR #2908 | Awaiting merge |
| Dispute and trajectory/Insight detail registration | PRs #2909–#2910 | Awaiting merge |
| Capability-gated V6 adapter in the existing D5 shell | PR #2911 | Awaiting merge; server capability absent |
| Canvas keyboard focus and Escape restoration | PR #2912 | Awaiting merge |
| Agent Attempt/lease facts and inspector focus | PRs #2913, #2919 | Awaiting merge |
| Unknown relation neutral degradation | PR #2914 | Awaiting merge |
| Mobile context sheet | PR #2915 | Awaiting merge |
| Semantic light/dark colour tokens | PR #2916 | Awaiting merge |
| Reduced-motion settlement | PR #2917 | Awaiting merge |
| V6 socket truth and ordered resync recovery | PRs #2918, #2920, #2922 | Awaiting merge; server capability absent |
| Canvas load/stale projection recovery | PRs #2924, #2932 | Awaiting merge |
| Lens keyboard access and selected neighbourhood focus | PRs #2925, #2933 | Awaiting merge |
| Node report lineage, contributors, and attempt history | PRs #2926–#2927 | Awaiting merge |
| Typed status/confidence fidelity | PR #2928 | Awaiting merge |
| Canvas visual and accessibility localisation | PRs #2930–#2931 | Awaiting merge |
| Session-isolated camera restoration | PR #2934 | Awaiting merge |
| 25% overview and DOM/card budgets | PR #2923 plus existing budget modules | Awaiting merge |
| Real V6 snapshot/delta/resume API | No matching route under `server/` at `dev@1617b8662` | Backend blocked; explicit V5/D5 fallback required |
| Runtime visual/performance/accessibility matrix | No current deployed-session artifacts for this revision | Unverified; required before completion |

### Completion evidence rule

The frontend is complete only when all applicable rows above are integrated and
the §8 matrix has evidence against one deployed revision. Static fixtures,
isolated modules, open PRs, or the absence of an obvious TODO do not prove the
session flow complete. V6 server-dependent rows may remain capability-gated,
but malformed successful responses must surface an interface error and must
never silently fall back.
