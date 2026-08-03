// @vitest-environment node
import { describe, expect, it } from "vitest";
import type { ResearchGraphEdge, ResearchGraphNode } from "@multica/core/types";
import {
  crossLaneNeighbor,
  isForkPoint,
  layoutResearchGraph,
  mainChainNeighbor,
  mainChainOrder,
  parallelGroupAtRank,
  researchGraphRanks,
} from "./layout-graph";
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

function edge(
  id: string,
  from: string,
  to: string,
  edge_type: ResearchGraphEdge["edge_type"] = "leads_to",
): ResearchGraphEdge {
  return {
    id,
    session_id: "s1",
    from_node_id: from,
    to_node_id: to,
    edge_type,
    created_at: "2026-07-29T00:00:00Z",
  };
}

/** Goal → fork → two parallel probes → merge finding. */
function forkFixture() {
  const nodes = [
    node({ id: "goal", node_type: "goal", title: "Goal" }),
    node({ id: "fork", node_type: "stage_gate", title: "Fork" }),
    node({ id: "a", node_type: "probe", title: "Lane A", payload: { logic_lane: "source" } }),
    node({
      id: "b",
      node_type: "finding",
      title: "Lane B",
      payload: { logic_lane: "deep_read" },
    }),
    node({ id: "merge", node_type: "finding", title: "Merge" }),
  ];
  const edges = [
    edge("e1", "goal", "fork"),
    edge("e2", "fork", "a"),
    edge("e3", "fork", "b"),
    edge("e4", "a", "merge"),
    edge("e5", "b", "merge"),
  ];
  return { nodes, edges };
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

describe("keyboard nav helpers (LRM-1105 / 1102 semantics A)", () => {
  it("mainChainOrder follows leads_to spine left→right", () => {
    const { nodes, edges } = forkFixture();
    const order = mainChainOrder(nodes, edges);
    expect(order[0]).toBe("goal");
    expect(order.indexOf("fork")).toBeLessThan(order.indexOf("a"));
    expect(order.indexOf("fork")).toBeLessThan(order.indexOf("b"));
    expect(order.indexOf("merge")).toBeGreaterThan(order.indexOf("a"));
  });

  it("researchGraphRanks puts parallel branch heads on the same rank", () => {
    const { nodes, edges } = forkFixture();
    const ranks = researchGraphRanks(nodes, edges);
    expect(ranks.get("a")).toBe(ranks.get("b"));
    expect(ranks.get("fork")).toBeLessThan(ranks.get("a")!);
  });

  it("parallelGroupAtRank returns same-rank nodes ordered by lane", () => {
    const { nodes, edges } = forkFixture();
    const ranks = researchGraphRanks(nodes, edges);
    const rank = ranks.get("a")!;
    expect(parallelGroupAtRank(nodes, edges, rank)).toEqual(["a", "b"]);
  });

  it("isForkPoint is true only when leads_to out-degree ≥ 2", () => {
    const { nodes, edges } = forkFixture();
    expect(isForkPoint("fork", nodes, edges)).toBe(true);
    expect(isForkPoint("goal", nodes, edges)).toBe(false);
    expect(isForkPoint("a", nodes, edges)).toBe(false);
  });

  it("mainChainNeighbor moves along leads_to; at fork prefers current lane", () => {
    const { nodes, edges } = forkFixture();
    expect(mainChainNeighbor(nodes, edges, "goal", 1)).toBe("fork");
    expect(mainChainNeighbor(nodes, edges, "fork", 1, { preferLaneFrom: "a" })).toBe("a");
    expect(mainChainNeighbor(nodes, edges, "fork", 1, { preferLaneFrom: "b" })).toBe("b");
    expect(mainChainNeighbor(nodes, edges, "a", -1)).toBe("fork");
  });

  it("crossLaneNeighbor only works at fork points (semantics A)", () => {
    const { nodes, edges } = forkFixture();
    expect(crossLaneNeighbor(nodes, edges, "a", 1)).toBeNull();
    expect(crossLaneNeighbor(nodes, edges, "goal", 1)).toBeNull();
    // At fork: ↑↓ cycles outgoing branch heads ordered by lane
    expect(crossLaneNeighbor(nodes, edges, "fork", 1)).toBe("a");
    expect(crossLaneNeighbor(nodes, edges, "fork", 1, { activeBranchId: "a" })).toBe("b");
    expect(crossLaneNeighbor(nodes, edges, "fork", -1, { activeBranchId: "a" })).toBe("b");
  });
});
