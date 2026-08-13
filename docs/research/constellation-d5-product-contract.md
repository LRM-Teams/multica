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
- Node-report review history may only render from projected review decisions.
  The current `ResearchRunSnapshot` exposes tasks, Attempts, evidence, Claims,
  and gate findings, but not the complete quality/citation decision history;
  the frontend must not reconstruct passing reviews from task status.

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

## 9. Implementation status at `dev@602ed7336`

Status in this table means code already present on `dev`. Open pull requests are
tracked separately in §11 and are not counted as integrated until merged.

| Area | Status | Next production work |
| --- | --- | --- |
| Shared Web/Desktop routes | Integrated | Keep parity gate |
| Top command bar and goal history | Integrated | Validate deployed visual density against target |
| Five-tier star graph, camera, clusters, relations | Integrated | Improve fact density and edge degradation |
| Context rail, chat, node detail | Integrated | Validate deployed responsive and deep-link flows |
| Agent inspector and shared Agent panel | Integrated | Validate the deployed responsive matrix |
| Local node report | Integrated | Project quality/citation review decisions; canonical contributor, Attempt, evidence, and lineage sections are integrated |
| Typed graph pagination/filter/lens/DOM budget | Integrated | Add viewport slice gateway when backend exists |
| V6 schema, API, adapter | Integrated behind capability detection | Validate against a real server route when available |
| V6 ordered live client | Implemented but not wired into the D5 shell | Resolve the WS event/envelope decision below before production wiring |
| 30-kind cards, Insight, Dispute, trajectory | Integrated in D5 detail/lens surfaces | Complete deployed visual evidence |
| V6 server snapshot/delta/resume | Not present on current dev | Backend dependency; explicit V5/D5 fallback remains required |

## 10. Delivery sequence

1. Resolve the V6 realtime transport contract: the existing
   `research_session:graph_updated` event carries V5 payloads, while the V6
   client requires a run-routable ordered Delta. Decide either a dedicated V6
   event or a versioned envelope containing both `run_id` and `delta`.
2. Validate the full visual/performance/accessibility matrix against one
   deployed Web/Desktop revision and attach durable artifacts.
3. Exercise the capability-gated V6 adapter/live client against real snapshot,
   delta, and resume routes when the backend exposes them; retain explicit
   V5/D5 fallback until then.
4. Close visual parity gaps found by that deployed matrix without reconstructing
   facts absent from the canonical projection.
## 11. Delivery ledger

This ledger is the merge checklist for the production target. A linked open PR
is implementation evidence, not completion evidence. After a PR merges, update
the corresponding §9 row and replace the PR evidence with the merge SHA.

