"use client";

import { Copy, MessageSquare } from "lucide-react";
import { toast } from "sonner";
import { ReactionBar } from "@multica/ui/components/common/reaction-bar";
import { QuickEmojiPicker } from "@multica/ui/components/common/quick-emoji-picker";
import { ActorAvatar } from "@multica/ui/components/common/actor-avatar";
import { copyText } from "@multica/ui/lib/clipboard";
import { cn } from "@multica/ui/lib/utils";
import { useActorName } from "@multica/core/workspace/hooks";
import type { ChannelMessage } from "@multica/core/types";
import { MemoizedMarkdown } from "../../common/markdown";
import { AttachmentList } from "../../issues/components/comment-card";
import { agentColor } from "../../common/agent-color";
import { ActorProfileTrigger } from "../../common/actor-profile-popover";
import { initialsOf } from "../../common/initials";
import { useT } from "../../i18n/use-t";
import { resolveChannelAuthorDisplayName } from "./message-preview";

type RuntimeNoticeState = "outdated" | "missing" | "disconnected";

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

function runtimeNoticeStateFromMessage(message: ChannelMessage): RuntimeNoticeState | null {
  switch (message.system_message_kind) {
    case "runtime_outdated":
      return "outdated";
    case "runtime_missing":
      return "missing";
    case "runtime_disconnected":
      return "disconnected";
    default:
      return null;
  }
}

function ChannelSystemMessageRow({
  message,
  highlighted,
  runtimeNoticeState,
  systemText,
  openRuntimesLabel,
  onOpenRuntimes,
}: {
  message: ChannelMessage;
  highlighted: boolean;
  runtimeNoticeState: RuntimeNoticeState | null;
  systemText: string;
  openRuntimesLabel: string;
  onOpenRuntimes?: () => void;
}) {
  return (
    <div
      id={`message-${message.id}`}
      data-testid="system-message-row"
      data-message-kind="system"
      className={cn(
        "mx-auto flex max-w-[min(720px,100%)] flex-wrap items-center justify-center gap-x-2 gap-y-1 rounded-md px-2 py-1.5 text-center text-xs text-muted-foreground outline-none transition-colors duration-1000",
        highlighted && "bg-primary/10 ring-1 ring-primary/25 duration-0",
      )}
    >
      <span className="min-w-0 break-words">{systemText}</span>
      {runtimeNoticeState && onOpenRuntimes && (
        <button
          type="button"
          className="inline-flex h-6 shrink-0 items-center gap-1 rounded-md border border-border/70 bg-background/80 px-2 text-[11px] font-medium text-foreground transition-colors hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
          onClick={onOpenRuntimes}
        >
          <span>{openRuntimesLabel}</span>
        </button>
      )}
      <span className="shrink-0 text-[11px] text-muted-foreground/70">
        {formatTime(message.created_at)}
      </span>
    </div>
  );
}

/**
 * One message in the shared Channel/DM/Thread timeline. Ordinary text renders
 * as an IM-style message item, while quote/attachment/code-like content keeps
 * local structure inside the shared Markdown pipeline.
 */
