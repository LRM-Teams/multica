import { describe, expect, it } from "vitest";
import type { Agent } from "@multica/core/types";
import { accountHasConfiguredWindy } from "./windy-setup-detection";

function agent(partial: Partial<Agent>): Agent {
  return { display_name: "", runtime_id: "", ...partial } as Agent;
}

describe("accountHasConfiguredWindy", () => {
  it("is true when a Wendy agent has a runtime configured", () => {
    expect(accountHasConfiguredWindy([agent({ display_name: "Wendy", runtime_id: "rt-1" })])).toBe(true);
  });

  it("is false when the Wendy agent has no runtime yet (setup incomplete)", () => {
    expect(accountHasConfiguredWindy([agent({ display_name: "Wendy", runtime_id: "" })])).toBe(false);
  });

  it("is false when no agent is named Wendy", () => {
    expect(
      accountHasConfiguredWindy([agent({ display_name: "Atlas", runtime_id: "rt-1" })]),
    ).toBe(false);
  });

  it("is false for an empty agent list", () => {
    expect(accountHasConfiguredWindy([])).toBe(false);
  });

  it("finds a configured Wendy among other agents", () => {
    expect(
      accountHasConfiguredWindy([
        agent({ display_name: "Atlas", runtime_id: "rt-1" }),
        agent({ display_name: "Wendy", runtime_id: "rt-2" }),
      ]),
    ).toBe(true);
  });
});
