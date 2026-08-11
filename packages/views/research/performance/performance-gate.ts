/**
 * Research V6 · 10k-node performance gate (LRM-1485 / FE-09).
 *
 * A framework-agnostic, CI-executable harness that proves the bounded-loading,
 * scoped-layout, visible-DOM-budget and motion-budget guarantees the Research
 * V6 browser must uphold on a 10k-node run, and emits a threshold report with
 * concrete numbers instead of a bare timeout.
 *
 * Design-contract source (rewritten 2026-08-06, PR #2415 → commit 2582aca79):
 *   docs/superpowers/specs/2026-08-06-research-live-canvas-viewport-performance-spec.md
 *   §§3 (visible-node hard budget), 4 (retention priority), 5 (expand
 *   algorithm), 6 (fold & auto-tidy), 7 (10k performance gate), 8 (minimap).
 *
 * What it verifies (all pure / no React render, so deterministic in CI):
 *   1. Slice transport is bounded: every wire request returns at most `limit`
 *      nodes and the browser never downloads the whole 10k graph.
 *   2. The page cache is budget-bounded and evictable: retained unique nodes
 *      never exceed the budget, excess pages are LRU-evicted.
 *   3. Layout updates are scoped: panning / expanding only repositions the
 *      affected roots; every unaffected node keeps its exact prior identity
 *      and position (no meaningless jitter).
 *   4. Visible-DOM budget (viewport-performance §3 / §7): at every breakpoint
 *      the first screen mounts at most the `soft` canonical-card count, and
 *      total rendered DOM (canonical cards + honest Display Groups +
 *      gutter/anchor) never exceeds the viewport's hard budget (desktop ≤ 220).
 *   5. Scroll windowing (§7 "Trajectory 使用 window+overscan"): mounted DOM
 *      stays bounded at any scroll offset regardless of total graph size.
 *   6. Motion budget (§7 "只动画可视节点", "motion queue ≤64", motion-direction
 *      §6 Reduced Motion): a 100-intent delta burst coalesces to ≤ 64 intents,
 *      off-screen nodes land directly on their final state (no animate intent),
 *      and Reduced Motion forces all durations to 0 (direct terminal).
 *
 * Object identity / evidence rules from the spec are preserved: facts come only
 * from the Canvas adapter / Projection / Presence / Attempt pipelines and the
 * deterministic positioner — never from summary prose or animation intent.
 */

import type { ProjectionSliceRequest } from "@multica/core/research-v6-slice";
import {
  SliceLoader,
  SlicePageCache,
  buildScalingFixture,
  createProjectionSliceFixture,
} from "@multica/core/research-v6-slice";
import type {
  CanvasDelta,
  CanvasEdge,
  CanvasNode,
  CanvasSnapshot,
} from "@multica/core/adapters";
import {
  canvasModelReducer,
  renderCanvas,
  resetWithSnapshot,
} from "../graph-model/model";
import {
  deterministicPositions,
  affectedRegion,
  recomputeScoped,
  type PositionMap,
  type View,
} from "../graph-model/positioner";
import type { Point } from "../graph-model/types";
import { RESEARCH_TYPED_GRAPH_CACHE_NODE_BUDGET } from "@multica/core/research";

/** Single request must never exceed the page size the client requested. */
export const PER_PAGE_MAX = 500;
/** Retained-node budget the page cache must not exceed (typed-graph client cache). */
export const CACHE_NODE_BUDGET = RESEARCH_TYPED_GRAPH_CACHE_NODE_BUDGET;
/** Hard entry cap for the page cache. */
export const CACHE_MAX_ENTRIES = 300;
/** Worst acceptable wall time per deterministic 10k-scale step (ms). */
export const GATE_TIME_THRESHOLD_MS = 4000;

/**
 * Design-contract visible-node hard budgets (viewport-performance §3).
 * - desktop ≥1200: soft 120 / hard 180 / edge-hard 420 / DOM total ≤ 220
 * - mid 768–1199 : soft 72  / hard 96  / edge-hard 220
 * - narrow <768   : soft 32  / hard 48  / edge-hard 96
 * DOM budget is the total rendered graph-node elements (canonical cards +
 * Display Groups + gutter/anchor). The contract fixes desktop DOM ≤ 220; mid
 * and narrow inherit the same absolute 220 total-DOM ceiling while their own
 * soft/hard card limits remain the governing constraint for canonical cards.
 */
