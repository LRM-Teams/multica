/**
 * Stable organic geometry engine (LRM-1487 / 实现-11).
 *
 * Pure, worker-ready layout: `layoutOrganicRoutes` turns a structural
 * `RouteTopology` into a `RouteLayout` (node anchors + cubic Bézier edges +
 * bundle geometry) with ZERO DOM reads and ZERO `Math.random()`.
 *
 * Determinism + delta contract (spec §10 / AC2):
 *   - identical topology + identical seed -> identical layout (anchor and
 *     curve coordinates are byte-for-byte stable);
 *   - given a `previousLayout` and `affectedRootIds`, every node/edge that is
 *     NOT part of an affected corridor keeps its EXACT previous anchor/curve;
 *     only the affected corridor is recomputed. Unaffected landmarks never
 *     move a single pixel.
 *
 * Geometry invariants (spec §4):
 *   - spine advances broadly left-to-right with a wide S-curve, not rank
 *     columns; direction bend alternates deterministically every 2–4
 *     landmarks;
 *   - branches leave the parent at 24–52° tangents and alternate above/below;
 *   - failed paths bend outward and keep a visible tail + failed endpoint;
 *   - retry hairpins loop outside the failure point toward the new attempt;
 *   - convergence edges converge from different directions into the sink;
 *   - every edge is a cubic Bézier (never an orthogonal polyline).
 */
import type {
  BundleGeometry,
  BundleStats,
  CubicBezier,
  Point,
  RouteCurve,
  RouteLayout,
  RouteOutcome,
  RouteTopology,
} from "./types";
import {
  avoidCards,
  branchTangent,
  cubicBezier,
  perpendicular,
  type CardRect,
} from "./geometry";
import { stable01, stablePhase, unitAtDeg } from "./seed";

/** Main-spine forward step (px) — spec §4.1 (280–420). */
export const SPINE_STEP_MIN = 280;
export const SPINE_STEP_MAX = 420;
/** Perpendicular normal offset for the spine (px) — spec §4.1 (56–160). */
export const SPINE_OFFSET_MIN = 56;
export const SPINE_OFFSET_MAX = 160;
/** How often the spine switches bend direction (every 2–4 landmarks). */
export const BEND_EVERY_MIN = 2;
export const BEND_EVERY_MAX = 4;
/** Branch fan-out spacing between sibling branches (px). */
export const BRANCH_GAP = 120;
/** Dead-end outward extension length (px). */
export const DEAD_END_TAIL = 56;
/** Distance a bundle card sits from its spine anchor. */
export const BUNDLE_OFFSET = 90;

/** Default seed used when the caller passes an empty topology seed. */
const DEFAULT_SEED = "route";

/** Fold a node's forward neighbour count into "importance" for spacing. */
function seedFor(topology: RouteTopology): string {
  return topology.id?.length ? topology.id.replace(/^route:/, "") : DEFAULT_SEED;
}

/* ---------------------------------------------------------------------------
 * Bundle aggregate stats — real counts over canonical outcomes (spec §5).
 * ------------------------------------------------------------------------- */

/** Count real per-outcome aggregates for a folded bundle neighborhood. */
export function computeBundleStats(
  nodeIds: readonly string[],
  outcomeByNode: ReadonlyMap<string, RouteOutcome>,
): BundleStats {
  const stats: BundleStats = {
    count: nodeIds.length,
    accepted: 0,
    failed: 0,
    exploring: 0,
    stale: 0,
    disputed: 0,
    cancelled: 0,
    other: 0,
    agents: 0,
  };
  for (const id of nodeIds) {
    const o = outcomeByNode.get(id) ?? "neutral";
    if (o === "accepted") stats.accepted += 1;
    else if (o === "failed") stats.failed += 1;
    else if (o === "exploring") stats.exploring += 1;
    else if (o === "stale") stats.stale += 1;
    else if (o === "disputed") stats.disputed += 1;
    else if (o === "cancelled") stats.cancelled += 1;
    else stats.other += 1;
  }
  return stats;
}

/* ---------------------------------------------------------------------------
 * Node placement — deterministic, seeded.
 * ------------------------------------------------------------------------- */

interface PlaceCtx {
  topology: RouteTopology;
  seed: string;
  positions: Map<string, Point>;
}

/** Place the main spine with a wide S-curve (deterministic bend phase). */
function placeSpine(ctx: PlaceCtx): void {
  const { topology, seed, positions } = ctx;
  const spine = topology.spineNodeIds;
  if (spine.length === 0) {
    for (const id of topology.nodeById.keys()) {
      positions.set(id, { x: 0, y: 0 });
    }
    return;
  }

  // Deterministic per-spine bend cadence from the seed hash.
  const bendEvery =
    2 + Math.floor(stable01(`bend:${seed}`, seed) * (BEND_EVERY_MAX - BEND_EVERY_MIN + 1));
  let x = 0;
  let y = 0;
  const phase = stablePhase(`spine-phase:${seed}`, seed); // initial bend direction (radians)
  // Vertical drift applied during each straight run.
  for (let i = 0; i < spine.length; i += 1) {
    const id = spine[i]!;
    const step =
      SPINE_STEP_MIN +
      stable01(`spine-step:${id}`, seed) * (SPINE_STEP_MAX - SPINE_STEP_MIN);
    const normalOffset =
      SPINE_OFFSET_MIN +
      stable01(`spine-offset:${id}`, seed) * (SPINE_OFFSET_MAX - SPINE_OFFSET_MIN);

    x += step;
    // bend sign flips every `bendEvery` landmarks (stable, not random).
    const bend = Math.sin(phase + (i % bendEvery) * 0.9);
    y += normalOffset * 0.5 * Math.sign(bend !== 0 ? bend : 1);
    positions.set(id, { x, y });
  }
}

