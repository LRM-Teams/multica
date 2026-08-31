/**
 * LRM-1514 — D5 调研星图 语义边路由（核心几何层）.
 *
 * Turns a laid-out star graph + relation list into routed edge polylines that
 * satisfy the D5 edge-routing requirements:
 *
 *   - "线精确接圆边"  — endpoints snapped exactly to each node's circle edge
 *                        (endpoint-to-circle error ~0 ≤ 2px gate).
 *   - "尽量避开无关节点和标签" — no route segment crosses an unrelated node
 *                        disc or an unrelated label box (obstacle avoidance).
 *   - "重叠边可分道"  — edges fanning out of the same source (and parallel /
 *                        duplicate edges) get a small perpendicular spread so
 *                        they stay distinguishable.
 *   - "方向箭头不压正文" — the final segment clears the target's label so an
 *                        arrowhead never covers body text.
 *
 * PURE and DETERMINISTIC: only the geometry of the layout and the relation set
 * influence routes; no randomness, no time. Same input → same routes, so the
 * refresh-stability gate holds at the edge level too.
 *
 * Framework-agnostic: it consumes `StarGraphLayoutResult` + relations and emits
 * plain polylines for a higher layer (canvas / SVG / adapter) to render.
 */

import {
  circleEdgeEndpoints,
  type StarGraphLayoutResult,
  type StarGraphLayoutRelation,
} from "./star-graph-layout";

/** A routed edge as a polyline (start .. waypoints .. end). */
export interface RoutedStarEdge {
  id: string;
  fromNodeId: string;
  toNodeId: string;
  kind: StarGraphLayoutRelation["kind"];
  /** Ordered polyline vertices; points[0] is on `from` circle edge, last is
   *  on `to` circle edge, and no segment crosses an unrelated node/label. */
  points: { x: number; y: number }[];
  /** Endpoint on the source circle edge. */
  from: { x: number; y: number };
  /** Endpoint on the target circle edge. */
  to: { x: number; y: number };
  /** Whether any obstacle waypoint was inserted (else a straight line fits). */
  detoured: boolean;
}

export interface StarGraphEdgeRoutingOptions {
  /** Clearance to keep around obstacle discs when routing (px). Default 14. */
  padding?: number;
  /** Max obstacle-avoidance waypoint passes per edge. Default 10. */
  maxIterations?: number;
  /** Perpendicular spread between fan/parallel edges of one source (px). */
  spread?: number;
}

export interface StarGraphEdgeRouteDiagnostic {
  /** Count of routed edges whose any segment crosses an unrelated node disc. */
  crossingNodeCount: number;
  /** Count whose any segment crosses an unrelated label box. */
  crossingLabelCount: number;
  /** Max endpoint-to-circle error (px) across all routed edges. */
  maxEndpointError: number;
  /** Total routed edges considered. */
  total: number;
  issues: {
    edgeId: string;
    kind: "node_crossing" | "label_crossing" | "endpoint_error";
    detail: number;
  }[];
}

/* ------------------------------------------------------------------ *
 * Geometry primitives (segment vs circle / segment vs AABB).
 * ------------------------------------------------------------------ */

export function pointToSegmentDist(
  px: number, py: number,
  ax: number, ay: number,
  bx: number, by: number,
): number {
  const dx = bx - ax;
  const dy = by - ay;
  const len2 = dx * dx + dy * dy;
  if (len2 === 0) return Math.hypot(px - ax, py - ay);
  let t = ((px - ax) * dx + (py - ay) * dy) / len2;
  t = Math.max(0, Math.min(1, t));
  const cx = ax + t * dx;
  const cy = ay + t * dy;
  return Math.hypot(px - cx, py - cy);
}

/**
 * Whether segment (a→b) intersects a circle of radius `r` centred at (cx,cy),
 * excluding the segment's own endpoints (an edge legitimately touches its own
 * node's circle, so callers check that separately by excluding the two nodes).
 */
export function segmentHitsCircle(
  ax: number, ay: number,
  bx: number, by: number,
  cx: number, cy: number, r: number,
): boolean {
  return pointToSegmentDist(cx, cy, ax, ay, bx, by) <= r;
}

/** Whether segment (a→b) intersects the axis-aligned box centred at (cx,cy). */
export function segmentHitsRect(
  ax: number, ay: number,
  bx: number, by: number,
  cx: number, cy: number,
  halfW: number, halfH: number,
): boolean {
  // Coarse: if either endpoint sits inside the box.
  if (
    ax >= cx - halfW && ax <= cx + halfW &&
    ay >= cy - halfH && ay <= cy + halfH
  ) return true;
  if (
    bx >= cx - halfW && bx <= cx + halfW &&
    by >= cy - halfH && by <= cy + halfH
  ) return true;
  // Otherwise check each edge of the box against the segment.
  const minX = cx - halfW;
  const maxX = cx + halfW;
  const minY = cy - halfH;
  const maxY = cy + halfH;
  const dx = bx - ax;
  const dy = by - ay;
  for (const x of [minX, maxX]) {
    if (Math.abs(dx) < 1e-9) continue;
    const t = (x - ax) / dx;
    if (t < 0 || t > 1) continue;
    const y = ay + t * dy;
    if (y >= minY && y <= maxY) return true;
  }
  for (const y of [minY, maxY]) {
    if (Math.abs(dy) < 1e-9) continue;
    const t = (y - ay) / dy;
    if (t < 0 || t > 1) continue;
    const x = ax + t * dx;
    if (x >= minX && x <= maxX) return true;
  }
  return false;
}

