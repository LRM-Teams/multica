/**
 * Pure, worker-ready types for the organic route topology & geometry engine
 * (LRM-1487 / 实现-11).
 *
 * This module describes the SEMANTIC structure of a research "route map" and
 * its deterministic layout, independent of any renderer. The renderer layer
 * (canvas plugins / node renderers / motion) consumes these types only; it
 * never authors canonical graph facts and never mutates the geometry.
 *
 * Two layers are kept strictly separate:
 *   - `RouteTopology` — structural / semantic: spine, branches, dead-ends,
 *     retries, corridors and Route Bundles, plus the read-only outcome
 *     registry. No coordinates.
 *   - `RouteLayout` — pure geometry: per-node anchor positions + per-edge
 *     stable cubic Bézier curves. Replaying an identical `RouteTopology`
 *     yields an identical `RouteLayout` (determinism), and a Delta only
 *     recomputes the affected corridor (scoped recompute).
 */

/** A 2D point in layout/canvas coordinates. */
export interface Point {
  x: number;
  y: number;
}

/**
 * A stable cubic Bézier segment. Four control points, never an orthogonal
 * polyline. `p0`/`p3` are the source/target ports; `p1`/`p2` are derived from
 * the endpoint tangents (see geometry module).
 */
export interface CubicBezier {
  p0: Point;
  p1: Point;
  p2: Point;
  p3: Point;
}

/** Distinct visual morphemes supported by the route layer (~§3 of spec). */
export type RouteOutcome =
  | "exploring"
  | "accepted"
  | "failed"
  | "cancelled"
  | "stale"
  | "disputed"
  | "neutral";

/** Semantic LOD bucket for a canonical node (~§2 of spec). */
export type SemanticLOD = "landmark" | "waypoint" | "trail-dot" | "route-bundle";

/** Structural role a node/edge plays inside the route map (~§4 of spec). */
export type RouteRole =
  | "spine"
  | "branch"
  | "dead-end"
  | "retry"
  | "convergence"
  | "bundle";

/** Edge visual kind carried through to the renderer. */
export type RouteEdgeKind =
  | "spine"
  | "branch"
  | "dead-end"
  | "retry-hairpin"
  | "convergence"
  | "bundle";

/** Structural info for one canonical node in the route map. */
export interface RouteNodeSpec {
  /** Canonical `CanvasNode.id`. */
  id: string;
  /** Read-only node/attempt outcome (never inferred from text). */
  outcome: RouteOutcome;
  /** Budget-aware semantic LOD (classifySemanticLOD). */
  lod: SemanticLOD;
  role: RouteRole;
  /** For `role: "branch"` — owning branch id. */
  branchId?: string;
  /** For `role: "spine"` — 0-based index along the main spine. */
  spineIndex?: number;
  /** For `role: "convergence"` — owning corridor id. */
  corridorId?: string;
}

/** Structural info for one edge in the route map. */
export interface RouteEdgeSpec {
  /** Canonical `CanvasEdge.id`. */
  id: string;
  from: string;
  to: string;
  /** Verbatim edge relation from the projection. */
  relation: string;
  /** Read-only edge/relation outcome. */
  outcome: RouteOutcome;
  kind: RouteEdgeKind;
}

/**
 * A Route Bundle: a compressed, honest aggregate of consecutive low-LOD nodes
 * (~§5 of spec). Counts are real aggregates over canonical nodes — never
 * fabricated conclusions.
 */
export interface RouteBundle {
  id: string;
  /** Anchor node id the bundle joins the map from. */
  anchorId: string;
  /** Node ids folded into this bundle. */
  nodeIds: string[];
  outcomeByNode: ReadonlyMap<string, RouteOutcome>;
  /** Showcase micro-dots (representative node ids, ≤12). */
  representativeIds: string[];
}

/** Per-bundle aggregate counters, derived from real node outcomes. */
export interface BundleStats {
  count: number;
  accepted: number;
  failed: number;
  exploring: number;
  stale: number;
  disputed: number;
  cancelled: number;
  other: number;
  agents: number;
}

/**
 * Structural route map. Pure — a function only of (slice, protectedIds, seed).
 */
export interface RouteTopology {
  id: string;
  rootId: string;
  nodeById: ReadonlyMap<string, RouteNodeSpec>;
  edges: RouteEdgeSpec[];
  /** Ordered canonical node ids of the main spine. */
  spineNodeIds: string[];
  /** Each exploration branch: parent spine node + ordered child ids. */
  branches: { branchId: string; fromId: string; nodeIds: string[] }[];
  /** Failed terminals (dead-ends): the failed tail kept visible. */
  deadEnds: { nodeIds: string[]; terminalId: string; fromId: string }[];
  /** Retry hairpins: old failure id → new attempt id continued from parent. */
  retries: { retryId: string; fromId: string; failureId: string; toId: string }[];
  /** Convergence corridors feeding into an Insight/Decision. */
  corridors: { corridorId: string; nodeIds: string[]; intoId: string }[];
  /** Compressed aggregates for over-budget / deep neighborhoods. */
  bundles: RouteBundle[];
  /** Public read-only outcome registry (status + relation). */
  registry: OutcomeRegistry;
}

/**
 * Read-only outcome registry. Verbatim projection fields only — build it from
 * `CanvasSlice` once; never derive semantics from prose afterwards.
 */
export interface OutcomeRegistry {
  /** nodeId → verbatim node status string. */
  nodeStatus: ReadonlyMap<string, string>;
  /** nodeId → attempt phase/status lifted from node payload (when present). */
  attemptStatus: ReadonlyMap<string, string>;
  /** edgeId → verbatim relation string. */
  relationByEdge: ReadonlyMap<string, string>;
}

/** One laid-out edge: canonical edge id + stable cubic geometry + outcome. */
export interface RouteCurve {
  edgeId: string;
  from: string;
  to: string;
  relation: string;
  outcome: RouteOutcome;
  kind: RouteEdgeKind;
  curve: CubicBezier;
}

/** Laid-out bundle geometry: an anchor position the bundle card sits at. */
export interface BundleGeometry {
  bundleId: string;
  anchorId: string;
  anchor: Point;
  nodeIds: string[];
  stats: BundleStats;
}

/**
 * Pure layout result. Every node has a position; every edge has a cubic
 * Bézier. Replaying an identical topology produces an identical layout.
 */
export interface RouteLayout {
  /** canonical node id → anchor position. */
  nodePositions: ReadonlyMap<string, Point>;
  /** edge id → curve geometry (dropped for tombstoned/hidden edges). */
  curves: RouteCurve[];
  bundles: BundleGeometry[];
}
