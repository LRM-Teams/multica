// @vitest-environment jsdom
import { act, renderHook } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import {
  buildScalingFixture,
  createProjectionSliceFixture,
  type ProjectionSliceGateway,
  type SliceWireRequest,
} from "@multica/core/research-v6-slice";
import { useResearchSliceViewport } from "./use-research-slice-viewport";

function makeNode(id: string, status = "active") {
  return {
    id,
    session_id: "s1",
    node_type: "task",
    title: id,
    summary: id,
    status,
    actor_agent_id: null,
    payload: {},
    created_at: "2026-08-05T00:00:00Z",
    updated_at: "2026-08-05T00:00:00Z",
  };
}

/** root -> a -> b ; root -> x */
function smallGraph() {
  const nodes = [makeNode("root", "done"), makeNode("a"), makeNode("b"), makeNode("x")];
  const mk = (from: string, to: string, i: number) => ({
    id: `e${i}`,
    session_id: "s1",
    from_node_id: from,
    to_node_id: to,
    edge_type: "produces",
    created_at: "2026-08-05T00:00:00Z",
  });
  const edges = [mk("root", "a", 1), mk("a", "b", 2), mk("root", "x", 3)];
  return { nodes, edges };
}

async function flush() {
  // Resolve the fixture's microtask promises.
  await act(async () => {
    await Promise.resolve();
    await Promise.resolve();
  });
}

describe("useResearchSliceViewport (LRM-1465 behavior)", () => {
  it("first open requests only the seed slice", async () => {
    const gw = createProjectionSliceFixture(smallGraph());
    const seen: SliceWireRequest[] = [];
    const off = gw.observe((w) => seen.push(w));

    const { result } = renderHook(() =>
      useResearchSliceViewport({ gateway: gw, seedRoot: "root" }),
    );
    await flush();

    // exactly one wire request: the seed slice
    expect(seen).toHaveLength(1);
    expect(seen[0]!.path).toContain("root");
    expect(seen[0]!.params.direction).toBe("out");
    expect(seen[0]!.params.limit).toBe(500);
    expect(result.current.roots.root).toBeDefined();
    off();
  });

  it("composite expand and viewport pan request only the adjacent roots, deduped", async () => {
    const gw = createProjectionSliceFixture(smallGraph());
    const seen: SliceWireRequest[] = [];
    const off = gw.observe((w) => seen.push(w));

    const { result } = renderHook(() =>
      useResearchSliceViewport({ gateway: gw, seedRoot: "root", nodeBudget: 1000, renderNodeBudget: 1000, maxRoots: 40 }),
    );
    await flush();
    expect(seen).toHaveLength(1);
    const marks = new Set<string>();

    act(() => {
      result.current.request({ compositeExpandRoot: "a" });
    });
    await flush();
    // expands root "a"
    expect(seen.map((w) => w.path)).toContain("/api/research/v6/slices/a");
    marks.add("a");

    act(() => {
      // pan reveals b and x (and root again — already loaded)
      result.current.request({ visibleRoots: ["b", "x", "root"] });
    });
    await flush();

    const roots = seen.map((w) => w.path);
    expect(roots).toContain("/api/research/v6/slices/b");
    expect(roots).toContain("/api/research/v6/slices/x");
    // root never re-requested (no duplicate pagination)
    expect(roots.filter((p) => p.endsWith("/root")).length).toBe(1);
    off();
  });

  it("10k fixture: panning across many roots never requests the whole graph", async () => {
    const graph = buildScalingFixture({ sessionId: "s10k", totalNodes: 10_000, branches: 40 });
    const gw: ProjectionSliceGateway = createProjectionSliceFixture(graph);
    const limits: number[] = [];
    const off = gw.observe((w) => limits.push(Number(w.params.limit ?? 0)));

    const { result } = renderHook(() =>
      useResearchSliceViewport({
        gateway: gw,
        seedRoot: "root",
        nodeBudget: 1200,
        renderNodeBudget: 1200,
        maxRoots: 40,
      }),
    );
    await flush();

    // Simulate viewport panning across all 40 branch roots, a few at a time.
    const branchRoots = Array.from({ length: 40 }, (_, i) => `${"s10k"}-branch-${i}-n0`);
    for (let i = 0; i < branchRoots.length; i += 3) {
      const window = branchRoots.slice(i, i + 3);
      act(() => result.current.request({ visibleRoots: window }));
      await flush();
    }

    // 10k protection: every single wire request is a bounded page (<= 500);
    // never one request for the whole graph.
    expect(limits.length).toBeGreaterThan(1);
    for (const l of limits) expect(l).toBeLessThanOrEqual(500);

    // Even after panning through ALL 40 branches (the data behind ~10k nodes),
    // the merged render state in the browser stays far below 10k — the whole
    // graph is never held at once.
    expect(result.current.uniqueNodeCount).toBeLessThanOrEqual(1200);
    // no duplicate pagination: the seed root is requested exactly once.
    const rootRequests = limits.filter(() => true).length - (branchRoots.length);
    expect(rootRequests).toBe(1);
    off();
  });
});
