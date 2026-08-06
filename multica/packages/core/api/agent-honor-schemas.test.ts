import { describe, expect, it } from "vitest";
import {
  agentHonorDashboardSchema,
  agentHonorRulesViewSchema,
  EMPTY_AGENT_HONOR_DASHBOARD,
  EMPTY_AGENT_HONOR_RULES_VIEW,
} from "./agent-honor-schemas";

describe("agent honor schemas", () => {
  it("accepts the safe dashboard and rules fallbacks", () => {
    expect(agentHonorDashboardSchema.parse(EMPTY_AGENT_HONOR_DASHBOARD)).toEqual(
      EMPTY_AGENT_HONOR_DASHBOARD,
    );
    expect(agentHonorRulesViewSchema.parse(EMPTY_AGENT_HONOR_RULES_VIEW)).toEqual(
      EMPTY_AGENT_HONOR_RULES_VIEW,
    );
  });

  it("rejects malformed progression and fleet payloads", () => {
    expect(
      agentHonorDashboardSchema.safeParse({
        ...EMPTY_AGENT_HONOR_DASHBOARD,
        level: "60",
      }).success,
    ).toBe(false);
    expect(
      agentHonorDashboardSchema.safeParse({
        ...EMPTY_AGENT_HONOR_DASHBOARD,
        fleet: {
          ...EMPTY_AGENT_HONOR_DASHBOARD.fleet,
          pillars: { delivery: 1 },
        },
      }).success,
    ).toBe(false);
    expect(
      agentHonorRulesViewSchema.safeParse({
        ...EMPTY_AGENT_HONOR_RULES_VIEW,
        rules: {
          ...EMPTY_AGENT_HONOR_RULES_VIEW.rules,
          fleet_weights: { delivery: "0.5" },
        },
      }).success,
    ).toBe(false);
  });
});
