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
    canonicalRef: { kind: tier === "S" ? "result" : "insight", id: RUN_ID },
    branchIds: [],
    state: {
      execution: "succeeded",
      conclusion: "accepted",
      integration: "candidate",
    },
    catalogSummary: `summary ${id}`,
    absorbed: false,
    terminal: true,
    expandable: false,
    hiddenChildCount: 0,
    updatedAt: "2026-08-17T08:00:00Z",
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
    fromNodeId: from,
    toNodeId: to,
    canonical: true,
    hiddenCount: 0,
    expandable: false,
  };
}

describe("Director V6 canvas adapter", () => {
  it("degrades an unknown future tier to the neutral M visual", () => {
    const result = adaptResearchV6DirectorCanvas({
      runId: RUN_ID,
      eventSequence: 1,
      nodes: [node("future", "FUTURE_TIER")],
      edges: [],
    });

    expect(result.graph.nodes[0]?.level).toBe("m");
    expect(
      (result.graph.nodes[0]?.payload as { projection_tier?: string } | undefined)
        ?.projection_tier,
    ).toBe("FUTURE_TIER");
  });

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

  it("keeps run-scoped Agent identity and Work assignment edges visible", () => {
    const agentId = "00000000-0000-4000-8000-000000000201";
    const result = adaptResearchV6DirectorCanvas({
      runId: RUN_ID,
      eventSequence: 10,
      nodes: [
        node("agent-node", "S", {
          kind: "agent",
          canonicalRef: { kind: "agent", id: agentId },
          title: "Manus 技术研究员",
        }),
        node("work-node", "S", {
          kind: "work_s",
          canonicalRef: { kind: "work_item", id: RUN_ID },
          title: "核验 Manus 技术进展",
        }),
      ],
      edges: [edge("assignment", "work-node", "agent-node", "assigned_to")],
    });

    expect(
      result.graph.nodes.find((item) => item.id === "agent-node")
        ?.actor_agent_id,
    ).toBe(agentId);
    expect(result.graph.edges[0]).toMatchObject({
      from_node_id: "work-node",
      to_node_id: "agent-node",
      edge_type: "assigned_to",
    });
  });

  it("preserves completed results and idle or offline Agent execution states", () => {
    const result = adaptResearchV6DirectorCanvas({
      runId: RUN_ID,
      eventSequence: 11,
      nodes: [
        node("result", "S", {
          state: {
            execution: "succeeded",
            conclusion: "proposed",
            integration: "candidate",
          },
        }),
        node("idle-agent", "S", {
          kind: "agent",
          state: {
            execution: "idle",
            conclusion: "proposed",
            integration: "unmatched",
          },
        }),
        node("offline-agent", "S", {
          kind: "agent",
          state: {
            execution: "offline",
            conclusion: "proposed",
            integration: "unmatched",
          },
        }),
      ],
      edges: [],
    });

    expect(result.graph.nodes.map((item) => item.status)).toEqual([
      "succeeded",
      "idle",
      "offline",
    ]);
  });

  it("groups nodes into server-declared Branch territories", () => {
    const branchA = "00000000-0000-4000-8000-000000000101";
    const branchB = "00000000-0000-4000-8000-000000000102";
    const result = adaptResearchV6DirectorCanvas({
      runId: RUN_ID,
      eventSequence: 9,
      nodes: [
        node("one", "M", { branchIds: [branchA] }),
        node("two", "L", { branchIds: [branchB, branchA] }),
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

  it("keeps the Goal tier canonical while leaving room for a larger synthesis", () => {
    const result = adaptResearchV6DirectorCanvas({
      runId: RUN_ID,
      eventSequence: 8,
      nodes: [node("goal", "GOAL", { kind: "goal" })],
      edges: [],
    });
    expect(result.graph.nodes[0]?.level).toBe("l");
    expect(result.graph.nodes[0]?.payload).toMatchObject({
      projection_tier: "GOAL",
      semantic_role: "goal",
    });
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
        node("expand", "L", { expandable: true, hiddenChildCount: 4 }),
        node("leaf", "M", { expandable: false, hiddenChildCount: 0 }),
      ],
      edges: [],
    });
    expect([...result.expandableNodeIds]).toEqual(["expand"]);
    expect(result.hiddenChildCountByNodeId.get("expand")).toBe(4);
  });
});
