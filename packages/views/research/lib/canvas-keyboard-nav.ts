/**
 * LRM-1105 / LRM-1102 — research canvas keyboard navigation (pure helpers).
 *
 * Owns ←→↑↓ / Home / End neighbor math (↑↓ = semantics A: fork-point only) plus
 * key→action resolution, accessible names, and aria-live announcement merge.
 *
 * Kept in a dedicated module (not layout-graph) so LRM-1091 planar rewrite of
 * layout geometry cannot delete the a11y contract. Canvas/graph-node wiring
 * stays out of this file until 1091 lands.
 */

import type { ResearchGraphEdge, ResearchGraphNode } from "@multica/core/types";
import { LOGIC_LANE_IDS, laneForNode, type LogicLaneId } from "./logic-lanes";
import {
  CONTENT_FACE_A11Y_ZH,
  CONTENT_FACE_COPY_ZH,
  CONTENT_FACE_KEYS,
  resolveContentFaceValues,
} from "./research-node-content-faces";

export type GraphEdgeLike = {
  from_node_id: string;
  to_node_id: string;
  edge_type: string;
};

export type CanvasOverlayLayer = "ring" | "detail" | null;

export type CanvasKeyboardAction =
  | { type: "moveFocus"; nodeId: string }
  | { type: "openDetail" }
  | { type: "openRing" }
  | { type: "closeOverlay"; layer: "ring" | "detail" }
  | { type: "zoomIn" }
  | { type: "zoomOut" }
  | { type: "fitView" }
  | { type: "noop" };

