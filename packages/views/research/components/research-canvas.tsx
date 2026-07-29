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
import type { ResearchGraphEdge, ResearchGraphNode } from "@multica/core/types";
import { layoutResearchGraph } from "../lib/layout-graph";
import { ResearchGraphNode as ResearchGraphNodeView } from "./research-graph-node";

const nodeTypes: NodeTypes = {
  research: ResearchGraphNodeView,
};

function ResearchCanvasInner({
  nodes,
  edges,
  selectedId,
  onSelect,
}: {
  nodes: ResearchGraphNode[];
  edges: ResearchGraphEdge[];
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
        const dashed =
          edgeType === "contradicts" ||
          edgeType === "supersedes" ||
          edgeType === "abandons";
        return {
          ...e,
          animated: edgeType === "leads_to",
          style: {
            stroke:
              edgeType === "supports"
                ? "var(--color-emerald-500, #10b981)"
                : edgeType === "contradicts" || edgeType === "abandons"
                  ? "var(--color-destructive, #ef4444)"
                  : edgeType === "pivot" || edgeType === "supersedes"
                    ? "var(--color-orange-500, #f97316)"
                    : undefined,
            strokeDasharray: dashed ? "6 4" : undefined,
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

  return (
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
      className="bg-[radial-gradient(circle_at_20%_0%,hsl(var(--primary)/0.06),transparent_45%),radial-gradient(circle_at_80%_100%,hsl(var(--chart-2)/0.08),transparent_40%),hsl(var(--background))]"
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
  );
}

export function ResearchCanvas(props: {
  nodes: ResearchGraphNode[];
  edges: ResearchGraphEdge[];
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
