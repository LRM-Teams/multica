import type { ChannelMember } from "@multica/core/types";

/**
 * #839 — identity key for a per-member failure notice.
 *
 * Keyed by `(member_type, member_id)` because a user and an agent can share an
 * id space, and because the notice must follow the member rather than a list
 * position: the roster re-sorts and refetches, and one member's failure must
 * never land on another's row.
 */
export function memberFailureKey(
  channelId: string,
  member: Pick<ChannelMember, "member_type" | "member_id">,
): string {
  // Channel-scoped: the state lives for the whole ChannelsPage, so a key of
  // member identity ALONE would let a failure in channel A surface on the same
  // member's row in channel B, where nothing was ever attempted (Iris review).
  return `${channelId}:${member.member_type}:${member.member_id}`;
}
