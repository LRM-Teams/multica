import type { StarEntityView } from "./star-canvas-view-model";

/** D5 desktop hard DOM budget (viewport-performance §3). */
export const STAR_GRAPH_DOM_BUDGET = 220;

const TIER_RANK: Record<string, number> = {
  xxl: 0,
  xl: 1,
  l: 2,
  m: 3,
  s: 4,
};

function tierRank(entity: StarEntityView): number {
  return TIER_RANK[entity.tier] ?? 5;
}

function retentionScore(
  entity: StarEntityView,
  options: {
    rootId: string | null;
    selectedNodeId?: string | null;
    relatedNodeIds?: ReadonlySet<string>;
  },
): number {
  if (options.rootId && entity.id === options.rootId) return -100;
  if (options.selectedNodeId && entity.id === options.selectedNodeId) return -90;
  if (options.relatedNodeIds?.has(entity.id)) return -80;
  if (entity.view.state === "run") return -70;
  if (entity.view.state === "failed" || entity.view.state === "restart") return -60;
  return tierRank(entity) * 10;
}

/**
 * Select which star-graph entities may mount as DOM nodes under the D5 budget.
 * Protected ids (root, selection, one-hop lineage) are kept first; the rest are
 * ranked by tier and runtime state.
 */
export function selectVisibleEntityIds(
  entities: readonly StarEntityView[],
  options: {
    rootId: string | null;
    selectedNodeId?: string | null;
    relatedNodeIds?: ReadonlySet<string>;
    budget?: number;
  },
): Set<string> {
  const budget = options.budget ?? STAR_GRAPH_DOM_BUDGET;
  if (entities.length <= budget) {
    return new Set(entities.map((entity) => entity.id));
  }

  const ranked = [...entities].sort(
    (a, b) => retentionScore(a, options) - retentionScore(b, options),
  );

  const visible = new Set<string>();
  for (const entity of ranked) {
    visible.add(entity.id);
    if (visible.size >= budget) break;
  }
  return visible;
}

export function filterRelationsToVisibleEntities<
  T extends { fromNodeId: string; toNodeId: string },
>(relations: readonly T[], visibleEntityIds: ReadonlySet<string>): T[] {
  return relations.filter(
    (relation) =>
      visibleEntityIds.has(relation.fromNodeId) &&
      visibleEntityIds.has(relation.toNodeId),
  );
}
