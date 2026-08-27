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
    kind: tier === "GOAL" ? "goal" : tier === "S" ? "result_s" : "insight",
    tier,
    canonicalRef: {
      kind: tier === "GOAL" ? "goal" : tier === "S" ? "result" : "insight",
      id: RUN_ID,
    },
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

  it("shows Agent satellites while the constellation is still staffing", () => {
    const directorId = "00000000-0000-4000-8000-000000000210";
    const researcherId = "00000000-0000-4000-8000-000000000211";
    const result = adaptResearchV6DirectorCanvas({
      runId: RUN_ID,
      eventSequence: 3,
      nodes: [
        node("goal", "GOAL", { kind: "goal" }),
        node("director", "S", {
          kind: "agent",
          canonicalRef: { kind: "agent", id: directorId },
          title: "Ronaldo",
          state: {
            execution: "idle",
            conclusion: "proposed",
            integration: "unmatched",
          },
        }),
        node("researcher", "S", {
          kind: "agent",
          canonicalRef: { kind: "agent", id: researcherId },
          title: "市场研究员",
          state: {
            execution: "offline",
            conclusion: "proposed",
            integration: "unmatched",
          },
        }),
      ],
      edges: [
        edge("director-goal", "director", "goal", "belongs_to"),
        edge("researcher-goal", "researcher", "goal", "belongs_to"),
      ],
    });

    expect(result.graph.nodes.map((item) => item.id)).toEqual([
      "goal",
      "director",
      "researcher",
    ]);
    expect(result.graph.nodes.find((item) => item.id === "researcher")).toMatchObject({
      node_type: "agent",
      status: "offline",
      actor_agent_id: researcherId,
      payload: { semantic_role: "roster" },
    });
    expect(result.graph.edges.map((item) => item.id)).toEqual([
      "director-goal",
      "researcher-goal",
    ]);
  });

  it("renders unnamed pending Agent placeholders during staffing", () => {
    const result = adaptResearchV6DirectorCanvas({
      runId: RUN_ID,
      eventSequence: 2,
      nodes: [
        node("goal", "GOAL", { kind: "goal" }),
        node("pending", "S", {
          kind: "agent",
          canonicalRef: {
            kind: "pending_agent",
            id: "00000000-0000-4000-8000-000000000213",
          },
          title: "",
          catalogSummary: "",
          state: {
            execution: "pending",
            conclusion: "proposed",
            integration: "unmatched",
          },
        }),
      ],
      edges: [edge("pending-goal", "pending", "goal", "belongs_to")],
    });

    expect(result.graph.nodes.map((item) => item.id)).toEqual([
      "goal",
      "pending",
    ]);
    expect(result.graph.nodes[1]).toMatchObject({
      status: "pending",
      payload: { semantic_role: "roster" },
    });
  });

  it("hides Agent satellites once research Work appears", () => {
    const result = adaptResearchV6DirectorCanvas({
      runId: RUN_ID,
      eventSequence: 6,
      nodes: [
        node("goal", "GOAL", { kind: "goal" }),
        node("agent-node", "S", {
          kind: "agent",
          canonicalRef: { kind: "agent", id: "00000000-0000-4000-8000-000000000212" },
          title: "市场研究员",
        }),
        node("work-node", "S", {
          kind: "work_s",
          canonicalRef: { kind: "work_item", id: RUN_ID },
          title: "核验 Manus 技术进展",
        }),
      ],
      edges: [
        edge("agent-goal", "agent-node", "goal", "belongs_to"),
        edge("work-goal", "work-node", "goal", "belongs_to"),
        edge("assignment", "work-node", "agent-node", "assigned_to"),
      ],
    });

    expect(result.graph.nodes.map((item) => item.id)).toEqual([
      "goal",
      "work-node",
    ]);
    expect(result.graph.nodes[1]).toMatchObject({
      id: "work-node",
      payload: {
        assigned_agent: { name: "市场研究员" },
      },
    });
    expect(result.graph.edges.map((item) => item.id)).toEqual(["work-goal"]);
  });

  it("folds assigned Agent identity into its Work node", () => {
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

    expect(result.graph.nodes).toHaveLength(1);
    expect(result.graph.nodes[0]).toMatchObject({
      id: "work-node",
      actor_agent_id: agentId,
      payload: {
        assigned_agent: {
          id: agentId,
          name: "Manus 技术研究员",
        },
      },
    });
    expect(result.graph.edges).toEqual([]);
  });

  it("omits standalone Agent roster nodes while preserving completed results", () => {
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

    expect(result.graph.nodes.map((item) => item.id)).toEqual(["result"]);
    expect(result.graph.nodes[0]?.status).toBe("succeeded");
  });

  it("groups leaf Branch nodes into server-declared top-level territories", () => {
    const rootBranch = "00000000-0000-4000-8000-000000000100";
    const branchA = "00000000-0000-4000-8000-000000000101";
    const branchB = "00000000-0000-4000-8000-000000000102";
    const result = adaptResearchV6DirectorCanvas({
      runId: RUN_ID,
      eventSequence: 9,
      nodes: [
        node("goal", "GOAL", { kind: "goal", branchIds: [rootBranch] }),
        node("legacy-root", "S", { branchIds: [rootBranch] }),
        node("one", "M", {
          branchIds: [branchA],
          territory: { branchId: branchA, label: "Market" },
        }),
        node("two", "L", {
          branchIds: [branchB],
          territory: { branchId: branchA, label: "Market" },
        }),
      ],
      edges: [],
    });

    expect(
      result.graph.nodes.find((item) => item.id === "goal")?.cluster_id,
    ).toBeNull();
    expect(
      result.graph.nodes.find((item) => item.id === "legacy-root")?.cluster_id,
    ).toBeNull();
    expect(result.graph.nodes.find((item) => item.id === "one")?.cluster_id).toBe(
      branchA,
    );
    expect(result.graph.nodes.find((item) => item.id === "two")?.cluster_id).toBe(
      branchA,
    );
    expect(result.graph.clusters.map((cluster) => cluster.id)).toEqual([branchA]);
    expect(result.graph.clusters[0]?.label).toBe("Market");
  });

  it("renders canonical Goal as a compact origin below synthesis tiers", () => {
    const result = adaptResearchV6DirectorCanvas({
      runId: RUN_ID,
      eventSequence: 8,
      nodes: [node("goal", "GOAL", { kind: "goal" })],
      edges: [],
    });
    expect(result.graph.nodes[0]?.level).toBe("m");
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

  it("reveals absorbed inputs when their successor is expanded", () => {
    const successor = node("successor", "M", {
      expandable: true,
      hiddenChildCount: 2,
    });
    const absorbedState = {
      execution: "succeeded" as const,
      conclusion: "accepted" as const,
      integration: "absorbed" as const,
    };
    const result = adaptResearchV6DirectorCanvas({
      runId: RUN_ID,
      eventSequence: 9,
      nodes: [
        successor,
        node("input-a", "S", { absorbed: true, state: absorbedState }),
        node("input-b", "S", { absorbed: true, state: absorbedState }),
      ],
      edges: [
        edge("input-a-into-successor", "input-a", "successor", "absorbed_into"),
        edge("input-b-into-successor", "input-b", "successor", "absorbed_into"),
      ],
      expandedRootIds: new Set(["successor"]),
    });

    expect(result.graph.nodes.map((item) => item.id)).toEqual([
      "successor",
      "input-a",
      "input-b",
    ]);
    expect(result.graph.edges.map((item) => item.edge_type)).toEqual([
      "absorbed_into",
      "absorbed_into",
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
