import type { ResearchGraphNode } from "@multica/core/types";

/** LRM-908 / C2: horizontal swimlanes = role / capability lines. */
export const LOGIC_LANE_IDS = [
  "orchestrate",
  "source",
  "deep_read",
  "validate",
  "draft",
] as const;

export type LogicLaneId = (typeof LOGIC_LANE_IDS)[number];

export const LOGIC_END_NODE_ID = "__research_logic_end__";

export type LogicNodeStatusTone = "ok" | "run" | "wait" | "fail" | "mute";

export type LogicNodeStatusKey =
  | "kickoff"
  | "done"
  | "running"
  | "waiting"
  | "pending_delivery"
  | "failed"
  | "abandoned";

const LANE_SET = new Set<string>(LOGIC_LANE_IDS);

function payloadLane(payload: unknown): LogicLaneId | null {
  if (!payload || typeof payload !== "object") return null;
  const raw =
    (payload as { logic_lane?: unknown }).logic_lane ??
    (payload as { lane?: unknown }).lane;
  if (typeof raw === "string" && LANE_SET.has(raw)) return raw as LogicLaneId;
  return null;
}

/** Map LRM-842 node types (+ optional payload.logic_lane) onto C2 lanes. */
export function laneForNode(node: ResearchGraphNode): LogicLaneId {
  const explicit = payloadLane(node.payload);
  if (explicit) return explicit;

  switch (node.node_type) {
    case "goal":
    case "stage_gate":
    case "product_round_gate":
    case "roster_change":
      return "orchestrate";
    case "probe":
    case "subquestion":
      return "source";
    case "finding":
    case "agent_activity":
      return "deep_read";
    case "conflict":
    case "refuted":
    case "dispute":
    case "dispute_position":
    case "decision":
    case "deliberation":
    case "deliberation_turn":
      return "validate";
    case "dead_end":
    case "pivot":
      return "draft";
    default:
      return "orchestrate";
  }
}

export function isLogicEndNode(node: ResearchGraphNode | { id: string }): boolean {
  return node.id === LOGIC_END_NODE_ID;
}

export function isLogicStartNode(node: ResearchGraphNode): boolean {
  return node.node_type === "goal" || Boolean(
    node.payload &&
      typeof node.payload === "object" &&
      (node.payload as { logic_role?: unknown }).logic_role === "start",
  );
}

export function resolveLogicStatus(node: ResearchGraphNode): {
  key: LogicNodeStatusKey;
  tone: LogicNodeStatusTone;
} {
  if (isLogicEndNode(node)) {
    return { key: "pending_delivery", tone: "wait" };
  }
  if (isLogicStartNode(node)) {
    const done = node.status === "done" || node.status === "completed" || node.status === "resolved";
    return done
      ? { key: "done", tone: "ok" }
      : { key: "kickoff", tone: "ok" };
  }
  const s = (node.status || "").toLowerCase();
  if (s === "failed" || s === "error") return { key: "failed", tone: "fail" };
  if (s === "abandoned" || s === "cancelled") return { key: "abandoned", tone: "mute" };
  if (s === "done" || s === "completed" || s === "resolved" || s === "success") {
    return { key: "done", tone: "ok" };
  }
  if (s === "waiting" || s === "pending" || s === "blocked" || s === "queued") {
    return { key: "waiting", tone: "wait" };
  }
  if (s === "active" || s === "running" || s === "in_progress") {
    return { key: "running", tone: "run" };
  }
  return { key: "waiting", tone: "wait" };
}

/**
 * Prefer a readable main-path spine: follow `leads_to` from goals when present,
 * else fall back to all node ids (for strip / filter).
 */
export function mainPathNodeIds(
  nodes: ResearchGraphNode[],
  edges: { from_node_id: string; to_node_id: string; edge_type: string }[],
): string[] {
  const byId = new Map(nodes.map((n) => [n.id, n]));
  const outs = new Map<string, string[]>();
  for (const e of edges) {
    if (e.edge_type !== "leads_to") continue;
    if (!byId.has(e.from_node_id) || !byId.has(e.to_node_id)) continue;
    const list = outs.get(e.from_node_id) ?? [];
    list.push(e.to_node_id);
    outs.set(e.from_node_id, list);
  }
  const roots = nodes.filter(
    (n) => n.node_type === "goal" || !edges.some((e) => e.to_node_id === n.id && e.edge_type === "leads_to"),
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
    for (const next of outs.get(id) ?? []) queue.push(next);
  }
  for (const n of nodes) {
    if (!seen.has(n.id)) ordered.push(n.id);
  }
  return ordered;
}
