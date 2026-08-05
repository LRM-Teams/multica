"use client";

import { useCallback, useMemo, useRef, useState } from "react";
import type { ResearchGraphEdge, ResearchGraphNode, ResearchNodeCommandAction } from "@multica/core/types";
import { cn } from "@multica/ui/lib/utils";
import { MoreHorizontal } from "lucide-react";
import { useT } from "../../i18n/use-t";
import { colorForLane, neighborByLane, neighborByRow } from "../lib/git-topology";
import {
  GIT_GUTTER_WIDTH,
  GIT_LANE_LINE_GAP,
  GIT_MARGIN_TOP,
  GIT_PORT_BASE_X,
  layoutResearchGraph,
} from "../lib/layout-graph";
import { LOGIC_END_NODE_ID, isLogicEndNode, resolveLogicStatus } from "../lib/logic-lanes";
import { NODE_ENTER_CLASS } from "../lib/node-enter-motion";
import { ResearchNodeActionRing } from "./research-node-action-ring";
import type { NodeRingAction } from "../lib/node-action-ring";
import { ResearchNodeContentFacesStack } from "./research-node-content-faces";
import {
  CONTENT_FACE_A11Y_ZH,
  CONTENT_FACE_COPY_ZH,
  CONTENT_FACE_KEYS,
  resolveContentFaceValues,
} from "../lib/research-node-content-faces";

/** LRM-1332 narrow content-face cards; layout-graph GIT_ROW_GAP stays for 1295. */
const CONTENT_FACE_GIT_ROW_MIN = 156;

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
  onRetry: _onRetry,
  onNodeCommand,
  onOpenDetail,
  liveMessage,
}: {
  nodes: ResearchGraphNode[];
  edges: ResearchGraphEdge[];
  selectedId?: string | null;
  onSelect?: (node: ResearchGraphNode | null) => void;
  onOpenDelivery?: () => void;
  onRetry?: (node: ResearchGraphNode) => void;
  onNodeCommand?: (node: ResearchGraphNode, action: ResearchNodeCommandAction) => Promise<void>;
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
  // Local keyboard focus; selectedId from parent wins when still present in the list.
  const [navFocusId, setNavFocusId] = useState<string | null>(null);
  const [menuId, setMenuId] = useState<string | null>(null);
  const [commandState, setCommandState] = useState<{ pending: NodeRingAction | null; error: string | null }>({ pending: null, error: null });
  const listRef = useRef<HTMLDivElement | null>(null);

  const focusId =
    (navFocusId && researchNodes.some((n) => n.id === navFocusId) ? navFocusId : null) ??
    (selectedId && researchNodes.some((n) => n.id === selectedId) ? selectedId : null) ??
    researchNodes[0]?.id ??
    null;

  const focusCard = useCallback(
    (id: string) => {
      setNavFocusId(id);
      const n = researchNodes.find((x) => x.id === id)?.data.research;
      if (n) {
        onSelect?.(n);
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
    [researchNodes, topology, liveMessage, onSelect, t],
  );

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
      role="application"
      tabIndex={-1}
      className="relative h-full min-h-0 overflow-y-auto bg-canvas-bg outline-none"
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
        height={GIT_MARGIN_TOP + researchNodes.length * CONTENT_FACE_GIT_ROW_MIN + 48}
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
              className={cn(
                "relative flex items-center py-1",
                // LRM-1335: the row is keyed by node id, so a newly added node mounts a
                // fresh element and the CSS enter animation plays exactly once. Rows that
                // already exist keep their element and never re-animate — no id bookkeeping.
                NODE_ENTER_CLASS,
                "research-logic-strip-card-enter",
              )}
              style={{ minHeight: CONTENT_FACE_GIT_ROW_MIN }}
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
              <div className="relative grid w-full grid-cols-[1fr_auto] gap-x-2">
                <button
                  type="button"
                  tabIndex={focusId === n.id || (!focusId && index === 0) ? 0 : -1}
                  data-node-id={n.id}
                  data-testid="research-git-list-card"
                  aria-label={(() => {
                    const title =
                      n.id === LOGIC_END_NODE_ID
                        ? t(($) => $.logic.end_title)
                        : n.title;
                    const faces = resolveContentFaceValues(
                      n,
                      "surface",
                      CONTENT_FACE_COPY_ZH,
                    );
                    const faceParts = CONTENT_FACE_KEYS.map(
                      (key) => `${CONTENT_FACE_A11Y_ZH[key]} ${faces[key]}`,
                    ).join("，");
                    return `${title}，${t(($) => $.logic.status[status.key])}，${rf.data.branchId ?? "main"}，${faceParts}`;
                  })()}
                  className={cn(
                    "col-start-1 row-span-2 grid w-full grid-cols-1 gap-y-1 rounded-lg border bg-card px-3 py-2.5 pr-12 text-left",
                    "min-h-[148px] outline-none",
                    "focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-[var(--brand)]",
                    selected &&
                      "border-[var(--brand)] ring-2 ring-[color-mix(in_oklch,var(--brand)_18%,transparent)]",
                    status.tone === "run" &&
                      "border-[color-mix(in_oklch,var(--brand)_45%,var(--border))]",
                    status.tone === "fail" &&
                      "border-[color-mix(in_oklch,var(--destructive)_40%,var(--border))]",
                  )}
                  onClick={() => openNode(n)}
                  onFocus={() => setNavFocusId(n.id)}
                >
                  <div className="line-clamp-1 text-sm font-semibold leading-5">
                    {n.id === LOGIC_END_NODE_ID
                      ? t(($) => $.logic.end_title)
                      : n.title}
                  </div>
                  {n.id !== LOGIC_END_NODE_ID ? (
                    <ResearchNodeContentFacesStack node={n} />
                  ) : null}
                  <div className="flex items-center gap-2 text-xs text-muted-foreground">
                    <span
                      className={cn(
                        "rounded-full px-1.5 py-0.5 text-xs font-medium",
                        status.tone === "ok" && "bg-success/15 text-success-strong",
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
                </button>
                <button
                  type="button"
                  className="absolute top-2.5 right-2 z-10 inline-flex size-11 items-center justify-center rounded-md text-muted-foreground hover:bg-muted"
                  aria-label={t(($) => $.card_menu.open)}
                  onClick={(e) => {
                    e.stopPropagation();
                    setMenuId((prev) => (prev === n.id ? null : n.id));
                  }}
                >
                  <MoreHorizontal className="size-4" aria-hidden />
                </button>
                {menuId === n.id ? (
                  <ResearchNodeActionRing
                    node={n}
                    mode="sheet"
                    onClose={() => setMenuId(null)}
                    pendingAction={commandState.pending}
                    error={commandState.error}
                    onAction={(action) => {
                      if (action === "detail" || action === "locate_source") { openNode(n); setMenuId(null); return; }
                      if (action === "copy_prompt") { void navigator.clipboard?.writeText(n.summary || n.title); setMenuId(null); return; }
                      if (action === "reassign" && !window.confirm(t(($) => $.ring.reassign_confirm))) return;
                      if (!onNodeCommand) return;
                      setCommandState({ pending: action, error: null });
                      void onNodeCommand(n, action).then(
                        () => { setCommandState({ pending: null, error: null }); setMenuId(null); },
                        (error: unknown) => setCommandState({ pending: null, error: error instanceof Error ? error.message : t(($) => $.ring.failure) }),
                      );
                    }}
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