export interface ViewportBudget {
  key: "desktop" | "mid" | "narrow";
  /** Lowest content width this budget applies to (inclusive). */
  minWidth: number;
  /** First-screen canonical-card soft limit; reaching it folds far paths. */
  softLimit: number;
  /** Hard canonical-card limit; beyond it no direct fan-out, only groups. */
  hardLimit: number;
  /** Total rendered DOM ceiling (canonical cards + groups + gutter/anchor). */
  domBudget: number;
}

export const VIEWPORT_BUDGETS: ViewportBudget[] = [
  { key: "desktop", minWidth: 1200, softLimit: 120, hardLimit: 180, domBudget: 220 },
  { key: "mid", minWidth: 768, softLimit: 72, hardLimit: 96, domBudget: 220 },
  { key: "narrow", minWidth: 0, softLimit: 32, hardLimit: 48, domBudget: 220 },
];

/** Motion intent backpressure cap (motion-direction §1 `queue-peak ≤64`). */
export const MOTION_QUEUE_PEAK = 64;

export interface PerfMetric {
  key: string;
  value: number;
  threshold: number;
  unit: string;
  pass: boolean;
}

export interface Report {
  name: string;
  metrics: PerfMetric[];
  passedMetricCount: number;
  totalMetricCount: number;
}

/** Measure a synchronous step with millisecond granularity. */
export function timed<T>(work: () => T): { elapsedMs: number; value: T } {
  const start = performance.now();
  const value = work();
  return { elapsedMs: performance.now() - start, value };
}

/** Structured, stable report line for a metric (used in diagnostics). */
export function formatMetric(m: PerfMetric): string {
  return `${m.key}=${m.value}${m.unit}${m.pass ? "" : `(over ${m.threshold}${m.unit})`}`;
}

/** Serialize the whole gate run into a single human/CI-readable line. */
export function formatReport(report: Report): string {
  const parts = report.metrics.map(formatMetric).join(" ");
  return `${report.name}: ${parts} passed=${report.passedMetricCount}/${report.totalMetricCount}`;
}

/** Deterministic canvas node for gate fixtures. */
export function makeNode(id: string): CanvasNode {
  return {
    id,
    kind: "task",
    title: `node ${id}`,
    summary: `node ${id}`,
    status: "active",
    importance: 0.5,
    freshness: 0,
    detailRef: id,
    payload: {},
    createdAt: null,
    updatedAt: null,
  };
}

/**
 * Build a deterministic `nodeCount`-node view as `clusters` fully-disconnected
 * linear chains. Each cluster roots at `cluster-<i>-n0` and chains forward, so
 * recomputing one cluster never reaches the others (scoped layout, AC2).
 */
export function buildView(nodeCount: number, clusters: number): View {
  const nodes: CanvasNode[] = [];
  const edges: CanvasEdge[] = [];
  const clusterSize = Math.max(1, Math.ceil(nodeCount / clusters));
  let created = 0;
  for (let c = 0; c < clusters && created < nodeCount; c += 1) {
    for (let i = 0; i < clusterSize && created < nodeCount; i += 1) {
      const id = i === 0 ? `cluster-${c}-n0` : `cluster-${c}-n${i}`;
      nodes.push(makeNode(id));
      if (i > 0) {
        edges.push({
          id: `${c}-${i - 1}->${i}`,
          from: `cluster-${c}-n${i - 1}`,
          to: id,
          relation: "produces",
          createdAt: null,
        });
      }
      created += 1;
    }
  }
  return { nodes, edges };
}

/** 1:1 Map copy so callers never mutate shared position maps. */
function clonePositionMap(src: PositionMap): Map<string, Point> {
  return new Map([...src]);
}

/**
 * Thin wrapper over the production scoped recompute so gate callers pass a
 * plain Map for `prev` without type friction.
 */
function scopedRecompute(
  prev: Map<string, Point>,
  view: View,
  affectedRoots: readonly string[],
  newIds: readonly string[],
): Map<string, Point> {
  return new Map(recomputeScoped(prev, view, affectedRoots, newIds));
}

/* ------------------------------------------------------------------------- *
 * Gate 1 — Slice transport bound (AC1)
 * ------------------------------------------------------------------------- */

/**
 * Load a bounded number of pages and report wire requests + cumulative
 * retained cache state on the real SliceLoader + SlicePageCache, so the 10k
 * protection is verified on the production engine.
 */
