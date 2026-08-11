import { describe, expect, it } from "vitest";
import {
  EMPTY_STAR_GRAPH_FILTER,
  computeStarGraphVisibility,
  hasActiveStarGraphFilter,
} from "./star-graph-filter";
import type { StarGraphVisibility } from "./star-graph-filter";

/** A small typed graph with two rounds, two tiers, statuses and lineage. */
function sampleGraph() {
  return {
    nodes: [
      // round 2, xl, stable
      { id: "n1", level: "xl", round: 2, status: "done", cluster_id: "c1" },
      // round 2, m, running
      { id: "n2", level: "m", round: 2, status: "running", cluster_id: "c1" },
      // round 1, m, superseded (retired)
      { id: "n3", level: "m", round: 1, status: "done", cluster_id: null },
      // round 1, s, abandoned (retired by status)
      { id: "n4", level: "s", round: 1, status: "abandoned", cluster_id: null },
    ],
    edges: [
      { id: "e1", from_node_id: "n1", to_node_id: "n2" }, // both visible → visible
      { id: "e2", from_node_id: "n2", to_node_id: "n3" }, // n3 hidden → hidden
    ],
    lineage: {
      derived: {},
      merged: {},
      superseded: { n3: "n1" },
      restarted: {},
      invalidated: {},
      supersedes: { n1: ["n3"] },
    },
  };
}

describe("computeStarGraphVisibility (LRM-1497 · quick navigation)", () => {
  it("keeps everything on an empty (all-open) filter", () => {
    const v = computeStarGraphVisibility(sampleGraph(), EMPTY_STAR_GRAPH_FILTER);
    expect(v.hiddenNodeCount).toBe(0);
    expect(v.hiddenEdgeCount).toBe(0);
    expect(v.visibleNodeIds.size).toBe(4);
    expect(v.visibleEdgeIds.size).toBe(2);
  });

  it("filters by round", () => {
    const v = computeStarGraphVisibility(sampleGraph(), {
      ...EMPTY_STAR_GRAPH_FILTER,
      rounds: [2],
    });
    expect(v.visibleNodeIds).toEqual(new Set(["n1", "n2"]));
    expect(v.hiddenNodeCount).toBe(2);
    // e2 (n2→n3) is hidden because n3 is filtered out (dangling edge).
    expect(v.visibleEdgeIds).toEqual(new Set(["e1"]));
  });

  it("filters by level (tier)", () => {
    const v = computeStarGraphVisibility(sampleGraph(), {
      ...EMPTY_STAR_GRAPH_FILTER,
      levels: ["xl"],
    });
    expect(v.visibleNodeIds).toEqual(new Set(["n1"]));
    expect(v.hiddenNodeCount).toBe(3);
  });

  it("filters by status", () => {
    const v = computeStarGraphVisibility(sampleGraph(), {
      ...EMPTY_STAR_GRAPH_FILTER,
      statuses: ["running"],
    });
    expect(v.visibleNodeIds).toEqual(new Set(["n2"]));
  });

  it("filters by cluster (includes only members of that cluster)", () => {
    const v = computeStarGraphVisibility(sampleGraph(), {
      ...EMPTY_STAR_GRAPH_FILTER,
      clusterIds: ["c1"],
    });
    expect(v.visibleNodeIds).toEqual(new Set(["n1", "n2"]));
  });

  it("focuses a single cluster when focusClusterId set", () => {
    const v = computeStarGraphVisibility(sampleGraph(), {
      ...EMPTY_STAR_GRAPH_FILTER,
      focusClusterId: "c1",
    });
    expect(v.visibleNodeIds).toEqual(new Set(["n1", "n2"]));
  });

  it("validOnly retires superseded (lineage) and abandoned (status) nodes", () => {
    const v = computeStarGraphVisibility(sampleGraph(), {
      ...EMPTY_STAR_GRAPH_FILTER,
      validOnly: true,
    });
    // n3 superseded via lineage, n4 abandoned via status — both retired.
    expect(v.visibleNodeIds).toEqual(new Set(["n1", "n2"]));
    expect(v.hiddenNodeCount).toBe(2);
  });

  it("never mutates the input graph", () => {
    const graph = sampleGraph();
    const snapshot = JSON.stringify(graph);
    computeStarGraphVisibility(graph, {
      ...EMPTY_STAR_GRAPH_FILTER,
      validOnly: true,
      rounds: [2],
    });
    expect(JSON.stringify(graph)).toBe(snapshot);
  });
});

describe("hasActiveStarGraphFilter", () => {
  it("is false for an all-open filter", () => {
    expect(hasActiveStarGraphFilter(EMPTY_STAR_GRAPH_FILTER)).toBe(false);
  });

  it("is true when any axis hides something", () => {
    expect(hasActiveStarGraphFilter({ ...EMPTY_STAR_GRAPH_FILTER, rounds: [2] })).toBe(true);
    expect(hasActiveStarGraphFilter({ ...EMPTY_STAR_GRAPH_FILTER, validOnly: true })).toBe(true);
    expect(hasActiveStarGraphFilter({ ...EMPTY_STAR_GRAPH_FILTER, focusClusterId: "c1" })).toBe(true);
  });

  it("reports from a computed visibility result", () => {
    const v: StarGraphVisibility = {
      visibleNodeIds: new Set(),
      visibleEdgeIds: new Set(),
      hiddenNodeCount: 1,
      hiddenEdgeCount: 0,
    };
    expect(hasActiveStarGraphFilter(v)).toBe(true);
    expect(
      hasActiveStarGraphFilter({ ...v, hiddenNodeCount: 0, hiddenEdgeCount: 0 }),
    ).toBe(false);
  });
});
