import type { StarEntityView } from "./star-canvas-view-model";
import type { ResearchCanvasFilter, ResearchCanvasFilterableNode } from "@multica/core/research";
import { isBlankFilter, matchesResearchCanvasFilter } from "@multica/core/research";

/** D5 desktop hard DOM budget (viewport-performance §3). */
export const STAR_GRAPH_DOM_BUDGET = 220;

/** D5 narrow viewport hard DOM budget (viewport-performance §3). */
export const STAR_GRAPH_MOBILE_DOM_BUDGET = 48;

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
  const baseBudget = options.budget ?? STAR_GRAPH_DOM_BUDGET;
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
>(relations: readonly T[], visibleEntityIds: ReadonlySet<string>): T[] {
  return relations.filter(
    (relation) =>
      visibleEntityIds.has(relation.fromNodeId) &&
      visibleEntityIds.has(relation.toNodeId),
  );
}