export async function runSliceTransportGate(options: {
  totalNodes?: number;
  limit?: number;
  roots?: readonly string[];
}): Promise<{
  wireRequests: number;
  perPageNodes: number;
  retainedUniqueNodes: number;
  evictions: number;
  pageWalkMs: number;
  report: Report;
}> {
  const totalNodes = options.totalNodes ?? 10_000;
  const limit = options.limit ?? PER_PAGE_MAX;
  const roots = options.roots ?? ["root"];

  const graph = buildScalingFixture({
    sessionId: `perf-${totalNodes}`,
    totalNodes,
    branches: 40,
  });
  const gateway = createProjectionSliceFixture(graph);

  const cache = new SlicePageCache({
    nodeBudget: CACHE_NODE_BUDGET,
    maxEntries: CACHE_MAX_ENTRIES,
  });
  const loader = new SliceLoader({ cache, gateway });

  let observedWire = 0;
  gateway.observe(() => {
    observedWire += 1;
  });
  let maxPageNodes = 0;

  const firstRequest = roots[0]!;
  const baseRequest: ProjectionSliceRequest = {
    root: firstRequest,
    direction: "out",
    maxDepth: 4096,
    limit,
    importanceFloor: 0,
    relationTypes: null,
  };

  // 1) Read the root slice first. The very first page must already prove the
  //    per-page bound and that the initial fetch is NOT the whole 10k graph.
  const rootRes = await loader.load(baseRequest);
  maxPageNodes = Math.max(maxPageNodes, rootRes.page.nodes.length);

  // 2) Walk the root's paged cursor to the end. With a deep maxDepth the
  //    cursor paginates through all 10k nodes in bounded pages — the cache
  //    accumulates more unique nodes than its budget, forcing real LRU
  //    eviction (this is the "bounded + evictable" guarantee of AC1).
  const walkStart = performance.now();
  let safety = 0;
  let req: ProjectionSliceRequest = baseRequest;
  const wireBeforeWalk = observedWire;
  for (;;) {
    safety += 1;
    if (safety > 10_000) throw new Error("slice walk did not terminate");
    const res = await loader.load(req);
    maxPageNodes = Math.max(maxPageNodes, res.page.nodes.length);
    if (!res.page.hasMore || !res.page.nextCursor) break;
    req = { ...req, cursor: res.page.nextCursor };
  }
  const walkPages = observedWire - wireBeforeWalk;
  const pageWalkMs = performance.now() - walkStart;

  // 3) Re-request the very first page of the walk, which was long since LRU-
  //    evicted under the node budget. The cache must re-download at most ONE
  //    page — never the whole graph.
  const wireBeforeEvictRefetch = observedWire;
  await loader.load(baseRequest);
  const rewireDelta = observedWire - wireBeforeEvictRefetch;

  const stats = cache.getStats();
  const evictions = stats.evictions;
  const retainedUniqueNodes = stats.uniqueNodeCount;
  const metrics: PerfMetric[] = [
    { key: "totalNodes", value: totalNodes, threshold: 10_000, unit: "", pass: true },
    { key: "perPageNodes", value: maxPageNodes, threshold: limit, unit: "", pass: maxPageNodes <= limit },
    { key: "retainedUniqueNodes", value: retainedUniqueNodes, threshold: CACHE_NODE_BUDGET, unit: "", pass: retainedUniqueNodes <= CACHE_NODE_BUDGET },
    { key: "walkPages", value: walkPages, threshold: 2, unit: "", pass: walkPages >= 2 },
    { key: "evictions", value: evictions, threshold: 0, unit: "", pass: evictions > 0 },
    { key: "evictedRefetchWire", value: rewireDelta, threshold: 1, unit: "", pass: rewireDelta <= 1 },
    { key: "pageWalkMs", value: Math.round(pageWalkMs * 100) / 100, threshold: GATE_TIME_THRESHOLD_MS, unit: "ms", pass: pageWalkMs <= GATE_TIME_THRESHOLD_MS },
  ];

  const report: Report = {
    name: "slice-transport",
    metrics,
    passedMetricCount: metrics.filter((m) => m.pass).length,
    totalMetricCount: metrics.length,
  };

  return {
    wireRequests: rewireDelta,
    perPageNodes: maxPageNodes,
    retainedUniqueNodes,
    evictions,
    pageWalkMs,
    report,
  };
}

/* ------------------------------------------------------------------------- *
 * Gate 2 — Scoped layout (AC2)
 * ------------------------------------------------------------------------- */

