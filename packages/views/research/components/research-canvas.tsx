"use client";

import { useEffect, useMemo, useRef } from "react";
import {
  Background,
  BackgroundVariant,
  Controls,
  MiniMap,
  ReactFlow,
  ReactFlowProvider,
  useEdgesState,
  useNodesState,
  useReactFlow,
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
import { layoutResearchGraph } from "../lib/layout-graph";
import { visualForEdgeType } from "../lib/node-visuals";
import { ResearchFleetStrip } from "./research-fleet-strip";
import { ResearchGraphNode as ResearchGraphNodeView } from "./research-graph-node";
import { ResearchNodeDetail } from "./research-node-detail";
import { ResearchSourceBadges } from "./research-source-badges";

const nodeTypes: NodeTypes = {
  research: ResearchGraphNodeView,
};

function prefersReducedMotion(): boolean {
  if (typeof window === "undefined" || !window.matchMedia) return false;
  return window.matchMedia("(prefers-reduced-motion: reduce)").matches;
}

function ResearchCanvasInner({
  nodes,
  edges,
  sources,
  members,
  presence,
  selectedId,
  onSelect,
}: {
  nodes: ResearchGraphNode[];
  edges: ResearchGraphEdge[];
  sources?: ResearchSource[];
  members?: ResearchFleetMember[];
  presence?: ResearchPresenceMap;
  selectedId?: string | null;
  onSelect?: (node: ResearchGraphNode | null) => void;
}) {
  const laid = useMemo(() => layoutResearchGraph(nodes, edges), [nodes, edges]);
  const [rfNodes, setRfNodes, onNodesChange] = useNodesState(laid.nodes);
  const [rfEdges, setRfEdges, onEdgesChange] = useEdgesState(laid.edges);
  const { fitView } = useReactFlow();
  // Enter motion is keyed once per node id (ref — not mirrored React state).
  const enterAnimationById = useRef<Map<string, string> | null>(null);
  if (enterAnimationById.current === null) {
    enterAnimationById.current = new Map();
  }

  useEffect(() => {
    const reduceMotion = prefersReducedMotion();
    setRfNodes(
      laid.nodes.map((n, index) => {
        const research = n.data.research;
        const actorId = research.actor_agent_id;
        const presenceLabel = actorId ? presence?.[actorId]?.activity : undefined;
        const animMap = enterAnimationById.current!;
        if (!animMap.has(n.id) && !reduceMotion) {
          const stagger = Math.min(index, 12) * 45;
          animMap.set(n.id, `research-node-enter 520ms ease ${stagger}ms both`);
        }
        return {
          ...n,
          selected: n.id === selectedId,
          style: {
            ...n.style,
            animation: animMap.get(n.id),
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
        };
      }),
    );
    setRfEdges(
      laid.edges.map((e) => {
        const edgeType = e.data?.edgeType ?? "leads_to";
        const visual = visualForEdgeType(edgeType);
        return {
          ...e,
          animated: visual.animated,
          style: {
            stroke: visual.stroke,
            strokeDasharray: visual.strokeDasharray,
            strokeWidth: 1.5,
          },
        };
      }),
    );
  }, [laid, selectedId, setRfNodes, setRfEdges, presence]);

  useEffect(() => {
    if (nodes.length === 0) return;
    const id = window.setTimeout(() => {
      void fitView({
        padding: 0.18,
        duration: prefersReducedMotion() ? 0 : 320,
      });
    }, 80);
    return () => window.clearTimeout(id);
  }, [nodes.length, edges.length, fitView]);

  const selectedNode = selectedId ? nodes.find((n) => n.id === selectedId) : null;
  const sourceList = sources ?? [];

  return (
    <div className="relative h-full w-full">
      <style>{`
        @keyframes research-node-enter {
          0% {
            opacity: 0;
            transform: translateY(10px) scale(0.98);
            box-shadow: 0 0 0 2px hsl(var(--primary) / 0.55), 0 8px 24px hsl(var(--primary) / 0.18);
          }
          55% {
            opacity: 1;
            transform: translateY(0) scale(1);
            box-shadow: 0 0 0 2px hsl(var(--primary) / 0.35), 0 8px 20px hsl(var(--primary) / 0.12);
          }
          100% {
            opacity: 1;
            transform: translateY(0) scale(1);
            box-shadow: none;
          }
        }
        @media (prefers-reduced-motion: reduce) {
          @keyframes research-node-enter {
            from { opacity: 1; transform: none; box-shadow: none; }
            to { opacity: 1; transform: none; box-shadow: none; }
          }
        }
      `}</style>
      <ReactFlow
        nodes={rfNodes}
        edges={rfEdges}
        onNodesChange={onNodesChange}
        onEdgesChange={onEdgesChange}
        nodeTypes={nodeTypes}
        fitView
        minZoom={0.25}
        maxZoom={1.75}
        proOptions={{ hideAttribution: true }}
        onNodeClick={(_evt, node) => {
          const research = node.data?.research as ResearchGraphNode | undefined;
          onSelect?.(research ?? null);
        }}
        onPaneClick={() => onSelect?.(null)}
        className="bg-[radial-gradient(circle_at_18%_0%,hsl(var(--primary)/0.07),transparent_42%),radial-gradient(circle_at_85%_95%,hsl(var(--chart-2)/0.09),transparent_38%),hsl(var(--background))]"
      >
        <Background variant={BackgroundVariant.Dots} gap={22} size={1} color="hsl(var(--muted-foreground)/0.25)" />
        <Controls showInteractive={false} className="!shadow-sm" />
        <MiniMap
          pannable
          zoomable
          className="!overflow-hidden !rounded-lg !border !border-border !bg-card/90"
          maskColor="hsl(var(--background)/0.7)"
        />
      </ReactFlow>
      {members && members.length > 0 ? <ResearchFleetStrip members={members} /> : null}
      {sourceList.length > 0 ? <ResearchSourceBadges sources={sourceList} /> : null}
      {selectedNode ? (
        <ResearchNodeDetail node={selectedNode} sources={sourceList} />
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
}) {
  return (
    <ReactFlowProvider>
      <div className="h-full w-full min-h-0">
        <ResearchCanvasInner {...props} />
      </div>
    </ReactFlowProvider>
  );
}
