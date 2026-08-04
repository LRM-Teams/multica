/**
 * LRM-1335 — Canvas reorg animation: pure functions, constants, and CSS generation.
 *
 * This module is side-effect-free and testable without DOM.
 * Follows LRM-1311 design spec (comment 91392d49):
 * - classifyCanvasDelta: detect reorg / appended / none
 * - Timing constants: single element ≤320ms, total budget ≤900ms, stagger cap ≤6
 * - CSS generation: transition scoped to [data-reorg="running"] .react-flow__node
 */

// ─── Types ───────────────────────────────────────────────────────────────────

export type CanvasNodeSnapshot = {
  x: number;
  y: number;
  lane: number;
  nodeType: string;
  status?: string;
};

export type CanvasDeltaKind = "none" | "appended" | "reorg";

export type CanvasDelta = {
  kind: CanvasDeltaKind;
  /** IDs of nodes that moved ≥ threshold or changed lane/type/status. */
  movedIds: string[];
  /** IDs of newly added nodes (present in next but not prev). */
  addedIds: string[];
  /** IDs of removed nodes (present in prev but not next). */
  removedIds: string[];
};

// ─── Constants (AC #4: single ≤320ms, total ≤900ms, stagger cap ≤6) ─────────

/** Minimum position displacement (px) to trigger reorg for a node. */
export const REORG_DISPLACEMENT_THRESHOLD_PX = 24;

/** Max duration for any single element transition (ms). */
export const REORG_SINGLE_ELEMENT_MAX_MS = 320;

/** Total reorg animation budget — nothing runs past this (ms). */
export const REORG_TOTAL_BUDGET_MS = 900;

/** Max stagger steps for gutter lane growth. */
export const REORG_LANE_STAGGER_CAP = 6;

/** Per-lane stagger delay (ms). */
export const REORG_LANE_STAGGER_MS = 40;

// ─── Phase timing (P0–P5) ────────────────────────────────────────────────────

/** P1: old-state fade-out window. */
export const P1_START_MS = 0;
export const P1_DURATION_MS = 160;

/** P2: node reposition (transform transition). */
export const P2_START_MS = 120;
export const P2_DURATION_MS = 320;

/** P3: gutter line growth (dashoffset). */
export const P3_START_MS = 380;
export const P3_DURATION_MS = 260;

/** P4: new node entrance (reuses node-enter-motion). */
export const P4_START_MS = 600;
export const P4_DURATION_MS = 280;

/** Easing curves. */
export const REORG_EASING = "cubic-bezier(0.22, 1, 0.36, 1)";
export const GUTTER_EASING = "ease-out";

// ─── classifyCanvasDelta ─────────────────────────────────────────────────────

/**
 * Compares two frames of canvas node positions to classify the delta.
 *
 * - "none": no meaningful change
 * - "appended": only new nodes added (walks existing 827 enter path)
 * - "reorg": at least one existing node displaced ≥24px, or lane/type/status changed
 */
export function classifyCanvasDelta(
  prev: Map<string, CanvasNodeSnapshot>,
  next: Map<string, CanvasNodeSnapshot>,
): CanvasDelta {
  const movedIds: string[] = [];
  const addedIds: string[] = [];
  const removedIds: string[] = [];

  // Detect removed nodes
  for (const id of prev.keys()) {
    if (!next.has(id)) {
      removedIds.push(id);
    }
  }

  // Detect added and moved/changed nodes
  for (const [id, snap] of next) {
    const prevSnap = prev.get(id);
    if (!prevSnap) {
      addedIds.push(id);
      continue;
    }
    // Check lane / nodeType / status change
    if (
      prevSnap.lane !== snap.lane ||
      prevSnap.nodeType !== snap.nodeType ||
      prevSnap.status !== snap.status
    ) {
      movedIds.push(id);
      continue;
    }
    // Check displacement
    const dx = snap.x - prevSnap.x;
    const dy = snap.y - prevSnap.y;
    const dist = Math.sqrt(dx * dx + dy * dy);
    if (dist >= REORG_DISPLACEMENT_THRESHOLD_PX) {
      movedIds.push(id);
    }
  }

  // Classify
  if (movedIds.length === 0 && removedIds.length === 0 && addedIds.length === 0) {
    return { kind: "none", movedIds, addedIds, removedIds };
  }
  if (movedIds.length === 0 && removedIds.length === 0 && addedIds.length > 0) {
    return { kind: "appended", movedIds, addedIds, removedIds };
  }
  return { kind: "reorg", movedIds, addedIds, removedIds };
}

// ─── CSS generation ──────────────────────────────────────────────────────────