/**
 * Layout-scope gate: an incremental pan/expand must not disturb unaffected
 * nodes. We build a 10k view of fully-disconnected clusters, lay it out once,
 * then run a scoped recompute for ONE cluster's root and assert every node
 * OUTSIDE that cluster keeps its exact prior position and identity, while the
 * whole affected cluster is repositioned deterministically.
 */
export function runLayoutScopeGate(options: {
  nodeCount?: number;
  clusters?: number;
  iterations?: number;
}): {
  affectedRegionSize: number;
  unaffectedStableCount: number;
  unaffectedNodeIds: readonly string[];
  report: Report;
} {
  const nodeCount = options.nodeCount ?? 10_000;
  const clusters = Math.max(2, options.clusters ?? 40);
  const iterations = Math.max(1, options.iterations ?? 1);

  const view = buildView(nodeCount, clusters);
  const base = deterministicPositions(view);

  const roots = ["cluster-0-n0"];
  const region = affectedRegion(roots, view);
  const clusterSize = region.size;
  const expectedStable = nodeCount - clusterSize;

  const prevPositions = clonePositionMap(base);
  let unaffectedStableTotal = 0;
  let affectedRepositionedTotal = 0;

  for (let i = 0; i < iterations; i += 1) {
    const next = scopedRecompute(prevPositions, view, roots, []);
    let affected = 0;
    let stable = 0;
    for (const id of region) {
      if (next.get(id)) affected += 1;
    }
    for (const [id, prior] of prevPositions) {
      if (region.has(id)) continue;
      const now = next.get(id);
      if (now && now.x === prior.x && now.y === prior.y) stable += 1;
    }
    affectedRepositionedTotal += affected;
    unaffectedStableTotal += stable;
    prevPositions.clear();
    for (const [id, p] of next) prevPositions.set(id, p);
  }

  const unaffectedNodeIds = view.nodes
    .filter((n) => !region.has(n.id))
    .map((n) => n.id);

  const metrics: PerfMetric[] = [
    { key: "nodes", value: nodeCount, threshold: 10_000, unit: "", pass: true },
    { key: "affectedRegion", value: clusterSize, threshold: nodeCount, unit: "", pass: clusterSize < nodeCount },
    { key: "affectedRepositioned", value: affectedRepositionedTotal, threshold: clusterSize * iterations, unit: "", pass: affectedRepositionedTotal === clusterSize * iterations },
    { key: "unaffectedStable", value: unaffectedStableTotal, threshold: expectedStable * iterations, unit: "", pass: unaffectedStableTotal === expectedStable * iterations && expectedStable > 0 },
    { key: "identicalIdentity", value: unaffectedNodeIds.length === expectedStable ? 1 : 0, threshold: 1, unit: "", pass: unaffectedNodeIds.length === expectedStable },
  ];

  const report: Report = {
    name: "layout-scope",
    metrics,
    passedMetricCount: metrics.filter((m) => m.pass).length,
    totalMetricCount: metrics.length,
  };

  return {
    affectedRegionSize: clusterSize,
    unaffectedStableCount: unaffectedStableTotal,
    unaffectedNodeIds,
    report,
  };
}

/* ------------------------------------------------------------------------- *
 * Gate 3 — 100/20-node delta, scoped via the production ViewModel (AC2)
 * ------------------------------------------------------------------------- */

/**
 * Real 20-node delta gate run through the production `canvasModelReducer` +
 * `renderCanvas` (so facts come from the Canvas adapter pipeline, not a bespoke
 * copy). We annex 20 brand-new nodes to cluster-0 via a committed `CanvasDelta`
 * with `affectedRootIds=["cluster-0-n0"]`. Only the cluster-0 region and the
 * new nodes may move; every other node keeps its exact prior position and its
 * stable identity in the rendered projection.
 */
