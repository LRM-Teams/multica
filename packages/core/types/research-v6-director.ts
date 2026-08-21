/**
 * Frontend boundary types for the unreleased Ronaldo/Director V6 Projection.
 *
 * Authority: docs/contracts/research-run-v6-director.schema.json and
 * docs/research-run-v6-http-contract.md §5. These deliberately do not extend
 * the legacy experimental V6 graph types: mixing those contracts would make a
 * successful response look valid while changing its meaning. Enum-like values
 * remain forward compatible so a newer server can render through the generic
 * visual path instead of taking down the complete canvas.
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
  | "evidence_link"
  | (string & {});

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
  | "insight"
  | (string & {});

export type ResearchV6DirectorProjectionTier =
  | "GOAL"
  | "S"
  | "M"
  | "L"
  | "XL"
  | "XXL"
  | (string & {});

export type ResearchV6DirectorExecutionState =
  | "pending"
  | "running"
  | "succeeded"
  | "failed"
  | "cancelled"
  | "lost"
  | (string & {});

export type ResearchV6DirectorConclusionState =
  | "proposed"
  | "accepted"
  | "challenged"
  | "refuted"
  | "invalid"
  | (string & {});

export type ResearchV6DirectorIntegrationState =
  | "unmatched"
  | "candidate"
  | "discussing"
  | "absorbed"
  | "excluded"
  | (string & {});

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
  | "other"
  | (string & {});

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
  | "collapsed_path"
  | (string & {});

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

export type ResearchV6DirectorNodeDetailView =
  | "brief"
  | "full"
  | "history"
  | (string & {});

export interface ResearchV6DirectorNodeDetail {
  snapshot_id: string;
  through_event_sequence: number;
  projection_hash: string;
  view: ResearchV6DirectorNodeDetailView;
  node: ResearchV6DirectorProjectionNode;
  incoming: ResearchV6DirectorProjectionEdge[];
  outgoing: ResearchV6DirectorProjectionEdge[];
  history_refs: ResearchV6DirectorEntityRef[];
  agent_refs: ResearchV6DirectorEntityRef[];
  work_item_refs: ResearchV6DirectorEntityRef[];
  attempt_refs: ResearchV6DirectorEntityRef[];
  evidence_refs: ResearchV6DirectorEntityRef[];
  discussion_refs: ResearchV6DirectorEntityRef[];
  report_refs: ResearchV6DirectorEntityRef[];
}

export interface ResearchV6DirectorReportReview {
  id?: string;
  decision: string;
  reason: string;
  input_state_version?: number;
  render_artifact_version_id?: string;
  render_diagnostics?: unknown;
  follow_up_work_item_refs?: unknown;
  created_at?: string;
}

export interface ResearchV6DirectorReportMetadata {
  id: string;
  revision: number;
  status: string;
  title: string;
  summary: string;
  package_hash: string;
  document_content_hash: string;
  published_at: string | null;
  created_at: string;
  author_agent_id: string;
  input_count: number;
  latest_review: ResearchV6DirectorReportReview;
  sandbox_url?: string;
  report_origin?: string;
}

export interface ResearchV6DirectorReportInputRef {
  branch_id: string;
  node_artifact_version_id: string;
  input_role: string;
  ordinal: number;
  content_hash: string;
}

export interface ResearchV6DirectorReportDetail {
  id: string;
  revision: number;
  status: string;
  title: string;
  summary: string;
  plain_text: string;
  package_hash: string;
  document_content_hash: string;
  outline: unknown;
  citations: unknown;
  input_refs: ResearchV6DirectorReportInputRef[];
  reviews: ResearchV6DirectorReportReview[];
  sandbox_url?: string;
  report_origin?: string;
}

export interface ResearchV6DirectorSelectedRef {
  stable_id: string;
  kind: ResearchV6DirectorEntityKind;
  entity_id: string;
  revision: number;
  content_hash: string;
  display_summary: string;
}

export interface ResearchV6DirectorAssignment {
  id: string;
  workspaceId: string;
  runId: string;
  directorAgentId: string;
  status: string;
  reason: string;
  generation: number;
  stateVersion: number;
}

export interface ResearchV6DirectorAssignmentRequest {
  directorAgentId: string;
  expectedStateVersion: number;
  reason: string;
  clientRequestId: string;
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

export interface ResearchV6DirectorDetailTransport {
  loadNodeDetail(
    workspaceId: string,
    runId: string,
    snapshotId: string,
    nodeId: string,
    view: ResearchV6DirectorNodeDetailView,
    signal?: AbortSignal,
  ): Promise<ResearchV6DirectorNodeDetail>;
  listReports(
    workspaceId: string,
    runId: string,
    signal?: AbortSignal,
  ): Promise<ResearchV6DirectorReportMetadata[]>;
  loadReport(
    workspaceId: string,
    runId: string,
    reportId: string,
    signal?: AbortSignal,
  ): Promise<ResearchV6DirectorReportDetail>;
  loadCompiledReport(
    workspaceId: string,
    runId: string,
    reportId: string,
    signal?: AbortSignal,
  ): Promise<string>;
}
