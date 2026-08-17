import { describe, expect, it } from "vitest";
import type {
  ResearchV6DirectorProjectionEdge,
  ResearchV6DirectorProjectionNode,
} from "@multica/core/types/research-v6-director";
import { adaptResearchV6DirectorCanvas } from "./director-session-adapter";

const RUN_ID = "00000000-0000-4000-8000-000000000003";

function node(
  id: string,
  tier: ResearchV6DirectorProjectionNode["tier"],
  overrides: Partial<ResearchV6DirectorProjectionNode> = {},
): ResearchV6DirectorProjectionNode {
  return {
    id,
    kind: tier === "S" ? "result_s" : "insight",
    tier,
    canonical_ref: { kind: tier === "S" ? "result" : "insight", id: RUN_ID },
    branch_ids: [],
    state: {
      execution: "succeeded",
      conclusion: "accepted",
      integration: "candidate",
    },
    catalog_summary: `summary ${id}`,
    absorbed: false,
    terminal: true,
    expandable: false,
    hidden_child_count: 0,
    updated_at: "2026-08-17T08:00:00Z",
    ...overrides,
  };
}

function edge(
  id: string,
  from: string,
  to: string,
  kind: ResearchV6DirectorProjectionEdge["kind"],
): ResearchV6DirectorProjectionEdge {
  return {
    id,
    kind,
    from_node_id: from,
    to_node_id: to,
    canonical: true,
    hidden_count: 0,
    expandable: false,
  };
}

describe("Director V6 canvas adapter", () => {
  it("copies server tiers and never promotes from title or counts", () => {
    const result = adaptResearchV6DirectorCanvas({
      runId: RUN_ID,
      eventSequence: 8,
      nodes: [
        node("small", "S", { title: "MASTER synthesis XXL" }),
        node("large", "XL"),
      ],
      edges: [],
    });
    expect(result.graph.nodes.map((item) => item.level)).toEqual(["s", "xl"]);
  });

  it("groups nodes into server-declared Branch territories", () => {
    const branchA = "00000000-0000-4000-8000-000000000101";
    const branchB = "00000000-0000-4000-8000-000000000102";
    const result = adaptResearchV6DirectorCanvas({
      runId: RUN_ID,
      eventSequence: 9,
      nodes: [
        node("one", "M", { branch_ids: [branchA] }),
        node("two", "L", { branch_ids: [branchB, branchA] }),
      ],
      edges: [],
    });

    expect(result.graph.nodes.find((item) => item.id === "one")?.cluster_id).toBe(
      branchA,
    );
    expect(result.graph.nodes.find((item) => item.id === "two")?.cluster_id).toBe(
      branchB,
    );
    expect(result.graph.clusters.map((cluster) => cluster.id)).toEqual([
      branchA,
      branchB,
    ]);
  });

  it("keeps the Goal tier canonical while using the top-size D5 presentation", () => {
    const result = adaptResearchV6DirectorCanvas({
      runId: RUN_ID,
      eventSequence: 8,
      nodes: [node("goal", "GOAL", { kind: "goal" })],
      edges: [],
    });
    expect(result.graph.nodes[0]?.level).toBe("xxl");
    expect(result.graph.nodes[0]?.payload).toMatchObject({ projection_tier: "GOAL" });
  });

  it("derives fusion presentation only from explicit absorbed_into edges", () => {
    const result = adaptResearchV6DirectorCanvas({
      runId: RUN_ID,
      eventSequence: 8,
      nodes: [node("input-a", "S"), node("input-b", "S"), node("successor", "M")],
      edges: [
        edge("a", "input-a", "successor", "absorbed_into"),
        edge("b", "input-b", "successor", "absorbed_into"),
      ],
    });
    expect(result.graph.nodes.find((item) => item.id === "successor")?.merged_from).toEqual([
      "input-a",
      "input-b",
    ]);
  });

  it("exposes only server-declared disclosure roots", () => {
    const result = adaptResearchV6DirectorCanvas({
      runId: RUN_ID,
      eventSequence: 8,
      nodes: [
        node("expand", "L", { expandable: true, hidden_child_count: 4 }),
        node("leaf", "M", { expandable: false, hidden_child_count: 0 }),
      ],
      edges: [],
    });
    expect([...result.expandableNodeIds]).toEqual(["expand"]);
    expect(result.hiddenChildCountByNodeId.get("expand")).toBe(4);
  });
});
