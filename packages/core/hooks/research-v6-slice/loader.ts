/**
 * Research V6 · Slice loader (LRM-1465 / FE-03).
 *
 * Framework-agnostic engine behind `useResearchSlice` and the viewport hook.
 * Responsibilities:
 *   - coalesce concurrent identical slice requests (one wire request per page);
 *   - honour the cache (a cached page never re-downloads);
 *   - keep an AbortController per in-flight request so a superseded viewport
 *     request is cancelled before landing;
 *   - guard against stale writes: each call gets a monotonic token, and a
 *     result is only published if its token is still the newest for its root.
 *
 * The 10k protection guarantee is enforced here: a SliceLoader never asks a
 * gateway for more than one `limit`-bounded page at a time, never requests the
 * whole graph, and merges pages into a caller-supplied bounded cache.
 */

import { SlicePageCache, slicePageKey } from "./cache";
import type { ProjectionSliceGateway } from "./fixture";
import type { ProjectionSliceRequest } from "./types";
import type { SliceLoadResult } from "./types";

export interface LoaderOptions {
  /** Shared page cache (bounded). */
  cache: SlicePageCache;
  /** Gateway serving pages (fixture or production adapter). */
  gateway: ProjectionSliceGateway;
}

interface Inflight {
  controller: AbortController;
  promise: Promise<SliceLoadResult>;
  token: number;
}

export class SliceLoader {
  private cache: SlicePageCache;
  private gateway: ProjectionSliceGateway;
  private inflight = new Map<string, Inflight>();
  private lastToken = 0;
  /** Latest published token per root — stale results with lower tokens are dropped. */
  private latestByRoot = new Map<string, number>();

  constructor(options: LoaderOptions) {
    this.cache = options.cache;
    this.gateway = options.gateway;
  }

  /**
   * Request one slice page (root+cursor). Returns immediately from cache when
   * present; otherwise fires a single wire request and coalesces concurrent
   * duplicates. Pass `expectFresh` when the caller wants a guaranteed fresh
   * fetch (bypasses cache) — used by explicit reload only.
   */
  load(req: ProjectionSliceRequest, opts?: { expectFresh?: boolean }): Promise<SliceLoadResult> {
    const key = slicePageKey(req);
    // Every load gets a fresh monotonic token and marks itself the latest for
    // its root, so a superseded result can be detected by the caller.
    const token = ++this.lastToken;
    this.latestByRoot.set(req.root, token);

    if (!opts?.expectFresh) {
      const cached = this.cache.get(key);
      if (cached) {
        return Promise.resolve({ page: cached.page, fromCache: true, token });
      }
    }

    const existing = this.inflight.get(key);
    if (existing && !opts?.expectFresh) {
      // Coalesce concurrent identical page requests onto one wire request, but
      // resolve with THIS caller's token so its own staleness is checked.
      return existing.promise.then((r) => ({ ...r, token }));
    }

    const controller = new AbortController();
    const promise = this.gateway
      .request(req, { signal: controller.signal })
      .then((page) => {
        this.inflight.delete(key);
        this.cache.set(key, page);
        return { page, fromCache: false, token } satisfies SliceLoadResult;
      })
      .catch((err) => {
        this.inflight.delete(key);
        throw err;
      });

    this.inflight.set(key, { controller, promise, token });
    return promise;
  }

  /** True when `token` is still the newest published token for `root`. */
  isLatest(root: string, token: number): boolean {
    return this.latestByRoot.get(root) === token;
  }

  /** Cancel all in-flight requests whose token is no longer the latest for their root. */
  cancelStale(): void {
    for (const [key, inf] of this.inflight) {
      const req = keyToRoot(key);
      if (req !== null && this.latestByRoot.get(req) !== inf.token) {
        inf.controller.abort();
        this.inflight.delete(key);
      }
    }
  }

  /** Cancel a specific root's in-flight requests. */
  cancelRoot(root: string): void {
    for (const [key, inf] of this.inflight) {
      if (keyToRoot(key) === root) {
        inf.controller.abort();
        this.inflight.delete(key);
      }
    }
  }

  cancelAll(): void {
    for (const inf of this.inflight.values()) inf.controller.abort();
    this.inflight.clear();
  }

  /** Number of distinct wire requests currently in flight. */
  inflightCount(): number {
    return this.inflight.size;
  }

  /** Cache statistics (unique retained nodes, hits, evictions, …). */
  getStats(): {
    uniqueNodeCount: number;
    entryCount: number;
    hits: number;
    misses: number;
    evictions: number;
  } {
    return this.cache.getStats();
  }
}

/** Extract the root id (first field) from a canonical slice page key. */
function keyToRoot(key: string): string | null {
  const root = key.split("|")[0];
  return root && root.length ? root : null;
}
