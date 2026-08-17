import type { StarEntityView } from "./star-canvas-view-model";

export interface StarGraphFusionTransition {
  sequence: string | number;
  /** Exact successor stable id declared by the committed Projection update. */
  successorNodeId: string;
  /** Exact absorbed source stable ids declared by the committed update. */
  sourceNodeIds: readonly string[];
}

export interface StarGraphFusionGhost {
  id: string;
  tier: StarEntityView["tier"];
  state: StarEntityView["view"]["state"];
  x: number;
  y: number;
  radius: number;
  translateX: number;
  translateY: number;
}

/**
 * Build presentation-only ghosts from caller-declared source/successor ids.
 * Missing nodes are skipped; this function never derives absorption from
 * topology, titles, tiers, proximity, or lifecycle state.
 */
export function buildStarGraphFusionGhosts(
  previousEntities: readonly StarEntityView[],
  currentEntities: readonly StarEntityView[],
  transition: StarGraphFusionTransition | null | undefined,
): StarGraphFusionGhost[] {
  if (!transition) return [];
  const previousById = new Map(previousEntities.map((entity) => [entity.id, entity]));
  const currentById = new Map(currentEntities.map((entity) => [entity.id, entity]));
  const successor = currentById.get(transition.successorNodeId);
  if (!successor) return [];

  const seen = new Set<string>();
  const ghosts: StarGraphFusionGhost[] = [];
  for (const sourceId of transition.sourceNodeIds) {
    if (seen.has(sourceId) || currentById.has(sourceId)) continue;
    seen.add(sourceId);
    const source = previousById.get(sourceId);
    if (!source) continue;
    ghosts.push({
      id: source.id,
      tier: source.tier,
      state: source.view.state,
      x: source.x,
      y: source.y,
      radius: source.radius,
      translateX: successor.x - source.x,
      translateY: successor.y - source.y,
    });
  }
  return ghosts;
}
