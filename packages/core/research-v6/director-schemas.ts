import { z } from "zod";
import { parseWithFallback } from "../api/schema";
import type {
  ResearchV6DirectorDensityBin,
  ResearchV6DirectorEntityRef,
  ResearchV6DirectorProjectionEdge,
  ResearchV6DirectorProjectionDelta,
  ResearchV6DirectorProjectionDeltaPage,
  ResearchV6DirectorProjectionNode,
  ResearchV6DirectorProjectionResumeRequest,
  ResearchV6DirectorProjectionSliceRequest,
  ResearchV6DirectorProjectionSnapshot,
  ResearchV6DirectorNodeDetail,
  ResearchV6DirectorReportDetail,
  ResearchV6DirectorReportMetadata,
  ResearchV6DirectorReportReview,
  ResearchV6DirectorWorkActivity,
} from "../types/research-v6-director";

const key = z.string().min(1).max(160).regex(/^[A-Za-z0-9][A-Za-z0-9._:-]*$/);
const hash = z.string().regex(/^sha256:[0-9a-f]{64}$/);
const uuid = z.string().uuid();
const optionalUuidIdentity = z.union([uuid, z.literal("")]);
const sequence = z.number().int().nonnegative();
const timestamp = z.string().datetime({ offset: true });
const forwardCompatibleToken = z
  .string()
  .min(1)
  .max(160)
  .regex(/^[a-z][a-z0-9_]*$/);

export const ResearchV6DirectorEntityRefSchema = z
  .object({
    kind: forwardCompatibleToken,
    id: uuid,
    revision: z.number().int().positive().optional(),
    version_id: uuid.optional(),
    content_hash: hash.optional(),
  })
  .strict();

export const ResearchV6DirectorProjectionStateSchema = z
  .object({
    execution: forwardCompatibleToken,
    conclusion: forwardCompatibleToken,
    integration: forwardCompatibleToken,
    termination: z
      .object({
        reason_code: forwardCompatibleToken,
        reason_detail: z.string().min(1).max(32_768),
      })
      .strict()
      .optional(),
  })
  .strict();

export const ResearchV6DirectorProjectionNodeSchema = z
  .object({
    id: key,
    kind: forwardCompatibleToken,
    tier: z.string().min(1).max(16).regex(/^[A-Z][A-Z0-9_]*$/),
    canonical_ref: ResearchV6DirectorEntityRefSchema,
    branch_ids: z
      .array(uuid)
      .max(128)
      .refine((values) => new Set(values).size === values.length),
    territory: z
      .object({
        branch_id: uuid,
        label: z.string().min(1).max(160),
      })
      .strict()
      .optional(),
    state: ResearchV6DirectorProjectionStateSchema,
    title: z.string().max(4096).optional(),
    catalog_summary: z.string().max(512),
    absorbed: z.boolean(),
    terminal: z.boolean(),
    expandable: z.boolean(),
    hidden_child_count: z.number().int().nonnegative(),
    updated_at: timestamp,
  })
  .strict();

export const ResearchV6DirectorProjectionEdgeSchema = z
  .object({
    id: key,
    kind: forwardCompatibleToken,
    from_node_id: key,
    to_node_id: key,
    canonical: z.boolean(),
    hidden_count: z.number().int().nonnegative(),
    expandable: z.boolean(),
  })
  .strict();

export const ResearchV6DirectorDensityBinSchema = z
  .object({
    id: key,
    branch_id: uuid,
    bounds: z
      .object({
        x: z.number(),
        y: z.number(),
        width: z.number().positive(),
        height: z.number().positive(),
      })
      .strict(),
    total: z.number().int().positive(),
    reason_counts: z.record(z.string(), z.number().int().nonnegative()),
    execution_counts: z.record(z.string(), z.number().int().nonnegative()),
  })
  .strict();

export const ResearchV6DirectorProjectionSnapshotSchema = z
  .object({
    contract_kind: z.literal("projection_snapshot"),
    schema_version: z.literal(6),
    snapshot_id: uuid,
    workspace_id: uuid,
    run_id: uuid,
    through_event_sequence: sequence,
    projection_hash: hash,
    slice_key: key,
    nodes: z.array(ResearchV6DirectorProjectionNodeSchema).max(10_000),
    edges: z.array(ResearchV6DirectorProjectionEdgeSchema).max(20_000),
    density_bins: z.array(ResearchV6DirectorDensityBinSchema).max(10_000),
    has_more: z.boolean(),
    next_cursor: z.string().max(4096).optional(),
  })
  .strict();

