import type { ChannelMember, ChannelMemberRole } from "../types";
import {
  channelMemberRole,
  canManageGroupMembers,
  isRemovableGroupMember,
} from "./member-role";

/**
 * Which management actions the current viewer may take on a given member row in
 * group settings (group management FE). Pure + UI-free so the owner-only gating
 * is unit-testable. The mutations themselves are gated behind #801; this only
 * drives which menu items render.
 *
 * V1 rule: only the group **owner** manages members. Every non-owner viewer —
 * and a viewer whose role is missing — gets zero actions (fail-closed).
 */
export interface GroupMemberActions {
  /** member → manager (群管 for agents, 管理员 for humans). */
  canPromoteToManager: boolean;
  /** manager → member. */
  canDemoteToMember: boolean;
  /** Hand ownership to this member. Humans only — agents are never owner. */
  canTransferOwnership: boolean;
  /** Remove (kick) this member. */
  canRemove: boolean;
}

const NO_ACTIONS: GroupMemberActions = {
  canPromoteToManager: false,
  canDemoteToMember: false,
  canTransferOwnership: false,
  canRemove: false,
};

export function groupMemberActions(
  viewer: Pick<ChannelMember, "role">,
  target: Pick<ChannelMember, "role" | "member_type" | "member_id">,
  viewerId: string,
): GroupMemberActions {
  // Only the owner manages members (V1). Fail closed on any other / missing role.
  if (!canManageGroupMembers(channelMemberRole(viewer))) return NO_ACTIONS;
  // No actions on your own row — the owner leaves by transferring ownership,
  // never by self-removal, and cannot promote/demote themselves.
  if (target.member_id === viewerId) return NO_ACTIONS;

  const role = channelMemberRole(target);
  return {
    canPromoteToManager: role === "member",
    canDemoteToMember: role === "manager",
    // Ownership can only move to a human; agents are never group owner.
    canTransferOwnership: target.member_type === "user" && role !== "owner",
    canRemove: isRemovableGroupMember(target),
  };
}

/**
 * Whether the viewer may leave the group. The owner cannot leave while still
 * owner — ownership must be transferred first (drives the "transfer ownership
 * first" copy instead of a Leave affordance).
 */
export function canLeaveGroup(viewerRole: ChannelMemberRole): boolean {
  return viewerRole !== "owner";
}
