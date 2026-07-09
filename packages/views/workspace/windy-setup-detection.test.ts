import { describe, expect, it } from "vitest";
import type { Agent } from "@multica/core/types";
import { accountHasConfiguredWindy, findWindyAgent } from "./windy-setup-detection";

function agent(partial: Partial<Agent>): Agent {
  return {
    id: partial.id ?? partial.display_name ?? partial.name ?? "agent",
    name: "",
    display_name: "",
    runtime_id: "",
    archived_at: null,
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    ...partial,
  } as Agent;
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

  it("prefers active configured canonical Wendy when duplicates exist", () => {
    const picked = findWindyAgent([
      agent({ id: "archived-newer", display_name: "Wendy", runtime_id: "rt-archived", archived_at: "2026-01-04T00:00:00Z", updated_at: "2026-01-04T00:00:00Z" }),
      agent({ id: "legacy-configured", display_name: "Joe", runtime_id: "rt-legacy", updated_at: "2026-01-03T00:00:00Z" }),
      agent({ id: "canonical-configured", display_name: "Wendy", runtime_id: "rt-canonical", updated_at: "2026-01-02T00:00:00Z" }),
      agent({ id: "canonical-unconfigured", display_name: "Wendy", runtime_id: "", updated_at: "2026-01-05T00:00:00Z" }),
    ]);

    expect(picked?.id).toBe("canonical-configured");
  });

  it("does not treat an archived Wendy as configured", () => {
    expect(
      accountHasConfiguredWindy([
        agent({ display_name: "Wendy", runtime_id: "rt-1", archived_at: "2026-01-01T00:00:00Z" }),
      ]),
    ).toBe(false);
  });
});