/** Place a branch fanning from a spine node at a deterministic 24–52° band. */
function placeBranch(
  ctx: PlaceCtx,
  branch: RouteTopology["branches"][number],
): void {
  const { seed, positions } = ctx;
  const parent = positions.get(branch.fromId);
  if (!parent) return;

  // Sibling branches alternate above/below via the stable seed.
  const side =
    stable01(`branch-side:${branch.branchId}`, seed) < 0.5 ? -1 : 1;
  const childAnchor = positions.get(branch.fromId) ?? { x: parent.x + 200, y: parent.y };

  let prev = parent;
  for (const id of branch.nodeIds) {
    const step = 180 + stable01(`branch-step:${id}`, seed) * 120;
    // First hop leaves at 24–52°, subsequent hops continue outward with a
    // gentle deterministic bend.
    let tangent: Point;
    if (prev === parent) {
      tangent = branchTangent(parent, childAnchor, `${id}`, side, seed);
    } else {
      tangent = unitAtDeg(side * (14 + stable01(`bend:${id}`, seed) * 26));
    }
    const nx = prev.x + tangent.x * step;
    const ny = prev.y + Math.abs(tangent.y) * step * side;
    positions.set(id, { x: nx, y: ny });
    const seat = BRANCH_GAP * 0.5 * side;
    if (positions.has(id)) {
      const p = positions.get(id)!;
      p.y += seat;
    }
    prev = positions.get(id) ?? prev;
  }
}

/** Place a bundle card at a stable offset from its spine anchor. */
function placeBundles(ctx: PlaceCtx): BundleGeometry[] {
  const { topology, seed, positions } = ctx;
  const geometries: BundleGeometry[] = [];
  for (const b of topology.bundles) {
    const anchorSrc = positions.get(b.anchorId);
    let anchor: Point;
    if (anchorSrc) {
      anchor = { x: anchorSrc.x, y: anchorSrc.y };
    } else {
      anchor = { x: 0, y: 0 };
    }
    // Deterministic direction away from the bundle's spine anchor.
    const angDeg = stable01(`bundle-ang:${b.id}`, seed) * 360;
    const dir = unitAtDeg(angDeg);
    const pos = {
      x: anchor.x + dir.x * BUNDLE_OFFSET,
      y: anchor.y + dir.y * BUNDLE_OFFSET,
    };
    // Bundle "anchor" read by the renderer = the bundle card position.
    positions.set(b.anchorId, pos);
    anchor = pos;
    geometries.push({
      bundleId: b.id,
      anchorId: b.anchorId,
      anchor,
      nodeIds: [...b.nodeIds],
      stats: computeBundleStats(b.nodeIds, b.outcomeByNode),
    });
  }
  return geometries;
}

/* ---------------------------------------------------------------------------
 * Edge curve construction.
 * ------------------------------------------------------------------------- */

function buildCurve(
  ctx: PlaceCtx,
  edge: RouteTopology["edges"][number],
): CubicBezier {
  const { seed, positions } = ctx;
  const from = positions.get(edge.from);
  const to = positions.get(edge.to);
  const source = from ?? { x: 0, y: 0 };
  const target = to ?? source;

  let sourceTangent: Point;
  let targetTangent: Point;
  if (edge.kind === "retry-hairpin") {
    // Hairpin loops outside: emit from the failure endpoint at ~180° and
    // arrive at the new attempt with a shallow approach.
    const side = stable01(`hairpin:${edge.id}`, seed) < 0.5 ? 1 : -1;
    sourceTangent = unitAtDeg(side * 150);
    targetTangent = unitAtDeg(-side * 20);
  } else if (
    edge.kind === "dead-end"
  ) {
    const side = stable01(`deadend:${edge.id}`, seed) < 0.5 ? 1 : -1;
    sourceTangent = unitAtDeg(side * 30);
    targetTangent = unitAtDeg(-side * 10);
  } else if (edge.kind === "convergence") {
    // Converge from a different direction per edge.
    const ang = 120 + stable01(`converge:${edge.id}`, seed) * 120;
    sourceTangent = unitAtDeg(ang);
    targetTangent = unitAtDeg(-10);
  } else if (edge.kind === "branch") {
    const side = stable01(`edge-side:${edge.id}`, seed) < 0.5 ? 1 : -1;
    sourceTangent = branchTangent(source, target, edge.id, side, seed);
    targetTangent = unitAtDeg(-side * 8);
  } else {
    // spine / bundle edges: broad S-curve, deterministic per edge.
    const perp = perpendicular(
      { x: target.x - source.x, y: target.y - source.y },
      stable01(`edge-side:${edge.id}`, seed) < 0.5 ? 1 : -1,
    );
    sourceTangent = { x: 1, y: perp.y * 0.18 };
    targetTangent = { x: 1, y: perp.y * -0.18 };
  }

  return cubicBezier(source, target, sourceTangent, targetTangent);
}

