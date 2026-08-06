/**
 * Research V6 · Slice page cache (LRM-1465 / FE-03).
 *
 * The browser must never hold the whole 10k-node graph, and repeated slice
 * requests must not re-download the same page. Cache entries (one per slice
 * page request) are bounded by a hard node budget with LRU eviction so memory
 * stays flat during long viewport panning and composite-node expansion.
 */

import type { ProjectionSliceRequest, ProjectionSliceResponse } from "./types";

export interface SliceCacheEntry {
  key: string;
  page: ProjectionSliceResponse;
  /** Nodes retained by this page. */
  nodeCount: number;
  /** LRU recency counter. */
  lastUsed: number;
}

export interface SliceCacheOptions {
  /** Retained-node budget. */
  nodeBudget: number;
  /** Hard entry cap regardless of node budget. */
  maxEntries?: number;
}

export interface SliceCacheStats {
  uniqueNodeCount: number;
  entryCount: number;
  hits: number;
  misses: number;
  evictions: number;
}

/**
 * Canonical key for a slice page: all request fields participate so two pages
 * with different filters are distinct even if the root/limit match.
 */
export function slicePageKey(req: ProjectionSliceRequest): string {
  const rel = req.relationTypes?.slice().sort().join(",") ?? "*";
  const st = req.status?.slice().sort().join(",") ?? "*";
  return [
    req.root,
    req.direction,
    rel,
    req.maxDepth,
    st,
    req.importanceFloor,
    req.limit,
    req.cursor ?? "",
  ].join("|");
}

export class SlicePageCache {
  private map = new Map<string, SliceCacheEntry>();
  private nodeBudget: number;
  private maxEntries: number;
  private clock = 0;
  private stats: SliceCacheStats = {
    uniqueNodeCount: 0,
    entryCount: 0,
    hits: 0,
    misses: 0,
    evictions: 0,
  };

  constructor(options: SliceCacheOptions) {
    this.nodeBudget = Math.max(1, Math.floor(options.nodeBudget));
    this.maxEntries = Math.max(1, Math.floor(options.maxEntries ?? 250));
  }

  get(key: string): SliceCacheEntry | null {
    const entry = this.map.get(key);
    if (!entry) {
      this.stats.misses += 1;
      return null;
    }
    this.stats.hits += 1;
    entry.lastUsed = this.clock++;
    this.map.delete(key);
    this.map.set(key, entry);
    return entry;
  }

  has(key: string): boolean {
    return this.map.has(key);
  }

  set(key: string, page: ProjectionSliceResponse): void {
    const nodeCount = countUniqueNodes(page);
    const existing = this.map.get(key);
    if (existing) {
      const delta = nodeCount - existing.nodeCount;
      existing.page = page;
      existing.nodeCount = nodeCount;
      existing.lastUsed = this.clock++;
      this.stats.uniqueNodeCount += delta;
      return;
    }
    this.clock += 1;
    this.map.set(key, {
      key,
      page,
      nodeCount,
      lastUsed: this.clock,
    });
    this.stats.uniqueNodeCount += nodeCount;
    this.stats.entryCount = this.map.size;
    this.evict();
  }

  /** Total unique nodes currently retained. */
  uniqueNodeCount(): number {
    return this.stats.uniqueNodeCount;
  }

  getStats(): SliceCacheStats {
    return { ...this.stats, entryCount: this.map.size, uniqueNodeCount: this.stats.uniqueNodeCount };
  }

  clear(): void {
    this.map.clear();
    this.stats.uniqueNodeCount = 0;
    this.stats.entryCount = 0;
    this.stats.evictions = 0;
  }

  /** LRU eviction until under both the node budget and the entry cap. */
  private evict(): void {
    let guard = 0;
    while (
      (this.stats.uniqueNodeCount > this.nodeBudget || this.map.size > this.maxEntries) &&
      this.map.size > 0 &&
      guard < 10_000
    ) {
      guard += 1;
      let lruKey: string | null = null;
      let lruUsed = Infinity;
      for (const [k, v] of this.map) {
        if (v.lastUsed < lruUsed) {
          lruUsed = v.lastUsed;
          lruKey = k;
        }
      }
      if (lruKey === null) break;
      const victim = this.map.get(lruKey)!;
      this.map.delete(lruKey);
      this.stats.uniqueNodeCount -= victim.nodeCount;
      this.stats.evictions += 1;
    }
    this.stats.entryCount = this.map.size;
  }
}

function countUniqueNodes(page: ProjectionSliceResponse): number {
  return page.nodes.length;
}

/** Serialize a request into a wire query string (for the production adapter). */
export function sliceQueryString(req: ProjectionSliceRequest): string {
  const p: Record<string, string> = {
    direction: req.direction,
    max_depth: String(req.maxDepth),
    importance_floor: String(req.importanceFloor),
    limit: String(req.limit),
  };
  if (req.relationTypes?.length) p.relation_types = req.relationTypes.join(",");
  if (req.status?.length) p.status = req.status.join(",");
  if (req.cursor) p.cursor = req.cursor;
  return Object.entries(p)
    .map(([k, v]) => `${encodeURIComponent(k)}=${encodeURIComponent(v)}`)
    .join("&");
}
