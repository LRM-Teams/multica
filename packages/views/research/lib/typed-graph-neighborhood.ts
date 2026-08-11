import type { TypedGraphResponse } from "@multica/core/research";

/** First-order typed-graph neighbors for camera focus and mobile DOM retention. */
export function firstOrderNeighborIds(
  typedGraph: TypedGraphResponse,
  focusId: string,
): Set<string> {
  const ids = new Set<string>([focusId]);
  const typed = typedGraph.nodes.find((node) => node.id === focusId);
  if (typed) {
    for (const id of typed.merged_from ?? []) {
      if (id) ids.add(id);
    }
    if (typed.parent_id) ids.add(typed.parent_id);
    for (const id of typed.child_ids ?? []) {
      if (id) ids.add(id);
    }
  }
  for (const edge of typedGraph.edges) {
    if (edge.from_node_id === focusId) ids.add(edge.to_node_id);
    if (edge.to_node_id === focusId) ids.add(edge.from_node_id);
  }
  return ids;
}
