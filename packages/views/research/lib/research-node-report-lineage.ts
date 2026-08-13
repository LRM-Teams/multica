import type { TypedGraphNode } from "@multica/core/research";
import type { ResearchGraphNode } from "@multica/core/types";

export type NodeReportLineageRelation =
  | "derived_from"
  | "merged_from"
  | "parent"
  | "children"
  | "used_by"
  | "restart_of"
  | "superseded_by"
  | "invalidated_by";

export interface NodeReportLineageGroup {
  relation: NodeReportLineageRelation;
  nodeIds: string[];
}

function cleanIds(values: readonly unknown[], selfId: string | undefined): string[] {
  const ids = new Set<string>();
  for (const value of values) {
    if (typeof value !== "string") continue;
    const id = value.trim();
    if (!id || id === selfId) continue;
    ids.add(id);
  }
  return [...ids];
}

function snapshotMergedFrom(node: ResearchGraphNode | null): unknown[] {
  if (!node?.payload || typeof node.payload !== "object") return [];
  const value = (node.payload as { merged_from?: unknown }).merged_from;
  return Array.isArray(value) ? value : [];
}

/** Preserve every canonical typed lineage family exposed by the graph API. */
export function buildNodeReportLineage(
  node: TypedGraphNode | null | undefined,
  snapshotNode: ResearchGraphNode | null,
): NodeReportLineageGroup[] {
  const selfId = node?.id ?? snapshotNode?.id;
  const candidates: Array<[NodeReportLineageRelation, readonly unknown[]]> = [
    ["derived_from", [node?.derived_from]],
    ["merged_from", node?.merged_from?.length ? node.merged_from : snapshotMergedFrom(snapshotNode)],
    ["parent", [node?.parent_id]],
    ["children", node?.child_ids ?? []],
    ["used_by", node?.children_of ?? []],
    ["restart_of", [node?.restart_of]],
    ["superseded_by", [node?.superseded_by]],
    ["invalidated_by", [node?.invalidated_by]],
  ];

  const groups: NodeReportLineageGroup[] = [];
  for (const [relation, values] of candidates) {
    const nodeIds = cleanIds(values, selfId);
    if (nodeIds.length > 0) groups.push({ relation, nodeIds });
  }
  return groups;
}
