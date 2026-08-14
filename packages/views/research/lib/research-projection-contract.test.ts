import { describe, expect, it } from "vitest";
import type { TypedGraphResponse } from "@multica/core/research";
import { guardPrematureGateProjection } from "./research-projection-contract";

function node(id: string, nodeType: string, status: string, payload = {}) {
  return {
    id, session_id: "run-1", node_type: nodeType, title: id, summary: "", status,
    actor_agent_id: null, payload, level: "m", cluster_id: null, confidence: null,
    goal_version_id: null, derived_from: null, merged_from: [], superseded_by: null,
    restart_of: null, invalidated_by: null, superseded_at: null, invalidated_at: null,
    parent_id: null, child_ids: [], children_of: [], created_at: "", updated_at: "",
  };
}

function graph(): TypedGraphResponse {
  return {
    session_id: "run-1", graph_version: 4, total_node_count: 3,
    nodes: [
      { ...node("goal", "goal", "active"), child_ids: ["task", "gate"] },
      node("task", "task", "failed"),
      node("gate", "gate", "failed", { gate: { findings: [{ code: "plan_incomplete" }] } }),
    ],
    edges: [
      { id: "goal-task", session_id: "run-1", from_node_id: "goal", to_node_id: "task", edge_type: "decomposes", created_at: "" },
      { id: "goal-gate", session_id: "run-1", from_node_id: "goal", to_node_id: "gate", edge_type: "leads_to", created_at: "" },
    ],
    clusters: [],
    lineage: { derived: {}, merged: {}, superseded: {}, restarted: {}, invalidated: {}, supersedes: {} },
  };
}

describe("guardPrematureGateProjection", () => {
  it("isolates a failed delivery gate during planning and preserves real task failures", () => {
    const result = guardPrematureGateProjection({ graph: graph(), runId: "run-1", stage: "s1_plan", sessionStatus: "running" });
    expect(result.graph?.nodes.map((item) => item.id)).toEqual(["goal", "task"]);
    expect(result.graph?.edges.map((edge) => edge.id)).toEqual(["goal-task"]);
    expect(result.graph?.nodes.find((item) => item.id === "task")?.status).toBe("failed");
    expect(result.diagnostic).toMatchObject({ findingCodes: ["plan_incomplete"] });
  });

  it("keeps the gate once the session reaches validation", () => {
    const result = guardPrematureGateProjection({ graph: graph(), runId: "run-1", stage: "s3_validation", sessionStatus: "running" });
    expect(result.graph?.nodes).toHaveLength(3);
    expect(result.diagnostic).toBeNull();
  });
});
