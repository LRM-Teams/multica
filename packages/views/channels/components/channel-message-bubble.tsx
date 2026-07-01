"use client";

import { MessageSquare, MoreHorizontal, Reply } from "lucide-react";
import { ReactionBar } from "@multica/ui/components/common/reaction-bar";
import { ActorAvatar } from "@multica/ui/components/common/actor-avatar";
import {
  ContextMenu,
  ContextMenuContent,
  ContextMenuItem,
  ContextMenuTrigger,
} from "@multica/ui/components/ui/context-menu";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@multica/ui/components/ui/dropdown-menu";
import { cn } from "@multica/ui/lib/utils";
import { useActorName } from "@multica/core/workspace/hooks";
import type { ChannelMessage } from "@multica/core/types";
import { MemoizedMarkdown } from "../../common/markdown";
import { AttachmentList } from "../../issues/components/comment-card";
import { agentColor } from "../../common/agent-color";
import { ActorProfileTrigger } from "../../common/actor-profile-popover";
import { initialsOf } from "../../common/initials";
import { useT } from "../../i18n";

function hasTextSelectionWithin(element: HTMLElement): boolean {
  const selection = window.getSelection();
  if (!selection || selection.isCollapsed || !selection.toString().trim()) {
    return false;
  }

  const { anchorNode, focusNode } = selection;
  return (
    (anchorNode != null && element.contains(anchorNode)) ||
    (focusNode != null && element.contains(focusNode))
  );
}

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

/**
 * One message in the group-chat thread. Own messages right-align with a neutral
 * card bubble; everyone else left-aligns. Agents get a per-agent identity color
 * on the avatar plus an "Agent" pill, and a faint-primary bubble so agent
 * output reads as distinct from human messages at a glance. Body is rendered
 * through the shared Markdown pipeline (mentions, attachments, light formatting).
 *
 * Desktop: right-click → context menu (Reply).
 * Mobile: long-press → context menu (ContextMenu handles this natively).
 * Keyboard: ContextMenu via Menu/Shift+F10 is the accessible path.
 */
