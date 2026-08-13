import type { InfiniteData, QueryClient } from "@tanstack/react-query";
import type { ResearchGraphEdge, ResearchGraphNode } from "../types/research";
import {
  TypedGraphEdgeSchema,
  TypedGraphNodeSchema,
  type TypedGraphEdge,
  type TypedGraphNode,
  type TypedGraphResponse,
} from "./graph-typed";
import { researchKeys } from "./queries";

export interface TypedGraphWsPatch {
  node?: unknown;
  edge?: unknown;
  edges?: unknown;
  graphVersion?: number;
}

export interface TypedGraphPatchResult {
  patched: boolean;
  needsResync: boolean;
  data?: InfiniteData<TypedGraphResponse>;
}

function asRecord(value: unknown): Record<string, unknown> {
  return value && typeof value === "object" && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : {};
}

/** Normalize a WS graph node into the typed graph node shape (never invent fields). */
export function normalizeWsGraphNode(raw: unknown): TypedGraphNode | null {
  const typed = TypedGraphNodeSchema.safeParse(raw);
  return typed.success ? typed.data : null;
}

function normalizeWsGraphEdge(raw: unknown): TypedGraphEdge | null {
  const typed = TypedGraphEdgeSchema.safeParse(raw);
  if (typed.success) return typed.data;

  const record = asRecord(raw);
  const from = record.from_node_id ?? record.from;
  const to = record.to_node_id ?? record.to;
  if (typeof from !== "string" || typeof to !== "string") return null;

  const parsed = TypedGraphEdgeSchema.safeParse({
    id: record.id,
    session_id: record.session_id,
    from_node_id: from,
    to_node_id: to,
    edge_type: record.edge_type ?? record.relation,
    created_at: record.created_at,
  });
  return parsed.success ? parsed.data : null;
}

const PATCHABLE_NODE_FIELDS = [
  "session_id",
  "node_type",
  "title",
  "summary",
  "status",
  "actor_agent_id",
  "level",
  "round",
  "cluster_id",
  "confidence",
  "document_count",
  "conclusion_count",
  "goal_version_id",
  "derived_from",
  "merged_from",
  "superseded_by",
  "restart_of",
  "invalidated_by",
  "superseded_at",
  "invalidated_at",
  "parent_id",
  "child_ids",
  "children_of",
  "created_at",
  "updated_at",
] as const satisfies readonly (keyof TypedGraphNode)[];

function mergeTypedNodes(
  existing: TypedGraphNode,
  incoming: TypedGraphNode,
  raw: unknown,
): TypedGraphNode {
  const patch = asRecord(raw);
  const merged = { ...existing };
  for (const field of PATCHABLE_NODE_FIELDS) {
    if (patch[field] !== undefined) {
      Object.assign(merged, { [field]: incoming[field] });
    }
  }
  if (patch.payload !== undefined) {
    merged.payload =
      patch.payload === null
        ? null
        : {
            ...asRecord(existing.payload),
            ...asRecord(incoming.payload),
          };
  }
  return merged;
}

function upsertNodeInPage(
  page: TypedGraphResponse,
  node: TypedGraphNode,
  raw: unknown,
): TypedGraphResponse {
  const idx = page.nodes.findIndex((entry) => entry.id === node.id);
  if (idx >= 0) {
    const nodes = page.nodes.slice();
    nodes[idx] = mergeTypedNodes(page.nodes[idx]!, node, raw);
    return { ...page, nodes };
  }
  return { ...page, nodes: [...page.nodes, node] };
}

