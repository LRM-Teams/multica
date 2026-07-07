import { describe, expect, it } from "vitest";
import type { Agent } from "@multica/core/types";
import { accountHasConfiguredWindy, findWindyAgent } from "./windy-setup-detection";

function agent(partial: Partial<Agent>): Agent {
  return { id: partial.display_name ?? partial.name ?? "agent", name: "", display_name: "", runtime_id: "", ...partial } as Agent;
}

describe("accountHasConfiguredWindy", () => {
  it("is true when a Wendy agent has a runtime configured", () => {
    expect(accountHasConfiguredWindy([agent({ display_name: "Wendy", runtime_id: "rt-1" })])).toBe(true);
  });

  it("is false when the Wendy agent has no runtime yet (setup incomplete)", () => {
    expect(accountHasConfiguredWindy([agent({ display_name: "Wendy", runtime_id: "" })])).toBe(false);
  });

  it("is true for legacy Windy and Joe agents with runtimes", () => {
    expect(accountHasConfiguredWindy([agent({ display_name: "Windy", runtime_id: "rt-1" })])).toBe(true);
    expect(accountHasConfiguredWindy([agent({ display_name: "Joe", runtime_id: "rt-2" })])).toBe(true);
  });

  it("matches legacy handles even after display names drift", () => {
    expect(accountHasConfiguredWindy([agent({ name: "Windy", display_name: "Helper", runtime_id: "rt-1" })])).toBe(true);
    expect(findWindyAgent([agent({ name: "Joe", display_name: "Helper", runtime_id: "rt-2" })])?.name).toBe("Joe");
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
