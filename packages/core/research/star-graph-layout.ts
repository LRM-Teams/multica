/**
 * LRM-1514 — D5 调研星图 稳定增量布局引擎（核心几何层）.
 *
 * A pure, deterministic, incremental layout engine for the D5 five-tier star
 * graph. It turns a flat list of tiered nodes + relations into concrete
 * circle positions, cluster boundaries and edge endpoints — WITHOUT fixed
 * example coordinates and WITHOUT any React / canvas dependency.
 *
 * Spatial semantics implemented (D5 spec):
 *   - The canonical goal is the origin; an XXL synthesis remains a distinct
 *     destination instead of being collapsed into the same visual role.
 *   - Branch landmarks plus unparented Work/Result points form a directional
 *     field to the right of the goal, grouped into contiguous angular sectors
 *     by `clusterId`.
 *   - S is a visual tier, not an orbit instruction. Explicit Agent satellites
 *     and parented Work orbit locally; unparented Work/Result nodes advance
 *     through the same directional field as the rest of their branch.
 *   - New / unrelated directions sit outside existing clusters and only link
 *     back to the goal via `challenge` / `newdir` relation semantics.
 *
 * Quantitative hard gates target:
 *   - node-circle collision = 0
 *   - core label collision      = 0
 *   - edge endpoint-to-circle error <= 2px
 *   - cluster boundary contains every member + label
 *   - refresh stability (same input -> same output; incremental runs keep
 *     unaffected nodes in place)
 *
 * Design properties:
 *   - PURE: no side effects, no Date.now(), no global randomness. All RNG goes
 *     through a seeded barrier (`mulberry32`), so identical input + seed always
 *     yields identical output.
 *   - INCREMENTAL: pass `options.previous` and the engine reuses the position
 *     of every node whose cluster membership and parent are unchanged. Only the
 *     affected region moves.
 *   - DETERMINISTIC: fixed-order input sorting and fixed iteration counts; no
 *     ordering decision depends on float noise.
 *
 * Framework-agnostic: a higher layer (canvas / adapter) maps real data into
 * `StarGraphLayoutNode` / relations and maps the result back to positions.
 */

/** The five D5 tiers (matches `@multica/ui/components/star-graph` tier order). */
export type StarGraphLayoutTier = "xxl" | "xl" | "l" | "m" | "s";

/** Fixed exact circle radius (px) for each tier — single source of geometry. */
export const STAR_GRAPH_RADIUS: Record<StarGraphLayoutTier, number> = {
  xxl: 124, // 248px diameter
  xl: 110, // 220px
  l: 84, // 168px
  m: 48, // 96px
  s: 29, // 58px
};

/** Label box model (half extents). Core labels never spill the circle. */
export interface StarGraphLabelBox {
  /** Half width of the label text region, px. */
  halfWidth: number;
  /** Half height of the label text region, px. */
  halfHeight: number;
}

/** Default label box for a node when no explicit geometry is given. */
export function defaultLabelBox(tier: StarGraphLayoutTier): StarGraphLabelBox {
  const r = STAR_GRAPH_RADIUS[tier];
  return { halfWidth: r * 0.6, halfHeight: r * 0.28 };
}

/** A node the layout engine understands. */
export interface StarGraphLayoutNode {
  id: string;
  tier: StarGraphLayoutTier;
  /** Optional semantic radius override. The Goal origin uses a compact 118px disc. */
  radius?: number;
  /** Canonical backend node type. Used only for spatial semantics. */
  nodeKind?: string | null;
  /** Cluster (theme/成果) grouping key. Null = unclustered / free direction. */
  clusterId?: string | null;
  /** Parent result id for S-tier exploration orbit. Other tiers ignore this. */
  parentId?: string | null;
  /** Optional custom label box; defaults to `defaultLabelBox(tier)`. */
  label?: StarGraphLabelBox;
  /** Kept verbatim through the layout (opaque passthrough for callers). */
  ref?: unknown;
}

/** A typed relation between two nodes. Order is significant (source -> target). */
export interface StarGraphLayoutRelation {
  id: string;
  fromNodeId: string;
  toNodeId: string;
  /** decompose | support | challenge | newdir. */
  kind: "decompose" | "support" | "challenge" | "newdir";
}

/** Result geometry for one node. */
export interface StarGraphLayoutNodePosition {
  id: string;
  tier: StarGraphLayoutTier;
  /** Circle centre x, px (axis origin = goal centre). */
  x: number;
  /** Circle centre y, px. */
  y: number;
  radius: number;
  label: StarGraphLabelBox;
  /** Cluster key for grouping; null = ungrouped. */
  clusterId: string | null;
  /** Angle (radians) of the node around its anchor (goal or S-orbit parent). */
  angle: number;
  /** Radial distance from anchor centre (px). */
  radiusOffset: number;
  parentId: string | null;
}

