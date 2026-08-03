import type { Edge, Node } from "@xyflow/react";
import type { ResearchGraphEdge, ResearchGraphNode } from "@multica/core/types";
import {
  LOGIC_END_NODE_ID,
  LOGIC_LANE_IDS,
  isLogicStartNode,
  laneForNode,
  type LogicLaneId,
} from "./logic-lanes";

export const RESEARCH_NODE_WIDTH = 200;
/** Approximate rendered card height (title + status + 2-line summary). */
/** Includes LRM-981 on-card retry CTA clearance. */
export const RESEARCH_NODE_HEIGHT = 118;

export const LOGIC_LANE_HEIGHT = 148;
export const LOGIC_LAYER_GAP = 236;
export const LOGIC_MARGIN_X = 28;
export const LOGIC_MARGIN_Y = 20;

export type ResearchFlowNodeData = {
  /** Present on research cards; omitted on lane-band chrome nodes. */
  research?: ResearchGraphNode;
  /** Live presence caption from research presence map (optional overlay). */
  presenceLabel?: string;
  /** Count of high-weight sources feeding a finding (optional badge). */
  sourceBadgeCount?: number;
  /** LRM-908 logic role for start/end visual weight. */
  logicRole?: "start" | "end" | "step";
  /** Assigned swimlane (C2). */
  laneId?: LogicLaneId;
  /** Lane band chrome (non-interactive). */
  laneLabelKey?: LogicLaneId;
  /** LRM-981 — scannable retry/reroute from the card surface. */
  onRetry?: (node: ResearchGraphNode) => void;
};

export type ResearchFlowEdgeData = {
  edgeType: string;
};

function layerIndex(
  nodes: ResearchGraphNode[],
  edges: ResearchGraphEdge[],
): Map<string, number> {
  const ids = new Set(nodes.map((n) => n.id));
  const indeg = new Map<string, number>();
  const outs = new Map<string, string[]>();
  for (const n of nodes) {
    indeg.set(n.id, 0);
    outs.set(n.id, []);
  }
  for (const e of edges) {
    if (!ids.has(e.from_node_id) || !ids.has(e.to_node_id)) continue;
    // Prefer leads_to for spine layering; other edges still constrain lightly.
    outs.get(e.from_node_id)!.push(e.to_node_id);
    indeg.set(e.to_node_id, (indeg.get(e.to_node_id) ?? 0) + 1);
  }

  const layer = new Map<string, number>();
  const queue: string[] = [];
  for (const n of nodes) {
    if ((indeg.get(n.id) ?? 0) === 0) {
      queue.push(n.id);
      layer.set(n.id, isLogicStartNode(n) ? 0 : 0);
    }
  }
  // Prefer goal roots at layer 0.
  for (const n of nodes) {
    if (n.node_type === "goal") layer.set(n.id, 0);
  }

  while (queue.length) {
    const id = queue.shift()!;
    const base = layer.get(id) ?? 0;
    for (const next of outs.get(id) ?? []) {
      const nextLayer = Math.max(layer.get(next) ?? 0, base + 1);
      layer.set(next, nextLayer);
      const left = (indeg.get(next) ?? 1) - 1;
      indeg.set(next, left);
      if (left === 0) queue.push(next);
    }
  }

  // Orphans / cycles: place after max known layer by created order.
  let max = 0;
  for (const v of layer.values()) max = Math.max(max, v);
  for (const n of nodes) {
    if (!layer.has(n.id)) {
      max += 1;
      layer.set(n.id, max);
    }
  }
  return layer;
}

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

/**
 * LRM-908: left→right main path + horizontal role swimlanes (C1–C3).
 * Positions are client-only (not persisted). Injects a synthetic END card (C4).
 */
