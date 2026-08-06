import { describe, expect, it } from "vitest";
import { dedupeResearchFleetMembers } from "./fleet-members";
import type { ResearchFleetMember } from "../types/research";

function member(
  partial: Partial<ResearchFleetMember> & Pick<ResearchFleetMember, "id" | "agent_id" | "role">,
): ResearchFleetMember {
  return {
    status: "active",
    is_lead: false,
    name: partial.role,
    display_name: partial.role,
    ...partial,
  };
}

describe("dedupeResearchFleetMembers", () => {
  it("keeps one member per role and prefers the lead", () => {
    const out = dedupeResearchFleetMembers([
      member({ id: "a1", agent_id: "g1", role: "lead", is_lead: false, name: "A" }),
      member({ id: "a2", agent_id: "g2", role: "lead", is_lead: true, name: "B" }),
      member({ id: "a3", agent_id: "g3", role: "scout", name: "C" }),
      member({ id: "a4", agent_id: "g4", role: "scout", name: "D" }),
    ]);
    expect(out).toHaveLength(2);
    expect(out.find((m) => m.role === "lead")?.name).toBe("B");
    expect(out.find((m) => m.role === "scout")?.id).toBe("a3");
  });

  it("drops archived members", () => {
    const out = dedupeResearchFleetMembers([
      member({ id: "a1", agent_id: "g1", role: "lead", status: "archived" }),
      member({ id: "a2", agent_id: "g2", role: "lead", is_lead: true }),
    ]);
    expect(out).toHaveLength(1);
    expect(out[0]?.id).toBe("a2");
  });
});
