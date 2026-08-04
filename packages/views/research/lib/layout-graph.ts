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
  LOGIC_LANE_IDS,
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
  onNodeCommand?: (node: ResearchGraphNode, action: ResearchNodeCommandAction) => Promise<void>;
  onViewDetail?: (node: ResearchGraphNode) => void;
  menuOpen?: boolean;
  onMenuOpenChange?: (open: boolean) => void;
  /** Gutter chrome only. */
  gutterSegments?: GitLaneSegment[];
  gutterHeight?: number;
  gutterWidth?: number;
  /** LRM-1295 presentational role supplied by the aggregate selector. */
  aggregateTier?: AggregateTreeCardTier;
  /** LRM-1295 card dimensions; old git-lane cards omit this. */
  aggregateSize?: AggregateTreeCardSize;
};

export type ResearchFlowEdgeData = {
  edgeType: string;
};

/**
 * LRM-1295 aggregate-tree viewport roles. The selector owns deriving these
 * collections from the snapshot; this layout deliberately does not read or
 * infer parent/child projection fields.
 */
export type AggregateTreeCardTier = "parent" | "sibling" | "child";

export type AggregateTreeCardSize = {
  width: number;
  height: number;
};

export type AggregateTreeShellInput = {
  parent: ResearchGraphNode;
  siblings: ResearchGraphNode[];
  children: ResearchGraphNode[];
  edges: ResearchGraphEdge[];
};

export type AggregateTreeShellLayout = {
  nodes: Node<ResearchFlowNodeData>[];
  edges: Edge<ResearchFlowEdgeData>[];
  board: { width: number; height: number };
};

/** Product-spec geometry for the three-column aggregate viewport. */
export const AGGREGATE_PARENT_CARD: AggregateTreeCardSize = { width: 282, height: 242 };
export const AGGREGATE_SIBLING_CARD: AggregateTreeCardSize = { width: 218, height: 142 };
export const AGGREGATE_CHILD_CARD: AggregateTreeCardSize = { width: 184, height: 76 };

const AGGREGATE_BOARD_WIDTH = 1408;
const AGGREGATE_BOARD_MIN_HEIGHT = 655;
const AGGREGATE_SIDE_INSET = 48;
const AGGREGATE_SIBLING_X = 454;
const AGGREGATE_SIBLING_COLUMN_GAP = 42;
const AGGREGATE_SIBLING_SPREAD_ROW_GAP = 240;
const AGGREGATE_SIBLING_DENSE_ROW_GAP = 56;
const AGGREGATE_CHILD_X = 1022;
const AGGREGATE_CHILD_GAP = 16;
const AGGREGATE_TOP_INSET = 56;

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

function aggregateTreeNode(
  research: ResearchGraphNode,
  tier: AggregateTreeCardTier,
  position: { x: number; y: number },
  size: AggregateTreeCardSize,
): Node<ResearchFlowNodeData> {
  return {
    id: research.id,
    type: "research",
    position,
    data: {
      research,
      logicRole: isLogicStartNode(research) ? "start" : "step",
      laneId: laneForNode(research),
      branchId: research.theme_key ?? branchIdForLane(0),
      branchColor: colorForLane(0),
      aggregateTier: tier,
      aggregateSize: size,
    },
    draggable: false,
    zIndex: 2,
    style: { width: size.width, height: size.height },
  };
}

/**
 * LRM-1295 presentational shell for the aggregate selector's three-column
 * window. It intentionally trusts only the explicit collections it receives:
 * contract reconciliation stays in FE A / the server, never in the canvas.
 */
