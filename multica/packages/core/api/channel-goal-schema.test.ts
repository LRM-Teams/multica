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
