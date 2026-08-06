/**
 * Research V6 · Projection Slice protocol (LRM-1465 / FE-03).
 *
 * A "slice" is a bounded, cursor-paginated window over the canonical graph
 * projection. The browser never downloads the whole run: it asks for the slice
 * rooted at a node, in one direction, through a subset of relation types, up to
 * a max depth, optionally filtered by status and an importance floor, page by
 * page via a stable cursor. The response carries per-node unloaded-neighbor /
 * descendant counts so the viewport knows what can still be expanded.
 *
 * Contract source: docs/superpowers/plans/2026-08-05-autonomous-research-system.md
 * §7.2 "无限画布投影协议". The server-owned registry (FE-01) owns the canonical
 * node kinds / edge types; this module only needs the slice transport shape and
 * reuses the existing @multica/core graph node/edge payloads.
 */

import type {
  ResearchGraphEdge,
  ResearchGraphNode,
} from "../../types/research";

/** Slice traversal direction (canonical edge orientation). */
export const SLICE_DIRECTIONS = ["out", "in", "both"] as const;
export type SliceDirection = (typeof SLICE_DIRECTIONS)[number];

/** Relation (edge) type filter. Empty/absent = all relation types. */
export type SliceRelationFilter = readonly string[] | null;

/** Node status filter. Empty/absent = all statuses. */
export type SliceStatusFilter = readonly string[] | null;

/**
 * Bounded slice request. Every field participates in the cache key and in the
 * wire request, so identical requests are de-duplicated and never re-downloaded.
 */
export interface ProjectionSliceRequest {
  /** Canonical root node id (. "(run_id, entity_kind, entity_id)" id). */
  root: string;
  direction: SliceDirection;
  /** Relation (edge) types to traverse. null = all. */
  relationTypes?: SliceRelationFilter;
  /** Max edge hops from root (inclusive). 0 = root only. */
  maxDepth: number;
  /** Node status filter. null = all. */
  status?: SliceStatusFilter;
  /** Importance floor (0–1 inclusive). 0 = no floor. */
  importanceFloor: number;
  /** Page size. */
  limit: number;
  /** Stable page cursor from the previous slice; absent for the first page. */
  cursor?: string | null;
}

/** Per-node discovery metadata the viewport needs to decide next expansion. */
export interface SliceNodeDiscovery {
  nodeId: string;
  /** Unloaded neighbors in the requested direction, within the relation filter. */
  unloadedNeighborCount: number;
  /** Unloaded descendants count (bounded estimate; truncated at a cap). */
  unloadedDescendantCount: number;
  /** True when this node has more to expand that is not present in the loaded set. */
  canExpand: boolean;
}

/** One node as returned by a slice, wrapped with discovery metadata. */
export interface SliceNode {
  node: ResearchGraphNode;
  discovery: SliceNodeDiscovery;
}

/** One edge as returned by a slice. */
export interface SliceEdge {
  edge: ResearchGraphEdge;
}

/** A complete, stable, bounded slice page. */
export interface ProjectionSliceResponse {
  /** Snapshot the entire logical slice is fixed to. */
  snapshotId: string;
  /** Canonical event sequence this slice is fixed to. */
  throughEventSequence: number;
  /** Content hash of this slice page (garbage for correctness checks). */
  contentHash: string;
  nodes: SliceNode[];
  edges: SliceEdge[];
  /** True while more pages exist for this request. */
  hasMore: boolean;
  /** Opaque cursor for the next page; null when hasMore is false. */
  nextCursor: string | null;
  /** Absolute listing size the server would expose for this request (bounded). */
  totalNodes: number;
  /** Nodes in the loaded set referenced by edges but not returned (dangling). */
  danglingCount: number;
}

/** Summary diagnostics for a loaded slice bundle. */
export interface SliceLoadStats {
  requestedPages: number;
  requestedNodes: number;
  cachedHits: number;
  aborted: number;
}

/** Loading phase of a root / slice bundle. */
export type SliceLoadPhase = "idle" | "loading" | "success" | "error";

/** A completed slice load (wire or cached). */
export interface SliceLoadResult {
  page: ProjectionSliceResponse;
  /** True when served from the page cache without a wire request. */
  fromCache: boolean;
  /** Monotonic token; lower = older/stale. */
  token: number;
}