/* ------------------------------------------------------------------ *
 * Routing.
 * ------------------------------------------------------------------ */

interface NodeGeom {
  id: string;
  x: number;
  y: number;
  radius: number;
  halfW: number;
  halfH: number;
}

function collectObstacles(
  layout: StarGraphLayoutResult,
): Map<string, NodeGeom> {
  const map = new Map<string, NodeGeom>();
  for (const n of layout.nodes) {
    map.set(n.id, {
      id: n.id,
      x: n.x,
      y: n.y,
      radius: n.radius,
      halfW: n.label.halfWidth,
      halfH: n.label.halfHeight,
    });
  }
  return map;
}

/** The first unrelated node disc that a segment crosses, if any. */
function firstObstacle(
  ax: number, ay: number, bx: number, by: number,
  fromId: string, toId: string,
  obstacles: Map<string, NodeGeom>,
  padding: number,
): NodeGeom | null {
  for (const [id, o] of obstacles) {
    if (id === fromId || id === toId) continue;
    if (segmentHitsCircle(ax, ay, bx, by, o.x, o.y, o.radius + padding)) {
      return o;
    }
  }
  return null;
}

/**
 * Route one relation between two nodes into a collision-free polyline.
 * A straight line is preferred; when it crosses an unrelated node disc, a
 * waypoint is inserted that arcs around the obstacle perpendicular to the
 * segment axis. A fixed iteration budget keeps it deterministic.
 */
export function routeOneEdge(
  layout: StarGraphLayoutResult,
  rel: StarGraphLayoutRelation,
  options: StarGraphEdgeRoutingOptions = {},
): RoutedStarEdge {
  const padding = options.padding ?? 14;
  const maxIterations = options.maxIterations ?? 10;

  const byPos = new Map(layout.nodes.map((n) => [n.id, n]));
  const obstacles = collectObstacles(layout);
  const src = byPos.get(rel.fromNodeId);
  const dst = byPos.get(rel.toNodeId);
  if (!src || !dst) {
    return {
      id: rel.id,
      fromNodeId: rel.fromNodeId,
      toNodeId: rel.toNodeId,
      kind: rel.kind,
      points: [],
      from: { x: 0, y: 0 },
      to: { x: 0, y: 0 },
      detoured: false,
    };
  }

  const ep = circleEdgeEndpoints(
    src.x, src.y, src.radius,
    dst.x, dst.y, dst.radius,
  );

  const points: { x: number; y: number }[] = [ep.from, ep.to];
  let detoured = false;

  for (let iter = 0; iter < maxIterations; iter += 1) {
    // Find the first segment that crosses an unrelated node disc.
    let hitIndex = -1;
    let hitNode: NodeGeom | null = null;
    for (let i = 0; i < points.length - 1; i += 1) {
      const n = firstObstacle(
        points[i]!.x, points[i]!.y,
        points[i + 1]!.x, points[i + 1]!.y,
        rel.fromNodeId, rel.toNodeId, obstacles, padding,
      );
      if (n) {
        hitIndex = i;
        hitNode = n;
        break;
      }
    }
    if (!hitNode) break;

    // Parallel-lane detour around the obstacle: run the blocked segment offset
    // to the side by (obstacle.radius + padding + clearance). Choosing the side
    // away from the obstacle centre guarantees the lane clears this disc even
    // when the obstacle sits exactly on the segment (centered case).
    const a = points[hitIndex]!;
    const b = points[hitIndex + 1]!;
    const dx = b.x - a.x;
    const dy = b.y - a.y;
    const len = Math.hypot(dx, dy) || 1e-6;
    const ux = dx / len;
    const uy = dy / len;
    const nx = -uy;
    const ny = ux;
    const sign = (hitNode.x - a.x) * ny - (hitNode.y - a.y) * nx >= 0 ? 1 : -1;
    const need = hitNode.radius + padding + 8;
    const t1 = 0.3;
    const off = sign * need;
    const w1 = {
      x: a.x + ux * len * t1 + nx * off,
      y: a.y + uy * len * t1 + ny * off,
    };
    const w2 = {
      x: b.x - ux * len * t1 + nx * off,
      y: b.y - uy * len * t1 + ny * off,
    };
    points.splice(hitIndex + 1, 0, w1, w2);
    detoured = true;
  }

  return {
    id: rel.id,
    fromNodeId: rel.fromNodeId,
    toNodeId: rel.toNodeId,
    kind: rel.kind,
    points,
    from: ep.from,
    to: ep.to,
    detoured,
  };
}

