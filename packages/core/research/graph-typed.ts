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
    round: z.number().int().optional(),
    cluster_id: z.string().nullable().optional().default(null),
    confidence: z.number().nullable().optional().default(null),
    document_count: z.number().int().optional(),
    conclusion_count: z.number().int().optional(),
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
    /** Server-side total when the graph is paginated (optional until BE ships). */
    total_node_count: z.number().int().nullable().optional().default(null),
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

/** Retained-node budget for paginated typed-graph client cache (viewport-performance §3). */
export const RESEARCH_TYPED_GRAPH_CACHE_NODE_BUDGET = 1500;

export type MergeTypedGraphPagesOptions = {
  /** Max nodes retained after merging pages; defaults to RESEARCH_TYPED_GRAPH_CACHE_NODE_BUDGET. */
  nodeBudget?: number;
  /** Node ids that stay loaded when trimming (e.g. canvas selection). */
  pinNodeIds?: readonly string[];
};

/** Empty fallback — a POJO (no frozen object, so tests/normalizers can copy). */
export const EMPTY_TYPED_GRAPH: TypedGraphResponse = {
  session_id: "",
  graph_version: 0,
  total_node_count: null,
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

function mergeRecordOfString(
  maps: readonly Record<string, string>[],
): Record<string, string> {
  const out: Record<string, string> = {};
  for (const map of maps) {
    for (const [key, value] of Object.entries(map)) {
      if (!(key in out)) out[key] = value;
    }
  }
  return out;
}

function mergeRecordOfStringArray(
  maps: readonly Record<string, string[]>[],
): Record<string, string[]> {
  const out: Record<string, string[]> = {};
  for (const map of maps) {
    for (const [key, values] of Object.entries(map)) {
      const bucket = out[key] ?? [];
      const seen = new Set(bucket);
      for (const value of values) {
        if (seen.has(value)) continue;
        bucket.push(value);
        seen.add(value);
      }
      out[key] = bucket;
    }
  }
  return out;
}

/** Merge paginated typed-graph pages into one render pass (deduped, stable order). */
export function mergeTypedGraphPages(
  pages: readonly TypedGraphResponse[],
  options?: MergeTypedGraphPagesOptions,
): TypedGraphResponse {
  if (pages.length === 0) return { ...EMPTY_TYPED_GRAPH };

  const orderedNodeIds: string[] = [];
  const nodeById = new Map<string, TypedGraphNode>();
  const edgeByKey = new Map<string, TypedGraphEdge>();
  const clusterById = new Map<string, TypedGraphCluster>();
  const first = pages[0]!;
  let graphVersion = first.graph_version ?? 0;

  for (const page of pages) {
    graphVersion = Math.max(graphVersion, page.graph_version ?? 0);
    for (const node of page.nodes ?? []) {
      if (!node?.id) continue;
      if (!nodeById.has(node.id)) orderedNodeIds.push(node.id);
      else continue;
      nodeById.set(node.id, node);
    }
    for (const edge of page.edges ?? []) {
      const key =
        edge.id ||
        `${edge.from_node_id}:${edge.to_node_id}:${edge.edge_type ?? ""}`;
      if (!edgeByKey.has(key)) edgeByKey.set(key, edge);
    }
    for (const cluster of page.clusters ?? []) {
      const id = cluster.id || cluster.name;
      if (id && !clusterById.has(id)) clusterById.set(id, cluster);
    }
  }

  const lineagePages = pages.map((page) => page.lineage ?? EMPTY_TYPED_GRAPH.lineage);
  const merged: TypedGraphResponse = {
    session_id: first.session_id,
    graph_version: graphVersion,
    total_node_count: first.total_node_count ?? null,
    nodes: orderedNodeIds.map((id) => nodeById.get(id)!),
    edges: [...edgeByKey.values()],
    clusters: [...clusterById.values()],
    lineage: {
      derived: mergeRecordOfString(lineagePages.map((l) => l.derived ?? {})),
      merged: mergeRecordOfStringArray(lineagePages.map((l) => l.merged ?? {})),
      superseded: mergeRecordOfString(lineagePages.map((l) => l.superseded ?? {})),
      restarted: mergeRecordOfString(lineagePages.map((l) => l.restarted ?? {})),
      invalidated: mergeRecordOfString(lineagePages.map((l) => l.invalidated ?? {})),
      supersedes: mergeRecordOfStringArray(lineagePages.map((l) => l.supersedes ?? {})),
    },
  };

  const budget = options?.nodeBudget ?? RESEARCH_TYPED_GRAPH_CACHE_NODE_BUDGET;
  if (merged.nodes.length <= budget) return merged;

  const keepIds = selectTypedGraphNodeIdsWithinBudget(
    orderedNodeIds,
    budget,
    options?.pinNodeIds ?? [],
  );
  return filterTypedGraphToNodeIds(merged, keepIds);
}

function selectTypedGraphNodeIdsWithinBudget(
  orderedNodeIds: readonly string[],
  budget: number,
  pinNodeIds: readonly string[],
): Set<string> {
  const keep = new Set(pinNodeIds.filter(Boolean));
  for (let i = orderedNodeIds.length - 1; i >= 0 && keep.size < budget; i -= 1) {
    keep.add(orderedNodeIds[i]!);
  }
  for (const pin of pinNodeIds) {
    if (pin) keep.add(pin);
  }
  return keep;
}

function filterTypedGraphToNodeIds(
  graph: TypedGraphResponse,
  keepIds: Set<string>,
): TypedGraphResponse {
  const nodes = graph.nodes.filter((node) => keepIds.has(node.id));
  const edges = graph.edges.filter(
    (edge) => keepIds.has(edge.from_node_id) && keepIds.has(edge.to_node_id),
  );

  const clusterIds = new Set<string>();
  for (const node of nodes) {
    const clusterId = node.cluster_id?.trim();
    if (clusterId) clusterIds.add(clusterId);
  }
  const clusters = graph.clusters.filter((cluster) => {
    const id = cluster.id || cluster.name;
    return id ? clusterIds.has(id) : false;
  });

  const filterStringMap = (map: Record<string, string>) => {
    const out: Record<string, string> = {};
    for (const [key, value] of Object.entries(map)) {
      if (!keepIds.has(key)) continue;
      if (keepIds.has(value)) out[key] = value;
    }
    return out;
  };

  const filterStringArrayMap = (map: Record<string, string[]>) => {
    const out: Record<string, string[]> = {};
    for (const [key, values] of Object.entries(map)) {
      if (!keepIds.has(key)) continue;
      const filtered = values.filter((value) => keepIds.has(value));
      if (filtered.length > 0) out[key] = filtered;
    }
    return out;
  };

  return {
    ...graph,
    nodes,
    edges,
    clusters,
    lineage: {
      derived: filterStringMap(graph.lineage.derived ?? {}),
      merged: filterStringArrayMap(graph.lineage.merged ?? {}),
      superseded: filterStringMap(graph.lineage.superseded ?? {}),
      restarted: filterStringMap(graph.lineage.restarted ?? {}),
      invalidated: filterStringMap(graph.lineage.invalidated ?? {}),
      supersedes: filterStringArrayMap(graph.lineage.supersedes ?? {}),
    },
  };
}
