// @vitest-environment node
import { describe, expect, it } from "vitest";

import { AGENT_COLOR_PALETTE, agentColor } from "./agent-color";

describe("agentColor", () => {
  it("is deterministic — the same agent id always maps to the same color", () => {
    const id = "agent-7f3c2a10";
    expect(agentColor(id)).toEqual(agentColor(id));
  });

  it("always returns a color from the palette", () => {
    for (const id of ["a", "b", "ccc", "agent-1", "9d8e7f", ""]) {
      expect(AGENT_COLOR_PALETTE).toContainEqual(agentColor(id));
    }
  });

  it("distributes distinct ids across more than one palette entry", () => {
    const ids = Array.from({ length: 50 }, (_, i) => `agent-${i}`);
    const distinct = new Set(ids.map((id) => agentColor(id).fg));
    expect(distinct.size).toBeGreaterThan(1);
  });

  it("handles an empty id without throwing", () => {
    expect(() => agentColor("")).not.toThrow();
    expect(agentColor("")).toEqual(agentColor(""));
  });

  it("treats null/undefined like an empty id (no throw mid-render)", () => {
    expect(() => agentColor(null)).not.toThrow();
    expect(() => agentColor(undefined)).not.toThrow();
    expect(agentColor(null)).toEqual(agentColor(""));
    expect(agentColor(undefined)).toEqual(agentColor(""));
  });
});
