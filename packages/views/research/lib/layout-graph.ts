import type { Edge, Node } from "@xyflow/react";
import type { ResearchGraphEdge, ResearchGraphNode } from "@multica/core/types";
import {
  branchIdForLane,
  colorForLane,
  computeGitTopology,
  type GitLaneSegment,
  type GitTopologyNode,
} from "./git-topology";
import {
  LOGIC_END_NODE_ID,
  isLogicStartNode,
  laneForNode,
  type LogicLaneId,
} from "./logic-lanes";

/** LRM-1116 card size band: 220–260 × 68–88 (prototype 240×76). */
export const RESEARCH_NODE_WIDTH = 240;
export const RESEARCH_NODE_HEIGHT = 76;

/** Vertical row pitch — one topology node per row, planar (no stack). */
export const GIT_ROW_GAP = 96;
export const GIT_GUTTER_WIDTH = 72;
export const GIT_LANE_CARD_OFFSET = 56;
export const GIT_LANE_LINE_GAP = 18;
export const GIT_MARGIN_TOP = 24;
export const GIT_MARGIN_RIGHT = 48;
export const GIT_PORT_BASE_X = 34;

/** @deprecated LRM-1091 — kept for any stray imports; planar layout ignores bands. */
export const LOGIC_LANE_HEIGHT = GIT_ROW_GAP;
/** @deprecated horizontal layer gap from LRM-908. */
export const LOGIC_LAYER_GAP = GIT_ROW_GAP;
export const LOGIC_MARGIN_X = GIT_GUTTER_WIDTH + 16;
export const LOGIC_MARGIN_Y = GIT_MARGIN_TOP;

export type ResearchFlowNodeData = {
  research?: ResearchGraphNode;
  presenceLabel?: string;
  sourceBadgeCount?: number;
  logicRole?: "start" | "end" | "step";
  /** Role swimlane (legacy C2) — still set for strip/status helpers. */
  laneId?: LogicLaneId;
  /** Git branch lane index (0=main). */
  gitLane?: number;
  branchId?: string;
  branchColor?: string;
  row?: number;
  laneLabelKey?: LogicLaneId;
  onRetry?: (node: ResearchGraphNode) => void;
  onViewDetail?: (node: ResearchGraphNode) => void;
  menuOpen?: boolean;
  onMenuOpenChange?: (open: boolean) => void;
  /** Gutter chrome only. */
  gutterSegments?: GitLaneSegment[];
  gutterHeight?: number;
  gutterWidth?: number;
};

export type ResearchFlowEdgeData = {
  edgeType: string;
};

function makeSyntheticEnd(sessionId: string): ResearchGraphNode {
  const ts = new Date().toISOString();
  return {
    id: LOGIC_END_NODE_ID,
    session_id: sessionId,
    node_type: "stage_gate",
    title: "View delivery",
    summary: "Open the delivery reading view",
    status: "pending",
    actor_agent_id: null,
    payload: { logic_role: "end", logic_lane: "orchestrate" },
    created_at: ts,
    updated_at: ts,
  };
}

function portX(lane: number): number {
  return GIT_PORT_BASE_X + lane * GIT_LANE_LINE_GAP;
}

function rowCenterY(row: number): number {
  return GIT_MARGIN_TOP + row * GIT_ROW_GAP + GIT_ROW_GAP / 2;
}

function cardPosition(topo: GitTopologyNode): { x: number; y: number } {
  return {
    x: GIT_GUTTER_WIDTH + 16 + topo.lane * GIT_LANE_CARD_OFFSET,
    y: GIT_MARGIN_TOP + topo.row * GIT_ROW_GAP + (GIT_ROW_GAP - RESEARCH_NODE_HEIGHT) / 2,
  };
}

/**
 * LRM-1091 / LRM-1116: planar Git graph — topology rows top→bottom,
 * left lane gutter, card nodes. No perspective / stackOffset / lane bands.
 */