export const ResearchV6DirectorProjectionDeltaSchema = z
  .object({
    contract_kind: z.literal("projection_delta"),
    schema_version: z.literal(6),
    workspace_id: uuid,
    run_id: uuid,
    snapshot_id: uuid,
    event_sequence: sequence,
    previous_projection_hash: hash,
    projection_hash: hash,
    upsert_nodes: z.array(ResearchV6DirectorProjectionNodeSchema).max(10_000),
    remove_node_ids: z
      .array(key)
      .max(10_000)
      .refine((values) => new Set(values).size === values.length),
    upsert_edges: z.array(ResearchV6DirectorProjectionEdgeSchema).max(20_000),
    remove_edge_ids: z
      .array(key)
      .max(20_000)
      .refine((values) => new Set(values).size === values.length),
    invalidate_slice_keys: z
      .array(key)
      .max(1024)
      .refine((values) => new Set(values).size === values.length),
  })
  .strict();

export const ResearchV6DirectorProjectionDeltaPageSchema = z
  .object({
    run_id: uuid,
    deltas: z.array(ResearchV6DirectorProjectionDeltaSchema),
    next_cursor: z.string().max(4096).nullable(),
    resync_required: z.boolean(),
  })
  .strict();

export const ResearchV6DirectorProjectionResumeRequestSchema = z
  .object({
    snapshot_id: uuid,
    last_confirmed_sequence: sequence,
    projection_hash: hash,
  })
  .strict();

export const ResearchV6DirectorProjectionSliceRequestSchema = z
  .object({
    root: key,
    depth: z.literal(1),
    snapshot_id: uuid,
    cursor: z.string().max(4096).optional(),
  })
  .strict();

export const ResearchV6DirectorAssignmentSchema = z.object({
  id: uuid,
  workspace_id: uuid,
  run_id: uuid,
  director_agent_id: uuid,
  status: z.string().min(1).max(160),
  reason: z.string().max(32_768),
  generation: z.number().int().positive(),
  state_version: z.number().int().nonnegative(),
}).strict();

const entityRefs = z.array(ResearchV6DirectorEntityRefSchema).max(10_000);
const contentText = z.string().min(1).max(32_768);
const ResearchV6DirectorContentLayersSchema = z
  .object({
    catalog_summary: z.string().min(1).max(512),
    brief_summary: contentText,
    objective: contentText,
    conclusion: contentText,
    content: z.string().min(1).max(1_048_576),
    scope: z.record(z.string(), z.unknown()),
    uncertainties: z.array(contentText).max(128),
    conflicts: z.array(contentText).max(128),
    open_questions: z.array(contentText).max(128),
  })
  .strict();

export const ResearchV6DirectorNodeDetailSchema = z
  .object({
    snapshot_id: uuid,
    through_event_sequence: sequence,
    projection_hash: hash,
    view: forwardCompatibleToken,
    node: ResearchV6DirectorProjectionNodeSchema,
    content_layers: ResearchV6DirectorContentLayersSchema.optional(),
    incoming: z.array(ResearchV6DirectorProjectionEdgeSchema).max(20_000),
    outgoing: z.array(ResearchV6DirectorProjectionEdgeSchema).max(20_000),
    history_refs: entityRefs,
    agent_refs: entityRefs,
    work_item_refs: entityRefs,
    attempt_refs: entityRefs,
    evidence_refs: entityRefs,
    discussion_refs: entityRefs,
    report_refs: entityRefs,
  })
  .strict();

export const ResearchV6DirectorWorkActivitySchema = z
  .object({
    work_item_id: uuid,
    attempt_id: optionalUuidIdentity,
    agent_id: optionalUuidIdentity,
    agent_name: z.string(),
    inbox_task_id: optionalUuidIdentity,
    mission: z.string(),
    status: z.string(),
    progress: z.string(),
    progress_step: z.number().int().nonnegative(),
    progress_total: z.number().int().nonnegative(),
    started_at: timestamp.optional(),
    completed_at: timestamp.optional(),
    updated_at: timestamp,
    timeline: z.array(z.unknown()).max(10_000),
    timeline_has_more: z.boolean(),
  })
  .strict();

