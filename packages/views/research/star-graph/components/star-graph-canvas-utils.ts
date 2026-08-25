import type {
  StarGraphLayoutCluster,
  StarGraphLayoutFrontier,
} from "@multica/core/research";
import type { StarGraphExpansionTransition } from "../lib/star-graph-expansion";
import type {
  StarCanvasViewModel,
  StarEntityView,
} from "../lib/star-canvas-view-model";

export interface StarGraphCamera {
  x: number;
  y: number;
  zoom: number;
}

export interface StarGraphBounds {
  minX: number;
  minY: number;
  maxX: number;
  maxY: number;
  width: number;
  height: number;
  centerX: number;
  centerY: number;
}

const MIN_ZOOM = 0.25;
const MAX_ZOOM = 2;
const MAX_AUTO_FIT_ZOOM = 1;
const MAX_SEMANTIC_FOCUS_ZOOM = 1.45;

const SEMANTIC_FOCUS_DIAMETER: Readonly<Record<string, number>> = {
  m: 132,
  l: 160,
  xl: 190,
  xxl: 210,
};

export function computeEntityBounds(entities: readonly StarEntityView[]): StarGraphBounds | null {
  if (entities.length === 0) return null;
  let minX = Infinity;
  let minY = Infinity;
  let maxX = -Infinity;
  let maxY = -Infinity;
  for (const entity of entities) {
    minX = Math.min(minX, entity.x - entity.radius);
    minY = Math.min(minY, entity.y - entity.radius);
    maxX = Math.max(maxX, entity.x + entity.radius);
    maxY = Math.max(maxY, entity.y + entity.radius);
  }
  const width = Math.max(maxX - minX, 1);
  const height = Math.max(maxY - minY, 1);
  return {
    minX,
    minY,
    maxX,
    maxY,
    width,
    height,
    centerX: (minX + maxX) / 2,
    centerY: (minY + maxY) / 2,
  };
}

/** Include branch territories so auto-fit preserves the intended areas of calm. */
export function computeConstellationBounds(
  entities: readonly StarEntityView[],
  clusters: readonly StarGraphLayoutCluster[],
  frontiers: readonly StarGraphLayoutFrontier[] = [],
): StarGraphBounds | null {
  const entityBounds = computeEntityBounds(entities);
  if (!entityBounds) return null;
  let minX = entityBounds.minX;
  let minY = entityBounds.minY;
  let maxX = entityBounds.maxX;
  let maxY = entityBounds.maxY;
  for (const cluster of clusters) {
    const halfWidth = (cluster.width ?? cluster.radius * 2) / 2;
    const halfHeight = (cluster.height ?? cluster.radius * 2) / 2;
    minX = Math.min(minX, cluster.x - halfWidth);
    minY = Math.min(minY, cluster.y - halfHeight);
    maxX = Math.max(maxX, cluster.x + halfWidth);
    maxY = Math.max(maxY, cluster.y + halfHeight);
  }
  for (const frontier of frontiers) {
    minX = Math.min(minX, frontier.x);
    minY = Math.min(minY, frontier.y);
    maxX = Math.max(maxX, frontier.x + frontier.width);
    maxY = Math.max(maxY, frontier.y + frontier.height);
  }
  const width = Math.max(maxX - minX, 1);
  const height = Math.max(maxY - minY, 1);
  return {
    minX,
    minY,
    maxX,
    maxY,
    width,
    height,
    centerX: (minX + maxX) / 2,
    centerY: (minY + maxY) / 2,
  };
}

export function computeEntityBoundsForIds(
  entities: readonly StarEntityView[],
  ids: ReadonlySet<string>,
): StarGraphBounds | null {
  return computeEntityBounds(entities.filter((entity) => ids.has(entity.id)));
}

