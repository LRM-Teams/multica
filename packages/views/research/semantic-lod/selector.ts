/**
 * Semantic LOD — selector (LRM-1488).
 *
 * `selectSemanticNodes` is the pure graph→decision seam. It consumes the
 * unified canvas projection (nodes + edges matching `CanvasNode`/`CanvasEdge`
 * field shapes), computes hop depth by BFS from a root, classifies each node
 * into a discrete semantic render form and enforces the viewport budget. It
 * reads no React, no DOM and no geometry — worker-ready and unit-testable.
 */
import { type SemanticContext, DEFAULT_VISIBLE_DEPTH, MAX_VISIBLE_DEPTH } from "./model";
import { classifySemanticLOD } from "./classify";
import {
  enforceVisibleBudget,
  type BudgetResult,
  type LODEntry,
} from "./budget";
import type { ViewportTier } from "./model";

/** Minimal node shape the selector needs (superset-safe with CanvasNode). */
export interface SemanticNodeInput {
  id: string;
  kind: string;
  status: string;
  importance: number;
}

/** Minimal edge shape (superset-safe with CanvasEdge). */
export interface SemanticEdgeInput {
  from: string;
  to: string;
  relation: string;
}

/**
 * Selector context = SemanticContext + the graph root used for BFS depth.
 * `rootId` defaults to `selectedId` when provided, else node with the lowest
 * id (stable tie-break) — callers normally pass an explicit canonical root.
 */
export interface SemanticSelectContext extends SemanticContext {
  rootId: string | null;
}

export interface SemanticSelectResult extends BudgetResult {
  /** BFS depth computed for each node. */
  depthById: ReadonlyMap<string, number>;
}

/**
 * Compute hop depth outward from `rootId` using BFS over the adjacency of
 * `edges`. Edges are traversed in both directions so ancestor and descendant
 * hops share the same depth scale; missing root yields depth 0 for all.
 */
export function computeDepthById(
  nodes: readonly SemanticNodeInput[],
  edges: readonly SemanticEdgeInput[],
  rootId: string | null,
): ReadonlyMap<string, number> {
  const depth = new Map<string, number>();
  if (!rootId) {
    for (const n of nodes) depth.set(n.id, 0);
    return depth;
  }
  const adj = new Map<string, string[]>();
  for (const n of nodes) adj.set(n.id, []);
  for (const e of edges) {
    const a = adj.get(e.from);
    const b = adj.get(e.to);
    if (a) a.push(e.to);
    if (b) b.push(e.from);
  }
  depth.set(rootId, 0);
  const queue = [rootId];
  let head = 0;
  while (head < queue.length) {
    const cur = queue[head++];
    if (cur === undefined) break; // noUncheckedIndexedAccess guard
    const d = depth.get(cur) ?? 0;
    for (const next of adj.get(cur) ?? []) {
      if (depth.has(next)) continue;
      depth.set(next, d + 1);
      queue.push(next);
    }
  }
  // Any node unreachable from root (disconnected) gets depth 0.
  for (const n of nodes) {
    if (!depth.has(n.id)) depth.set(n.id, 0);
  }
  return depth;
}

/**
 * Root preference for BFS: the passed root, else selected, else the first
 * node id in stable (sorted) order — deterministic, never random.
 */
export function resolveRoot(
  nodes: readonly SemanticNodeInput[],
  rootId: string | null,
  selectedId: string | null,
): string | null {
  if (rootId) return rootId;
  if (selectedId) return selectedId;
  if (nodes.length === 0) return null;
  const sorted = [...nodes].map((n) => n.id).sort();
  return (sorted[0] as string | undefined) ?? null;
}

/**
 * Full semantic selection pipeline: depth → classify → enforce budget.
 */
export function selectSemanticNodes(
  args: {
    nodes: readonly SemanticNodeInput[];
    edges: readonly SemanticEdgeInput[];
    context: SemanticSelectContext;
    tier: ViewportTier;
    budget?: import("./model").VisibleBudget;
  },
): SemanticSelectResult {
  const { nodes, edges, context, tier } = args;
  const rootId = resolveRoot(nodes, context.rootId, context.selectedId);

  const depthById = computeDepthById(nodes, edges, rootId);

  const ctx: SemanticContext = {
    ...context,
    depthById,
  };

  const entries: LODEntry[] = nodes.map((n) => ({
    id: n.id,
    kind: n.kind,
    status: n.status,
    importance: n.importance,
    classification: classifySemanticLOD({
      id: n.id,
      kind: n.kind,
      status: n.status,
      importance: n.importance,
      context: ctx,
    }),
  }));

  const enforced = enforceVisibleBudget({
    entries,
    context: ctx,
    tier,
    budget: args.budget,
  });

  return {
    ...enforced,
    depthById,
  };
}

export { DEFAULT_VISIBLE_DEPTH, MAX_VISIBLE_DEPTH };