const reportReview = z
  .object({
    id: uuid.optional(),
    decision: z.string().max(160),
    reason: z.string().max(32_768),
    input_state_version: sequence.optional(),
    render_artifact_version_id: z.string().optional(),
    render_diagnostics: z.unknown().optional(),
    follow_up_work_item_refs: z.unknown().optional(),
    created_at: timestamp.optional(),
  })
  .strict();

export const ResearchV6DirectorReportMetadataSchema = z
  .object({
    id: uuid,
    revision: z.number().int().positive(),
    status: z.string().min(1).max(160),
    title: z.string().max(4096),
    summary: z.string().max(32_768),
    package_hash: z.string(),
    document_content_hash: z.string(),
    published_at: timestamp.nullable(),
    created_at: timestamp,
    author_agent_id: z.string(),
    input_count: z.number().int().nonnegative(),
    latest_review: reportReview,
    sandbox_url: z.string().url().optional(),
    report_origin: z.string().url().optional(),
  })
  .strict();

const reportInputRef = z
  .object({
    branch_id: uuid,
    node_artifact_version_id: uuid,
    input_role: z.string().min(1).max(160),
    ordinal: z.number().int().nonnegative(),
    content_hash: hash,
  })
  .strict();

export const ResearchV6DirectorReportDetailSchema = z
  .object({
    id: uuid,
    revision: z.number().int().positive(),
    status: z.string().min(1).max(160),
    title: z.string().max(4096),
    summary: z.string().max(32_768),
    plain_text: z.string(),
    package_hash: z.string(),
    document_content_hash: z.string(),
    outline: z.unknown(),
    citations: z.unknown(),
    input_refs: z.array(reportInputRef).max(10_000),
    reviews: z.array(reportReview).max(10_000),
    sandbox_url: z.string().url().optional(),
    report_origin: z.string().url().optional(),
  })
  .strict();

type WireEntityRef = z.output<typeof ResearchV6DirectorEntityRefSchema>;
type WireProjectionNode = z.output<typeof ResearchV6DirectorProjectionNodeSchema>;
type WireProjectionEdge = z.output<typeof ResearchV6DirectorProjectionEdgeSchema>;
type WireDensityBin = z.output<typeof ResearchV6DirectorDensityBinSchema>;
type WireProjectionSnapshot = z.output<typeof ResearchV6DirectorProjectionSnapshotSchema>;
type WireProjectionDelta = z.output<typeof ResearchV6DirectorProjectionDeltaSchema>;
type WireNodeDetail = z.output<typeof ResearchV6DirectorNodeDetailSchema>;
type WireReportReview = z.output<typeof reportReview>;
type WireReportMetadata = z.output<typeof ResearchV6DirectorReportMetadataSchema>;
type WireReportDetail = z.output<typeof ResearchV6DirectorReportDetailSchema>;

function entityRefFromWire(value: WireEntityRef): ResearchV6DirectorEntityRef {
  return {
    kind: value.kind,
    id: value.id,
    revision: value.revision,
    versionId: value.version_id,
    contentHash: value.content_hash,
  };
}

function projectionNodeFromWire(
  value: WireProjectionNode,
): ResearchV6DirectorProjectionNode {
  return {
    id: value.id,
    kind: value.kind,
    tier: value.tier,
    canonicalRef: entityRefFromWire(value.canonical_ref),
    branchIds: value.branch_ids,
    territory: value.territory
      ? {
          branchId: value.territory.branch_id,
          label: value.territory.label,
        }
      : undefined,
    state: {
      execution: value.state.execution,
      conclusion: value.state.conclusion,
      integration: value.state.integration,
      termination: value.state.termination
        ? {
            reasonCode: value.state.termination.reason_code,
            reasonDetail: value.state.termination.reason_detail,
          }
        : undefined,
    },
    title: value.title,
    catalogSummary: value.catalog_summary,
    absorbed: value.absorbed,
    terminal: value.terminal,
    expandable: value.expandable,
    hiddenChildCount: value.hidden_child_count,
    updatedAt: value.updated_at,
  };
}

