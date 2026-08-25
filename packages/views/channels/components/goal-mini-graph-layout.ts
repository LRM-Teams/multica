import type { WorkGraphEdge, WorkGraphNode } from "@multica/core/types";

/** Compact chip size — readable two-line Chinese without ballooning the goal card. */
export const GOAL_MINI_NODE_WIDTH = 148;
export const GOAL_MINI_NODE_HEIGHT = 44;
const LAYER_GAP_X = 40;
const NODE_GAP_Y = 10;
const CANVAS_PAD_X = 16;
const CANVAS_PAD_Y = 14;

/** Initial columns shown for large graphs; more layers load on demand. */
export const GOAL_MINI_INITIAL_LAYER_BUDGET = 4;

export type GoalNodeVisualState =
  | "pending"
  | "working"
  | "reviewing"
  | "done"
  | "blocked"
  | "error"
  | "stale";

export interface GoalMiniGraphLayoutNode {
  id: string;
  x: number;
  y: number;
  width: number;
  height: number;
  layer: number;
}

export interface GoalMiniGraphLayoutEdge extends WorkGraphEdge {
  path: string;
}

export interface GoalMiniGraphLayout {
  width: number;
  height: number;
  maxLayer: number;
  nodes: GoalMiniGraphLayoutNode[];
  edges: GoalMiniGraphLayoutEdge[];
}

export function goalNodeVisualState(node: WorkGraphNode): GoalNodeVisualState {
  if (
    node.execution_status === "failed" ||
    node.review_status === "rejected" ||
    node.effective_completion === "revoked" ||
    node.validity_status === "invalidated"
  ) {
    return "error";
  }
  if (
    node.effective_completion === "stale" ||
    node.validity_status === "stale" ||
    node.validity_status === "superseded"
  ) {
    return "stale";
  }
  if (node.review_status === "blocked") return "blocked";
  if (node.effective_completion === "satisfied") return "done";
  if (node.review_status === "reviewing") return "reviewing";
  if (node.execution_status === "running") return "working";
  return "pending";
}

function round(value: number): number {
  return Math.round(value * 10) / 10;
}

function edgePath(
  source: GoalMiniGraphLayoutNode,
  target: GoalMiniGraphLayoutNode,
): string {
  const sourceX = source.x + source.width / 2;
  const targetX = target.x - target.width / 2;
  const dx = Math.max(24, (targetX - sourceX) * 0.45);
  return `M ${round(sourceX)} ${source.y} C ${round(sourceX + dx)} ${source.y}, ${round(targetX - dx)} ${target.y}, ${round(targetX)} ${target.y}`;
}