export function ChannelMessageBubble({
  message,
  currentUserId,
  ownName,
  highlighted = false,
  onOpenThread,
  onScrollTo,
  onReact,
  onOpenRuntimes,
  searchHighlighted = false,
  searchQuery,
}: {
  message: ChannelMessage;
  currentUserId: string | null;
  /** Display name for the viewer's own messages. Defaults to the stored name. */
  ownName?: string;
  /** Deep-link target: briefly ring-highlights the message, then fades. */
  highlighted?: boolean;
  /** Called when the user opens the message's side thread. */
  onOpenThread?: (message: ChannelMessage) => void;
  /** Called when the user clicks the inline quote block to jump to the original. */
  onScrollTo?: (messageId: string) => void;
  /** Toggle/add a lightweight emoji reaction on this message. */
  onReact?: (message: ChannelMessage, emoji: string) => void;
  /** Opens the workspace runtime settings for actionable system notices. */
  onOpenRuntimes?: () => void;
  /** Search hit: marks matching visible text while search is open. */
  searchHighlighted?: boolean;
  /** Trimmed conversation search phrase to mark inside this hit's visible text. */
  searchQuery?: string;
}) {
  const { t } = useT("channels");
  const { getActorAvatarUrl, getActorName } = useActorName();
  const runtimeNoticeState = runtimeNoticeStateFromMessage(message);

  if (message.author_type === "system") {
    const systemText = runtimeNoticeState
      ? t(($) => $.daemon_notice[runtimeNoticeState])
      : message.content.trim() || message.author_name || "System";

    return (
      <ChannelSystemMessageRow
        message={message}
        highlighted={highlighted}
        runtimeNoticeState={runtimeNoticeState}
        systemText={systemText}
        openRuntimesLabel={t(($) => $.daemon_notice.open_runtimes)}
        onOpenRuntimes={onOpenRuntimes}
      />
    );
  }

  const isOwn =
    message.author_type === "user" &&
    message.author_id != null &&
    message.author_id === currentUserId;
  const isAgent = message.author_type === "agent";
  const isExternal = message.source === "lark";
  const tint = isAgent
    ? agentColor(message.author_id ?? message.author_name)
    : undefined;
  // Resolve the avatar from the live members/agents cache (keyed by id) rather
  // than a value snapshotted into the message — so a settings avatar change
  // shows up here too. Falls back to the tinted/initials avatar when the author
  // isn't a workspace member/agent (lark) or has no photo.
  const avatarUrl =
    message.author_id == null
      ? null
      : isAgent
        ? getActorAvatarUrl("agent", message.author_id)
        : message.author_type === "user"
          ? getActorAvatarUrl("member", message.author_id)
          : null;
  const displayName = resolveChannelAuthorDisplayName(message, {
    currentUserId,
    ownName,
    getActorName,
  });
  const replyAuthorName = message.reply_to
    ? resolveChannelAuthorDisplayName(message.reply_to, {
        currentUserId,
        ownName,
        getActorName,
      })
    : "";
  const profileActorType =
    message.author_type === "agent"
      ? "agent"
      : message.author_type === "user"
        ? "user"
        : null;
  const profileActorId = profileActorType ? message.author_id : null;
  const avatar = (
    <ActorAvatar
      name={displayName}
      initials={initialsOf(displayName)}
      avatarUrl={avatarUrl ?? undefined}
      isAgent={isAgent}
      isSystem={false}
      size={28}
      className="mt-0.5 select-none"
      tint={tint}
    />
  );
  const nameLabel = (
    <span className="truncate font-medium text-foreground">{displayName}</span>
  );

  const canOpenThread = !!onOpenThread && !message.thread_root_message_id;
  const threadReplyCount = message.thread_reply_count ?? 0;
  const threadUnreadCount = message.thread_unread_count ?? 0;
  const hasThreadActivity = threadReplyCount > 0 || threadUnreadCount > 0;
  const hasFeedback = (message.reactions?.length ?? 0) > 0 || hasThreadActivity;
  const quickReactionEmojis = ["👍", "👎", "😄", "🎉", "😕", "❤️", "🚀", "👀"];
  const handleCopy = async () => {
    if (await copyText(message.content)) {
      toast.success(t(($) => $.message.copied_toast));
    } else {
      toast.error(t(($) => $.message.copy_failed_toast));
    }
  };

  return (
    <div
      id={`message-${message.id}`}
      data-testid="message-bubble"
      data-own={isOwn}
      className={cn(
        "group relative grid grid-cols-[28px_minmax(0,1fr)] gap-2.5 rounded-lg px-2 py-1.5 outline-none transition-colors duration-1000 hover:bg-muted/35 focus-within:bg-muted/35",
        highlighted && "bg-primary/10 ring-1 ring-primary/25 duration-0",
      )}
    >
      {profileActorType && profileActorId ? (
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
      )}
      <div className="min-w-0 max-w-[min(760px,100%)]">
        <div className="mb-0.5 flex select-none items-baseline gap-2 pr-24 text-sm">
          {profileActorType && profileActorId ? (
            <ActorProfileTrigger
              memberType={profileActorType}
              memberId={profileActorId}
              side="top"
              sideOffset={8}
            >
              {nameLabel}
            </ActorProfileTrigger>
          ) : (
            nameLabel
          )}
          {isAgent && (
            <span className="shrink-0 rounded-full border border-primary/20 bg-primary/[0.08] px-2 py-0.5 text-[11px] font-normal leading-none text-primary">
              {t(($) => $.message.agent_badge)}
            </span>
          )}
          {isExternal && (
            <span className="shrink-0 rounded-full border bg-muted px-2 py-0.5 text-[11px] leading-none text-muted-foreground">
              {t(($) => $.message.feishu_badge)}
            </span>
          )}
          <span className="shrink-0 text-[11px] text-muted-foreground">
            {formatTime(message.created_at)}
          </span>
        </div>
        <div className="pointer-events-none absolute right-3 top-2 z-10 flex items-center gap-0.5 text-muted-foreground opacity-0 transition-opacity group-hover:pointer-events-auto group-hover:opacity-100 group-focus-within:pointer-events-auto group-focus-within:opacity-100">
          {onReact && (
            <QuickEmojiPicker
              onSelect={(emoji) => onReact(message, emoji)}
              align="end"
              side="bottom"
              className="size-7 rounded-md hover:bg-background/70 hover:text-foreground focus-visible:bg-background/70 focus-visible:text-foreground"
              ariaLabel={t(($) => $.message.add_reaction)}
              sideOffset={4}
              emojis={quickReactionEmojis}
              showMore={false}
              contentClassName="rounded-md border border-border/70 bg-popover/95 shadow-none ring-0"
            />
          )}
          <button
            type="button"
            onClick={handleCopy}
            className="inline-flex size-7 items-center justify-center rounded-md transition-colors hover:bg-background/70 hover:text-foreground focus-visible:bg-background/70 focus-visible:text-foreground"
            aria-label={t(($) => $.message.copy_action)}
            title={t(($) => $.message.copy_action)}
          >
            <Copy className="size-3.5" />
          </button>
          {canOpenThread && (
            <button
              type="button"
              onClick={() => onOpenThread?.(message)}
              className="inline-flex size-7 items-center justify-center rounded-md transition-colors hover:bg-background/70 hover:text-foreground focus-visible:bg-background/70 focus-visible:text-foreground"
              aria-label={t(($) => $.thread.reply)}
              title={t(($) => $.thread.reply)}
            >
              <MessageSquare className="size-3.5" />
            </button>
          )}
        </div>
        <div
          className={cn(
            "min-w-0 max-w-full select-text overflow-hidden break-words text-sm leading-6 text-foreground",
            searchHighlighted && "rounded-md bg-primary/5",
          )}
          data-testid="message-body"
          style={{ WebkitTouchCallout: "default" }}
        >
          {/* Inline quote block: rendered when reply_to is present (BE task #23) */}
          {message.reply_to && (
            <button
              type="button"
              onClick={() =>
                message.reply_to_message_id && onScrollTo?.(message.reply_to_message_id)
              }
              className="mb-2 w-full cursor-pointer rounded border-l-2 border-muted-foreground/30 bg-muted/30 px-2 py-1 text-left transition-opacity hover:opacity-80"
              aria-label={t(($) => $.quote.jump_to)}
            >
              <p className="truncate text-[11px] font-semibold text-foreground/70">
                {replyAuthorName}
              </p>
              <p className="line-clamp-1 text-[11px] text-muted-foreground">
                {message.reply_to.content}
              </p>
            </button>
          )}
          <MemoizedMarkdown
            attachments={message.attachments}
            highlightQuery={searchHighlighted ? searchQuery : undefined}
          >
            {message.content}
          </MemoizedMarkdown>
          <AttachmentList
            attachments={message.attachments}
            content={message.content}
            className="mt-1.5"
          />
        </div>
        {hasFeedback && (
          <div className="mt-2 flex flex-wrap items-center gap-1.5">
            {hasThreadActivity && onOpenThread && (
              <button
                type="button"
                onClick={() => onOpenThread(message)}
                className={cn(
                  "inline-flex h-7 items-center gap-1.5 rounded-md border border-border/80 bg-muted/45 px-2 text-xs font-medium text-foreground transition-colors hover:bg-muted focus-visible:bg-muted focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring md:h-6",
                  threadUnreadCount > 0 && "border-primary/35 bg-primary/10 text-primary",
                )}
              >
                <MessageSquare className="size-3.5" />
                <span>{t(($) => $.thread.reply_count, { count: threadReplyCount })}</span>
                {threadUnreadCount > 0 && (
                  <span className="rounded-full bg-primary px-1.5 py-0.5 text-[10px] leading-none text-primary-foreground">
                    {threadUnreadCount}
                  </span>
                )}
              </button>
            )}
            {onReact && (message.reactions?.length ?? 0) > 0 && (
              <ReactionBar
                reactions={message.reactions ?? []}
                currentUserId={currentUserId ?? undefined}
                onToggle={(emoji) => onReact(message, emoji)}
                getActorName={getActorName}
                hideAddButton
                showQuickReactions={false}
              />
            )}
          </div>
        )}
      </div>
    </div>
  );
}
