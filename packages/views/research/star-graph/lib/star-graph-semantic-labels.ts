import type { StarEntityView } from "./star-canvas-view-model";

const MIN_SCREEN_DIAMETER: Readonly<Record<string, number>> = {
  m: 76,
  l: 104,
  xl: 132,
  xxl: 156,
};

const TIER_PRIORITY: Readonly<Record<string, number>> = {
  xxl: 0,
  xl: 1,
  l: 2,
  m: 3,
};

interface LabelRect {
  left: number;
  right: number;
  top: number;
  bottom: number;
}

/**
 * Select readable M+ labels in screen space. This is presentation only: it
 * never changes node identity, tier, visibility, or the canonical graph.
 */
export function selectSemanticLabelNodeIds(
  entities: readonly StarEntityView[],
  options: {
    zoom: number;
    selectedNodeId?: string | null;
    enabled?: boolean;
    collisionGap?: number;
    alwaysVisibleNodeIds?: ReadonlySet<string>;
  },
): Set<string> {
  const landmarks = entities.filter((entity) => entity.tier !== "s");
  if (options.enabled === false) {
    return new Set(landmarks.map((entity) => entity.id));
  }

  const zoom = Math.max(options.zoom, 0);
  const candidates = landmarks
    .filter((entity) => {
      if (entity.id === options.selectedNodeId) return true;
      if (options.alwaysVisibleNodeIds?.has(entity.id)) return true;
      const threshold = MIN_SCREEN_DIAMETER[entity.tier] ?? Infinity;
      return entity.radius * 2 * zoom >= threshold;
    })
    .sort((left, right) => {
      if (left.id === options.selectedNodeId) return -1;
      if (right.id === options.selectedNodeId) return 1;
      return (
        (TIER_PRIORITY[left.tier] ?? 9) -
          (TIER_PRIORITY[right.tier] ?? 9) ||
        left.id.localeCompare(right.id)
      );
    });

  const accepted = new Set<string>();
  const occupied: LabelRect[] = [];
  const gap = options.collisionGap ?? 8;

  for (const entity of candidates) {
    const rect = screenLabelRect(entity, zoom, gap);
    const selected = entity.id === options.selectedNodeId;
    if (!selected && occupied.some((other) => overlaps(rect, other))) continue;
    accepted.add(entity.id);
    occupied.push(rect);
  }

  return accepted;
}

function screenLabelRect(
  entity: StarEntityView,
  zoom: number,
  gap: number,
): LabelRect {
  const halfWidth = entity.label.halfWidth * zoom + gap / 2;
  const halfHeight = entity.label.halfHeight * zoom + gap / 2;
  const x = entity.x * zoom;
  const y = entity.y * zoom;
  return {
    left: x - halfWidth,
    right: x + halfWidth,
    top: y - halfHeight,
    bottom: y + halfHeight,
  };
}

function overlaps(left: LabelRect, right: LabelRect): boolean {
  return !(
    left.right <= right.left ||
    left.left >= right.right ||
    left.bottom <= right.top ||
    left.top >= right.bottom
  );
}
