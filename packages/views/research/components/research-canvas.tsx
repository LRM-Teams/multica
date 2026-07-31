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
import { layoutResearchGraph, type ResearchFlowNodeData } from "../lib/layout-graph";
import { visualForEdgeType } from "../lib/node-visuals";
import { ResearchCanvasDock } from "./research-canvas-dock";
import { ResearchFleetStrip } from "./research-fleet-strip";
import { ResearchGraphNode as ResearchGraphNodeView } from "./research-graph-node";
import { SYSTEM_NODE_TYPES, type NodeRingAction } from "../lib/node-action-ring";
import { ResearchNodeActionRing } from "./research-node-action-ring";
import { ResearchNodeDetail } from "./research-node-detail";
import { ResearchSourceBadges } from "./research-source-badges";

const nodeTypes: NodeTypes = {
  research: ResearchGraphNodeView,
};

function prefersReducedMotion(): boolean {
  if (typeof window === "undefined" || !window.matchMedia) return false;
  return window.matchMedia("(prefers-reduced-motion: reduce)").matches;
}

type FlowNode = Node<ResearchFlowNodeData>;

function ResearchCanvasInner({
  nodes,
  edges,
  sources,
  members,
  presence,
  selectedId,
  onSelect,
  onRetry,
}: {
  nodes: ResearchGraphNode[];
  edges: ResearchGraphEdge[];
  sources?: ResearchSource[];
  members?: ResearchFleetMember[];
  presence?: ResearchPresenceMap;
  selectedId?: string | null;
  onSelect?: (node: ResearchGraphNode | null) => void;
  onRetry?: (node: ResearchGraphNode) => void;
}) {
  const laid = useMemo(() => layoutResearchGraph(nodes, edges), [nodes, edges]);
  const [rfNodes, setRfNodes, onNodesChange] = useNodesState(laid.nodes);
  const [rfEdges, setRfEdges, onEdgesChange] = useEdgesState(laid.edges);
  const { fitView, zoomIn, zoomOut, getZoom } = useReactFlow();
  const isMobile = useIsMobile();
  const [ringNodeId, setRingNodeId] = useState<string | null>(null);
  const [detailPinned, setDetailPinned] = useState(false);
  const [zoomPct, setZoomPct] = useState(100);
  // Opacity-only enter motion (never touch transform — RF uses it for position).
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

    setRfNodes((prev) => {
      const prevById = new Map(prev.map((n) => [n.id, n]));
      return laid.nodes.map((n, index) => {
        const research = n.data.research;
        const actorId = research.actor_agent_id;
        const presenceLabel = actorId ? presence?.[actorId]?.activity : undefined;
        if (!classMap.has(n.id) && !reduceMotion) {
          classMap.set(n.id, `research-node-enter`);
        }
        const dragged = posMap.get(n.id);
        const previous = prevById.get(n.id);
        const position = dragged ?? previous?.position ?? n.position;
        const delayMs = !posMap.has(n.id) && !previous ? Math.min(index, 12) * 45 : 0;
        return {
          ...n,
          position,
          selected: n.id === selectedId || n.id === ringNodeId,
          draggable: true,
          className: classMap.get(n.id),
          style: {
            ...n.style,
            animationDelay: delayMs ? `${delayMs}ms` : undefined,
          },
          data: {
            ...n.data,
            presenceLabel: presenceLabel || undefined,
            sourceBadgeCount:
              research.node_type === "finding" &&
              research.payload &&
              typeof research.payload === "object" &&
              "source_id" in (research.payload as object)
                ? 1
                : 0,
          },
        } satisfies FlowNode;
      });
    });
    setRfEdges(
      laid.edges.map((e) => {
        const edgeType = e.data?.edgeType ?? "leads_to";
        const visual = visualForEdgeType(edgeType);
        const isMain = edgeType === "leads_to" || visual.animated;
        return {
          ...e,
          animated: visual.animated,
          style: {
            stroke: isMain ? "var(--brand)" : visual.stroke,
            strokeDasharray: visual.strokeDasharray,
            strokeWidth: isMain ? 2.5 : 1.5,
            filter: isMain
              ? "drop-shadow(0 0 5px color-mix(in oklch, var(--brand) 45%, transparent))"
              : undefined,
          },
        };
      }),
    );
  }, [laid, laidIdsKey, selectedId, ringNodeId, setRfNodes, setRfEdges, presence]);

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

  const selectedNode = selectedId ? nodes.find((n) => n.id === selectedId) : null;
  const ringNode = ringNodeId ? nodes.find((n) => n.id === ringNodeId) : null;
  const sourceList = sources ?? [];
  const showDetail = detailPinned || (!!selectedNode && SYSTEM_NODE_TYPES.has(selectedNode.node_type));

  const handleRingAction = useCallback(
    async (action: NodeRingAction) => {
      if (!ringNode) return;
      switch (action) {
        case "detail":
        case "more":
          onSelect?.(ringNode);
          setDetailPinned(true);
          closeRing();
          break;
        case "locate_source": {
          onSelect?.(ringNode);
          setDetailPinned(true);
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
    [ringNode, onSelect, onRetry, closeRing],
  );

  return (
    <div className="relative h-full w-full bg-canvas-bg text-foreground">
      <style>{`
        .research-node-enter {
          animation: research-node-enter 520ms ease both;
        }
        @keyframes research-node-enter {
          0% {
            opacity: 0;
            box-shadow: 0 0 0 2px color-mix(in oklch, var(--brand) 55%, transparent), 0 8px 24px color-mix(in oklch, var(--brand) 18%, transparent);
          }
          55% {
            opacity: 1;
            box-shadow: 0 0 0 2px color-mix(in oklch, var(--brand) 35%, transparent), 0 8px 20px color-mix(in oklch, var(--brand) 12%, transparent);
          }
          100% {
            opacity: 1;
            box-shadow: none;
          }
        }
        @media (prefers-reduced-motion: reduce) {
          .research-node-enter { animation: none; }
        }
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
          const research = node.data?.research as ResearchGraphNode | undefined;
          if (!research) return;
          if (SYSTEM_NODE_TYPES.has(research.node_type)) {
            closeRing();
            onSelect?.(research);
            setDetailPinned(true);
            return;
          }
          // Toggle ring; keep selection for halo.
          onSelect?.(research);
          setRingNodeId((prev) => (prev === research.id ? null : research.id));
          setDetailPinned(false);
        }}
        onPaneClick={() => {
          closeRing();
          onSelect?.(null);
          setDetailPinned(false);
        }}
        className="!bg-transparent"
      >
        <Background
          variant={BackgroundVariant.Dots}
          gap={24}
          size={1}
          color="var(--canvas-dot)"
        />
        <MiniMap
          pannable
          zoomable
          className="!bottom-20 !left-4 !overflow-hidden !rounded-lg !border !border-border !bg-card/90 max-lg:!hidden"
          maskColor="color-mix(in oklch, var(--canvas-bg) 70%, transparent)"
        />
        <Panel position="bottom-center" className="!m-0 !w-full !bg-transparent">
          <ResearchCanvasDock
            zoomPct={zoomPct}
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
                setDetailPinned(false);
                onSelect?.(null);
              } else if (selectedNode || ringNode) {
                const n = selectedNode ?? ringNode!;
                onSelect?.(n);
                setDetailPinned(true);
                closeRing();
              }
            }}
          />
        </Panel>
        {/* Desktop: NodeToolbar tracks the node — no DOM-measure sync effect. */}
        {ringNode && !isMobile ? (
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
      {members && members.length > 0 ? <ResearchFleetStrip members={members} /> : null}
      {sourceList.length > 0 ? <ResearchSourceBadges sources={sourceList} /> : null}
      {showDetail && selectedNode ? (
        <ResearchNodeDetail node={selectedNode} sources={sourceList} />
      ) : null}
      {ringNode && isMobile ? (
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

export function ResearchCanvas(props: {
  nodes: ResearchGraphNode[];
  edges: ResearchGraphEdge[];
  sources?: ResearchSource[];
  members?: ResearchFleetMember[];
  presence?: ResearchPresenceMap;
  selectedId?: string | null;
  onSelect?: (node: ResearchGraphNode | null) => void;
  onRetry?: (node: ResearchGraphNode) => void;
}) {
  return (
    <ReactFlowProvider>
      <div className="h-full w-full min-h-0">
        <ResearchCanvasInner {...props} />
      </div>
    </ReactFlowProvider>
  );
}
