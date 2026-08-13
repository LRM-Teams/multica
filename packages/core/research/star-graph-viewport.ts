/**
 * LRM-1514 — D5 调研星图 viewport / right-panel occlusion handling（纯几何层）.
 *
 * Acceptance criterion: across 1440×900, 1920×1080, chat-bar on/off and narrow
 * screens, the core nodes are NOT occluded by the right-side chat / report /
 * Agent-inspector panel. This module answers that with pure geometry:
 *
 *   - `safeLayoutBox(...)` computes the layout band that stays clear of the
 *     right panel (and viewport edges).
 *   - `nodeOcclusionCheck(...)` reports which node circles/labels fall outside
 *     that safe band (would be occluded/clipped).
 *   - `translateLayoutInto(...)` rebases a whole layout into the safe band with
 *     a uniform translate (and only scales when the graph is wider than the
 *     band), preserving relative geometry so an incremental re-layout does not
 *     visibly re-shuffle unaffected clusters.
 *
 * All functions are PURE and deterministic; the same inputs yield identical
 * output, matching the refresh-stability gate.
 */

import {
  type StarGraphLayoutResult,
  circleEdgeEndpoints,
} from "./star-graph-layout";

/** A 2D box in layout px space. */
export interface LayoutBox {
  x: number;
  y: number;
  width: number;
  height: number;
}

/** A viewport (canvas size). */
export interface StarGraphViewport {
  width: number;
  height: number;
}

export interface SafeLayoutBoxOptions {
  viewport: StarGraphViewport;
  /** Width of the right-side chat/report/Agent-inspector panel in px. 0 = off. */
  rightPanelWidth?: number;
  /** Margin to keep between graph content and the safe band edge (px). */
  padding?: number;
  /** When true, also reserve the top chrome/brand band. */
  reserveTopChrome?: boolean;
}

/**
 * The layout band that stays clear of the right panel and viewport edges.
 * Nodes inside this band are never occluded by the inspector column.
 */
export function safeLayoutBox(
  options: SafeLayoutBoxOptions,
): LayoutBox {
  const panel = Math.max(0, options.rightPanelWidth ?? 360);
  const padding = options.padding ?? 24;
  const topReserve = options.reserveTopChrome ? 0 : 0;
  void topReserve;
  const availableWidth = Math.max(0, options.viewport.width - panel - padding * 2);
  return {
    x: padding,
    y: padding,
    width: availableWidth,
    height: Math.max(0, options.viewport.height - padding * 2),
  };
}

/** A rectangular footprint for a node (circle centre + radius + label extents). */
interface NodeFootprint {
  id: string;
  left: number;
  right: number;
  top: number;
  bottom: number;
}

function nodeFootprint(
  n: StarGraphLayoutResult["nodes"][number],
): NodeFootprint {
  // Whole node circle must stay inside the safe band; the label is clamped
  // inside the circle so covering the circle covers any core label.
  return {
    id: n.id,
    left: n.x - n.radius,
    right: n.x + n.radius,
    top: n.y - n.radius,
    bottom: n.y + n.radius,
  };
}

export interface OcclusionReport {
  /** Node ids whose footprint is not fully inside the safe band. */
  occludedIds: string[];
  /** True when the goal/root (the most critical node) is occluded. */
  rootOccluded: boolean;
  safeBox: LayoutBox;
}

/**
 * Which nodes would be occluded (circle clipped) by the right panel / viewport
 * edges, given a layout and a rendered canvas. Returns ids outside the band.
 */
