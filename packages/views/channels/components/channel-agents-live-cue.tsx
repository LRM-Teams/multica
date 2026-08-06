"use client";

import { ActorAvatar } from "../../common/actor-avatar";
import type { ChannelMemberBrief } from "@multica/core/types";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n";

const FACE_MAX = 3;
const FACE_SIZE = 22;

export interface ChannelPresenceClusterProps {
  members: readonly ChannelMemberBrief[];
  onOpenMembers?: () => void;
  className?: string;
}

// Presence is channel membership only. Agent execution belongs to the explicit
// product-task surface and never changes the chat read model or header state.
export function ChannelPresenceCluster({
  members,
  onOpenMembers,
  className,
}: ChannelPresenceClusterProps) {
  const { t } = useT("channels");
  const overlap = Math.round(FACE_SIZE * 0.28);
  const ariaLabel = t(($) => $.header.view_members_aria);

  return (
    <button
      type="button"
      className={cn(
        "inline-flex min-h-8 min-w-8 items-center rounded-md py-0.5 pl-1 pr-1.5 text-foreground outline-none transition-colors",
        "hover:bg-muted focus-visible:ring-2 focus-visible:ring-ring",
        className,
      )}
      aria-label={ariaLabel}
      data-testid="channel-header-members-chip"
      onClick={() => onOpenMembers?.()}
    >
      <span
        className="relative isolate inline-flex items-center"
        data-testid="channel-presence-faces"
      >
        {members.slice(0, FACE_MAX).map((member, index) => (
          <span
            key={`${member.member_type}:${member.member_id}`}
            className="relative inline-flex rounded-full ring-2 ring-background"
            style={{ marginLeft: index === 0 ? 0 : -overlap, zIndex: index + 1 }}
          >
            <ActorAvatar
              actorType={member.member_type === "agent" ? "agent" : "member"}
              actorId={member.member_id}
              name={member.display_name}
              size={FACE_SIZE}
              avatarUrlHint={member.avatar_url}
              showStatusDot={false}
              profileLink={false}
            />
          </span>
        ))}
      </span>
    </button>
  );
}
