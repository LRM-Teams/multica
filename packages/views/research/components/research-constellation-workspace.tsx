"use client";

import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from "react";
import type { TypedGraphResponse, StarGraphLayoutResult } from "@multica/core/research";
import {
  countHiddenByFilter,
  isBlankFilter,
  useResearchCanvasStore,
  useResearchUiStore,
} from "@multica/core/research";
import type {
  ResearchFleetMember,
  ResearchGraphNode,
  ResearchRunSnapshot,
  ResearchSource,
} from "@multica/core/types";
import type { OpenAgentPanelFn } from "@multica/core/agents";
import { Button } from "@multica/ui/components/ui/button";
import { useIsMobile } from "@multica/ui/hooks/use-mobile";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n/use-t";
import { buildD5SessionCanvasModel } from "../lib/build-d5-session-canvas";
import {
  buildTypedGraphMotionEvents,
  shouldSkipTypedGraphMotionCatchUp,
} from "../lib/build-typed-graph-motion-events";
import { diffTypedGraphLayout, scopeMotionEventsToLayoutDiff } from "../lib/diff-typed-graph-layout";
import { buildD5LensDisplayHints } from "../lib/research-d5-lens-display";
import { buildNodeAccessibleName } from "../lib/canvas-keyboard-nav";
import { summarizeTypedGraph } from "../lib/research-d5-summary";
import {
  mergeResearchCanvasNodes,
  resolveResearchCanvasNode,
} from "../lib/resolve-research-canvas-node";
import { firstOrderNeighborIds } from "../lib/typed-graph-neighborhood";
import type { CanvasBodyMode } from "../lib/canvas-body-mode";
import type { ResearchD5Lens } from "../lib/research-d5-lens";
import type { ExecutionRow } from "../execution-overlay";
import { capTransitionGlowDirectives } from "../motion/glow-budget";
import { semanticMotionCss } from "../motion/directives";
import { useSemanticTransition } from "../motion/use-semantic-transition";
import { StarGraphCanvas } from "../star-graph";
import { TrajectoryExplorer } from "../trajectory-explorer";
import { STAR_GRAPH_DOM_BUDGET, STAR_GRAPH_MOBILE_DOM_BUDGET, selectVisibleEntityIds } from "../star-graph/lib/star-graph-visible-budget";
import { ResearchAgentInspector } from "./research-agent-inspector";
import { ResearchCanvasEmptyState } from "./research-canvas-empty-state";
import { ResearchCanvasForming } from "./research-canvas-forming";
import { ResearchCanvasProjectionMismatch } from "./research-canvas-projection-mismatch";
import { ResearchD5MobileRail, ResearchD5Rail } from "./research-d5-rail";
import { ResearchNodeReportModal } from "./research-node-report-modal";
import "./research-d5-layout.css";

export type ResearchReportController = {
  open: () => void;
  close: () => void;
};

