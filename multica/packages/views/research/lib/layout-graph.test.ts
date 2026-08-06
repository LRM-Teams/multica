// @vitest-environment node
import { describe, expect, it } from "vitest";
import type { ResearchGraphEdge, ResearchGraphNode } from "@multica/core/types";
import { layoutResearchGraph } from "./layout-graph";
import { LOGIC_END_NODE_ID, laneForNode } from "./logic-lanes";

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
  it("places connected nodes left-to-right on role swimlanes", () => {
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
    const a = laid.nodes.find((n) => n.id === "n1")!;
    const b = laid.nodes.find((n) => n.id === "n2")!;
    const end = laid.nodes.find((n) => n.id === LOGIC_END_NODE_ID)!;
    expect(a.data.logicRole).toBe("start");
    expect(b.position.x).toBeGreaterThan(a.position.x);
    expect(end.position.x).toBeGreaterThan(b.position.x);
    expect(a.data.laneId).toBe("orchestrate");
    expect(b.data.laneId).toBe("source");
    expect(laid.nodes.some((n) => n.type === "laneBand")).toBe(true);
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
    expect(laid.nodes.some((n) => n.id === "n1")).toBe(true);
    expect(laid.edges.every((e) => e.target !== "missing")).toBe(true);
  });

  it("maps conflict nodes into the validate lane", () => {
    expect(laneForNode(node({ id: "c", node_type: "conflict", title: "C" }))).toBe("validate");
  });
});