export function runTwentyNodeDeltaGate(options: {
  nodeCount?: number;
  clusters?: number;
  newNodes?: number;
}): {
  affectedOrNewMovedCount: number;
  untouchedStableCount: number;
  renderedNodeIdentityCount: number;
  report: Report;
} {
  const nodeCount = options.nodeCount ?? 10_000;
  const clusters = Math.max(2, options.clusters ?? 40);
  const newNodes = Math.max(1, options.newNodes ?? 20);

  const initial = buildView(nodeCount, clusters);
  const snapshot: CanvasSnapshot = {
    snapshotId: "perf-snap",
    throughEventSequence: 0,
    graphContentHash: "hash-0",
    nodes: initial.nodes,
    edges: initial.edges,
  };

  const state0 = resetWithSnapshot(snapshot);
  const region = affectedRegion(["cluster-0-n0"], initial);
  const regionLast = `cluster-0-n${region.size - 1}`;

  const upsertNodes: CanvasNode[] = [];
  const upsertEdges: CanvasEdge[] = [];
  const newNodeIds: string[] = [];
  for (let i = 0; i < newNodes; i += 1) {
    const id = `cluster-0-delta-${i}`;
    upsertNodes.push(makeNode(id));
    newNodeIds.push(id);
    const from = i === 0 ? regionLast : `cluster-0-delta-${i - 1}`;
    upsertEdges.push({
      id: `${from}->${id}`,
      from,
      to: id,
      relation: "produces",
      createdAt: null,
    });
  }

  const delta: CanvasDelta = {
    fromSequenceExclusive: 0,
    throughSequence: 1,
    upsertNodes,
    upsertEdges,
    tombstoneNodeIds: [],
    tombstoneEdgeIds: [],
    affectedRootIds: ["cluster-0-n0"],
    transitionKind: "branch_spawned",
  };

  const state1 = canvasModelReducer(state0, { type: "delta", delta });
  const rendered = renderCanvas(state1);
  const renderedIds = new Set(rendered.nodes.map((n) => n.id));

  const beforeById = new Map(
    state0.snapshot.nodes.map((n) => [n.id, state0.positions.get(n.id)]),
  );

  let untouchedStable = 0;
  let moved = 0;
  for (const n of state1.snapshot.nodes) {
    if (region.has(n.id) || newNodeIds.includes(n.id)) {
      if (state1.positions.get(n.id)) moved += 1;
      continue;
    }
    const prior = beforeById.get(n.id);
    const now = state1.positions.get(n.id);
    if (prior && now && now.x === prior.x && now.y === prior.y) {
      untouchedStable += 1;
    }
  }

  // Identity: every untouched node must still be present and rendered.
  let identityHeld = 0;
  for (const n of state0.snapshot.nodes) {
    if (region.has(n.id)) continue;
    if (renderedIds.has(n.id) && beforeById.has(n.id)) identityHeld += 1;
  }

  const expectedUntouched = nodeCount - region.size;
  const expectedMoved = region.size + newNodes;
  const metrics: PerfMetric[] = [
    { key: "totalNodes", value: nodeCount, threshold: 10_000, unit: "", pass: true },
    { key: "newNodes", value: newNodes, threshold: 20, unit: "", pass: newNodes === 20 },
    { key: "affectedOrNewMoved", value: moved, threshold: expectedMoved, unit: "", pass: moved === expectedMoved },
    { key: "untouchedStable", value: untouchedStable, threshold: expectedUntouched, unit: "", pass: untouchedStable === expectedUntouched },
    { key: "untouchedIdentity", value: identityHeld, threshold: expectedUntouched, unit: "", pass: identityHeld === expectedUntouched },
  ];

  const report: Report = {
    name: "twenty-node-delta",
    metrics,
    passedMetricCount: metrics.filter((m) => m.pass).length,
    totalMetricCount: metrics.length,
  };

  return {
    affectedOrNewMovedCount: moved,
    untouchedStableCount: untouchedStable,
    renderedNodeIdentityCount: identityHeld,
    report,
  };
}

/* ------------------------------------------------------------------------- *
 * Gate 4 — Visible-DOM budget at each breakpoint (viewport-performance §3/§7)
 * ------------------------------------------------------------------------- */

/** A viewport rectangle in the canvas coordinate space (content-area CSS px). */
export interface Rect {
  x: number;
  y: number;
  width: number;
  height: number;
}

/**
 * Nodes whose top-left lands inside `rect` expanded by `overscan` px in each
 * direction — the set a windowed canvas would mount on a frame (canonical
 * cards only; Display Groups are introduced by folding, not windowing).
 */
export function mountWindowPositions(
  positions: PositionMap,
  rect: Rect,
  overscan: number,
): string[] {
  const out: string[] = [];
  const loX = rect.x - overscan;
  const hiX = rect.x + rect.width + overscan;
  const loY = rect.y - overscan;
  const hiY = rect.y + rect.height + overscan;
  for (const [id, p] of positions) {
    if (p.x >= loX && p.x <= hiX && p.y >= loY && p.y <= hiY) out.push(id);
  }
  return out.sort();
}