/** Positioned edge with endpoints snapped onto each circle's perimeter. */
export interface StarGraphLayoutEdge {
  id: string;
  fromNodeId: string;
  toNodeId: string;
  kind: StarGraphLayoutRelation["kind"];
  from: { x: number; y: number };
  to: { x: number; y: number };
}

/** A cluster boundary circle that contains all members + their labels. */
export interface StarGraphLayoutCluster {
  clusterId: string;
  x: number;
  y: number;
  radius: number;
  /** Elliptical presentation footprint; radius remains the containment contract. */
  width?: number;
  height?: number;
  memberIds: string[];
}

/** Dashed territory enclosing canonical `newdir` relation targets. */
export interface StarGraphLayoutFrontier {
  id: string;
  x: number;
  y: number;
  width: number;
  height: number;
  memberIds: string[];
}

export interface StarGraphLayoutOptions {
  /**
   * Previous layout result for incremental stability. Nodes whose cluster and
   * parent are unchanged keep their previous position.
   */
  previous?: StarGraphLayoutResult;
  /** PRNG seed — same (nodes, seed, version) -> same output. Default 1. */
  seed?: number;
  /** Stable layout version for determinism bookkeeping. Default "d5-1". */
  version?: string;
  /** Extra padding between node circles after collision resolution (px). */
  padding?: number;
}

export interface StarGraphLayoutResult {
  nodes: StarGraphLayoutNodePosition[];
  edges: StarGraphLayoutEdge[];
  clusters: StarGraphLayoutCluster[];
  frontiers?: StarGraphLayoutFrontier[];
  /** Goal (origin) node id, if any. */
  rootId: string | null;
  /** The stable layout version that produced this geometry. */
  version: string;
  /** Bookkeeping: how many nodes re-used the previous position vs moved. */
  stats: { reused: number; moved: number; total: number };
  /** Node id -> stable signature for incremental reuse. */
  keyByNode: Map<string, string>;
}

/* ------------------------------------------------------------------ *
 * Deterministic PRNG (mulberry32) — no global randomness.
 * ------------------------------------------------------------------ */

function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

function hashStr(input: string): number {
  let h = 0x811c9dc5;
  for (let i = 0; i < input.length; i += 1) {
    h ^= input.charCodeAt(i);
    h = Math.imul(h, 0x01000193);
  }
  return h >>> 0;
}

/* ------------------------------------------------------------------ *
 * Geometry helpers.
 * ------------------------------------------------------------------ */

function dist(ax: number, ay: number, bx: number, by: number): number {
  const dx = ax - bx;
  const dy = ay - by;
  return Math.sqrt(dx * dx + dy * dy);
}

/** Edge endpoints snapped exactly onto each circle's perimeter. */
export function circleEdgeEndpoints(
  ax: number, ay: number, ar: number,
  bx: number, by: number, br: number,
): { from: { x: number; y: number }; to: { x: number; y: number } } {
  const dxb = bx - ax;
  const dyb = by - ay;
  const d = Math.sqrt(dxb * dxb + dyb * dyb);
  if (d === 0) {
    return {
      from: { x: ax, y: ay - ar },
      to: { x: bx + br, y: by },
    };
  }
  const ux = dxb / d;
  const uy = dyb / d;
  return {
    from: { x: ax + ux * ar, y: ay + uy * ar },
    to: { x: bx - ux * br, y: by - uy * br },
  };
}

function circleEdgeError(
  cx: number, cy: number, r: number,
  px: number, py: number,
): number {
  return Math.abs(dist(cx, cy, px, py) - r);
}

/** Axis-aligned label rectangles must not intersect. */
export function labelBoxesOverlap(
  ax: number, ay: number, ahw: number, ahh: number,
  bx: number, by: number, bhw: number, bhh: number,
): boolean {
  return (
    Math.abs(ax - bx) < ahw + bhw &&
    Math.abs(ay - by) < ahh + bhh
  );
}

/* ------------------------------------------------------------------ *
 * Diagnostics (quantitative hard-gate report).
 * ------------------------------------------------------------------ */

export interface StarGraphLayoutIssue {
  kind:
    | "circle_collision"
    | "label_collision"
    | "endpoint_error"
    | "cluster_containment"
    | "root_occlusion";
  aId: string;
  bId: string;
  detail: number;
}

