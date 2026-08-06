"use client";

import { memo } from "react";
import { Handle, Position, type NodeProps, type Node } from "@xyflow/react";
import { RotateCcw } from "lucide-react";
import { Button } from "@multica/ui/components/ui/button";
import { cn } from "@multica/ui/lib/utils";
import { useWorkspaceId } from "@multica/core/hooks";
import type { ResearchFlowNodeData } from "../lib/layout-graph";
import {
  isLogicEndNode,
  isLogicStartNode,
  resolveLogicStatus,
  type LogicLaneId,
} from "../lib/logic-lanes";
import { nodeOffersRetry } from "../lib/node-action-ring";
import {
  isLowConfidence,
  nodeConfidence,
  nodeIsVisuallyBusy,
  visualForNodeType,
} from "../lib/node-visuals";
import { useAgentActivityProjection } from "../../agents/use-agent-live-status";
import { isCompactActivityLabel } from "../../channels/components/is-compact-activity-label";
import { ActorAvatar } from "../../common/actor-avatar";
import { useT } from "../../i18n/use-t";

export type ResearchFlowNode = Node<ResearchFlowNodeData, "research">;

function ResearchGraphNodeComponent({ data, selected }: NodeProps<ResearchFlowNode>) {
  const { t } = useT("research");
  const wsId = useWorkspaceId();
  const n = data.research;
  // Hooks must run before any early return — React Doctor blocks conditional hooks.
  const actorId = n?.actor_agent_id ?? undefined;
  const projection = useAgentActivityProjection(wsId, actorId);
  if (!n) return null;

  const visual = visualForNodeType(n.node_type);
  const lowConf = isLowConfidence(nodeConfidence(n));
  const presenceBusy = !!(data.presenceLabel && data.presenceLabel.trim());
  const actorBusy =
    presenceBusy || !!(projection && isCompactActivityLabel(projection.label));
  const pulse = nodeIsVisuallyBusy(n.status, n.node_type, actorBusy);
  const activityCaption =
    (data.presenceLabel && data.presenceLabel.trim()) ||
    (actorBusy && projection ? projection.label : "");
  const logicRole =
    data.logicRole ??
    (isLogicEndNode(n) ? "end" : isLogicStartNode(n) ? "start" : "step");
  const status = resolveLogicStatus(n);
  const laneId = (data.laneId ?? "orchestrate") as LogicLaneId;
  const showRetry = logicRole === "step" && nodeOffersRetry(n) && !!data.onRetry;

  const typeLabel = (() => {
    if (logicRole === "start") return t(($) => $.logic.start);
    if (logicRole === "end") return t(($) => $.logic.end);
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
        return t(($) => $.logic.lane[laneId]);
    }
  })();

  const title =
    logicRole === "end" ? t(($) => $.logic.end_title) : n.title || typeLabel;
  const summary =
    logicRole === "end" ? t(($) => $.logic.end_summary) : n.summary;

  return (
    <div
      className={cn(
        "research-graph-node-shell relative overflow-hidden rounded-xl border bg-card/95 text-left shadow-sm backdrop-blur-sm transition-[box-shadow,transform] duration-300",
        logicRole === "start" || logicRole === "end" ? "w-[216px]" : "w-[200px]",
        n.status === "abandoned" && !visual.shellClass && "opacity-85",
        logicRole === "start" &&
          "border-[color-mix(in_oklch,var(--success)_45%,var(--border))] ring-2 ring-[color-mix(in_oklch,var(--success)_28%,transparent)]",
        logicRole === "end" &&
          "border-[color-mix(in_oklch,var(--brand)_50%,var(--border))] ring-2 ring-brand/25",
        logicRole === "step" && visual.ringClass,
        logicRole === "step" && visual.shellClass,
        // LRM-793 / LRM-972: low confidence = dashed border + text (not color alone).
        logicRole === "step" && lowConf && "border-dashed",
        selected && "scale-[1.02]",
        pulse && "motion-safe:animate-pulse motion-safe:[animation-duration:1.8s]",
        actorBusy && "shadow-[0_0_22px_color-mix(in_oklch,var(--brand)_30%,transparent)]",
        status.tone === "run" &&
          !selected &&
          n.node_type !== "dead_end" &&
          n.node_type !== "refuted" &&
          "shadow-[0_0_0_3px_color-mix(in_oklch,var(--brand)_18%,transparent)]",
      )}
      data-logic-role={logicRole}
      data-lane={laneId}
      data-node-type={n.node_type}
      data-low-confidence={lowConf ? "true" : undefined}
      data-presence-busy={presenceBusy ? "true" : undefined}
      data-testid={
        logicRole === "start"
          ? "research-logic-start"
          : logicRole === "end"
            ? "research-logic-end"
            : n.node_type === "dead_end"
              ? "research-node-dead-end"
              : n.node_type === "conflict"
                ? "research-node-conflict"
                : lowConf
                  ? "research-node-low-confidence"
                  : presenceBusy
                    ? "research-node-presence-busy"
                    : "research-logic-card"
      }
    >
      <div
        className={cn(
          "absolute inset-y-0 left-0 w-1",
          logicRole === "start" && "bg-success",
          logicRole === "end" && "bg-brand",
          logicRole === "step" && visual.accentBarClass,
        )}
      />
      <Handle
        type="target"
        position={Position.Left}
        className="!h-2 !w-2 !border-border !bg-muted-foreground/40"
      />
      <div className="flex gap-2 px-3 py-2.5 pl-4">
        {actorId && logicRole === "step" ? (
          <div
            className="nodrag nopan pt-0.5"
            onClick={(e) => e.stopPropagation()}
            onPointerDown={(e) => e.stopPropagation()}
          >
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
          <div className="flex items-center justify-between gap-2 text-[10px] font-semibold tracking-wide text-muted-foreground uppercase">
            <span
              className={cn(
                "truncate",
                visual.labelTone === "warning" && "text-warning",
                visual.labelTone === "danger" && "text-destructive",
                visual.labelTone === "success" && "text-success",
                visual.labelTone === "info" && "text-brand",
              )}
            >
              {logicRole === "step" && !visual.emphasizeType
                ? t(($) => $.logic.lane[laneId])
                : typeLabel}
            </span>
            <span
              className={cn(
                "shrink-0 rounded px-1.5 py-0.5 text-[9.5px] font-bold normal-case",
                status.tone === "ok" && "bg-success/15 text-success",
                status.tone === "run" && "bg-brand/15 text-brand",
                status.tone === "wait" && "bg-warning/15 text-warning",
                status.tone === "fail" && "bg-destructive/15 text-destructive",
                status.tone === "mute" && "bg-muted text-muted-foreground",
              )}
            >
              {t(($) => $.logic.status[status.key])}
            </span>
          </div>
          <div
            className={cn(
              "mt-1 truncate text-sm font-semibold text-foreground",
              visual.titleClass,
            )}
          >
            {title}
          </div>
          {summary ? (
            <p className="mt-1 line-clamp-2 text-[11px] leading-snug text-muted-foreground">
              {summary}
            </p>
          ) : null}
          {lowConf ? (
            <p
              data-testid="research-node-low-confidence-label"
              className="mt-1.5 text-[10.5px] font-medium text-warning"
            >
              {t(($) => $.node.low_confidence)}
            </p>
          ) : null}
          {activityCaption ? (
            <p
              data-testid="research-node-presence-caption"
              className="mt-1 truncate text-[10px] font-medium text-primary"
            >
              {activityCaption}
            </p>
          ) : null}
          {showRetry ? (
            <Button
              type="button"
              size="sm"
              variant="outline"
              data-testid="research-node-retry"
              className="nodrag nopan mt-1.5 h-7 gap-1 border-destructive/35 bg-destructive/5 px-2 text-[11px] font-semibold text-destructive hover:bg-destructive/10 hover:text-destructive"
              onClick={(e) => {
                e.stopPropagation();
                data.onRetry?.(n);
              }}
              onPointerDown={(e) => e.stopPropagation()}
            >
              <RotateCcw className="size-3 shrink-0" aria-hidden />
              {t(($) => $.node.retry_cta)}
            </Button>
          ) : null}
        </div>
      </div>
      <Handle
        type="source"
        position={Position.Right}
        className="!h-2 !w-2 !border-border !bg-muted-foreground/40"
      />
    </div>
  );
}

export const ResearchGraphNode = memo(ResearchGraphNodeComponent);
