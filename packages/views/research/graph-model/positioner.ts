/**
 * Deterministic canvas positioner for the unified render layer.
 *
 * A layout is a pure function of the current VIEW graph (canonical snapshot
 * minus display-hidden nodes/edges). The same view always yields the same
 * positions — recomputing a snapshot never produces jitter (AC1).
 *
 * Two entry points:
 *   - `deterministicPositions(view)` — full layout from scratch. Pure.
 *   - `recomputeScoped(prev, view, affectedRootIds, newIds)` — retains every
 *     prior position that is not in the affected regions (or a brand-new node)
 *     and only re-places the affected subgraph. This is the "visibility
 *     tombstone only triggers local recompute" guarantee (AC2).
 */
import type { CanvasEdge, CanvasNode } from "@multica/core/adapters";
import type { Point } from "./types";

export const RANK_COLUMN_WIDTH = 320;
export const ROW_HEIGHT = 120;
export const MARGIN_X = 24;
export const MARGIN_Y = 24;

export interface View {
  nodes: CanvasNode[];
  edges: CanvasEdge[];
}

export type PositionMap = ReadonlyMap<string, Point>;

/** Longest-path rank (0 = no dependencies). Deterministic given the node set. */
export function computeRanks(
  nodes: CanvasNode[],
  edges: CanvasEdge[],
): Map<string, number> {
  const idSet = new Set(nodes.map((n) => n.id));
  const indeg = new Map<string, number>();
  const outs = new Map<string, string[]>();
  for (const n of nodes) {
    indeg.set(n.id, 0);
    outs.set(n.id, []);
  }
  for (const e of edges) {
    if (!idSet.has(e.from) || !idSet.has(e.to)) continue;
    outs.get(e.from)!.push(e.to);
    indeg.set(e.to, (indeg.get(e.to) ?? 0) + 1);
  }

  const rank = new Map<string, number>();
  const queue: string[] = [];
  for (const n of nodes) {
    if ((indeg.get(n.id) ?? 0) === 0) {
      rank.set(n.id, 0);
      queue.push(n.id);
    }
  }
  for (const n of nodes) {
    if (!rank.has(n.id)) rank.set(n.id, 0);
  }

  while (queue.length) {
    const id = queue.shift()!;
    const base = rank.get(id) ?? 0;
    for (const next of outs.get(id) ?? []) {
      const nextRank = Math.max(rank.get(next) ?? 0, base + 1);
      rank.set(next, nextRank);
      const left = (indeg.get(next) ?? 1) - 1;
      indeg.set(next, left);
      if (left === 0) queue.push(next);
    }
  }
  return rank;
}

/** Full deterministic layout. Same view → identical positions (AC1). */
export function deterministicPositions(view: View): PositionMap {
  const ranks = computeRanks(view.nodes, view.edges);
  const byRank = new Map<number, string[]>();
  for (const n of view.nodes) {
    const r = ranks.get(n.id) ?? 0;
    const list = byRank.get(r) ?? [];
    list.push(n.id);
    byRank.set(r, list);
  }
  const positions = new Map<string, Point>();
  for (const [rank, ids] of byRank) {
    // Stable ordering within a rank → deterministic y placement.
    const ordered = ids.sort((a, b) => a.localeCompare(b));
    ordered.forEach((id, order) => {
      positions.set(id, {
        x: MARGIN_X + rank * RANK_COLUMN_WIDTH,
        y: MARGIN_Y + order * ROW_HEIGHT,
      });
    });
  }
  return positions;
}

/** Nodes reachable from `roots` in either direction within the view graph. */
export function affectedRegion(
  roots: readonly string[],
  view: View,
): Set<string> {
  const present = new Set(view.nodes.map((n) => n.id));
  const out = new Map<string, string[]>();
  const ins = new Map<string, string[]>();
  const push = (m: Map<string, string[]>, key: string, value: string) => {
    const list = m.get(key) ?? [];
    list.push(value);
    m.set(key, list);
  };
  for (const e of view.edges) {
    if (!present.has(e.from) || !present.has(e.to)) continue;
    push(out, e.from, e.to);
    push(ins, e.to, e.from);
  }
  const region = new Set<string>();
  const stack = roots.filter((r) => present.has(r));
  while (stack.length) {
    const id = stack.pop()!;
    if (region.has(id)) continue;
    region.add(id);
    for (const next of [...(out.get(id) ?? []), ...(ins.get(id) ?? [])]) {
      if (!region.has(next)) stack.push(next);
    }
  }
  return region;
}

/**
 * Scoped recompute: place only the affected regions + new nodes; every other
 * retained node keeps its previous position verbatim (no meaningless jitter).
 */
export function recomputeScoped(
  prev: PositionMap,
  view: View,
  affectedRoots: readonly string[],
  newIds: readonly string[],
): PositionMap {
  const base = deterministicPositions(view);
  const region = affectedRegion(affectedRoots, view);
  const isNew = new Set(newIds);
  const next = new Map<string, Point>();
  const present = new Set(view.nodes.map((n) => n.id));

  for (const n of view.nodes) {
    const affected = region.has(n.id) || isNew.has(n.id);
    const prior = prev.get(n.id);
    if (!affected && prior && present.has(n.id)) {
      next.set(n.id, prior);
      continue;
    }
    next.set(n.id, base.get(n.id) ?? {
      x: MARGIN_X,
      y: MARGIN_Y,
    });
  }
  return next;
}
