/**
 * LRM-1497 — geometry for research-canvas connection endpoints.
 *
 * React Flow draws default bezier edges between node *handles* (left/right/
 * top/bottom of the node bounds). Because research nodes are rounded
 * rectangles (240×76, `rounded-lg`), a straight spring from a neighbour toward
 * the node centre can terminate just inside the visual shell or skim a corner,
 * which shows up as a line that "penetrates" the node or floats with a visible
 * gap through the border. This module computes, in pure world-space maths, the
 * exact point where the source→target segment meets the target's *visible*
 * boundary (rounded-rect edge), so callers can place the handle / arrow-head
 * so it lands on the node edge and never covers the node title.
 *
 * The function is deliberately pure and DOM/react-free so it can be unit
 * tested in node and reused by any render layer (React Flow today, the
 * FE-04/06 ViewModel layer next).
 */

export interface RoundedRect {
  /** World-space top-left of the node bounds. */
  x: number;
  /** World-space top-left of the node bounds. */
  y: number;
  /** Node bounds width (world units). */
  width: number;
  /** Node bounds height (world units). */
  height: number;
  /** Corner radius of the visible shell, world units (0 => axis-aligned rect). */
  radius: number;
}

export interface PointLike {
  x: number;
  y: number;
}

/** Centre of a rounded-rect node bounds. */
export function rectCenter(rect: RoundedRect): PointLike {
  return { x: rect.x + rect.width / 2, y: rect.y + rect.height / 2 };
}

function normalize(x: number, y: number): { x: number; y: number } {
  const len = Math.hypot(x, y) || 1;
  return { x: x / len, y: y / len };
}

interface Segment {
  a: PointLike;
  b: PointLike;
}

/**
 * Intersect a ray (origin `c`, unit direction `dir`) with a line segment.
 * Return the parameter `t` (distance from `c` along the ray) of the first hit,
 * or `Infinity` if the ray misses the segment.
 */
function raySegmentHit(c: PointLike, dir: PointLike, s: Segment): number {
  const dx = s.b.x - s.a.x;
  const dy = s.b.y - s.a.y;
  const denom = dir.x * dy - dir.y * dx;
  if (Math.abs(denom) < 1e-9) return Number.POSITIVE_INFINITY;
  const cx = s.a.x - c.x;
  const cy = s.a.y - c.y;
  const t = (cx * dy - cy * dx) / denom;
  const u = (cx * dir.y - cy * dir.x) / denom;
  if (t < 0) return Number.POSITIVE_INFINITY;
  if (u < 0 || u > 1) return Number.POSITIVE_INFINITY;
  return t;
}

/** Intersect a ray with a circle: returns nearest `t >= 0` hit or Infinity. */
function rayCircleHit(c: PointLike, dir: PointLike, centre: PointLike, r: number): number {
  const ox = c.x - centre.x;
  const oy = c.y - centre.y;
  const b = ox * dir.x + oy * dir.y;
  const cc = ox * ox + oy * oy - r * r;
  const disc = b * b - cc;
  if (disc < 0) return Number.POSITIVE_INFINITY;
  const sq = Math.sqrt(disc);
  // First positive hit along the ray.
  let t = -b - sq;
  if (t < 0) t = -b + sq;
  if (t < 0) return Number.POSITIVE_INFINITY;
  return t;
}

/**
 * Returns the exit point of the ray `centre → dir` with the rounded-rect
 * silhouette: the first boundary contact walking outward. The four straight
 * edges live outside the corner radii; the four corners are quarter circles
 * centred at the inset corners. We take the smallest positive hit across all
 * seven primitives (4 edges, 3 visible corner arcs — the far corner arcs are
 * never the first exit for an outward ray but are included for safety).
 */
function rayBoundaryExit(centre: PointLike, rect: RoundedRect, dir: PointLike): PointLike {
  if (rect.width <= 0 || rect.height <= 0) {
    return { x: centre.x, y: centre.y };
  }
  const r = Math.max(0, Math.min(rect.radius, rect.width / 2, rect.height / 2));
  const left = centre.x - rect.width / 2;
  const right = centre.x + rect.width / 2;
  const top = centre.y - rect.height / 2;
  const bottom = centre.y + rect.height / 2;
  // Inset straight-edge rectangle endpoints.
  const il = left + r;
  const ir = right - r;
  const it = top + r;
  const ib = bottom - r;

  const segments: Segment[] = [
    { a: { x: il, y: top }, b: { x: ir, y: top } },
    { a: { x: ir, y: bottom }, b: { x: il, y: bottom } },
    { a: { x: left, y: it }, b: { x: left, y: ib } },
    { a: { x: right, y: ib }, b: { x: right, y: it } },
  ];

  const corners: Array<{ centre: PointLike; x: number; y: number }> =
    r > 0
      ? [
          { centre: { x: il, y: it }, x: -1, y: -1 }, // top-left
          { centre: { x: ir, y: it }, x: 1, y: -1 }, // top-right
          { centre: { x: il, y: ib }, x: -1, y: 1 }, // bottom-left
          { centre: { x: ir, y: ib }, x: 1, y: 1 }, // bottom-right
        ]
      : [];

  let best = Number.POSITIVE_INFINITY;
  let bestPoint: PointLike = centre;

  const consider = (t: number, p: PointLike) => {
    if (t < best) {
      best = t;
      bestPoint = p;
    }
  };

  for (const s of segments) {
    const t = raySegmentHit(centre, dir, s);
    if (Number.isFinite(t)) consider(t, { x: centre.x + dir.x * t, y: centre.y + dir.y * t });
  }
  for (const corner of corners) {
    // Only the two corner arcs the ray is heading toward can be the first exit;
    // test only when the ray points into this corner's quadrant to avoid a hit
    // on the *opposite* corner arc's silhouette.
    if (dir.x * corner.x < -1e-9 && dir.y * corner.y < -1e-9) {
      const t = rayCircleHit(centre, dir, corner.centre, r);
      if (Number.isFinite(t)) consider(t, { x: centre.x + dir.x * t, y: centre.y + dir.y * t });
    }
  }

  return bestPoint;
}

/**
 * The point on `rect`'s *visible boundary* where a segment arriving from
 * `source` (a point outside the node) first touches the node's shell.
 *
 * Equivalent to the exit point of the reverse ray from the node centre toward
 * `source`, clipped to the rounded-rect silhouette. Used to place a connection
 * handle so an incoming edge meets the node edge exactly — with no gap through
 * the border and no penetration into the node interior.
 */
export function edgeEndpointOnNodeBoundary(source: PointLike, rect: RoundedRect): PointLike {
  const c = rectCenter(rect);
  const dir = normalize(source.x - c.x, source.y - c.y);
  return rayBoundaryExit(c, rect, dir);
}

/**
 * Arrow-head landing point for a connection entering `rect` from `source`.
 *
 * The boundary contact is pushed `outset` world units further out along the
 * source→centre direction: a `markerEnd` arrow-head centred exactly here has
 * its tip on the node edge and its shaft outside the shell — so the arrowhead
 * never covers the node border or its title.
 */
export function arrowEndpoint(
  source: PointLike,
  rect: RoundedRect,
  outset = 0,
): PointLike {
  const boundary = edgeEndpointOnNodeBoundary(source, rect);
  const c = rectCenter(rect);
  const dir = normalize(boundary.x - c.x, boundary.y - c.y);
  return { x: boundary.x + dir.x * outset, y: boundary.y + dir.y * outset };
}