export function fitCameraToBounds(
  bounds: StarGraphBounds,
  viewport: { width: number; height: number },
  padding = 56,
): StarGraphCamera {
  const usableW = Math.max(viewport.width - padding * 2, 1);
  const usableH = Math.max(viewport.height - padding * 2, 1);
  const zoom = clamp(
    Math.min(usableW / bounds.width, usableH / bounds.height, MAX_AUTO_FIT_ZOOM),
    MIN_ZOOM,
    MAX_ZOOM,
  );
  return {
    x: viewport.width / 2 - bounds.centerX * zoom,
    y: viewport.height / 2 - bounds.centerY * zoom,
    zoom,
  };
}

export function zoomCamera(
  camera: StarGraphCamera,
  nextZoom: number,
  anchor: { x: number; y: number },
): StarGraphCamera {
  const zoom = clamp(nextZoom, MIN_ZOOM, MAX_ZOOM);
  const scale = zoom / camera.zoom;
  return {
    zoom,
    x: anchor.x - (anchor.x - camera.x) * scale,
    y: anchor.y - (anchor.y - camera.y) * scale,
  };
}

export function zoomPercent(camera: StarGraphCamera): number {
  return Math.round(camera.zoom * 100);
}

/** Pan the camera so a world point sits in the safe band (excluding right panel). */
export function centerCameraOnPoint(
  point: { x: number; y: number },
  viewport: { width: number; height: number },
  camera: StarGraphCamera,
  options: { rightPanelWidth?: number; padding?: number } = {},
): StarGraphCamera {
  const panel = Math.max(0, options.rightPanelWidth ?? 0);
  const padding = options.padding ?? 56;
  const targetX = padding + Math.max(viewport.width - panel - padding * 2, 1) / 2;
  const targetY = viewport.height / 2;
  return {
    zoom: camera.zoom,
    x: targetX - point.x * camera.zoom,
    y: targetY - point.y * camera.zoom,
  };
}

/**
 * Bring an M+ landmark to a readable screen size without undoing a closer
 * camera chosen by the user. S nodes retain the current scale because their
 * detail belongs in the inspector rather than inside the point itself.
 */
export function focusCameraOnEntity(
  entity: Pick<StarEntityView, "x" | "y" | "radius" | "tier">,
  viewport: { width: number; height: number },
  camera: StarGraphCamera,
  options: { rightPanelWidth?: number; padding?: number } = {},
): StarGraphCamera {
  const targetDiameter = SEMANTIC_FOCUS_DIAMETER[entity.tier];
  const semanticZoom = targetDiameter
    ? targetDiameter / Math.max(entity.radius * 2, 1)
    : camera.zoom;
  const zoom = Math.max(
    camera.zoom,
    clamp(semanticZoom, MIN_ZOOM, MAX_SEMANTIC_FOCUS_ZOOM),
  );
  return centerCameraOnPoint(
    { x: entity.x, y: entity.y },
    viewport,
    { ...camera, zoom },
    options,
  );
}

/**
 * Plans camera continuity for one explicit Projection disclosure transaction.
 * It only frames ids named by the transaction and never discovers descendants.
 */
export function planExpansionTransactionCamera(
  model: StarCanvasViewModel,
  transition: StarGraphExpansionTransition | null | undefined,
  viewport: { width: number; height: number },
  camera: StarGraphCamera,
  options: { rightPanelWidth?: number; padding?: number } = {},
): StarGraphCamera | null {
  if (!transition || viewport.width <= 0 || viewport.height <= 0) return null;
  const root = model.entities.find(
    (entity) => entity.id === transition.rootNodeId,
  );
  if (!root) return null;

  if (transition.kind === "collapse") {
    return focusCameraOnEntity(root, viewport, camera, options);
  }

  const revealedIds = new Set(transition.revealedNodeIds);
  const disclosed = model.entities.filter(
    (entity) => entity.id === root.id || revealedIds.has(entity.id),
  );
  if (disclosed.length <= 1) return null;
  const bounds = computeEntityBounds(disclosed);
  if (!bounds) return null;

  const rightPanelWidth = Math.max(0, options.rightPanelWidth ?? 0);
  const safeViewport = {
    width: Math.max(viewport.width - rightPanelWidth, 1),
    height: viewport.height,
  };
  return fitCameraToBounds(bounds, safeViewport, options.padding ?? 72);
}

