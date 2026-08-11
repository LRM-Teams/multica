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
import type { ResearchGraphNode } from "@multica/core/types";
import type { OpenAgentPanelFn } from "@multica/core/agents";
import { useIsMobile } from "@multica/ui/hooks/use-mobile";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n/use-t";
import { buildD5SessionCanvasModel } from "../lib/build-d5-session-canvas";
import { summarizeTypedGraph } from "../lib/research-d5-summary";
import type { CanvasBodyMode } from "../lib/canvas-body-mode";
import type { ResearchD5Lens } from "../lib/research-d5-lens";
import type { ExecutionRow } from "../execution-overlay";
import { StarGraphCanvas } from "../star-graph";
import { ResearchAgentInspector } from "./research-agent-inspector";
import { ResearchCanvasEmptyState } from "./research-canvas-empty-state";
import { ResearchCanvasForming } from "./research-canvas-forming";
import { ResearchD5Rail, type ResearchD5RailMode } from "./research-d5-rail";
import { ResearchNodeReportModal } from "./research-node-report-modal";
import "./research-d5-layout.css";

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
  formingMode,
  formingStage,
  formingMembers,
  formingTasks,
  formingMessages,
  chatPanel,
  detailPanel,
  composer,
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
  formingMode?: "forming" | "stalled";
  formingStage?: string;
  formingMembers?: Parameters<typeof ResearchCanvasForming>[0]["members"];
  formingTasks?: Parameters<typeof ResearchCanvasForming>[0]["tasks"];
  formingMessages?: Parameters<typeof ResearchCanvasForming>[0]["messages"];
  chatPanel: ReactNode;
  detailPanel: ReactNode;
  composer: ReactNode;
  className?: string;
}) {
  const { t } = useT("research");
  const isMobile = useIsMobile();
  const hostRef = useRef<HTMLDivElement>(null);
  const [viewport, setViewport] = useState({ width: 0, height: 0 });
  const [railMode, setRailMode] = useState<ResearchD5RailMode>("chat");
  const [inspectorAgentId, setInspectorAgentId] = useState<string | null>(null);
  const [reportOpen, setReportOpen] = useState(false);

  const railWidth = isMobile ? 0 : viewport.width >= 1200 ? 360 : 320;

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

  const canvasModel = useMemo(
    () =>
      buildD5SessionCanvasModel(typedGraph, viewport, {
        rightPanelWidth: railWidth,
      }),
    [typedGraph, viewport, railWidth],
  );

  const summary = useMemo(
    () => summarizeTypedGraph(typedGraph?.nodes ?? []),
    [typedGraph?.nodes],
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

  const inspectorRow =
    inspectorAgentId != null
      ? executionRows.find((row) => row.id === inspectorAgentId) ?? null
      : null;

  const handleCanvasSelect = useCallback(
    (nodeId: string) => {
      const snapshotNode = snapshotNodes.find((node) => node.id === nodeId) ?? null;
      onSelectNode(snapshotNode);
      setRailMode("detail");

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
    [onSelectNode, snapshotNodes, typedGraph?.nodes],
  );

  const showEmpty = canvasMode === "empty" && !canvasModel;
  const showForming =
    (canvasMode === "forming" || canvasMode === "stalled") && !canvasModel;

  return (
    <div
      className={cn("d5-workspace", className)}
      data-testid="research-constellation-workspace"
      data-d5-lens={activeLens}
    >
      <section ref={hostRef} className="d5-canvas-host" data-testid="research-session-canvas-host">
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
            summaryTitle={summaryTitle}
            summaryDetail={summaryDetail}
            newFrontierLabel={t(($) => $.d5.new_frontier_label)}
            showMapKey
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
          open={Boolean(inspectorRow)}
          onClose={() => setInspectorAgentId(null)}
          onOpenAgentConfig={
            inspectorRow
              ? () => {
                  onOpenAgentPanel(inspectorRow.id, undefined);
                  setInspectorAgentId(null);
                }
              : undefined
          }
          className={isMobile ? "research-agent-inspector-mobile" : undefined}
        />
        <ResearchNodeReportModal
          open={reportOpen && Boolean(selectedNode)}
          node={selectedNode}
          onClose={() => setReportOpen(false)}
        />
      </section>

      <ResearchD5Rail
        mode={railMode}
        onModeChange={setRailMode}
        chatPanel={chatPanel}
        detailPanel={detailPanel}
        composer={composer}
      />
      <span className="sr-only" data-testid="research-d5-active-lens">
        {activeLens}
      </span>
    </div>
  );
}
