import type { StarEntityView } from "./star-canvas-view-model";
import type { ResearchCanvasFilter, ResearchCanvasFilterableNode } from "@multica/core/research";
import { isBlankFilter, matchesResearchCanvasFilter } from "@multica/core/research";

/** D5 desktop hard DOM budget (viewport-performance §3). */
export const STAR_GRAPH_DOM_BUDGET = 220;

/** D5 desktop semantic-node hard limit; entity cards must stop here. */
export const STAR_GRAPH_SEMANTIC_NODE_BUDGET = 180;

/** D5 narrow viewport hard DOM budget (viewport-performance §3). */
export const STAR_GRAPH_MOBILE_DOM_BUDGET = 48;

/** D5 relation-edge hard budgets (viewport-performance §3). */
export const STAR_GRAPH_EDGE_BUDGETS = {
  desktop: 420,
  mid: 220,
  narrow: 96,
} as const;

export function edgeBudgetForViewport(width: number): number {
  if (width >= 1200) return STAR_GRAPH_EDGE_BUDGETS.desktop;
  if (width >= 768) return STAR_GRAPH_EDGE_BUDGETS.mid;
  return STAR_GRAPH_EDGE_BUDGETS.narrow;
}

/** Below this zoom, non-protected nodes collapse to one representative per cluster. */
export const LOW_ZOOM_CLUSTER_COLLAPSE = 0.55;

/** Overview zoom contract: at most 12 full Landmark-style cards. */
export const OVERVIEW_ZOOM_MAX = 0.35;
export const OVERVIEW_LANDMARK_BUDGET = 12;

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