export function layoutGoalMiniGraph(
  nodes: WorkGraphNode[],
  edges: WorkGraphEdge[],
): GoalMiniGraphLayout {
  const nodeById = new Map(nodes.map((node) => [node.id, node]));
  const validEdges = edges.filter(
    (edge) =>
      edge.from_node_id !== edge.to_node_id &&
      nodeById.has(edge.from_node_id) &&
      nodeById.has(edge.to_node_id),
  );
  const indegree = new Map(nodes.map((node) => [node.id, 0]));
  const depth = new Map(nodes.map((node) => [node.id, 0]));
  const outgoing = new Map(nodes.map((node) => [node.id, [] as WorkGraphEdge[]]));
  for (const edge of validEdges) {
    indegree.set(edge.to_node_id, (indegree.get(edge.to_node_id) ?? 0) + 1);
    outgoing.get(edge.from_node_id)?.push(edge);
  }

  const order = new Map(nodes.map((node, index) => [node.id, index]));
  const queue: string[] = [];
  for (const node of nodes) {
    if (indegree.get(node.id) === 0) queue.push(node.id);
  }
  const visited = new Set<string>();
  while (queue.length > 0) {
    const current = queue.shift()!;
    visited.add(current);
    for (const edge of outgoing.get(current) ?? []) {
      depth.set(
        edge.to_node_id,
        Math.max(depth.get(edge.to_node_id) ?? 0, (depth.get(current) ?? 0) + 1),
      );
      const nextDegree = (indegree.get(edge.to_node_id) ?? 1) - 1;
      indegree.set(edge.to_node_id, nextDegree);
      if (nextDegree === 0) queue.push(edge.to_node_id);
    }
    queue.sort((left, right) => (order.get(left) ?? 0) - (order.get(right) ?? 0));
  }

  // Invalid future API data must degrade to a stable first layer, not hang the UI.
  for (const node of nodes) {
    if (!visited.has(node.id)) depth.set(node.id, 0);
  }

  const maxDepth = nodes.length === 0 ? 0 : Math.max(0, ...depth.values());
  const layers = new Map<number, WorkGraphNode[]>();
  for (const node of nodes) {
    const nodeDepth = depth.get(node.id) ?? 0;
    layers.set(nodeDepth, [...(layers.get(nodeDepth) ?? []), node]);
  }
  for (const [layer, layerNodes] of layers) {
    layerNodes.sort((left, right) => (order.get(left.id) ?? 0) - (order.get(right.id) ?? 0));
    layers.set(layer, layerNodes);
  }

  const maxLayerCount = Math.max(1, ...[...layers.values()].map((layerNodes) => layerNodes.length));
  const contentWidth =
    nodes.length === 0
      ? GOAL_MINI_NODE_WIDTH
      : (maxDepth + 1) * GOAL_MINI_NODE_WIDTH + maxDepth * LAYER_GAP_X;
  const contentHeight =
    maxLayerCount * GOAL_MINI_NODE_HEIGHT + Math.max(0, maxLayerCount - 1) * NODE_GAP_Y;
  const width = round(contentWidth + CANVAS_PAD_X * 2);
  const height = round(contentHeight + CANVAS_PAD_Y * 2);

  const positioned: GoalMiniGraphLayoutNode[] = [];
  for (const [layer, layerNodes] of layers) {
    const columnHeight =
      layerNodes.length * GOAL_MINI_NODE_HEIGHT + Math.max(0, layerNodes.length - 1) * NODE_GAP_Y;
    const columnTop = CANVAS_PAD_Y + (contentHeight - columnHeight) / 2;
    layerNodes.forEach((node, index) => {
      positioned.push({
        id: node.id,
        x: round(
          CANVAS_PAD_X +
            GOAL_MINI_NODE_WIDTH / 2 +
            layer * (GOAL_MINI_NODE_WIDTH + LAYER_GAP_X),
        ),
        y: round(
          columnTop + GOAL_MINI_NODE_HEIGHT / 2 + index * (GOAL_MINI_NODE_HEIGHT + NODE_GAP_Y),
        ),
        width: GOAL_MINI_NODE_WIDTH,
        height: GOAL_MINI_NODE_HEIGHT,
        layer,
      });
    });
  }
  positioned.sort((left, right) => (order.get(left.id) ?? 0) - (order.get(right.id) ?? 0));
  const positionById = new Map(positioned.map((node) => [node.id, node]));
  const laidOutEdges = validEdges.map((edge) => {
    const source = positionById.get(edge.from_node_id)!;
    const target = positionById.get(edge.to_node_id)!;
    return {
      ...edge,
      path: edgePath(source, target),
    };
  });

  return {
    width,
    height,
    maxLayer: maxDepth,
    nodes: positioned,
    edges: laidOutEdges,
  };
}

/** Progressive column window — keeps large graphs cheap without changing API payloads. */
export function visibleGoalMiniGraphSlice(
  layout: GoalMiniGraphLayout,
  layerBudget: number,
): { nodes: GoalMiniGraphLayoutNode[]; edges: GoalMiniGraphLayoutEdge[]; hasMore: boolean } {
  const budget = Math.max(1, layerBudget);
  const nodes = layout.nodes.filter((node) => node.layer < budget);
  const visibleIds = new Set(nodes.map((node) => node.id));
  const edges = layout.edges.filter(
    (edge) => visibleIds.has(edge.from_node_id) && visibleIds.has(edge.to_node_id),
  );
  return {
    nodes,
    edges,
    hasMore: layout.maxLayer + 1 > budget,
  };
}
