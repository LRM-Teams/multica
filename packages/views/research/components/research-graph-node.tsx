"use client";

import { memo, useState } from "react";
import { Handle, Position, type NodeProps, type Node } from "@xyflow/react";
import { MoreHorizontal } from "lucide-react";
import { cn } from "@multica/ui/lib/utils";
import { useWorkspaceId } from "@multica/core/hooks";
import type { ResearchFlowNodeData } from "../lib/layout-graph";
import { buildNodeAccessibleName } from "../lib/canvas-keyboard-nav";
import {
  isLogicEndNode,
  isLogicStartNode,
  resolveLogicStatus,
  type LogicLaneId,
} from "../lib/logic-lanes";
import { isAbandonedStatus } from "../lib/abandon-reason";
import { nodeIsVisuallyBusy } from "../lib/node-visuals";
import { useAgentActivityProjection } from "../../agents/use-agent-live-status";
import { isCompactActivityLabel } from "../../channels/components/is-compact-activity-label";
import { useT } from "../../i18n/use-t";
import { ResearchNodeActionRing } from "./research-node-action-ring";
import type { NodeRingAction } from "../lib/node-action-ring";
import { ResearchNodeContentFaces } from "./research-node-content-faces";

export type ResearchFlowNode = Node<ResearchFlowNodeData, "research">;

function formatPhaseTime(iso: string | undefined): string {
  if (!iso) return "";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "";
  const mm = String(d.getMonth() + 1).padStart(2, "0");
  const dd = String(d.getDate()).padStart(2, "0");
  return `${mm}-${dd}`;
}

 type ExecutionSummary = { agent?: string | null; status?: string | null; duration?: string | null; failure?: string | null };

function executionSummary(payload: unknown): ExecutionSummary | null {
  if (!payload || typeof payload !== "object" || Array.isArray(payload)) return null;
  const execution = (payload as { execution?: unknown }).execution;
  if (!execution || typeof execution !== "object" || Array.isArray(execution)) return null;
  return execution as ExecutionSummary;
}

