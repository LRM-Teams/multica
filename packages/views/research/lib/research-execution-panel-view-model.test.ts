import { describe, expect, it } from "vitest";
import type { ResearchPresenceMap } from "@multica/core/research";
import type { ResearchFleetMember, ResearchGraphNode } from "@multica/core/types";
import { buildResearchExecutionAgents } from "./research-execution-panel-view-model";

const members = ["lead", "scout", "domain", "reviewer", "reporter"].map(
  (role, index) => ({
    id: `member-${index}`,
    agent_id: `agent-${index}`,
    role,
    status: "active",
    is_lead: index === 0,
    display_name: `Agent ${index + 1}`,
  }),
) satisfies ResearchFleetMember[];

function signal(overrides: Partial<ResearchPresenceMap[string]>): ResearchPresenceMap[string] {
  return {
    activity: "",
    updatedAt: 1,
    phase: "idle",
    role: "",
    name: "",
    avatarUrl: null,
    fleetMemberId: null,
    taskId: null,
    nodeId: null,
    branchId: null,
    stage: null,
    expiresAt: null,
    staleReason: null,
    ...overrides,
  };
}

const node = {
  id: "node-running",
  session_id: "session",
  node_type: "probe",
  title: "Pricing evidence",
  summary: "",
  status: "active",
  actor_agent_id: "agent-1",
  payload: {},
  created_at: "",
  updated_at: "",
} satisfies ResearchGraphNode;

describe("buildResearchExecutionAgents", () => {
  it("maps a five-person mixed roster and only locates real snapshot nodes", () => {
    const rows = buildResearchExecutionAgents(
      members,
      {
        "agent-0": signal({ phase: "queued" }),
        "agent-1": signal({ phase: "running", activity: "Checking prices", nodeId: node.id }),
        "agent-2": signal({ phase: "done" }),
        "agent-3": signal({ phase: "failed" }),
        "agent-4": signal({ phase: "stale", nodeId: "missing-node" }),
      },
      [node],
    );

    expect(rows.map((row) => row.status)).toEqual([
      "queued",
      "running",
      "done",
      "failed",
      "stale",
    ]);
    expect(rows[1]).toEqual(expect.objectContaining({
      action: "Checking prices",
      currentNodeId: node.id,
      locationLabel: "Pricing evidence",
    }));
    expect(rows[4]!.currentNodeId).toBeUndefined();
  });

  it("keeps the full roster idle when presence is missing", () => {
    expect(buildResearchExecutionAgents(members, {}, []).map((row) => row.status)).toEqual([
      "idle", "idle", "idle", "idle", "idle",
    ]);
  });
});