export function layoutResearchGraph(
  nodes: ResearchGraphNode[],
  edges: ResearchGraphEdge[],
  options?: { includeEnd?: boolean },
): {
  nodes: Node<ResearchFlowNodeData>[];
  edges: Edge<ResearchFlowEdgeData>[];
  topology: Map<string, GitTopologyNode>;
  board: { width: number; height: number };
} {
  const includeEnd = options?.includeEnd !== false;
  const sessionId = nodes[0]?.session_id ?? "session";
  const workingNodes = [...nodes];
  const workingEdges = [...edges];

  if (
    includeEnd &&
    workingNodes.length > 0 &&
    !workingNodes.some((n) => n.id === LOGIC_END_NODE_ID)
  ) {
    const end = makeSyntheticEnd(sessionId);
    workingNodes.push(end);
    const topoPreview = computeGitTopology(nodes, edges, {
      portX,
      rowCenterY,
    });
    const lastId = topoPreview.order[topoPreview.order.length - 1];
    const attachFrom =
      (lastId && nodes.find((n) => n.id === lastId)) ||
      nodes[nodes.length - 1];
    if (attachFrom) {
      workingEdges.push({
        id: `${LOGIC_END_NODE_ID}-edge`,
        session_id: sessionId,
        from_node_id: attachFrom.id,
        to_node_id: LOGIC_END_NODE_ID,
        edge_type: "leads_to",
        created_at: end.created_at,
      });
    }
  }

  const topologyResult = computeGitTopology(workingNodes, workingEdges, {
    portX,
    rowCenterY,
  });
  const rowCount = Math.max(1, topologyResult.order.length);
  const boardHeight = GIT_MARGIN_TOP + rowCount * GIT_ROW_GAP + 48;
  const boardWidth =
    GIT_GUTTER_WIDTH +
    16 +
    topologyResult.maxLane * GIT_LANE_CARD_OFFSET +
    RESEARCH_NODE_WIDTH +
    GIT_MARGIN_RIGHT;

  const gutterNode: Node<ResearchFlowNodeData> = {
    id: "git-gutter",
    type: "gitGutter",
    position: { x: 0, y: 0 },
    data: {
      gutterSegments: topologyResult.segments,
      gutterHeight: boardHeight,
      gutterWidth: GIT_GUTTER_WIDTH,
      logicRole: "step",
    },
    draggable: false,
    selectable: false,
    focusable: false,
    connectable: false,
    zIndex: -2,
    style: {
      width: boardWidth,
      height: boardHeight,
      pointerEvents: "none",
    },
  };

  const rfNodes: Node<ResearchFlowNodeData>[] = workingNodes.map((n) => {
    const topo = topologyResult.byId.get(n.id)!;
    const logicRole =
      n.id === LOGIC_END_NODE_ID
        ? "end"
        : isLogicStartNode(n)
          ? "start"
          : "step";
    const pos = cardPosition(topo);
    return {
      id: n.id,
      type: "research",
      position: pos,
      data: {
        research: n,
        logicRole,
        laneId: laneForNode(n),
        gitLane: topo.lane,
        branchId: topo.branchId || branchIdForLane(topo.lane),
        branchColor: colorForLane(topo.lane),
        row: topo.row,
      },
      draggable: false,
      zIndex: 2,
      style: { width: RESEARCH_NODE_WIDTH },
    };
  });

  // Edges stay in the data model for selection/path helpers but are not drawn
  // through cards — gutter SVG owns branch visuals (no edge-through-card).
  const rfEdges: Edge<ResearchFlowEdgeData>[] = workingEdges
    .filter(
      (e) =>
        workingNodes.some((n) => n.id === e.from_node_id) &&
        workingNodes.some((n) => n.id === e.to_node_id),
    )
    .map((e) => ({
      id: e.id,
      source: e.from_node_id,
      target: e.to_node_id,
      type: "default",
      hidden: true,
      data: { edgeType: e.edge_type },
      zIndex: 0,
    }));

  return {
    nodes: [gutterNode, ...rfNodes],
    edges: rfEdges,
    topology: topologyResult.byId,
    board: { width: boardWidth, height: boardHeight },
  };
}

/** Public AABB helpers for layout regression tests. */
export function layoutCardBoxes(
  laid: ReturnType<typeof layoutResearchGraph>,
): { id: string; x: number; y: number; w: number; h: number }[] {
  return laid.nodes
    .filter((n) => n.type === "research")
    .map((n) => ({
      id: n.id,
      x: n.position.x,
      y: n.position.y,
      w: RESEARCH_NODE_WIDTH,
      h: RESEARCH_NODE_HEIGHT,
    }));
}

type GraphEdgeLike = {
  from_node_id: string;
  to_node_id: string;
  edge_type: string;
};

function leadsToOuts(
  edges: GraphEdgeLike[],
  ids: Set<string>,
): Map<string, string[]> {
  const outs = new Map<string, string[]>();
  for (const e of edges) {
    if (e.edge_type !== "leads_to") continue;
    if (!ids.has(e.from_node_id) || !ids.has(e.to_node_id)) continue;
    const list = outs.get(e.from_node_id) ?? [];
    list.push(e.to_node_id);
    outs.set(e.from_node_id, list);
  }
  return outs;
}

function leadsToIns(
  edges: GraphEdgeLike[],
  ids: Set<string>,
): Map<string, string[]> {
  const ins = new Map<string, string[]>();
  for (const e of edges) {
    if (e.edge_type !== "leads_to") continue;
    if (!ids.has(e.from_node_id) || !ids.has(e.to_node_id)) continue;
    const list = ins.get(e.to_node_id) ?? [];
    list.push(e.from_node_id);
    ins.set(e.to_node_id, list);
  }
  return ins;
}

function sortByLane(
  nodeIds: string[],
  byId: Map<string, ResearchGraphNode>,
): string[] {
  return [...nodeIds].sort((a, b) => {
    const la = LOGIC_LANE_IDS.indexOf(laneForNode(byId.get(a)!));
    const lb = LOGIC_LANE_IDS.indexOf(laneForNode(byId.get(b)!));
    if (la !== lb) return la - lb;
    return a.localeCompare(b);
  });
}

