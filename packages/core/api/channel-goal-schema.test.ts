import { describe, expect, it } from "vitest";
import { ChannelGoalEnvelopeSchema } from "./schemas";
import { parseWithFallback } from "./schema";

describe("ChannelGoalEnvelopeSchema", () => {
  it("accepts the current goal shape and defaults progress arrays", () => {
    const parsed = ChannelGoalEnvelopeSchema.parse({
      goal: {
        id: "goal-1",
        workspace_id: "ws-1",
        channel_id: "channel-1",
        title: "Ship",
        objective: "Ship the feature",
        success_criteria: ["Visible"],
        status: "active",
        version: 1,
        created_by_type: "user",
        created_by_id: "user-1",
        updated_by_type: "user",
        updated_by_id: "user-1",
        created_at: "2026-07-31T00:00:00Z",
        updated_at: "2026-07-31T00:00:00Z",
      },
    });
    expect(parsed.goal?.completed_criteria).toEqual([]);
    expect(parsed.goal?.current_step).toBe("");
  });

  it("keeps a missing goal safe", () => {
    expect(ChannelGoalEnvelopeSchema.parse({}).goal).toBeNull();
  });

  it("accepts a bounded work graph summary and defaults its counters", () => {
    const parsed = ChannelGoalEnvelopeSchema.parse({
      goal: {
        id: "goal-1", workspace_id: "ws-1", channel_id: "channel-1",
        title: "Ship", objective: "Ship", status: "active", version: 2,
        created_by_type: "agent", created_by_id: "agent-1",
        updated_by_type: "agent", updated_by_id: "agent-1",
        created_at: "2026-08-06T00:00:00Z", updated_at: "2026-08-06T00:00:00Z",
        work_graph: { id: "graph-1", version: 3, status: "active" },
      },
    });
    expect(parsed.goal?.work_graph).toMatchObject({ id: "graph-1", total: 0, stale: 0 });
  });

  it("accepts the server-owned delivery control plane", () => {
    const parsed = ChannelGoalEnvelopeSchema.parse({
      goal: {
        id: "goal-1",
        workspace_id: "ws-1",
        channel_id: "channel-1",
        title: "Ship",
        objective: "Ship",
        status: "active",
        version: 2,
        created_by_type: "agent",
        created_by_id: "agent-1",
        updated_by_type: "agent",
        updated_by_id: "agent-1",
        created_at: "2026-08-06T00:00:00Z",
        updated_at: "2026-08-06T00:00:00Z",
        coordination: {
          project_id: "project-1",
          git_repository_bound: true,
          agent_member_count: 3,
          project_issue_total: 4,
          open_project_issue_total: 2,
          execution_admission: "ready",
        },
      },
    });
    expect(parsed.goal?.coordination).toMatchObject({
      project_id: "project-1",
      channel_issue_total: 0,
      channel_project_issue_total: 0,
      in_review_project_issue_total: 0,
      execution_admission: "ready",
    });
  });

  it("fails a malformed response closed instead of crashing the channel", () => {
    const parsed = parseWithFallback(
      { goal: { id: 42, status: "future-state" } },
      ChannelGoalEnvelopeSchema,
      { goal: null },
      { endpoint: "test channel goal" },
    );
    expect(parsed).toEqual({ goal: null });
  });
});
