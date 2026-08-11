import type { TypedGraphNode, TypedGraphResponse } from "@multica/core/research";

/** Fields that change star-graph layout signatures (cluster/parent/tier band). */
export function typedGraphNodeLayoutSignature(node: TypedGraphNode): string {
  return [
    (node.level || "").toLowerCase(),
    node.cluster_id ?? "",
    node.parent_id ?? node.derived_from ?? "",
    (node.status || "").toLowerCase(),
  ].join("|");
}

export interface TypedGraphLayoutDiff {
  newNodeIds: string[];
  removedNodeIds: string[];
  changedNodeIds: string[];
  /** Neighbors of changed/new nodes — useful for scoped motion/layout bookkeeping. */
  affectedRootIds: string[];
}

export function diffTypedGraphLayout(
  previous: TypedGraphResponse | undefined,
  next: TypedGraphResponse | undefined,
): TypedGraphLayoutDiff {
  const empty: TypedGraphLayoutDiff = {
    newNodeIds: [],
    removedNodeIds: [],
    changedNodeIds: [],
    affectedRootIds: [],
  };
  if (!next) return empty;

  const prevById = new Map((previous?.nodes ?? []).map((node) => [node.id, node]));
  const nextById = new Map(next.nodes.map((node) => [node.id, node]));

  const newNodeIds: string[] = [];
  const changedNodeIds: string[] = [];
  for (const node of next.nodes) {
    const prior = prevById.get(node.id);
    if (!prior) {
      newNodeIds.push(node.id);
      continue;
    }
    if (typedGraphNodeLayoutSignature(prior) !== typedGraphNodeLayoutSignature(node)) {
      changedNodeIds.push(node.id);
    }
  }

  const removedNodeIds = (previous?.nodes ?? [])
    .filter((node) => !nextById.has(node.id))
    .map((node) => node.id);

  const touched = new Set([...newNodeIds, ...changedNodeIds, ...removedNodeIds]);
  const affectedRootIds = new Set<string>(touched);
  for (const edge of next.edges ?? []) {
    if (touched.has(edge.from_node_id)) affectedRootIds.add(edge.to_node_id);
    if (touched.has(edge.to_node_id)) affectedRootIds.add(edge.from_node_id);
  }

  return {
    newNodeIds,
    removedNodeIds,
    changedNodeIds,
    affectedRootIds: [...affectedRootIds],
  };
}
