import { describe, it, expect } from "vitest";
import type { ChannelMember } from "../types";
import { groupMemberActions, canLeaveGroup } from "./group-member-actions";

function member(overrides: Partial<ChannelMember>): ChannelMember {
  return {
    member_type: "user",
    member_id: "u-target",
    name: "t",
    display_name: "T",
    created_at: "2026-07-28T00:00:00Z",
    ...overrides,
  };
}

const OWNER_VIEWER = "u-owner";

describe("groupMemberActions — owner viewer", () => {
  const owner = { role: "owner" as const };

  it("member target: can promote / transfer (human) / remove; not demote", () => {
    const a = groupMemberActions(owner, member({ member_id: "u-1", role: "member" }), OWNER_VIEWER);
    expect(a).toEqual({
      canPromoteToManager: true,
      canDemoteToMember: false,
      canTransferOwnership: true,
      canRemove: true,
    });
  });

  it("manager+agent target: can demote / remove; NOT transfer (agents are never owner); not promote", () => {
    const a = groupMemberActions(
      owner,
      member({ member_id: "a-1", member_type: "agent", role: "manager" }),
      OWNER_VIEWER,
    );
    expect(a).toEqual({
      canPromoteToManager: false,
      canDemoteToMember: true,
      canTransferOwnership: false,
      canRemove: true,
    });
  });

  it("manager+human target: can demote / transfer / remove", () => {
    const a = groupMemberActions(owner, member({ member_id: "u-2", role: "manager" }), OWNER_VIEWER);
    expect(a).toEqual({
      canPromoteToManager: false,
      canDemoteToMember: true,
      canTransferOwnership: true,
      canRemove: true,
    });
  });

  it("member+agent target: can promote (→群管) / remove; NOT transfer", () => {
    const a = groupMemberActions(
      owner,
      member({ member_id: "a-2", member_type: "agent", role: "member" }),
      OWNER_VIEWER,
    );
    expect(a).toEqual({
      canPromoteToManager: true,
      canDemoteToMember: false,
      canTransferOwnership: false,
      canRemove: true,
    });
  });

  it("own row (owner acting on self): no actions — owner leaves via transfer, never self-remove", () => {
    const a = groupMemberActions(
      owner,
      member({ member_id: OWNER_VIEWER, role: "owner" }),
      OWNER_VIEWER,
    );
    expect(a).toEqual({
      canPromoteToManager: false,
      canDemoteToMember: false,
      canTransferOwnership: false,
      canRemove: false,
    });
  });
});

describe("groupMemberActions — non-owner viewers get zero actions (fail-closed)", () => {
  for (const viewerRole of ["manager", "member", undefined] as const) {
    it(`viewer role=${viewerRole ?? "missing"} → all false`, () => {
      const a = groupMemberActions(
        { role: viewerRole },
        member({ member_id: "u-9", role: "member" }),
        "u-someone",
      );
      expect(a).toEqual({
        canPromoteToManager: false,
        canDemoteToMember: false,
        canTransferOwnership: false,
        canRemove: false,
      });
    });
  }
});

describe("canLeaveGroup", () => {
  it("owner cannot leave (must transfer ownership first)", () => {
    expect(canLeaveGroup("owner")).toBe(false);
  });
  it("manager and member can leave", () => {
    expect(canLeaveGroup("manager")).toBe(true);
    expect(canLeaveGroup("member")).toBe(true);
  });
});
