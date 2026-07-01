"use client";

import {
  useEffect,
  useMemo,
  useRef,
  type ReactNode,
} from "react";
import type { ChannelMessage } from "@multica/core/types";
import { ChannelMessageBubble } from "./channel-message-bubble";

/**
 * Virtualized message list shared by the group conversation (channels-page)
 * and the DM channel conversation (dm-conversation).
 *
 * This intentionally renders rows directly instead of relying on
 * `react-virtuoso`: s89 showed multiple production deploys where the API
 * returned messages but Virtuoso mounted an empty item list (height 0,
 * children 0), blanking the entire conversation. The P0 contract is stricter
 * than windowing performance: when messages are present, message bubbles must
 * exist in the DOM.
 *
 * The composer/agent-working/typing affordances stay in the caller; only the
 * scrolling message area is owned here. `footer` renders below the last message
 * (inside the scroll window) so the agent-working / typing indicators ride the
 * bottom-stick along with new messages.
 *
 * Opens scrolled to the latest message (chat convention), or to a deep-linked
 * `highlightMessageId` when present.
 */
function MessageViewport({
  messages,
  currentUserId,
  ownName,
  highlightMessageId,
  emptyLabel,
  footer,
  onOpenThread,
  onScrollToMessage,
  onReact,
  searchHitIds,
  searchQuery,
}: {
  messages: ChannelMessage[];
  currentUserId: string | null;
  /** Display name for the viewer's own messages. */
  ownName?: string;
  /** Deep-link target id — scrolls to and ring-highlights that bubble. */
  highlightMessageId?: string | null;
  /** Centered placeholder shown when there are no messages yet. */
  emptyLabel: string;
  /** Live affordances (agent-working / typing) pinned beneath the last message. */
  footer?: ReactNode;
  /** Called when the user opens the message's side thread. */
  onOpenThread?: (message: ChannelMessage) => void;
  /**
   * Called when the user clicks an inline quote block to jump to the original.
   * The parent updates `highlightMessageId` so the list scrolls + highlights.
   */
  onScrollToMessage?: (messageId: string) => void;
  /** Toggle/add a lightweight emoji reaction on this message. */
  onReact?: (message: ChannelMessage, emoji: string) => void;
  /** Search hit ids — all matching messages get inline keyword marks while search is open. */
  searchHitIds?: Set<string>;
  /** Conversation search phrase used for inline keyword marks within search hits. */
  searchQuery?: string;
}) {
  const scrollRef = useRef<HTMLDivElement>(null);
  const messageRefs = useRef(new Map<string, HTMLDivElement>());

  const highlightIndex = useMemo(() => {
    if (!highlightMessageId) return -1;
    return messages.findIndex((m) => m.id === highlightMessageId);
  }, [messages, highlightMessageId]);

  useEffect(() => {
    if (highlightMessageId) {
      if (highlightIndex < 0) return;
      messageRefs.current.get(highlightMessageId)?.scrollIntoView({
        block: "center",
        behavior: "smooth",
      });
      return;
    }
    const scroller = scrollRef.current;
    if (!scroller) return;
    scroller.scrollTop = scroller.scrollHeight;
  }, [highlightIndex, highlightMessageId, messages.length]);

  // Empty thread: render the placeholder + live affordances directly (no
  // message rows). The previous plain-map rendering always kept the
  // agent-working / typing indicators visible even before the first message —
  // preserve that.
  if (messages.length === 0) {
    return (
      <div className="flex min-h-0 min-w-0 flex-1 flex-col overflow-y-auto px-5 pb-5 pt-3">
        <div className="flex flex-1 items-center justify-center text-sm text-muted-foreground">
          {emptyLabel}
        </div>
        {footer}
      </div>
    );
  }

  return (
    <div
      ref={scrollRef}
      className="virtuoso-scroller min-h-0 min-w-0 flex-1 overflow-y-auto"
      data-testid="message-scroller"
    >
      <div className="virtuoso-item-list pt-3" data-testid="message-item-list">
        {messages.map((msg) => {
          const searchHighlighted = searchHitIds?.has(msg.id) ?? false;
          return (
            <div
              key={msg.id}
              ref={(node) => {
                if (node) {
                  messageRefs.current.set(msg.id, node);
                } else {
                  messageRefs.current.delete(msg.id);
                }
              }}
              className="px-5 pt-1.5"
              data-testid="message-row"
            >
              <ChannelMessageBubble
                message={msg}
                currentUserId={currentUserId}
                ownName={ownName}
                highlighted={msg.id === highlightMessageId}
                onOpenThread={onOpenThread}
                onScrollTo={onScrollToMessage}
                onReact={onReact}
                searchHighlighted={searchHighlighted}
                searchQuery={searchHighlighted ? searchQuery : undefined}
              />
            </div>
          );
        })}
        {footer ? <div className="px-5 pb-5 pt-2">{footer}</div> : <div className="pb-5" />}
      </div>
    </div>
  );
}

export { MessageViewport, MessageViewport as ChannelMessageList };
