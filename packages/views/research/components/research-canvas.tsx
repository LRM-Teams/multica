"use client";

import { useEffect, useMemo } from "react";
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
import type { ResearchFleetMember, ResearchGraphEdge, ResearchGraphNode } from "@multica/core/types";
import { layoutResearchGraph } from "../lib/layout-graph";
import { visualForEdgeType } from "../lib/node-visuals";
import { ResearchFleetStrip } from "./research-fleet-strip";
import { ResearchGraphNode as ResearchGraphNodeView } from "./research-graph-node";
import { ResearchNodeDetail } from "./research-node-detail";

const nodeTypes: NodeTypes = {
  research: ResearchGraphNodeView,
};

function ResearchCanvasInner({
  nodes,
  edges,
  members,
  selectedId,
  onSelect,
}: {
  nodes: ResearchGraphNode[];
  edges: ResearchGraphEdge[];
  members?: ResearchFleetMember[];
  selectedId?: string | null;
  onSelect?: (node: ResearchGraphNode | null) => void;
}) {
  const laid = useMemo(() => layoutResearchGraph(nodes, edges), [nodes, edges]);
  const [rfNodes, setRfNodes, onNodesChange] = useNodesState(laid.nodes);
  const [rfEdges, setRfEdges, onEdgesChange] = useEdgesState(laid.edges);
  const { fitView } = useReactFlow();

  useEffect(() => {
    setRfNodes(
      laid.nodes.map((n) => ({
        ...n,
        selected: n.id === selectedId,
      })),
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
  }, [laid, selectedId, setRfNodes, setRfEdges]);

  useEffect(() => {
    const id = window.setTimeout(() => {
      void fitView({ padding: 0.18, duration: 280 });
    }, 40);
    return () => window.clearTimeout(id);
  }, [nodes.length, edges.length, fitView]);

  const selectedNode = selectedId ? nodes.find((n) => n.id === selectedId) : null;

  return (
    <div className="relative h-full w-full">
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
      {selectedNode ? <ResearchNodeDetail node={selectedNode} /> : null}
    </div>
  );
}

export function ResearchCanvas(props: {
  nodes: ResearchGraphNode[];
  edges: ResearchGraphEdge[];
  members?: ResearchFleetMember[];
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
