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
import { layoutResearchGraph, type ResearchFlowNodeData } from "../lib/layout-graph";
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

type FlowNode = Node<ResearchFlowNodeData>;

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
          const stagger = Math.min(index, 12) * 45;
          classMap.set(n.id, `research-node-enter`);
          // Stagger via animationDelay on the inner style only (opacity keyframes).
          void stagger;
        }
        const dragged = posMap.get(n.id);
        const previous = prevById.get(n.id);
        const position = dragged ?? previous?.position ?? n.position;
        const delayMs = !posMap.has(n.id) && !previous ? Math.min(index, 12) * 45 : 0;
        return {
          ...n,
          position,
          selected: n.id === selectedId,
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
  }, [laid, laidIdsKey, selectedId, setRfNodes, setRfEdges, presence]);

  useEffect(() => {
    if (!laidIdsKey) return;
    const id = window.setTimeout(() => {
      void fitView({
        padding: 0.18,
        duration: prefersReducedMotion() ? 0 : 320,
      });
    }, 80);
    return () => window.clearTimeout(id);
  }, [laidIdsKey, fitView]);

  const selectedNode = selectedId ? nodes.find((n) => n.id === selectedId) : null;
  const sourceList = sources ?? [];

  return (
    <div className="relative h-full w-full">
      <style>{`
        .research-node-enter {
          animation: research-node-enter 520ms ease both;
        }
        @keyframes research-node-enter {
          0% {
            opacity: 0;
            box-shadow: 0 0 0 2px hsl(var(--primary) / 0.55), 0 8px 24px hsl(var(--primary) / 0.18);
          }
          55% {
            opacity: 1;
            box-shadow: 0 0 0 2px hsl(var(--primary) / 0.35), 0 8px 20px hsl(var(--primary) / 0.12);
          }
          100% {
            opacity: 1;
            box-shadow: none;
          }
        }
        @media (prefers-reduced-motion: reduce) {
          .research-node-enter { animation: none; }
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
        nodeTypes={nodeTypes}
        fitView
        minZoom={0.25}
        maxZoom={1.75}
        nodesDraggable
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
