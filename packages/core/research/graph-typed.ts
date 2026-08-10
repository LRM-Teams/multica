/**
 * LRM-1497 · D5 render-layer data substrate — typed graph (fed by LRM-1505).
 *
 * This module is the CANONICAL server-state layer for the typed research
 * star graph. It models the real `GET /api/research/sessions/{id}/graph/typed`
 * endpoint that landed on `dev` with PR #2594 (LRM-1505) and validates every
 * field with a Zod schema — no field is guessed, invented or defaulted to a
 * fake value (anti-surface red line).
 *
 * React Query owns this server state (see `researchGraphTypedOptions` in
 * `queries.ts`); clients/Zustand never fabricate graph topology. The views
 * star-graph surface reads the normalized nodes/clusters/lineage from here.
 */

import { z } from "zod";

/* ------------------------------------------------------------------ *
 * Typed graph node — mirrors ResearchGraphTypedNodeResp (1:1 json tags).
 * ------------------------------------------------------------------ */

export const TypedGraphNodeSchema = z
  .object({
    id: z.string(),
    session_id: z.string().optional().default(""),
    node_type: z.string().optional().default(""),
    title: z.string().optional().default(""),
    summary: z.string().optional().default(""),
    status: z.string().optional().default(""),
    actor_agent_id: z.string().nullable().optional().default(null),
    payload: z.unknown().optional(),
    /** LRM-1505 typed 5-level tier ("xxl"|"xl"|"l"|"m"|"s") — authoritative. */
    level: z.string().optional().default(""),
    round: z.number().int().optional().default(0),
    cluster_id: z.string().nullable().optional().default(null),
    confidence: z.number().nullable().optional().default(null),
    document_count: z.number().int().optional().default(0),
    conclusion_count: z.number().int().optional().default(0),
    goal_version_id: z.string().nullable().optional().default(null),
    derived_from: z.string().nullable().optional().default(null),
    merged_from: z.array(z.string()).optional().default([]),
    superseded_by: z.string().nullable().optional().default(null),
    restart_of: z.string().nullable().optional().default(null),
    invalidated_by: z.string().nullable().optional().default(null),
    superseded_at: z.string().nullable().optional().default(null),
    invalidated_at: z.string().nullable().optional().default(null),
    parent_id: z.string().nullable().optional().default(null),
    child_ids: z.array(z.string()).optional().default([]),
    children_of: z.array(z.string()).optional().default([]),
    created_at: z.string().optional().default(""),
    updated_at: z.string().optional().default(""),
  })
  .passthrough();

export type TypedGraphNode = z.infer<typeof TypedGraphNodeSchema>;

/* ------------------------------------------------------------------ *
 * Edge — mirrors ResearchGraphEdgeResp.
 * ------------------------------------------------------------------ */

export const TypedGraphEdgeSchema = z
  .object({
    id: z.string().optional().default(""),
    session_id: z.string().optional().default(""),
    from_node_id: z.string(),
    to_node_id: z.string(),
    edge_type: z.string().optional().default(""),
    created_at: z.string().optional().default(""),
  })
  .passthrough();

export type TypedGraphEdge = z.infer<typeof TypedGraphEdgeSchema>;

/* ------------------------------------------------------------------ *
 * Cluster — mirrors ResearchGraphClusterResp.
 * ------------------------------------------------------------------ */

export const TypedGraphClusterSchema = z
  .object({
    id: z.string().optional().default(""),
    session_id: z.string().optional().default(""),
    name: z.string().optional().default(""),
    label: z.string().optional().default(""),
    level: z.string().optional().default(""),
    cluster_type: z.string().optional().default(""),
    goal_version_id: z.string().nullable().optional().default(null),
    payload: z.unknown().optional(),
    created_at: z.string().optional().default(""),
    updated_at: z.string().optional().default(""),
  })
  .passthrough();

export type TypedGraphCluster = z.infer<typeof TypedGraphClusterSchema>;

/* ------------------------------------------------------------------ *
 * Lineage index — mirrors ResearchGraphLineageResp.
 * ------------------------------------------------------------------ */

/** Lineage maps: parent→child (derived) / conclusion→inputs (merged) etc. */
// NOTE: Zod v4 requires the two-arg record form (key schema, value schema) —
// the single-arg `z.record(valueType)` form is broken (throws on _zod).
const StringStringMap = z.record(z.string(), z.string()).optional().default({});
const StringStringArrayMap = z
  .record(z.string(), z.array(z.string()))
  .optional()
  .default({});

export const TypedGraphLineageSchema = z
  .object({
    derived: StringStringMap,
    merged: StringStringArrayMap,
    superseded: StringStringMap,
    restarted: StringStringMap,
    invalidated: StringStringMap,
    supersedes: StringStringArrayMap,
  })
  .passthrough();

export type TypedGraphLineage = z.infer<typeof TypedGraphLineageSchema>;

/* ------------------------------------------------------------------ *
 * Top-level response — mirrors ResearchGraphTypedResp.
 * ------------------------------------------------------------------ */

export const TypedGraphResponseSchema = z
  .object({
    session_id: z.string().optional().default(""),
    graph_version: z.number().int().optional().default(0),
    nodes: z.array(TypedGraphNodeSchema).optional().default([]),
    edges: z.array(TypedGraphEdgeSchema).optional().default([]),
    clusters: z.array(TypedGraphClusterSchema).optional().default([]),
    lineage: TypedGraphLineageSchema.optional().default({
      derived: {},
      merged: {},
      superseded: {},
      restarted: {},
      invalidated: {},
      supersedes: {},
    }),
  })
  .passthrough();

export type TypedGraphResponse = z.infer<typeof TypedGraphResponseSchema>;

/** Empty fallback — a POJO (no frozen object, so tests/normalizers can copy). */
export const EMPTY_TYPED_GRAPH: TypedGraphResponse = {
  session_id: "",
  graph_version: 0,
  nodes: [],
  edges: [],
  clusters: [],
  lineage: {
    derived: {},
    merged: {},
    superseded: {},
    restarted: {},
    invalidated: {},
    supersedes: {},
  },
};

/** Node id → node convenience index (for canvas linkage lookup). */
export type TypedGraphNodeIndex = Map<string, TypedGraphNode>;

/**
 * Build a node id → node index. Never fabricates nodes; only indexes what the
 * server returned. Missing edges target nodes are simply not present.
 */
export function indexTypedGraphNodes(nodes: readonly TypedGraphNode[]): TypedGraphNodeIndex {
  const index: TypedGraphNodeIndex = new Map();
  for (const node of nodes) {
    if (node && typeof node.id === "string" && node.id) index.set(node.id, node);
  }
  return index;
}
