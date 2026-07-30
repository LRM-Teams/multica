import dagre from "@dagrejs/dagre";
import type { Edge, Node } from "@xyflow/react";
import type { ResearchGraphEdge, ResearchGraphNode } from "@multica/core/types";

export const RESEARCH_NODE_WIDTH = 260;
/** Approximate rendered card height (avatar + title + 2-line summary). */
export const RESEARCH_NODE_HEIGHT = 120;

export type ResearchFlowNodeData = {
  research: ResearchGraphNode;
  /** Live presence caption from research presence map (optional overlay). */
  presenceLabel?: string;
  /** Count of high-weight sources feeding a finding (optional badge). */
  sourceBadgeCount?: number;
};

export type ResearchFlowEdgeData = {
  edgeType: string;
};

/**
 * Layered top-down layout for research exploration graphs.
 * Positions are client-only (not persisted).
 */
export function layoutResearchGraph(
  nodes: ResearchGraphNode[],
  edges: ResearchGraphEdge[],
): { nodes: Node<ResearchFlowNodeData>[]; edges: Edge<ResearchFlowEdgeData>[] } {
  const g = new dagre.graphlib.Graph();
  g.setDefaultEdgeLabel(() => ({}));
  g.setGraph({ rankdir: "TB", nodesep: 56, ranksep: 100, marginx: 32, marginy: 32 });

  for (const n of nodes) {
    g.setNode(n.id, { width: RESEARCH_NODE_WIDTH, height: RESEARCH_NODE_HEIGHT });
  }
  for (const e of edges) {
    if (g.hasNode(e.from_node_id) && g.hasNode(e.to_node_id)) {
      g.setEdge(e.from_node_id, e.to_node_id);
    }
  }
  dagre.layout(g);

  const rfNodes: Node<ResearchFlowNodeData>[] = nodes.map((n) => {
    const pos = g.node(n.id);
    const x = typeof pos?.x === "number" ? pos.x - RESEARCH_NODE_WIDTH / 2 : 0;
    const y = typeof pos?.y === "number" ? pos.y - RESEARCH_NODE_HEIGHT / 2 : 0;
    return {
      id: n.id,
      type: "research",
      position: { x, y },
      data: { research: n },
      draggable: true,
    };
  });

  const rfEdges: Edge<ResearchFlowEdgeData>[] = edges
    .filter((e) => nodes.some((n) => n.id === e.from_node_id) && nodes.some((n) => n.id === e.to_node_id))
    .map((e) => ({
      id: e.id,
      source: e.from_node_id,
      target: e.to_node_id,
      type: "smoothstep",
      data: { edgeType: e.edge_type },
    }));

  return { nodes: rfNodes, edges: rfEdges };
}
