import { describe, expect, it } from "vitest";
import type { TypedGraphResponse } from "@multica/core/research";
import { firstOrderNeighborIds } from "./typed-graph-neighborhood";

describe("firstOrderNeighborIds", () => {
  it("includes parent, children, merged inputs, and edge neighbors", () => {
    const typed = {
      session_id: "s1",
      graph_version: 1,
      nodes: [
        {
          id: "root",
          level: "origin",
          parent_id: null,
          child_ids: ["child"],
          merged_from: [],
        },
        {
          id: "child",
          level: "s",
          parent_id: "root",
          child_ids: [],
          merged_from: ["input"],
        },
        {
          id: "input",
          level: "s",
          parent_id: "root",
          child_ids: [],
          merged_from: [],
        },
        {
          id: "peer",
          level: "s",
          parent_id: "root",
          child_ids: [],
          merged_from: [],
        },
      ],
      edges: [{ from_node_id: "child", to_node_id: "peer", edge_type: "supports" }],
      clusters: [],
    } as unknown as TypedGraphResponse;

    const ids = firstOrderNeighborIds(typed, "child");
    expect([...ids].sort()).toEqual(["child", "input", "peer", "root"]);
  });
});
