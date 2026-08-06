/**
 * Idempotent, sequence-framed application of a projection delta (§7.2).
 *
 * Contract guarantees:
 *   - Upserts are keyed by stable id, so replaying a delta (or overlapping
 *     deltas) never duplicates a node or edge.
 *   - A visibility tombstone removes the view node AND every dangling edge
 *     touching it, and returns the affected roots (the roots of the subgraph
 *     the removal actually changed) so the ViewModel can recompute layout only
 *     for those regions instead of relaying the whole canvas.
 *   - Out-of-order deltas are buffered until their gap is closed; a gap that
 *     can never close (frame already applied or from the future) is surfaced as
 *     `needsResync=true` so the caller can refetch the snapshot instead of
 *     guessing.
 */
import type {
  CanvasDelta,
  CanvasEdge,
  CanvasNode,
  CanvasSnapshot,
} from "./canvas-types";
import { snapshotContentHash } from "./snapshot-hash";

export interface AppliedDeltaResult {
  snapshot: CanvasSnapshot;
  /** Nodes/edges actually added by this application (empty for a no-op). */
  appliedNodeIds: string[];
  appliedEdgeIds: string[];
  /** Roots of the subgraph touched by tombstones (empty when nothing removed). */
  affectedRootIds: string[];
  /** True when the delta's sequence frame cannot be applied locally. */
  needsResync: boolean;
  /** True when this delta was a duplicate of an already-applied frame. */
  wasDuplicate: boolean;
}

function upsertNode(nodes: CanvasNode[], incoming: CanvasNode): boolean {
  const idx = nodes.findIndex((n) => n.id === incoming.id);
  if (idx >= 0) {
    // Unchanged replay → no-op; content-identical node is never duplicated.
    if (nodes[idx]!.updatedAt === incoming.updatedAt) return false;
    nodes[idx] = incoming;
    return true;
  }
  nodes.push(incoming);
  return true;
}

function upsertEdge(edges: CanvasEdge[], incoming: CanvasEdge): boolean {
  const idx = edges.findIndex((e) => e.id === incoming.id);
  if (idx >= 0) return false;
  edges.push(incoming);
  return true;
}

/**
 * Follow `through` edges backwards from each removed root to the nearest
 * retained parent whose own layout depends on the removed subtree.
 */
/**
 * Affected roots for local recompute (AC2): the retained nodes that are
 * neighbors (either direction, in the PRE-removal edge set) of any tombstoned
 * node. The renderer recomputes only from these roots; anything not reachable
 * from them keeps its position verbatim.
 */
function computeAffectedRoots(
  allEdges: readonly CanvasEdge[],
  removedNodes: Set<string>,
  present: Set<string>,
): string[] {
  const roots = new Set<string>();
  for (const e of allEdges) {
    if (removedNodes.has(e.from) && present.has(e.to) && !removedNodes.has(e.to)) {
      roots.add(e.to);
    }
    if (removedNodes.has(e.to) && present.has(e.from) && !removedNodes.has(e.from)) {
      roots.add(e.from);
    }
  }
  return [...roots];
}

/**
 * Apply a delta to a snapshot. `appliedThrough` is the highest event sequence
 * already applied by this client. Returns either an applied snapshot or the
 * `needsResync` signal with an untouched snapshot.
 */
export function applyCanvasDelta(
  snapshot: CanvasSnapshot,
  delta: CanvasDelta,
): AppliedDeltaResult {
  const watermark = snapshot.throughEventSequence;

  // A frame whose whole range already lies below the watermark is a duplicate
  // replay — apply nothing, but do not force a resync.
  if (delta.throughSequence <= watermark) {
    return {
      snapshot,
      appliedNodeIds: [],
      appliedEdgeIds: [],
      affectedRootIds: [],
      needsResync: false,
      wasDuplicate: true,
    };
  }

  // A non-duplicate frame that does not start exactly at the watermark is a
  // gap: either from the future (from > watermark) or overlapping a partially
  // applied past range (from < watermark < through). Never guess — resync.
  if (delta.fromSequenceExclusive !== watermark) {
    return {
      snapshot,
      appliedNodeIds: [],
      appliedEdgeIds: [],
      affectedRootIds: [],
      needsResync: true,
      wasDuplicate: false,
    };
  }

  const nodes = snapshot.nodes.slice();
  const edges = snapshot.edges.slice();
  const appliedNodeIds: string[] = [];
  const appliedEdgeIds: string[] = [];

  for (const n of delta.upsertNodes) {
    if (upsertNode(nodes, n)) appliedNodeIds.push(n.id);
  }
  for (const e of delta.upsertEdges) {
    if (upsertEdge(edges, e)) appliedEdgeIds.push(e.id);
  }

  // Visibility tombstones: drop the view node and every dangling edge.
  const removedNodeIds = new Set<string>();
  for (const id of delta.tombstoneNodeIds) {
    const idx = nodes.findIndex((n) => n.id === id);
    if (idx >= 0) {
      nodes.splice(idx, 1);
      removedNodeIds.add(id);
    }
  }

  const removedNodeSet = removedNodeIds; // capture for closure below
  let nextEdges = edges;
  if (removedNodeIds.size > 0) {
    nextEdges = edges.filter(
      (e) =>
        !removedNodeIds.has(e.from) &&
        !removedNodeIds.has(e.to) &&
        delta.tombstoneEdgeIds.indexOf(e.id) < 0,
    );
  } else if (delta.tombstoneEdgeIds.length > 0) {
    const gone = new Set(delta.tombstoneEdgeIds);
    nextEdges = edges.filter((e) => !gone.has(e.id));
  }

  const affectedRootIds =
    removedNodeSet.size > 0
      ? computeAffectedRoots(
          edges,
          removedNodeSet,
          new Set(nodes.map((n) => n.id)),
        )
      : [];

  const next: CanvasSnapshot = {
    ...snapshot,
    throughEventSequence: delta.throughSequence,
    graphContentHash: snapshotContentHash({ ...snapshot, nodes, edges: nextEdges }),
    nodes,
    edges: nextEdges,
  };

  return {
    snapshot: next,
    appliedNodeIds,
    appliedEdgeIds,
    affectedRootIds,
    needsResync: false,
    wasDuplicate: false,
  };
}
