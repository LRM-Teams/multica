import type { TypedGraphNode, TypedGraphResponse } from "@multica/core/research";
import type { ProjectionTransitionEvent } from "../motion/transition-queue";

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

/** Keep motion events whose related ids touch the layout-affected subgraph. */
export function scopeMotionEventsToLayoutDiff(
  events: readonly ProjectionTransitionEvent[],
  diff: TypedGraphLayoutDiff,
): ProjectionTransitionEvent[] {
  if (events.length === 0) return [];
  if (diff.affectedRootIds.length === 0) return [...events];

  const affected = new Set(diff.affectedRootIds);
  const scoped: ProjectionTransitionEvent[] = [];

  for (const event of events) {
    const related = event.related_ids.filter((id) => affected.has(id));
    if (related.length === 0) continue;
    scoped.push({
      ...event,
      related_ids: related,
      anchor_id:
        event.anchor_id && affected.has(event.anchor_id)
          ? event.anchor_id
          : related[0] ?? null,
    });
  }

  return scoped;
}
