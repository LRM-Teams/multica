import type { ChannelMessage } from "@multica/core/types";
import type { ThreadMemberType, ThreadParticipant } from "./thread-participants";

// The read-model (#251) speaks a broader `member_type` string than the panel's
// two-value union; anything that isn't an agent is a user for presentation.
function normalizeMemberType(raw: string): ThreadMemberType {
  return raw === "agent" ? "agent" : "user";
}

/**
 * Map the thread root's BE-provided participant list (#251
 * `thread_participants`) to the panel's {@link ThreadParticipant} chips. This
 * is the source of truth for identity when present; the caller falls back to
 * the structural {@link deriveThreadParticipants} only when this returns empty.
 * `sources` is left empty — the BE list carries identity, not the structural
 * "why they're here" reasons, which the chips don't render.
 */
export function mapThreadParticipants(root: ChannelMessage): ThreadParticipant[] {
  const list = root.thread_participants ?? [];
  return list.flatMap((participant) => {
    if (!participant.member_id) return [];
    const memberType = normalizeMemberType(participant.member_type);
    const displayName =
      participant.display_name || participant.name || participant.member_id;
    return [
      {
        key: participant.key || `${memberType}:${participant.member_id}`,
        memberType,
        memberId: participant.member_id,
        name: participant.name || displayName,
        displayName,
        sources: [],
      },
    ];
  });
}
