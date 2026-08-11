import { describe, expect, it } from "vitest";
import { summarizeTypedGraph } from "./research-d5-summary";
import { testTypedCluster, testTypedNode } from "./test-typed-graph-node";

describe("summarizeTypedGraph", () => {
  it("surfaces totalDirections when more canonical nodes exist server-side", () => {
    const summary = summarizeTypedGraph(
      [testTypedNode({ id: "g1", level: "xxl", node_type: "goal", status: "active" })],
      { totalNodeCount: 10_042 },
    );
    expect(summary.loadedDirections).toBe(1);
    expect(summary.totalDirections).toBe(10_042);
  });

  it("omits totalDirections when the loaded page is complete", () => {
    const nodes = Array.from({ length: 40 }, (_, index) =>
      testTypedNode({
        id: `n${index}`,
        level: "s",
        status: "done",
        node_type: "finding",
      }),
    );
    const summary = summarizeTypedGraph(nodes, { totalNodeCount: 40 });
    expect(summary.totalDirections).toBeNull();
  });

  it("counts new frontiers only from server cluster metadata", () => {
    const summary = summarizeTypedGraph(
      [testTypedNode({ id: "n1", level: "l", node_type: "finding", cluster_id: null })],
      {
        clusters: [
          testTypedCluster({ id: "c1", cluster_type: "new_frontier", label: "New area" }),
          testTypedCluster({ id: "c2", cluster_type: "topic", label: "Topic" }),
        ],
      },
    );
    expect(summary.newFrontiers).toBe(1);
  });
});
