/**
 * Route topology builder (LRM-1487 / 实现-11).
 *
 * Turns a bounded `CanvasSlice` + `protectedIds` + a stable seed into a pure,
 * structural `RouteTopology`: the main spine, exploration branches, failed
 * dead-ends, retry hairpins, convergence corridors and Route Bundles. It
 * performs NO layout (see `layoutOrganicRoutes`) and NO text inference
 * (see `outcome.ts`).
 *
 * Determinism contract: `buildRouteTopology` is a pure function of
 * (slice, protectedIds, stableSeed). Identical inputs -> identical topology.
 * All orderings are stable sorts over stable node ids; no `Math.random()`.
 */
import type { CanvasEdge, CanvasNode, CanvasSlice } from "@multica/core/adapters";
import { buildOutcomeRegistry, classifyNodeOutcome } from "./outcome";
import type {
  RouteBundle,
  RouteEdgeSpec,
  RouteNodeSpec,
  RouteOutcome,
  RouteTopology,
  SemanticLOD,
} from "./types";

/* ---------------------------------------------------------------------------
 * Explicit structural registries (content-free; consistency with outcome.ts).
 * ------------------------------------------------------------------------- */

/** Relations that denote a retry/re-attempt hairpin. */
const KNOWN_RETRY_RELATION = new Set<string>([
  "retry",
  "retried",
  "retry_of",
  "reiterate",
]);

/** Relations that carry a convergence (integration) semantic. */
const KNOWN_CONVERGE_RELATION = new Set<string>([
  "integrates",
  "derived_from",
  "integrated_from",
]);

/** Node kinds that are natural convergence / decision landmarks. */
const KNOWN_SINK_KINDS = new Set<string>([
  "insight",
  "decision",
  "report_revision",
  "report",
]);

/* ---------------------------------------------------------------------------
 * Budget knobs (consistent with viewport-performance spec hard limits).
 * ------------------------------------------------------------------------- */

export const ROUTE_BUDGETS = {
  /** Desktop semantic node hard limit. */
  semanticNodeHardLimit: 180,
  /** Default relationship reveal depth (2, expandable to 3). */
  defaultDepth: 2,
  /** Explicit expand depth cap. */
  maxDepth: 3,
  /** Bundle is formed when a neighborhood exceeds this many nodes. */
  bundleNodeThreshold: 12,
} as const;

/** Terminal outcomes considered to end a narrative path. */
const isTerminal = (outcome: RouteOutcome): boolean =>
  outcome === "failed" ||
  outcome === "accepted" ||
  outcome === "cancelled" ||
  outcome === "stale" ||
  outcome === "disputed";

/* ---------------------------------------------------------------------------
 * Small graph primitives over a CanvasSlice (deterministic).
 * ------------------------------------------------------------------------- */

interface SliceIndex {
  byId: Map<string, CanvasNode>;
  out: Map<string, CanvasEdge[]>;
  inEdges: Map<string, CanvasEdge[]>;
}

function indexSlice(slice: CanvasSlice): SliceIndex {
  const byId = new Map<string, CanvasNode>();
  const out = new Map<string, CanvasEdge[]>();
  const inEdges = new Map<string, CanvasEdge[]>();
  for (const n of slice.nodes) byId.set(n.id, n);
  for (const e of slice.edges) {
    if (!byId.has(e.from) || !byId.has(e.to)) continue;
    const ol = out.get(e.from) ?? [];
    ol.push(e);
    out.set(e.from, ol);
    const il = inEdges.get(e.to) ?? [];
    il.push(e);
    inEdges.set(e.to, il);
  }
  for (const list of out.values()) list.sort((a, b) => a.id.localeCompare(b.id));
  for (const list of inEdges.values()) list.sort((a, b) => a.id.localeCompare(b.id));
  return { byId, out, inEdges };
}

