/**
 * Internal frontend types for the Ronaldo/Director V6 Projection.
 *
 * Authority: docs/contracts/research-run-v6-director.schema.json and
 * docs/research-run-v6-http-contract.md §5. These deliberately do not extend
 * the legacy experimental V6 graph types: mixing those contracts would make a
 * successful response look valid while changing its meaning. The schema module
 * owns snake_case wire decoding; callers only see these camelCase fields.
 * Enum-like values remain forward compatible so a newer server can render
 * through the generic visual path instead of taking down the complete canvas.
 */

import type { RunnerActivityTimelineRow } from "./events";

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
  versionId?: string;
  contentHash?: string;
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
  reasonCode: ResearchV6DirectorTerminationReason;
  reasonDetail: string;
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
  canonicalRef: ResearchV6DirectorEntityRef;
  branchIds: string[];
  state: ResearchV6DirectorProjectionState;
  title?: string;
  catalogSummary: string;
  absorbed: boolean;
  terminal: boolean;
  expandable: boolean;
  hiddenChildCount: number;
  updatedAt: string;
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
  fromNodeId: string;
  toNodeId: string;
  canonical: boolean;
  hiddenCount: number;
  expandable: boolean;
}

export interface ResearchV6DirectorDensityBin {
  id: string;
  branchId: string;
  bounds: { x: number; y: number; width: number; height: number };
  total: number;
  reasonCounts: Record<string, number>;
  executionCounts: Record<string, number>;
}

export interface ResearchV6DirectorProjectionSnapshot {
  contractKind: "projection_snapshot";
  schemaVersion: 6;
  snapshotId: string;
  workspaceId: string;
  runId: string;
  throughEventSequence: number;
  projectionHash: string;
  sliceKey: string;
  nodes: ResearchV6DirectorProjectionNode[];
  edges: ResearchV6DirectorProjectionEdge[];
  densityBins: ResearchV6DirectorDensityBin[];
  hasMore: boolean;
  nextCursor?: string;
}

export interface ResearchV6DirectorProjectionDelta {
  contractKind: "projection_delta";
  schemaVersion: 6;
  workspaceId: string;
  runId: string;
  snapshotId: string;
  eventSequence: number;
  previousProjectionHash: string;
  projectionHash: string;
  upsertNodes: ResearchV6DirectorProjectionNode[];
  removeNodeIds: string[];
  upsertEdges: ResearchV6DirectorProjectionEdge[];
  removeEdgeIds: string[];
  invalidateSliceKeys: string[];
}

export interface ResearchV6DirectorProjectionDeltaPage {
  runId: string;
  deltas: ResearchV6DirectorProjectionDelta[];
  nextCursor: string | null;
  resyncRequired: boolean;
}

export interface ResearchV6DirectorProjectionResumeRequest {
  snapshotId: string;
  lastConfirmedSequence: number;
  projectionHash: string;
}

export type ResearchV6DirectorNodeDetailView =
  | "brief"
  | "full"
  | "history"
  | (string & {});

export interface ResearchV6DirectorContentLayers {
  catalogSummary: string;
  briefSummary: string;
  objective: string;
  conclusion: string;
  content: string;
  scope: Record<string, unknown>;
  uncertainties: string[];
  conflicts: string[];
  openQuestions: string[];
}

export interface ResearchV6DirectorNodeDetail {
  snapshotId: string;
  throughEventSequence: number;
  projectionHash: string;
  view: ResearchV6DirectorNodeDetailView;
  node: ResearchV6DirectorProjectionNode;
  contentLayers?: ResearchV6DirectorContentLayers;
  incoming: ResearchV6DirectorProjectionEdge[];
  outgoing: ResearchV6DirectorProjectionEdge[];
  historyRefs: ResearchV6DirectorEntityRef[];
  agentRefs: ResearchV6DirectorEntityRef[];
  workItemRefs: ResearchV6DirectorEntityRef[];
  attemptRefs: ResearchV6DirectorEntityRef[];
  evidenceRefs: ResearchV6DirectorEntityRef[];
  discussionRefs: ResearchV6DirectorEntityRef[];
  reportRefs: ResearchV6DirectorEntityRef[];
}

export interface ResearchV6DirectorWorkActivity {
  workItemId: string;
  attemptId: string;
  agentId: string;
  agentName: string;
  inboxTaskId: string;
  mission: string;
  status: string;
  progress: string;
  progressStep: number;
  progressTotal: number;
  startedAt?: string;
  completedAt?: string;
  updatedAt: string;
  timeline: RunnerActivityTimelineRow[];
  timelineHasMore: boolean;
}

export interface ResearchV6DirectorReportReview {
  id?: string;
  decision: string;
  reason: string;
  inputStateVersion?: number;
  renderArtifactVersionId?: string;
  renderDiagnostics?: unknown;
  followUpWorkItemRefs?: unknown;
  createdAt?: string;
}

export interface ResearchV6DirectorReportMetadata {
  id: string;
  revision: number;
  status: string;
  title: string;
  summary: string;
  packageHash: string;
  documentContentHash: string;
  publishedAt: string | null;
  createdAt: string;
  authorAgentId: string;
  inputCount: number;
  latestReview: ResearchV6DirectorReportReview;
  sandboxUrl?: string;
  reportOrigin?: string;
}

export interface ResearchV6DirectorReportInputRef {
  branchId: string;
  nodeArtifactVersionId: string;
  inputRole: string;
  ordinal: number;
  contentHash: string;
}

export interface ResearchV6DirectorReportDetail {
  id: string;
  revision: number;
  status: string;
  title: string;
  summary: string;
  plainText: string;
  packageHash: string;
  documentContentHash: string;
  outline: unknown;
  citations: unknown;
  inputRefs: ResearchV6DirectorReportInputRef[];
  reviews: ResearchV6DirectorReportReview[];
  sandboxUrl?: string;
  reportOrigin?: string;
}

export interface ResearchV6DirectorSelectedRef {
  stableId: string;
  kind: ResearchV6DirectorEntityKind;
  entityId: string;
  revision: number;
  contentHash: string;
  displaySummary: string;
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
  snapshotId: string;
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
  loadWorkActivity(
    workspaceId: string,
    runId: string,
    workItemId: string,
    signal?: AbortSignal,
  ): Promise<ResearchV6DirectorWorkActivity>;
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
