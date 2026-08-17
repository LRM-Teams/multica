import { z } from "zod";
import { parseWithFallback } from "../api/schema";
import type {
  ResearchV6DirectorProjectionDelta,
  ResearchV6DirectorProjectionDeltaPage,
  ResearchV6DirectorProjectionResumeRequest,
  ResearchV6DirectorProjectionSliceRequest,
  ResearchV6DirectorProjectionSnapshot,
  ResearchV6DirectorNodeDetail,
  ResearchV6DirectorReportDetail,
  ResearchV6DirectorReportMetadata,
} from "../types/research-v6-director";

const key = z.string().min(1).max(160).regex(/^[A-Za-z0-9][A-Za-z0-9._:-]*$/);
const hash = z.string().regex(/^sha256:[0-9a-f]{64}$/);
const uuid = z.string().uuid();
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
    state: ResearchV6DirectorProjectionStateSchema,
    title: z.string().max(4096).optional(),
    catalog_summary: z.string().min(1).max(512),
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

export const ResearchV6DirectorNodeDetailSchema = z
  .object({
    snapshot_id: uuid,
    through_event_sequence: sequence,
    projection_hash: hash,
    view: z.enum(["brief", "full", "history"]),
    node: ResearchV6DirectorProjectionNodeSchema,
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

const EMPTY_HASH = `sha256:${"0".repeat(64)}`;
const EMPTY_ID = "00000000-0000-0000-0000-000000000000";
const EMPTY_DIRECTOR_SNAPSHOT: ResearchV6DirectorProjectionSnapshot = {
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
};
const EMPTY_DIRECTOR_DELTA: ResearchV6DirectorProjectionDelta = {
  contract_kind: "projection_delta", schema_version: 6, workspace_id: EMPTY_ID, run_id: EMPTY_ID,
  snapshot_id: EMPTY_ID, event_sequence: 0, previous_projection_hash: EMPTY_HASH, projection_hash: EMPTY_HASH,
  upsert_nodes: [], remove_node_ids: [], upsert_edges: [], remove_edge_ids: [], invalidate_slice_keys: [],
};
const EMPTY_DIRECTOR_NODE_DETAIL: ResearchV6DirectorNodeDetail = {
  snapshot_id: EMPTY_ID, through_event_sequence: 0, projection_hash: EMPTY_HASH, view: "brief",
  node: { id: "invalid-response", kind: "goal", tier: "GOAL", canonical_ref: { kind: "goal", id: EMPTY_ID }, branch_ids: [], state: { execution: "failed", conclusion: "invalid", integration: "excluded" }, catalog_summary: "Invalid response", absorbed: false, terminal: true, expandable: false, hidden_child_count: 0, updated_at: "1970-01-01T00:00:00.000Z" },
  incoming: [], outgoing: [], history_refs: [], agent_refs: [], work_item_refs: [], attempt_refs: [], evidence_refs: [], discussion_refs: [], report_refs: [],
};
const EMPTY_DIRECTOR_REPORT_DETAIL: ResearchV6DirectorReportDetail = {
  id: EMPTY_ID, revision: 1, status: "technical_failure", title: "Invalid response", summary: "", plain_text: "", package_hash: EMPTY_HASH, document_content_hash: EMPTY_HASH, outline: [], citations: [], input_refs: [], reviews: [],
};

function invalidRequest(name: string): never {
  throw new Error(`${name} failed schema validation`);
}

export function parseResearchV6DirectorProjectionSnapshot(
  value: unknown,
): ResearchV6DirectorProjectionSnapshot {
  return parseWithFallback(value, ResearchV6DirectorProjectionSnapshotSchema, EMPTY_DIRECTOR_SNAPSHOT, {
    endpoint: "GET Director V6 projection snapshot",
  });
}

export function parseResearchV6DirectorProjectionDelta(
  value: unknown,
): ResearchV6DirectorProjectionDelta {
  return parseWithFallback(value, ResearchV6DirectorProjectionDeltaSchema, EMPTY_DIRECTOR_DELTA, {
    endpoint: "Director V6 projection delta",
  });
}

export function parseResearchV6DirectorProjectionDeltaPage(
  value: unknown,
): ResearchV6DirectorProjectionDeltaPage {
  return parseWithFallback(value, ResearchV6DirectorProjectionDeltaPageSchema, {
    run_id: EMPTY_ID,
    deltas: [],
    next_cursor: null,
    resync_required: true,
  } satisfies ResearchV6DirectorProjectionDeltaPage, {
    endpoint: "GET Director V6 projection deltas",
  });
}

export function parseResearchV6DirectorProjectionResumeRequest(
  value: unknown,
): ResearchV6DirectorProjectionResumeRequest {
  const result = ResearchV6DirectorProjectionResumeRequestSchema.safeParse(value);
  return result.success
    ? (result.data as ResearchV6DirectorProjectionResumeRequest)
    : invalidRequest("Director V6 projection resume request");
}

export function parseResearchV6DirectorProjectionSliceRequest(
  value: unknown,
): ResearchV6DirectorProjectionSliceRequest {
  const result = ResearchV6DirectorProjectionSliceRequestSchema.safeParse(value);
  return result.success
    ? (result.data as ResearchV6DirectorProjectionSliceRequest)
    : invalidRequest("Director V6 projection slice request");
}

export function parseResearchV6DirectorNodeDetail(
  value: unknown,
): ResearchV6DirectorNodeDetail {
  return parseWithFallback(value, ResearchV6DirectorNodeDetailSchema, EMPTY_DIRECTOR_NODE_DETAIL, {
    endpoint: "GET Director V6 projection node detail",
  });
}

export function parseResearchV6DirectorReportList(
  value: unknown,
): ResearchV6DirectorReportMetadata[] {
  const envelope = parseWithFallback(value, z
    .object({
      reports: z.array(ResearchV6DirectorReportMetadataSchema).max(10_000),
    })
    .strict(), { reports: [] as ResearchV6DirectorReportMetadata[] }, {
      endpoint: "GET Director V6 reports",
    });
  return envelope.reports;
}

export function parseResearchV6DirectorReportDetail(
  value: unknown,
): ResearchV6DirectorReportDetail {
  return parseWithFallback(value, ResearchV6DirectorReportDetailSchema, EMPTY_DIRECTOR_REPORT_DETAIL, {
    endpoint: "GET Director V6 report detail",
  });
}
