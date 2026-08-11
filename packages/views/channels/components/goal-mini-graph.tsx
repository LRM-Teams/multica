"use client";

import { useMemo } from "react";
import type { WorkGraphDetail, WorkGraphNode } from "@multica/core/types";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n";
import {
  goalNodeVisualState,
  layoutGoalMiniGraph,
  type GoalNodeVisualState,
} from "./goal-mini-graph-layout";

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
