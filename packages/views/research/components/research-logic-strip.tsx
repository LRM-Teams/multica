"use client";

import type { ResearchPresenceMap } from "@multica/core/research";
import type { ResearchGraphEdge, ResearchGraphNode } from "@multica/core/types";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n/use-t";
import {
  LOGIC_END_NODE_ID,
  isLogicEndNode,
  isLogicStartNode,
  laneForNode,
  mainPathNodeIds,
  resolveLogicStatus,
} from "../lib/logic-lanes";
import {
  isLowConfidence,
  nodeConfidence,
  nodeIsVisuallyBusy,
  visualForNodeType,
} from "../lib/node-visuals";

/**
 * LRM-908 C8: narrow-screen vertical logic strip — start → path → end,
 * card rows (not git dots). Desktop keeps the full swimlane canvas.
 * LRM-775: presence activity → pulse + caption on related cards.
 * LRM-972: dead_end / conflict / low-confidence dual-coded on strip cards.
 */
export function ResearchLogicStrip({
  nodes,
  edges,
  selectedId,
  onSelect,
  onOpenDelivery,
  presence,
}: {
  nodes: ResearchGraphNode[];
  edges: ResearchGraphEdge[];
  selectedId?: string | null;
  onSelect?: (node: ResearchGraphNode | null) => void;
  onOpenDelivery?: () => void;
  presence?: ResearchPresenceMap;
}) {
  const { t } = useT("research");
  const byId = new Map(nodes.map((n) => [n.id, n]));
  const pathIds = mainPathNodeIds(nodes, edges).filter((id) => byId.has(id));

  const endSynthetic: ResearchGraphNode = {
    id: LOGIC_END_NODE_ID,
    session_id: nodes[0]?.session_id ?? "session",
    node_type: "stage_gate",
    title: t(($) => $.logic.end_title),
    summary: t(($) => $.logic.end_summary),
    status: "pending",
    actor_agent_id: null,
    payload: { logic_role: "end" },
    created_at: new Date().toISOString(),
    updated_at: new Date().toISOString(),
  };

  const items: ResearchGraphNode[] = [
    ...pathIds.map((id) => byId.get(id)!),
    endSynthetic,
  ];

  return (
    <div
      className="flex h-full min-h-0 flex-col overflow-y-auto bg-canvas-bg px-3 py-3"
      data-testid="research-logic-strip"
      aria-label={t(($) => $.logic.strip_label)}
    >
      <div className="mb-3 text-[11px] font-semibold tracking-wide text-muted-foreground uppercase">
        {t(($) => $.logic.label)}
      </div>
      <ol className="relative space-y-0">
        {items.map((node, index) => {
          const status = resolveLogicStatus(node);
          const start = isLogicStartNode(node);
          const end = isLogicEndNode(node);
          const laneId = laneForNode(node);
          const selected = selectedId === node.id || (end && selectedId === LOGIC_END_NODE_ID);
          const presenceLabel = node.actor_agent_id
            ? presence?.[node.actor_agent_id]?.activity?.trim()
            : undefined;
          const presenceBusy = !!presenceLabel;
          const pulse = nodeIsVisuallyBusy(node.status, node.node_type, presenceBusy);
          const visual = visualForNodeType(node.node_type);
          const lowConf = !end && isLowConfidence(nodeConfidence(node));
          const isDeadEnd = node.node_type === "dead_end";
          const isRefuted = node.node_type === "refuted";
          const isConflict = node.node_type === "conflict";
          const title = end
            ? t(($) => $.logic.end_title)
            : isDeadEnd
              ? node.title || t(($) => $.node.dead_end)
              : isConflict
                ? node.title || t(($) => $.node.conflict)
              : start
                ? `${t(($) => $.logic.start)} · ${node.title || t(($) => $.node.goal)}`
                : `${t(($) => $.logic.lane[laneId])} · ${
                    status.key === "done"
                      ? t(($) => $.logic.status.done)
                      : status.key === "running"
                        ? t(($) => $.logic.status.running)
                        : node.title
                  }`;
          const summary = end
            ? t(($) => $.logic.end_summary)
            : node.summary || t(($) => $.logic.status[status.key]);

          return (
            <li key={node.id} className="relative flex gap-3">
              <div className="flex w-4 flex-col items-center">
                <span
                  className={cn(
                    "mt-3 size-2.5 shrink-0 rounded-full",
                    status.tone === "ok" && "bg-success",
                    status.tone === "run" && "bg-brand",
                    status.tone === "wait" && "bg-muted-foreground/45",
                    status.tone === "fail" && "bg-destructive",
                    status.tone === "mute" && "bg-muted-foreground/30",
                    (isDeadEnd || isRefuted) && "bg-muted-foreground/35",
                    pulse && "motion-safe:animate-pulse",
                  )}
                  aria-hidden
                />
                {index < items.length - 1 ? (
                  <span
                    className={cn(
                      "my-1 w-0.5 flex-1 bg-border",
                      (isDeadEnd || isRefuted) && "opacity-40",
                    )}
                    aria-hidden
                  />
                ) : null}
              </div>
              <button
                type="button"
                onClick={() => {
                  if (end) {
                    onOpenDelivery?.();
                    onSelect?.(endSynthetic);
                    return;
                  }
                  onSelect?.(node);
                }}
                className={cn(
                  "mb-2 min-w-0 flex-1 rounded-xl border bg-card px-3 py-2.5 text-left shadow-sm transition-colors",
                  selected && "border-brand ring-2 ring-brand/30",
                  !selected &&
                    !lowConf &&
                    !isDeadEnd &&
                    !isRefuted &&
                    !isConflict &&
                    "border-border hover:bg-muted/30",
                  pulse && "motion-safe:animate-pulse motion-safe:[animation-duration:2.2s]",
                  presenceBusy &&
                    "shadow-[0_0_18px_color-mix(in_oklch,var(--brand)_28%,transparent)]",
                  // LRM-793 / LRM-972: sunk muted dead-end / refuted / conflict shells
                  (isDeadEnd || isRefuted || isConflict) && visual.shellClass,
                  // Low confidence = dashed + text (dual-coded)
                  lowConf && !isDeadEnd && !isRefuted && "border-dashed border-warning/70",
                )}
                data-node-type={end ? "logic_end" : node.node_type}
                data-low-confidence={lowConf ? "true" : undefined}
                data-presence-busy={presenceBusy ? "true" : undefined}
                data-testid={
                  start
                    ? "research-logic-strip-start"
                    : end
                      ? "research-logic-strip-end"
                      : isDeadEnd
                        ? "research-node-dead-end"
                        : isConflict
                          ? "research-node-conflict"
                          : lowConf
                            ? "research-node-low-confidence"
                            : presenceBusy
                              ? "research-node-presence-busy"
                              : "research-logic-strip-card"
                }
              >
                <div
                  className={cn(
                    "text-sm font-semibold text-foreground",
                    isRefuted && "line-through decoration-muted-foreground/70",
                  )}
                >
                  {title}
                </div>
                <p className="mt-0.5 line-clamp-2 text-[11px] text-muted-foreground">{summary}</p>
                {lowConf ? (
                  <p
                    data-testid="research-node-low-confidence-label"
                    className="mt-1 text-[10.5px] font-medium text-warning"
                  >
                    {t(($) => $.node.low_confidence)}
                  </p>
                ) : null}
                {isDeadEnd ? (
                  <p className="mt-1 text-[10.5px] font-medium text-muted-foreground">
                    {t(($) => $.node.dead_end)}
                  </p>
                ) : null}
                {presenceLabel ? (
                  <p
                    data-testid="research-node-presence-caption"
                    className="mt-1 truncate text-[10px] font-medium text-primary"
                  >
                    {presenceLabel}
                  </p>
                ) : null}
              </button>
            </li>
          );
        })}
      </ol>
    </div>
  );
}
