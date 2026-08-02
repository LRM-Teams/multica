"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  Background,
  BackgroundVariant,
  MiniMap,
  NodeToolbar,
  Panel,
  Position,
  ReactFlow,
  ReactFlowProvider,
  useEdgesState,
  useNodesState,
  useReactFlow,
  type Node,
  type NodeTypes,
} from "@xyflow/react";
import "@xyflow/react/dist/style.css";
import type {
  ResearchFleetMember,
  ResearchGraphEdge,
  ResearchGraphNode,
  ResearchSource,
} from "@multica/core/types";
import type { ResearchPresenceMap } from "@multica/core/research";
import { useIsMobile } from "@multica/ui/hooks/use-mobile";
import {
  CONTROLS_BOTTOM_PX,
  DETAIL_CARD_BOTTOM_PX,
  MINIMAP_HEIGHT_PX,
  MINIMAP_WIDTH_PX,
  OVERLAY_INSET_PX,
} from "../lib/canvas-overlay-grid";
import type { ChatDrawerMode } from "../lib/chat-drawer-mode";
import { layoutResearchGraph, type ResearchFlowNodeData } from "../lib/layout-graph";
import { LOGIC_END_NODE_ID, isLogicEndNode } from "../lib/logic-lanes";
import {
  NODE_ENTER_CLASS,
  nodeEnterDelayStyle,
  nodeEnterMotionCss,
  nodeEnterStaggerDelayMs,
} from "../lib/node-enter-motion";
import { edgeVisualForConnection } from "../lib/node-visuals";
import { ResearchCanvasDock } from "./research-canvas-dock";
import { ResearchChatFab } from "./research-chat-fab";
import { ResearchFleetAvatarStack } from "./research-fleet-avatar-stack";
import { ResearchGraphNode as ResearchGraphNodeView } from "./research-graph-node";
import { ResearchLaneBandNodeView } from "./research-lane-band-node";
import { ResearchLogicStrip } from "./research-logic-strip";
import { SYSTEM_NODE_TYPES, type NodeRingAction } from "../lib/node-action-ring";
import { ResearchNodeActionRing } from "./research-node-action-ring";
import { ResearchNodeDetail } from "./research-node-detail";
import { useT } from "../../i18n/use-t";

const NODE_ENTER_MOTION_CSS = nodeEnterMotionCss();

const nodeTypes: NodeTypes = {
  research: ResearchGraphNodeView,
  laneBand: ResearchLaneBandNodeView,
};

const EMPTY_FLEET_MEMBERS: ResearchFleetMember[] = [];

function prefersReducedMotion(): boolean {
  if (typeof window === "undefined" || !window.matchMedia) return false;
  return window.matchMedia("(prefers-reduced-motion: reduce)").matches;
}

type FlowNode = Node<ResearchFlowNodeData>;

