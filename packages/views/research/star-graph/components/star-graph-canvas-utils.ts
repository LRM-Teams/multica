import type { StarEntityView } from "../lib/star-canvas-view-model";

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

export function fitCameraToBounds(
  bounds: StarGraphBounds,
  viewport: { width: number; height: number },
  padding = 56,
): StarGraphCamera {
  const usableW = Math.max(viewport.width - padding * 2, 1);
  const usableH = Math.max(viewport.height - padding * 2, 1);
  const zoom = clamp(
    Math.min(usableW / bounds.width, usableH / bounds.height, MAX_ZOOM),
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

export function relationEdgeClass(kind: string, edgeType: string): string {
  if (edgeType === "merged_from" || edgeType === "integration_formed") {
    return "sg-edge-merge";
  }
  switch (kind) {
    case "decompose":
      return "sg-edge-decompose";
    case "challenge":
      return "sg-edge-challenge";
    case "newdir":
      return "sg-edge-newdir";
    case "support":
    default:
      return "sg-edge-support";
  }
}

export function quadraticEdgePath(
  from: { x: number; y: number },
  to: { x: number; y: number },
): string {
  const dx = to.x - from.x;
  const dy = to.y - from.y;
  const len = Math.hypot(dx, dy) || 1;
  const bend = Math.min(32, len * 0.09);
  const mx = (from.x + to.x) / 2 - (dy / len) * bend;
  const my = (from.y + to.y) / 2 + (dx / len) * bend;
  return `M ${from.x.toFixed(1)} ${from.y.toFixed(1)} Q ${mx.toFixed(1)} ${my.toFixed(1)} ${to.x.toFixed(1)} ${to.y.toFixed(1)}`;
}

function clamp(value: number, min: number, max: number): number {
  return Math.min(max, Math.max(min, value));
}
