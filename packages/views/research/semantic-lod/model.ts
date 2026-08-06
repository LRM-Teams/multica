/**
 * Semantic LOD — model types (LRM-1488).
 *
 * The four discrete semantic render forms used on the V6 research route
 * canvas (route-topology §2 / node-card §3). Cards, waypoint pills, trail
 * dots and bundles are treated as distinct render shapes with stable anchors —
 * they are never formed by scaling a card's font or by shrinking a whole card
 * into an unreadable block.
 *
 * This module owns only types + the explicit status registry. It imports no
 * React, no DOM and no other view-layer module, so the whole decision seam is
 * testable in the node environment.
 */

/** Discrete semantic node render forms (route-topology §2). */
export type SemanticLOD =
  | "landmark"
  | "waypoint"
  | "trail-dot"
  | "route-bundle"
  | "display-group";

/**
 * Per-route status for path colour / line / endpoint encoding. Classified from
 * an explicit registry (route-topology §3) — never inferred from prose or
 * `title.includes(...)`.
 */
export type RouteOutcome =
  | "exploring"
  | "accepted"
  | "failed"
  | "cancelled"
  | "stale"
  | "disputed"
  | "neutral";

/**
 * Everything the classifier is allowed to read. Protection sets are explicit
 * inputs supplied by the caller (selection, blocking path, etc.) so the
 * classifier never guesses graph intent from node prose.
 */
export interface SemanticContext {
  /** Currently selected node id (kept as a full Landmark). */
  selectedId: string | null;
  /** Selected node's ancestor path ids — never cut by a budget. */
  ancestorIds: readonly string[];
  /** blocking / failed / cancelling / stale path ids — never cut. */
  blockingIds: readonly string[];
  /** running task/attempt ids — high retention. */
  runningIds: readonly string[];
  /** Pinned node ids. */
  pinnedIds: readonly string[];
  /** Most recent transition's affected roots (temporarily promoted). */
  transitionRootIds: readonly string[];
  /**
   * Current zoom as a percentage (0–200+). Drives which LOD tier applies.
   * 25 / 50 / 100 / 120 map to the spec's <35 / 35–65 / 66–119 / ≥120 bands.
   */
  zoomPct: number;
  /**
   * Hop depth of each node from the relevant root (BFS). Node with depth 0 is
   * the root; depth 1–2 are the default visible range; depth 3 is visible only
   * after an explicit expand; depth ≥4 folds into a bundle/spotlight.
   */
  depthById: ReadonlyMap<string, number>;
  /** True when the user explicitly expanded to allow a 3rd visible level. */
  explicitThirdLevel: boolean;
}

/** Budget per viewport tier (route-topology §6.3 / viewport-performance §3). */
export interface VisibleBudget {
  /** Total semantic node soft limit. */
  semanticNodeSoft: number;
  /** Total semantic node hard limit. */
  semanticNodeHard: number;
  /** Total graphic-DOM hard cap (cards + groups + hit targets + anchors). */
  graphicDomHard: number;
  /** Landmark Card hard cap. */
  landmarkHard: number;
  /** Waypoint hard cap. */
  waypointHard: number;
  /** Trail Dot hard cap. */
  trailDotHard: number;
  /** Display Group / Route Bundle hard cap. */
  bundleHard: number;
  /** Edge hard cap. */
  edgeHard: number;
}

export type ViewportTier = "desktop" | "tablet" | "mobile";

/** §6.3 — per-viewport caps. Semantic types are upper bounds included in total. */
export const VIEWPORT_BUDGETS: Record<ViewportTier, VisibleBudget> = {
  desktop: {
    semanticNodeSoft: 120,
    semanticNodeHard: 180,
    graphicDomHard: 220,
    landmarkHard: 48,
    waypointHard: 56,
    trailDotHard: 96,
    bundleHard: 16,
    edgeHard: 420,
  },
  tablet: {
    semanticNodeSoft: 72,
    semanticNodeHard: 96,
    graphicDomHard: 220,
    landmarkHard: 28,
    waypointHard: 36,
    trailDotHard: 52,
    bundleHard: 12,
    edgeHard: 220,
  },
  mobile: {
    semanticNodeSoft: 32,
    semanticNodeHard: 48,
    graphicDomHard: 220,
    landmarkHard: 12,
    waypointHard: 16,
    trailDotHard: 24,
    bundleHard: 8,
    edgeHard: 96,
  },
};

/** Default hop depth shown before an explicit expand (§6.2 / viewport §2). */
export const DEFAULT_VISIBLE_DEPTH = 2;
/** Maximum visible level after an explicit expand (§6.2 / viewport §2). */
export const MAX_VISIBLE_DEPTH = 3;
/** Depth at or beyond which nodes always fold into a bundle/spotlight. */
export const BUNDLE_FOLD_DEPTH = 4;

/** Zoom bands mapping to LOD working tiers (route-topology §6.1). */
export const ZOOM_BANDS = {
  overview: 35,   // < 35% — root/selected/blocking cards only, ≤12
  route: 65,      // 35–65% — compact landmark cards
  work: 119,      // 66–119% — standard landmark cards
  inspect: 120,   // ≥120% — cards may add facts, waypoints promote
} as const;

/** Zoom-band tier labels. */
export type ZoomTier = "overview" | "route" | "work" | "inspect";

/**
 * Select the working zoom tier for a percentage.
 *   <35  → overview
 *   35–65 → route
 *   66–119 → work
 *   ≥120 → inspect
 */
export function zoomTier(zoomPct: number): ZoomTier {
  if (zoomPct < ZOOM_BANDS.overview) return "overview";
  if (zoomPct <= ZOOM_BANDS.route) return "route";
  if (zoomPct <= ZOOM_BANDS.work) return "work";
  return "inspect";
}
