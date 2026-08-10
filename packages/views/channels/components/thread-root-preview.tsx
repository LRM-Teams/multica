"use client";

import { useCallback, useLayoutEffect, useRef, useState } from "react";
import { Bot } from "lucide-react";
import { cn } from "@multica/ui/lib/utils";
import { useActorName } from "@multica/core/workspace/hooks";
import type { ChannelMessage } from "@multica/core/types";
import type { OpenAgentPanelFn } from "@multica/core/agents";
import { messageCollapseFadeClassName } from "../../common/mention-token";
import { ActorAvatar } from "../../common/actor-avatar";
import { ActorStyledName } from "../../common/actor-styled-name";
import { ActorProfileTrigger } from "../../common/actor-profile-popover";
import { initialsOf } from "../../common/initials";
import { useT } from "../../i18n/use-t";
import { useMessageTime } from "../../i18n/use-message-time";
import { Time } from "../../i18n/time";
import { resolveChannelAuthorDisplayName } from "./message-preview";
import { MessageBody } from "./message-body";
import { VoiceMessageAudio } from "./voice-message-audio";
import { resolveVoiceMessagePresentation } from "../lib/voice-message-presentation";
import { useMessageContentExpanded } from "../hooks/use-message-content-expanded";

/** Match main-column long-message clamp (channel-message-bubble LRM-268). */
const MESSAGE_COLLAPSE_MAX_HEIGHT_PX = 320;
const MESSAGE_COLLAPSE_HEIGHT_CLASS = "max-h-[320px]";
const MESSAGE_COLLAPSE_OVERFLOW_EPSILON_PX = 2;

/**
 * LRM-572 / LRM-568 — Slack-style thread root: full body (not compact clamp);
 * top zone + border-b only — no brand spine/tint.「查看原消息」shares
 * `onViewParent` with the header「在 #频道 查看」link (close + highlight).
 */
export function ThreadRootPreview({
  message,
  currentUserId,
  ownName,
  onViewParent,
  onOpenAgent,
  onOpenMember,
}: {
  message: ChannelMessage;
  currentUserId: string | null;
  ownName?: string;
  onViewParent?: () => void;
  onOpenAgent?: OpenAgentPanelFn;
  onOpenMember?: (userId: string) => void;
}) {
  const { t } = useT("channels");
  const messageTime = useMessageTime();
  const {
    getActorName,
    getMemberHonor,
    getAgentFleetRank,
    getAgentHonorLevel,
  } = useActorName();
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
  // the main channel bubble. Human author → the LRM-619 member Profile dock.
  // (#488 / LRM-292)
  const handleOpenAgentCapture =
    isAgent && onOpenAgent && profileActorId
      ? () => onOpenAgent(profileActorId)
      : undefined;
  const handleOpenMemberCapture =
    !isAgent && onOpenMember && profileActorId
      ? () => onOpenMember(profileActorId)
      : undefined;
  const handleOpenProfileCapture = handleOpenAgentCapture ?? handleOpenMemberCapture;
  const avatarActorType = isAgent ? "agent" : "member";
  const authorHonor =
    message.type === "user" && profileActorId ? getMemberHonor(profileActorId) : undefined;
  const authorFleet =
    isAgent && profileActorId ? getAgentFleetRank(profileActorId) : undefined;
  const authorHonorLevel =
    isAgent && profileActorId ? getAgentHonorLevel(profileActorId) : undefined;
  const avatar = profileActorId ? (
    <ActorAvatar
      actorType={avatarActorType}
      actorId={profileActorId}
      size={30}
      fleetRank={authorFleet?.fleet_rank}
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
        onClickCapture={handleOpenProfileCapture}
      >
        {avatar}
      </ActorProfileTrigger>
    ) : (
      avatar
    );
  const nameNode = (
    <ActorStyledName
      displayName={displayName}
      honor={authorHonor}
      agentHonorLevel={authorHonorLevel}
      className="text-sm font-medium text-foreground"
    />
  );

  // Same long-message See more / See less as the main column (LRM-268 / LRM-572 / LRM-987).
  const collapseIdentity = `${message.id}\0${message.content ?? ""}\0${message.parts?.length ?? 0}\0${message.attachments?.length ?? 0}`;
  const { contentExpanded, expand, collapse } = useMessageContentExpanded(
    message.id,
    collapseIdentity,
  );
  const [contentOverflows, setContentOverflows] = useState(false);
  const messageBodyRef = useRef<HTMLDivElement | null>(null);

  const measureContentOverflow = useCallback(() => {
    const body = messageBodyRef.current;
    if (!body) {
      setContentOverflows(false);
      return;
    }
    const overflows =
      body.scrollHeight > MESSAGE_COLLAPSE_MAX_HEIGHT_PX + MESSAGE_COLLAPSE_OVERFLOW_EPSILON_PX;
    setContentOverflows((previous) => (previous === overflows ? previous : overflows));
  }, []);

  useLayoutEffect(() => {
    measureContentOverflow();
  }, [collapseIdentity, measureContentOverflow]);

  useLayoutEffect(() => {
    const body = messageBodyRef.current;
    if (!body || typeof ResizeObserver === "undefined") return;
    const observer = new ResizeObserver(() => measureContentOverflow());
    observer.observe(body);
    return () => observer.disconnect();
  }, [measureContentOverflow]);

  const canCollapseContent = contentOverflows;
  const isContentCollapsed = canCollapseContent && !contentExpanded;

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
                onClickCapture={handleOpenProfileCapture}
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
              <Time kind="message" value={message.created_at} title={false} />
            </span>
          </div>
          <div
            ref={messageBodyRef}
            className={cn(
              "message-surface relative mt-1 min-w-0 overflow-hidden text-sm leading-6 text-foreground",
              isContentCollapsed && "overflow-hidden",
              isContentCollapsed ? MESSAGE_COLLAPSE_HEIGHT_CLASS : "overflow-visible",
            )}
            data-testid="thread-root-body"
            data-collapsed={isContentCollapsed ? "true" : undefined}
          >
            <MessageBody
              content={message.content}
              parts={message.parts}
              attachments={message.attachments}
              sourceMessageId={message.id}
              consumedAttachmentIds={voicePresentation?.consumedAttachmentIds}
              contentMode={hidesVoiceTranscript ? "non-transcript" : "all"}
            />
            <VoiceMessageAudio message={message} presentation={voicePresentation} />
            {isContentCollapsed && (
              <div
                className={messageCollapseFadeClassName("default")}
                data-testid="thread-root-collapse-fade"
              >
                <button
                  type="button"
                  className="pointer-events-auto inline-flex min-h-8 items-center px-0 text-sm font-normal text-primary underline-offset-2 hover:underline focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
                  onClick={() => expand()}
                >
                  {t(($) => $.message.expand_action)}
                </button>
              </div>
            )}
          </div>
          {canCollapseContent && !isContentCollapsed && (
            <div className="mt-1 flex justify-start" data-testid="thread-root-collapse-less">
              <button
                type="button"
                className="inline-flex min-h-8 items-center px-0 text-sm font-normal text-primary underline-offset-2 hover:underline focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
                onClick={() => collapse()}
              >
                {t(($) => $.message.collapse_action)}
              </button>
            </div>
          )}
          {onViewParent && (
            <button
              type="button"
              className="mt-2 inline-flex min-h-8 items-center rounded-md text-xs font-medium text-primary underline-offset-4 hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
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