function isProtectedEntity(
  entity: StarEntityView,
  options: {
    rootId: string | null;
    selectedNodeId?: string | null;
    relatedNodeIds?: ReadonlySet<string>;
  },
): boolean {
  if (options.rootId && entity.id === options.rootId) return true;
  if (options.selectedNodeId && entity.id === options.selectedNodeId) return true;
  if (options.relatedNodeIds?.has(entity.id)) return true;
  if (entity.view.state === "run") return true;
  if (entity.view.state === "failed" || entity.view.state === "restart") return true;
  return false;
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
 * Scale the DOM budget down at low zoom so cluster summaries carry detail instead
 * of unreadable 248px circles.
 */
export function effectiveEntityBudget(
  baseBudget: number,
  zoom: number,
): number {
  if (zoom >= 1) return baseBudget;
  if (zoom >= 0.75) return Math.max(60, Math.round(baseBudget * 0.85));
  if (zoom >= LOW_ZOOM_CLUSTER_COLLAPSE) {
    return Math.max(40, Math.round(baseBudget * 0.65));
  }
  if (zoom <= OVERVIEW_ZOOM_MAX) {
    return Math.min(baseBudget, OVERVIEW_LANDMARK_BUDGET);
  }
  return Math.max(24, Math.round(baseBudget * 0.45));
}

/**
 * Select which star-graph entities may mount as DOM nodes under the D5 budget.
 * Protected ids (root, selection, one-hop lineage, running/failed) are kept
 * first; at low zoom only one representative per cluster is retained for the rest.
 */
export function selectVisibleEntityIds(
  entities: readonly StarEntityView[],
  options: {
    rootId: string | null;
    selectedNodeId?: string | null;
    relatedNodeIds?: ReadonlySet<string>;
    budget?: number;
    zoom?: number;
  },
): Set<string> {
  const baseBudget = Math.min(
    options.budget ?? STAR_GRAPH_SEMANTIC_NODE_BUDGET,
    STAR_GRAPH_SEMANTIC_NODE_BUDGET,
  );
  const budget =
    options.zoom != null
      ? effectiveEntityBudget(baseBudget, options.zoom)
      : baseBudget;

  const collapseClusters =
    options.zoom != null && options.zoom < LOW_ZOOM_CLUSTER_COLLAPSE;

  if (!collapseClusters && entities.length <= budget) {
    return new Set(entities.map((entity) => entity.id));
  }

  const ranked = [...entities].sort(
    (a, b) => retentionScore(a, options) - retentionScore(b, options),
  );

  const visible = new Set<string>();
  const clustersRepresented = new Set<string>();

  for (const entity of ranked) {
    // A hard DOM/overview budget is still hard when many nodes are protected.
    // The rank ordering guarantees root → selection → neighbors → active /
    // failure nodes win before ordinary landmarks.
    if (visible.size >= budget) break;

    if (isProtectedEntity(entity, options)) {
      visible.add(entity.id);
      if (entity.clusterId) clustersRepresented.add(entity.clusterId);
      continue;
    }

    if (collapseClusters && entity.clusterId) {
      if (clustersRepresented.has(entity.clusterId)) continue;
      clustersRepresented.add(entity.clusterId);
    }

    visible.add(entity.id);
  }

  return visible;
}

/** Hidden node counts per cluster for low-zoom cluster summary badges. */
export function computeClusterHiddenCounts(
  entities: readonly StarEntityView[],
  visibleEntityIds: ReadonlySet<string>,
): ReadonlyMap<string, number> {
  const totals = new Map<string, number>();
  for (const entity of entities) {
    if (!entity.clusterId) continue;
    totals.set(entity.clusterId, (totals.get(entity.clusterId) ?? 0) + 1);
  }

  const hidden = new Map<string, number>();
  for (const [clusterId, total] of totals) {
    let visibleCount = 0;
    for (const entity of entities) {
      if (entity.clusterId === clusterId && visibleEntityIds.has(entity.id)) {
        visibleCount += 1;
      }
    }
    const hiddenCount = total - visibleCount;
    if (hiddenCount > 0) hidden.set(clusterId, hiddenCount);
  }
  return hidden;
}

/** Apply display-only canvas filter; root/selection/lineage stay visible. */
export function filterEntitiesForCanvasDisplay(
  entities: readonly StarEntityView[],
  options: {
    filter: ResearchCanvasFilter;
    nodeById: ReadonlyMap<string, ResearchCanvasFilterableNode>;
    rootId: string | null;
    selectedNodeId?: string | null;
    relatedNodeIds?: ReadonlySet<string>;
  },
): StarEntityView[] {
  if (isBlankFilter(options.filter)) return [...entities];

  return entities.filter((entity) => {
    if (options.rootId && entity.id === options.rootId) return true;
    if (options.selectedNodeId && entity.id === options.selectedNodeId) return true;
    if (options.relatedNodeIds?.has(entity.id)) return true;

    const canonical = options.nodeById.get(entity.id);
    return matchesResearchCanvasFilter(
      canonical ?? {
        id: entity.id,
        level: entity.tier,
        title: entity.view.title,
        status: entity.view.state,
        cluster_id: entity.clusterId,
      },
      options.filter,
    );
  });
}

export function filterRelationsToVisibleEntities<
  T extends { fromNodeId: string; toNodeId: string },
>(
  relations: readonly T[],
  visibleEntityIds: ReadonlySet<string>,
  options: {
    budget?: number;
    focusNodeId?: string | null;
    relatedNodeIds?: ReadonlySet<string>;
    nodeTierById?: ReadonlyMap<string, string>;
  } = {},
): T[] {
  const visibleRelations = relations.filter(
    (relation) => {
      if (
        !visibleEntityIds.has(relation.fromNodeId) ||
        !visibleEntityIds.has(relation.toNodeId)
      ) {
        return false;
      }

      const hasSTierEndpoint =
        options.nodeTierById?.get(relation.fromNodeId) === "s" ||
        options.nodeTierById?.get(relation.toNodeId) === "s";
      if (!hasSTierEndpoint) return true;

      // S-tier edges stay quiet in the default constellation. Selecting one
      // S node reveals only that node's committed relationships, preventing
      // large runs from turning into an unreadable line mesh.
      return (
        options.focusNodeId != null &&
        options.nodeTierById?.get(options.focusNodeId) === "s" &&
        (relation.fromNodeId === options.focusNodeId ||
          relation.toNodeId === options.focusNodeId)
      );
    },
  );

  const budget = Math.max(
    0,
    Math.floor(options.budget ?? Number.POSITIVE_INFINITY),
  );
  if (visibleRelations.length <= budget) return visibleRelations;

  const priority = (relation: T): number => {
    if (
      options.focusNodeId &&
      (relation.fromNodeId === options.focusNodeId ||
        relation.toNodeId === options.focusNodeId)
    ) {
      return 0;
    }
    if (
      options.relatedNodeIds?.has(relation.fromNodeId) &&
      options.relatedNodeIds.has(relation.toNodeId)
    ) {
      return 1;
    }
    if (
      options.relatedNodeIds?.has(relation.fromNodeId) ||
      options.relatedNodeIds?.has(relation.toNodeId)
    ) {
      return 2;
    }
    return 3;
  };

  return visibleRelations
    .map((relation, index) => ({ relation, index }))
    .sort(
      (left, right) =>
        priority(left.relation) - priority(right.relation) ||
        left.index - right.index,
    )
    .slice(0, budget)
    .map(({ relation }) => relation);
}