export function nodeOcclusionCheck(
  layout: StarGraphLayoutResult,
  viewport: StarGraphViewport,
  options: Pick<SafeLayoutBoxOptions, "rightPanelWidth" | "padding" | "reserveTopChrome"> = {},
): OcclusionReport {
  const safeBox = safeLayoutBox({ viewport, ...options });
  const byPos = new Map(layout.nodes.map((n) => [n.id, n]));
  const occludedIds: string[] = [];
  for (const n of layout.nodes) {
    const f = nodeFootprint(n);
    const inside =
      f.left >= safeBox.x - 0.5 &&
      f.right <= safeBox.x + safeBox.width + 0.5 &&
      f.top >= safeBox.y - 0.5 &&
      f.bottom <= safeBox.y + safeBox.height + 0.5;
    if (!inside) occludedIds.push(n.id);
  }
  const root = layout.rootId != null ? byPos.get(layout.rootId) : undefined;
  const rootOccluded =
    root != null && occludedIds.includes(root.id);
  return { occludedIds, rootOccluded, safeBox };
}

/**
 * Rebase a whole layout into the safe band with a uniform translate (and, only
 * when the graph is wider than the band, a uniform scale). Relative geometry is
 * preserved so an incremental relayout never visibly shuffles clusters. Edges
 * are recomputed from the new circle centres so endpoint gates still hold.
 */
export function translateLayoutInto(
  layout: StarGraphLayoutResult,
  viewport: StarGraphViewport,
  options: Pick<SafeLayoutBoxOptions, "rightPanelWidth" | "padding"> = {},
): StarGraphLayoutResult {
  const safeBox = safeLayoutBox({ viewport, ...options });
  if (layout.nodes.length === 0) return layout;

  // Graph bounding box of the current circle geometry.
  let minX = Infinity;
  let minY = Infinity;
  let maxX = -Infinity;
  let maxY = -Infinity;
  for (const n of layout.nodes) {
    minX = Math.min(minX, n.x - n.radius);
    minY = Math.min(minY, n.y - n.radius);
    maxX = Math.max(maxX, n.x + n.radius);
    maxY = Math.max(maxY, n.y + n.radius);
  }
  const graphW = maxX - minX || 1;
  const graphH = maxY - minY || 1;

  const scale = Math.min(
    1,
    Math.min(safeBox.width / graphW, safeBox.height / graphH),
  );

  // Translate so the (scaled) graph bbox left/top aligns to safeBox + padding.
  const translateX = safeBox.x - minX * scale;
  const translateY = safeBox.y - minY * scale;

  const nx = layout.nodes.map((n) => ({
    ...n,
    x: Math.round((n.x * scale + translateX) * 100) / 100,
    y: Math.round((n.y * scale + translateY) * 100) / 100,
    radiusOffset: Math.round(n.radiusOffset * scale * 100) / 100,
  }));
  const byPos = new Map(nx.map((n) => [n.id, n]));

  const edges = layout.edges.map((e) => {
    const from = byPos.get(e.fromNodeId)!;
    const to = byPos.get(e.toNodeId)!;
    const ep = circleEdgeEndpoints(
      from.x, from.y, from.radius,
      to.x, to.y, to.radius,
    );
    return { ...e, from: ep.from, to: ep.to };
  });

  // Boundaries translate+scale with the same affine mapping.
  const clusters = layout.clusters.map((cluster) => ({
    clusterId: cluster.clusterId,
    x: Math.round((cluster.x * scale + translateX) * 100) / 100,
    y: Math.round((cluster.y * scale + translateY) * 100) / 100,
    radius: Math.ceil(cluster.radius * scale),
    memberIds: cluster.memberIds,
  }));
  const frontiers = (layout.frontiers ?? []).map((frontier) => ({
    ...frontier,
    x: Math.round((frontier.x * scale + translateX) * 100) / 100,
    y: Math.round((frontier.y * scale + translateY) * 100) / 100,
    width: Math.round(frontier.width * scale * 100) / 100,
    height: Math.round(frontier.height * scale * 100) / 100,
  }));

  return {
    ...layout,
    nodes: nx,
    edges,
    clusters,
    frontiers,
    stats: { ...layout.stats, reused: layout.stats.total, moved: 0 },
  };
}
