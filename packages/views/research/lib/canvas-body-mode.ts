import type { ResearchGraphEdge, ResearchGraphNode } from "@multica/core/types";

const EVENT_NODE_TYPES = new Set([
  "roster_change",
  "stage_gate",
  "product_round_gate",
  "agent_activity",
]);

export type CanvasBodyMode = "ready" | "loading" | "forming" | "stalled" | "empty";

type CanvasBodyModeInput = {
  nodes: Pick<ResearchGraphNode, "id" | "node_type">[];
  edges: Pick<ResearchGraphEdge, "from_node_id" | "to_node_id">[];
  sessionStatus?: string | null;
  loading?: boolean;
};

export function hasValidBusinessEdge(
  nodes: CanvasBodyModeInput["nodes"],
  edges: CanvasBodyModeInput["edges"],
): boolean {
  const nodeById = new Map(nodes.map((node) => [node.id, node]));
  return edges.some((edge) => {
    const from = nodeById.get(edge.from_node_id);
    const to = nodeById.get(edge.to_node_id);
    if (!from || !to) return false;
    return [from, to].some(
      (node) => node.node_type !== "goal" && !EVENT_NODE_TYPES.has(node.node_type),
    );
  });
}

export function resolveCanvasBodyMode({ nodes, edges, sessionStatus, loading = false }: CanvasBodyModeInput): CanvasBodyMode {
  if (loading) return "loading";
  if (hasValidBusinessEdge(nodes, edges)) return "ready";
  if (sessionStatus === "paused") return "stalled";
  if (sessionStatus === "running") return "forming";
  return "empty";
}
