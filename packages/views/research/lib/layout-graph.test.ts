import { describe, expect, it } from "vitest";
import type { ResearchGraphEdge, ResearchGraphNode } from "@multica/core/types";
import { layoutResearchGraph } from "./layout-graph";

function node(partial: Partial<ResearchGraphNode> & Pick<ResearchGraphNode, "id" | "node_type" | "title">): ResearchGraphNode {
  return {
    session_id: "s1",
    summary: "",
    status: "active",
    actor_agent_id: null,
    payload: {},
    created_at: "2026-07-29T00:00:00Z",
    updated_at: "2026-07-29T00:00:00Z",
    ...partial,
  };
}

describe("layoutResearchGraph", () => {
  it("places connected nodes at distinct coordinates", () => {
    const nodes = [
      node({ id: "n1", node_type: "goal", title: "Goal" }),
      node({ id: "n2", node_type: "probe", title: "Probe" }),
    ];
    const edges: ResearchGraphEdge[] = [
      {
        id: "e1",
        session_id: "s1",
        from_node_id: "n1",
        to_node_id: "n2",
        edge_type: "leads_to",
        created_at: "2026-07-29T00:00:00Z",
      },
    ];
    const laid = layoutResearchGraph(nodes, edges);
    expect(laid.nodes).toHaveLength(2);
    expect(laid.edges).toHaveLength(1);
    const a = laid.nodes.find((n) => n.id === "n1")!;
    const b = laid.nodes.find((n) => n.id === "n2")!;
    expect(a.position.x !== b.position.x || a.position.y !== b.position.y).toBe(true);
    expect(b.position.y).toBeGreaterThan(a.position.y);
  });

  it("ignores edges that reference missing nodes", () => {
    const nodes = [node({ id: "n1", node_type: "goal", title: "Goal" })];
    const edges: ResearchGraphEdge[] = [
      {
        id: "e1",
        session_id: "s1",
        from_node_id: "n1",
        to_node_id: "missing",
        edge_type: "leads_to",
        created_at: "2026-07-29T00:00:00Z",
      },
    ];
    const laid = layoutResearchGraph(nodes, edges);
    expect(laid.nodes).toHaveLength(1);
    expect(laid.edges).toHaveLength(0);
  });
});