export function layoutResearchGraph(
  nodes: ResearchGraphNode[],
  edges: ResearchGraphEdge[],
  options?: { includeEnd?: boolean },
): { nodes: Node<ResearchFlowNodeData>[]; edges: Edge<ResearchFlowEdgeData>[] } {
  const includeEnd = options?.includeEnd !== false;
  const sessionId = nodes[0]?.session_id ?? "session";
  const workingNodes = [...nodes];
  const workingEdges = [...edges];

  if (includeEnd && workingNodes.length > 0 && !workingNodes.some((n) => n.id === LOGIC_END_NODE_ID)) {
    const end = makeSyntheticEnd(sessionId);
    workingNodes.push(end);
    // Link rightmost layer roots / sinks into END via leads_to for spine readability.
    const layers = layerIndex(nodes, edges);
    let maxLayer = 0;
    for (const v of layers.values()) maxLayer = Math.max(maxLayer, v);
    const sinks = nodes.filter((n) => {
      const hasOut = edges.some(
        (e) => e.from_node_id === n.id && nodes.some((x) => x.id === e.to_node_id),
      );
      return !hasOut || (layers.get(n.id) ?? 0) === maxLayer;
    });
    const attachFrom =
      sinks.find((n) => n.node_type === "stage_gate" || n.node_type === "finding") ??
      sinks[sinks.length - 1] ??
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

  const layers = layerIndex(workingNodes, workingEdges);
  let maxLayer = 0;
  for (const v of layers.values()) maxLayer = Math.max(maxLayer, v);
  // Pin END to the far right on the orchestrate lane.
  if (workingNodes.some((n) => n.id === LOGIC_END_NODE_ID)) {
    layers.set(LOGIC_END_NODE_ID, maxLayer + 1);
    maxLayer += 1;
  }

  const laneBuckets = new Map<string, ResearchGraphNode[]>();
  for (const n of workingNodes) {
    const lane = laneForNode(n);
    const key = `${layers.get(n.id) ?? 0}:${lane}`;
    const list = laneBuckets.get(key) ?? [];
    list.push(n);
    laneBuckets.set(key, list);
  }

  const boardWidth =
    LOGIC_MARGIN_X + (maxLayer + 1) * LOGIC_LAYER_GAP + RESEARCH_NODE_WIDTH + 48;
  const boardHeight = LOGIC_MARGIN_Y + LOGIC_LANE_IDS.length * LOGIC_LANE_HEIGHT + 24;

  const bandNodes: Node<ResearchFlowNodeData>[] = LOGIC_LANE_IDS.map((laneId, index) => ({
    id: `lane-band-${laneId}`,
    type: "laneBand",
    position: { x: 0, y: LOGIC_MARGIN_Y + index * LOGIC_LANE_HEIGHT },
    data: {
      laneId,
      laneLabelKey: laneId,
      logicRole: "step",
    },
    draggable: false,
    selectable: false,
    focusable: false,
    connectable: false,
    zIndex: -2,
    style: { width: boardWidth, height: LOGIC_LANE_HEIGHT, pointerEvents: "none" },
  }));

  const rfNodes: Node<ResearchFlowNodeData>[] = workingNodes.map((n) => {
    const laneId = laneForNode(n);
    const laneIndex = LOGIC_LANE_IDS.indexOf(laneId);
    const layer = layers.get(n.id) ?? 0;
    const bucketKey = `${layer}:${laneId}`;
    const bucket = laneBuckets.get(bucketKey) ?? [n];
    const stackIndex = Math.max(
      0,
      bucket.findIndex((x) => x.id === n.id),
    );
    const stackOffset = stackIndex * 10;
    const logicRole =
      n.id === LOGIC_END_NODE_ID ? "end" : isLogicStartNode(n) ? "start" : "step";
    const width =
      logicRole === "start" || logicRole === "end"
        ? RESEARCH_NODE_WIDTH + 16
        : RESEARCH_NODE_WIDTH;
    const x = LOGIC_MARGIN_X + layer * LOGIC_LAYER_GAP + stackOffset;
    const y =
      LOGIC_MARGIN_Y +
      laneIndex * LOGIC_LANE_HEIGHT +
      (LOGIC_LANE_HEIGHT - RESEARCH_NODE_HEIGHT) / 2 +
      stackOffset * 0.4;

    return {
      id: n.id,
      type: "research",
      position: { x, y },
      data: { research: n, logicRole, laneId },
      draggable: true,
      zIndex: 2,
      style: { width },
    };
  });

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
      type: "smoothstep",
      data: { edgeType: e.edge_type },
      zIndex: 1,
    }));

  // Keep boardHeight referenced for future viewport padding helpers.
  void boardHeight;

  return { nodes: [...bandNodes, ...rfNodes], edges: rfEdges };
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