/** Forward neighbours of `id` (following the slice direction). */
function forwardNeighbours(
  id: string,
  idx: SliceIndex,
  direction: CanvasSlice["direction"],
): string[] {
  if (!idx.byId.has(id)) return [];
  const ids: string[] = [];
  const push = (e: CanvasEdge) => {
    if (idx.byId.has(e.to) && !ids.includes(e.to)) ids.push(e.to);
  };
  const pushRev = (e: CanvasEdge) => {
    if (idx.byId.has(e.from) && !ids.includes(e.from)) ids.push(e.from);
  };
  if (direction === "in") {
    for (const e of idx.inEdges.get(id) ?? []) pushRev(e);
  } else if (direction === "both") {
    for (const e of idx.out.get(id) ?? []) push(e);
    for (const e of idx.inEdges.get(id) ?? []) pushRev(e);
  } else {
    for (const e of idx.out.get(id) ?? []) push(e);
  }
  return ids;
}

function neighbours(id: string, idx: SliceIndex): string[] {
  if (!idx.byId.has(id)) return [];
  const set = new Set<string>();
  for (const e of idx.out.get(id) ?? []) {
    if (idx.byId.has(e.to)) set.add(e.to);
  }
  for (const e of idx.inEdges.get(id) ?? []) {
    if (idx.byId.has(e.from)) set.add(e.from);
  }
  return [...set].sort((a, b) => a.localeCompare(b));
}

/* ---------------------------------------------------------------------------
 * Semantic LOD (internal; a renderer registry may refine this later — kept
 * here so geometry has a stable size model and the AC1 landmark/waypoint/
 * trail-dot split is deterministic).
 * ------------------------------------------------------------------------- */

function classifySemanticLOD(
  node: CanvasNode,
  outcome: RouteOutcome,
  protectedIds: ReadonlySet<string>,
  onSpine: boolean,
): SemanticLOD {
  if (protectedIds.has(node.id)) return "landmark";
  if (
    outcome === "failed" ||
    outcome === "stale" ||
    outcome === "disputed" ||
    outcome === "accepted"
  ) {
    return "landmark";
  }
  if (onSpine) return "landmark";
  if (node.kind === "insight" || node.kind === "decision") return "landmark";
  if (node.importance >= 0.6 || node.kind === "claim" || node.kind === "question") {
    return "waypoint";
  }
  return "trail-dot";
}

/* ---------------------------------------------------------------------------
 * Main spine detection.
 * ------------------------------------------------------------------------- */

/** Greedy narrative walk from root choosing the strongest candidate each step. */
function buildSpine(
  slice: CanvasSlice,
  idx: SliceIndex,
  protectedIds: ReadonlySet<string>,
  outcomes: Map<string, RouteOutcome>,
): string[] {
  if (!idx.byId.has(slice.rootId)) {
    const ids = [...idx.byId.values()].sort(
      (a, b) => b.importance - a.importance || a.id.localeCompare(b.id),
    );
    return ids.length ? [ids[0]!.id] : [];
  }
  const spine: string[] = [slice.rootId];
  const visited = new Set<string>([slice.rootId]);
  let cursor = slice.rootId;
  for (let step = 0; step < slice.nodes.length; step += 1) {
    const nexts = forwardNeighbours(cursor, idx, slice.direction).filter(
      (n) => !visited.has(n),
    );
    if (nexts.length === 0) break;
    const ranked = [...nexts].sort((a, b) => {
      const pa = protectedIds.has(a) ? 1 : 0;
      const pb = protectedIds.has(b) ? 1 : 0;
      if (pa !== pb) return pb - pa;
      const oa = outcomes.get(a) ?? "neutral";
      const ob = outcomes.get(b) ?? "neutral";
      const sa = isTerminal(oa) ? 0 : 1;
      const sb = isTerminal(ob) ? 0 : 1;
      if (sa !== sb) return sb - sa;
      const imp = idx.byId.get(b)!.importance - idx.byId.get(a)!.importance;
      if (imp !== 0) return imp;
      return a.localeCompare(b);
    });
    const next = ranked[0]!;
    spine.push(next);
    visited.add(next);
    if (isTerminal(outcomes.get(next) ?? "neutral")) break;
    cursor = next;
  }
  return spine;
}

