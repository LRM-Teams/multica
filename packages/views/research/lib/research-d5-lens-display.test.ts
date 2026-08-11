import { describe, expect, it } from "vitest";
import type { TypedGraphResponse } from "@multica/core/research";
import { buildStarCanvasViewModel } from "../star-graph";
import { buildD5LensDisplayHints, isResearchD5Lens } from "./research-d5-lens-display";

const typed = {
  session_id: "s1",
  graph_version: 1,
  nodes: [
    { id: "high", title: "High confidence", level: "l", confidence: 90 },
    { id: "low", title: "Low confidence", level: "l", confidence: 20 },
    { id: "unknown", title: "Unknown confidence", level: "m", confidence: null },
    { id: "agent", title: "Agent task", level: "s", actor_agent_id: "a1" },
    { id: "merge", title: "Merged", level: "xl", merged_from: ["high", "low"] },
  ],
  edges: [
    { id: "e1", from_node_id: "high", to_node_id: "merge", edge_type: "merged_from" },
    { id: "e2", from_node_id: "agent", to_node_id: "high", edge_type: "supports" },
  ],
  clusters: [],
  lineage: {
    merged: { merge: ["high", "low"] },
    derived: {},
    restarted: {},
    superseded: {},
    invalidated: {},
    supersedes: {},
  },
} as unknown as TypedGraphResponse;

const model = buildStarCanvasViewModel({
  nodes: typed.nodes,
  edges: typed.edges,
  seed: typed.graph_version,
  version: "test",
});

describe("buildD5LensDisplayHints", () => {
  it("returns empty hints for relations lens", () => {
    const hints = buildD5LensDisplayHints("relations", typed, model);
    expect(hints.dimmedNodeIds.size).toBe(0);
  });

  it("dims unknown confidence without ranking it as zero", () => {
    const hints = buildD5LensDisplayHints("confidence", typed, model);
    expect(hints.emphasizedNodeIds.has("high")).toBe(true);
    expect(hints.dimmedNodeIds.has("unknown")).toBe(true);
  });
});

describe("isResearchD5Lens", () => {
  it("accepts known lens ids", () => {
    expect(isResearchD5Lens("confidence")).toBe(true);
    expect(isResearchD5Lens("nope")).toBe(false);
  });
});
