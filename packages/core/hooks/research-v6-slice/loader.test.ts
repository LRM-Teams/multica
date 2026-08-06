import { describe, expect, it } from "vitest";
import { SlicePageCache } from "./cache";
import type { ProjectionSliceGateway } from "./fixture";
import { SliceLoader } from "./loader";
import type { ProjectionSliceRequest, ProjectionSliceResponse } from "./types";

/** A gateway whose requests resolve on our command, to simulate slow/raced fetches. */
class DeferredGateway implements ProjectionSliceGateway {
  pending: Array<{
    req: ProjectionSliceRequest;
    signal: AbortSignal | null;
    resolve: (r: ProjectionSliceResponse) => void;
    reject: (e: unknown) => void;
  }> = [];
  aborted: string[] = [];
  autoResolve: boolean;

  constructor(autoResolve = false) {
    this.autoResolve = autoResolve;
  }

  request(req: ProjectionSliceRequest, options?: { signal?: AbortSignal }): Promise<ProjectionSliceResponse> {
    const signal = options?.signal ?? null;
    if (signal) {
      signal.addEventListener("abort", () => {
        this.aborted.push(`${req.root}@${req.cursor ?? ""}`);
        // Real `fetch` rejects with AbortError when its signal aborts; a
        // gateway must do the same so the loader sees cancellation.
        const idx = this.pending.findIndex((p) => p.req === req);
        if (idx >= 0) {
          const [p] = this.pending.splice(idx, 1);
          const pending = p!;
          const e = new Error("aborted");
          e.name = "AbortError";
          pending.reject(e);
        }
      });
    }
    return new Promise<ProjectionSliceResponse>((resolve, reject) => {
      this.pending.push({ req, signal, resolve, reject });
      if (this.autoResolve && signal && !signal.aborted) {
        this.resolveAll(makePage);
      }
    });
  }

  resolveAll(page: (req: ProjectionSliceRequest) => ProjectionSliceResponse): void {
    const batch = this.pending.splice(0);
    for (const p of batch) {
      if (!p.signal?.aborted) p.resolve(page(p.req));
    }
  }

  resolveFront(page: (req: ProjectionSliceRequest) => ProjectionSliceResponse): void {
    const p = this.pending.shift()!;
    if (!p.signal?.aborted) p.resolve(page(p.req));
  }

  observe(): () => void {
    return () => undefined;
  }
}

function makePage(req: ProjectionSliceRequest): ProjectionSliceResponse {
  const nodeIds = req.root === "root" ? ["a", "b"] : [req.root];
  return {
    snapshotId: "snap",
    throughEventSequence: 1,
    contentHash: `${req.root}-${req.limit}-${req.cursor ?? ""}`,
    nodes: nodeIds.map((id) => ({
      node: {
        id,
        session_id: "s1",
        node_type: "task",
        title: id,
        summary: "",
        status: "active",
        actor_agent_id: null,
        payload: {},
        created_at: "2026-08-05T00:00:00Z",
        updated_at: "2026-08-05T00:00:00Z",
      },
      discovery: { nodeId: id, unloadedNeighborCount: 0, unloadedDescendantCount: 0, canExpand: false },
    })),
    edges: [],
    hasMore: false,
    nextCursor: null,
    totalNodes: nodeIds.length,
    danglingCount: 0,
  };
}

function baseReq(root: string, cursor?: string): ProjectionSliceRequest {
  return { root, direction: "out", maxDepth: 8, limit: 500, status: null, importanceFloor: 0, relationTypes: null, cursor };
}

describe("SliceLoader (LRM-1465)", () => {
  it("coalesces concurrent identical page requests onto one wire request", async () => {
    const gw = new DeferredGateway();
    const loader = new SliceLoader({ cache: new SlicePageCache({ nodeBudget: 5000 }), gateway: gw });
    const p1 = loader.load(baseReq("root"));
    const p2 = loader.load(baseReq("root"));
    expect(gw.pending).toHaveLength(1);
    gw.resolveAll(makePage);
    const [r1, r2] = await Promise.all([p1, p2]);
    expect(r1.page.nodes.length).toBe(2);
    expect(r2.page.nodes.length).toBe(2);
  });

  it("cancels a superseded in-flight request for the same root", async () => {
    const gw = new DeferredGateway();
    const loader = new SliceLoader({ cache: new SlicePageCache({ nodeBudget: 5000 }), gateway: gw });
    // older page for root (cursor p0)
    const oldP = loader.load(baseReq("root", "p0"));
    // a newer page for the same root (p1) supersedes it
    loader.load(baseReq("root", "p1"));
    loader.cancelStale();
    expect(gw.aborted).toContain("root@p0");
    await expect(oldP).rejects.toMatchObject({ name: "AbortError" });
  });

  it("serves a cached page without a second wire request", async () => {
    const gw = new DeferredGateway(true);
    const loader = new SliceLoader({ cache: new SlicePageCache({ nodeBudget: 5000 }), gateway: gw });
    await loader.load(baseReq("root"));
    const wires = gw.pending.length;
    const again = await loader.load(baseReq("root"));
    expect(again.fromCache).toBe(true);
    expect(gw.pending.length).toBeLessThanOrEqual(wires);
  });

  it("keeps a stale result from being reported as the newest (token guard)", async () => {
    const gw = new DeferredGateway();
    const loader = new SliceLoader({ cache: new SlicePageCache({ nodeBudget: 5000 }), gateway: gw });
    // older page for root
    const stale = loader.load(baseReq("root", "p0"));
    // newer page for the SAME root supersedes it
    const fresh = loader.load(baseReq("root", "p1"));
    await gw.resolveAll(makePage);
    const [s, f] = await Promise.all([stale, fresh]);
    expect(loader.isLatest("root", s.token)).toBe(false);
    expect(loader.isLatest("root", f.token)).toBe(true);
  });
});
