import type { ChannelMember } from "@multica/core/types";

/**
 * #836 — identity key for a per-member failure notice.
 *
 * Keyed by `(member_type, member_id)` because a user and an agent can share an
 * id space, and because the notice must follow the member rather than a list
 * position: the roster re-sorts and refetches, and one member's failure must
 * never land on another's row.
 */
export function memberFailureKey(
  member: Pick<ChannelMember, "member_type" | "member_id">,
): string {
  return `${member.member_type}:${member.member_id}`;
}
