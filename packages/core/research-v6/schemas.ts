import { z } from "zod";
import type {
  ResearchV6Delta,
  ResearchV6ResumeVerdict,
  ResearchV6Snapshot,
} from "../types/research-v6";
import {
  RESEARCH_V6_EDGE_TYPES,
  RESEARCH_V6_NODE_KINDS,
  RESEARCH_V6_TRANSITION_KINDS,
} from "./registry";

/**
 * Research V6 projection wire schemas — the unified, single source of truth.
 *
 * The `node_kind` / `edge_type` / `transition_kind` constants are anchored to
 * the frontend node registry (`./registry`) so the acceptance set can never
 * drift between registry and wire layer.
 *
 * Schemas are lenient by convention (see `packages/core/api/schema.ts`):
 * enums stay `z.string()` so an unknown future `node_kind` / `edge_type`
 * still parses and degrades to a generic node instead of crashing an old
 * client (design doc 7.1).
 */

/** The minimum `node_kind` set V6 must register (design doc 7.1). */
export const ResearchV6NodeKinds = RESEARCH_V6_NODE_KINDS;

/** Stable typed edge types (design doc 7.1) — anchored to the registry. */
export const ResearchV6EdgeTypes = RESEARCH_V6_EDGE_TYPES;

/** Transition kinds (design doc 7.2) — anchored to the registry. */
export const ResearchV6TransitionKinds = RESEARCH_V6_TRANSITION_KINDS;

export const ResearchV6ProjectionNodeSchema = z
  .object({
    id: z.string(),
    run_id: z.string(),
    entity_kind: z.string(),
    entity_id: z.string(),
    node_kind: z.string(),
    node_subtype: z.string().optional().default(""),
    schema_version: z.number().optional().default(1),
    title: z.string().optional().default(""),
    summary: z.string().optional().default(""),
    status: z.string().optional().default(""),
    importance: z.number().optional().default(0),
    freshness: z.string().nullable().optional().default(null),
    contract_version: z.string().nullable().optional().default(null),
    plan_version: z.string().nullable().optional().default(null),
    strategy_version: z.string().nullable().optional().default(null),
    actor_agent_id: z.string().nullable().optional().default(null),
    task_id: z.string().nullable().optional().default(null),
    attempt_id: z.string().nullable().optional().default(null),
    created_at: z.string().nullable().optional().default(null),
    updated_at: z.string().nullable().optional().default(null),
    cost: z.number().nullable().optional().default(null),
    detail: z.unknown().optional(),
    created_sequence: z.number().nullable().optional().default(null),
    updated_sequence: z.number().nullable().optional().default(null),
    terminal_sequence: z.number().nullable().optional().default(null),
  })
  .passthrough();

export const ResearchV6ProjectionEdgeSchema = z
  .object({
    id: z.string(),
    run_id: z.string(),
    from_node_id: z.string(),
    to_node_id: z.string(),
    edge_type: z.string(),
    created_sequence: z.number().nullable().optional().default(null),
    tombstoned_at_sequence: z.number().nullable().optional().default(null),
  })
  .passthrough();

export const ResearchV6DeltaSchema = z
  .object({
    from_sequence_exclusive: z.number(),
    through_sequence: z.number(),
    node_upserts: z.array(ResearchV6ProjectionNodeSchema).optional().default([]),
    edge_upserts: z.array(ResearchV6ProjectionEdgeSchema).optional().default([]),
    node_tombstones: z.array(z.string()).optional().default([]),
    edge_tombstones: z.array(z.string()).optional().default([]),
    affected_root_node_ids: z.array(z.string()).optional().default([]),
    transition_kind: z.string().nullable().optional().default(null),
  })
  .passthrough();

export const ResearchV6SnapshotSchema = z
  .object({
    snapshot_id: z.string(),
    run_id: z.string(),
    through_event_sequence: z.number(),
    graph_content_hash: z
      .object({
        nodes: z.string(),
        edges: z.string(),
      })
      .optional(),
    nodes: z.array(ResearchV6ProjectionNodeSchema).optional().default([]),
    edges: z.array(ResearchV6ProjectionEdgeSchema).optional().default([]),
    next_cursor: z.string().nullable().optional().default(null),
  })
  .passthrough();

export const ResearchV6ResumeVerdictSchema = z.union([
  z.object({ ok: z.literal(true), delta: ResearchV6DeltaSchema }),
  z.object({ ok: z.literal(false), resync_required: z.literal(true) }),
]);

export const EMPTY_RESEARCH_V6_SNAPSHOT: ResearchV6Snapshot = {
  snapshot_id: "",
  run_id: "",
  through_event_sequence: 0,
  graph_content_hash: { nodes: "", edges: "" },
  nodes: [],
  edges: [],
  next_cursor: null,
};

/** Lightweight runtime validation of a wire delta. Returns null if invalid. */
export function parseResearchV6Delta(raw: unknown): ResearchV6Delta | null {
  const result = ResearchV6DeltaSchema.safeParse(raw);
  return result.success ? (result.data as ResearchV6Delta) : null;
}

export function parseResearchV6Snapshot(raw: unknown): ResearchV6Snapshot {
  const result = ResearchV6SnapshotSchema.safeParse(raw);
  return result.success ? (result.data as ResearchV6Snapshot) : EMPTY_RESEARCH_V6_SNAPSHOT;
}

export function parseResearchV6ResumeVerdict(raw: unknown): ResearchV6ResumeVerdict {
  const result = ResearchV6ResumeVerdictSchema.safeParse(raw);
  return result.success
    ? (result.data as ResearchV6ResumeVerdict)
    : { ok: false, resync_required: true };
}