/* ---------------------------------------------------------------------------
 * Branch assignment.
 * ------------------------------------------------------------------------- */

interface BranchInfo {
  branchId: string;
  fromId: string;
  nodeIds: string[];
}

/** Assign every non-spine node to the branch rooted at the nearest spine node. */
function assignBranches(
  idx: SliceIndex,
  spineSet: Set<string>,
  spineOrder: Map<string, number>,
): BranchInfo[] {
  const branches: BranchInfo[] = [];
  const visitedBranch = new Set<string>();

  // A node entered via a convergence/integration edge is a corridor sink — it
  // must NOT be absorbed into an exploration branch. Precompute that set once.
  const convergeSinks = new Set<string>();
  for (const list of idx.inEdges.values()) {
    for (const e of list) {
      if (KNOWN_CONVERGE_RELATION.has(e.relation)) convergeSinks.add(e.to);
    }
  }

  for (const sp of spineOrder.keys()) {
    for (const nb of neighbours(sp, idx)) {
      if (spineSet.has(nb) || visitedBranch.has(nb)) continue;
      // A sink reached through a convergence relation is a corridor target,
      // not an exploration tail — it does not start a branch.
      if (convergeSinks.has(nb)) continue;
      const bi: BranchInfo = {
        branchId: `branch:${spineOrder.get(sp) ?? 0}-${nb}`,
        fromId: sp,
        nodeIds: [nb],
      };
      branches.push(bi);
      const biSet = new Set<string>([nb]);
      const stack: string[] = [nb];
      while (stack.length) {
        const cur = stack.pop()!;
        for (const e of (idx.out.get(cur) ?? [])
          .concat(idx.inEdges.get(cur) ?? [])) {
          // Don't continue branch absorption across a convergence edge.
          if (KNOWN_CONVERGE_RELATION.has(e.relation)) continue;
          const nxt = e.from === cur ? e.to : e.from;
          if (spineSet.has(nxt) || visitedBranch.has(nxt)) continue;
          visitedBranch.add(nxt);
          if (!biSet.has(nxt)) {
            biSet.add(nxt);
            bi.nodeIds.push(nxt);
          }
          stack.push(nxt);
        }
      }
    }
  }
  branches.sort((a, b) => a.branchId.localeCompare(b.branchId));
  return branches;
}

/* ---------------------------------------------------------------------------
 * Dead-end / retry / corridor detection.
 * ------------------------------------------------------------------------- */

interface DeadEndInfo {
  nodeIds: string[];
  terminalId: string;
  fromId: string;
}

interface RetryInfo {
  retryId: string;
  fromId: string;
  failureId: string;
  toId: string;
}

interface CorridorInfo {
  corridorId: string;
  nodeIds: string[];
  intoId: string;
}