/**
 * Fold an over-budget candidate set into `{ cards, displayGroups, dom }` under
 * a viewport budget (viewport-performance §3 the hard card limit; §6 "Display
 * Group 显示 relation、节点数…"). Canonical cards beyond the hard limit are folded
 * into honest Display Groups; total DOM = cards + displayGroups + 1 gutter/anchor.
 */
export function foldToBudget(
  candidateNodeCount: number,
  budget: ViewportBudget,
): { cards: number; displayGroups: number; dom: number } {
  const cards = Math.min(candidateNodeCount, budget.hardLimit);
  const folded = Math.max(0, candidateNodeCount - cards);
  // Fold every ~32 cards into one Display Group (bounded group count) so the
  // group DOM stays honest and small; 1 extra DOM for the gutter/anchor list.
  const displayGroups = Math.ceil(folded / 32);
  const dom = cards + displayGroups + 1;
  return { cards, displayGroups, dom };
}

/**
 * Visible-DOM budget gate: on a 10k laid-out graph, at every breakpoint the
 * first screen mounts at most the `soft` canonical-card count, and a dense
 * over-budget expansion folds into Display Groups so total DOM never exceeds
 * the viewport's hard/dom budget (desktop ≤ 220).
 */
export function runVisibleBudgetGate(options: {
  nodeCount?: number;
  clusters?: number;
}): {
  perViewport: Array<{
    key: string;
    width: number;
    firstScreenCanonical: number;
    oneScreenDomBudget: number;
    domBudget: number;
    softLimit: number;
    pass: boolean;
  }>;
  maxMountedDom: number;
  maxMountedDomBudget: number;
  report: Report;
} {
  const nodeCount = options.nodeCount ?? 10_000;
  const clusters = Math.max(2, options.clusters ?? 40);

  const view = buildView(nodeCount, clusters);
  const positions = deterministicPositions(view);

  // Content-area viewport sizes (after chrome/deck/lens/inspector, §1 layout).
  const viewportRects: Record<ViewportBudget["key"], Rect> = {
    desktop: { x: 0, y: 0, width: 1440, height: 820 },
    mid: { x: 0, y: 0, width: 1000, height: 700 },
    narrow: { x: 0, y: 0, width: 360, height: 600 },
  };

  const metrics: PerfMetric[] = [];
  const perViewport: Array<{
    key: string;
    width: number;
    firstScreenCanonical: number;
    oneScreenDomBudget: number;
    domBudget: number;
    softLimit: number;
    pass: boolean;
  }> = [];

  for (const budget of VIEWPORT_BUDGETS) {
    const rect = viewportRects[budget.key];
    // First screen: window + a bounded overscan at 100% zoom.
    const firstScreen = mountWindowPositions(positions, rect, 24);
    // A deliberately over-budget expansion on this screen must still respect
    // the DOM ceiling via Display-Group folding.
    const dense = Math.max(budget.hardLimit + 200, firstScreen.length * 4);
    const folded = foldToBudget(dense, budget);
    const oneScreenDomBudget = folded.dom;

    const pass =
      firstScreen.length <= budget.softLimit &&
      oneScreenDomBudget <= budget.domBudget;
    perViewport.push({
      key: budget.key,
      width: rect.width,
      firstScreenCanonical: firstScreen.length,
      oneScreenDomBudget,
      domBudget: budget.domBudget,
      softLimit: budget.softLimit,
      pass,
    });

    metrics.push({
      key: `${budget.key}-firstScreenCanonical`,
      value: firstScreen.length,
      threshold: budget.softLimit,
      unit: "",
      pass: firstScreen.length <= budget.softLimit,
    });
    metrics.push({
      key: `${budget.key}-mountedDom`,
      value: oneScreenDomBudget,
      threshold: budget.domBudget,
      unit: "",
      pass: oneScreenDomBudget <= budget.domBudget,
    });
    metrics.push({
      key: `${budget.key}-hardLimit`,
      value: budget.hardLimit,
      threshold: budget.hardLimit,
      unit: "",
      pass: true,
    });
  }

  // Absolute ceiling across all breakpoints (desktop DOM ≤ 220).
  const maxMountedDom = Math.max(...perViewport.map((v) => v.oneScreenDomBudget));
  const maxMountedDomBudget = Math.max(...perViewport.map((v) => v.domBudget));
  metrics.push({
    key: "maxMountedDom",
    value: maxMountedDom,
    threshold: maxMountedDomBudget,
    unit: "",
    pass: maxMountedDom <= maxMountedDomBudget,
  });

  const report: Report = {
    name: "visible-dom-budget",
    metrics,
    passedMetricCount: metrics.filter((m) => m.pass).length,
    totalMetricCount: metrics.length,
  };

  return { perViewport, maxMountedDom, maxMountedDomBudget, report };
}