/**
 * Generates scoped CSS for the reorg animation.
 * AC #5: only targets .react-flow__node within [data-reorg="running"],
 *         never targets .react-flow__viewport.
 *
 * The CSS adds transform transition on RF node containers so position changes
 * animate smoothly, then is removed when data-reorg clears.
 */
export function reorgTransitionCss(): string {
  return `
[data-reorg="running"] .react-flow__node {
  transition: transform ${P2_DURATION_MS}ms ${REORG_EASING} ${P2_START_MS}ms;
}
[data-reorg="running"] .react-flow__node.dragging {
  transition: none;
}
`;
}

/**
 * Generates the reduced-motion badge CSS (static label, no animation).
 */
export function reorgBadgeCss(): string {
  return `
.research-reorg-badge {
  position: absolute;
  top: 12px;
  right: 12px;
  z-index: 30;
  padding: 4px 10px;
  border-radius: 6px;
  background: color-mix(in oklch, var(--card) 92%, transparent);
  border: 1px solid var(--border);
  font-size: 12px;
  line-height: 1.4;
  color: var(--muted-foreground);
  pointer-events: auto;
  cursor: default;
  backdrop-filter: blur(6px);
}
.research-reorg-badge-close {
  margin-left: 8px;
  padding: 2px;
  border: none;
  background: none;
  color: var(--muted-foreground);
  cursor: pointer;
  font-size: 12px;
  line-height: 1;
}
@media (prefers-reduced-motion: no-preference) {
  .research-reorg-badge {
    display: none;
  }
}
`;
}

// ─── Snapshot helpers ────────────────────────────────────────────────────────

/**
 * Builds a snapshot map from laid nodes for use in classifyCanvasDelta.
 */
export function buildNodeSnapshotMap(
  nodes: ReadonlyArray<{
    id: string;
    position: { x: number; y: number };
    type?: string;
    data: {
      research?: { node_type?: string; status?: string } | null;
      gitLane?: number;
    };
  }>,
): Map<string, CanvasNodeSnapshot> {
  const map = new Map<string, CanvasNodeSnapshot>();
  for (const n of nodes) {
    if (n.type === "gitGutter") continue;
    const research = n.data.research;
    map.set(n.id, {
      x: n.position.x,
      y: n.position.y,
      lane: n.data.gitLane ?? 0,
      nodeType: research?.node_type ?? "unknown",
      status: research?.status,
    });
  }
  return map;
}

// ─── Gutter dash helpers ─────────────────────────────────────────────────────

/**
 * Computes stroke-dasharray and stroke-dashoffset for a gutter growth animation.
 * During reorg: dasharray=pathLength, dashoffset=pathLength (invisible).
 * Animation transitions dashoffset to 0 (fully drawn).
 *
 * @param pathLength - Total length of the SVG path
 * @param laneIndex - 0-based lane index for stagger delay
 * @returns Style props to apply on the path element
 */
export function gutterGrowthStyle(
  pathLength: number,
  laneIndex: number,
): {
  strokeDasharray: number;
  strokeDashoffset: number;
  transition: string;
} {
  const staggerDelay = Math.min(laneIndex, REORG_LANE_STAGGER_CAP) * REORG_LANE_STAGGER_MS;
  return {
    strokeDasharray: pathLength,
    strokeDashoffset: pathLength,
    transition: `stroke-dashoffset ${P3_DURATION_MS}ms ${GUTTER_EASING} ${P3_START_MS + staggerDelay}ms`,
  };
}

/**
 * Returns the target state (fully drawn) for a gutter path during reorg.
 */
export function gutterGrowthTargetStyle(
  pathLength: number,
  laneIndex: number,
): {
  strokeDasharray: number;
  strokeDashoffset: number;
  transition: string;
} {
  const staggerDelay = Math.min(laneIndex, REORG_LANE_STAGGER_CAP) * REORG_LANE_STAGGER_MS;
  return {
    strokeDasharray: pathLength,
    strokeDashoffset: 0,
    transition: `stroke-dashoffset ${P3_DURATION_MS}ms ${GUTTER_EASING} ${P3_START_MS + staggerDelay}ms`,
  };
}

// ─── A11y text formatters ────────────────────────────────────────────────────

/**
 * Format the reorg-start announcement text.
 */
export function reorgStartText(
  t: (accessor: (ns: { a11y: { reorg_start: string } }) => string) => string,
): string {
  return t(($) => $.a11y.reorg_start);
}

/**
 * Format the reorg-done announcement text.
 */
export function reorgDoneText(
  t: (
    accessor: (ns: { a11y: { reorg_done: string } }) => string,
    params: { count: number; paths: number },
  ) => string,
  updateCount: number,
  newPathCount: number,
): string {
  return t(($) => $.a11y.reorg_done, { count: updateCount, paths: newPathCount });
}
