import { describe, expect, it } from "vitest";
import { SlicePageCache, slicePageKey } from "./cache";
import type { ProjectionSliceRequest, ProjectionSliceResponse } from "./types";

function page(nodes: number[], hasMore: boolean): ProjectionSliceResponse {
  return {
    snapshotId: "snap",
    throughEventSequence: 1,
    contentHash: `h-${nodes.join("-")}`,
    nodes: nodes.map((n) => ({
      node: {
        id: `n${n}`,
        session_id: "s1",
        node_type: "task",
        title: `n${n}`,
        summary: "",
        status: "active",
        actor_agent_id: null,
        payload: {},
        created_at: "2026-08-05T00:00:00Z",
        updated_at: "2026-08-05T00:00:00Z",
      },
      discovery: { nodeId: `n${n}`, unloadedNeighborCount: 0, unloadedDescendantCount: 0, canExpand: false },
    })),
    edges: [],
    hasMore,
    nextCursor: hasMore ? "p1" : null,
    totalNodes: nodes.length,
    danglingCount: 0,
  };
}

function req(cursor?: string): ProjectionSliceRequest {
  return {
    root: "root",
    direction: "out",
    maxDepth: 8,
    limit: 500,
    status: null,
    importanceFloor: 0,
    relationTypes: null,
    cursor,
  };
}

describe("SlicePageCache (LRM-1465)", () => {
  it("enforces the retained-node budget", () => {
    const cache = new SlicePageCache({ nodeBudget: 1000 });
    for (let i = 0; i < 10; i += 1) {
      cache.set(`page-${i}`, page([i, i + 1, i + 2], true));
    }
    expect(cache.uniqueNodeCount()).toBeLessThanOrEqual(1000);
    expect(cache.getStats().entryCount).toBe(10);
  });

  it("evicts the least-recently-used entry first (LRU)", () => {
    const cache = new SlicePageCache({ nodeBudget: 1000, maxEntries: 3 });
    cache.set("a", page([1, 2, 3], false));
    cache.set("b", page([4, 5, 6], false));
    cache.set("c", page([7, 8, 9], false));
    // touch "a" to make it most recent
    cache.get("a");
    cache.set("d", page([10, 11, 12], false));
    // maxEntries=3 → oldest (b) evicted; a was touched so it stays
    expect(cache.get("a")).not.toBeNull();
    expect(cache.get("b")).toBeNull();
    expect(cache.get("c")).not.toBeNull();
    expect(cache.get("d")).not.toBeNull();
    expect(cache.getStats().evictions).toBeGreaterThanOrEqual(1);
  });

  it("keeps retained nodes within budget even when many pages are added", () => {
    const cache = new SlicePageCache({ nodeBudget: 150 });
    for (let i = 0; i < 200; i += 1) {
      cache.set(`p${i}`, page([i * 3, i * 3 + 1, i * 3 + 2], false));
      expect(cache.uniqueNodeCount()).toBeLessThanOrEqual(150);
    }
  });

  it("tracks hits and misses", () => {
    const cache = new SlicePageCache({ nodeBudget: 100 });
    cache.set(slicePageKey(req()), page([1, 2], false));
    expect(cache.get(slicePageKey(req()))).not.toBeNull();
    cache.get(slicePageKey(req("p1"))); // miss
    const stats = cache.getStats();
    expect(stats.hits).toBe(1); // the single successful get
    expect(stats.misses).toBe(1);
  });

  it("slicePageKey distinguishes all request fields", () => {
    expect(slicePageKey(req())).not.toBe(slicePageKey({ ...req(), cursor: "p0" }));
    expect(slicePageKey(req())).not.toBe(slicePageKey({ ...req(), direction: "in" }));
    expect(slicePageKey(req())).not.toBe(slicePageKey({ ...req(), maxDepth: 2 }));
    expect(slicePageKey(req())).not.toBe(
      slicePageKey({ ...req(), importanceFloor: 0.5 }),
    );
    expect(slicePageKey(req())).not.toBe(
      slicePageKey({ ...req(), relationTypes: ["produces"] }),
    );
    expect(slicePageKey(req())).not.toBe(slicePageKey({ ...req(), status: ["done"] }));
    expect(slicePageKey(req("p0"))).toBe(slicePageKey({ ...req(), cursor: "p0" }));
  });
});