export function layoutAggregateTreeShell({
  parent,
  siblings,
  children,
  edges,
}: AggregateTreeShellInput): AggregateTreeShellLayout {
  // A two-branch window is the common route state. Give each branch a distinct
  // vertical track so the aggregate tree reads as a branching surface rather
  // than a narrow row floating in the middle of the desktop canvas.
  const siblingColumns = siblings.length <= 2 ? 1 : 2;
  const siblingRows = Math.max(1, Math.ceil(siblings.length / siblingColumns));
  // Spread one or two visible rows across the desktop canvas. Denser projections
  // use a compact rhythm so 5–8 siblings remain legible without shrinking the tree.
  const siblingRowGap =
    siblingRows <= 2 ? AGGREGATE_SIBLING_SPREAD_ROW_GAP : AGGREGATE_SIBLING_DENSE_ROW_GAP;
  const siblingHeight =
    siblingRows * AGGREGATE_SIBLING_CARD.height +
    (siblingRows - 1) * siblingRowGap;
  const childHeight =
    Math.max(1, children.length) * AGGREGATE_CHILD_CARD.height +
    Math.max(0, children.length - 1) * AGGREGATE_CHILD_GAP;
  const boardHeight = Math.max(
    AGGREGATE_BOARD_MIN_HEIGHT,
    siblingHeight + AGGREGATE_TOP_INSET * 2,
    childHeight + AGGREGATE_TOP_INSET * 2,
    AGGREGATE_PARENT_CARD.height + AGGREGATE_TOP_INSET * 2,
  );
  const siblingY = Math.round((boardHeight - siblingHeight) / 2);
  const childY = Math.round((boardHeight - childHeight) / 2);
  const parentY = Math.round((boardHeight - AGGREGATE_PARENT_CARD.height) / 2);

  const laidNodes: Node<ResearchFlowNodeData>[] = [
    aggregateTreeNode(
      parent,
      "parent",
      { x: AGGREGATE_SIDE_INSET, y: parentY },
      AGGREGATE_PARENT_CARD,
    ),
    ...siblings.map((sibling, index) => {
      const column = index % siblingColumns;
      const row = Math.floor(index / siblingColumns);
      return aggregateTreeNode(
        sibling,
        "sibling",
        {
          x: AGGREGATE_SIBLING_X +
            column * (AGGREGATE_SIBLING_CARD.width + AGGREGATE_SIBLING_COLUMN_GAP),
          y: siblingY + row * (AGGREGATE_SIBLING_CARD.height + siblingRowGap),
        },
        AGGREGATE_SIBLING_CARD,
      );
    }),
    ...children.map((child, index) =>
      aggregateTreeNode(
        child,
        "child",
        { x: AGGREGATE_CHILD_X, y: childY + index * (AGGREGATE_CHILD_CARD.height + AGGREGATE_CHILD_GAP) },
        AGGREGATE_CHILD_CARD,
      ),
    ),
  ];
  const visibleIds = new Set(laidNodes.map((node) => node.id));
  const shellEdges = edges
    .filter(
      (edge) =>
        edge.edge_type === "leads_to" &&
        visibleIds.has(edge.from_node_id) &&
        visibleIds.has(edge.to_node_id),
    )
    .map((edge) => ({
      id: edge.id,
      source: edge.from_node_id,
      target: edge.to_node_id,
      type: "smoothstep",
      className: "aggregate-tree-edge",
      data: { edgeType: edge.edge_type },
      zIndex: 0,
    } satisfies Edge<ResearchFlowEdgeData>));

  return {
    nodes: laidNodes,
    edges: shellEdges,
    board: { width: AGGREGATE_BOARD_WIDTH, height: boardHeight },
  };
}

/** Public AABB helpers for LRM-1295 aggregate-shell geometry regressions. */
export function aggregateTreeCardBoxes(
  laid: AggregateTreeShellLayout,
): { id: string; tier: AggregateTreeCardTier; x: number; y: number; w: number; h: number }[] {
  return laid.nodes.map((node) => {
    const size = node.data.aggregateSize!;
    return {
      id: node.id,
      tier: node.data.aggregateTier!,
      x: node.position.x,
      y: node.position.y,
      w: size.width,
      h: size.height,
    };
  });
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

/** Topological layer index along graph edges (keyboard rank / parallel groups). */
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
    outs.get(e.from_node_id)!.push(e.to_node_id);
    indeg.set(e.to_node_id, (indeg.get(e.to_node_id) ?? 0) + 1);
  }

  const layer = new Map<string, number>();
  const queue: string[] = [];
  for (const n of nodes) {
    if ((indeg.get(n.id) ?? 0) === 0) {
      queue.push(n.id);
      layer.set(n.id, 0);
    }
  }
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
