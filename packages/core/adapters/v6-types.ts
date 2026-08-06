/**
 * V6 projection wire contract — defined strictly from §7.1 / §7.2 of the
 * autonomous-research-system plan. The V6 backend is not yet landed, so this
 * module is a CONTRACT FIXTURE: it is not produced by production code and is
 * consumed only by adapters/tests. It must stay byte-for-byte aligned with the
 * plan (node_kind set, scope of node fields, edge relation families,
 * snapshot/delta framing). Production paths never manufacture instances of
 * these shapes as fake data.
 */

/** §7.1 — kinds a V6 projection must register (unknown future kinds degrade). */
export type V6NodeKind =
  | "task"
  | "attempt"
  | "result_artifact"
  | "search_plan"
  | "query_execution"
  | "source_candidate"
  | "screening_decision"
  | "source_snapshot"
  | "observation"
  | "claim"
  | "question"
  | "hypothesis"
  | "branch"
  | "insight"
  | "insight_derivation"
  | "integration_round"
  | "integration_contribution"
  | "dispute"
  | "dispute_position"
  | "deliberation"
  | "deliberation_turn"
  | "decision"
  | "team_formation"
  | "team_membership"
  | "divergence_pass"
  | "capability_observation"
  | "report_revision"
  | "evaluation_defect"
  | "monitoring_cycle"
  | "episode";

/** §7.1 — stable typed edge families. */
export type V6Relation =
  | "decomposes"
  | "tests"
  | "depends_on"
  | "triggered"
  | "produced"
  | "consumed"
  | "derived_from"
  | "integrates"
  | "supports"
  | "contradicts"
  | "refines"
  | "supersedes"
  | "invalidates"
  | "discussed_by"
  | "challenged_by"
  | "escalated_to"
  | "resolved_by"
  | "reported_in"
  | "reviewed_by"
  | "revised_by"
  | "staffed_by"
  | "created_for"
  | "retired_after";

export interface V6ProjectionNode {
  run_id: string;
  entity_kind: V6NodeKind | string;
  entity_id: string;
  node_subtype?: string;
  schema_version?: string;
  title: string;
  summary: string;
  status: string;
  /** 0..1 importance rank. */
  importance: number;
  /** 0..1 freshness. */
  freshness: number;
  /** Contract/plan/strategy version. */
  plan_version?: string | null;
  actor_agent_id?: string | null;
  detail_ref: string;
  created_event_sequence?: number;
  updated_event_sequence?: number;
  payload: Record<string, unknown>;
  created_at: string;
  updated_at: string;
}

export interface V6ProjectionEdge {
  id: string;
  from: { run_id: string; entity_kind: string; entity_id: string };
  to: { run_id: string; entity_kind: string; entity_id: string };
  relation: V6Relation | string;
  created_at: string;
}

/** §7.2 — full snapshot fixture. */
export interface V6ProjectionSnapshot {
  run_id: string;
  snapshot_id: string;
  through_event_sequence: number;
  graph_content_hash: string;
  nodes: V6ProjectionNode[];
  edges: V6ProjectionEdge[];
}

/** §7.2 — incremental delta fixture. */
export interface V6ProjectionDelta {
  from_sequence_exclusive: number;
  through_sequence: number;
  upsert_nodes: V6ProjectionNode[];
  upsert_edges: V6ProjectionEdge[];
  visibility_tombstones: { node_ids: string[]; edge_ids: string[] };
  affected_roots: string[];
  transition_kind: string;
}