function ResearchGraphNodeComponent({ data, selected }: NodeProps<ResearchFlowNode>) {
  const { t } = useT("research");
  const wsId = useWorkspaceId();
  const n = data.research;
  const [commandState, setCommandState] = useState<{ pending: NodeRingAction | null; error: string | null }>({ pending: null, error: null });
  const actorId = n?.actor_agent_id ?? undefined;
  const projection = useAgentActivityProjection(wsId, actorId);
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
  const execution = executionSummary(n.payload);

  const title =
    logicRole === "end" ? t(($) => $.logic.end_title) : n.title || t(($) => $.logic.lane[laneId]);

  const ariaLabel = buildNodeAccessibleName(n);
  const aggregateTier = data.aggregateTier;
  const aggregateSize = data.aggregateSize;
  const nodeSizeStyle = aggregateSize
    ? { width: aggregateSize.width, height: aggregateSize.height }
    : undefined;

  const menuOpen = !!data.menuOpen;
  const abandoned = isAbandonedStatus(n.status);
  const runAction = (action: NodeRingAction) => {
    if (action === "detail" || action === "locate_source") {
      data.onViewDetail?.(n);
      data.onMenuOpenChange?.(false);
      return;
    }
    if (action === "copy_prompt") {
      void navigator.clipboard?.writeText(n.summary || n.title);
      data.onMenuOpenChange?.(false);
      return;
    }
    if (action === "reassign" && !window.confirm(t(($) => $.ring.reassign_confirm))) return;
    if (!data.onNodeCommand) return;
    setCommandState({ pending: action, error: null });
    void data.onNodeCommand(n, action).then(
      () => { setCommandState({ pending: null, error: null }); data.onMenuOpenChange?.(false); },
      (error: unknown) => setCommandState({ pending: null, error: error instanceof Error ? error.message : t(($) => $.ring.failure) }),
    );
  };

  return (
    <div
      className={cn(
        "research-graph-node-shell relative grid w-full grid-cols-[1fr_auto] gap-x-2 gap-y-1 overflow-hidden rounded-lg border bg-card px-3 py-2.5 text-left transition-colors duration-150",
        // LRM-1332 content face height (112–124); global row gap remains LRM-1295.
        aggregateSize ? "min-h-0 max-h-none" : "min-h-[112px] max-h-[124px]",
        "hover:border-muted-foreground/40",
        // LRM-1333: abandoned = dashed + muted wash (not destructive / strikethrough).
        abandoned && "border-dashed border-muted-foreground/40 bg-muted text-muted-foreground",
        selected &&
          "border-[var(--brand)] ring-2 ring-[color-mix(in_oklch,var(--brand)_18%,transparent)]",
        !abandoned &&
          status.tone === "run" &&
          "border-[color-mix(in_oklch,var(--brand)_45%,var(--border))]",
        !abandoned &&
          status.tone === "fail" &&
          "border-[color-mix(in_oklch,var(--destructive)_40%,var(--border))]",
        pulse && "motion-safe:[&_[data-status-dot]]:animate-pulse",
      )}
      style={nodeSizeStyle}
      data-logic-role={logicRole}
      data-aggregate-tier={aggregateTier}
      data-lane={laneId}
      data-git-lane={data.gitLane ?? 0}
      data-branch={data.branchId}
      data-node-type={n.node_type}
      data-abandoned={abandoned || undefined}
      data-testid={
        logicRole === "start"
          ? "research-logic-start"
          : logicRole === "end"
            ? "research-logic-end"
            : "research-logic-card"
      }
    >
      {/* Visual port: legacy edges live in the gutter; aggregate edges leave right. */}
      <span
        className={cn(
          "pointer-events-none absolute top-1/2 size-3 -translate-y-1/2 rounded-full border-2 bg-card",
          aggregateTier ? "-right-[22px]" : "-left-[22px]",
        )}
        style={{ borderColor: branchColor }}
        aria-hidden
      />
      <Handle
        type="target"
        position={Position.Left}
        className="!h-2 !w-2 !opacity-0"
      />
      {/* LRM-1105: native button + roving tabindex (avoid nested role=button). */}
      <button
        type="button"
        tabIndex={selected ? 0 : -1}
        aria-label={ariaLabel}
        aria-busy={pulse || undefined}
        className={cn(
          "nodrag nopan col-start-1 row-span-2 grid w-full grid-cols-1 gap-y-1 rounded-md text-left outline-none",
          "focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-[var(--brand)]",
        )}
        onClick={() => data.onViewDetail?.(n)}
      >
        <span className="line-clamp-1 text-sm font-semibold leading-5 text-foreground">
          {title}
        </span>
        {logicRole === "step" ? (
          <ResearchNodeContentFaces node={n} density="surface" />
        ) : null}
        <span className="flex min-w-0 items-center gap-2 text-xs text-muted-foreground">
          <span
            data-status-dot
            data-testid={abandoned ? "research-node-abandoned-pill" : undefined}
            aria-hidden={abandoned || undefined}
            className={cn(
              "inline-flex shrink-0 items-center rounded-full px-1.5 py-0.5 text-xs font-medium",
              !abandoned && status.tone === "ok" && "bg-success/15 text-success-strong",
              !abandoned && status.tone === "run" && "bg-brand/15 text-brand",
              !abandoned && status.tone === "wait" && "bg-warning/15 text-warning",
              !abandoned && status.tone === "fail" && "bg-destructive/15 text-destructive",
              (abandoned || status.tone === "mute") && "bg-muted text-muted-foreground",
            )}
          >
            {abandoned
              ? t(($) => $.logic.status.abandoned)
              : t(($) => $.logic.status[status.key])}
          </span>
          <span className="truncate">{ownerLabel}</span>
          <span className="shrink-0 truncate">
            {phaseLabel}
            {updated ? ` · ${updated}` : ""}
          </span>
        </span>
        {execution ? (
          <span className="flex min-w-0 gap-1.5 text-[11px] text-muted-foreground" data-testid="research-node-execution">
            {execution.agent ? <span className="truncate font-medium text-foreground">{execution.agent}</span> : null}
            {execution.status ? <span className="shrink-0">· {execution.status}</span> : null}
            {execution.duration ? <span className="shrink-0">· {execution.duration}</span> : null}
            {execution.failure ? <span className="truncate text-destructive">· {execution.failure}</span> : null}
          </span>
        ) : null}
      </button>
      <button
        type="button"
        className="nodrag nopan col-start-2 row-span-2 self-start inline-flex size-7 items-center justify-center rounded-md text-muted-foreground hover:bg-muted hover:text-foreground"
        aria-label={t(($) => $.card_menu.open)}
        data-testid="research-card-menu-trigger"
        onClick={(e) => {
          e.stopPropagation();
          data.onMenuOpenChange?.(!menuOpen);
        }}
        onPointerDown={(e) => e.stopPropagation()}
      >
        <MoreHorizontal className="size-4" aria-hidden />
      </button>
      {menuOpen ? (
        <ResearchNodeActionRing
          node={n}
          mode="ring"
          onClose={() => data.onMenuOpenChange?.(false)}
          onAction={runAction}
          pendingAction={commandState.pending}
          error={commandState.error}
        />
      ) : null}
      <Handle
        type="source"
        position={Position.Right}
        className="!h-2 !w-2 !opacity-0"
      />
    </div>
  );
}

export const ResearchGraphNode = memo(ResearchGraphNodeComponent);
