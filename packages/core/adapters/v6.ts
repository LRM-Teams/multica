/**
 * V6 adapter — reduces a V6 graph projection (contract fixture, see
 * v6-types.ts) into the unified canvas projection model.
 *
 * NO FIELD GUESSING: every field in the output is copied verbatim from a
 * documented §7.1 projection node/edge field. Node identity is the stable
 * `(run_id, entity_kind, entity_id)` triple (§7.1). Unknown future `node_kind`
 * values degrade to `kind:"generic"` (plan §7.1) without dropping the node or
 * crashing the renderer.
 */
import type { CanvasDelta, CanvasEdge, CanvasNode, CanvasSnapshot } from "./canvas-types";
import { computeGraphContentHash } from "./snapshot-hash";
import type {
  V6ProjectionDelta,
  V6ProjectionEdge,
  V6ProjectionNode,
  V6ProjectionSnapshot,
} from "./v6-types";

/** Stable projection node id for a canonical entity (§7.1). */
export function v6NodeId(
  runId: string,
  entityKind: string,
  entityId: string,
): string {
  return `v6:${runId}:${entityKind}:${entityId}`;
}

const GENERIC_KIND = "generic";

/**
 * The registered §7.1 node_kind set. Unknown future kinds MUST degrade to a
 * generic node (plan §7.1) so an older client never crashes on a new kind.
 */
const KNOWN_V6_KINDS = new Set<string>([
  "task",
  "attempt",
  "result_artifact",
  "search_plan",
  "query_execution",
  "source_candidate",
  "screening_decision",
  "source_snapshot",
  "observation",
  "claim",
  "question",
  "hypothesis",
  "branch",
  "insight",
  "insight_derivation",
  "integration_round",
  "integration_contribution",
  "dispute",
  "dispute_position",
  "deliberation",
  "deliberation_turn",
  "decision",
  "team_formation",
  "team_membership",
  "divergence_pass",
  "capability_observation",
  "report_revision",
  "evaluation_defect",
  "monitoring_cycle",
  "episode",
]);

export function degradeKind(entityKind: string): string {
  if (entityKind && KNOWN_V6_KINDS.has(entityKind)) return entityKind;
  return GENERIC_KIND;
}

function mapNode(n: V6ProjectionNode): CanvasNode {
  const kind = degradeKind(n.entity_kind);
  return {
    id: v6NodeId(n.run_id, n.entity_kind, n.entity_id),
    kind,
    subtype: n.node_subtype,
    schemaVersion: n.schema_version,
    title: n.title,
    summary: n.summary,
    status: n.status,
    // Verbatim canonical importance/freshness — never recomputed or guessed.
    importance: n.importance,
    freshness: n.freshness,
    detailRef: n.detail_ref,
    actor: n.actor_agent_id ?? null,
    planVersion: n.plan_version ?? null,
    createdAtSequence: n.created_event_sequence,
    updatedAtSequence: n.updated_event_sequence,
    payload: n.payload,
    createdAt: n.created_at,
    updatedAt: n.updated_at,
  };
}

function endpointsIds(
  e: V6ProjectionEdge,
): { from: string; to: string } | null {
  const from = (e.from && v6NodeId(e.from.run_id, e.from.entity_kind, e.from.entity_id)) || "";
  const to = (e.to && v6NodeId(e.to.run_id, e.to.entity_kind, e.to.entity_id)) || "";
  if (!from || !to) return null;
  return { from, to };
}

function mapEdge(e: V6ProjectionEdge): CanvasEdge | null {
  const pts = endpointsIds(e);
  if (!pts) return null;
  return {
    id: e.id,
    from: pts.from,
    to: pts.to,
    relation: e.relation,
    createdAt: e.created_at,
  };
}

/** Build a unified canvas snapshot from a V6 projection snapshot. */
export function adaptV6Snapshot(input: V6ProjectionSnapshot): CanvasSnapshot {
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

/** Map a V6 delta fixture to the unified CanvasDelta for applyCanvasDelta. */
export function adaptV6Delta(input: V6ProjectionDelta): CanvasDelta {
  const upsertNodes = input.upsert_nodes.map(mapNode);
  const upsertEdges: CanvasEdge[] = [];
  for (const e of input.upsert_edges) {
    const mapped = mapEdge(e);
    if (mapped) upsertEdges.push(mapped);
  }
  const tombNodeIds = input.visibility_tombstones?.node_ids ?? [];
  return {
    fromSequenceExclusive: input.from_sequence_exclusive,
    throughSequence: input.through_sequence,
    upsertNodes,
    upsertEdges,
    tombstoneNodeIds: tombNodeIds,
    tombstoneEdgeIds: input.visibility_tombstones?.edge_ids ?? [],
    affectedRootIds: input.affected_roots ?? [],
    transitionKind: input.transition_kind as CanvasDelta["transitionKind"],
  };
}
