/**
 * Exact frontend wire types for the unreleased Ronaldo/Director V6 Projection.
 *
 * Authority: docs/contracts/research-run-v6-director.schema.json and
 * docs/research-run-v6-http-contract.md §5. These deliberately do not extend
 * the legacy experimental V6 graph types: mixing those contracts would make a
 * successful response look valid while changing its meaning.
 */

export type ResearchV6DirectorEntityKind =
  | "goal"
  | "branch"
  | "task"
  | "attempt"
  | "work_item"
  | "agent"
  | "result"
  | "insight"
  | "discussion"
  | "dispute"
  | "integration"
  | "report"
  | "source_snapshot"
  | "observation"
  | "claim"
  | "evidence_link";

export interface ResearchV6DirectorEntityRef {
  kind: ResearchV6DirectorEntityKind;
  id: string;
  revision?: number;
  version_id?: string;
  content_hash?: string;
}

export type ResearchV6DirectorProjectionNodeKind =
  | "goal"
  | "work_s"
  | "result_s"
  | "insight";

export type ResearchV6DirectorProjectionTier =
  | "GOAL"
  | "S"
  | "M"
  | "L"
  | "XL"
  | "XXL";

export type ResearchV6DirectorExecutionState =
  | "pending"
  | "running"
  | "succeeded"
  | "failed"
  | "cancelled"
  | "lost";

export type ResearchV6DirectorConclusionState =
  | "proposed"
  | "accepted"
  | "challenged"
  | "refuted"
  | "invalid";

export type ResearchV6DirectorIntegrationState =
  | "unmatched"
  | "candidate"
  | "discussing"
  | "absorbed"
  | "excluded";

export type ResearchV6DirectorTerminationReason =
  | "invalid_direction"
  | "dead_end"
  | "no_semantic_gain"
  | "duplicate"
  | "out_of_scope"
  | "stopped_by_user"
  | "stopped_by_director"
  | "resource_failure"
  | "superseded"
  | "other";

export interface ResearchV6DirectorTermination {
  reason_code: ResearchV6DirectorTerminationReason;
  reason_detail: string;
}

export interface ResearchV6DirectorProjectionState {
  execution: ResearchV6DirectorExecutionState;
  conclusion: ResearchV6DirectorConclusionState;
  integration: ResearchV6DirectorIntegrationState;
  termination?: ResearchV6DirectorTermination;
}

export interface ResearchV6DirectorProjectionNode {
  id: string;
  kind: ResearchV6DirectorProjectionNodeKind;
  tier: ResearchV6DirectorProjectionTier;
  canonical_ref: ResearchV6DirectorEntityRef;
  branch_ids: string[];
  state: ResearchV6DirectorProjectionState;
  title?: string;
  catalog_summary: string;
  absorbed: boolean;
  terminal: boolean;
  expandable: boolean;
  hidden_child_count: number;
  updated_at: string;
}

export type ResearchV6DirectorProjectionEdgeKind =
  | "derived_from"
  | "absorbed_into"
  | "produced_by"
  | "belongs_to"
  | "challenges"
  | "collapsed_path";

export interface ResearchV6DirectorProjectionEdge {
  id: string;
  kind: ResearchV6DirectorProjectionEdgeKind;
  from_node_id: string;
  to_node_id: string;
  canonical: boolean;
  hidden_count: number;
  expandable: boolean;
}

export interface ResearchV6DirectorDensityBin {
  id: string;
  branch_id: string;
  bounds: { x: number; y: number; width: number; height: number };
  total: number;
  reason_counts: Record<string, number>;
  execution_counts: Record<string, number>;
}

export interface ResearchV6DirectorProjectionSnapshot {
  contract_kind: "projection_snapshot";
  schema_version: 6;
  snapshot_id: string;
  workspace_id: string;
  run_id: string;
  through_event_sequence: number;
  projection_hash: string;
  slice_key: string;
  nodes: ResearchV6DirectorProjectionNode[];
  edges: ResearchV6DirectorProjectionEdge[];
  density_bins: ResearchV6DirectorDensityBin[];
  has_more: boolean;
  next_cursor?: string;
}

export interface ResearchV6DirectorProjectionDelta {
  contract_kind: "projection_delta";
  schema_version: 6;
  workspace_id: string;
  run_id: string;
  snapshot_id: string;
  event_sequence: number;
  previous_projection_hash: string;
  projection_hash: string;
  upsert_nodes: ResearchV6DirectorProjectionNode[];
  remove_node_ids: string[];
  upsert_edges: ResearchV6DirectorProjectionEdge[];
  remove_edge_ids: string[];
  invalidate_slice_keys: string[];
}

export interface ResearchV6DirectorProjectionDeltaPage {
  run_id: string;
  deltas: ResearchV6DirectorProjectionDelta[];
  next_cursor: string | null;
  resync_required: boolean;
}

export interface ResearchV6DirectorProjectionResumeRequest {
  snapshot_id: string;
  last_confirmed_sequence: number;
  projection_hash: string;
}

/** V6 derivation expansion is contractually one layer; callers cannot vary it. */
export interface ResearchV6DirectorProjectionSliceRequest {
  root: string;
  depth: 1;
  snapshot_id: string;
  cursor?: string;
}

export interface ResearchV6DirectorProjectionTransport {
  loadSnapshot(
    workspaceId: string,
    runId: string,
    cursor?: string,
    signal?: AbortSignal,
  ): Promise<ResearchV6DirectorProjectionSnapshot>;
  loadSlice(
    workspaceId: string,
    runId: string,
    request: ResearchV6DirectorProjectionSliceRequest,
    signal?: AbortSignal,
  ): Promise<ResearchV6DirectorProjectionSnapshot>;
  loadDeltaPage(
    workspaceId: string,
    runId: string,
    after: number,
    cursor?: string,
    signal?: AbortSignal,
  ): Promise<ResearchV6DirectorProjectionDeltaPage>;
  resume(
    workspaceId: string,
    runId: string,
    request: ResearchV6DirectorProjectionResumeRequest,
    signal?: AbortSignal,
  ): Promise<ResearchV6DirectorProjectionDeltaPage>;
}