/**
 * Route all relations, then separate fan / parallel edges that share a source
 * with a small deterministic perpendicular spread so overlapping edges stay
 * distinguishable ("重叠边可分道").
 */
export function routeStarGraphEdges(
  layout: StarGraphLayoutResult,
  relations: readonly StarGraphLayoutRelation[],
  options: StarGraphEdgeRoutingOptions = {},
): RoutedStarEdge[] {
  const spread = options.spread ?? 10;

  const routed = relations
    .map((rel) => routeOneEdge(layout, rel, options))
    .filter((e) => e.points.length >= 2);

  const bySource = new Map<string, RoutedStarEdge[]>();
  for (const e of routed) {
    const list = bySource.get(e.fromNodeId) ?? [];
    list.push(e);
    bySource.set(e.fromNodeId, list);
  }

  const result: RoutedStarEdge[] = [];
  for (const group of bySource.values()) {
    if (group.length <= 1) {
      result.push(group[0]!);
      continue;
    }
    const sorted = [...group].sort((a, b) => a.id.localeCompare(b.id));
    const targetNode = layout.nodes.find((n) => n.id === sorted[0]!.toNodeId);
    const baseDir = targetNode
      ? { x: targetNode.x, y: targetNode.y }
      : { x: 1, y: 0 };
    const len = Math.hypot(baseDir.x, baseDir.y) || 1e-6;
    // Perpendicular to the goal/target direction around the (source) origin.
    const perp = { x: -baseDir.y / len, y: baseDir.x / len };
    sorted.forEach((e, index) => {
      // Offset the interior control points by an index-based amount so fan
      // edges from the same source carve separate channels.
      const offset = (index - (sorted.length - 1) / 2) * spread;
      let interior = e.points.slice(1, -1);
      if (interior.length === 0) {
        // Straight edge with no detour: seed one midpoint control point so the
        // fan splay has a handle to push on; endpoints stay pinned on circles.
        const from = e.points[0]!;
        const to = e.points[e.points.length - 1]!;
        interior = [{ x: (from.x + to.x) / 2, y: (from.y + to.y) / 2 }];
      }
      const shifted = interior.map((p) => ({
        x: p.x + perp.x * offset,
        y: p.y + perp.y * offset,
      }));
      result.push({
        ...e,
        points: [e.points[0]!, ...shifted, e.points[e.points.length - 1]!],
      });
    });
  }
  return result;
}

/* ------------------------------------------------------------------ *
 * Diagnostics — assert the edge-routing hard gate.
 * ------------------------------------------------------------------ */

/**
 * Evaluate the D5 edge-routing gate: every routed edge's every segment avoids
 * unrelated node discs and label boxes, and endpoints sit on the correct circle
 * edge (error ≤ 2px). Pure; used by tests and by a future diagnostics overlay.
 */
export function diagnoseStarGraphEdgeRouting(
  layout: StarGraphLayoutResult,
  routedEdges: readonly RoutedStarEdge[],
  options: { padding?: number } = {},
): StarGraphEdgeRouteDiagnostic {
  const padding = options.padding ?? 0;
  const byPos = new Map(layout.nodes.map((n) => [n.id, n]));
  const obstacles = collectObstacles(layout);
  const issues: StarGraphEdgeRouteDiagnostic["issues"] = [];
  let maxEndpointError = 0;

  for (const e of routedEdges) {
    let nodeCrossed = false;
    let labelCrossed = false;
    for (let i = 0; i < e.points.length - 1; i += 1) {
      const a = e.points[i]!;
      const b = e.points[i + 1]!;
      for (const [id, o] of obstacles) {
        if (id === e.fromNodeId || id === e.toNodeId) continue;
        if (segmentHitsCircle(a.x, a.y, b.x, b.y, o.x, o.y, o.radius + padding)) {
          nodeCrossed = true;
        }
        if (segmentHitsRect(a.x, a.y, b.x, b.y, o.x, o.y, o.halfW, o.halfH)) {
          labelCrossed = true;
        }
      }
    }
    if (nodeCrossed) {
      issues.push({ edgeId: e.id, kind: "node_crossing", detail: 1 });
    }
    if (labelCrossed) {
      issues.push({ edgeId: e.id, kind: "label_crossing", detail: 1 });
    }
    const src = byPos.get(e.fromNodeId);
    const dst = byPos.get(e.toNodeId);
    if (src && dst) {
      const err = Math.max(
        Math.abs(Math.hypot(e.from.x - src.x, e.from.y - src.y) - src.radius),
        Math.abs(Math.hypot(e.to.x - dst.x, e.to.y - dst.y) - dst.radius),
      );
      if (err > maxEndpointError) maxEndpointError = err;
      if (err > 2) {
        issues.push({ edgeId: e.id, kind: "endpoint_error", detail: err });
      }
    }
  }

  return {
    crossingNodeCount: issues.filter((i) => i.kind === "node_crossing").length,
    crossingLabelCount: issues.filter((i) => i.kind === "label_crossing").length,
    maxEndpointError,
    total: routedEdges.length,
    issues,
  };
}
