import { describe, expect, it } from "vitest";
import { agentFleetRankSchema } from "./agent-fleet-schemas";

const rankPayload = {
  agent_id: "agent-1",
  fleet_score: 10,
  class_id: "reserve",
  class_label: "Reserve",
  fleet_rank: 1,
  fleet_size: 1,
  sample_tasks: 2,
  sample_sufficient: false,
  frozen: false,
  pillars: { delivery: 0, evolution: 0, growth: 0, efficiency: 0 },
};

describe("agent fleet rank schema", () => {
  it("preserves the configured minimum sample size", () => {
    expect(
      agentFleetRankSchema.parse({ ...rankPayload, min_sample_tasks: 12 }).min_sample_tasks,
    ).toBe(12);
  });

  it("uses the historical default at the API boundary for older responses", () => {
    expect(agentFleetRankSchema.parse(rankPayload).min_sample_tasks).toBe(5);
  });
});