export type CanvasKeyboardContext = {
  focusId: string | null;
  nodes: ResearchGraphNode[];
  edges: GraphEdgeLike[];
  /** Preferred branch head (or any id on that lane) for ←→ at forks / ↑↓ cycle. */
  activeBranchId?: string | null;
  overlay: CanvasOverlayLayer;
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

/** Main-chain order following `leads_to` from the goal/root (BFS). */
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
  const byId = new Map(nodes.map((n) => [n.id, n]));
  while (queue.length) {
    const id = queue.shift()!;
    if (seen.has(id)) continue;
    seen.add(id);
    ordered.push(id);
    for (const next of sortByLane(outs.get(id) ?? [], byId)) queue.push(next);
  }
  for (const n of nodes) {
    if (!seen.has(n.id)) ordered.push(n.id);
  }
  return ordered;
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
  options?: { preferLaneFrom?: string | null },
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
 */
export function crossLaneNeighbor(
  nodes: ResearchGraphNode[],
  edges: GraphEdgeLike[],
  currentId: string,
  direction: 1 | -1,
  options?: { activeBranchId?: string | null },
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

const STATUS_LABEL_ZH: Record<string, string> = {
  done: "已完成",
  completed: "已完成",
  resolved: "已完成",
  success: "已完成",
  failed: "已失败",
  error: "已失败",
  conflict: "冲突",
  refuted: "已否定",
  dead_end: "死胡同",
  abandoned: "已废弃",
  cancelled: "已取消",
  active: "运行中",
  running: "运行中",
  in_progress: "运行中",
  waiting: "等待中",
  pending: "等待中",
  blocked: "阻塞",
  queued: "排队中",
};

const LANE_LABEL_ZH: Record<LogicLaneId, string> = {
  orchestrate: "编排轨",
  source: "寻源轨",
  deep_read: "深读轨",
  validate: "校验轨",
  draft: "起草轨",
};

function statusPhrase(node: ResearchGraphNode): string {
  const s = (node.status || "").toLowerCase();
  if (STATUS_LABEL_ZH[s]) return STATUS_LABEL_ZH[s]!;
  if (node.node_type === "conflict") return "冲突";
  if (node.node_type === "refuted") return "已否定";
  if (node.node_type === "dead_end") return "死胡同";
  return s || "未知状态";
}

function isLowConfidence(node: ResearchGraphNode): boolean {
  const payload = node.payload;
  if (!payload || typeof payload !== "object") return false;
  const p = payload as { low_confidence?: unknown; confidence?: unknown };
  if (p.low_confidence === true) return true;
  if (typeof p.confidence === "number" && p.confidence < 0.5) return true;
  return false;
}

/** `aria-label` = 标题 + 状态 + 轨道（低置信含文本）. */
export function buildNodeAccessibleName(node: ResearchGraphNode): string {
  const title = node.title?.trim() || node.id;
  const status = statusPhrase(node);
  const lane = LANE_LABEL_ZH[laneForNode(node)];
  const parts = [title, status, lane];
  if (isLowConfidence(node)) parts.push("低置信");
  // LRM-1332: four content-face labels must not rely on visual position alone.
  const faces = resolveContentFaceValues(node, "surface", CONTENT_FACE_COPY_ZH);
  for (const key of CONTENT_FACE_KEYS) {
    parts.push(`${CONTENT_FACE_A11Y_ZH[key]} ${faces[key]}`);
  }
  return parts.join("，");
}

export type StatusAnnouncement = {
  nodeId: string;
  title: string;
  status: string;
};

/**
 * Same-tick merge for aria-live=polite: one node →「标题 已完成」;
 * multiple →「N 个节点更新」.
 */
export function mergeStatusAnnouncements(
  updates: StatusAnnouncement[],
): string {
  if (updates.length === 0) return "";
  if (updates.length === 1) {
    const u = updates[0]!;
    const s = (u.status || "").toLowerCase();
    const phrase = STATUS_LABEL_ZH[s] ?? s;
    return `${u.title} ${phrase}`.trim();
  }
  return `${updates.length} 个节点更新`;
}

function moveOrNoop(nodeId: string | null): CanvasKeyboardAction {
  return nodeId ? { type: "moveFocus", nodeId } : { type: "noop" };
}

/**
 * Resolve a keyboard event against LRM-1102 desktop canvas mapping.
 * Overlay Esc is layered: ring → detail → (caller restores node focus).
 */
export function resolveCanvasKeyAction(
  key: string,
  ctx: CanvasKeyboardContext,
): CanvasKeyboardAction {
  const { focusId, nodes, edges, activeBranchId, overlay } = ctx;

  if (key === "Escape") {
    if (overlay === "ring") return { type: "closeOverlay", layer: "ring" };
    if (overlay === "detail") return { type: "closeOverlay", layer: "detail" };
    return { type: "noop" };
  }

  // While overlays own Esc/focus, ignore graph navigation keys.
  if (overlay === "ring" || overlay === "detail") {
    return { type: "noop" };
  }

  if (key === "Enter" || key === " ") {
    return focusId ? { type: "openDetail" } : { type: "noop" };
  }
  if (key === "." || key === "ContextMenu") {
    return focusId ? { type: "openRing" } : { type: "noop" };
  }
  if (key === "+" || key === "=") return { type: "zoomIn" };
  if (key === "-" || key === "_") return { type: "zoomOut" };
  if (key === "0") return { type: "fitView" };

  const chain = mainChainOrder(nodes, edges);
  if (key === "Home") {
    return moveOrNoop(chain[0] ?? null);
  }
  if (key === "End") {
    return moveOrNoop(chain.length ? chain[chain.length - 1]! : null);
  }

  if (!focusId) return { type: "noop" };

  if (key === "ArrowRight") {
    return moveOrNoop(
      mainChainNeighbor(nodes, edges, focusId, 1, {
        preferLaneFrom: activeBranchId,
      }),
    );
  }
  if (key === "ArrowLeft") {
    return moveOrNoop(mainChainNeighbor(nodes, edges, focusId, -1));
  }
  if (key === "ArrowDown") {
    return moveOrNoop(
      crossLaneNeighbor(nodes, edges, focusId, 1, { activeBranchId }),
    );
  }
  if (key === "ArrowUp") {
    return moveOrNoop(
      crossLaneNeighbor(nodes, edges, focusId, -1, { activeBranchId }),
    );
  }

  return { type: "noop" };
}

/** Convenience: Shift+F10 shares the ring open action with `.`. */
export function resolveCanvasKeyEvent(
  event: Pick<KeyboardEvent, "key" | "shiftKey">,
  ctx: CanvasKeyboardContext,
): CanvasKeyboardAction {
  if (event.key === "F10" && event.shiftKey) {
    return resolveCanvasKeyAction("ContextMenu", ctx);
  }
  return resolveCanvasKeyAction(event.key, ctx);
}

/** Re-export edge type for callers wiring ResearchGraphEdge[]. */
export type { ResearchGraphEdge };
