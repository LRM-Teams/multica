"use client";

import { useCallback, useState } from "react";
import { Copy, MessageSquare, Pencil, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { ReactionBar } from "@multica/ui/components/common/reaction-bar";
import { QuickEmojiPicker } from "@multica/ui/components/common/quick-emoji-picker";
import { ActorAvatar } from "@multica/ui/components/common/actor-avatar";
import { copyText } from "@multica/ui/lib/clipboard";
import { cn } from "@multica/ui/lib/utils";
import { useActorName } from "@multica/core/workspace/hooks";
import type { ChannelMessage } from "@multica/core/types";
import { AttachmentList } from "../../issues/components/comment-card";
import { agentColor } from "../../common/agent-color";
import { ActorProfileTrigger } from "../../common/actor-profile-popover";
import { AgentPresenceOverlay } from "../../common/actor-avatar";
import { initialsOf } from "../../common/initials";
import { useT } from "../../i18n/use-t";
import { useMessageTime } from "../../i18n/use-message-time";
import { resolveChannelAuthorDisplayName } from "./message-preview";
import {
  formatMessagePartsCopyText,
  formatMessagePartsPreview,
  resolveMessageParts,
  unwrapStructuredPreviewContent,
} from "./message-parts-preview";
import { MessageBody } from "./message-body";
import { isLegacyRuntimeSystemNotice } from "./runtime-system-notice";

function ChannelSystemMessageRow({
  message,
  highlighted,
  systemText,
}: {
  message: ChannelMessage;
  highlighted: boolean;
  systemText: string;
}) {
  const messageTime = useMessageTime();
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
      <span
        className="shrink-0 text-[11px] text-muted-foreground/70"
        title={messageTime.full(message.created_at)}
      >
        {messageTime.format(message.created_at)}
      </span>
    </div>
  );
}

/**
 * Inline single-message editor. Enter (without Shift) saves, Escape cancels —
 * a save calls back into the bubble's onEdit (a PATCH), never a re-send, so an
 * edit can never produce a new agent wake (H5).
 */
