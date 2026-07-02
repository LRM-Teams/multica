"use client";

import { Bot } from "lucide-react";
import { cn } from "@multica/ui/lib/utils";
import type { ChannelMessage } from "@multica/core/types";
import { MemoizedMarkdown } from "../../common/markdown";
import { ActorAvatar } from "../../common/actor-avatar";
import { ActorProfileTrigger } from "../../common/actor-profile-popover";
import { agentColor } from "../../common/agent-color";
import { initialsOf } from "../../common/initials";
import { AttachmentList } from "../../issues/components/comment-card";
import { useT } from "../../i18n/use-t";

function formatTime(value: string): string {
  try {
    return new Intl.DateTimeFormat(undefined, {
      hour: "2-digit",
      minute: "2-digit",
    }).format(new Date(value));
  } catch {
    return "";
  }
}

export function ThreadRootPreview({
  message,
  currentUserId,
  ownName,
}: {
  message: ChannelMessage;
  currentUserId: string | null;
  ownName?: string;
}) {
  const { t } = useT("channels");
  const isOwn =
    message.author_type === "user" &&
    message.author_id != null &&
    message.author_id === currentUserId;
  const isAgent = message.author_type === "agent";
  const displayName = isOwn ? ownName ?? message.author_name : message.author_name;
  const profileActorType =
    message.author_type === "agent"
      ? "agent"
      : message.author_type === "user"
        ? "user"
        : null;
  const profileActorId = message.author_id ?? null;
  const avatarActorType = isAgent ? "agent" : "member";
  const avatar = profileActorId ? (
    <ActorAvatar
      actorType={avatarActorType}
      actorId={profileActorId}
      size={30}
      tint={isAgent ? agentColor(message.author_id ?? message.author_name) : undefined}
      profileLink={false}
    />
  ) : (
    <span
      className={cn(
        "inline-flex size-[30px] shrink-0 items-center justify-center rounded-full bg-muted text-xs font-medium text-muted-foreground",
        isAgent && "bg-primary/[0.08] text-primary",
      )}
    >
      {isAgent ? <Bot className="size-3.5" /> : initialsOf(displayName || "?")}
    </span>
  );
  const avatarNode =
    profileActorType && profileActorId ? (
      <ActorProfileTrigger
        memberType={profileActorType}
        memberId={profileActorId}
        side="top"
        sideOffset={8}
      >
        {avatar}
      </ActorProfileTrigger>
    ) : (
      avatar
    );
  const nameNode = <span className="font-medium text-foreground">{displayName}</span>;

  return (
    <div className="shrink-0 border-b border-border/40 bg-background px-5 py-3">
      <div className="flex gap-2.5">
        {avatarNode}
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-2 text-sm">
            {profileActorType && profileActorId ? (
              <ActorProfileTrigger
                memberType={profileActorType}
                memberId={profileActorId}
                side="top"
                sideOffset={8}
              >
                {nameNode}
              </ActorProfileTrigger>
            ) : (
              nameNode
            )}
            {isAgent && (
              <span className="shrink-0 rounded-full border border-primary/20 bg-primary/[0.08] px-1.5 py-0.5 text-[10px] font-normal leading-none text-primary">
                {t(($) => $.message.agent_badge)}
              </span>
            )}
            <span className="text-[11px] text-muted-foreground">
              {formatTime(message.created_at)}
            </span>
          </div>
          <div
            className="mt-1 min-w-0 text-sm leading-6 text-foreground"
          >
            <MemoizedMarkdown attachments={message.attachments}>{message.content}</MemoizedMarkdown>
            <AttachmentList
              attachments={message.attachments}
              content={message.content}
              className="mt-1.5"
            />
          </div>
        </div>
      </div>
    </div>
  );
}
