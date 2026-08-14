/**
 * Research V6 — Graph Projection client contract.
 *
 * Mirrors `docs/superpowers/plans/2026-08-05-autonomous-research-system.md`
 * section 7.1 (Graph Projection Contract) and 7.2 (无限画布投影协议).
 *
 * Frontend only consumes the backend Projection read model. It never infers
 * canonical research state from chat text, animation, or display grouping, and
 * never writes display grouping back as a real Insight.
 */

/** Stable Projection Node ID = `(run_id, entity_kind, entity_id)`. */
export type ResearchV6NodeKind =
  | "goal"
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
  | "episode"
  /** Unknown future kinds must degrade to a generic node, never crash. */
  | (string & {});

export type ResearchV6EdgeType =
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
  | "retired_after"
  | "restart_of"
  | (string & {});

export type ResearchV6TransitionKind =
  | "branch_spawned"
  | "task_dispatched"
  | "result_accepted"
  | "integration_formed"
  | "insight_staled"
  | "dispute_opened"
  | "deliberation_progressed"
  | "lead_escalated"
  | "team_membership_changed"
  | "report_revised"
  | (string & {});

/**
 * Projection Node (7.1). Every canonical entity uses the stable
 * `(run_id, entity_kind, entity_id)` identity.
 */
export interface ResearchV6ProjectionNode {
  /** Stable node id = `${runId}:${entityKind}:${entityId}`. */
  id: string;
  run_id: string;
  entity_kind: ResearchV6NodeKind;
  entity_id: string;
  node_kind: ResearchV6NodeKind;
  node_subtype: string;
  schema_version: number;
  title: string;
  /** Bounded summary — never the source of canonical facts. */
  summary: string;
  status: string;
  /** Server-owned D5 visual hierarchy; clients must not infer it from importance. */
  level?: "xxl" | "xl" | "l" | "m" | "s";
  cluster_id?: string | null;
  parent_id?: string | null;
  round?: number;
  confidence?: number | null;
  document_count?: number | null;
  conclusion_count?: number | null;
  derived_from?: string | null;
  merged_from?: string[];
  superseded_by?: string | null;
  restart_of?: string | null;
  invalidated_by?: string | null;
  importance: number;
  /** Opaque freshness token/sortable value set by the server. */
  freshness: string | null;
  /** contract/plan/strategy version held by the server. */
  contract_version: string | null;
  plan_version: string | null;
  strategy_version: string | null;
  /** Provenance. */
  actor_agent_id: string | null;
  task_id: string | null;
  attempt_id: string | null;
  created_at: string | null;
  updated_at: string | null;
  cost?: number | null;
  /** Detail payload — opaque canonical entity reference. */
  detail: unknown;
  /** Created / updated / terminal event sequences (7.1). */
  created_sequence: number | null;
  updated_sequence: number | null;
  terminal_sequence: number | null;
}

export interface ResearchV6ProjectionCluster {
  id: string;
  label: string;
  cluster_type: "stable_result" | "exploration" | "new_frontier";
  member_node_ids: string[];
  confidence: number | null;
  document_count: number | null;
  conclusion_count: number | null;
}

export interface ResearchV6ProjectionEdge {
  id: string;
  run_id: string;
  from_node_id: string;
  to_node_id: string;
  edge_type: ResearchV6EdgeType;
  created_sequence: number | null;
  /** Tombstone — when set the edge is no longer visible. */
  tombstoned_at_sequence: number | null;
}

/** Server-declared content hash over the canonical node/edge set. */
export interface ResearchV6GraphContentHash {
  /** Deterministic hash over canonical node ids+content. */
  nodes: string;
  /** Deterministic hash over canonical edge ids+content. */
  edges: string;
}

/**
 * Full Snapshot (7.2). All pages of one logical snapshot are pinned to the
 * same `snapshot_id` and `through_event_sequence`.
 */
export interface ResearchV6Snapshot {
  snapshot_id: string;
  run_id: string;
  through_event_sequence: number;
  graph_content_hash: ResearchV6GraphContentHash;
  nodes: ResearchV6ProjectionNode[];
  edges: ResearchV6ProjectionEdge[];
  clusters?: ResearchV6ProjectionCluster[];
  /** Stable pagination cursor for the next page of this same snapshot. */
  next_cursor: string | null;
}

/**
 * Incremental Delta (7.2). Clients apply idempotently by stable id; out-of-order
 * deltas are buffered until the gap fills; a gap timeout or a server that has
 * cleared the needed history must trigger a full snapshot resync.
 */
