import { describe, expect, it } from "vitest";
import type { TypedGraphNode } from "@multica/core/research";
import { buildD5FilterOptions } from "./research-d5-filter-options";

describe("buildD5FilterOptions", () => {
  it("collects unique statuses, tiers, rounds and clusters", () => {
    const nodes = [
      { id: "a", status: "running", level: "s", round: 2, cluster_id: "cost" },
      { id: "b", status: "done", level: "l", round: 1, cluster_id: "regulatory" },
      { id: "c", status: "running", level: "m", round: 2, cluster_id: "cost" },
    ] as TypedGraphNode[];

    const options = buildD5FilterOptions(nodes);
    expect(options.statuses).toEqual(["done", "running"]);
    expect(options.tiers).toEqual(["l", "m", "s"]);
    expect(options.rounds).toEqual(["1", "2"]);
    expect(options.clusters).toEqual(["cost", "regulatory"]);
  });

  it("returns empty facets for an empty graph", () => {
    expect(buildD5FilterOptions([])).toEqual({
      statuses: [],
      tiers: [],
      rounds: [],
      clusters: [],
    });
  });
});
