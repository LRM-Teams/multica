"use client";

import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from "react";
import type { TypedGraphResponse } from "@multica/core/research";
import type { StarGraphLayoutResult } from "@multica/core/research";
import { useResearchUiStore } from "@multica/core/research";
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
import { extractLayoutResultFromViewModel } from "../star-graph/lib/star-canvas-view-model";
import { buildTypedGraphMotionEvents } from "../lib/build-typed-graph-motion-events";
import { buildD5LensDisplayHints } from "../lib/research-d5-lens-display";
import { buildNodeAccessibleName } from "../lib/canvas-keyboard-nav";
import { summarizeTypedGraph } from "../lib/research-d5-summary";
import type { CanvasBodyMode } from "../lib/canvas-body-mode";
import type { ResearchD5Lens } from "../lib/research-d5-lens";
import type { ExecutionRow } from "../execution-overlay";
import { semanticMotionCss } from "../motion/directives";
import { useSemanticTransition } from "../motion/use-semantic-transition";
import { StarGraphCanvas } from "../star-graph";
import { ResearchAgentInspector } from "./research-agent-inspector";
import { ResearchCanvasEmptyState } from "./research-canvas-empty-state";
import { ResearchCanvasForming } from "./research-canvas-forming";
import { ResearchD5Rail } from "./research-d5-rail";
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
  snapshotNodes,
  selectedNode,
  onSelectNode,
  executionRows,
  onOpenAgentPanel,
  canvasMode,
  activeLens,
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
  className,
}: {
  typedGraph: TypedGraphResponse | undefined;
  typedLoading: boolean;
  typedError: boolean;
  snapshotNodes: ResearchGraphNode[];
  selectedNode: ResearchGraphNode | null;
  onSelectNode: (node: ResearchGraphNode | null) => void;
  executionRows: ExecutionRow[];
  onOpenAgentPanel: OpenAgentPanelFn;
  canvasMode: CanvasBodyMode;
  activeLens: ResearchD5Lens;
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
        setInspectorAgentId(null);
        setReportOpen(true);
      },
      close: () => setReportOpen(false),
    }),
    [],
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

  useEffect(() => {
    if (!typedGraph) return;
    if (!prevGraphRef.current) {
      prevGraphRef.current = typedGraph;
      return;
    }
    const events = buildTypedGraphMotionEvents(prevGraphRef.current, typedGraph);
    if (events.length > 0) {
      setGraphLiveMessage(
        t(($) => $.d5.graph_live.updating, { version: typedGraph.graph_version }),
      );
    } else if (prevGraphRef.current.graph_version !== typedGraph.graph_version) {
      setGraphLiveMessage(
        t(($) => $.d5.graph_live.updated, { version: typedGraph.graph_version }),
      );
    }
    for (const event of events) motion.enqueue(event);
    prevGraphRef.current = typedGraph;
  }, [typedGraph, motion.enqueue, t]);

  const canvasModel = useMemo(
    () =>
      buildD5SessionCanvasModel(typedGraph, viewport, {
        rightPanelWidth: effectiveRailWidth,
        previousLayout: previousLayoutRef.current,
      }),
    [typedGraph, viewport, effectiveRailWidth],
  );

  useEffect(() => {
    if (!canvasModel) return;
    previousLayoutRef.current = extractLayoutResultFromViewModel(canvasModel);
  }, [canvasModel]);

  const lensHints = useMemo(
    () => buildD5LensDisplayHints(activeLens, typedGraph, canvasModel),
    [activeLens, typedGraph, canvasModel],
  );

  const motionDirectives = useMemo(() => {
    if (!canvasModel) return undefined;
    const map = new Map<
      string,
      ReturnType<typeof motion.directiveFor>
    >();
    for (const entity of canvasModel.entities) {
      const directive = motion.directiveFor(entity.id);
      if (directive) {
        map.set(entity.id, directive);
        continue;
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
    return map;
  }, [canvasModel, motion.queueSize, motion.directiveFor, motion.markerFor, motion.profile.lowPerformance]);

  const summary = useMemo(
    () =>
      summarizeTypedGraph(typedGraph?.nodes ?? [], {
        totalNodeCount: typedGraph?.total_node_count ?? null,
      }),
    [typedGraph?.nodes, typedGraph?.total_node_count],
  );

  const summaryTitle = t(($) => $.d5.summary.title, {
    loaded: summary.loadedDirections,
    total: summary.totalDirections ?? summary.loadedDirections,
  });
  const summaryDetail = t(($) => $.d5.summary.detail, {
    stable: summary.stableResults,
    active: summary.activeProbes,
    newDir: summary.newFrontiers,
    stopped: summary.stoppedDirections,
  });

  const nodeAccessibleNames = useMemo(() => {
    const map = new Map<string, string>();
    for (const node of snapshotNodes) {
      map.set(node.id, buildNodeAccessibleName(node));
    }
    return map;
  }, [snapshotNodes]);

  const relatedNodeIds = useMemo(() => {
    if (!selectedNode || !typedGraph) return undefined;
    const typed = typedGraph.nodes.find((node) => node.id === selectedNode.id);
    if (!typed) return undefined;
    const ids = new Set<string>();
    for (const id of typed.merged_from ?? []) {
      if (id) ids.add(id);
    }
    if (typed.parent_id) ids.add(typed.parent_id);
    for (const id of typed.child_ids ?? []) {
      if (id) ids.add(id);
    }
    for (const edge of typedGraph.edges) {
      if (edge.from_node_id === selectedNode.id) ids.add(edge.to_node_id);
      if (edge.to_node_id === selectedNode.id) ids.add(edge.from_node_id);
    }
    return ids;
  }, [selectedNode, typedGraph]);

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
      const snapshotNode = snapshotNodes.find((node) => node.id === nodeId) ?? null;
      onSelectNode(snapshotNode);
      setRailMode("detail");
      if (isMobile) setRailOpen(true);

      const typedNode = typedGraph?.nodes.find((node) => node.id === nodeId);
      const level = (typedNode?.level || "").toLowerCase();
      if (level === "s" && typedNode?.actor_agent_id) {
        setInspectorAgentId(typedNode.actor_agent_id);
        setReportOpen(false);
        return;
      }
      setInspectorAgentId(null);
      if (level === "l" || level === "xl" || level === "xxl") {
        setReportOpen(true);
      }
    },
    [isMobile, onSelectNode, snapshotNodes, typedGraph?.nodes],
  );

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
      data-d5-rail-open={showDesktopRail ? "true" : "false"}
    >
      <section
        ref={hostRef}
        className="d5-canvas-host"
        data-testid="research-session-canvas-host"
        {...(backgroundInert ? { inert: true } : {})}
      >
        {typedLoading && !canvasModel ? (
          <div className="grid h-full place-items-center text-sm text-muted-foreground">
            {t(($) => $.d5.canvas.loading)}
          </div>
        ) : null}
        {typedError && !canvasModel ? (
          <div
            role="alert"
            className="grid h-full place-items-center px-6 text-center text-sm text-destructive"
          >
            {t(($) => $.d5.canvas.error)}
          </div>
        ) : null}
        {canvasModel ? (
          <StarGraphCanvas
            model={canvasModel}
            selectedNodeId={selectedNode?.id ?? null}
            onSelectNode={handleCanvasSelect}
            onOpenNode={handleCanvasSelect}
            summaryTitle={summaryTitle}
            summaryDetail={summaryDetail}
            newFrontierLabel={t(($) => $.d5.new_frontier_label)}
            lensHints={lensHints}
            motionDirectives={motionDirectives}
            showMapKey
            rightPanelWidth={effectiveRailWidth}
            nodeAccessibleNames={nodeAccessibleNames}
            relatedNodeIds={relatedNodeIds}
            typedNodes={typedGraph?.nodes}
            hiddenCountLabel={(count) => t(($) => $.d5.cluster_hidden, { count })}
            keyboardNav={{
              nodes: snapshotNodes,
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
        <ResearchD5Rail
          mode={railMode}
          onModeChange={setRailMode}
          chatPanel={chatPanel}
          detailPanel={detailPanel}
          composer={composer}
          className={!railOpen ? "d5-rail-collapsed" : undefined}
          {...(backgroundInert ? { inert: true } : {})}
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