export interface ResearchV6Delta {
  /** The previous contiguous sequence the client must already have. */
  from_sequence_exclusive: number;
  through_sequence: number;
  node_upserts: ResearchV6ProjectionNode[];
  edge_upserts: ResearchV6ProjectionEdge[];
  /** Visibility tombstones (removed node/edge ids). */
  node_tombstones: string[];
  edge_tombstones: string[];
  cluster_upserts?: ResearchV6ProjectionCluster[];
  cluster_tombstones?: string[];
  /** Affected root node ids (for canvas invalidation). */
  affected_root_node_ids: string[];
  /** Semantic transition derived from canonical events. */
  transition_kind: ResearchV6TransitionKind | null;
}

/** Server WS resume verdict. */
export type ResearchV6ResumeVerdict =
  | { ok: true; delta: ResearchV6Delta }
  | { ok: false; resync_required: true };

export interface ResearchV6ReconnectRequest {
  run_id: string;
  last_confirmed_sequence: number;
}

/** Transport surface the projection client depends on. */
export interface ResearchV6ProjectionTransport {
  loadSnapshot(runId: string): Promise<ResearchV6Snapshot>;
  loadDeltaPage(
    runId: string,
    fromSequenceExclusive: number,
  ): Promise<ResearchV6Delta | null>;
  /** WS resume call — server either continues contiguously or demands resync. */
  resume(runId: string, lastConfirmedSequence: number): Promise<ResearchV6ResumeVerdict>;
}

/**
 * §7.2 Projection Slice — bounded on-demand expansion of a large canvas.
 *
 * A slice is a page of a stable, ordered expansion bounded by depth, relation
 * types, status, and importance. Every page is pinned to the same logical
 * `snapshot_id`; the same slice parameters under the same snapshot return a
 * stable order and content. It is a display read model and never mutates
 * canonical state.
 */
export type ResearchV6SliceDirection = "out" | "in" | "both";

/** Slice query parameters (§7.2). */
export interface ResearchV6ProjectionSliceRequest {
  /** Root node id the slice expands from. */
  root_node_id: string;
  /** Traversal direction. */
  direction: ResearchV6SliceDirection;
  /** Allowed relation types; empty = all. */
  relation_types: ResearchV6EdgeType[];
  /** Max expansion depth; 0 = root only. */
  max_depth: number;
  /** Optional status filter; empty = all statuses. */
  statuses: string[];
  /** Minimum importance floor (0..1); 0 = none. */
  importance_floor: number;
  /** Stable pagination cursor; null = first page. */
  cursor: string | null;
  /** Max nodes per page (bounded by the server). */
  limit: number;
}

/** Slice node with server-declared expandability signals (§7.2). */
export interface ResearchV6SliceNode {
  node: ResearchV6ProjectionNode;
  /** Count of neighbours not yet loaded in the current slice. */
  unloaded_neighbor_count: number;
  /** Total descendant count under this node (server-computed). */
  descendant_count: number;
  /** True when more neighbours/depth remain to expand. */
  can_expand: boolean;
}

/** One stable page of a bounded projection slice (§7.2). */
export interface ResearchV6ProjectionSlice {
  /** The logical snapshot this page is pinned to. */
  snapshot_id: string;
  /** The slice parameters echoed back for cache identity. */
  request: ResearchV6ProjectionSliceRequest;
  nodes: ResearchV6SliceNode[];
  /** Server-declared edges among the returned slice nodes. */
  edges: ResearchV6ProjectionEdge[];
  /** Cursor for the next page of this same snapshot+parameters. */
  next_cursor: string | null;
}

/**
 * Unknown kind degradation record (AC: unknown inputs render generic).
 *
 * The frontend registry never guesses a rendering for an unrecognised kind. It
 * keeps enough metadata — the raw kind string and the canonical node/edge id —
 * so the page can render a generic node and expose a diagnostic instead of
 * crashing.
 */
export interface ResearchV6UnknownKindDiagnostic {
  /** The raw, unrecognised kind/type string as sent by the server. */
  raw: string;
  /** Where the unknown value appeared: node kind / edge type / transition. */
  field: "node_kind" | "edge_type" | "transition_kind";
  /** canonical node or edge id this unknown value was attached to. */
  owner_id: string;
  /** server snapshot / delta in which it appeared. */
  run_id: string;
  /** monotonic diagnostic counter for observability. */
  sequence: number;
}