/** Rank / layer index used by layout (leads_to spine). Exported for ←→↑↓. */
export function researchGraphRanks(
  nodes: ResearchGraphNode[],
  edges: GraphEdgeLike[],
): Map<string, number> {
  return layerIndex(nodes, edges as ResearchGraphEdge[]);
}

/**
 * Main-chain order following `leads_to` from the goal/root (BFS).
 * Parallel branch heads appear after their fork; extras append at end.
 */
export function mainChainOrder(
  nodes: ResearchGraphNode[],
  edges: GraphEdgeLike[],
): string[] {
  const ids = new Set(nodes.map((n) => n.id));
  const outs = leadsToOuts(edges, ids);
  const roots = nodes.filter(
    (n) =>
      n.node_type === "goal" ||
      !edges.some((e) => e.to_node_id === n.id && e.edge_type === "leads_to"),
  );
  const start = roots.find((n) => n.node_type === "goal") ?? roots[0];
  if (!start) return nodes.map((n) => n.id);

  const ordered: string[] = [];
  const seen = new Set<string>();
  const queue = [start.id];
  while (queue.length) {
    const id = queue.shift()!;
    if (seen.has(id)) continue;
    seen.add(id);
    ordered.push(id);
    const byId = new Map(nodes.map((n) => [n.id, n]));
    for (const next of sortByLane(outs.get(id) ?? [], byId)) queue.push(next);
  }
  for (const n of nodes) {
    if (!seen.has(n.id)) ordered.push(n.id);
  }
  return ordered;
}

/** Same-rank nodes ordered by swimlane (parallel tracks). */
export function parallelGroupAtRank(
  nodes: ResearchGraphNode[],
  edges: GraphEdgeLike[],
  rank: number,
): string[] {
  const ranks = researchGraphRanks(nodes, edges);
  const byId = new Map(nodes.map((n) => [n.id, n]));
  const ids = nodes.filter((n) => ranks.get(n.id) === rank).map((n) => n.id);
  return sortByLane(ids, byId);
}

/** Fork point = ≥2 `leads_to` outs to existing nodes (1102 semantics A gate). */
export function isForkPoint(
  nodeId: string,
  nodes: ResearchGraphNode[],
  edges: GraphEdgeLike[],
): boolean {
  const ids = new Set(nodes.map((n) => n.id));
  const outs = leadsToOuts(edges, ids).get(nodeId) ?? [];
  return outs.length >= 2;
}

/**
 * ←→ along main chain. At a fork going forward, prefer the lane of
 * `preferLaneFrom` (or the first outbound by lane order).
 */
export function mainChainNeighbor(
  nodes: ResearchGraphNode[],
  edges: GraphEdgeLike[],
  currentId: string,
  direction: 1 | -1,
  options?: { preferLaneFrom?: string },
): string | null {
  const ids = new Set(nodes.map((n) => n.id));
  if (!ids.has(currentId)) return null;
  const byId = new Map(nodes.map((n) => [n.id, n]));
  const outs = leadsToOuts(edges, ids);
  const ins = leadsToIns(edges, ids);

  if (direction === 1) {
    const nexts = sortByLane(outs.get(currentId) ?? [], byId);
    if (nexts.length === 0) return null;
    if (nexts.length === 1) return nexts[0]!;
    const prefer = options?.preferLaneFrom;
    if (prefer && byId.has(prefer)) {
      const preferLane = laneForNode(byId.get(prefer)!);
      const match = nexts.find((id) => laneForNode(byId.get(id)!) === preferLane);
      if (match) return match;
      if (nexts.includes(prefer)) return prefer;
    }
    return nexts[0]!;
  }

  const prevs = sortByLane(ins.get(currentId) ?? [], byId);
  if (prevs.length === 0) return null;
  return prevs[0]!;
}

/**
 * ↑↓ cross-lane — **only at fork points** (1102 / 1116 semantics A).
 * Cycles the fork's outbound branch heads ordered by lane.
 * `activeBranchId` is the currently preferred branch head (or any id on that lane).
 */
export function crossLaneNeighbor(
  nodes: ResearchGraphNode[],
  edges: GraphEdgeLike[],
  currentId: string,
  direction: 1 | -1,
  options?: { activeBranchId?: string },
): string | null {
  if (!isForkPoint(currentId, nodes, edges)) return null;
  const ids = new Set(nodes.map((n) => n.id));
  const byId = new Map(nodes.map((n) => [n.id, n]));
  const branches = sortByLane(leadsToOuts(edges, ids).get(currentId) ?? [], byId);
  if (branches.length < 2) return null;

  const active = options?.activeBranchId;
  if (active == null) {
    return direction === 1 ? branches[0]! : branches[branches.length - 1]!;
  }

  let index = 0;
  const direct = branches.indexOf(active);
  if (direct >= 0) {
    index = direct;
  } else if (byId.has(active)) {
    const lane = laneForNode(byId.get(active)!);
    const byLane = branches.findIndex(
      (id) => laneForNode(byId.get(id)!) === lane,
    );
    if (byLane >= 0) index = byLane;
  }

  const nextIndex = (index + direction + branches.length) % branches.length;
  return branches[nextIndex]!;
}
