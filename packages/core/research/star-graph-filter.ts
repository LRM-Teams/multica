/**
 * LRM-1497 · D5 star-graph filtering (quick navigation AC).
 *
 * Filtering is pure CLIENT-side presentation: it changes what is SHOWN, never
 * the canonical graph (server-owned via React Query, LRM-1505). This module is
 * a pure, deterministic, unit-testable function over the typed graph — it does
 * not mutate `graph`, does not fabricate nodes, and always reports how many
 * nodes/edges are hidden so the UI can surface a "hidden count" + one-click
 * clear.
 *
 * Filter state itself (rounds/levels/statuses/clusters/focus/validOnly) is
 * client state (Zustand) per the architecture rule; this module only computes
 * visibility from a given filter.
 */

import type { TypedGraphResponse } from "./graph-typed";

/** Structural subset the filter actually reads — keeps it schema-decoupled. */
interface FilterNode {
  id: string;
  level?: string | null;
  round?: number | null;
  status?: string | null;
  cluster_id?: string | null;
}

interface FilterEdge {
  id: string;
  from_node_id: string;
  to_node_id: string;
}

/** The slice of a typed graph the filter consumes. */
export interface StarGraphFilterInput {
  nodes: readonly FilterNode[];
  edges: readonly FilterEdge[];
  lineage: Pick<TypedGraphResponse["lineage"], "superseded" | "invalidated">;
}

/** Nodes dropped by `validOnly` are the superseded / invalidated / retired. */
const RETIRED_STATUSES = new Set([
  "stale",
  "superseded",
  "abandoned",
  "deprecated",
  "superseded_at",
]);

/** Client-only filter state. Empty arrays mean "no restriction on that axis". */
export interface StarGraphFilter {
  /** Keep only nodes whose integration `round` is in this set (empty = all). */
  rounds: number[];
  /** Keep only nodes whose 5-level tier is in this set (empty = all). */
  levels: string[];
  /** Keep only nodes whose canonical status is in this set (empty = all). */
  statuses: string[];
  /** Keep only nodes whose `cluster_id` is in this set (empty = all). */
  clusterIds: string[];
  /**
   * True while the "只看当前有效图" quick action is on: retire superseded /
   * invalidated / abandoned nodes.
   */
  validOnly: boolean;
  /** When set, keep only the focused cluster (null/undefined = no focus). */
  focusClusterId?: string | null;
}

/** An all-open filter (nothing hidden, nothing focused). */
export const EMPTY_STAR_GRAPH_FILTER: StarGraphFilter = {
  rounds: [],
  levels: [],
  statuses: [],
  clusterIds: [],
  validOnly: false,
  focusClusterId: null,
};

export interface StarGraphVisibility {
  /** ids of nodes that survive the filter. */
  visibleNodeIds: Set<string>;
  /** ids of edges whose BOTH endpoints survive the filter. */
  visibleEdgeIds: Set<string>;
  hiddenNodeCount: number;
  hiddenEdgeCount: number;
}

/** Whether this filter hides anything (for "一键清除" enablement / badge). */
export function hasActiveStarGraphFilter(
  filter: StarGraphFilter | StarGraphVisibility,
): boolean {
  if ("visibleNodeIds" in filter) {
    return filter.hiddenNodeCount > 0 || filter.hiddenEdgeCount > 0;
  }
  return (
    filter.rounds.length > 0 ||
    filter.levels.length > 0 ||
    filter.statuses.length > 0 ||
    filter.clusterIds.length > 0 ||
    filter.validOnly ||
    Boolean(filter.focusClusterId)
  );
}

/** Keep a node per `validOnly` — never invent; only consult real lineage. */
function isNodeRetired(
  node: FilterNode,
  graph: StarGraphFilterInput,
): boolean {
  if (RETIRED_STATUSES.has(node.status ?? "")) return true;
  const lineage = graph.lineage;
  if (lineage.superseded[node.id]) return true;
  if (lineage.invalidated[node.id]) return true;
  return false;
}

function nodeMatches(
  node: FilterNode,
  filter: StarGraphFilter,
  graph: StarGraphFilterInput,
): boolean {
  if (filter.validOnly && isNodeRetired(node, graph)) return false;

  if (filter.rounds.length > 0) {
    const round = node.round;
    if (round == null || !filter.rounds.includes(round)) return false;
  }
  if (filter.levels.length > 0 && !filter.levels.includes(node.level ?? "")) {
    return false;
  }
  if (filter.statuses.length > 0 && !filter.statuses.includes(node.status ?? "")) {
    return false;
  }
  if (filter.clusterIds.length > 0) {
    if (!node.cluster_id || !filter.clusterIds.includes(node.cluster_id)) {
      return false;
    }
  }
  if (filter.focusClusterId) {
    if (node.cluster_id !== filter.focusClusterId) return false;
  }
  return true;
}

/**
 * Pure visibility computation. Deterministic; never mutates `graph`. Edges are
 * shown only when BOTH endpoints are visible (dangling edges are hidden, so
 * lines never point at blank space).
 */
export function computeStarGraphVisibility(
  graph: StarGraphFilterInput,
  filter: StarGraphFilter,
): StarGraphVisibility {
  const visibleNodeIds = new Set<string>();
  for (const node of graph.nodes) {
    if (nodeMatches(node, filter, graph)) visibleNodeIds.add(node.id);
  }

  const visibleEdgeIds = new Set<string>();
  for (const edge of graph.edges) {
    if (visibleNodeIds.has(edge.from_node_id) && visibleNodeIds.has(edge.to_node_id)) {
      visibleEdgeIds.add(edge.id);
    }
  }

  return {
    visibleNodeIds,
    visibleEdgeIds,
    hiddenNodeCount: graph.nodes.length - visibleNodeIds.size,
    hiddenEdgeCount: graph.edges.length - visibleEdgeIds.size,
  };
}
