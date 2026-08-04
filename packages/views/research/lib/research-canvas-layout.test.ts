// @vitest-environment node
import { describe, expect, it } from "vitest";
import type { ResearchGraphEdge, ResearchGraphNode } from "@multica/core/types";
import { layoutResearchCanvas } from "./research-canvas-layout";

const timestamp = "2026-08-04T00:00:00Z";

function node(
  id: string,
  parentId: string | null,
  childIds: string[],
): ResearchGraphNode {
  return {
    id,
    session_id: "canvas-layout-fixture",
    node_type: id === "root" ? "goal" : "finding",
    title: id,
    summary: "",
    status: "active",
    actor_agent_id: null,
    payload: {},
    parent_id: parentId,
    child_ids: childIds,
    child_count: childIds.length,
    descendant_count: childIds.length,
    theme_key: `theme:${id}`,
    assessment: "pending_review",
    created_at: timestamp,
    updated_at: timestamp,
  };
}

function edge(from: string, to: string): ResearchGraphEdge {
  return {
    id: `${from}-${to}`,
    session_id: "canvas-layout-fixture",
    from_node_id: from,
    to_node_id: to,
    edge_type: "leads_to",
    created_at: timestamp,
  };
}

describe("layoutResearchCanvas (LRM-1295)", () => {
  it("renders the server-projected aggregate window and retains topology for keyboard navigation", () => {
    const nodes = [
      node("root", null, ["branch-a", "branch-b"]),
      node("branch-a", "root", ["leaf-a"]),
      node("branch-b", "root", []),
      node("leaf-a", "branch-a", []),
    ];
    const edges = [
      edge("root", "branch-a"),
      edge("root", "branch-b"),
      edge("branch-a", "leaf-a"),
    ];

    const result = layoutResearchCanvas(nodes, edges);

    expect(result.mode).toBe("aggregate");
    expect(result.layout.nodes.map((item) => item.id)).toEqual([
      "root",
      "branch-a",
      "branch-b",
      "leaf-a",
    ]);
    expect(result.layout.nodes.map((item) => item.data.aggregateTier)).toEqual([
      "parent",
      "sibling",
      "sibling",
      "child",
    ]);
    expect(result.topology.get("leaf-a")?.branchId).toBeTruthy();
  });

  it("keeps the existing Git layout when the server omits the structural projection", () => {
    const incomplete = { ...node("root", null, []), child_ids: undefined };
    const result = layoutResearchCanvas([incomplete], []);

    expect(result.mode).toBe("git");
    expect(result.layout.nodes.some((item) => item.id === "git-gutter")).toBe(true);
  });
});
