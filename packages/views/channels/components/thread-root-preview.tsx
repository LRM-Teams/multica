"use client";

import { Bot } from "lucide-react";
import { cn } from "@multica/ui/lib/utils";
import { useActorName } from "@multica/core/workspace/hooks";
import type { ChannelMessage } from "@multica/core/types";
import type { OpenAgentPanelFn } from "@multica/core/agents";
import { ActorAvatar } from "../../common/actor-avatar";
import { ActorProfileTrigger } from "../../common/actor-profile-popover";
import { initialsOf } from "../../common/initials";
import { useT } from "../../i18n/use-t";
import { useMessageTime } from "../../i18n/use-message-time";
import { resolveChannelAuthorDisplayName } from "./message-preview";
import { MessageBody } from "./message-body";
import { VoiceMessageAudio } from "./voice-message-audio";
import { resolveVoiceMessagePresentation } from "../lib/voice-message-presentation";

export function ThreadRootPreview({
  message,
  currentUserId,
  ownName,
  onViewParent,
  onOpenAgent,
}: {
  message: ChannelMessage;
  currentUserId: string | null;
  ownName?: string;
  onViewParent?: () => void;
  onOpenAgent?: OpenAgentPanelFn;
}) {
  const { t } = useT("channels");
  const messageTime = useMessageTime();
  const { getActorName } = useActorName();
  const isAgent = message.type === "agent";
  const voicePresentation = resolveVoiceMessagePresentation(message);
  const hidesVoiceTranscript =
    (isAgent && voicePresentation !== null) || voicePresentation?.source === "recording";
  const displayName = resolveChannelAuthorDisplayName(message, {
    currentUserId,
    ownName,
    getActorName,
  });
  // LRM-270 (Slack align): thread author row — name + time only; no Owner/Admin
  // chrome; no Agent pill. Member-list muted role is unchanged.
  const profileActorType =
    message.type === "agent"
      ? "agent"
      : message.type === "user"
        ? "user"
        : null;
  const profileActorId = message.author_id ?? null;
  // Agent author → clicking the avatar/name opens the agent side panel, same as
  // the main channel bubble. Members keep the hover card only. (#488 / LRM-292)
  const handleOpenAgentCapture =
    isAgent && onOpenAgent && profileActorId
      ? () =>
          onOpenAgent(profileActorId, {
            display_name: message.author_name,
            avatar_url: message.author_avatar_url ?? null,
          })
      : undefined;
  const avatarActorType = isAgent ? "agent" : "member";
  const avatar = profileActorId ? (
    <ActorAvatar
      actorType={avatarActorType}
      actorId={profileActorId}
      size={30}
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
        onClickCapture={handleOpenAgentCapture}
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
                onClickCapture={handleOpenAgentCapture}
              >
                {nameNode}
              </ActorProfileTrigger>
            ) : (
              nameNode
            )}
            <span
              className="text-[11px] text-muted-foreground"
              title={messageTime.full(message.created_at)}
            >
              {messageTime.format(message.created_at)}
            </span>
          </div>
          <div className="message-surface mt-1 min-w-0 overflow-hidden text-sm leading-6 text-foreground">
            <MessageBody
              content={message.content}
              parts={message.parts}
              attachments={message.attachments}
              compact
              sourceMessageId={message.id}
              consumedAttachmentIds={voicePresentation?.consumedAttachmentIds}
              contentMode={hidesVoiceTranscript ? "non-transcript" : "all"}
            />
            <VoiceMessageAudio message={message} presentation={voicePresentation} />
          </div>
          {onViewParent && (
            <button
              type="button"
              className="mt-2 rounded-md text-xs font-medium text-primary underline-offset-4 hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
              onClick={onViewParent}
            >
              {t(($) => $.thread.view_parent)}
            </button>
          )}
        </div>
      </div>
    </div>
  );
}
