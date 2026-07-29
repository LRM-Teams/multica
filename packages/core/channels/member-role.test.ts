import { describe, it, expect } from "vitest";
import type { ChannelMember } from "../types";
import {
  channelMemberRole,
  channelMemberBadge,
  canManageGroupMembers,
  isRemovableGroupMember,
} from "./member-role";

function member(overrides: Partial<ChannelMember>): ChannelMember {
  return {
    member_type: "user",
    member_id: "u-1",
    name: "u",
    display_name: "U",
    created_at: "2026-07-28T00:00:00Z",
    ...overrides,
  };
}

describe("channelMemberRole", () => {
  it("defaults a missing role to member (least privilege)", () => {
    expect(channelMemberRole(member({ role: undefined }))).toBe("member");
    expect(channelMemberRole(member({ role: "owner" }))).toBe("owner");
  });
});

describe("channelMemberBadge (elevated-only)", () => {
  it("owner → owner badge", () => {
    expect(channelMemberBadge(member({ role: "owner" }))).toBe("owner");
  });
  it("manager → the SAME badge for humans and agents (#832)", () => {
    // This assertion used to require the two to DIFFER, which pinned a
    // distinction nobody had decided — it was written alongside the badge and
    // then guarded it. A green, working test can be protecting an expired
    // decision, and it resists the correction rather than merely misleading.
    expect(channelMemberBadge(member({ role: "manager", member_type: "user" }))).toBe("manager");
    expect(channelMemberBadge(member({ role: "manager", member_type: "agent" }))).toBe("manager");
    expect(channelMemberBadge(member({ role: "manager", member_type: "user" }))).toBe(
      channelMemberBadge(member({ role: "manager", member_type: "agent" })),
    );
  });
  it("member (or missing) → no badge", () => {
    expect(channelMemberBadge(member({ role: "member" }))).toBeNull();
    expect(channelMemberBadge(member({ role: undefined }))).toBeNull();
  });
});

describe("canManageGroupMembers", () => {
  // #845 widened this from owner-only: a manager may open the menu too, but
  // only ever gets "remove an ordinary member" inside it (groupMemberActions).
  it("admits owner and manager; ordinary members get no menu", () => {
    expect(canManageGroupMembers("owner")).toBe(true);
    expect(canManageGroupMembers("manager")).toBe(true);
    expect(canManageGroupMembers("member")).toBe(false);
  });
});

describe("isRemovableGroupMember", () => {
  it("blocks removing the owner (must transfer first)", () => {
    expect(isRemovableGroupMember(member({ role: "owner" }))).toBe(false);
    expect(isRemovableGroupMember(member({ role: "manager" }))).toBe(true);
    expect(isRemovableGroupMember(member({ role: "member" }))).toBe(true);
  });
});
