"use client";

import { memo, useState } from "react";
import { Handle, Position, type NodeProps, type Node } from "@xyflow/react";
import { MoreHorizontal } from "lucide-react";
import { cn } from "@multica/ui/lib/utils";
import { useWorkspaceId } from "@multica/core/hooks";
import type { ResearchFlowNodeData } from "../lib/layout-graph";
import {
  isLogicEndNode,
  isLogicStartNode,
  resolveLogicStatus,
  type LogicLaneId,
} from "../lib/logic-lanes";
import { nodeIsVisuallyBusy } from "../lib/node-visuals";
import { useAgentActivityProjection } from "../../agents/use-agent-live-status";
import { isCompactActivityLabel } from "../../channels/components/is-compact-activity-label";
import { useT } from "../../i18n/use-t";
import { ResearchCardMenu } from "./research-card-menu";

export type ResearchFlowNode = Node<ResearchFlowNodeData, "research">;

function formatPhaseTime(iso: string | undefined): string {
  if (!iso) return "";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "";
  const mm = String(d.getMonth() + 1).padStart(2, "0");
  const dd = String(d.getDate()).padStart(2, "0");
  return `${mm}-${dd}`;
}

function ResearchGraphNodeComponent({ data, selected }: NodeProps<ResearchFlowNode>) {
  const { t } = useT("research");
  const wsId = useWorkspaceId();
  const n = data.research;
  const actorId = n?.actor_agent_id ?? undefined;
  const projection = useAgentActivityProjection(wsId, actorId);
  const [menuOpen, setMenuOpen] = useState(false);
  if (!n) return null;

  const presenceBusy = !!(data.presenceLabel && data.presenceLabel.trim());
  const actorBusy =
    presenceBusy || !!(projection && isCompactActivityLabel(projection.label));
  const pulse = nodeIsVisuallyBusy(n.status, n.node_type, actorBusy);
  const logicRole =
    data.logicRole ??
    (isLogicEndNode(n) ? "end" : isLogicStartNode(n) ? "start" : "step");
  const status = resolveLogicStatus(n);
  const laneId = (data.laneId ?? "orchestrate") as LogicLaneId;
  const branchColor = data.branchColor ?? "var(--brand)";
  const payloadOwner =
    n.payload &&
    typeof n.payload === "object" &&
    typeof (n.payload as { owner?: unknown }).owner === "string"
      ? (n.payload as { owner: string }).owner
      : null;
  const ownerLabel: string =
    payloadOwner ||
    (actorBusy && projection?.label ? projection.label : "") ||
    "—";
  const payloadPhase =
    n.payload &&
    typeof n.payload === "object" &&
    typeof (n.payload as { phase?: unknown }).phase === "string"
      ? (n.payload as { phase: string }).phase
      : null;
  const phaseLabel: string = payloadPhase || t(($) => $.logic.lane[laneId]);
  const updated = formatPhaseTime(n.updated_at);

  const title =
    logicRole === "end" ? t(($) => $.logic.end_title) : n.title || t(($) => $.logic.lane[laneId]);

  const ariaLabel = [
    title,
    t(($) => $.logic.status[status.key]),
    data.branchId ?? "main",
    ownerLabel,
  ].join(", ");

  return (
    <div
      className={cn(
        "research-graph-node-shell relative grid w-[240px] grid-cols-[1fr_auto] gap-x-2 gap-y-1 overflow-hidden rounded-[10px] border bg-card px-3 py-2.5 text-left shadow-[0_1px_0_rgba(28,25,23,0.04)] transition-[border-color,box-shadow] duration-150",
        "min-h-[68px] max-h-[88px]",
        "hover:border-muted-foreground/40",
        "focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-[var(--brand)]",
        selected &&
          "border-[var(--brand)] shadow-[0_0_0_2px_color-mix(in_oklch,var(--brand)_18%,transparent)]",
        status.tone === "run" &&
          "border-[color-mix(in_oklch,var(--brand)_45%,var(--border))]",
        status.tone === "fail" &&
          "border-[color-mix(in_oklch,var(--destructive)_40%,var(--border))]",
        pulse && "motion-safe:[&_[data-status-dot]]:animate-pulse",
      )}
      data-logic-role={logicRole}
      data-lane={laneId}
      data-git-lane={data.gitLane ?? 0}
      data-branch={data.branchId}
      data-node-type={n.node_type}
      data-testid={
        logicRole === "start"
          ? "research-logic-start"
          : logicRole === "end"
            ? "research-logic-end"
            : "research-logic-card"
      }
      aria-label={ariaLabel}
    >
      {/* Port dot — visual only; edges live in gutter */}
      <span
        className="pointer-events-none absolute top-1/2 -left-[22px] size-3 -translate-y-1/2 rounded-full border-2 bg-card"
        style={{ borderColor: branchColor }}
        aria-hidden
      />
      <Handle
        type="target"
        position={Position.Left}
        className="!h-2 !w-2 !opacity-0"
      />
      <div
        className={cn(
          "col-start-1 line-clamp-2 text-[13px] font-semibold leading-snug text-foreground",
        )}
      >
        {title}
      </div>
      <button
        type="button"
        className="nodrag nopan col-start-2 row-span-2 self-start inline-flex size-7 items-center justify-center rounded-md text-muted-foreground hover:bg-muted hover:text-foreground"
        aria-label={t(($) => $.card_menu.open)}
        data-testid="research-card-menu-trigger"
        onClick={(e) => {
          e.stopPropagation();
          setMenuOpen((v) => !v);
        }}
        onPointerDown={(e) => e.stopPropagation()}
      >
        <MoreHorizontal className="size-4" aria-hidden />
      </button>
      <div className="col-start-1 flex min-w-0 items-center gap-2 text-[11px] text-muted-foreground">
        <span
          data-status-dot
          className={cn(
            "inline-flex shrink-0 items-center rounded-full px-1.5 py-0.5 text-[10px] font-semibold",
            status.tone === "ok" && "bg-success/15 text-success",
            status.tone === "run" && "bg-brand/15 text-brand",
            status.tone === "wait" && "bg-warning/15 text-warning",
            status.tone === "fail" && "bg-destructive/15 text-destructive",
            status.tone === "mute" && "bg-muted text-muted-foreground",
          )}
        >
          {t(($) => $.logic.status[status.key])}
        </span>
        <span className="truncate">{ownerLabel}</span>
        <span className="shrink-0 truncate">
          {phaseLabel}
          {updated ? ` · ${updated}` : ""}
        </span>
      </div>
      {menuOpen ? (
        <ResearchCardMenu
          node={n}
          onClose={() => setMenuOpen(false)}
          onRetry={data.onRetry}
        />
      ) : null}
      <Handle
        type="source"
        position={Position.Left}
        className="!h-2 !w-2 !opacity-0"
      />
    </div>
  );
}

export const ResearchGraphNode = memo(ResearchGraphNodeComponent);
