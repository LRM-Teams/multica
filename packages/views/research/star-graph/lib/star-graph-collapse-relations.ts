import type {
  StarCanvasViewModel,
  StarRelationView,
} from "./star-canvas-view-model";
import type { StarGraphExpansionTransition } from "./star-graph-expansion";

const COLLAPSE_RELATION_LIMIT = 64;

/**
 * Retains only direct root relations explicitly removed by a server-backed
 * one-layer collapse. It does not traverse, infer ancestry, or invent edges.
 */
export function selectStarGraphCollapseRelations(
  previousModel: StarCanvasViewModel | null,
  currentModel: StarCanvasViewModel,
  transition: StarGraphExpansionTransition | null | undefined,
): readonly StarRelationView[] {
  if (!previousModel || transition?.kind !== "collapse") return [];
  if (!currentModel.entities.some((entity) => entity.id === transition.rootNodeId)) {
    return [];
  }

  const removedNodeIds = new Set(transition.revealedNodeIds);
  const currentRelationIds = new Set(
    currentModel.relations.map((relation) => relation.id),
  );

  return previousModel.relations
    .filter((relation) => {
      if (currentRelationIds.has(relation.id)) return false;
      const fromRoot = relation.fromNodeId === transition.rootNodeId;
      const toRoot = relation.toNodeId === transition.rootNodeId;
      return (
        (fromRoot && removedNodeIds.has(relation.toNodeId)) ||
        (toRoot && removedNodeIds.has(relation.fromNodeId))
      );
    })
    .slice(0, COLLAPSE_RELATION_LIMIT)
    .map((relation) =>
      relation.fromNodeId === transition.rootNodeId
        ? {
            ...relation,
            fromNodeId: relation.toNodeId,
            toNodeId: relation.fromNodeId,
            from: relation.to,
            to: relation.from,
          }
        : relation,
    );
}