function projectionEdgeFromWire(
  value: WireProjectionEdge,
): ResearchV6DirectorProjectionEdge {
  return {
    id: value.id,
    kind: value.kind,
    fromNodeId: value.from_node_id,
    toNodeId: value.to_node_id,
    canonical: value.canonical,
    hiddenCount: value.hidden_count,
    expandable: value.expandable,
  };
}

function densityBinFromWire(value: WireDensityBin): ResearchV6DirectorDensityBin {
  return {
    id: value.id,
    branchId: value.branch_id,
    bounds: value.bounds,
    total: value.total,
    reasonCounts: value.reason_counts,
    executionCounts: value.execution_counts,
  };
}

function projectionSnapshotFromWire(
  value: WireProjectionSnapshot,
): ResearchV6DirectorProjectionSnapshot {
  return {
    contractKind: value.contract_kind,
    schemaVersion: value.schema_version,
    snapshotId: value.snapshot_id,
    workspaceId: value.workspace_id,
    runId: value.run_id,
    throughEventSequence: value.through_event_sequence,
    projectionHash: value.projection_hash,
    sliceKey: value.slice_key,
    nodes: value.nodes.map(projectionNodeFromWire),
    edges: value.edges.map(projectionEdgeFromWire),
    densityBins: value.density_bins.map(densityBinFromWire),
    hasMore: value.has_more,
    nextCursor: value.next_cursor,
  };
}

function projectionDeltaFromWire(
  value: WireProjectionDelta,
): ResearchV6DirectorProjectionDelta {
  return {
    contractKind: value.contract_kind,
    schemaVersion: value.schema_version,
    workspaceId: value.workspace_id,
    runId: value.run_id,
    snapshotId: value.snapshot_id,
    eventSequence: value.event_sequence,
    previousProjectionHash: value.previous_projection_hash,
    projectionHash: value.projection_hash,
    upsertNodes: value.upsert_nodes.map(projectionNodeFromWire),
    removeNodeIds: value.remove_node_ids,
    upsertEdges: value.upsert_edges.map(projectionEdgeFromWire),
    removeEdgeIds: value.remove_edge_ids,
    invalidateSliceKeys: value.invalidate_slice_keys,
  };
}

function nodeDetailFromWire(value: WireNodeDetail): ResearchV6DirectorNodeDetail {
  return {
    snapshotId: value.snapshot_id,
    throughEventSequence: value.through_event_sequence,
    projectionHash: value.projection_hash,
    view: value.view,
    node: projectionNodeFromWire(value.node),
    contentLayers: value.content_layers
      ? {
          catalogSummary: value.content_layers.catalog_summary,
          briefSummary: value.content_layers.brief_summary,
          objective: value.content_layers.objective,
          conclusion: value.content_layers.conclusion,
          content: value.content_layers.content,
          scope: value.content_layers.scope,
          uncertainties: value.content_layers.uncertainties,
          conflicts: value.content_layers.conflicts,
          openQuestions: value.content_layers.open_questions,
        }
      : undefined,
    incoming: value.incoming.map(projectionEdgeFromWire),
    outgoing: value.outgoing.map(projectionEdgeFromWire),
    historyRefs: value.history_refs.map(entityRefFromWire),
    agentRefs: value.agent_refs.map(entityRefFromWire),
    workItemRefs: value.work_item_refs.map(entityRefFromWire),
    attemptRefs: value.attempt_refs.map(entityRefFromWire),
    evidenceRefs: value.evidence_refs.map(entityRefFromWire),
    discussionRefs: value.discussion_refs.map(entityRefFromWire),
    reportRefs: value.report_refs.map(entityRefFromWire),
  };
}

function reportReviewFromWire(
  value: WireReportReview,
): ResearchV6DirectorReportReview {
  return {
    id: value.id,
    decision: value.decision,
    reason: value.reason,
    inputStateVersion: value.input_state_version,
    renderArtifactVersionId: value.render_artifact_version_id,
    renderDiagnostics: value.render_diagnostics,
    followUpWorkItemRefs: value.follow_up_work_item_refs,
    createdAt: value.created_at,
  };
}

