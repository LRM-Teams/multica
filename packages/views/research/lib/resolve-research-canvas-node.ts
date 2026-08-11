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

/** Merge typed payload fields onto a snapshot node for detail rendering. */
export function enrichResearchNodeForDetail(
  node: ResearchGraphNode,
  typedGraph?: Pick<TypedGraphResponse, "nodes"> | null,
): ResearchGraphNode {
  const typed = typedGraph?.nodes.find((entry) => entry.id === node.id);
  if (!typed) return node;

  const fromTyped = typedNodeToSnapshotNode(typed);
  const payload =
    fromTyped.payload && typeof fromTyped.payload === "object"
      ? {
          ...(node.payload && typeof node.payload === "object" && !Array.isArray(node.payload)
            ? (node.payload as Record<string, unknown>)
            : {}),
          ...(fromTyped.payload as Record<string, unknown>),
        }
      : node.payload;

  return {
    ...node,
    title: fromTyped.title || node.title,
    summary: fromTyped.summary || node.summary,
    status: fromTyped.status || node.status,
    node_type: fromTyped.node_type || node.node_type,
    actor_agent_id: fromTyped.actor_agent_id ?? node.actor_agent_id,
    parent_id: fromTyped.parent_id ?? node.parent_id,
    child_ids: fromTyped.child_ids?.length ? fromTyped.child_ids : node.child_ids,
    confidence: fromTyped.confidence ?? node.confidence,
    payload,
    created_at: fromTyped.created_at || node.created_at,
    updated_at: fromTyped.updated_at || node.updated_at,
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