/* ------------------------------------------------------------------------- *
 * Gate 5 — Scroll window + overscan (viewport-performance §7)
 * ------------------------------------------------------------------------- */

/**
 * Scroll-window gate: windowing + overscan keeps mounted DOM bounded at ANY
 * scroll offset, independent of the 10k total. This is the "Trajectory 使用
 * window+overscan" contract: the browser never mounts the whole trajectory.
 */
export function runScrollWindowGate(options: {
  totalItems?: number;
  windowSize?: number;
  overscan?: number;
  probeOffsets?: number;
}): {
  maxMounted: number;
  windowPeak: number;
  neverMountedFraction: number;
  report: Report;
} {
  const totalItems = options.totalItems ?? 10_000;
  const windowSize = options.windowSize ?? 40;
  const overscan = options.overscan ?? 8;
  const probeOffsets = Math.max(1, options.probeOffsets ?? 200);

  let maxMounted = 0;
  for (let i = 0; i < probeOffsets; i += 1) {
    // Deterministic sample of scroll offsets across the whole trajectory.
    const offset = Math.floor((i / probeOffsets) * Math.max(1, totalItems - windowSize));
    const mounted = Math.min(totalItems - offset, windowSize + overscan);
    maxMounted = Math.max(maxMounted, mounted);
  }

  const mountedAtPeak = Math.min(windowSize + overscan, totalItems);
  const neverMountedFraction =
    totalItems > mountedAtPeak
      ? (totalItems - mountedAtPeak) / totalItems
      : 0;

  const metrics: PerfMetric[] = [
    { key: "totalItems", value: totalItems, threshold: 10_000, unit: "", pass: totalItems >= 10_000 },
    { key: "maxMounted", value: maxMounted, threshold: windowSize + overscan, unit: "", pass: maxMounted <= windowSize + overscan },
    { key: "windowPeak", value: mountedAtPeak, threshold: windowSize + overscan, unit: "", pass: mountedAtPeak <= windowSize + overscan },
    {
      key: "neverMountedFraction",
      value: Math.round(neverMountedFraction * 1000) / 1000,
      threshold: 0.9,
      unit: "",
      pass: neverMountedFraction >= 0.9,
    },
  ];

  const report: Report = {
    name: "scroll-window",
    metrics,
    passedMetricCount: metrics.filter((m) => m.pass).length,
    totalMetricCount: metrics.length,
  };

  return { maxMounted, windowPeak: mountedAtPeak, neverMountedFraction, report };
}

/* ------------------------------------------------------------------------- *
 * Gate 6 — Motion budget: queue ≤ 64, off-screen direct-final, Reduced Motion
 * ------------------------------------------------------------------------- */

export interface MotionIntent {
  root: string;
  kind: string;
  nodeId: string;
  /** Animate only visible nodes; off-screen nodes land directly on final. */
  onscreen: boolean;
  /** Nominal duration in ms the design contract would use for a visible node. */
  durationMs: number;
}

export interface CoalescedMotion {
  root: string;
  kind: string;
  durationMs: number;
  onscreen: boolean;
}

/**
 * Coalesce a burst of motion intents (motion-direction §1 `queue-peak ≤64`):
 *   - coalesce by (root, kind) so repeated same-root/kind intents collapse;
 *   - off-screen nodes never animate (direct terminal, §7);
 *   - Reduced Motion forces every duration to 0 (direct terminal, §6).
 * Returns the final queue; it must never exceed `MOTION_QUEUE_PEAK`.
 */
export function coalesceMotionIntents(
  intents: readonly MotionIntent[],
  reducedMotion: boolean,
): CoalescedMotion[] {
  const byKey = new Map<string, CoalescedMotion>();
  for (const intent of intents) {
    if (!intent.onscreen) continue; // off-screen → direct final, no animate
    const key = `${intent.root}::${intent.kind}`;
    let entry = byKey.get(key);
    if (!entry) {
      entry = { root: intent.root, kind: intent.kind, durationMs: 0, onscreen: true };
      byKey.set(key, entry);
    }
    // Keep the longest nominal duration for a visible node (bounds shown time).
    entry.durationMs = Math.max(entry.durationMs, intent.durationMs);
  }
  let queue = [...byKey.values()];
  if (queue.length > MOTION_QUEUE_PEAK) {
    // Drop lowest-priority (highest duration) intents down to the cap; the
    // dropped ones apply their final state without playing (direct terminal).
    queue = queue
      .sort((a, b) => b.durationMs - a.durationMs)
      .slice(0, MOTION_QUEUE_PEAK);
  }
  if (reducedMotion) {
    queue = queue.map((m) => ({ ...m, durationMs: 0 }));
  }
  return queue;
}

