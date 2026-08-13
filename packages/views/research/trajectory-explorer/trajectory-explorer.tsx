"use client";

import { useCallback, useMemo, useRef, useState } from "react";
import { RefreshCw } from "lucide-react";
import type { ResearchGraphEdge, ResearchGraphNode } from "@multica/core/types";
import { buildTrajectoryLaneLayout } from "@multica/core/research";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n/use-t";
import {
  deriveTrajectoryCommits,
  EMPTY_FILTERS,
  filterNodesForTrajectory,
  type TrajectoryFilters,
} from "./data-adapter";
import { TrajectoryToolbar } from "./trajectory-toolbar";
import { TrajectoryGraph } from "./trajectory-graph";
import { TrajectoryMinimap } from "./trajectory-minimap";
import { TrajectoryDetail } from "./trajectory-detail";

export interface TrajectoryExplorerProps {
  nodes: readonly ResearchGraphNode[];
  edges?: readonly ResearchGraphEdge[];
  /** Session status: completed/archived/done disable interaction. */
  sessionStatus?: string;
  /** Selected node id coming from the canvas/session (bidirectional sync). */
  selectedId?: string | null;
  onSelect: (nodeId: string | null) => void;
  /** Jump back to the canvas and focus the given node. */
  onJumpToCanvas: (nodeId: string) => void;
  /** Open the full node detail (expands summary/sources). */
  onOpenNodeDetail: (nodeId: string) => void;
  loading?: boolean;
  error?: string | null;
  onRetry?: () => void;
}

/**
 * LRM-1480 / UI-06: independent Git multi-lane trajectory explorer view.
 *
 * Replaces the narrative ExplorationRail for the "trajectory" aux panel with a
 * virtualized Git graph: toolbar (filter/sort/zoom) + graph + minimap + detail.
 * Layout is derived read-only from the session snapshot; no display state is
 * ever written back to canonical research data.
 */