export function relationEdgeClass(_kind: string, edgeType: string): string {
  if (
    edgeType === "merged_from" ||
    edgeType === "integrates" ||
    edgeType === "integration_formed" ||
    edgeType === "absorbed_into"
  ) {
    return "sg-edge-merge";
  }
  if (DECOMPOSITION_EDGE_TYPES.has(edgeType)) return "sg-edge-decompose";
  if (SUPPORT_EDGE_TYPES.has(edgeType)) return "sg-edge-support";
  if (CHALLENGE_EDGE_TYPES.has(edgeType)) return "sg-edge-challenge";
  if (NEW_DIRECTION_EDGE_TYPES.has(edgeType)) return "sg-edge-newdir";
  // The layout engine still needs a known geometry kind, but visual semantics
  // come from the canonical edge type. Unknown/future relations must not look
  // like supporting evidence merely because layout conservatively used
  // `support` geometry.
  return "sg-edge-neutral";
}

const DECOMPOSITION_EDGE_TYPES = new Set([
  "leads_to",
  "decomposes",
  "depends_on",
  "tests",
  "triggered",
  "produced",
  "consumed",
  "refines",
  "escalated_to",
  "decompose",
  "derived_from",
  "collapsed_path",
  "deepens",
]);
const SUPPORT_EDGE_TYPES = new Set([
  "supports",
  "resolved_by",
  "produced_by",
  "belongs_to",
]);
const CHALLENGE_EDGE_TYPES = new Set([
  "challenged_by",
  "contradicts",
  "invalidates",
  "supersedes",
  "superseded_by",
  "invalidated_by",
  "abandons",
  "challenges",
]);
const NEW_DIRECTION_EDGE_TYPES = new Set(["restart_of"]);

export function quadraticEdgePath(
  from: { x: number; y: number },
  to: { x: number; y: number },
): string {
  const control = quadraticEdgeControl(from, to);
  return [
    `M ${from.x.toFixed(1)} ${from.y.toFixed(1)}`,
    `Q ${control.x.toFixed(1)} ${control.y.toFixed(1)}`,
    `${to.x.toFixed(1)} ${to.y.toFixed(1)}`,
  ].join(" ");
}

function quadraticEdgeControl(
  from: { x: number; y: number },
  to: { x: number; y: number },
): { x: number; y: number } {
  const dx = to.x - from.x;
  const dy = to.y - from.y;
  const len = Math.hypot(dx, dy) || 1;
  const bend = Math.min(32, len * 0.09);
  return {
    x: (from.x + to.x) / 2 - (dy / len) * bend,
    y: (from.y + to.y) / 2 + (dx / len) * bend,
  };
}

export function isEdgeLabelClear(
  relation: {
    fromNodeId: string;
    toNodeId: string;
    from: { x: number; y: number };
    to: { x: number; y: number };
  },
  obstacles: readonly { id: string; x: number; y: number; radius: number }[],
  clearance = 18,
): boolean {
  const control = quadraticEdgeControl(relation.from, relation.to);
  const labelPoint = {
    x: relation.from.x * 0.25 + control.x * 0.5 + relation.to.x * 0.25,
    y: relation.from.y * 0.25 + control.y * 0.5 + relation.to.y * 0.25,
  };
  for (const obstacle of obstacles) {
    if (
      obstacle.id === relation.fromNodeId ||
      obstacle.id === relation.toNodeId
    ) {
      continue;
    }
    if (
      Math.hypot(labelPoint.x - obstacle.x, labelPoint.y - obstacle.y) <
      obstacle.radius + clearance
    ) {
      return false;
    }
  }
  return true;
}

function clamp(value: number, min: number, max: number): number {
  return Math.min(max, Math.max(min, value));
}