| Contract surface | Current evidence | Delivery state |
| --- | --- | --- |
| Target D5 shell and shared Web/Desktop composition | `dev@602ed7336`, PR #2907 | Integrated |
| Canonical conversation change receipts | `2edba39a`, PR #2908 | Integrated |
| Dispute and trajectory/Insight detail registration | `cee8dc2a`, `4cac759b`, PRs #2909–#2910 | Integrated |
| Capability-gated V6 adapter in the existing D5 shell | `a0963726`, PR #2911 | Integrated; server capability absent |
| Canvas keyboard focus and Escape restoration | `e9ef764c`, PR #2912 | Integrated |
| Agent Attempt/lease facts and inspector focus | `a95806d1`, `e7e4097c`, `0fe34d207`, PRs #2913, #2919, #2978 | Integrated |
| Unknown relation neutral degradation | `938401fc`, PR #2914 | Integrated |
| Mobile context sheet | `87d6019f`, PR #2915 | Integrated |
| Semantic light/dark colour tokens | `453117e3`, PR #2916 | Integrated |
| Reduced-motion settlement | `97e7cb3b`, PR #2917 | Integrated |
| V6 socket truth and ordered resync recovery engine | `b70cfc3b`, `19bb7100`, `8b5a942c`, PRs #2918, #2920, #2922 | Engine integrated; D5 production wiring awaits WS envelope decision |
| Canvas load/stale projection recovery | `d10e76c5`, `ed0c9ce4`, PRs #2924, #2932; PRs #2963–#2973 | Integrated |
| Lens keyboard access and selected neighbourhood focus | `748a82e3`, `7918beed`, PRs #2925, #2933 | Integrated |
| Node report lineage, contributors, and attempt history | `471d7e29`, `ddf93eed`, PRs #2926–#2927 | Integrated |
| Typed status/confidence fidelity | `a4ad3529`, PR #2928 | Integrated |
| Canvas visual and accessibility localisation | `6b8d4853`, `efe31ec4`, `b3c28a38`, `4d272991`, `2e1dad935`, PRs #2930–#2931, #2974–#2976 | Integrated |
| Session-isolated camera, selection, and inspector restoration | `fa389f8f`, `4d21b75ea`, `4707ce9d5`, PRs #2934, #2998–#2999 | Integrated |
| 25% overview and DOM/card/node/edge budgets | `e8dd9b46`, `792ef4ac8`, `3dc864714`, PRs #2923, #2996–#2997 | Integrated; runtime evidence still required |
| Attempt-bound Agent identity and session-isolated filters | `ebbe40594`, `a437e30a5`, PRs #3001–#3002 | Integrated |
| Canonical V6 edge-family registration | `dd48ef731`, PR #3005 | Integrated |
| Canonical node commands (`continue | fork | retry | reassign`) | `2302775c9`, `29af08fa0`, PRs #3006–#3007 | Integrated; replaces chat-text command fallback and aligns task-only recovery eligibility |
| Capability fallback and localized recovery disclosure | `de0f2f194`, `1c3497112`, `d11b72e9f`, `9e5133c75`, PRs #3008, #3014–#3016 | Integrated; raw diagnostics remain collapsed and malformed V6 success remains an interface error |
| D5 command-bar product round | `7ee3283ac`, PR #3009 | Integrated; reads only canonical session round/budget facts |
| Node detail opening and deep-link restoration | `c62653dfc`, `2bb0db0ff`, PRs #3010–#3011 | Integrated; includes poll-safe one-time node-link restoration |
| Recovery-action focus retention | `29af08fa0`, `aa636fdef`, `7d28aa5ea`, PRs #3007, #3012–#3013 | Integrated; pending actions remain focusable and suppress duplicate activation |
| Remaining D5/V6 copy localisation | `046acd8e2`, `f2f2344bd`, PRs #3017–#3018 | Integrated; includes all registered V6 node kinds |
| Context-rail responsive geometry | `0daa942e5`, `0ad395142`, PRs #3019–#3020 | Integrated; flex-child canvas excludes the sibling rail exactly once and duplicate open-rail toggle is hidden |
| Custom control and responsive Lens accessibility | `08aa6f3d8`, PR #3021 | Integrated; semantic focus rings plus active Lens announcement/state |
| Long unbroken node-card copy containment | `cb8fa3b50`, PR #3022 | Integrated; registered and generic node cards retain bounded two-line density |
| Shared Research frontend typecheck baseline | `612034aea`, PR #3024 | Integrated; V6 Dispute panel uses a type-only `ReactNode` import |
| V6 WS Delta routing envelope | Existing V5 `research_session:graph_updated` payloads plus a direct-Delta V6 listener | Protocol decision required: a workspace event must identify `run_id`, including mutation-free Delta frames |
| Real V6 snapshot/delta/resume API | No matching route under `server/` at `dev@602ed7336` | Backend blocked; explicit V5/D5 fallback required |
| Runtime visual/performance/accessibility matrix | No current deployed-session artifacts for this revision | Unverified; required before completion |

### Completion evidence rule

The frontend is complete only when all applicable rows above are integrated and
the §8 matrix has evidence against one deployed revision. Static fixtures,
isolated modules, open PRs, or the absence of an obvious TODO do not prove the
session flow complete. V6 server-dependent rows may remain capability-gated,
but malformed successful responses must surface an interface error and must
never silently fall back.