function MessageInlineEditor({
  value,
  onChange,
  onSave,
  onCancel,
  editLabel,
  saveLabel,
  cancelLabel,
}: {
  value: string;
  onChange: (next: string) => void;
  onSave: () => void;
  onCancel: () => void;
  editLabel: string;
  saveLabel: string;
  cancelLabel: string;
}) {
  // Move focus into the editor the user just opened (the Edit trigger it
  // replaced has unmounted). A stable ref callback focuses once on mount —
  // no autoFocus prop, no effect.
  const focusOnMount = useCallback((node: HTMLTextAreaElement | null) => {
    node?.focus();
  }, []);
  return (
    <div data-testid="message-editor" className="mt-0.5">
      <textarea
        ref={focusOnMount}
        aria-label={editLabel}
        value={value}
        onChange={(event) => onChange(event.target.value)}
        onKeyDown={(event) => {
          if (event.key === "Enter" && !event.shiftKey) {
            event.preventDefault();
            onSave();
          } else if (event.key === "Escape") {
            event.preventDefault();
            onCancel();
          }
        }}
        rows={2}
        className="w-full resize-none rounded-md border border-border bg-background px-2 py-1.5 text-sm leading-6 text-foreground outline-none focus-visible:ring-1 focus-visible:ring-ring"
      />
      <div className="mt-1.5 flex items-center gap-2">
        <button
          type="button"
          onClick={onSave}
          className="inline-flex h-7 items-center rounded-md bg-primary px-3 text-xs font-medium text-primary-foreground transition-colors hover:bg-primary/90 focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
        >
          {saveLabel}
        </button>
        <button
          type="button"
          onClick={onCancel}
          className="inline-flex h-7 items-center rounded-md px-3 text-xs font-medium text-muted-foreground transition-colors hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
        >
          {cancelLabel}
        </button>
      </div>
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
  onEdit,
  onDelete,
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
  /**
   * Save an inline edit of the viewer's own message. H5: this is an edit, never
   * a re-send — it must not go through a send/dispatch path (no new wake).
   */
  onEdit?: (message: ChannelMessage, content: string) => void;
  /** Soft-delete the viewer's own message; the bubble then renders a tombstone. */
  onDelete?: (message: ChannelMessage) => void;
  /** Search hit: marks matching visible text while search is open. */
  searchHighlighted?: boolean;
  /** Trimmed conversation search phrase to mark inside this hit's visible text. */
  searchQuery?: string;
}) {
  const { t } = useT("channels");
  const { getActorAvatarUrl, getActorName } = useActorName();
  const messageTime = useMessageTime();
  const [editDraft, setEditDraft] = useState<string | null>(null);

  if (message.deleted_at) {
    return (
      <div
        id={`message-${message.id}`}
        data-testid="message-tombstone"
        data-message-kind="deleted"
        className={cn(
          "mx-2 flex items-center gap-2 rounded-md px-2 py-1.5 text-sm italic text-muted-foreground outline-none transition-colors duration-1000",
          highlighted && "bg-primary/10 ring-1 ring-primary/25 duration-0",
        )}
      >
        <Trash2 className="size-3.5 shrink-0" />
        <span className="min-w-0 break-words">
          {t(($) => $.message.deleted_placeholder)}
        </span>
      </div>
    );
  }

  if (message.type === "system") {
    if (isLegacyRuntimeSystemNotice(message)) return null;

    const systemText = message.content.trim() || message.author_name || "System";
    return (
      <ChannelSystemMessageRow
        message={message}
        highlighted={highlighted}
        systemText={systemText}
      />
    );
  }

  const isOwn =
    message.type === "user" &&
    message.author_id != null &&
    message.author_id === currentUserId;
  const isAgent = message.type === "agent";
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
        : message.type === "user"
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
    message.type === "agent"
      ? "agent"
      : message.type === "user"
        ? "user"
        : null;
  const profileActorId = profileActorType ? message.author_id : null;
  // The 2px baseline nudge (`mt-0.5`) lives on the outer wrapper, not the
  // avatar itself, so that when the avatar sits inside the fixed-size presence
  // box the box hugs the avatar exactly (a margin on the inner avatar would
  // overflow the box and lift the dot off the avatar's bottom edge).
  const avatarNode = (
    <ActorAvatar
      name={displayName}
      initials={initialsOf(displayName)}
      avatarUrl={avatarUrl ?? undefined}
      isAgent={isAgent}
      isSystem={false}
      size={28}
      className="select-none"
      tint={tint}
    />
  );
  // Overlay the shared presence dot (breathing when the agent is actively
  // running a task, static otherwise) on agent authors only — members have no
  // presence backbone, system/lark authors aren't resolvable actors. Routed
  // through the single, stretch-proof `AgentPresenceOverlay` so the dot can't
  // detach in this CSS-grid row (root cause of the detached-dot bug).
  const avatar =
    isAgent && message.author_id != null ? (
      <AgentPresenceOverlay agentId={message.author_id} size={28} className="mt-0.5">
        {avatarNode}
      </AgentPresenceOverlay>
    ) : (
      <span className="mt-0.5 inline-flex shrink-0">{avatarNode}</span>
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
  // Resolved once here for copy + attachment de-dupe; `MessageBody` resolves the
  // same way to render the body (see resolveMessageParts for the envelope-unwrap
  // rationale). Non-null for real parts / historical envelopes, null for
  // ordinary content.
  const effectiveParts = resolveMessageParts(message.content, message.parts);
  const handleCopy = async () => {
    const copyPayload =
      formatMessagePartsCopyText(effectiveParts) ??
      unwrapStructuredPreviewContent(message.content) ??
      message.content;
    if (await copyText(copyPayload)) {
      toast.success(t(($) => $.message.copied_toast));
    } else {
      toast.error(t(($) => $.message.copy_failed_toast));
    }
  };

  // Edit / delete are viewer-own affordances only. H5: saving an edit routes
  // through onEdit (a PATCH), never a re-send — it cannot produce a new wake.
  const isEditing = editDraft !== null;
  // Edit unshipped 2026-07-05 (Frank/Miles): the inline editor is a plain
  // textarea with no @mention/composer parity; hidden until rebuilt on the
  // unified composer (#258 backlog). onEdit/MessageInlineEditor kept dormant for
  // that rebuild. Delete stays.
  const canEdit = false;
  const canDelete = isOwn && !!onDelete;
  const isEdited = !!message.edited_at;
  const handleStartEdit = () => setEditDraft(message.content);
  const handleCancelEdit = () => setEditDraft(null);
  const handleSaveEdit = () => {
    const next = (editDraft ?? "").trim();
    if (next && next !== message.content) {
      onEdit?.(message, next);
    }
    setEditDraft(null);
  };
  const handleDelete = () => onDelete?.(message);

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
          <span
            className="shrink-0 text-[11px] text-muted-foreground"
            title={messageTime.full(message.created_at)}
          >
            {messageTime.format(message.created_at)}
          </span>
          {isEdited && (
            <span
              data-testid="message-edited"
              className="shrink-0 text-[11px] text-muted-foreground/70"
            >
              {t(($) => $.message.edited_label)}
            </span>
          )}
        </div>
        {!isEditing && (
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
          {canEdit && (
            <button
              type="button"
              onClick={handleStartEdit}
              className="inline-flex size-7 items-center justify-center rounded-md transition-colors hover:bg-background/70 hover:text-foreground focus-visible:bg-background/70 focus-visible:text-foreground"
              aria-label={t(($) => $.message.edit_action)}
              title={t(($) => $.message.edit_action)}
            >
              <Pencil className="size-3.5" />
            </button>
          )}
          {canDelete && (
            <button
              type="button"
              onClick={handleDelete}
              className="inline-flex size-7 items-center justify-center rounded-md transition-colors hover:bg-destructive/10 hover:text-destructive focus-visible:bg-destructive/10 focus-visible:text-destructive"
              aria-label={t(($) => $.message.delete_action)}
              title={t(($) => $.message.delete_action)}
            >
              <Trash2 className="size-3.5" />
            </button>
          )}
        </div>
        )}
        {isEditing ? (
          <MessageInlineEditor
            value={editDraft ?? ""}
            onChange={setEditDraft}
            onSave={handleSaveEdit}
            onCancel={handleCancelEdit}
            editLabel={t(($) => $.message.edit_action)}
            saveLabel={t(($) => $.message.save_edit)}
            cancelLabel={t(($) => $.message.cancel_edit)}
          />
        ) : (
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
                {formatMessagePartsPreview(message.reply_to.parts) ??
                  unwrapStructuredPreviewContent(message.reply_to.content) ??
                  message.reply_to.content}
              </p>
            </button>
          )}
          <MessageBody
            content={message.content}
            parts={message.parts}
            attachments={message.attachments}
            highlightQuery={searchHighlighted ? searchQuery : undefined}
          />
          <AttachmentList
            attachments={message.attachments}
            content={effectiveParts ? "" : message.content}
            className="mt-1.5"
          />
        </div>
        )}
        {!isEditing && hasFeedback && (
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
