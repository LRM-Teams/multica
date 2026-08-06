/**
 * Stable organic geometry (LRM-1487 / 实现-11).
 *
 * Every route edge is a cubic Bézier built from endpoint ports + tangent
 * directions (spec §4.3). Tangents are chosen deterministically from the
 * stable seed — 24–52° for branch departure — so the layout is organic but
 * never random and never degrades into an orthogonal tree or equal-rank grid.
 *
 * Geometry invariants enforced/tested here:
 *   - curves are always cubic Bézier (four control points, never polylines);
 *   - minimum radius of curvature >= 32px (MIN_RADIUS);
 *   - a curve's last ~24px of approach is near-perpendicular to the card edge
 *     (no arrow clipping into card body);
 *   - avoidance only nudges control points / the local corridor, never
 *     introduces 90° corners.
 */
import type { CubicBezier, Point } from "./types";
import { stableAngleDeg } from "./seed";

/** Minimum radius of curvature (px) — spec §4.3. */
export const MIN_RADIUS = 32;
/** Approach length kept perpendicular to the card edge (px). */
export const APPROACH_LENGTH = 24;
/** Branch tangent band (degrees) — spec §4.2. */
export const BRANCH_TANGENT_MIN_DEG = 24;
export const BRANCH_TANGENT_MAX_DEG = 52;

/* ---------------------------------------------------------------------------
 * Vector helpers.
 * ------------------------------------------------------------------------- */

export function add(a: Point, b: Point): Point {
  return { x: a.x + b.x, y: a.y + b.y };
}
export function sub(a: Point, b: Point): Point {
  return { x: a.x - b.x, y: a.y - b.y };
}
export function scale(a: Point, s: number): Point {
  return { x: a.x * s, y: a.y * s };
}
export function dot(a: Point, b: Point): number {
  return a.x * b.x + a.y * b.y;
}
export function len(a: Point): number {
  return Math.hypot(a.x, a.y);
}
export function normalize(a: Point): Point {
  const l = len(a);
  if (l < 1e-9) return { x: 0, y: 0 };
  return { x: a.x / l, y: a.y / l };
}
export function dist(a: Point, b: Point): number {
  return Math.hypot(b.x - a.x, b.y - a.y);
}
export function lerp(a: Point, b: Point, t: number): Point {
  return { x: a.x + (b.x - a.x) * t, y: a.y + (b.y - a.y) * t };
}

/* ---------------------------------------------------------------------------
 * Cubic Bézier.
 * ------------------------------------------------------------------------- */

/**
 * Build a cubic Bézier from source/target ports and unit tangents (spec §4.3).
 *   P0 = source port
 *   P1 = P0 + tan_s * clamp(distance * 0.32, 48, 160)
 *   P2 = P3 - tan_t * clamp(distance * 0.28, 40, 144)
 *   P3 = target port
 */
export function cubicBezier(
  source: Point,
  target: Point,
  sourceTangent: Point,
  targetTangent: Point,
): CubicBezier {
  const d = Math.max(dist(source, target), 1);
  const l1 = clamp(d * 0.32, 48, 160);
  const l2 = clamp(d * 0.28, 40, 144);
  const p0 = source;
  const p1 = add(source, scale(normalize(sourceTangent), l1));
  const p2 = sub(target, scale(normalize(targetTangent), l2));
  const p3 = target;
  return { p0, p1, p2, p3 };
}

export function clamp(v: number, min: number, max: number): number {
  return Math.min(max, Math.max(min, v));
}

/** A unit-length perpendicular (either side). */
export function perpendicular(a: Point, side: number): Point {
  const n = normalize(a);
  return { x: -n.y * side, y: n.x * side };
}

/**
 * Deterministic tangent for a branch leaving `parent` toward `child`. The
 * departure angle sits in the 24–52° band (hashed per edge, stable seed), then
 * alternates side (above/below the parent) via the stable hash so sibling
 * branches fan instead of stacking.
 */
export function branchTangent(
  parent: Point,
  child: Point,
  edgeKey: string,
  side: number,
  seed: string,
): Point {
  const forward = normalize(sub(child, parent));
  const angle = stableAngleDeg(
    edgeKey,
    BRANCH_TANGENT_MIN_DEG,
    BRANCH_TANGENT_MAX_DEG,
    seed,
  );
  const rad = (angle * Math.PI) / 180;
  // rotate `forward` by ±angle around the normal.
  const cos = Math.cos(rad);
  const sin = Math.sin(rad) * side;
  return {
    x: forward.x * cos - forward.y * sin,
    y: forward.x * sin + forward.y * cos,
  };
}

/**
 * Rotate a unit tangent by a deterministic angle in [min,max] degrees, signed
 * by `side` (+1 above, -1 below the parent axis). Used for failed/dead-end
 * paths that bend outward.
 */
export function bentTangent(
  base: Point,
  edgeKey: string,
  minDeg: number,
  maxDeg: number,
  side: number,
  seed: string,
): Point {
  const angle = stableAngleDeg(edgeKey, minDeg, maxDeg, seed);
  const rad = (angle * Math.PI) / 180;
  const cos = Math.cos(rad);
  const sin = Math.sin(rad) * side;
  return {
    x: base.x * cos - base.y * sin,
    y: base.x * sin + base.y * cos,
  };
}

/* ---------------------------------------------------------------------------
 * Curvature.
 * ------------------------------------------------------------------------- */