export interface StarGraphDiagnosticReport {
  nodeCollisions: number;
  labelCollisions: number;
  maxEndpointError: number;
  clusterContainmentFailures: number;
  /** Max distance any node moved vs `previous` (px); null when no previous. */
  maxDisplacement: number | null;
  hasRootOcclusion: boolean;
  issues: StarGraphLayoutIssue[];
}

/** Public gate evaluator. Used by tests and a future diagnostics overlay. */
export function diagnoseStarGraphLayout(
  result: StarGraphLayoutResult,
  options: { padding?: number; previous?: StarGraphLayoutResult } = {},
): StarGraphDiagnosticReport {
  const padding = options.padding ?? 0;
  const issues: StarGraphLayoutIssue[] = [];
  const byPos = new Map(result.nodes.map((n) => [n.id, n]));

  for (let i = 0; i < result.nodes.length; i += 1) {
    for (let j = i + 1; j < result.nodes.length; j += 1) {
      const a = result.nodes[i]!;
      const b = result.nodes[j]!;
      const dx = a.x - b.x;
      const dy = a.y - b.y;
      const d = Math.sqrt(dx * dx + dy * dy);
      const overlap = a.radius + b.radius + padding - d;
      if (overlap > 0.5) {
        issues.push({ kind: "circle_collision", aId: a.id, bId: b.id, detail: overlap });
      }
      if (labelBoxesOverlap(
        a.x, a.y, a.label.halfWidth, a.label.halfHeight,
        b.x, b.y, b.label.halfWidth, b.label.halfHeight,
      )) {
        issues.push({ kind: "label_collision", aId: a.id, bId: b.id, detail: 0 });
      }
    }
  }

  let maxEndpointError = 0;
  for (const e of result.edges) {
    const from = byPos.get(e.fromNodeId);
    const to = byPos.get(e.toNodeId);
    if (!from || !to) continue;
    const err = Math.max(
      circleEdgeError(from.x, from.y, from.radius, e.from.x, e.from.y),
      circleEdgeError(to.x, to.y, to.radius, e.to.x, e.to.y),
    );
    if (err > maxEndpointError) maxEndpointError = err;
    if (err > 2) {
      issues.push({ kind: "endpoint_error", aId: e.id, bId: e.id, detail: err });
    }
  }

  let clusterContainmentFailures = 0;
  for (const cluster of result.clusters) {
    for (const memberId of cluster.memberIds) {
      const node = byPos.get(memberId);
      if (!node) continue;
      const d = dist(cluster.x, cluster.y, node.x, node.y);
      const need = node.radius + node.label.halfWidth * 0.5 + 8;
      if (d > cluster.radius - need) {
        clusterContainmentFailures += 1;
      }
    }
  }

  let hasRootOcclusion = false;
  const rootId = result.rootId;
  const root = rootId != null ? byPos.get(rootId) : undefined;
  if (root) {
    for (const n of result.nodes) {
      if (n.id === root.id) continue;
      if (dist(n.x, n.y, root.x, root.y) < n.radius + root.radius) {
        hasRootOcclusion = true;
        issues.push({ kind: "root_occlusion", aId: n.id, bId: root.id, detail: 0 });
      }
    }
  }

  let maxDisplacement: number | null = null;
  if (options.previous) {
    const prevMap = new Map(options.previous.nodes.map((n) => [n.id, n]));
    for (const n of result.nodes) {
      const p = prevMap.get(n.id);
      if (p) {
        const d = dist(n.x, n.y, p.x, p.y);
        if (maxDisplacement == null || d > maxDisplacement) maxDisplacement = d;
      }
    }
  }

  return {
    nodeCollisions: issues.filter((i) => i.kind === "circle_collision").length,
    labelCollisions: issues.filter((i) => i.kind === "label_collision").length,
    maxEndpointError,
    clusterContainmentFailures,
    maxDisplacement,
    hasRootOcclusion,
    issues,
  };
}

/* ------------------------------------------------------------------ *
 * Core engine.
 * ------------------------------------------------------------------ */

interface EngineNode extends StarGraphLayoutNode {
  label: StarGraphLabelBox;
}

function nodeRadius(node: Pick<StarGraphLayoutNode, "tier" | "radius">): number {
  return node.radius ?? STAR_GRAPH_RADIUS[node.tier];
}

function nodeSignature(node: StarGraphLayoutNode): string {
  return [
    node.id,
    node.tier,
    node.clusterId ?? "",
    node.parentId ?? "",
    nodeRadius(node),
    node.nodeKind ?? "",
  ].join("|");
}

function usesLocalOrbit(node: StarGraphLayoutNode): boolean {
  if (node.tier !== "s") return false;
  const kind = node.nodeKind?.trim().toLowerCase() ?? "";
  if (kind === "agent" || kind === "agent_activity") return true;
  if (kind === "work_s") return node.parentId != null;
  // Geometry-only callers can still declare an explicit local orbit without
  // importing Research node kinds into this framework-agnostic engine.
  return kind === "" && node.parentId != null;
}