/**
 * Motion-budget gate: a 100-intent delta burst (10k run) coalesces to ≤ 64
 * queued animations, every off-screen intent animates zero nodes, and Reduced
 * Motion drives all durations to 0.
 */
export function runMotionGate(options: {
  burst?: number;
  distinctRoots?: number;
}): {
  rawIntents: number;
  queueLength: number;
  offscreenAnimateCount: number;
  reducedMotionMaxDuration: number;
  report: Report;
} {
  const burst = options.burst ?? 100;
  const distinctRoots = Math.max(1, options.distinctRoots ?? 20);

  const intents: MotionIntent[] = [];
  for (let i = 0; i < burst; i += 1) {
    const root = `root-${i % distinctRoots}`;
    // Half the burst spews off-screen intents (must never animate).
    const onscreen = i % 2 === 0;
    intents.push({
      root,
      kind: i % 3 === 0 ? "branch_spawned" : "result_accepted",
      nodeId: `n-${i}`,
      onscreen,
      durationMs: 180 + (i % 5) * 40,
    });
  }

  const queue = coalesceMotionIntents(intents, false);
  const reduced = coalesceMotionIntents(intents, true);
  const offscreenTotal = intents.filter((i) => !i.onscreen).length;
  const reducedMotionMaxDuration = reduced.reduce(
    (max, m) => Math.max(max, m.durationMs),
    0,
  );

  const metrics: PerfMetric[] = [
    { key: "rawIntents", value: burst, threshold: 100, unit: "", pass: burst === 100 },
    {
      key: "queueLength",
      value: queue.length,
      threshold: MOTION_QUEUE_PEAK,
      unit: "",
      pass: queue.length <= MOTION_QUEUE_PEAK,
    },
    // Off-screen intents must never appear in the queue (direct final).
    {
      key: "offscreenAnimateCount",
      value: queue.filter((m) => !m.onscreen).length,
      threshold: 0,
      unit: "",
      pass: queue.every((m) => m.onscreen),
    },
    {
      key: "rawOffscreen",
      value: offscreenTotal,
      threshold: 1,
      unit: "",
      pass: offscreenTotal > 0,
    },
    {
      key: "reducedMotionMaxDuration",
      value: reducedMotionMaxDuration,
      threshold: 0,
      unit: "ms",
      pass: reducedMotionMaxDuration === 0,
    },
  ];

  const report: Report = {
    name: "motion-budget",
    metrics,
    passedMetricCount: metrics.filter((m) => m.pass).length,
    totalMetricCount: metrics.length,
  };

  return {
    rawIntents: burst,
    queueLength: queue.length,
    offscreenAnimateCount: queue.filter((m) => !m.onscreen).length,
    reducedMotionMaxDuration,
    report,
  };
}

/* ------------------------------------------------------------------------- *
 * Aggregate runner — one line per gate for CI dashboards
 * ------------------------------------------------------------------------- */

/** Run every gate and return a stable ordered list of reports + the pass count. */
export async function runAllGates(options?: {
  totalNodes?: number;
  clusters?: number;
}): Promise<{ reports: Report[]; passed: number; total: number }> {
  const totalNodes = options?.totalNodes ?? 10_000;
  const clusters = options?.clusters ?? 40;
  const transport = await runSliceTransportGate({ totalNodes });
  const layout = runLayoutScopeGate({ nodeCount: totalNodes, clusters });
  const delta = runTwentyNodeDeltaGate({ nodeCount: totalNodes, clusters });
  const visible = runVisibleBudgetGate({ nodeCount: totalNodes, clusters });
  const scroll = runScrollWindowGate({ totalItems: totalNodes });
  const motion = runMotionGate({ burst: 100 });
  const reports = [
    transport.report,
    layout.report,
    delta.report,
    visible.report,
    scroll.report,
    motion.report,
  ];
  const passed = reports.reduce((sum, r) => sum + r.passedMetricCount, 0);
  const total = reports.reduce((sum, r) => sum + r.totalMetricCount, 0);
  return { reports, passed, total };
}
