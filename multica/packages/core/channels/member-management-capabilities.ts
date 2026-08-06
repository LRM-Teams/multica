import type {
  ChannelMember,
  ChannelMemberManagementCapabilities,
  ChannelMemberManagementCapabilityTarget,
} from "../types";
import type { GroupMemberActions } from "./group-member-actions";

/** Stable map key for a channel member row / capabilities target. */
export function memberCapabilityKey(
  memberType: string,
  memberId: string,
): string {
  return `${memberType}:${memberId}`;
}

export function indexMemberManagementCapabilities(
  capabilities: ChannelMemberManagementCapabilities | undefined | null,
): Map<string, ChannelMemberManagementCapabilityTarget> {
  const map = new Map<string, ChannelMemberManagementCapabilityTarget>();
  for (const t of capabilities?.targets ?? []) {
    map.set(memberCapabilityKey(t.member_type, t.member_id), t);
  }
  return map;
}

/**
 * LRM-872 / LRM-879: when the server capabilities projection is available for
 * a row, use it as the sole source of truth for menu flags (including inviter
 * `can_remove`). Otherwise fall back to local `groupMemberActions` (channel
 * owner/manager heuristics) so privileged viewers still work while the query
 * loads or fails.
 */
export function resolveGroupMemberActions(
  local: GroupMemberActions,
  target: Pick<ChannelMember, "member_type" | "member_id">,
  capabilitiesByKey: Map<string, ChannelMemberManagementCapabilityTarget> | undefined,
): GroupMemberActions {
  const cap = capabilitiesByKey?.get(
    memberCapabilityKey(target.member_type, target.member_id),
  );
  if (!cap) return local;
  return {
    canPromoteToManager: !!cap.can_promote_to_manager,
    canDemoteToMember: !!cap.can_demote_to_member,
    canTransferOwnership: !!cap.can_transfer_ownership,
    canRemove: !!cap.can_remove,
  };
}
