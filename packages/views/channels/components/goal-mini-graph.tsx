"use client";

import { useMemo } from "react";
import type { WorkGraphDetail, WorkGraphEdge, WorkGraphNode } from "@multica/core/types";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n";

const VIEWBOX_WIDTH = 360;
const VIEWBOX_HEIGHT = 144;

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
  compact: boolean;
}

export interface GoalMiniGraphLayoutEdge extends WorkGraphEdge {
  path: string;
}

export interface GoalMiniGraphLayout {
  width: number;
  height: number;
  nodes: GoalMiniGraphLayoutNode[];
  edges: GoalMiniGraphLayoutEdge[];
}

const stateClasses: Record<GoalNodeVisualState, { fill: string; stroke: string; text: string; dot: string }> = {
  pending: {
    fill: "fill-muted",
    stroke: "stroke-border",
    text: "fill-muted-foreground",
    dot: "fill-muted-foreground",
  },
  working: {
    fill: "fill-primary/15",
    stroke: "stroke-primary",
    text: "fill-primary",
    dot: "fill-primary",
  },
  reviewing: {
    fill: "fill-brand-soft",
    stroke: "stroke-brand",
    text: "fill-brand",
    dot: "fill-brand",
  },
  done: {
    fill: "fill-success/15",
    stroke: "stroke-success",
    text: "fill-success-strong",
    dot: "fill-success",
  },
  blocked: {
    fill: "fill-warning/15",
    stroke: "stroke-warning",
    text: "fill-warning",
    dot: "fill-warning",
  },
  error: {
    fill: "fill-destructive/15",
    stroke: "stroke-destructive",
    text: "fill-destructive-strong",
    dot: "fill-destructive",
  },
  stale: {
    fill: "fill-warning/10",
    stroke: "stroke-warning/70",
    text: "fill-warning",
    dot: "fill-warning",
  },
};

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
  const queue = nodes.filter((node) => indegree.get(node.id) === 0).map((node) => node.id);
  const visited = new Set<string>();
  while (queue.length > 0) {
    const current = queue.shift()!;
    visited.add(current);
    for (const edge of outgoing.get(current) ?? []) {
      depth.set(edge.to_node_id, Math.max(depth.get(edge.to_node_id) ?? 0, (depth.get(current) ?? 0) + 1));
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

  const maxDepth = Math.max(0, ...depth.values());
  const layers = new Map<number, WorkGraphNode[]>();
  for (const node of nodes) {
    const nodeDepth = depth.get(node.id) ?? 0;
    layers.set(nodeDepth, [...(layers.get(nodeDepth) ?? []), node]);
  }
  const nodeWidth = maxDepth >= 4 ? 58 : maxDepth === 3 ? 68 : 82;
  const horizontalMargin = nodeWidth / 2 + 12;
  const horizontalStep = maxDepth === 0 ? 0 : (VIEWBOX_WIDTH - horizontalMargin * 2) / maxDepth;
  const positioned: GoalMiniGraphLayoutNode[] = [];
  for (const [layer, layerNodes] of layers) {
    const slotHeight = (VIEWBOX_HEIGHT - 20) / Math.max(1, layerNodes.length);
    const nodeHeight = Math.max(12, Math.min(24, slotHeight - 5));
    layerNodes.forEach((node, index) => {
      positioned.push({
        id: node.id,
        x: round(maxDepth === 0 ? VIEWBOX_WIDTH / 2 : horizontalMargin + layer * horizontalStep),
        y: round(10 + slotHeight * (index + 0.5)),
        width: nodeHeight < 18 ? nodeHeight : nodeWidth,
        height: nodeHeight,
        compact: nodeHeight < 18,
      });
    });
  }
  positioned.sort((left, right) => (order.get(left.id) ?? 0) - (order.get(right.id) ?? 0));
  const positionById = new Map(positioned.map((node) => [node.id, node]));
  const laidOutEdges = validEdges.map((edge) => {
    const source = positionById.get(edge.from_node_id)!;
    const target = positionById.get(edge.to_node_id)!;
    const sourceX = source.x + source.width / 2;
    const targetX = target.x - target.width / 2;
    const controlX = sourceX + (targetX - sourceX) / 2;
    return {
      ...edge,
      path: `M ${round(sourceX)} ${source.y} C ${round(controlX)} ${source.y}, ${round(controlX)} ${target.y}, ${round(targetX)} ${target.y}`,
    };
  });

  return { width: VIEWBOX_WIDTH, height: VIEWBOX_HEIGHT, nodes: positioned, edges: laidOutEdges };
}

function shortNodeLabel(node: WorkGraphNode, compact: boolean): string {
  if (compact) return "";
  const label = node.objective.trim() || node.role;
  const limit = 11;
  return label.length > limit ? `${label.slice(0, limit - 1)}…` : label;
}

export function GoalMiniGraph({ graph }: { graph: WorkGraphDetail }) {
  const { t } = useT("channels");
  const layout = useMemo(() => layoutGoalMiniGraph(graph.nodes, graph.edges), [graph.nodes, graph.edges]);
  const nodeById = useMemo(() => new Map(graph.nodes.map((node) => [node.id, node])), [graph.nodes]);
  const stateLabels: Record<GoalNodeVisualState, string> = {
    pending: t(($) => $.goal.graph_status_pending),
    working: t(($) => $.goal.graph_status_working),
    reviewing: t(($) => $.goal.graph_status_reviewing),
    done: t(($) => $.goal.graph_status_done),
    blocked: t(($) => $.goal.graph_status_blocked),
    error: t(($) => $.goal.graph_status_error),
    stale: t(($) => $.goal.graph_status_stale),
  };

  return (
    <div className="rounded-lg border border-border/60 bg-background/70 p-2" data-testid="goal-mini-graph">
      <svg
        className="block h-auto w-full"
        viewBox={`0 0 ${layout.width} ${layout.height}`}
        role="img"
        aria-label={t(($) => $.goal.graph_accessible_label, { count: graph.nodes.length })}
      >
        <g aria-hidden="true" className="fill-none stroke-border/80" strokeWidth="1.5">
          {layout.edges.map((edge) => <path key={edge.id} d={edge.path} vectorEffect="non-scaling-stroke" />)}
        </g>
        {layout.nodes.map((position) => {
          const node = nodeById.get(position.id)!;
          const state = goalNodeVisualState(node);
          const colors = stateClasses[state];
          const label = shortNodeLabel(node, position.compact);
          const description = `${node.objective || node.issue_id} · ${node.role} · ${stateLabels[state]}`;
          return (
            <g key={node.id} data-node-id={node.id} data-state={state} aria-hidden="true">
              <title>{description}</title>
              <rect
                x={position.x - position.width / 2}
                y={position.y - position.height / 2}
                width={position.width}
                height={position.height}
                rx={Math.min(7, position.height / 2)}
                className={cn(colors.fill, colors.stroke, "transition-colors duration-200 motion-reduce:transition-none")}
                strokeDasharray={node.role === "verifier" ? "3 2" : undefined}
                strokeWidth="1.5"
                vectorEffect="non-scaling-stroke"
              />
              {position.compact ? null : (
                <>
                  <circle cx={position.x - position.width / 2 + 9} cy={position.y} r="2.5" className={colors.dot} />
                  <text
                    x={position.x - position.width / 2 + 15}
                    y={position.y}
                    dominantBaseline="middle"
                    className={cn(colors.text, "text-[8px] font-medium")}
                  >
                    {label}
                  </text>
                </>
              )}
            </g>
          );
        })}
      </svg>
      <ol className="sr-only">
        {graph.nodes.map((node) => {
          const state = goalNodeVisualState(node);
          return <li key={node.id}>{node.objective || node.issue_id}: {node.role}, {stateLabels[state]}</li>;
        })}
      </ol>
    </div>
  );
}
