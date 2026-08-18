import type { StarEntityView, StarCanvasViewModel } from "./star-canvas-view-model";
import type { StarGraphExpansionTransition } from "./star-graph-expansion";

export interface StarGraphCollapseGhost {
  entity: StarEntityView;
  targetX: number;
  targetY: number;
  delayMs: number;
}

const COLLAPSE_GHOST_LIMIT = 48;
const COLLAPSE_STAGGER_MS = 18;
const COLLAPSE_STAGGER_CAP = 8;

/**
 * Selects only the nodes explicitly named by a server-backed collapse
 * transition. Geometry comes from the last committed render and the retained
 * root in the current render; this function never discovers graph ancestry.
 */
export function selectStarGraphCollapseGhosts(
  previousModel: StarCanvasViewModel | null,
  currentModel: StarCanvasViewModel,
  transition: StarGraphExpansionTransition | null | undefined,
): readonly StarGraphCollapseGhost[] {
  if (!previousModel || transition?.kind !== "collapse") return [];

  const root = currentModel.entities.find(
    (entity) => entity.id === transition.rootNodeId,
  );
  if (!root) return [];

  const previousById = new Map(
    previousModel.entities.map((entity) => [entity.id, entity] as const),
  );
  const currentIds = new Set(currentModel.entities.map((entity) => entity.id));

  return [...new Set(transition.revealedNodeIds)]
    .slice(0, COLLAPSE_GHOST_LIMIT)
    .flatMap((nodeId, index): StarGraphCollapseGhost[] => {
      if (currentIds.has(nodeId)) return [];
      const entity = previousById.get(nodeId);
      if (!entity) return [];
      return [{
        entity,
        targetX: root.x,
        targetY: root.y,
        delayMs: Math.min(index, COLLAPSE_STAGGER_CAP) * COLLAPSE_STAGGER_MS,
      }];
    });
}
