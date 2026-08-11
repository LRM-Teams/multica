import { describe, expect, it } from "vitest";
import type { ResearchMessage } from "@multica/core/types";
import { buildGoalVersionHistory } from "./research-d5-goal-history";

function processMessage(
  partial: Partial<ResearchMessage> & Pick<ResearchMessage, "id">,
): ResearchMessage {
  return {
    session_id: "s1",
    sender_type: "system",
    sender_id: null,
    target_agent_id: null,
    body: "",
    created_at: "2026-08-11T00:00:00.000Z",
    ...partial,
  };
}

describe("buildGoalVersionHistory", () => {
  it("collects goal_steered process events and marks current version", () => {
    const history = buildGoalVersionHistory({
      currentGoal: "Current goal",
      currentVersion: 2,
      messages: [
        processMessage({
          id: "m1",
          card_kind: "process",
          meta: {
            op: "goal_steered",
            goal_version: 1,
            goal: "First goal",
            reason: "initial",
          },
        }),
        processMessage({
          id: "m2",
          card_kind: "process",
          meta: {
            op: "goal_steered",
            goal_version: 2,
            goal: "Second goal",
          },
        }),
      ],
    });

    expect(history).toHaveLength(2);
    expect(history[0]?.version).toBe(2);
    expect(history[0]?.isCurrent).toBe(true);
    expect(history[0]?.goal).toBe("Current goal");
    expect(history[1]?.version).toBe(1);
  });
});
