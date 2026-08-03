import type { ResearchGraphEdge, ResearchGraphNode } from "@multica/core/types";

/**
 * LRM-1091 / LRM-1116: deterministic client-only adapter.
 * Maps existing leads_to (and soft parent) edges → topological row + git branch lane.
 * Does not require backend parent/merge schema changes.
 */

export const GIT_BRANCH_COLORS = ["#0f766e", "#c2410c", "#1d4ed8", "#7c3aed", "#b45309"] as const;

export type GitTopologyNode = {
  id: string;
  row: number;
  lane: number;
  branchId: string;
};

export type GitLaneSegment = {
  /** SVG path `d` in gutter coordinates (absolute board space). */
  d: string;
  lane: number;
  color: string;
};

export type GitTopologyResult = {
  order: string[];
  byId: Map<string, GitTopologyNode>;
  maxLane: number;
  segments: GitLaneSegment[];
};

function stableIdSort(a: string, b: string): number {
  return a < b ? -1 : a > b ? 1 : 0;
}

function leadsToEdges(
  edges: ResearchGraphEdge[],
  ids: Set<string>,
): ResearchGraphEdge[] {
  const preferred = edges.filter(
    (e) =>
      e.edge_type === "leads_to" &&
      ids.has(e.from_node_id) &&
      ids.has(e.to_node_id),
  );
  if (preferred.length > 0) return preferred;
  // Soft fallback: any edge between known nodes (deterministic adapter).
  return edges.filter((e) => ids.has(e.from_node_id) && ids.has(e.to_node_id));
}

/** Kahn topo with stable tie-break on created_at then id. */
export function topologicalOrder(
  nodes: ResearchGraphNode[],
  edges: ResearchGraphEdge[],
): string[] {
  const ids = new Set(nodes.map((n) => n.id));
  const byId = new Map(nodes.map((n) => [n.id, n]));
  const rel = leadsToEdges(edges, ids);
  const indeg = new Map<string, number>();
  const outs = new Map<string, string[]>();
  for (const n of nodes) {
    indeg.set(n.id, 0);
    outs.set(n.id, []);
  }
  for (const e of rel) {
    outs.get(e.from_node_id)!.push(e.to_node_id);
    indeg.set(e.to_node_id, (indeg.get(e.to_node_id) ?? 0) + 1);
  }
  for (const [id, list] of outs) {
    list.sort((a, b) => {
      const na = byId.get(a)!;
      const nb = byId.get(b)!;
      const ta = na.created_at.localeCompare(nb.created_at);
      return ta !== 0 ? ta : stableIdSort(a, b);
    });
    outs.set(id, list);
  }

  const ready = nodes
    .filter((n) => (indeg.get(n.id) ?? 0) === 0)
    .sort((a, b) => {
      if (a.node_type === "goal" && b.node_type !== "goal") return -1;
      if (b.node_type === "goal" && a.node_type !== "goal") return 1;
      const t = a.created_at.localeCompare(b.created_at);
      return t !== 0 ? t : stableIdSort(a.id, b.id);
    })
    .map((n) => n.id);

  const ordered: string[] = [];
  const seen = new Set<string>();
  while (ready.length) {
    const id = ready.shift()!;
    if (seen.has(id)) continue;
    seen.add(id);
    ordered.push(id);
    for (const next of outs.get(id) ?? []) {
      const left = (indeg.get(next) ?? 1) - 1;
      indeg.set(next, left);
      if (left === 0) {
        ready.push(next);
        ready.sort((a, b) => {
          const na = byId.get(a)!;
          const nb = byId.get(b)!;
          const t = na.created_at.localeCompare(nb.created_at);
          return t !== 0 ? t : stableIdSort(a, b);
        });
      }
    }
  }
  for (const n of nodes) {
    if (!seen.has(n.id)) ordered.push(n.id);
  }
  return ordered;
}

/**
 * Assign git lanes: first child inherits parent lane; extra children fork;
 * multi-parent nodes merge onto the lowest parent lane.
 */
export function assignGitLanes(
  order: string[],
  edges: ResearchGraphEdge[],
): Map<string, number> {
  const ids = new Set(order);
  const rel = leadsToEdges(
    edges.filter((e) => ids.has(e.from_node_id) && ids.has(e.to_node_id)),
    ids,
  );
  const children = new Map<string, string[]>();
  const parents = new Map<string, string[]>();
  for (const id of order) {
    children.set(id, []);
    parents.set(id, []);
  }
  for (const e of rel) {
    children.get(e.from_node_id)!.push(e.to_node_id);
    parents.get(e.to_node_id)!.push(e.from_node_id);
  }
  // Preserve edge encounter order already stable from topo child sort.
  for (const [id, list] of children) {
    const uniq = [...new Set(list)];
    children.set(id, uniq);
  }
  for (const [id, list] of parents) {
    parents.set(id, [...new Set(list)]);
  }

  const lane = new Map<string, number>();
  let maxLane = 0;

  for (const id of order) {
    const pars = parents.get(id) ?? [];
    if (pars.length === 0) {
      lane.set(id, 0);
      continue;
    }
    if (pars.length >= 2) {
      const parentLanes = pars.map((p) => lane.get(p) ?? 0);
      lane.set(id, Math.min(...parentLanes));
      continue;
    }
    const parent = pars[0]!;
    const siblings = children.get(parent) ?? [];
    // Multi-child parent = fork: each child gets its own lane so parallel
    // branches are scannable (LRM-1116). Single child inherits parent lane.
    if (siblings.length > 1) {
      maxLane += 1;
      lane.set(id, maxLane);
    } else {
      lane.set(id, lane.get(parent) ?? 0);
    }
  }
  return lane;
}