function detectFailureStructures(
  slice: CanvasSlice,
  idx: SliceIndex,
  outcomes: Map<string, RouteOutcome>,
  spineSet: Set<string>,
  branches: BranchInfo[],
): { deadEnds: DeadEndInfo[]; retries: RetryInfo[] } {
  const deadEnds: DeadEndInfo[] = [];
  const retries: RetryInfo[] = [];

  // Branch membership per node.
  const branchByNode = new Map<string, BranchInfo>();
  for (const bi of branches) {
    for (const n of bi.nodeIds) branchByNode.set(n, bi);
  }

  // Retry edges: failed node -> a new attempt via an explicit retry relation.
  const retryByTerminal = new Map<string, RetryInfo>();
  for (const e of slice.edges) {
    if ((outcomes.get(e.from) ?? "neutral") !== "failed") continue;
    if (!KNOWN_RETRY_RELATION.has(e.relation)) continue;
    const toOutcome = outcomes.get(e.to) ?? "neutral";
    if (toOutcome === "failed" || toOutcome === "cancelled") continue;
    const bi = branchByNode.get(e.from);
    if (!bi) continue;
    const retryId = `retry:${e.from}:${e.to}`;
    retries.push({ retryId, fromId: bi.fromId, failureId: e.from, toId: e.to });
    retryByTerminal.set(e.from, {
      retryId,
      fromId: bi.fromId,
      failureId: e.from,
      toId: e.to,
    });
  }

  // Dead-ends are failed terminals without a retry continuation.
  for (const bi of branches) {
    for (const n of bi.nodeIds) {
      if ((outcomes.get(n) ?? "neutral") !== "failed") continue;
      if (retryByTerminal.has(n)) continue;
      const hasOut = (idx.out.get(n) ?? []).some(
        (e) => !spineSet.has(e.to) && !KNOWN_CONVERGE_RELATION.has(e.relation),
      );
      if (hasOut) continue;
      deadEnds.push({ nodeIds: bi.nodeIds, terminalId: n, fromId: bi.fromId });
      break;
    }
  }
  return { deadEnds, retries };
}

function detectCorridors(
  slice: CanvasSlice,
  idx: SliceIndex,
  outcomes: Map<string, RouteOutcome>,
  spineSet: Set<string>,
): CorridorInfo[] {
  const corridors: CorridorInfo[] = [];
  const seen = new Set<string>();
  for (const n of slice.nodes) {
    const isSink =
      KNOWN_SINK_KINDS.has(n.kind) ||
      (idx.inEdges.get(n.id) ?? []).some((e) =>
        KNOWN_CONVERGE_RELATION.has(e.relation),
      );
    if (!isSink) continue;
    const feeders: string[] = [];
    const feederSet = new Set<string>();
    for (const e of idx.inEdges.get(n.id) ?? []) {
      const from = e.from;
      if (spineSet.has(from)) continue;
      if (KNOWN_CONVERGE_RELATION.has(e.relation)) {
        if (!feederSet.has(from)) {
          feederSet.add(from);
          feeders.push(from);
        }
        continue;
      }
      const o = outcomes.get(from) ?? "neutral";
      if (o === "accepted" || o === "neutral" || o === "exploring") {
        if (!feederSet.has(from)) {
          feederSet.add(from);
          feeders.push(from);
        }
      }
    }
    if (feeders.length === 0) continue;
    const corridorId = `corridor:${n.id}`;
    if (seen.has(corridorId)) continue;
    seen.add(corridorId);
    corridors.push({ corridorId, nodeIds: feeders, intoId: n.id });
  }
  corridors.sort((a, b) => a.corridorId.localeCompare(b.corridorId));
  return corridors;
}

/* ---------------------------------------------------------------------------
 * Route Bundles (honest aggregates over folded neighborhoods).
 * ------------------------------------------------------------------------- */

