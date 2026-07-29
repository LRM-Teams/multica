"use client";

import { memo } from "react";
import { Handle, Position, type NodeProps, type Node } from "@xyflow/react";
import { cn } from "@multica/ui/lib/utils";
import type { ResearchFlowNodeData } from "../lib/layout-graph";
import { useT } from "../../i18n/use-t";

export type ResearchFlowNode = Node<ResearchFlowNodeData, "research">;

const STATUS_CLASS: Record<string, string> = {
  active: "border-border bg-card shadow-sm",
  done: "border-border/80 bg-muted/50",
  abandoned: "border-destructive/40 bg-destructive/5 opacity-90",
};

const TYPE_RING: Record<string, string> = {
  goal: "ring-2 ring-primary/45",
  subquestion: "ring-1 ring-primary/25",
  probe: "ring-1 ring-sky-500/45",
  finding: "ring-1 ring-emerald-500/45",
  conflict: "ring-1 ring-amber-500/50",
  dead_end: "ring-1 ring-destructive/40",
  refuted: "ring-1 ring-destructive/50",
  pivot: "ring-1 ring-orange-500/50",
  stage_gate: "ring-1 ring-primary/35",
  roster_change: "ring-1 ring-muted-foreground/30",
  agent_activity: "ring-1 ring-violet-500/40",
};

function ResearchGraphNodeComponent({ data, selected }: NodeProps<ResearchFlowNode>) {
  const { t } = useT("research");
  const n = data.research;
  const typeLabel = (() => {
    switch (n.node_type) {
      case "goal":
        return t(($) => $.node.goal);
      case "subquestion":
        return t(($) => $.node.subquestion);
      case "probe":
        return t(($) => $.node.probe);
      case "finding":
        return t(($) => $.node.finding);
      case "conflict":
        return t(($) => $.node.conflict);
      case "dead_end":
        return t(($) => $.node.dead_end);
      case "refuted":
        return t(($) => $.node.refuted);
      case "pivot":
        return t(($) => $.node.pivot);
      case "roster_change":
        return t(($) => $.node.roster_change);
      case "stage_gate":
        return t(($) => $.node.stage_gate);
      case "agent_activity":
        return t(($) => $.node.agent_activity);
      default:
        return n.node_type;
    }
  })();

  return (
    <div
      className={cn(
        "w-[260px] rounded-xl border px-3 py-2.5 text-left transition-shadow",
        STATUS_CLASS[n.status] ?? STATUS_CLASS.active,
        TYPE_RING[n.node_type] ?? "",
        selected && "shadow-md ring-2 ring-primary",
        n.status === "active" && n.node_type !== "goal" && "animate-pulse [animation-duration:2.4s]",
      )}
    >
      <Handle type="target" position={Position.Top} className="!h-2 !w-2 !border-border !bg-muted-foreground/40" />
      <div className="flex items-center justify-between gap-2 text-[10px] uppercase tracking-wide text-muted-foreground">
        <span>{typeLabel}</span>
        <span>{n.status}</span>
      </div>
      <div className="mt-1 truncate text-sm font-semibold text-foreground">{n.title || typeLabel}</div>
      {n.summary ? (
        <p className="mt-1 line-clamp-2 text-[11px] leading-snug text-muted-foreground">{n.summary}</p>
      ) : null}
      <Handle type="source" position={Position.Bottom} className="!h-2 !w-2 !border-border !bg-muted-foreground/40" />
    </div>
  );
}

export const ResearchGraphNode = memo(ResearchGraphNodeComponent);
