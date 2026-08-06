/**
 * V5 adapter — reduces the historical `research-run-v5` graph
 * (`ResearchGraphNode[]` / `ResearchGraphEdge[]`) into the unified canvas
 * projection model.
 *
 * NO FIELD GUESSING: every emitted field maps to a documented V5 field or to a
 * documented neutral/derived value. The adapter never reads node.summary,
 * node.title, node.payload prose or any edge label to extract a research fact.
 * `importance`/`freshness` — absent from the V5 projection — use a documented
 * neutral constant / timestamp-only derivation respectively.
 */
import type {
  ResearchGraphEdge,
  ResearchGraphNode,
} from "../types/research";
import type { CanvasEdge, CanvasNode, CanvasSnapshot } from "./canvas-types";
import { computeGraphContentHash } from "./snapshot-hash";

/** V5 projection carries no importance signal — every node gets this neutral rank. */
export const V5_NEUTRAL_IMPORTANCE = 0.5;

/** V5 snapshot rows are keyed per research session. */
export function v5SnapshotId(sessionId: string): string {
  return `v5:${sessionId}:0`;
}

function nodeActor(node: ResearchGraphNode): string {
  return node.actor_agent_id ?? "";
}

/**
 * Freshness: normalized recency of `updated_at` against the newest update in
 * the batch. Timestamp-derived only (documented numeric fields), never prose.
 */
function freshnessForNodes(nodes: ResearchGraphNode[]): Map<string, number> {
  const out = new Map<string, number>();
  if (nodes.length === 0) return out;
  const newest = Math.max(
    ...nodes.map((n) => Date.parse(n.updated_at || n.created_at)),
  );
  for (const n of nodes) {
    const updated = Date.parse(n.updated_at || n.created_at);
    if (!Number.isFinite(newest) || !Number.isFinite(updated)) {
      out.set(n.id, 0);
      continue;
    }
    const age = newest - updated;
    // Monotonic recency in the batch: newest node is 1, oldest tends to 0.
    out.set(n.id, Math.max(0, Math.min(1, 1 - age / 86_400_000)));
  }
  return out;
}

function mapNode(n: ResearchGraphNode, freshness: number): CanvasNode {
  return {
    id: n.id,
    kind: n.node_type,
    title: n.title,
    summary: n.summary,
    status: n.status,
    importance: V5_NEUTRAL_IMPORTANCE,
    freshness,
    detailRef: `v5-node:${n.id}`,
    actor: nodeActor(n) || null,
    payload: (n.payload ?? {}) as Record<string, unknown>,
    createdAt: n.created_at,
    updatedAt: n.updated_at,
  };
}

function mapEdge(e: ResearchGraphEdge): CanvasEdge {
  return {
    id: e.id,
    from: e.from_node_id,
    to: e.to_node_id,
    relation: e.edge_type,
    createdAt: e.created_at,
  };
}

/**
 * Build a unified canvas snapshot from a V5 session graph.
 * The same input always yields the same snapshot and the same content hash.
 */
export function adaptV5Graph(
  sessionId: string,
  nodes: ResearchGraphNode[],
  edges: ResearchGraphEdge[],
): CanvasSnapshot {
  const fresh = freshnessForNodes(nodes);
  const canvasNodes: CanvasNode[] = nodes.map((n) => mapNode(n, fresh.get(n.id) ?? 0));
  const canvasEdges: CanvasEdge[] = edges.map(mapEdge);
  return {
    snapshotId: v5SnapshotId(sessionId),
    throughEventSequence: 0,
    graphContentHash: computeGraphContentHash(canvasNodes, canvasEdges),
    nodes: canvasNodes,
    edges: canvasEdges,
  };
}
