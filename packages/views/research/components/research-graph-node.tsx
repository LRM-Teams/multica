"use client";

import { memo } from "react";
import { Handle, Position, type NodeProps, type Node } from "@xyflow/react";
import { cn } from "@multica/ui/lib/utils";
import { useWorkspaceId } from "@multica/core/hooks";
import type { ResearchFlowNodeData } from "../lib/layout-graph";
import { nodeIsVisuallyBusy, visualForNodeType } from "../lib/node-visuals";
import { useAgentActivityProjection } from "../../agents/use-agent-live-status";
import { isCompactActivityLabel } from "../../channels/components/is-compact-activity-label";
import { ActorAvatar } from "../../common/actor-avatar";
import { useT } from "../../i18n/use-t";

export type ResearchFlowNode = Node<ResearchFlowNodeData, "research">;

function ResearchGraphNodeComponent({ data, selected }: NodeProps<ResearchFlowNode>) {
  const { t } = useT("research");
  const wsId = useWorkspaceId();
  const n = data.research;
  const visual = visualForNodeType(n.node_type);
  const actorId = n.actor_agent_id ?? undefined;
  const projection = useAgentActivityProjection(wsId, actorId);
  const actorBusy = !!(projection && isCompactActivityLabel(projection.label));
  const pulse = nodeIsVisuallyBusy(n.status, n.node_type, actorBusy);
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
        "relative w-[260px] overflow-hidden rounded-xl border bg-card/95 text-left shadow-sm backdrop-blur-sm transition-[box-shadow,transform] duration-300",
        n.status === "abandoned" && "opacity-85",
        visual.ringClass,
        selected && "scale-[1.02] shadow-md ring-2 ring-primary",
        pulse && "motion-safe:animate-pulse motion-safe:[animation-duration:2.2s]",
        actorBusy && "shadow-[0_0_0_1px_hsl(var(--primary)/0.35)]",
      )}
    >
      <div className={cn("absolute inset-y-0 left-0 w-1", visual.accentBarClass)} />
      <Handle type="target" position={Position.Top} className="!h-2 !w-2 !border-border !bg-muted-foreground/40" />
      <div className="flex gap-2 px-3 py-2.5 pl-4">
        {actorId ? (
          <div className="pt-0.5" onClick={(e) => e.stopPropagation()}>
            <ActorAvatar
              actorType="agent"
              actorId={actorId}
              size={28}
              enableHoverCard
              showStatusDot
              profileLink
            />
          </div>
        ) : null}
        <div className="min-w-0 flex-1">
          <div className="flex items-center justify-between gap-2 text-[10px] uppercase tracking-wide text-muted-foreground">
            <span>{typeLabel}</span>
            <span>{n.status}</span>
          </div>
          <div className="mt-1 truncate text-sm font-semibold text-foreground">{n.title || typeLabel}</div>
          {n.summary ? (
            <p className="mt-1 line-clamp-2 text-[11px] leading-snug text-muted-foreground">{n.summary}</p>
          ) : null}
          {actorBusy && projection ? (
            <p className="mt-1 truncate text-[10px] font-medium text-primary">{projection.label}</p>
          ) : null}
        </div>
      </div>
      <Handle type="source" position={Position.Bottom} className="!h-2 !w-2 !border-border !bg-muted-foreground/40" />
    </div>
  );
}

export const ResearchGraphNode = memo(ResearchGraphNodeComponent);