function buildBundles(
  slice: CanvasSlice,
  idx: SliceIndex,
  spineSet: Set<string>,
  outcomes: Map<string, RouteOutcome>,
  protectedIds: ReadonlySet<string>,
): RouteBundle[] {
  const bundles: RouteBundle[] = [];
  const visited = new Set<string>();

  for (const seedNode of [...slice.nodes].sort((a, b) =>
    a.id.localeCompare(b.id),
  )) {
    if (spineSet.has(seedNode.id)) continue;
    if (protectedIds.has(seedNode.id)) continue;
    if (visited.has(seedNode.id)) continue;

    const cluster: string[] = [];
    const queue = [seedNode.id];
    visited.add(seedNode.id);
    while (queue.length) {
      const cur = queue.pop()!;
      if (!spineSet.has(cur) && !protectedIds.has(cur)) cluster.push(cur);
      for (const nb of neighbours(cur, idx)) {
        if (spineSet.has(nb) || protectedIds.has(nb)) continue;
        if (visited.has(nb)) continue;
        visited.add(nb);
        queue.push(nb);
      }
    }
    if (cluster.length < ROUTE_BUDGETS.bundleNodeThreshold) continue;

    const bundleId = `bundle:${seedNode.id}`;
    const outcomeByNode = new Map<string, RouteOutcome>();
    for (const id of cluster) {
      outcomeByNode.set(id, outcomes.get(id) ?? "neutral");
    }
    const representatives = [...cluster]
      .sort((a, b) => {
        const oa = outcomes.get(a) ?? "neutral";
        const ob = outcomes.get(b) ?? "neutral";
        const rank = (o: string) =>
          o === "failed" || o === "stale" || o === "disputed"
            ? 0
            : o === "accepted"
              ? 1
              : 2;
        const ra = rank(oa);
        const rb = rank(ob);
        if (ra !== rb) return ra - rb;
        return a.localeCompare(b);
      })
      .slice(0, 12);

    let anchorId = seedNode.id;
    outer: for (const id of cluster) {
      for (const e of idx.inEdges.get(id) ?? []) {
        if (spineSet.has(e.from)) {
          anchorId = id;
          break outer;
        }
      }
    }
    bundles.push({
      id: bundleId,
      anchorId,
      nodeIds: cluster,
      outcomeByNode,
      representativeIds: representatives,
    });
  }
  bundles.sort((a, b) => a.id.localeCompare(b.id));
  return bundles;
}

/* ---------------------------------------------------------------------------
 * Edges — build route edge specs with outcome + kind.
 * ------------------------------------------------------------------------- */

/**
 * Determine the structural kind and outcome of an edge in the route map.
 */
function analyzeEdge(
  e: CanvasEdge,
  spineSet: Set<string>,
  spineOrder: Map<string, number>,
  retryIds: Set<string>,
  corridorSets: Map<string, Set<string>>, // corridorId -> feeder node ids
): RouteEdgeSpec["kind"] {
  if (retryIds.has(`${e.from}\u0000${e.to}`)) return "retry-hairpin";
  for (const feeders of corridorSets.values()) {
    if (feeders.has(e.from) && !spineSet.has(e.to)) return "convergence";
  }
  const onSpineFrom = spineSet.has(e.from);
  const onSpineTo = spineSet.has(e.to);
  if (onSpineFrom && onSpineTo) {
    const fi = spineOrder.get(e.from) ?? 0;
    const ti = spineOrder.get(e.to) ?? 0;
    return Math.abs(ti - fi) === 1 ? "spine" : "bundle";
  }
  return "branch";
}

/**
 * Build the pure route topology for a bounded slice.
 *
 * @param slice         bounded CanvasSlice (nodes/edges/root/direction).
 * @param protectedIds  node ids that must stay landmark & never folded.
 * @param stableSeed    opaque seed controlling stable organic variation.
 */
