import { describe, expect, it } from "vitest";
import {
  indexMemberManagementCapabilities,
  memberCapabilityKey,
  resolveGroupMemberActions,
} from "./member-management-capabilities";
import type { GroupMemberActions } from "./group-member-actions";
import type { ChannelMemberManagementCapabilities } from "../types";

const NO: GroupMemberActions = {
  canPromoteToManager: false,
  canDemoteToMember: false,
  canTransferOwnership: false,
  canRemove: false,
};

const OWNER_LOCAL: GroupMemberActions = {
  canPromoteToManager: true,
  canDemoteToMember: false,
  canTransferOwnership: true,
  canRemove: true,
};

describe("memberCapabilityKey", () => {
  it("joins type and id", () => {
    expect(memberCapabilityKey("agent", "a-1")).toBe("agent:a-1");
  });
});

describe("resolveGroupMemberActions (LRM-879 / LRM-872)", () => {
  it("falls back to local actions when capabilities are missing", () => {
    expect(
      resolveGroupMemberActions(OWNER_LOCAL, { member_type: "agent", member_id: "a-1" }, undefined),
    ).toEqual(OWNER_LOCAL);
    expect(
      resolveGroupMemberActions(OWNER_LOCAL, { member_type: "agent", member_id: "a-1" }, new Map()),
    ).toEqual(OWNER_LOCAL);
  });

  it("uses server can_remove for an inviter who has no local channel-role actions", () => {
    const caps = indexMemberManagementCapabilities({
      channel_id: "c1",
      name: "g",
      kind: "group",
      archived: false,
      can_add_members: true,
      can_remove_members: false,
      can_leave: true,
      targets: [
        {
          member_type: "agent",
          member_id: "agent-self-added",
          display_name: "Bot",
          role: "member",
          can_remove: true,
          can_promote_to_manager: false,
          can_demote_to_member: false,
          can_transfer_ownership: false,
        },
        {
          member_type: "agent",
          member_id: "agent-other",
          display_name: "Other",
          role: "member",
          can_remove: false,
          can_promote_to_manager: false,
          can_demote_to_member: false,
          can_transfer_ownership: false,
        },
      ],
    } satisfies ChannelMemberManagementCapabilities);

    expect(
      resolveGroupMemberActions(
        NO,
        { member_type: "agent", member_id: "agent-self-added" },
        caps,
      ),
    ).toEqual({
      canPromoteToManager: false,
      canDemoteToMember: false,
      canTransferOwnership: false,
      canRemove: true,
    });

    expect(
      resolveGroupMemberActions(
        NO,
        { member_type: "agent", member_id: "agent-other" },
        caps,
      ).canRemove,
    ).toBe(false);
  });

  it("lets server capabilities override local owner heuristics", () => {
    const caps = indexMemberManagementCapabilities({
      channel_id: "c1",
      name: "g",
      kind: "group",
      archived: false,
      can_add_members: true,
      can_remove_members: true,
      can_leave: false,
      targets: [
        {
          member_type: "user",
          member_id: "u-2",
          display_name: "Peer",
          role: "member",
          can_remove: false,
          can_promote_to_manager: false,
          can_demote_to_member: false,
          can_transfer_ownership: false,
        },
      ],
    } satisfies ChannelMemberManagementCapabilities);

    expect(
      resolveGroupMemberActions(
        OWNER_LOCAL,
        { member_type: "user", member_id: "u-2" },
        caps,
      ),
    ).toEqual(NO);
  });
});