export function branchIdForLane(lane: number): string {
  if (lane === 0) return "main";
  if (lane === 1) return "explore";
  if (lane === 2) return "verify";
  return `branch-${lane}`;
}

export function colorForLane(lane: number): string {
  return GIT_BRANCH_COLORS[lane % GIT_BRANCH_COLORS.length]!;
}

export type GutterGeometry = {
  portX: (lane: number) => number;
  rowCenterY: (row: number) => number;
};

/** Build continuous gutter paths (fork / merge elbows) for the assigned lanes. */
export function buildLaneSegments(
  topology: Omit<GitTopologyResult, "segments">,
  geo: GutterGeometry,
): GitLaneSegment[] {
  const nodes = [...topology.byId.values()].sort((a, b) => a.row - b.row);
  if (nodes.length === 0) return [];

  const byLane = new Map<number, GitTopologyNode[]>();
  for (const n of nodes) {
    const list = byLane.get(n.lane) ?? [];
    list.push(n);
    byLane.set(n.lane, list);
  }

  const segments: GitLaneSegment[] = [];

  // Lane 0: continuous vertical through full span.
  const main = byLane.get(0);
  if (main && main.length > 0) {
    const first = main[0]!;
    const last = main[main.length - 1]!;
    const x = geo.portX(0);
    segments.push({
      lane: 0,
      color: colorForLane(0),
      d: `M ${x} ${geo.rowCenterY(first.row)} V ${geo.rowCenterY(last.row)}`,
    });
  }

  for (const [lane, list] of byLane) {
    if (lane === 0 || list.length === 0) continue;
    const sorted = [...list].sort((a, b) => a.row - b.row);
    const first = sorted[0]!;
    const last = sorted[sorted.length - 1]!;
    const xLane = geo.portX(lane);
    const xMain = geo.portX(0);
    // Fork from main at the row before first (or at first if root fork).
    const forkRow = Math.max(0, first.row - 1);
    const mergeRow = last.row;
    const d = [
      `M ${xMain} ${geo.rowCenterY(forkRow)}`,
      `H ${xLane}`,
      `V ${geo.rowCenterY(mergeRow)}`,
      `H ${xMain}`,
    ].join(" ");
    segments.push({ lane, color: colorForLane(lane), d });
  }

  return segments;
}

export function computeGitTopology(
  nodes: ResearchGraphNode[],
  edges: ResearchGraphEdge[],
  geo: GutterGeometry,
): GitTopologyResult {
  const order = topologicalOrder(nodes, edges);
  const lanes = assignGitLanes(order, edges);
  let maxLane = 0;
  const byId = new Map<string, GitTopologyNode>();
  order.forEach((id, row) => {
    const lane = lanes.get(id) ?? 0;
    maxLane = Math.max(maxLane, lane);
    byId.set(id, {
      id,
      row,
      lane,
      branchId: branchIdForLane(lane),
    });
  });
  const base = { order, byId, maxLane };
  return {
    ...base,
    segments: buildLaneSegments(base, geo),
  };
}

/** Keyboard: next/prev by topology row. */
export function neighborByRow(
  topology: Map<string, GitTopologyNode>,
  focusId: string,
  delta: number,
): string | null {
  const cur = topology.get(focusId);
  if (!cur) return null;
  const targetRow = cur.row + delta;
  for (const n of topology.values()) {
    if (n.row === targetRow) return n.id;
  }
  return null;
}

/** Keyboard: jump to nearest node on adjacent lane. */
export function neighborByLane(
  topology: Map<string, GitTopologyNode>,
  focusId: string,
  laneDelta: number,
): string | null {
  const cur = topology.get(focusId);
  if (!cur) return null;
  const targetLane = cur.lane + laneDelta;
  let best: GitTopologyNode | null = null;
  let bestDist = Number.POSITIVE_INFINITY;
  for (const n of topology.values()) {
    if (n.lane !== targetLane) continue;
    const dist = Math.abs(n.row - cur.row);
    if (dist < bestDist || (dist === bestDist && n.row < (best?.row ?? 0))) {
      best = n;
      bestDist = dist;
    }
  }
  return best?.id ?? null;
}

/** Axis-aligned box overlap test (inclusive edges count as overlap). */
export function boxesOverlap(
  a: { x: number; y: number; w: number; h: number },
  b: { x: number; y: number; w: number; h: number },
): boolean {
  return !(
    a.x + a.w <= b.x ||
    b.x + b.w <= a.x ||
    a.y + a.h <= b.y ||
    b.y + b.h <= a.y
  );
}
