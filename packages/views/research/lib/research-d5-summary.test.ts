import { describe, expect, it } from "vitest";
import { summarizeTypedGraph } from "./research-d5-summary";

describe("summarizeTypedGraph", () => {
  it("surfaces totalDirections when more canonical nodes exist server-side", () => {
    const summary = summarizeTypedGraph(
      [{ id: "g1", level: "xxl", node_type: "goal", status: "active" }],
      { totalNodeCount: 10_042 },
    );
    expect(summary.loadedDirections).toBe(1);
    expect(summary.totalDirections).toBe(10_042);
  });

  it("omits totalDirections when the loaded page is complete", () => {
    const nodes = Array.from({ length: 40 }, (_, index) => ({
      id: `n${index}`,
      level: "s",
      status: "done",
      node_type: "finding",
    }));
    const summary = summarizeTypedGraph(nodes, { totalNodeCount: 40 });
    expect(summary.totalDirections).toBeNull();
  });
});