export function ChannelMessageBubble({
  message,
  currentUserId,
  ownName,
  highlighted = false,
  onQuote,
  onOpenThread,
  onScrollTo,
  onReact,
  searchHighlighted = false,
  searchQuery,
}: {
  message: ChannelMessage;
  currentUserId: string | null;
  /** Display name for the viewer's own messages. Defaults to the stored name. */
  ownName?: string;
  /** Deep-link target: briefly ring-highlights the message, then fades. */
  highlighted?: boolean;
  /** Called when the user triggers "Reply" on this message. */
  onQuote?: (message: ChannelMessage) => void;
  /** Called when the user opens the message's side thread. */
  onOpenThread?: (message: ChannelMessage) => void;
  /** Called when the user clicks the inline quote block to jump to the original. */
  onScrollTo?: (messageId: string) => void;
  /** Toggle/add a lightweight emoji reaction on this message. */
  onReact?: (message: ChannelMessage, emoji: string) => void;
  /** Search hit: marks matching visible text while search is open. */
  searchHighlighted?: boolean;
  /** Trimmed conversation search phrase to mark inside this hit's visible text. */
  searchQuery?: string;
}) {
  const { t } = useT("channels");
  const { getActorAvatarUrl, getActorName } = useActorName();
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
  // isn't a workspace member/agent (lark / system) or has no photo.
  const avatarUrl =
    message.author_id == null
      ? null
      : isAgent
        ? getActorAvatarUrl("agent", message.author_id)
        : message.author_type === "user"
          ? getActorAvatarUrl("member", message.author_id)
          : null;
  const displayName = isOwn ? ownName ?? message.author_name : message.author_name;
  const profileActorType =
    message.author_type === "agent"
      ? "agent"
      : message.author_type === "user"
        ? "user"
        : null;
  const profileActorId = profileActorType ? message.author_id : null;
  const avatar = (
    <ActorAvatar
      name={message.author_name}
      initials={initialsOf(message.author_name)}
      avatarUrl={avatarUrl ?? undefined}
      isAgent={isAgent}
      isSystem={message.author_type === "system"}
      size={28}
      className="mt-0.5 select-none"
      tint={tint}
    />
  );
  const nameLabel = (
    <span className="truncate font-medium text-foreground">{displayName}</span>
  );

  const canQuote = !!onQuote;
  const canOpenThread = !!onOpenThread && !message.thread_root_message_id;
  const threadReplyCount = message.thread_reply_count ?? 0;
  const threadUnreadCount = message.thread_unread_count ?? 0;
  const hasThreadActivity = threadReplyCount > 0 || threadUnreadCount > 0;

  return (
    <ContextMenu>
      <ContextMenuTrigger
        className="select-text"
        render={
          <div
            id={`message-${message.id}`}
            data-testid="message-bubble"
            data-own={isOwn}
            tabIndex={0}
            className={cn(
              "group relative flex select-text gap-2.5 px-1 py-1.5 outline-none transition-colors duration-1000",
              isOwn ? "flex-row-reverse" : "flex-row",
              highlighted && "rounded-lg bg-primary/10 ring-1 ring-primary/25 duration-0",
            )}
          />
        }
      >
        {profileActorType && profileActorId ? (
          <ActorProfileTrigger
            memberType={profileActorType}
            memberId={profileActorId}
          >
            {avatar}
          </ActorProfileTrigger>
        ) : (
          avatar
        )}
        <div
          className={cn(
            "flex min-w-0 max-w-[82%] flex-col gap-1 md:max-w-[min(680px,68%)]",
            isOwn && "items-end",
          )}
        >
          <div
            className={cn(
              "flex select-none items-center gap-2 text-sm",
              isOwn && "flex-row-reverse",
            )}
          >
            {profileActorType && profileActorId ? (
              <ActorProfileTrigger
                memberType={profileActorType}
                memberId={profileActorId}
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
          <div
            className={cn(
              "w-fit min-w-0 max-w-full select-text overflow-hidden break-words rounded-lg border px-3 py-2 text-sm leading-6 shadow-none",
              isOwn
                ? "border-border/35 bg-background"
                : isAgent
                  ? "border-primary/10 bg-primary/[0.035]"
                  : "border-border/35 bg-muted/30",
            )}
            data-testid="message-body"
            onContextMenuCapture={(e) => {
              if (hasTextSelectionWithin(e.currentTarget)) {
                e.stopPropagation();
              }
            }}
            onTouchStartCapture={(e) => {
              if (e.target !== e.currentTarget) {
                e.stopPropagation();
              }
            }}
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
                  {message.reply_to.author_name}
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
          {onReact && (
            <ReactionBar
              reactions={message.reactions ?? []}
              currentUserId={currentUserId ?? undefined}
              onToggle={(emoji) => onReact(message, emoji)}
              getActorName={getActorName}
              className={cn(isOwn && "justify-end")}
              quickEmojis={["👍", "❤️", "💯", "🎉"]}
            />
          )}
          {(canOpenThread || hasThreadActivity) && onOpenThread && (
            <div
              className={cn(
                "flex w-fit items-center gap-1",
                isOwn && "flex-row-reverse",
              )}
            >
              {hasThreadActivity && (
                <button
                  type="button"
                  onClick={() => onOpenThread(message)}
                  className={cn(
                    "flex w-fit items-center gap-1.5 rounded-md px-1.5 py-0.5 text-xs text-muted-foreground transition-colors hover:bg-accent hover:text-foreground",
                    threadUnreadCount > 0 && "font-medium text-primary",
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
              {canOpenThread && (
                <DropdownMenu>
                  <DropdownMenuTrigger
                    className="flex size-7 items-center justify-center rounded-md text-muted-foreground transition hover:bg-accent hover:text-foreground data-[state=open]:opacity-100 md:size-6 md:opacity-0 md:group-hover:opacity-100 md:group-focus-within:opacity-100"
                    aria-label={t(($) => $.quote.more_aria)}
                  >
                    <MoreHorizontal className="size-3.5" />
                  </DropdownMenuTrigger>
                  <DropdownMenuContent align={isOwn ? "start" : "end"}>
                    <DropdownMenuItem onClick={() => onOpenThread(message)}>
                      <MessageSquare className="size-4" />
                      {t(($) => $.thread.reply)}
                    </DropdownMenuItem>
                    {canQuote && (
                      <DropdownMenuItem onClick={() => onQuote?.(message)}>
                        <Reply className="size-4" />
                        {t(($) => $.quote.reply)}
                      </DropdownMenuItem>
                    )}
                  </DropdownMenuContent>
                </DropdownMenu>
              )}
            </div>
          )}
        </div>
      </ContextMenuTrigger>

      {/* Right-click / long-press context menu */}
      {(canQuote || canOpenThread) && (
        <ContextMenuContent>
          {canOpenThread && (
            <ContextMenuItem onClick={() => onOpenThread(message)}>
              <MessageSquare className="size-4" />
              {t(($) => $.thread.reply)}
            </ContextMenuItem>
          )}
          {canQuote && (
            <ContextMenuItem onClick={() => onQuote(message)}>
              <Reply className="size-4" />
              {t(($) => $.quote.reply)}
            </ContextMenuItem>
          )}
        </ContextMenuContent>
      )}
    </ContextMenu>
  );
}
