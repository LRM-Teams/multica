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
  if (typed.success) return typed.data;

  const record = asRecord(raw);
  if (typeof record.id !== "string" || record.id === "") return null;

  const parsed = TypedGraphNodeSchema.safeParse({
    id: record.id,
    session_id: record.session_id,
    node_type: record.node_type,
    title: record.title,
    summary: record.summary,
    status: record.status,
    actor_agent_id: record.actor_agent_id,
    payload: record.payload,
    parent_id: record.parent_id,
    child_ids: record.child_ids,
    created_at: record.created_at,
    updated_at: record.updated_at,
  });
  return parsed.success ? parsed.data : null;
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

function mergeTypedNodes(existing: TypedGraphNode, incoming: TypedGraphNode): TypedGraphNode {
  return {
    ...existing,
    ...incoming,
    payload: {
      ...asRecord(existing.payload),
      ...asRecord(incoming.payload),
    },
    child_ids: incoming.child_ids?.length ? incoming.child_ids : existing.child_ids,
    merged_from: incoming.merged_from?.length ? incoming.merged_from : existing.merged_from,
  };
}

function upsertNodeInPage(page: TypedGraphResponse, node: TypedGraphNode): TypedGraphResponse {
  const idx = page.nodes.findIndex((entry) => entry.id === node.id);
  if (idx >= 0) {
    const nodes = page.nodes.slice();
    nodes[idx] = mergeTypedNodes(page.nodes[idx]!, node);
    return { ...page, nodes };
  }
  return { ...page, nodes: [...page.nodes, node] };
}

function upsertEdgeInPage(page: TypedGraphResponse, edge: TypedGraphEdge): TypedGraphResponse {
  const key = edge.id || `${edge.from_node_id}:${edge.to_node_id}:${edge.edge_type ?? ""}`;
  const exists = page.edges.some(
    (entry) => (entry.id || `${entry.from_node_id}:${entry.to_node_id}:${entry.edge_type ?? ""}`) === key,
  );
  if (exists) return page;
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
  const edgeList: TypedGraphEdge[] = [];
  if (patch.edge != null) {
    const edge = normalizeWsGraphEdge(patch.edge);
    if (edge) edgeList.push(edge);
  }
  if (Array.isArray(patch.edges)) {
    for (const raw of patch.edges) {
      const edge = normalizeWsGraphEdge(raw);
      if (edge) edgeList.push(edge);
    }
  }

  if (!node && edgeList.length === 0) {
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
        next = upsertNodeInPage(next, node);
      }
    }
    for (const edge of edgeList) {
      next = upsertEdgeInPage(next, edge);
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