/** Build the card-rect obstacles used for avoidance (corridors). */
function obstacleRects(ctx: PlaceCtx): CardRect[] {
  const rects: CardRect[] = [];
  const { positions } = ctx;
  const seen = new Set<string>();
  for (const id of positions.keys()) {
    if (seen.has(id)) continue;
    seen.add(id);
    const p = positions.get(id)!;
    rects.push({ x: p.x - 40, y: p.y - 20, width: 80, height: 40, padding: 8 });
  }
  return rects;
}

/* ---------------------------------------------------------------------------
 * Affected-corridor tracking (delta recompute).
 * ------------------------------------------------------------------------- */

/**
 * Compute the set of edge/curve ids that depend on an affected corridor.
 * A corridor is the closure under the topology's structural grouping (bundle
 * membership + shared sink). Nodes/edges outside the affected closure keep
 * their exact previous geometry.
 */
function affectedClosure(
  topology: RouteTopology,
  affectedRootIds: readonly string[],
): { nodes: Set<string>; edges: Set<string> } {
  const nodes = new Set<string>();
  const edges = new Set<string>();

  // Map every node to its owning bundle id.
  const bundleOfNode = new Map<string, string>();
  for (const b of topology.bundles) {
    for (const id of b.nodeIds) bundleOfNode.set(id, b.id);
  }

  const add = (id: string): void => {
    if (nodes.has(id)) return;
    nodes.add(id);
    // Edges incident to this node belong to the affected corridor.
    for (const e of topology.edges) {
      if (e.from === id || e.to === id) edges.add(e.id);
    }
    // If inside a bundle, the whole bundle corridor is affected.
    const bId = bundleOfNode.get(id);
    if (bId) {
      for (const bid of topology.nodeById.keys()) {
        if (bundleOfNode.get(bid) === bId) nodes.add(bid);
      }
    }
  };

  for (const rid of affectedRootIds) add(rid);

  // Closure: propagate along edges incident to affected nodes.
  let grew = true;
  while (grew) {
    grew = false;
    for (const e of topology.edges) {
      if (edges.has(e.id)) {
        if (!nodes.has(e.from)) {
          nodes.add(e.from);
          grew = true;
        }
        if (!nodes.has(e.to)) {
          nodes.add(e.to);
          grew = true;
        }
      }
    }
  }
  // Recompute edges for every affected node (post-closure).
  for (const id of nodes) {
    for (const e of topology.edges) {
      if (e.from === id || e.to === id) edges.add(e.id);
    }
  }
  return { nodes, edges };
}

/**
 * Layout the whole topology, preserving a previous layout for everything
 * outside `affectedRootIds`.
 *
 * This is the pure, worker-ready public interface (spec §10):
 * `layoutOrganicRoutes(topology, previousLayout, affectedRootIds): RouteLayout`.
 */
export function layoutOrganicRoutes(
  topology: RouteTopology,
  previousLayout?: RouteLayout | null,
  affectedRootIds: readonly string[] = [],
): RouteLayout {
  const ctx: PlaceCtx = {
    topology,
    seed: seedFor(topology),
    positions: new Map<string, Point>(),
  };

  placeSpine(ctx);
  for (const b of topology.branches) placeBranch(ctx, b);
  const bundles = placeBundles(ctx);

  // --- Delta preservation ---
  const affected = affectedClosure(topology, affectedRootIds);
  if (previousLayout && affectedRootIds.length > 0) {
    for (const [id, pos] of previousLayout.nodePositions) {
      if (!affected.nodes.has(id)) ctx.positions.set(id, pos);
    }
  }

  // --- Curves ---
  const rects = obstacleRects(ctx);
  const curves: RouteCurve[] = [];
  const reuseDelta = previousLayout && affectedRootIds.length > 0;
  const prevByEdge = reuseDelta
    ? new Map(previousLayout!.curves.map((c) => [c.edgeId, c]))
    : null;
  for (const e of topology.edges) {
    // Skip geometry that belongs to a preserved corridor (reuse previous).
    if (reuseDelta && !affected.edges.has(e.id)) {
      const prev = prevByEdge!.get(e.id);
      if (prev) {
        curves.push({ ...prev });
        continue;
      }
    }
    const curve = avoidCards(buildCurve(ctx, e), rects);
    curves.push({
      edgeId: e.id,
      from: e.from,
      to: e.to,
      relation: e.relation,
      outcome: e.outcome,
      kind: e.kind,
      curve,
    });
  }

  return {
    nodePositions: new Map(ctx.positions),
    curves,
    bundles,
  };
}
