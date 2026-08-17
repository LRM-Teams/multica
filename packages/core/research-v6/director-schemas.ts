import { z } from "zod";
import type {
  ResearchV6DirectorProjectionDelta,
  ResearchV6DirectorProjectionDeltaPage,
  ResearchV6DirectorProjectionResumeRequest,
  ResearchV6DirectorProjectionSliceRequest,
  ResearchV6DirectorProjectionSnapshot,
} from "../types/research-v6-director";

const key = z.string().min(1).max(160).regex(/^[A-Za-z0-9][A-Za-z0-9._:-]*$/);
const hash = z.string().regex(/^sha256:[0-9a-f]{64}$/);
const uuid = z.string().uuid();
const sequence = z.number().int().nonnegative();
const timestamp = z.string().datetime({ offset: true });

export const ResearchV6DirectorEntityRefSchema = z
  .object({
    kind: z.enum([
      "goal",
      "branch",
      "task",
      "attempt",
      "work_item",
      "agent",
      "result",
      "insight",
      "discussion",
      "dispute",
      "integration",
      "report",
      "source_snapshot",
      "observation",
      "claim",
      "evidence_link",
    ]),
    id: uuid,
    revision: z.number().int().positive().optional(),
    version_id: uuid.optional(),
    content_hash: hash.optional(),
  })
  .strict();

export const ResearchV6DirectorProjectionStateSchema = z
  .object({
    execution: z.enum([
      "pending",
      "running",
      "succeeded",
      "failed",
      "cancelled",
      "lost",
    ]),
    conclusion: z.enum([
      "proposed",
      "accepted",
      "challenged",
      "refuted",
      "invalid",
    ]),
    integration: z.enum([
      "unmatched",
      "candidate",
      "discussing",
      "absorbed",
      "excluded",
    ]),
    termination: z
      .object({
        reason_code: z.enum([
          "invalid_direction",
          "dead_end",
          "no_semantic_gain",
          "duplicate",
          "out_of_scope",
          "stopped_by_user",
          "stopped_by_director",
          "resource_failure",
          "superseded",
          "other",
        ]),
        reason_detail: z.string().min(1).max(32_768),
      })
      .strict()
      .optional(),
  })
  .strict();

export const ResearchV6DirectorProjectionNodeSchema = z
  .object({
    id: key,
    kind: z.enum(["goal", "work_s", "result_s", "insight"]),
    tier: z.enum(["GOAL", "S", "M", "L", "XL", "XXL"]),
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
    kind: z.enum([
      "derived_from",
      "absorbed_into",
      "produced_by",
      "belongs_to",
      "challenges",
      "collapsed_path",
    ]),
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

export function parseResearchV6DirectorProjectionSnapshot(
  value: unknown,
): ResearchV6DirectorProjectionSnapshot {
  return ResearchV6DirectorProjectionSnapshotSchema.parse(
    value,
  ) as ResearchV6DirectorProjectionSnapshot;
}

export function parseResearchV6DirectorProjectionDelta(
  value: unknown,
): ResearchV6DirectorProjectionDelta {
  return ResearchV6DirectorProjectionDeltaSchema.parse(value) as ResearchV6DirectorProjectionDelta;
}

export function parseResearchV6DirectorProjectionDeltaPage(
  value: unknown,
): ResearchV6DirectorProjectionDeltaPage {
  return ResearchV6DirectorProjectionDeltaPageSchema.parse(
    value,
  ) as ResearchV6DirectorProjectionDeltaPage;
}

export function parseResearchV6DirectorProjectionResumeRequest(
  value: unknown,
): ResearchV6DirectorProjectionResumeRequest {
  return ResearchV6DirectorProjectionResumeRequestSchema.parse(
    value,
  ) as ResearchV6DirectorProjectionResumeRequest;
}

export function parseResearchV6DirectorProjectionSliceRequest(
  value: unknown,
): ResearchV6DirectorProjectionSliceRequest {
  return ResearchV6DirectorProjectionSliceRequestSchema.parse(
    value,
  ) as ResearchV6DirectorProjectionSliceRequest;
}
