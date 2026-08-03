"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { ResearchGraphEdge, ResearchGraphNode } from "@multica/core/types";
import { cn } from "@multica/ui/lib/utils";
import { MoreHorizontal } from "lucide-react";
import { useT } from "../../i18n/use-t";
import { colorForLane, neighborByLane, neighborByRow } from "../lib/git-topology";
import {
  GIT_GUTTER_WIDTH,
  GIT_LANE_LINE_GAP,
  GIT_MARGIN_TOP,
  GIT_PORT_BASE_X,
  GIT_ROW_GAP,
  layoutResearchGraph,
} from "../lib/layout-graph";
import { LOGIC_END_NODE_ID, isLogicEndNode, resolveLogicStatus } from "../lib/logic-lanes";
import { ResearchCardMenu } from "./research-card-menu";

/**
 * LRM-1116 narrow (<768): vertical Git list — left colored lines + cards.
 * No pan/zoom/minimap (not a shrunk free canvas).
 */
export function ResearchGitList({
  nodes,
  edges,
  selectedId,
  onSelect,
  onOpenDelivery,
  onRetry,
  onOpenDetail,
  liveMessage,
}: {
  nodes: ResearchGraphNode[];
  edges: ResearchGraphEdge[];
  selectedId?: string | null;
  onSelect?: (node: ResearchGraphNode | null) => void;
  onOpenDelivery?: () => void;
  onRetry?: (node: ResearchGraphNode) => void;
  onOpenDetail?: (node: ResearchGraphNode) => void;
  liveMessage?: (text: string) => void;
}) {
  const { t } = useT("research");
  const laid = useMemo(
    () => layoutResearchGraph(nodes, edges, { includeEnd: true }),
    [nodes, edges],
  );
  const topology = laid.topology;
  const researchNodes = useMemo(
    () =>
      laid.nodes
        .filter((n) => n.type === "research" && n.data.research)
        .sort((a, b) => (a.data.row ?? 0) - (b.data.row ?? 0)),
    [laid.nodes],
  );
  const segments = laid.nodes.find((n) => n.type === "gitGutter")?.data.gutterSegments ?? [];
  const [focusId, setFocusId] = useState<string | null>(researchNodes[0]?.id ?? null);
  const [menuId, setMenuId] = useState<string | null>(null);
  const listRef = useRef<HTMLDivElement | null>(null);

  const focusCard = useCallback(
    (id: string) => {
      setFocusId(id);
      const n = researchNodes.find((x) => x.id === id)?.data.research;
      if (n) {
        liveMessage?.(
          t(($) => $.a11y.focus_node, {
            title: n.title,
            branch: topology.get(id)?.branchId ?? "main",
          }),
        );
      }
      const el = listRef.current?.querySelector<HTMLElement>(`[data-node-id="${id}"]`);
      el?.focus();
      el?.scrollIntoView({ block: "nearest" });
    },
    [researchNodes, topology, liveMessage, t],
  );

  useEffect(() => {
    if (selectedId && researchNodes.some((n) => n.id === selectedId)) {
      setFocusId(selectedId);
    }
  }, [selectedId, researchNodes]);

  const openNode = (node: ResearchGraphNode) => {
    if (isLogicEndNode(node) || node.id === LOGIC_END_NODE_ID) {
      onOpenDelivery?.();
      return;
    }
    onSelect?.(node);
    onOpenDetail?.(node);
    liveMessage?.(t(($) => $.a11y.opened_detail, { title: node.title }));
  };

  return (
    <div
      ref={listRef}
      className="relative h-full min-h-0 overflow-y-auto bg-canvas-bg"
      data-testid="research-git-list"
      aria-label={t(($) => $.logic.git_list_label)}
      onKeyDown={(e) => {
        if (!focusId) return;
        if (e.key === "ArrowDown" || e.key === "ArrowUp") {
          e.preventDefault();
          const next = neighborByRow(
            topology,
            focusId,
            e.key === "ArrowDown" ? 1 : -1,
          );
          if (next) focusCard(next);
        } else if (e.key === "ArrowLeft" || e.key === "ArrowRight") {
          e.preventDefault();
          const next = neighborByLane(
            topology,
            focusId,
            e.key === "ArrowRight" ? 1 : -1,
          );
          if (next) focusCard(next);
        } else if (e.key === "Enter" || e.key === " ") {
          e.preventDefault();
          const n = researchNodes.find((x) => x.id === focusId)?.data.research;
          if (n) openNode(n);
        } else if (e.key === "Escape") {
          setMenuId(null);
        } else if (e.key === "ContextMenu" || (e.key === "F10" && e.shiftKey)) {
          e.preventDefault();
          setMenuId(focusId);
        }
      }}
    >
      <svg
        className="pointer-events-none absolute top-0 left-0"
        width={GIT_GUTTER_WIDTH}
        height={GIT_MARGIN_TOP + researchNodes.length * GIT_ROW_GAP + 48}
        aria-hidden
      >
        {segments.map((seg) => (
          <path
            key={`gl-${seg.lane}`}
            d={seg.d}
            fill="none"
            stroke={seg.color}
            strokeWidth={2}
            strokeLinecap="round"
            strokeLinejoin="round"
          />
        ))}
      </svg>
      <div
        className="relative flex flex-col py-6"
        style={{ paddingLeft: GIT_GUTTER_WIDTH + 8, paddingRight: 12 }}
      >
        {researchNodes.map((rf, index) => {
          const n = rf.data.research!;
          const status = resolveLogicStatus(n);
          const selected = selectedId === n.id;
          const lane = rf.data.gitLane ?? 0;
          const branchColor = rf.data.branchColor ?? colorForLane(lane);
          return (
            <div
              key={n.id}
              className="relative flex items-center"
              style={{ height: GIT_ROW_GAP }}
            >
              <span
                className="absolute size-3 rounded-full border-2 bg-card"
                style={{
                  left:
                    GIT_PORT_BASE_X +
                    lane * GIT_LANE_LINE_GAP -
                    GIT_GUTTER_WIDTH -
                    8 -
                    6,
                  borderColor: branchColor,
                }}
                aria-hidden
              />
              <div
                role="button"
                tabIndex={focusId === n.id || (!focusId && index === 0) ? 0 : -1}
                data-node-id={n.id}
                data-testid="research-git-list-card"
                aria-label={`${n.title}, ${t(($) => $.logic.status[status.key])}, ${rf.data.branchId ?? "main"}`}
                className={cn(
                  "relative grid w-full max-w-[320px] grid-cols-[1fr_auto] gap-x-2 gap-y-1 rounded-[10px] border bg-card px-3 py-2.5",
                  "min-h-[68px] max-h-[88px] outline-none",
                  "focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-[var(--brand)]",
                  selected &&
                    "border-[var(--brand)] shadow-[0_0_0_2px_color-mix(in_oklch,var(--brand)_18%,transparent)]",
                  status.tone === "run" &&
                    "border-[color-mix(in_oklch,var(--brand)_45%,var(--border))]",
                  status.tone === "fail" &&
                    "border-[color-mix(in_oklch,var(--destructive)_40%,var(--border))]",
                )}
                onClick={() => openNode(n)}
                onFocus={() => setFocusId(n.id)}
              >
                <div className="col-start-1 line-clamp-2 text-[13px] font-semibold leading-snug">
                  {n.id === LOGIC_END_NODE_ID
                    ? t(($) => $.logic.end_title)
                    : n.title}
                </div>
                <button
                  type="button"
                  className="col-start-2 row-span-2 self-start inline-flex size-11 items-center justify-center rounded-md text-muted-foreground hover:bg-muted"
                  aria-label={t(($) => $.card_menu.open)}
                  onClick={(e) => {
                    e.stopPropagation();
                    setMenuId((prev) => (prev === n.id ? null : n.id));
                  }}
                >
                  <MoreHorizontal className="size-4" aria-hidden />
                </button>
                <div className="col-start-1 flex items-center gap-2 text-[11px] text-muted-foreground">
                  <span
                    className={cn(
                      "rounded-full px-1.5 py-0.5 text-[10px] font-semibold",
                      status.tone === "ok" && "bg-success/15 text-success",
                      status.tone === "run" && "bg-brand/15 text-brand",
                      status.tone === "fail" && "bg-destructive/15 text-destructive",
                      status.tone === "wait" && "bg-warning/15 text-warning",
                      status.tone === "mute" && "bg-muted text-muted-foreground",
                    )}
                  >
                    {t(($) => $.logic.status[status.key])}
                  </span>
                  <span className="truncate">{rf.data.branchId}</span>
                </div>
                {menuId === n.id ? (
                  <ResearchCardMenu
                    node={n}
                    onClose={() => setMenuId(null)}
                    onRetry={onRetry}
                    onViewDetail={(node) => openNode(node)}
                  />
                ) : null}
              </div>
            </div>
          );
        })}
      </div>
    </div>
  );
}
