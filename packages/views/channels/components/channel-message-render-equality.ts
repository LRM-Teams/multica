import type { ChannelMessage } from "@multica/core/types";

/** Stable JSON for optional nested message fields used in render. */
function stableJson(value: unknown): string {
  return JSON.stringify(value ?? null);
}

/**
 * Returns true when two channel messages would paint the same bubble body.
 * Used by {@link ChannelMessageBubble} memo to skip re-renders when Virtuoso
 * re-invokes itemContent for unrelated rows (LRM-286 optimization A).
 */
export function channelMessageRenderEqual(
  prev: ChannelMessage,
  next: ChannelMessage,
): boolean {
  if (prev.id !== next.id) return false;
  if (prev.content !== next.content) return false;
  if (prev.edited_at !== next.edited_at) return false;
  if (prev.deleted_at !== next.deleted_at) return false;
  if (prev.local_send_status !== next.local_send_status) return false;
  if (prev.author_name !== next.author_name) return false;
  if (prev.author_avatar_url !== next.author_avatar_url) return false;
  if (prev.thread_reply_count !== next.thread_reply_count) return false;
  if (prev.thread_unread_count !== next.thread_unread_count) return false;
  if (prev.thread_followed !== next.thread_followed) return false;
  if (prev.thread_last_reply_at !== next.thread_last_reply_at) return false;
  if (stableJson(prev.parts) !== stableJson(next.parts)) return false;
  if (stableJson(prev.reactions) !== stableJson(next.reactions)) return false;
  if (stableJson(prev.attachments) !== stableJson(next.attachments)) return false;
  if (stableJson(prev.quote) !== stableJson(next.quote)) return false;
  if (stableJson(prev.reply_to) !== stableJson(next.reply_to)) return false;
  if (stableJson(prev.thread_root) !== stableJson(next.thread_root)) return false;
  if (stableJson(prev.thread_participants) !== stableJson(next.thread_participants)) {
    return false;
  }
  return true;
}

export type ChannelMessageBubbleMemoProps = {
  message: ChannelMessage;
  currentUserId: string | null;
  ownName?: string;
  highlighted?: boolean;
  searchHighlighted?: boolean;
  searchQuery?: string;
  collapseLongContent?: boolean;
  compact?: boolean;
  onOpenThread?: unknown;
  onScrollTo?: unknown;
  onReact?: unknown;
  onQuote?: unknown;
  onEdit?: unknown;
  onDelete?: unknown;
  onOpenAgent?: unknown;
  onOpenMember?: unknown;
  onRetrySend?: unknown;
};

/** Props comparator for memoized {@link ChannelMessageBubble}. */
export function areChannelMessageBubblePropsEqual(
  prev: ChannelMessageBubbleMemoProps,
  next: ChannelMessageBubbleMemoProps,
): boolean {
  if (!channelMessageRenderEqual(prev.message, next.message)) return false;
  if (prev.currentUserId !== next.currentUserId) return false;
  if (prev.ownName !== next.ownName) return false;
  if (prev.highlighted !== next.highlighted) return false;
  if (prev.searchHighlighted !== next.searchHighlighted) return false;
  if (prev.searchQuery !== next.searchQuery) return false;
  if (prev.collapseLongContent !== next.collapseLongContent) return false;
  if (prev.compact !== next.compact) return false;
  if (prev.onOpenThread !== next.onOpenThread) return false;
  if (prev.onScrollTo !== next.onScrollTo) return false;
  if (prev.onReact !== next.onReact) return false;
  if (prev.onQuote !== next.onQuote) return false;
  if (prev.onEdit !== next.onEdit) return false;
  if (prev.onDelete !== next.onDelete) return false;
  if (prev.onOpenAgent !== next.onOpenAgent) return false;
  if (prev.onOpenMember !== next.onOpenMember) return false;
  if (prev.onRetrySend !== next.onRetrySend) return false;
  return true;
}

export type MessageBodyMemoProps = {
  content: string;
  parts?: ChannelMessage["parts"];
  attachments?: ChannelMessage["attachments"];
  highlightQuery?: string;
  compact?: boolean;
  sourceMessageId?: string;
  consumedAttachmentIds?: readonly string[];
  contentMode?: "all" | "transcript" | "non-transcript";
  choiceContext?: { channelId: string; messageId: string };
};

/** Props comparator for memoized {@link MessageBody}. */
export function areMessageBodyPropsEqual(
  prev: MessageBodyMemoProps,
  next: MessageBodyMemoProps,
): boolean {
  if (prev.content !== next.content) return false;
  if (prev.highlightQuery !== next.highlightQuery) return false;
  if (prev.compact !== next.compact) return false;
  if (prev.sourceMessageId !== next.sourceMessageId) return false;
  if (prev.contentMode !== next.contentMode) return false;
  if (stableJson(prev.parts) !== stableJson(next.parts)) return false;
  if (stableJson(prev.attachments) !== stableJson(next.attachments)) return false;
  if (stableJson(prev.consumedAttachmentIds) !== stableJson(next.consumedAttachmentIds)) {
    return false;
  }
  if (stableJson(prev.choiceContext) !== stableJson(next.choiceContext)) return false;
  return true;
}
