/**
 * V6 adapter — reduces the canonical V6 graph projection read model into the
 * unified canvas projection model.
 *
 * The wire contract is the MERGED canonical `packages/core/types/research-v6.ts`
 * and the node-kind registry is the MERGED `packages/core/research-v6` registry
 * (single source of truth). This module deliberately adds no duplicated V6
 * contract.
 *
 * NO FIELD GUESSING: every field in the output is copied verbatim from a
 * documented `ResearchV6ProjectionNode` / `ResearchV6ProjectionEdge` field.
 * Node identity is the stable canonical `id` (`${runId}:${entityKind}:${entityId}`,
 * §7.1). Unknown future node_kind values degrade to `kind:"generic"` (plan §7.1)
 * without dropping the node or crashing the renderer.
 */
import type {
  ResearchV6Delta,
  ResearchV6ProjectionEdge,
  ResearchV6ProjectionNode,
  ResearchV6Snapshot,
} from "../types/research-v6";
import { RESEARCH_V6_NODE_KINDS } from "../research-v6/registry";
import type { CanvasDelta, CanvasEdge, CanvasNode, CanvasSnapshot } from "./canvas-types";
import { computeGraphContentHash } from "./snapshot-hash";

/** Stable projection node id for a canonical entity (§7.1). */
export function v6NodeId(
  runId: string,
  entityKind: string,
  entityId: string,
): string {
  return `${runId}:${entityKind}:${entityId}`;
}

const GENERIC_KIND = "generic";

/**
 * The registered §7.1 node_kind set, taken directly from the merged canonical
 * registry. Unknown future kinds MUST degrade to a generic node (plan §7.1) so
 * an older client never crashes on a new kind.
 */
const KNOWN_V6_KINDS = new Set<string>(RESEARCH_V6_NODE_KINDS as readonly string[]);

export function degradeKind(entityKind: string): string {
  if (entityKind && KNOWN_V6_KINDS.has(entityKind)) return entityKind;
  return GENERIC_KIND;
}

function mapNode(n: ResearchV6ProjectionNode): CanvasNode {
  const kind = degradeKind(n.entity_kind);
  return {
    id: n.id,
    kind,
    subtype: n.node_subtype || undefined,
    schemaVersion: n.schema_version != null ? String(n.schema_version) : undefined,
    title: n.title,
    summary: n.summary,
    status: n.status,
    // Verbatim canonical importance/freshness — never recomputed or guessed.
    importance: n.importance,
    freshness: n.freshness,
    detailRef: `${n.entity_kind}:${n.entity_id}`,
    actor: n.actor_agent_id ?? null,
    planVersion: n.plan_version ?? null,
    createdAtSequence: n.created_sequence ?? undefined,
    updatedAtSequence: n.updated_sequence ?? undefined,
    payload: (n.detail ?? {}) as Record<string, unknown>,
    createdAt: n.created_at,
    updatedAt: n.updated_at,
  };
}

function mapEdge(e: ResearchV6ProjectionEdge): CanvasEdge | null {
  if (!e.from_node_id || !e.to_node_id) return null;
  return {
    id: e.id,
    from: e.from_node_id,
    to: e.to_node_id,
    relation: e.edge_type,
    // The canonical edge carries a sequence number, not an ISO timestamp —
    // copy it verbatim (never fabricated into a fake timestamp).
    createdAt: e.created_sequence,
  };
}

/** Build a unified canvas snapshot from a canonical V6 projection snapshot. */
export function adaptV6Snapshot(input: ResearchV6Snapshot): CanvasSnapshot {
  const nodes = input.nodes.map(mapNode);
  const edges: CanvasEdge[] = [];
  for (const e of input.edges) {
    const mapped = mapEdge(e);
    if (mapped) edges.push(mapped);
  }
  return {
    snapshotId: input.snapshot_id,
    throughEventSequence: input.through_event_sequence,
    graphContentHash: computeGraphContentHash(nodes, edges),
    nodes,
    edges,
  };
}

/** Map a canonical V6 delta to the unified CanvasDelta for applyCanvasDelta. */
export function adaptV6Delta(input: ResearchV6Delta): CanvasDelta {
  const upsertNodes = input.node_upserts.map(mapNode);
  const upsertEdges: CanvasEdge[] = [];
  for (const e of input.edge_upserts) {
    const mapped = mapEdge(e);
    if (mapped) upsertEdges.push(mapped);
  }
  return {
    fromSequenceExclusive: input.from_sequence_exclusive,
    throughSequence: input.through_sequence,
    upsertNodes,
    upsertEdges,
    tombstoneNodeIds: input.node_tombstones,
    tombstoneEdgeIds: input.edge_tombstones,
    affectedRootIds: input.affected_root_node_ids,
    transitionKind: input.transition_kind as CanvasDelta["transitionKind"],
  };
}