/**
 * Adaptive S-orbit ring radius: large enough to hold `count` agent circles of
 * diameter `2*S_RADIUS` without the agents colliding on the ring, never below
 * the natural ring that clears the parent disc.
 */
function orbitRadiusFor(
  parentRadius: number,
  count: number,
  sRadius: number = STAR_GRAPH_RADIUS.s,
  gap = 12,
): number {
  const minRing = parentRadius + sRadius + 14;
  if (count <= 1) return minRing;
  const need = ((2 * sRadius + gap) * count) / (2 * Math.PI);
  return Math.max(minRing, need);
}

export function layoutStarGraph(
  input: readonly StarGraphLayoutNode[],
  relations: readonly StarGraphLayoutRelation[] = [],
  options: StarGraphLayoutOptions = {},
): StarGraphLayoutResult {
  const seed = options.seed ?? 1;
  const version = options.version ?? "d5-1";
  const padding = options.padding ?? 10;

  if (input.length === 0) {
    return {
      nodes: [],
      edges: [],
      clusters: [],
      frontiers: [],
      rootId: null,
      version,
      keyByNode: new Map(),
      stats: { reused: 0, moved: 0, total: 0 },
    };
  }

  const nodes: EngineNode[] = input.map((n) => ({
    ...n,
    label: n.label ?? defaultLabelBox(n.tier),
  }));
  nodes.sort((a, b) => a.id.localeCompare(b.id));
  const total = nodes.length;

  const root =
    nodes.find((n) => n.nodeKind?.toLowerCase() === "goal") ??
    nodes.find((n) => n.tier === "xxl") ??
    nodes.find((n) => n.clusterId == null && n.parentId == null) ??
    nodes[0]!;
  const rootId = root.id;

  const orbit = nodes.filter(usesLocalOrbit);
  const orbitIds = new Set(orbit.map((node) => node.id));
  const stable = nodes.filter(
    (node) => node.id !== rootId && !orbitIds.has(node.id),
  );

  /* ---- Incremental reuse: same signature + version -> keep previous pos. ---- */
  const prevUsable =
    options.previous != null && options.previous.version === version;
  const previousPos = prevUsable
    ? new Map(options.previous!.nodes.map((n) => [n.id, n]))
    : new Map<string, StarGraphLayoutNodePosition>();

  /* Deterministic per-node signatures for incremental reuse decisions. */
  const signatureNow = new Map<string, string>();
  for (const node of nodes) signatureNow.set(node.id, nodeSignature(node));

  /* Seeded RNG for deterministic placement variety (S-orbit jitter). */
  const rng = mulberry32((seed ^ hashStr(version)) >>> 0);

  /* (x, y) by node id. */
  const pos = new Map<string, { x: number; y: number }>();
  const angleOf = new Map<string, number>();
  const offsetOf = new Map<string, number>();

  /* ---- Group stable nodes into clusters (theme/成果 sectors). ---- */
  const byCluster = new Map<string, EngineNode[]>();
  const unclusteredWork: EngineNode[] = [];
  const freeStable: EngineNode[] = [];
  for (const n of stable) {
    if (n.clusterId == null || n.clusterId === "") {
      if (n.nodeKind?.trim().toLowerCase() === "work_s") {
        unclusteredWork.push(n);
      } else {
        freeStable.push(n);
      }
    } else {
      const list = byCluster.get(n.clusterId) ?? [];
      list.push(n);
      byCluster.set(n.clusterId, list);
    }
  }
  // Work S can be the first visible content in a branch. Include its explicit
  // branch scope even before a stable M+ result exists, otherwise every early
  // Work S falls back to the Goal orbit and the canvas loses its branch shape.
  const clusterKeys = [
    ...new Set(
      nodes
        .filter((node) => node.id !== rootId && node.clusterId)
        .map((node) => node.clusterId as string),
    ),
  ].sort();
  type StableGroup = string | "__work__" | "__free__";
  const orderedGroups: StableGroup[] = [
    ...clusterKeys,
    ...(unclusteredWork.length > 0 ? (["__work__"] as const) : []),
    ...(freeStable.length > 0 ? (["__free__"] as const) : []),
  ];
  const sectorCount = orderedGroups.length;

  /* ---- Angular sector per cluster group; deterministic (sorted keys). ---- */
  const sectorStart = new Map<StableGroup, number>();
  const sectorSpan = new Map<StableGroup, number>();
  {
    const hasCanonicalOrigin = root.nodeKind?.toLowerCase() === "goal";
    const gap = hasCanonicalOrigin ? 0.16 : 0.22;
    // A goal-led graph reads left-to-right like the D5 exploration narrative.
    // Legacy/non-goal inputs retain the full radial field.
    const fieldSpan = hasCanonicalOrigin ? Math.PI * 0.78 : 2 * Math.PI;
    const usable = fieldSpan - gap * Math.max(0, sectorCount - 1);
    const slot = sectorCount > 0 ? usable / sectorCount : 0;
    // Rotate the whole field by a quarter turn. With sparse data this keeps
    // two stable results on the left/right axis and three in a broad triangle
    // instead of collapsing the first impression into a vertical chain.
    let cursor = hasCanonicalOrigin ? -fieldSpan / 2 : -Math.PI / 2;
    for (const g of orderedGroups) {
      sectorStart.set(g, cursor + gap / 2);
      sectorSpan.set(g, slot);
      cursor += slot + gap;
    }
  }

  /* ---- Radial band: place each cluster's members outward from the root. ---- */
  // Each stable node gets an angle inside its group sector and a radial offset
  // that grows so big tiers sit further out and clear the root's own disc.
  const rootRadius = nodeRadius(root);
  const hasCanonicalOrigin = root.nodeKind?.toLowerCase() === "goal";
  const reused = new Set<string>();

  // The reference constellation is intentionally not an equal-radius wheel.
  // Branches advance through alternating depth bands, producing short local
  // edges, medium branch edges, and long cross-field synthesis edges.
  const directionalDepthFor = (group: StableGroup) => {
    if (group === "__work__") return 40;
    const index = Math.max(0, orderedGroups.indexOf(group));
    const depthPattern = [0, 220, 430, 110, 330, 560] as const;
    return depthPattern[index % depthPattern.length]! +
      Math.floor(index / depthPattern.length) * 240;
  };

  const placeStableCluster = (
    nodesInGroup: EngineNode[],
    group: StableGroup,
  ) => {
    const start = sectorStart.get(group) ?? 0;
    const span = sectorSpan.get(group) ?? 0;
    // Sort members largest-first so big nodes anchor the innermost radius.
    const members = [...nodesInGroup].sort(
      (a, b) =>
        nodeRadius(b) - nodeRadius(a) ||
        a.id.localeCompare(b.id),
    );
    const firstRadius = nodeRadius(members[0]!);
    const baseRadial = hasCanonicalOrigin
      ? rootRadius + firstRadius + 300 + directionalDepthFor(group)
      : rootRadius + members[0]!.label.halfWidth * 0.5 + 46;
    const largestDiameter = Math.max(
      ...members.map((member) => nodeRadius(member) * 2),
    );
    const usableArc = Math.max(baseRadial * span * 0.86, largestDiameter);
    const layerCapacity = Math.max(
      2,
      Math.min(
        6,
        Math.floor(usableArc / (largestDiameter + padding + 14)),
      ),
    );
    const layerStep = Math.max(92, largestDiameter + padding + 24);
    const depthCadence = [0, 42, 14, 58, 26, 48] as const;
    for (let i = 0; i < members.length; i += 1) {
      const n = members[i]!;
      // Reuse previous position when signature is stable.
      const sig = signatureNow.get(n.id)!;
      const prev = previousPos.get(n.id);
      if (prev && nodeSignatureCompat(prev, sig)) {
        pos.set(n.id, { x: prev.x, y: prev.y });
        angleOf.set(n.id, prev.angle);
        offsetOf.set(n.id, prev.radiusOffset);
        reused.add(n.id);
        continue;
      }
      const layer = Math.floor(i / layerCapacity);
      const layerStart = layer * layerCapacity;
      const nodesInLayer = Math.min(
        layerCapacity,
        members.length - layerStart,
      );
      const positionInLayer = i - layerStart;
      const orderedPosition =
        layer % 2 === 0
          ? positionInLayer
          : nodesInLayer - positionInLayer - 1;
      const fraction =
        nodesInLayer === 1
          ? 0.5
          : (orderedPosition + 0.5) / nodesInLayer;
      const layerLean =
        ((layer % 3) - 1) * Math.min(span * 0.055, 0.045);
      const angle = start + fraction * span + layerLean;
      const radial =
        baseRadial +
        layer * layerStep +
        depthCadence[orderedPosition % depthCadence.length]!;
      pos.set(n.id, { x: Math.cos(angle) * radial, y: Math.sin(angle) * radial });
      angleOf.set(n.id, angle);
      offsetOf.set(n.id, radial);
    }
  };

  for (const key of clusterKeys) {
    const members = byCluster.get(key) ?? [];
    if (members.length > 0) placeStableCluster(members, key);
  }
  if (freeStable.length > 0) {
    placeStableCluster(freeStable, "__free__");
  }
  if (unclusteredWork.length > 0) {
    placeStableCluster(unclusteredWork, "__work__");
  }

  /* ---- Explicit local satellites: orbit their parent result. ---- */
  const clusterCenters = new Map<string, { x: number; y: number }>();
  const clusterAnchorRadii = new Map<string, number>();
  for (const key of clusterKeys) {
    const stableMembers = byCluster.get(key) ?? [];
    if (stableMembers.length > 0) {
      const center = stableMembers.reduce(
        (sum, member) => {
          const memberPosition = pos.get(member.id)!;
          return {
            x: sum.x + memberPosition.x,
            y: sum.y + memberPosition.y,
          };
        },
        { x: 0, y: 0 },
      );
      center.x /= stableMembers.length;
      center.y /= stableMembers.length;
      clusterCenters.set(key, center);
      clusterAnchorRadii.set(
        key,
        Math.max(
          ...stableMembers.map((member) => {
            const memberPosition = pos.get(member.id)!;
            return (
              dist(center.x, center.y, memberPosition.x, memberPosition.y) +
              nodeRadius(member)
            );
          }),
        ),
      );
      continue;
    }
    const start = sectorStart.get(key) ?? 0;
    const span = sectorSpan.get(key) ?? 0;
    const angle = start + span / 2;
    const radial =
      rootRadius + STAR_GRAPH_RADIUS.m + 300 + directionalDepthFor(key);
    clusterCenters.set(key, {
      x: Math.cos(angle) * radial,
      y: Math.sin(angle) * radial,
    });
    clusterAnchorRadii.set(key, STAR_GRAPH_RADIUS.m);
  }

  const virtualParentKey = (clusterId: string) => `cluster:${clusterId}`;
  const sByParent = new Map<string, EngineNode[]>();
  for (const n of orbit) {
    const parentId =
      n.parentId ??
      (n.clusterId ? virtualParentKey(n.clusterId) : rootId);
    const list = sByParent.get(parentId) ?? [];
    list.push(n);
    sByParent.set(parentId, list);
  }
  for (const [parentId, children] of sByParent) {
    const clusterId = parentId.startsWith("cluster:")
      ? parentId.slice("cluster:".length)
      : null;
    const parentCenter =
      pos.get(parentId) ??
      (clusterId ? clusterCenters.get(clusterId) : undefined) ??
      { x: 0, y: 0 };
    const parent = nodes.find((n) => n.id === parentId);
    const parentRadius = parent
      ? nodeRadius(parent)
      : clusterId
        ? (clusterAnchorRadii.get(clusterId) ?? STAR_GRAPH_RADIUS.m)
        : STAR_GRAPH_RADIUS.m;
    const childrenSorted = [...children].sort((a, b) => a.id.localeCompare(b.id));
    // Adaptive ring: wide enough that all children sit on the ring without
    // colliding, so the exploration orbit is dense but collision-free.
    const orbitRadius = orbitRadiusFor(parentRadius, childrenSorted.length);
    const step =
      childrenSorted.length > 1 ? (2 * Math.PI) / childrenSorted.length : 0;
    const startOff = rng();
    childrenSorted.forEach((child, i) => {
      const prev = previousPos.get(child.id);
      const sig = signatureNow.get(child.id)!;
      if (prev && nodeSignatureCompat(prev, sig)) {
        pos.set(child.id, { x: prev.x, y: prev.y });
        angleOf.set(child.id, prev.angle);
        offsetOf.set(child.id, prev.radiusOffset);
        reused.add(child.id);
        return;
      }
      const angle = startOff * 2 * Math.PI + step * i;
      const px = parentCenter.x + Math.cos(angle) * orbitRadius;
      const py = parentCenter.y + Math.sin(angle) * orbitRadius;
      pos.set(child.id, { x: px, y: py });
      angleOf.set(child.id, angle);
      offsetOf.set(child.id, orbitRadius);
    });
  }

  /* ---- Root pinned. ---- */
  pos.set(rootId, { x: 0, y: 0 });
  angleOf.set(rootId, 0);
  offsetOf.set(rootId, 0);

  /* ---- Collision relaxation (deterministic, fixed iterations). ---- */
  const allOrdered = [root, ...nodes.filter((n) => n.id !== rootId)].sort((a, b) =>
    a.id.localeCompare(b.id),
  );
  // Settle residual overlaps at the 220-node product cap while keeping the
  // relaxation deterministic and strictly bounded.
  const ITERATIONS = 64;
  for (let iter = 0; iter < ITERATIONS; iter += 1) {
    for (let i = 0; i < allOrdered.length; i += 1) {
      const a = allOrdered[i]!;
      for (let j = i + 1; j < allOrdered.length; j += 1) {
        const b = allOrdered[j]!;
        const pa = pos.get(a.id)!;
        const pb = pos.get(b.id)!;
        const dx = pb.x - pa.x;
        const dy = pb.y - pa.y;
        const d = Math.sqrt(dx * dx + dy * dy) || 1e-6;
        const ar = nodeRadius(a);
        const br = nodeRadius(b);
        const want = ar + br + padding;
        if (d < want) {
          // Incremental stability: reused/root nodes are anchors. New content
          // flows AROUND unchanged clusters rather than shoving them aside, so
          // an unrelated cluster does not visibly jump when another grows.
          const aAnchor = a.id === rootId || reused.has(a.id);
          const bAnchor = b.id === rootId || reused.has(b.id);
          const ux = dx / d;
          const uy = dy / d;
          const overlap = want - d;
          if (aAnchor && bAnchor) {
            // Both previously placed — they should already be resolved; nudge
            // each by half to converge any residual drift deterministically.
            pa.x -= ux * overlap * 0.25;
            pa.y -= uy * overlap * 0.25;
            pb.x += ux * overlap * 0.25;
            pb.y += uy * overlap * 0.25;
          } else if (bAnchor) {
            // Only a is free: push a away fully around the anchor b.
            pa.x -= ux * overlap;
            pa.y -= uy * overlap;
          } else if (aAnchor) {
            // Only b is free: push b away fully around the anchor a.
            pb.x += ux * overlap;
            pb.y += uy * overlap;
          } else {
            // Both free: share the displacement.
            pa.x -= ux * overlap * 0.5;
            pa.y -= uy * overlap * 0.5;
            pb.x += ux * overlap * 0.5;
            pb.y += uy * overlap * 0.5;
          }
        }
      }
    }
    // Softly re-bind S agents back toward their parent orbit so the
    // exploration ring reads as a ring while still letting separation win:
    // pull halfway to the target radius each pass instead of snapping rigidly
    // (a rigid snap can undo the collision separation and oscillate).
    // Softly re-bind S agents back toward their parent orbit for the first few
    // iterations (keeps the exploration ring aesthetic), then stop so later
    // passes can fully separate S agents of different parents without the ring
    // pull re-introducing overlap.
    if (iter < 12) {
      for (const [parentId, children] of sByParent) {
        const clusterId = parentId.startsWith("cluster:")
          ? parentId.slice("cluster:".length)
          : null;
        const parentCenter =
          pos.get(parentId) ??
          (clusterId ? clusterCenters.get(clusterId) : undefined);
        if (!parentCenter) continue;
        const parent = nodes.find((n) => n.id === parentId);
        const parentRadius = parent
          ? nodeRadius(parent)
          : clusterId
            ? (clusterAnchorRadii.get(clusterId) ?? STAR_GRAPH_RADIUS.m)
            : STAR_GRAPH_RADIUS.m;
        const orbitRadius = orbitRadiusFor(parentRadius, children.length);
        for (const child of children) {
          if (reused.has(child.id)) continue;
          const pc = pos.get(child.id)!;
          const dx = pc.x - parentCenter.x;
          const dy = pc.y - parentCenter.y;
          const d = Math.sqrt(dx * dx + dy * dy) || 1e-6;
          if (d < orbitRadius * 0.5 || d > orbitRadius * 1.5) {
            const scale = orbitRadius / d;
            const tx = parentCenter.x + dx * scale;
            const ty = parentCenter.y + dy * scale;
            pc.x += (tx - pc.x) * 0.5;
            pc.y += (ty - pc.y) * 0.5;
          }
        }
      }
    }
  }

  /* ---- Build final result records. ---- */
  let moved = 0;
  const nodeResults: StarGraphLayoutNodePosition[] = [];
  for (const n of nodes) {
    const p = pos.get(n.id)!;
    const prev = previousPos.get(n.id);
    const isReused = reused.has(n.id);
    if (!isReused) moved += 1;
    nodeResults.push({
      id: n.id,
      tier: n.tier,
      x: Math.round(p.x * 100) / 100,
      y: Math.round(p.y * 100) / 100,
      radius: nodeRadius(n),
      label: n.label,
      clusterId: n.clusterId ?? null,
      angle: angleOf.get(n.id) ?? 0,
      radiusOffset: offsetOf.get(n.id) ?? 0,
      parentId: n.parentId ?? null,
    });
    void prev;
  }

  /* ---- Cluster boundaries (bounding circle around members). ---- */
  const clusters: StarGraphLayoutCluster[] = [];
  for (const key of clusterKeys) {
    const stableMembers = byCluster.get(key) ?? [];
    const memberIdSet = new Set(stableMembers.map((n) => n.id));
    const orbitMembers = orbit.filter((n) => {
      if (n.clusterId === key) return true;
      return n.parentId != null && memberIdSet.has(n.parentId);
    });
    const members = [...stableMembers, ...orbitMembers];
    const memberIds = members.map((n) => n.id).sort();
    let minX = Infinity;
    let minY = Infinity;
    let maxX = -Infinity;
    let maxY = -Infinity;
    for (const m of members) {
      const p = pos.get(m.id)!;
      const radius = nodeRadius(m);
      const horizontalPad = m.label.halfWidth * 0.35 + 36;
      const verticalPad = m.label.halfHeight * 0.25 + 34;
      minX = Math.min(minX, p.x - radius - horizontalPad);
      maxX = Math.max(maxX, p.x + radius + horizontalPad);
      minY = Math.min(minY, p.y - radius - verticalPad);
      maxY = Math.max(maxY, p.y + radius + verticalPad);
    }
    const width = Math.max(260, maxX - minX);
    const height = Math.max(190, maxY - minY);
    const cx = (minX + maxX) / 2;
    const cy = (minY + maxY) / 2;
    let maxR = 0;
    for (const m of members) {
      const p = pos.get(m.id)!;
      const d = dist(cx, cy, p.x, p.y);
      const ext = d + nodeRadius(m) + m.label.halfWidth * 0.5 + 12;
      if (ext > maxR) maxR = ext;
    }
    clusters.push({
      clusterId: key,
      x: Math.round(cx * 100) / 100,
      y: Math.round(cy * 100) / 100,
      radius: Math.ceil(maxR),
      width: Math.ceil(width),
      height: Math.ceil(height),
      memberIds,
    });
  }

  /* ---- New-frontier territory from canonical `newdir` relations only. ---- */
  const frontierTargetIds = new Set(
    relations
      .filter((relation) => relation.kind === "newdir")
      .map((relation) => relation.toNodeId),
  );
  const frontierMembers = nodes.filter((node) => frontierTargetIds.has(node.id));
  const frontiers: StarGraphLayoutFrontier[] = [];
  if (frontierMembers.length > 0) {
    const pad = 42;
    let minX = Infinity;
    let minY = Infinity;
    let maxX = -Infinity;
    let maxY = -Infinity;
    for (const member of frontierMembers) {
      const point = pos.get(member.id)!;
      const radius = nodeRadius(member);
      minX = Math.min(minX, point.x - radius - pad);
      minY = Math.min(minY, point.y - radius - pad);
      maxX = Math.max(maxX, point.x + radius + pad);
      maxY = Math.max(maxY, point.y + radius + pad);
    }
    frontiers.push({
      id: "newdir",
      x: Math.round(minX * 100) / 100,
      y: Math.round(minY * 100) / 100,
      width: Math.round((maxX - minX) * 100) / 100,
      height: Math.round((maxY - minY) * 100) / 100,
      memberIds: frontierMembers.map((member) => member.id).sort(),
    });
  }

  /* ---- Edges: endpoints snapped to each circle's perimeter. ---- */
  const byPosResult = new Map(nodeResults.map((n) => [n.id, n]));
  const edges: StarGraphLayoutEdge[] = [];
  for (const rel of relations) {
    const from = byPosResult.get(rel.fromNodeId);
    const to = byPosResult.get(rel.toNodeId);
    if (!from || !to) continue;
    const ep = circleEdgeEndpoints(
      from.x, from.y, from.radius,
      to.x, to.y, to.radius,
    );
    edges.push({
      id: rel.id,
      fromNodeId: rel.fromNodeId,
      toNodeId: rel.toNodeId,
      kind: rel.kind,
      from: ep.from,
      to: ep.to,
    });
  }

  return {
    nodes: nodeResults,
    edges,
    clusters,
    frontiers,
    rootId,
    version,
    keyByNode: signatureNow,
    stats: { reused: reused.size, moved, total },
  };
}

/** Whether a previous node position is reusable for the current signature. */
function nodeSignatureCompat(
  prev: StarGraphLayoutNodePosition,
  currentSig: string,
): boolean {
  // Older position records do not carry nodeKind. Compare the geometry part;
  // the full signature still invalidates through keyByNode between versions.
  const geometrySig = `${prev.id}|${prev.tier}|${prev.clusterId ?? ""}|${prev.parentId ?? ""}|${prev.radius}`;
  return currentSig.startsWith(`${geometrySig}|`);
}