export function TrajectoryExplorer({
  nodes,
  edges = [],
  sessionStatus,
  selectedId,
  onSelect,
  onJumpToCanvas,
  onOpenNodeDetail,
  loading,
  error,
  onRetry,
}: TrajectoryExplorerProps) {
  const { t } = useT("research");
  const [filters, setFilters] = useState<TrajectoryFilters>(EMPTY_FILTERS);
  const [zoom, setZoom] = useState(1);
  const [showMinimap, setShowMinimap] = useState(true);
  const [localSelectedId, setLocalSelectedId] = useState<string | null>(null);
  const explorerRef = useRef<HTMLDivElement>(null);

  const isCompleted =
    sessionStatus === "completed" ||
    sessionStatus === "archived" ||
    sessionStatus === "done";

  const effectiveSelectedId =
    selectedId != null ? selectedId : localSelectedId;

  const layout = useMemo(() => {
    const filteredNodes = filterNodesForTrajectory(nodes, filters);
    const visibleIds = new Set(filteredNodes.map((node) => node.id));
    const filteredEdges = edges.filter(
      (edge) => visibleIds.has(edge.from_node_id) && visibleIds.has(edge.to_node_id),
    );
    const commits = deriveTrajectoryCommits(filteredNodes, filteredEdges);
    return buildTrajectoryLaneLayout(commits);
    // Adapter orders by created_at (time); lane-layout preserves that positional
    // order as logical order. A time/logical sort switch is a later slice; both
    // deterministic paths consume the same filtered commit set (AC3 reflow).
  }, [edges, nodes, filters]);

  const selectedNode = useMemo(() => {
    if (!effectiveSelectedId) return null;
    return nodes.find((n) => n.id === effectiveSelectedId) ?? null;
  }, [nodes, effectiveSelectedId]);

  const close = useCallback(() => {
    const closingId = effectiveSelectedId;
    setLocalSelectedId(null);
    onSelect(null);
    requestAnimationFrame(() => {
      const root = explorerRef.current;
      const commit = Array.from(
        root?.querySelectorAll<HTMLElement>("[data-commit-id]") ?? [],
      ).find((candidate) => candidate.dataset.commitId === closingId);
      (commit ?? root)?.focus({ preventScroll: true });
    });
  }, [effectiveSelectedId, onSelect]);

  if (loading) {
    return (
      <div
        data-testid="trajectory-loading"
        className="flex min-h-48 flex-col gap-3 px-3 py-4"
        aria-busy="true"
        aria-label={t((s) => s.trajectory_explorer.loading)}
      >
        <div aria-hidden="true" className="h-8 w-full animate-pulse rounded bg-muted/50" />
        <div aria-hidden="true" className="h-5 w-2/3 animate-pulse rounded bg-muted/50" />
        <div aria-hidden="true" className="h-5 w-1/2 animate-pulse rounded bg-muted/50" />
        <div aria-hidden="true" className="h-40 w-full animate-pulse rounded bg-muted/50" />
      </div>
    );
  }

  if (error) {
    return (
      <div
        data-testid="trajectory-error"
        className="flex min-h-40 flex-col items-center justify-center gap-3 px-3 py-6 text-center"
      >
        <p className="text-sm text-destructive">{error}</p>
        {onRetry ? (
          <button
            type="button"
            data-testid="trajectory-retry"
            onClick={onRetry}
            className="inline-flex items-center gap-1.5 rounded border border-border/60 px-2.5 py-1 text-xs text-foreground hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-brand"
          >
            <RefreshCw className="h-3.5 w-3.5" />
            {t((s) => s.trajectory_explorer.retry)}
          </button>
        ) : null}
      </div>
    );
  }

  if (!nodes || nodes.length === 0) {
    return (
      <div
        data-testid="trajectory-empty"
        className="flex min-h-40 flex-col items-center justify-center gap-2 px-3 py-6 text-center"
      >
        <p className="text-sm font-medium text-foreground">
          {t((s) => s.trajectory_explorer.title)}
        </p>
        <p className="text-xs text-muted-foreground">
          {t((s) => s.trajectory_explorer.empty)}
        </p>
        <p className="text-xs text-muted-foreground">
          {t((s) => s.trajectory_explorer.empty_hint)}
        </p>
      </div>
    );
  }

  return (
    <div
      ref={explorerRef}
      data-testid="trajectory-explorer"
      data-disabled={isCompleted ? "true" : "false"}
      className={cn(
        "flex h-full min-h-0 flex-col overflow-hidden",
        isCompleted ? "opacity-90" : "",
      )}
      aria-label={t((s) => s.trajectory_explorer.title)}
      tabIndex={-1}
      onKeyDown={(event) => {
        if (event.key !== "Escape" || !effectiveSelectedId) return;
        event.preventDefault();
        event.stopPropagation();
        close();
      }}
    >
      <TrajectoryToolbar
        nodes={nodes}
        layout={layout}
        filters={filters}
        zoom={zoom}
        showMinimap={showMinimap}
        onFiltersChange={setFilters}
        onToggleMinimap={() => setShowMinimap((v) => !v)}
        onResetZoom={() => setZoom(1)}
      />
      <div className="relative flex min-h-0 flex-1">
        <TrajectoryGraph
          layout={layout}
          selectedId={effectiveSelectedId}
          onSelect={(id) => {
            setLocalSelectedId(id);
            onSelect(id);
          }}
          onOpenDetail={onOpenNodeDetail}
          className="min-w-0 flex-1"
        />
        {showMinimap ? (
          <TrajectoryMinimap layout={layout} className="w-24 shrink-0" />
        ) : null}
      </div>
      <TrajectoryDetail
        node={selectedNode}
        statusTone={selectedNode ? statusToneOf(selectedNode) : undefined}
        onJumpToCanvas={onJumpToCanvas}
        onClose={close}
        onOpenNodeDetail={onOpenNodeDetail}
      />
    </div>
  );
}

function statusToneOf(node: ResearchGraphNode): string {
  const s = (node.status || "").toLowerCase();
  if (s === "failed" || s === "error") return "failed";
  if (s === "abandoned" || s === "cancelled") return "abandoned";
  if (s === "done" || s === "completed" || s === "resolved") return "ok";
  if (s === "active" || s === "running" || s === "in_progress") return "running";
  return "waiting";
}