function reportMetadataFromWire(
  value: WireReportMetadata,
): ResearchV6DirectorReportMetadata {
  return {
    id: value.id,
    revision: value.revision,
    status: value.status,
    title: value.title,
    summary: value.summary,
    packageHash: value.package_hash,
    documentContentHash: value.document_content_hash,
    publishedAt: value.published_at,
    createdAt: value.created_at,
    authorAgentId: value.author_agent_id,
    inputCount: value.input_count,
    latestReview: reportReviewFromWire(value.latest_review),
    sandboxUrl: value.sandbox_url,
    reportOrigin: value.report_origin,
  };
}

function reportDetailFromWire(
  value: WireReportDetail,
): ResearchV6DirectorReportDetail {
  return {
    id: value.id,
    revision: value.revision,
    status: value.status,
    title: value.title,
    summary: value.summary,
    plainText: value.plain_text,
    packageHash: value.package_hash,
    documentContentHash: value.document_content_hash,
    outline: value.outline,
    citations: value.citations,
    inputRefs: value.input_refs.map((ref) => ({
      branchId: ref.branch_id,
      nodeArtifactVersionId: ref.node_artifact_version_id,
      inputRole: ref.input_role,
      ordinal: ref.ordinal,
      contentHash: ref.content_hash,
    })),
    reviews: value.reviews.map(reportReviewFromWire),
    sandboxUrl: value.sandbox_url,
    reportOrigin: value.report_origin,
  };
}

const EMPTY_HASH = `sha256:${"0".repeat(64)}`;
const EMPTY_ID = "00000000-0000-0000-0000-000000000000";
const EMPTY_DIRECTOR_SNAPSHOT_WIRE = {
  contract_kind: "projection_snapshot",
  schema_version: 6,
  snapshot_id: EMPTY_ID,
  workspace_id: EMPTY_ID,
  run_id: EMPTY_ID,
  through_event_sequence: 0,
  projection_hash: EMPTY_HASH,
  slice_key: "invalid-response",
  nodes: [],
  edges: [],
  density_bins: [],
  has_more: false,
} satisfies WireProjectionSnapshot;
const EMPTY_DIRECTOR_DELTA_WIRE = {
  contract_kind: "projection_delta",
  schema_version: 6,
  workspace_id: EMPTY_ID,
  run_id: EMPTY_ID,
  snapshot_id: EMPTY_ID,
  event_sequence: 0,
  previous_projection_hash: EMPTY_HASH,
  projection_hash: EMPTY_HASH,
  upsert_nodes: [],
  remove_node_ids: [],
  upsert_edges: [],
  remove_edge_ids: [],
  invalidate_slice_keys: [],
} satisfies WireProjectionDelta;
const EMPTY_DIRECTOR_NODE_DETAIL_WIRE = {
  snapshot_id: EMPTY_ID, through_event_sequence: 0, projection_hash: EMPTY_HASH, view: "brief",
  node: { id: "invalid-response", kind: "goal", tier: "GOAL", canonical_ref: { kind: "goal", id: EMPTY_ID }, branch_ids: [], state: { execution: "failed", conclusion: "invalid", integration: "excluded" }, catalog_summary: "Invalid response", absorbed: false, terminal: true, expandable: false, hidden_child_count: 0, updated_at: "1970-01-01T00:00:00.000Z" },
  incoming: [], outgoing: [], history_refs: [], agent_refs: [], work_item_refs: [], attempt_refs: [], evidence_refs: [], discussion_refs: [], report_refs: [],
} satisfies WireNodeDetail;
const EMPTY_DIRECTOR_REPORT_DETAIL_WIRE = {
  id: EMPTY_ID, revision: 1, status: "technical_failure", title: "Invalid response", summary: "", plain_text: "", package_hash: EMPTY_HASH, document_content_hash: EMPTY_HASH, outline: [], citations: [], input_refs: [], reviews: [],
} satisfies WireReportDetail;

export function parseResearchV6DirectorProjectionSnapshot(
  value: unknown,
): ResearchV6DirectorProjectionSnapshot {
  const wire = parseWithFallback(
    value,
    ResearchV6DirectorProjectionSnapshotSchema,
    EMPTY_DIRECTOR_SNAPSHOT_WIRE,
    { endpoint: "GET Director V6 projection snapshot" },
  );
  return projectionSnapshotFromWire(wire);
}