function upsertEdgeInPage(
  page: TypedGraphResponse,
  edge: TypedGraphEdge,
  raw: unknown,
): TypedGraphResponse {
  const key = edge.id || `${edge.from_node_id}:${edge.to_node_id}:${edge.edge_type ?? ""}`;
  const index = page.edges.findIndex(
    (entry) => (entry.id || `${entry.from_node_id}:${entry.to_node_id}:${entry.edge_type ?? ""}`) === key,
  );
  if (index >= 0) {
    const patch = asRecord(raw);
    const existing = page.edges[index]!;
    const edges = page.edges.slice();
    edges[index] = {
      ...existing,
      ...(patch.session_id !== undefined ? { session_id: edge.session_id } : {}),
      ...(patch.from_node_id !== undefined || patch.from !== undefined
        ? { from_node_id: edge.from_node_id }
        : {}),
      ...(patch.to_node_id !== undefined || patch.to !== undefined
        ? { to_node_id: edge.to_node_id }
        : {}),
      ...(patch.edge_type !== undefined || patch.relation !== undefined
        ? { edge_type: edge.edge_type }
        : {}),
      ...(patch.created_at !== undefined ? { created_at: edge.created_at } : {}),
    };
    return { ...page, edges };
  }
  return { ...page, edges: [...page.edges, edge] };
}

export function patchTypedGraphInfiniteData(
  data: InfiniteData<TypedGraphResponse> | undefined,
  patch: TypedGraphWsPatch,
): TypedGraphPatchResult {
  if (!data || data.pages.length === 0) {
    return { patched: false, needsResync: true };
  }

  const node = patch.node != null ? normalizeWsGraphNode(patch.node) : null;
  const edgePatches: Array<{ edge: TypedGraphEdge; raw: unknown }> = [];
  if (patch.edge != null) {
    const edge = normalizeWsGraphEdge(patch.edge);
    if (edge) edgePatches.push({ edge, raw: patch.edge });
  }
  if (Array.isArray(patch.edges)) {
    for (const raw of patch.edges) {
      const edge = normalizeWsGraphEdge(raw);
      if (edge) edgePatches.push({ edge, raw });
    }
  }

  if (!node && edgePatches.length === 0) {
    return { patched: false, needsResync: false };
  }

  const currentVersion = Math.max(...data.pages.map((page) => page.graph_version ?? 0));
  if (
    patch.graphVersion != null &&
    Number.isFinite(patch.graphVersion) &&
    patch.graphVersion > currentVersion + 1
  ) {
    return { patched: false, needsResync: true };
  }

  const pages = data.pages.map((page, index) => {
    let next = page;
    if (node) {
      const onPage = page.nodes.some((entry) => entry.id === node.id);
      if (onPage || index === 0) {
        next = upsertNodeInPage(next, node, patch.node);
      }
    }
    for (const { edge, raw } of edgePatches) {
      next = upsertEdgeInPage(next, edge, raw);
    }
    if (patch.graphVersion != null && Number.isFinite(patch.graphVersion)) {
      next = { ...next, graph_version: Math.max(next.graph_version ?? 0, patch.graphVersion) };
    }
    return next;
  });

  return {
    patched: true,
    needsResync: false,
    data: { ...data, pages },
  };
}

export function applyTypedGraphWsPatch(
  qc: QueryClient,
  wsId: string,
  sessionId: string,
  patch: TypedGraphWsPatch,
): TypedGraphPatchResult {
  const key = researchKeys.graphTypedInfinite(wsId, sessionId);
  let result: TypedGraphPatchResult = { patched: false, needsResync: false };

  qc.setQueryData<InfiniteData<TypedGraphResponse>>(key, (prev) => {
    const graphVersionRaw =
      patch.graphVersion ??
      (typeof asRecord(patch.node).graph_version === "number"
        ? (asRecord(patch.node).graph_version as number)
        : undefined);
    const applied = patchTypedGraphInfiniteData(prev, {
      ...patch,
      graphVersion: graphVersionRaw,
    });
    result = applied;
    return applied.data ?? prev;
  });

  return result;
}

/** Upsert a snapshot node into typed infinite cache when the WS payload is legacy-shaped. */
export function snapshotNodeToTypedPatch(node: ResearchGraphNode): TypedGraphNode | null {
  return normalizeWsGraphNode(node);
}

export function snapshotEdgeToTypedPatch(edge: ResearchGraphEdge): TypedGraphEdge | null {
  return normalizeWsGraphEdge(edge);
}