export function buildRouteTopology(
  slice: CanvasSlice,
  protectedIds: ReadonlySet<string>,
  stableSeed: string,
): RouteTopology {
  const idx = indexSlice(slice);
  const registry = buildOutcomeRegistry(slice);

  // Node outcomes.
  const outcomes = new Map<string, RouteOutcome>();
  for (const n of slice.nodes) {
    outcomes.set(n.id, classifyNodeOutcome(n.id, registry));
  }

  // Main spine.
  const spine = buildSpine(slice, idx, protectedIds, outcomes);
  const spineSet = new Set(spine);
  const spineOrder = new Map<string, number>();
  spine.forEach((id, i) => spineOrder.set(id, i));

  // Branches (stable).
  const branches = assignBranches(idx, spineSet, spineOrder);

  // Dead ends + retries.
  const { deadEnds, retries } = detectFailureStructures(
    slice,
    idx,
    outcomes,
    spineSet,
    branches,
  );
  const retryIds = new Set<string>();
  for (const r of retries) {
    retryIds.add(`${r.failureId}\u0000${r.toId}`);
  }

  // Corridors.
  const corridors = detectCorridors(slice, idx, outcomes, spineSet);
  const corridorSets = new Map<string, Set<string>>();
  const corridorOfNode = new Map<string, string>();
  for (const c of corridors) {
    corridorSets.set(c.corridorId, new Set(c.nodeIds));
    for (const n of c.nodeIds) corridorOfNode.set(n, c.corridorId);
  }

  // Route bundles.
  const bundles = buildBundles(slice, idx, spineSet, outcomes, protectedIds);

  // Node specs.
  const nodeById = new Map<string, RouteNodeSpec>();
  spine.forEach((id, i) => {
    const n = idx.byId.get(id)!;
    nodeById.set(id, {
      id,
      outcome: outcomes.get(id) ?? "neutral",
      lod: classifySemanticLOD(n, outcomes.get(id) ?? "neutral", protectedIds, true),
      role: "spine",
      spineIndex: i,
    });
  });
  deadEnds.forEach((de) => {
    const n = idx.byId.get(de.terminalId)!;
    const spec = nodeById.get(de.terminalId);
    nodeById.set(de.terminalId, {
      ...(spec ?? {}),
      id: de.terminalId,
      outcome: outcomes.get(de.terminalId) ?? "neutral",
      lod: classifySemanticLOD(n, outcomes.get(de.terminalId) ?? "neutral", protectedIds, false),
      role: "dead-end",
    });
    void (spec);
  });
  for (const bi of branches) {
    for (const id of bi.nodeIds) {
      if (nodeById.has(id)) continue;
      const n = idx.byId.get(id)!;
      const role: RouteNodeSpec["role"] = retries.some((r) => r.toId === id)
        ? "retry"
        : corridorOfNode.has(id)
          ? "convergence"
          : "branch";
      nodeById.set(id, {
        id,
        outcome: outcomes.get(id) ?? "neutral",
        lod: classifySemanticLOD(n, outcomes.get(id) ?? "neutral", protectedIds, false),
        role,
        branchId: bi.branchId,
        corridorId: corridorOfNode.get(id),
      });
    }
  }
  // Nodes consumed by a bundle get bundle role/lod.
  for (const b of bundles) {
    for (const id of b.nodeIds) {
      const n = idx.byId.get(id);
      if (!n) continue;
      nodeById.set(id, {
        id,
        outcome: outcomes.get(id) ?? "neutral",
        lod: "route-bundle",
        role: "bundle",
      });
    }
  }
  // Fallback: any remaining node (convergence sinks not absorbed by a branch,
  // isolated landmarks, etc.) still gets a spec so the layout never drops a
  // canonical node. Roles default by outcome; protected ids stay landmark.
  for (const n of slice.nodes) {
    if (nodeById.has(n.id)) continue;
    const outcome = outcomes.get(n.id) ?? "neutral";
    nodeById.set(n.id, {
      id: n.id,
      outcome,
      lod: classifySemanticLOD(n, outcome, protectedIds, false),
      role: corridorOfNode.has(n.id) ? "convergence" : "branch",
      corridorId: corridorOfNode.get(n.id),
    });
  }

  // Edge specs.
  const edges: RouteEdgeSpec[] = [];
  for (const e of slice.edges) {
    if (!idx.byId.has(e.from) || !idx.byId.has(e.to)) continue;
    const kind = analyzeEdge(
      e,
      spineSet,
      spineOrder,
      retryIds,
      corridorSets,
    );
    const outcome =
      kind === "retry-hairpin"
        ? "exploring"
        : classifyNodeOutcome(e.to, registry) ?? "neutral";
    edges.push({
      id: e.id,
      from: e.from,
      to: e.to,
      relation: e.relation,
      outcome,
      kind,
    });
  }

  const topology: RouteTopology = {
    id: `route:${stableSeed}`,
    rootId: slice.rootId,
    nodeById,
    edges,
    spineNodeIds: spine,
    branches: branches.map((b) => ({ ...b })),
    deadEnds,
    retries,
    corridors,
    bundles,
    registry,
  };
  return topology;
}