export function parseResearchV6DirectorProjectionDelta(
  value: unknown,
): ResearchV6DirectorProjectionDelta {
  const wire = parseWithFallback(
    value,
    ResearchV6DirectorProjectionDeltaSchema,
    EMPTY_DIRECTOR_DELTA_WIRE,
    { endpoint: "Director V6 projection delta" },
  );
  return projectionDeltaFromWire(wire);
}

export function parseResearchV6DirectorProjectionDeltaPage(
  value: unknown,
): ResearchV6DirectorProjectionDeltaPage {
  const wire = parseWithFallback(value, ResearchV6DirectorProjectionDeltaPageSchema, {
    run_id: EMPTY_ID,
    deltas: [],
    next_cursor: null,
    resync_required: true,
  }, {
    endpoint: "GET Director V6 projection deltas",
  });
  return {
    runId: wire.run_id,
    deltas: wire.deltas.map(projectionDeltaFromWire),
    nextCursor: wire.next_cursor,
    resyncRequired: wire.resync_required,
  };
}

export function encodeResearchV6DirectorProjectionResumeRequest(
  value: ResearchV6DirectorProjectionResumeRequest,
): z.output<typeof ResearchV6DirectorProjectionResumeRequestSchema> {
  const result = ResearchV6DirectorProjectionResumeRequestSchema.safeParse({
    snapshot_id: value.snapshotId,
    last_confirmed_sequence: value.lastConfirmedSequence,
    projection_hash: value.projectionHash,
  });
  if (!result.success) throw new Error("Director V6 projection resume request is invalid");
  return result.data;
}

export function encodeResearchV6DirectorProjectionSliceRequest(
  value: ResearchV6DirectorProjectionSliceRequest,
): z.output<typeof ResearchV6DirectorProjectionSliceRequestSchema> {
  const result = ResearchV6DirectorProjectionSliceRequestSchema.safeParse({
    root: value.root,
    depth: value.depth,
    snapshot_id: value.snapshotId,
    cursor: value.cursor,
  });
  if (!result.success) throw new Error("Director V6 projection slice request is invalid");
  return result.data;
}

export function parseResearchV6DirectorNodeDetail(
  value: unknown,
): ResearchV6DirectorNodeDetail {
  const wire = parseWithFallback(
    value,
    ResearchV6DirectorNodeDetailSchema,
    EMPTY_DIRECTOR_NODE_DETAIL_WIRE,
    { endpoint: "GET Director V6 projection node detail" },
  );
  return nodeDetailFromWire(wire);
}

export function parseResearchV6DirectorWorkActivity(
  value: unknown,
): ResearchV6DirectorWorkActivity | null {
  const result = ResearchV6DirectorWorkActivitySchema.safeParse(value);
  if (!result.success) return null;
  return {
    workItemId: result.data.work_item_id,
    attemptId: result.data.attempt_id,
    agentId: result.data.agent_id,
    agentName: result.data.agent_name,
    inboxTaskId: result.data.inbox_task_id,
    mission: result.data.mission,
    status: result.data.status,
    progress: result.data.progress,
    progressStep: result.data.progress_step,
    progressTotal: result.data.progress_total,
    startedAt: result.data.started_at,
    completedAt: result.data.completed_at,
    updatedAt: result.data.updated_at,
    timeline:
      result.data.timeline as ResearchV6DirectorWorkActivity["timeline"],
    timelineHasMore: result.data.timeline_has_more,
  };
}

export function parseResearchV6DirectorReportList(
  value: unknown,
): ResearchV6DirectorReportMetadata[] {
  const envelope = parseWithFallback(value, z
    .object({
      reports: z.array(ResearchV6DirectorReportMetadataSchema).max(10_000),
    })
    .strict(), { reports: [] as WireReportMetadata[] }, {
      endpoint: "GET Director V6 reports",
    });
  return envelope.reports.map(reportMetadataFromWire);
}

export function parseResearchV6DirectorReportDetail(
  value: unknown,
): ResearchV6DirectorReportDetail {
  const wire = parseWithFallback(
    value,
    ResearchV6DirectorReportDetailSchema,
    EMPTY_DIRECTOR_REPORT_DETAIL_WIRE,
    { endpoint: "GET Director V6 report detail" },
  );
  return reportDetailFromWire(wire);
}