function ResearchCanvasInner({
  nodes,
  edges,
  sources,
  members = EMPTY_FLEET_MEMBERS,
  sessionStatus,
  presence,
  selectedId,
  onSelect,
  onRetry,
  onOpenDelivery,
  onOpenChat,
  chatOpen = false,
  chatMode = "empty",
}: {
  nodes: ResearchGraphNode[];
  edges: ResearchGraphEdge[];
  sources?: ResearchSource[];
  members?: ResearchFleetMember[];
  sessionStatus?: string | null;
  presence?: ResearchPresenceMap;
  selectedId?: string | null;
  onSelect?: (node: ResearchGraphNode | null) => void;
  onRetry?: (node: ResearchGraphNode) => void;
  onOpenDelivery?: () => void;
  onOpenChat?: () => void;
  chatOpen?: boolean;
  /** LRM-992 — FAB four-state mode (empty / loading / running / error). */
  chatMode?: ChatDrawerMode;
}) {
  const { t } = useT("research");
  const laid = useMemo(() => layoutResearchGraph(nodes, edges), [nodes, edges]);
  const [rfNodes, setRfNodes, onNodesChange] = useNodesState(laid.nodes);
  const [rfEdges, setRfEdges, onEdgesChange] = useEdgesState(laid.edges);
  const { fitView, zoomIn, zoomOut, getZoom } = useReactFlow();
  const isMobile = useIsMobile();
  const [ringNodeId, setRingNodeId] = useState<string | null>(null);
  const [detailPinned, setDetailPinned] = useState(false);
  const [pinnedNodeId, setPinnedNodeId] = useState<string | null>(null);
  const [zoomPct, setZoomPct] = useState(100);
  // LRM-827: enter class on RF node; fade+slide runs on inner shell so RF
  // position transform is never overridden.
  const enterClassById = useRef<Map<string, string> | null>(null);
  if (enterClassById.current === null) {
    enterClassById.current = new Map();
  }
  // Preserve user-dragged positions across layout refreshes.
  const userPositions = useRef<Map<string, { x: number; y: number }> | null>(null);
  if (userPositions.current === null) {
    userPositions.current = new Map();
  }
  const laidIdsKey = useMemo(() => laid.nodes.map((n) => n.id).join("|"), [laid.nodes]);

  useEffect(() => {
    const reduceMotion = prefersReducedMotion();
    const classMap = enterClassById.current!;
    const posMap = userPositions.current!;
    const delayById = new Map<string, number>();
    let newEnterIndex = 0;
    for (const n of laid.nodes) {
      const research = n.data.research;
      const isBand = n.type === "laneBand" || !research;
      if (!isBand && !classMap.has(n.id) && !reduceMotion) {
        classMap.set(n.id, NODE_ENTER_CLASS);
        delayById.set(n.id, nodeEnterStaggerDelayMs(newEnterIndex));
        newEnterIndex += 1;
      }
    }

    setRfNodes((prev) => {
      const prevById = new Map(prev.map((n) => [n.id, n]));
      return laid.nodes.map((n) => {
        const research = n.data.research;
        const isBand = n.type === "laneBand" || !research;
        const actorId = research?.actor_agent_id;
        const presenceLabel = actorId ? presence?.[actorId]?.activity : undefined;
        const delayMs = delayById.get(n.id) ?? 0;
        const dragged = isBand ? undefined : posMap.get(n.id);
        const previous = prevById.get(n.id);
        const position = dragged ?? previous?.position ?? n.position;
        return {
          ...n,
          position,
          selected: !isBand && (n.id === selectedId || n.id === ringNodeId),
          draggable: isBand ? false : true,
          className: isBand ? undefined : classMap.get(n.id),
          style: {
            ...n.style,
            ...(delayMs ? nodeEnterDelayStyle(delayMs) : undefined),
          },
          data: {
            ...n.data,
            presenceLabel: presenceLabel || undefined,
            sourceBadgeCount:
              research?.node_type === "finding" &&
              research.payload &&
              typeof research.payload === "object" &&
              "source_id" in (research.payload as object)
                ? 1
                : 0,
            onRetry: onRetry ?? undefined,
          },
        } satisfies FlowNode;
      });
    });
    const typeById = new Map<string, string>();
    for (const n of laid.nodes) {
      const research = n.data.research;
      if (research) typeById.set(n.id, research.node_type);
    }
    setRfEdges(
      laid.edges.map((e) => {
        const edgeType = e.data?.edgeType ?? "leads_to";
        const toType = typeById.get(e.target);
        const visual = edgeVisualForConnection(edgeType, toType);
        const isMain = visual.role === "main" || visual.role === "active";
        const isDetour = visual.role === "recessed";
        return {
          ...e,
          animated: visual.animated && !isDetour,
          style: {
            stroke: isMain ? "var(--brand)" : visual.stroke,
            strokeDasharray: visual.strokeDasharray,
            strokeWidth: visual.strokeWidth ?? (isMain ? 2.5 : 1.5),
            strokeOpacity: visual.strokeOpacity,
            filter:
              isMain && !isDetour
                ? "drop-shadow(0 0 5px color-mix(in oklch, var(--brand) 45%, transparent))"
                : undefined,
          },
          data: {
            edgeType: String(edgeType),
          },
        };
      }),
    );
  }, [laid, laidIdsKey, selectedId, ringNodeId, setRfNodes, setRfEdges, presence, onRetry]);

  useEffect(() => {
    if (!laidIdsKey) return;
    const id = window.setTimeout(() => {
      void fitView({
        padding: 0.18,
        duration: prefersReducedMotion() ? 0 : 320,
      });
      setZoomPct(Math.round(getZoom() * 100));
    }, 80);
    return () => window.clearTimeout(id);
  }, [laidIdsKey, fitView, getZoom]);

  const closeRing = useCallback(() => {
    setRingNodeId(null);
  }, []);

  const selectedNode =
    selectedId && selectedId !== LOGIC_END_NODE_ID
      ? nodes.find((n) => n.id === selectedId) ?? null
      : null;
  const ringNode = ringNodeId ? nodes.find((n) => n.id === ringNodeId) : null;
  const pinnedNode = pinnedNodeId ? nodes.find((n) => n.id === pinnedNodeId) : null;
  // Prefer prop selection; keep a local pin so detail still opens if parent select lags.
  const detailNode = selectedNode ?? pinnedNode ?? (detailPinned ? ringNode : null);
  const sourceList = sources ?? [];
  const showDetail =
    detailPinned ||
    (!!detailNode && SYSTEM_NODE_TYPES.has(detailNode.node_type));

  const pinDetail = useCallback(
    (node: ResearchGraphNode) => {
      onSelect?.(node);
      setPinnedNodeId(node.id);
      setDetailPinned(true);
    },
    [onSelect],
  );

  const clearDetail = useCallback(() => {
    setDetailPinned(false);
    setPinnedNodeId(null);
    onSelect?.(null);
  }, [onSelect]);

  const handleRingAction = useCallback(
    async (action: NodeRingAction) => {
      if (!ringNode) return;
      switch (action) {
        case "detail":
        case "more":
          pinDetail(ringNode);
          closeRing();
          break;
        case "locate_source": {
          pinDetail(ringNode);
          closeRing();
          break;
        }
        case "copy_prompt": {
          const text = [ringNode.title, ringNode.summary].filter(Boolean).join("\n");
          try {
            await navigator.clipboard.writeText(text);
          } catch {
            /* ignore */
          }
          closeRing();
          break;
        }
        case "retry":
          onRetry?.(ringNode);
          closeRing();
          break;
        case "dig_deeper":
          break;
      }
    },
    [ringNode, pinDetail, onRetry, closeRing],
  );

  // Narrow + detail sheet: hide FAB so it does not sit under the sheet / drawer.
  const chatFab =
    !chatOpen && onOpenChat && !(isMobile && showDetail) ? (
      <ResearchChatFab mode={chatMode} onOpen={onOpenChat} isMobile={isMobile} />
    ) : null;

  if (isMobile) {
    return (
      <div
        className="relative h-full w-full bg-canvas-bg text-foreground"
        data-testid="research-canvas-overlay-grid"
        data-overlay="narrow"
      >
        <style>{NODE_ENTER_MOTION_CSS}</style>
        <ResearchLogicStrip
          nodes={nodes}
          edges={edges}
          selectedId={selectedId}
          presence={presence}
          onSelect={(node) => {
            if (node && isLogicEndNode(node)) {
              onOpenDelivery?.();
              return;
            }
            onSelect?.(node);
            if (node) {
              setPinnedNodeId(node.id);
              setDetailPinned(true);
            }
          }}
          onOpenDelivery={onOpenDelivery}
          onRetry={onRetry}
        />
        <ResearchFleetAvatarStack
          members={members}
          sessionStatus={sessionStatus}
          className="absolute top-3 right-3 z-20"
        />
        {chatFab}
        {showDetail && detailNode ? (
          <ResearchNodeDetail
            node={detailNode}
            sources={sourceList}
            open={showDetail}
            placement="sheet"
            onClose={clearDetail}
          />
        ) : null}
        {ringNode ? (
          <ResearchNodeActionRing
            node={ringNode}
            mode="sheet"
            onAction={handleRingAction}
            onClose={closeRing}
          />
        ) : null}
      </div>
    );
  }

  return (
    <div
      className="relative h-full w-full bg-canvas-bg text-foreground"
      data-testid="research-canvas-overlay-grid"
      data-overlay="desktop"
    >
      <style>{`
        ${NODE_ENTER_MOTION_CSS}
        .react-flow__node.selected .research-graph-node-shell {
          outline: 2px solid var(--brand);
          outline-offset: 2px;
          box-shadow: 0 0 0 1px color-mix(in oklch, var(--brand) 30%, transparent), 0 0 28px color-mix(in oklch, var(--brand) 32%, transparent);
        }
      `}</style>
      <ReactFlow
        nodes={rfNodes}
        edges={rfEdges}
        onNodesChange={(changes) => {
          for (const change of changes) {
            if (change.type === "position" && change.position && change.id) {
              userPositions.current!.set(change.id, change.position);
            }
          }
          onNodesChange(changes);
        }}
        onEdgesChange={onEdgesChange}
        onMoveEnd={() => {
          setZoomPct(Math.round(getZoom() * 100));
        }}
        nodeTypes={nodeTypes}
        fitView
        minZoom={0.25}
        maxZoom={1.75}
        nodesDraggable
        proOptions={{ hideAttribution: true }}
        onNodeClick={(_evt, node) => {
          if (node.type === "laneBand") return;
          const research = node.data?.research as ResearchGraphNode | undefined;
          if (!research) return;
          if (isLogicEndNode(research)) {
            closeRing();
            onOpenDelivery?.();
            return;
          }
          if (SYSTEM_NODE_TYPES.has(research.node_type)) {
            closeRing();
            pinDetail(research);
            return;
          }
          // Toggle ring; keep selection for halo.
          onSelect?.(research);
          setRingNodeId((prev) => (prev === research.id ? null : research.id));
          setDetailPinned(false);
          setPinnedNodeId(null);
        }}
        onPaneClick={() => {
          closeRing();
          clearDetail();
        }}
        className="!bg-transparent"
      >
        <Background
          variant={BackgroundVariant.Dots}
          gap={24}
          size={1}
          color="var(--canvas-dot)"
        />
        <Panel
          position="top-left"
          className="!m-3 flex items-center gap-2 rounded-lg border bg-card/90 px-2.5 py-1.5 text-[11px] text-muted-foreground shadow-sm backdrop-blur-md"
        >
          <span className="font-semibold text-foreground">{t(($) => $.logic.label)}</span>
          <span aria-hidden>·</span>
          <span>{t(($) => $.logic.lr_hint)}</span>
        </Panel>
        {/* LRM-797: MiniMap bottom-right; FAB stacks 12px above (outside RF). */}
        <MiniMap
          pannable
          zoomable
          style={{
            width: MINIMAP_WIDTH_PX,
            height: MINIMAP_HEIGHT_PX,
          }}
          className="!absolute !top-auto !bottom-4 !left-auto !right-4 !m-0 !overflow-hidden !rounded-lg !border !border-border !bg-card/90"
          maskColor="color-mix(in oklch, var(--canvas-bg) 70%, transparent)"
          nodeColor={(n) => (n.type === "laneBand" ? "transparent" : "var(--brand)")}
        />
        {ringNode ? (
          <NodeToolbar
            nodeId={ringNode.id}
            isVisible
            position={Position.Right}
            offset={12}
            className="!border-0 !bg-transparent !p-0 !shadow-none"
          >
            <ResearchNodeActionRing
              node={ringNode}
              mode="ring"
              onAction={handleRingAction}
              onClose={closeRing}
            />
          </NodeToolbar>
        ) : null}
      </ReactFlow>
      {/* LRM-797: Controls bottom-left (outside RF so Panel centering cannot win). */}
      <div
        className="pointer-events-auto absolute z-20"
        style={{ left: OVERLAY_INSET_PX, bottom: CONTROLS_BOTTOM_PX }}
        data-testid="research-canvas-controls-slot"
      >
        <ResearchCanvasDock
          zoomPct={zoomPct}
          className="!mb-0"
          onZoomIn={() => {
            void zoomIn({ duration: 160 });
            setZoomPct(Math.round(getZoom() * 100));
          }}
          onZoomOut={() => {
            void zoomOut({ duration: 160 });
            setZoomPct(Math.round(getZoom() * 100));
          }}
          onFit={() => {
            void fitView({ padding: 0.18, duration: 240 });
            setZoomPct(Math.round(getZoom() * 100));
          }}
          detailOpen={showDetail}
          onToggleDetail={() => {
            if (showDetail) {
              clearDetail();
            } else if (selectedNode || ringNode) {
              const n = selectedNode ?? ringNode!;
              pinDetail(n);
              closeRing();
            }
          }}
        />
      </div>
      {/* LRM-797: detail card 12px above Controls (substantial, not a chip). */}
      {showDetail && detailNode ? (
        <div
          className="pointer-events-auto absolute z-20"
          style={{
            left: OVERLAY_INSET_PX,
            bottom: DETAIL_CARD_BOTTOM_PX,
          }}
          data-testid="research-detail-overlay-slot"
        >
          <ResearchNodeDetail
            node={detailNode}
            sources={sourceList}
            open={showDetail}
            placement="overlay-card"
            onClose={clearDetail}
          />
        </div>
      ) : null}
      {chatFab}
    </div>
  );
}

export function ResearchCanvas(props: {
  nodes: ResearchGraphNode[];
  edges: ResearchGraphEdge[];
  sources?: ResearchSource[];
  members?: ResearchFleetMember[];
  sessionStatus?: string | null;
  presence?: ResearchPresenceMap;
  selectedId?: string | null;
  onSelect?: (node: ResearchGraphNode | null) => void;
  onRetry?: (node: ResearchGraphNode) => void;
  onOpenDelivery?: () => void;
  onOpenChat?: () => void;
  chatOpen?: boolean;
  chatMode?: ChatDrawerMode;
}) {
  return (
    <ReactFlowProvider>
      <div className="h-full w-full min-h-0">
        <ResearchCanvasInner {...props} />
      </div>
    </ReactFlowProvider>
  );
}
