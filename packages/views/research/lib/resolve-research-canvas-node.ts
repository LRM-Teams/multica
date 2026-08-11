import type { TypedGraphNode, TypedGraphResponse } from "@multica/core/research";
import type { ResearchGraphNode } from "@multica/core/types";

/** Map a typed graph node into the snapshot node shape used by detail / keyboard nav. */
export function typedNodeToSnapshotNode(node: TypedGraphNode): ResearchGraphNode {
  const payload =
    node.payload != null && typeof node.payload === "object" && !Array.isArray(node.payload)
      ? (node.payload as Record<string, unknown>)
      : {};

  return {
    id: node.id,
    session_id: node.session_id || "",
    node_type: node.node_type || "",
    title: node.title || "",
    summary: node.summary || "",
    status: node.status || "",
    actor_agent_id: node.actor_agent_id ?? null,
    payload,
    confidence: node.confidence ?? null,
    parent_id: node.parent_id ?? null,
    child_ids: node.child_ids ?? [],
    created_at: node.created_at || "",
    updated_at: node.updated_at || "",
  };
}

/** Snapshot wins when present; otherwise fall back to the typed graph node. */
export function resolveResearchCanvasNode(
  nodeId: string | null | undefined,
  options: {
    snapshotNodes?: readonly ResearchGraphNode[];
    typedGraph?: Pick<TypedGraphResponse, "nodes"> | null;
  },
): ResearchGraphNode | null {
  if (!nodeId) return null;

  const fromSnapshot = options.snapshotNodes?.find((node) => node.id === nodeId);
  if (fromSnapshot) return fromSnapshot;

  const typed = options.typedGraph?.nodes.find((node) => node.id === nodeId);
  return typed ? typedNodeToSnapshotNode(typed) : null;
}

/** Union snapshot + typed-only nodes for keyboard nav and accessible names. */
export function mergeResearchCanvasNodes(
  snapshotNodes: readonly ResearchGraphNode[],
  typedGraph?: Pick<TypedGraphResponse, "nodes"> | null,
): ResearchGraphNode[] {
  const byId = new Map<string, ResearchGraphNode>();
  for (const node of snapshotNodes) {
    byId.set(node.id, node);
  }
  for (const typed of typedGraph?.nodes ?? []) {
    if (!byId.has(typed.id)) {
      byId.set(typed.id, typedNodeToSnapshotNode(typed));
    }
  }
  return Array.from(byId.values());
}
