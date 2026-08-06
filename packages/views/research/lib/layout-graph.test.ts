// @vitest-environment node
import { describe, expect, it } from "vitest";
import type { ResearchGraphEdge, ResearchGraphNode } from "@multica/core/types";
import {
  crossLaneNeighbor,
  isForkPoint,
  layoutCardBoxes,
  layoutResearchGraph,
  mainChainNeighbor,
  mainChainOrder,
  parallelGroupAtRank,
  RESEARCH_NODE_HEIGHT,
  RESEARCH_NODE_WIDTH,
  researchGraphRanks,
} from "./layout-graph";
import { boxesOverlap, neighborByLane, neighborByRow } from "./git-topology";
import { buildPlanarFixture30 } from "./planar-fixture-30";
import { cardMenuItemsForNode } from "./card-menu-actions";
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

describe("layoutResearchGraph (planar git)", () => {
  it("places connected nodes top-to-bottom without stackOffset overlap", () => {
    const { nodes, edges } = buildPlanarFixture30();
    const small = {
      nodes: nodes.slice(0, 2),
      edges: edges.filter(
        (e) => e.from_node_id === "n0" && e.to_node_id === "n1",
      ),
    };
    const laid = layoutResearchGraph(small.nodes, small.edges);
    const a = laid.nodes.find((n) => n.id === "n0")!;
    const b = laid.nodes.find((n) => n.id === "n1")!;
    const end = laid.nodes.find((n) => n.id === LOGIC_END_NODE_ID)!;
    expect(a.data.logicRole).toBe("start");
    expect(b.position.y).toBeGreaterThan(a.position.y);
    expect(end.position.y).toBeGreaterThan(b.position.y);
    expect(laid.nodes.some((n) => n.type === "gitGutter")).toBe(true);
    expect(laid.nodes.some((n) => n.type === "laneBand")).toBe(false);
  });

  it("30/3/2 fixture: stable, no AABB overlap, card size band, hidden edges", () => {
    const { nodes, edges } = buildPlanarFixture30();
    expect(nodes).toHaveLength(30);
    const a = layoutResearchGraph(nodes, edges, { includeEnd: false });
    const b = layoutResearchGraph(nodes, edges, { includeEnd: false });
    const boxesA = layoutCardBoxes(a);
    const boxesB = layoutCardBoxes(b);
    expect(boxesA).toHaveLength(30);
    expect(boxesA.map((x) => `${x.id}:${x.x},${x.y}`)).toEqual(
      boxesB.map((x) => `${x.id}:${x.x},${x.y}`),
    );

    for (const box of boxesA) {
      expect(box.w).toBeGreaterThanOrEqual(220);
      expect(box.w).toBeLessThanOrEqual(260);
      expect(box.h).toBeGreaterThanOrEqual(68);
      expect(box.h).toBeLessThanOrEqual(88);
      expect(box.w).toBe(RESEARCH_NODE_WIDTH);
      expect(box.h).toBe(RESEARCH_NODE_HEIGHT);
    }

    for (let i = 0; i < boxesA.length; i++) {
      for (let j = i + 1; j < boxesA.length; j++) {
        expect(boxesOverlap(boxesA[i]!, boxesA[j]!)).toBe(false);
      }
    }

    const lanes = new Set(
      a.nodes.filter((n) => n.type === "research").map((n) => n.data.gitLane),
    );
    expect(lanes.size).toBeGreaterThanOrEqual(3);
    expect(a.edges.every((e) => e.hidden)).toBe(true);

    // Gutter segments present (branch lines, not through cards).
    const gutter = a.nodes.find((n) => n.type === "gitGutter");
    expect(gutter?.data.gutterSegments?.length).toBeGreaterThanOrEqual(2);
  });

  it("ignores edges that reference missing nodes", () => {
    const { nodes } = buildPlanarFixture30();
    const laid = layoutResearchGraph(
      [nodes[0]!],
      [
        {
          id: "e1",
          session_id: "s1",
          from_node_id: "n0",
          to_node_id: "missing",
          edge_type: "leads_to",
          created_at: "2026-08-03T00:00:00Z",
        },
      ],
      { includeEnd: false },
    );
    expect(laid.nodes.some((n) => n.id === "n0")).toBe(true);
    expect(laid.edges.every((e) => e.target !== "missing")).toBe(true);
  });

  it("maps conflict nodes into the validate lane", () => {
    expect(laneForNode(node({ id: "c", node_type: "conflict", title: "C" }))).toBe("validate");
  });
});

describe("git keyboard neighbors", () => {
  it("moves by row and lane", () => {
    const { nodes, edges } = buildPlanarFixture30();
    const laid = layoutResearchGraph(nodes, edges, { includeEnd: false });
    const topo = laid.topology;
    const down = neighborByRow(topo, "n0", 1);
    expect(down).toBe("n1");
    const up = neighborByRow(topo, "n1", -1);
    expect(up).toBe("n0");
    // From fork explore node, right should find verify branch
    const right = neighborByLane(topo, "n13", 1);
    expect(right).toBeTruthy();
    expect(topo.get(right!)!.lane).toBe(2);
  });
});

describe("cardMenuItemsForNode", () => {
  it("enables retry only on failed nodes; keeps missing APIs disabled with reasons", () => {
    const { nodes } = buildPlanarFixture30();
    const failed = nodes.find((n) => n.id === "n19")!;
    const ok = nodes.find((n) => n.id === "n0")!;
    const failItems = cardMenuItemsForNode(failed);
    const okItems = cardMenuItemsForNode(ok);
    expect(failItems.find((i) => i.id === "retry_failed")?.enabled).toBe(true);
    expect(okItems.find((i) => i.id === "retry_failed")?.enabled).toBe(false);
    expect(failItems.find((i) => i.id === "fork_from")?.enabled).toBe(false);
    expect(
      failItems.find((i) => i.id === "fork_from")?.disabledReason?.trim(),
    ).toBeTruthy();
    expect(failItems.find((i) => i.id === "reassign")?.enabled).toBe(false);
    expect(failItems.find((i) => i.id === "cancel_run")?.enabled).toBe(false);
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
