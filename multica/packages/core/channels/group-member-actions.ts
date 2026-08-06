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
 * Rule (#845): the **owner** may do everything below. A **manager** may only
 * remove ordinary members — no promote, no demote, no ownership transfer, and
 * nothing at all against another manager or the owner. Every other viewer, and
 * a viewer whose role is missing, gets zero actions (fail-closed).
 *
 * Menu visibility only. The server re-decides each mutation (#844), so this
 * never grants anything — at worst it offers an action that then fails.
 */
/** The mutating actions a group owner can invoke from a member's menu. */
export type GroupMemberActionKind = "promote" | "demote" | "transfer" | "remove";

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
  const viewerRole = channelMemberRole(viewer);
  // Owner or manager only. Fail closed on any other / missing role.
  if (!canManageGroupMembers(viewerRole)) return NO_ACTIONS;
  // No actions on your own row — the owner leaves by transferring ownership,
  // never by self-removal, and cannot promote/demote themselves.
  if (target.member_id === viewerId) return NO_ACTIONS;

  const role = channelMemberRole(target);

  // A manager acts on ordinary members only: it can neither touch a peer
  // manager or the owner, nor change anyone's role. Ownership transfer stays
  // owner-only regardless of the target.
  if (viewerRole === "manager") {
    return { ...NO_ACTIONS, canRemove: role === "member" };
  }

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
