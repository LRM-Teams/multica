import type { ChannelMemberBrief } from "@multica/core/types";
import { ActorAvatar } from "../../common/actor-avatar";

const MEMBER_STACK_MAX = 5;

/** LRM-494 — overlapping member avatars on the details members row. */
export function ChannelDetailsMemberStack({
  members,
  overflowText,
}: {
  members: ChannelMemberBrief[];
  overflowText: (count: number) => string;
}) {
  const visible = members.slice(0, MEMBER_STACK_MAX);
  const overflow = Math.max(0, members.length - visible.length);
  const overlap = 10;
  return (
    <span className="inline-flex items-center" data-testid="channel-details-member-stack">
      {visible.map((m, i) => (
        <span
          key={`${m.member_type}:${m.member_id}`}
          style={{ marginLeft: i === 0 ? 0 : -overlap }}
          className="inline-flex rounded-full ring-2 ring-card"
        >
          <ActorAvatar
            actorType={m.member_type === "agent" ? "agent" : "member"}
            actorId={m.member_id}
            size={28}
            avatarUrlHint={m.avatar_url}
            profileLink={false}
          />
        </span>
      ))}
      {overflow > 0 ? (
        <span
          style={{ marginLeft: -overlap }}
          className="inline-flex size-7 items-center justify-center rounded-full bg-muted text-[10px] font-semibold text-muted-foreground ring-2 ring-card"
        >
          {overflowText(overflow)}
        </span>
      ) : null}
    </span>
  );
}