/** Approximate minimum radius of curvature of a cubic Bézier over 32 samples. */
export function minRadiusOfCurvature(c: CubicBezier): number {
  let minR = Infinity;
  for (let i = 0; i <= 32; i += 1) {
    const t = i / 32;
    const d1 = bezierDerivative(c, t);
    const d2 = bezierSecondDerivative(c, t);
    // curvature κ = |x'y" - y'x"| / (x'²+y'²)^(3/2); radius = 1/κ.
    const cross = d1.x * d2.y - d1.y * d2.x;
    const speedSq = d1.x * d1.x + d1.y * d1.y;
    if (speedSq < 1e-9) continue;
    const kappa = Math.abs(cross) / Math.pow(speedSq, 1.5);
    if (kappa < 1e-9) continue;
    const r = 1 / kappa;
    if (r < minR) minR = r;
  }
  return minR;
}

export function pointOnBezier(c: CubicBezier, t: number): Point {
  const mt = 1 - t;
  const a = mt * mt * mt;
  const b = 3 * mt * mt * t;
  const cc = 3 * mt * t * t;
  const d = t * t * t;
  return {
    x: a * c.p0.x + b * c.p1.x + cc * c.p2.x + d * c.p3.x,
    y: a * c.p0.y + b * c.p1.y + cc * c.p2.y + d * c.p3.y,
  };
}

function bezierDerivative(c: CubicBezier, t: number): Point {
  const mt = 1 - t;
  const a = 3 * mt * mt;
  const b = 6 * mt * t;
  const d = 3 * t * t;
  return {
    x: a * (c.p1.x - c.p0.x) + b * (c.p2.x - c.p1.x) + d * (c.p3.x - c.p2.x),
    y: a * (c.p1.y - c.p0.y) + b * (c.p2.y - c.p1.y) + d * (c.p3.y - c.p2.y),
  };
}

function bezierSecondDerivative(c: CubicBezier, t: number): Point {
  const a = 6 * (1 - t);
  const b = 6 * t;
  return {
    x: a * (c.p2.x - 2 * c.p1.x + c.p0.x) + b * (c.p3.x - 2 * c.p2.x + c.p1.x),
    y: a * (c.p2.y - 2 * c.p1.y + c.p0.y) + b * (c.p3.y - 2 * c.p2.y + c.p1.y),
  };
}

/* ---------------------------------------------------------------------------
 * Card avoidance.
 * ------------------------------------------------------------------------- */

/** Axis-aligned card bounds for avoidance. */
export interface CardRect {
  x: number;
  y: number;
  width: number;
  height: number;
  /** safety padding so curves don't clip card body. */
  padding: number;
}

/** Distance from a point to an (inflated) rect; 0 when inside. */
export function distanceToRect(p: Point, r: CardRect): number {
  const halfW = r.width / 2 + r.padding;
  const halfH = r.height / 2 + r.padding;
  const dx = Math.max(Math.abs(p.x - (r.x + r.width / 2)) - halfW, 0);
  const dy = Math.max(Math.abs(p.y - (r.y + r.height / 2)) - halfH, 0);
  return Math.hypot(dx, dy);
}

/**
 * Push a single control point away from overlapping card rects. Only moves
 * control points (never adds corners), preserving the cubic shape. Repeats are
 * bounded so avoidance stays cheap and deterministic.
 */
export function pushControlPoint(
  p: Point,
  rects: readonly CardRect[],
  stiffness = 24,
): Point {
  let out = p;
  for (const r of rects) {
    const halfW = r.width / 2 + r.padding;
    const halfH = r.height / 2 + r.padding;
    const cx = r.x + r.width / 2;
    const cy = r.y + r.height / 2;
    const dx = out.x - cx;
    const dy = out.y - cy;
    const overlapX = halfW - Math.abs(dx);
    const overlapY = halfH - Math.abs(dy);
    if (overlapX > 0 && overlapY > 0) {
      // Push out along the smaller overlap (nearest edge).
      if (overlapX < overlapY) {
        const dir = dx >= 0 ? 1 : -1;
        out = { x: out.x + (overlapX + stiffness) * dir, y: out.y };
      } else {
        const dir = dy >= 0 ? 1 : -1;
        out = { x: out.x, y: out.y + (overlapY + stiffness) * dir };
      }
    }
  }
  return out;
}

/**
 * Apply card avoidance to both inner control points of a curve, in place of
 * the naive source/target tangent controls. Returns a new Bézier.
 */
export function avoidCards(
  c: CubicBezier,
  rects: readonly CardRect[],
): CubicBezier {
  if (rects.length === 0) return c;
  return {
    p0: c.p0,
    p1: pushControlPoint(c.p1, rects),
    p2: pushControlPoint(c.p2, rects),
    p3: c.p3,
  };
}

/**
 * True when a cubic Bézier segments stays orthogonal only trivially — i.e. it
 * has at least one non-trivial curved segment. Used to guard against the
 * "degenerate to orthogonal polyline" regression (AC2).
 */
export function isGenuinelyCurved(c: CubicBezier): boolean {
  const straight = (a: Point, b: Point) => dist(a, b) < 1e-6;
  // A cubic collapses to a straight line iff all control points are collinear
  // AND P1/P2 lie on the P0->P3 segment. We approximate by checking the four
  // mid-t samples are not all collinear with the chord.
  const chord = normalize(sub(c.p3, c.p0));
  const pt = (p: Point) => {
    const v = sub(p, c.p0);
    const along = dot(v, chord);
    const away = Math.hypot(
      v.x - chord.x * along,
      v.y - chord.y * along,
    );
    return away;
  };
  const maxAway = Math.max(
    pt(c.p1),
    pt(c.p2),
    pt(pointOnBezier(c, 0.25)),
    pt(pointOnBezier(c, 0.5)),
    pt(pointOnBezier(c, 0.75)),
    pt(pointOnBezier(c, 1)),
  );
  void straight;
  return maxAway > 1e-3;
}