export function ResearchConstellationWorkspace({
  typedGraph,
  typedLoading,
  typedError,
  projectionMismatch = false,
  onRetryTypedGraph,
  retryTypedGraphPending = false,
  snapshotNodeCount = 0,
  typedGraphSessionId,
  typedGraphVersion = null,
  snapshotNodes,
  selectedNode,
  onSelectNode,
  executionRows,
  onOpenAgentPanel,
  canvasMode,
  activeLens,
  onActiveLensChange,
  sessionStatus,
  sources,
  run,
  members,
  formingMode,
  formingStage,
  formingMembers,
  formingTasks,
  formingMessages,
  chatPanel,
  detailPanel,
  composer,
  registerReportController,
  typedGraphHasNextPage = false,
  typedGraphLoadMorePending = false,
  onLoadMoreTypedGraph,
  className,
}: {
  typedGraph: TypedGraphResponse | undefined;
  typedLoading: boolean;
  typedError: boolean;
  projectionMismatch?: boolean;
  onRetryTypedGraph?: () => void;
  retryTypedGraphPending?: boolean;
  snapshotNodeCount?: number;
  typedGraphSessionId?: string;
  typedGraphVersion?: number | null;
  typedGraphHasNextPage?: boolean;
  typedGraphLoadMorePending?: boolean;
  onLoadMoreTypedGraph?: () => void;
  snapshotNodes: ResearchGraphNode[];
  selectedNode: ResearchGraphNode | null;
  onSelectNode: (node: ResearchGraphNode | null) => void;
  executionRows: ExecutionRow[];
  onOpenAgentPanel: OpenAgentPanelFn;
  canvasMode: CanvasBodyMode;
  activeLens: ResearchD5Lens;
  onActiveLensChange?: (lens: ResearchD5Lens) => void;
  sessionStatus?: string;
  sources: ResearchSource[];
  run?: ResearchRunSnapshot;
  members: ResearchFleetMember[];
  formingMode?: "forming" | "stalled";
  formingStage?: string;
  formingMembers?: Parameters<typeof ResearchCanvasForming>[0]["members"];
  formingTasks?: Parameters<typeof ResearchCanvasForming>[0]["tasks"];
  formingMessages?: Parameters<typeof ResearchCanvasForming>[0]["messages"];
  chatPanel: ReactNode;
  detailPanel: ReactNode;
  composer: ReactNode;
  registerReportController?: (controller: ResearchReportController) => void;
  className?: string;
}) {
  const { t } = useT("research");
  const isMobile = useIsMobile();
  const railOpen = useResearchUiStore((s) => s.d5RailOpen);
  const setRailOpen = useResearchUiStore((s) => s.setD5RailOpen);
  const railMode = useResearchUiStore((s) => s.d5RailMode);
  const setRailMode = useResearchUiStore((s) => s.setD5RailMode);
  const canvasFilter = useResearchCanvasStore((s) => s.filter);
  const hostRef = useRef<HTMLDivElement>(null);
  const prevGraphRef = useRef<TypedGraphResponse | undefined>(undefined);
  const previousLayoutRef = useRef<StarGraphLayoutResult | undefined>(undefined);
  const [viewport, setViewport] = useState({ width: 0, height: 0 });
  const [graphLiveMessage, setGraphLiveMessage] = useState("");
  const [inspectorAgentId, setInspectorAgentId] = useState<string | null>(null);
  const [reportOpen, setReportOpen] = useState(false);
  const motion = useSemanticTransition();

  const railWidthBase = viewport.width >= 1200 ? 360 : 320;
  const effectiveRailWidth =
    isMobile || !railOpen ? 0 : railWidthBase;
  const showDesktopRail = !isMobile && railOpen;
  const backgroundInert = reportOpen;

  const reportController = useMemo<ResearchReportController>(
    () => ({
      open: () => {
        if (isMobile) setRailOpen(false);
        setInspectorAgentId(null);
        setReportOpen(true);
      },
      close: () => setReportOpen(false),
    }),
    [isMobile, setRailOpen],
  );

  useEffect(() => {
    registerReportController?.(reportController);
  }, [registerReportController, reportController]);

  useEffect(() => {
    const id = "research-semantic-motion-css";
    if (document.getElementById(id)) return;
    const style = document.createElement("style");
    style.id = id;
    style.textContent = semanticMotionCss();
    document.head.appendChild(style);
  }, []);

  useEffect(() => {
    const node = hostRef.current;
    if (!node) return;
    const observer = new ResizeObserver((entries) => {
      const entry = entries[0];
      if (!entry) return;
      setViewport({
        width: entry.contentRect.width,
        height: entry.contentRect.height,
      });
    });
    observer.observe(node);
    return () => observer.disconnect();
  }, []);

  // react-doctor-disable-next-line react-doctor/no-event-handler -- typed graph arrives from query/WS, not a user event; motion enqueue must react to server graph_version deltas.
  useEffect(() => {
    if (!typedGraph) return;
    if (
      prevGraphRef.current &&
      prevGraphRef.current.session_id &&
      prevGraphRef.current.session_id !== typedGraph.session_id
    ) {
      prevGraphRef.current = typedGraph;
      motion.settleNow();
      return;
    }
    if (!prevGraphRef.current) {
      prevGraphRef.current = typedGraph;
      return;
    }
    const previous = prevGraphRef.current;
    const layoutDiff = diffTypedGraphLayout(previous, typedGraph);
    const events = scopeMotionEventsToLayoutDiff(
      buildTypedGraphMotionEvents(previous, typedGraph),
      layoutDiff,
    );
    const skipMotion = shouldSkipTypedGraphMotionCatchUp(previous, typedGraph, events);

    if (events.length > 0 && !skipMotion) {
      setGraphLiveMessage(
        t(($) => $.d5.graph_live.updating, { version: typedGraph.graph_version }),
      );
    } else if (previous.graph_version !== typedGraph.graph_version) {
      setGraphLiveMessage(
        t(($) => $.d5.graph_live.updated, { version: typedGraph.graph_version }),
      );
    }

    if (skipMotion) {
      motion.settleNow();
    } else {
      for (const event of events) motion.enqueue(event);
    }
    prevGraphRef.current = typedGraph;
  }, [typedGraph, motion.enqueue, motion.settleNow, t]);

  const canvasBuild = useMemo(
    () =>
      buildD5SessionCanvasModel(typedGraph, viewport, {
        rightPanelWidth: effectiveRailWidth,
        previousLayout: previousLayoutRef.current,
      }),
    [typedGraph, viewport, effectiveRailWidth],
  );
  const canvasModel = canvasBuild?.model ?? null;

  // react-doctor-disable-next-line react-doctor/no-event-handler -- incremental layout seed is derived from canvas build output, not a click/key handler.
  useEffect(() => {
    if (canvasBuild?.layoutForNext) {
      previousLayoutRef.current = canvasBuild.layoutForNext;
    }
  }, [canvasBuild?.layoutForNext]);

  const lensHints = useMemo(
    () =>
      buildD5LensDisplayHints(activeLens, typedGraph, canvasModel, {
        filterRound: canvasFilter.round,
      }),
    [activeLens, typedGraph, canvasModel, canvasFilter.round],
  );

  const relatedNodeIds = useMemo(() => {
    if (!typedGraph) return undefined;
    const focusId = selectedNode?.id ?? canvasModel?.rootId ?? null;
    if (!focusId) return undefined;
    return firstOrderNeighborIds(typedGraph, focusId);
  }, [canvasModel?.rootId, selectedNode?.id, typedGraph]);

  const motionDirectives = useMemo(() => {
    if (!canvasModel || motion.queueSize < 0) return undefined;
    const visibleIds = selectVisibleEntityIds(canvasModel.entities, {
      rootId: canvasModel.rootId,
      selectedNodeId: selectedNode?.id ?? null,
      relatedNodeIds,
      budget: isMobile ? STAR_GRAPH_MOBILE_DOM_BUDGET : STAR_GRAPH_DOM_BUDGET,
    });
    const map = new Map<
      string,
      ReturnType<typeof motion.directiveFor>
    >();
    for (const entity of canvasModel.entities) {
      const onScreen = visibleIds.has(entity.id);
      if (onScreen) {
        const directive = motion.directiveFor(entity.id);
        if (directive) {
          map.set(entity.id, directive);
          continue;
        }
      }
      const marker = motion.markerFor(entity.id);
      if (marker) {
        map.set(entity.id, {
          className: marker,
          style: {},
          markerClass: marker,
          dataVerb: "reappear",
          glowDisabled: motion.profile.lowPerformance,
        });
      }
    }
    return capTransitionGlowDirectives(map);
  }, [
    canvasModel,
    isMobile,
    motion.directiveFor,
    motion.markerFor,
    motion.profile.lowPerformance,
    motion.queueSize,
    relatedNodeIds,
    selectedNode?.id,
  ]);

  const canvasNodes = useMemo(
    () => mergeResearchCanvasNodes(snapshotNodes, typedGraph),
    [snapshotNodes, typedGraph],
  );

  const clusterLabels = useMemo(() => {
    const map = new Map<string, string>();
    for (const cluster of typedGraph?.clusters ?? []) {
      const id = cluster.id || cluster.name;
      if (!id) continue;
      const label = (cluster.label || cluster.name || "").trim();
      if (label) map.set(id, label);
    }
    return map;
  }, [typedGraph?.clusters]);

  const summary = useMemo(
    () =>
      summarizeTypedGraph(typedGraph?.nodes ?? [], {
        totalNodeCount: typedGraph?.total_node_count ?? null,
        clusters: typedGraph?.clusters,
      }),
    [typedGraph?.clusters, typedGraph?.nodes, typedGraph?.total_node_count],
  );

  const summaryTitle =
    summary.totalDirections != null
      ? t(($) => $.d5.summary.title_with_total, {
          loaded: summary.loadedDirections,
          total: summary.totalDirections,
        })
      : t(($) => $.d5.summary.title, {
          loaded: summary.loadedDirections,
        });
  const summaryDetail = t(($) => $.d5.summary.detail, {
    stable: summary.stableResults,
    active: summary.activeProbes,
    newDir: summary.newFrontiers,
    stopped: summary.stoppedDirections,
  });
  const filterHiddenCount = useMemo(() => {
    if (!typedGraph?.nodes.length || isBlankFilter(canvasFilter)) return 0;
    return countHiddenByFilter(typedGraph.nodes, canvasFilter).hidden;
  }, [canvasFilter, typedGraph?.nodes]);
  const filterHiddenNote =
    filterHiddenCount > 0
      ? t(($) => $.d5.filter.hidden_count, { count: filterHiddenCount })
      : undefined;

  const nodeAccessibleNames = useMemo(() => {
    const map = new Map<string, string>();
    for (const node of canvasNodes) {
      map.set(node.id, buildNodeAccessibleName(node));
    }
    return map;
  }, [canvasNodes]);

  const mobileNeighborhoodIdList = useMemo((): string[] | undefined => {
    if (!isMobile || !typedGraph) return undefined;
    const focusId = selectedNode?.id ?? canvasModel?.rootId ?? null;
    if (!focusId) return undefined;
    return Array.from(firstOrderNeighborIds(typedGraph, focusId)).toSorted();
  }, [canvasModel?.rootId, isMobile, selectedNode?.id, typedGraph]);

  const mobileNeighborhoodIds = useMemo(
    () => (mobileNeighborhoodIdList ? new Set(mobileNeighborhoodIdList) : undefined),
    [mobileNeighborhoodIdList],
  );

  const inspectorRow =
    inspectorAgentId != null
      ? executionRows.find((row) => row.id === inspectorAgentId) ?? null
      : null;

  const selectedTypedNode =
    selectedNode && typedGraph
      ? typedGraph.nodes.find((node) => node.id === selectedNode.id) ?? null
      : null;

  const handleCanvasSelect = useCallback(
    (nodeId: string) => {
      const resolved = resolveResearchCanvasNode(nodeId, {
        snapshotNodes,
        typedGraph,
      });
      onSelectNode(resolved);
      setRailMode("detail");
      if (isMobile) setRailOpen(true);

      const typedNode = typedGraph?.nodes.find((node) => node.id === nodeId);
      const level = (typedNode?.level || "").toLowerCase();
      if (level === "s" && typedNode?.actor_agent_id) {
        if (isMobile) setRailOpen(false);
        setInspectorAgentId(typedNode.actor_agent_id);
        setReportOpen(false);
        return;
      }
      setInspectorAgentId(null);
      if (level === "l" || level === "xl" || level === "xxl") {
        if (isMobile) setRailOpen(false);
        setReportOpen(true);
      }
    },
    [isMobile, onSelectNode, snapshotNodes, typedGraph],
  );

  const graphRemainingCount =
    typedGraph?.total_node_count != null
      ? Math.max(0, typedGraph.total_node_count - (typedGraph.nodes?.length ?? 0))
      : 0;
  const loadMoreLabel =
    typedGraphHasNextPage && onLoadMoreTypedGraph
      ? typedGraphLoadMorePending
        ? t(($) => $.d5.summary.load_more_pending)
        : t(($) => $.d5.summary.load_more, { count: graphRemainingCount })
      : undefined;

  const handleLineageSelect = useCallback(
    (nodeId: string) => {
      handleCanvasSelect(nodeId);
      setReportOpen(false);
    },
    [handleCanvasSelect],
  );

  const showEmpty = canvasMode === "empty" && !canvasModel;
  const showForming =
    (canvasMode === "forming" || canvasMode === "stalled") && !canvasModel;

  return (
    <div
      className={cn("d5-workspace", className)}
      data-testid="research-constellation-workspace"
      data-d5-lens={activeLens}
      data-d5-rail-open={railOpen ? "true" : "false"}
    >
      <section
        ref={hostRef}
        className="d5-canvas-host"
        data-testid="research-session-canvas-host"
        {...(backgroundInert ? { inert: true } : {})}
      >
        {typedLoading && !canvasModel && !projectionMismatch ? (
          <div className="grid h-full place-items-center text-sm text-muted-foreground">
            {t(($) => $.d5.canvas.loading)}
          </div>
        ) : null}
        {typedError && !canvasModel && !projectionMismatch ? (
          <div
            role="alert"
            className="grid h-full place-items-center px-6 text-center text-sm text-destructive"
          >
            {t(($) => $.d5.canvas.error)}
          </div>
        ) : null}
        {projectionMismatch ? (
          <ResearchCanvasProjectionMismatch
            sessionId={typedGraphSessionId}
            snapshotNodeCount={snapshotNodeCount}
            typedNodeCount={typedGraph?.nodes.length ?? 0}
            graphVersion={typedGraphVersion}
            onRetry={() => onRetryTypedGraph?.()}
            retryPending={retryTypedGraphPending}
          />
        ) : null}
        {canvasModel && !projectionMismatch && activeLens === "lineage" ? (
          <TrajectoryExplorer
            nodes={canvasNodes}
            edges={(typedGraph?.edges ?? []).map((edge) => ({
              id: edge.id,
              session_id: edge.session_id,
              from_node_id: edge.from_node_id,
              to_node_id: edge.to_node_id,
              edge_type: edge.edge_type,
              created_at: edge.created_at,
            }))}
            sessionStatus={sessionStatus}
            selectedId={selectedNode?.id ?? null}
            onSelect={(nodeId) => {
              if (nodeId) handleCanvasSelect(nodeId);
              else onSelectNode(null);
            }}
            onOpenNodeDetail={handleCanvasSelect}
            onJumpToCanvas={(nodeId) => {
              onActiveLensChange?.("relations");
              handleCanvasSelect(nodeId);
            }}
          />
        ) : null}
        {canvasModel && !projectionMismatch && activeLens !== "lineage" ? (
          <StarGraphCanvas
            model={canvasModel}
            selectedNodeId={selectedNode?.id ?? null}
            onSelectNode={handleCanvasSelect}
            onOpenNode={handleCanvasSelect}
            summaryTitle={summaryTitle}
            summaryDetail={summaryDetail}
            filterHiddenNote={filterHiddenNote}
            clusterLabels={clusterLabels}
            lensHints={lensHints}
            motionDirectives={motionDirectives}
            showMapKey
            rightPanelWidth={effectiveRailWidth}
            nodeAccessibleNames={nodeAccessibleNames}
            relatedNodeIds={isMobile ? mobileNeighborhoodIds : relatedNodeIds}
            initialFitEntityIdList={isMobile ? mobileNeighborhoodIdList : undefined}
            entityBudget={isMobile ? STAR_GRAPH_MOBILE_DOM_BUDGET : undefined}
            typedNodes={typedGraph?.nodes}
            hiddenCountLabel={(count) => t(($) => $.d5.cluster_hidden, { count })}
            loadMoreLabel={loadMoreLabel}
            onLoadMore={onLoadMoreTypedGraph}
            loadMorePending={typedGraphLoadMorePending}
            keyboardNav={{
              nodes: canvasNodes,
              edges: (typedGraph?.edges ?? []).map((edge) => ({
                from_node_id: edge.from_node_id,
                to_node_id: edge.to_node_id,
                edge_type: edge.edge_type,
              })),
              overlay: reportOpen || inspectorRow ? "detail" : null,
              onCloseOverlay: () => {
                if (reportOpen) setReportOpen(false);
                if (inspectorRow) setInspectorAgentId(null);
              },
            }}
          />
        ) : null}
        {showForming ? (
          <ResearchCanvasForming
            mode={formingMode ?? "forming"}
            stage={formingStage}
            members={formingMembers ?? []}
            tasks={formingTasks ?? []}
            messages={formingMessages ?? []}
          />
        ) : null}
        {showEmpty ? <ResearchCanvasEmptyState /> : null}

        <ResearchAgentInspector
          row={inspectorRow}
          typedNode={selectedTypedNode}
          open={Boolean(inspectorRow) && !reportOpen}
          onClose={() => setInspectorAgentId(null)}
          onOpenAgentConfig={
            inspectorRow
              ? () => {
                  onOpenAgentPanel(inspectorRow.id, undefined);
                  setInspectorAgentId(null);
                }
              : undefined
          }
        />
      </section>

      <Button
        type="button"
        size="sm"
        variant="secondary"
        className={cn(
          isMobile ? "d5-rail-toggle" : "d5-rail-toggle-desktop",
        )}
        data-testid="research-d5-rail-toggle"
        onClick={() => setRailOpen(!railOpen)}
      >
        {railOpen ? t(($) => $.d5.rail.hide) : t(($) => $.d5.rail.show)}
      </Button>

      {showDesktopRail ? (
        <ResearchD5Rail
          mode={railMode}
          onModeChange={setRailMode}
          chatPanel={chatPanel}
          detailPanel={detailPanel}
          composer={composer}
          onClose={() => setRailOpen(false)}
          {...(backgroundInert ? { inert: true } : {})}
        />
      ) : null}

      {isMobile ? (
        <ResearchD5MobileRail
          open={railOpen}
          onOpenChange={setRailOpen}
          mode={railMode}
          onModeChange={setRailMode}
          chatPanel={chatPanel}
          detailPanel={detailPanel}
          composer={composer}
        />
      ) : null}

      <ResearchNodeReportModal
        open={reportOpen && Boolean(selectedNode)}
        node={selectedNode}
        typedNode={selectedTypedNode}
        sources={sources}
        run={run}
        members={members}
        onClose={() => setReportOpen(false)}
        onSelectLineageNode={handleLineageSelect}
      />
      <span className="sr-only" aria-live="polite" data-testid="research-d5-graph-live">
        {graphLiveMessage}
      </span>
      <span className="sr-only" data-testid="research-d5-active-lens">
        {activeLens}
      </span>
    </div>
  );
}
