"use client";

import {
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { Virtuoso, type VirtuosoHandle } from "react-virtuoso";
import type { ChannelMessage } from "@multica/core/types";
import { ChannelMessageBubble } from "./channel-message-bubble";

/**
 * Virtualized message list shared by the group conversation (channels-page)
 * and the DM channel conversation (dm-conversation). Both previously rendered
 * the full `messages` array with a plain `.map()`, mounting every bubble — the
 * root cause of the switch/scroll lag in busy channels. This windows the list
 * with `react-virtuoso` (same engine as the legacy chat list), so only the
 * visible bubbles plus a small buffer are mounted.
 *
 * The composer/agent-working/typing affordances stay in the caller; only the
 * scrolling message area is owned here. `footer` renders below the last message
 * (inside the scroll window) so the agent-working / typing indicators ride the
 * bottom-stick along with new messages.
 *
 * Opens scrolled to the latest message (chat convention), or to a deep-linked
 * `highlightMessageId` when present. `initialTopMostItemIndex` is mount-only, so
 * **callers must `key` this by conversation id** — otherwise switching
 * conversations keeps the previous scroll offset instead of jumping to the
 * newest message.
 */
function MessageViewport({
  messages,
  currentUserId,
  ownName,
  highlightMessageId,
  emptyLabel,
  footer,
  onQuote,
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
  /** Called when the user triggers Reply on a message (quote-reply flow). */
  onQuote?: (message: ChannelMessage) => void;
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
  const virtuosoRef = useRef<VirtuosoHandle>(null);
  const [isNearBottom, setIsNearBottom] = useState(true);

  // Index of the deep-link target, or -1 when none / not loaded yet.
  const highlightIndex = useMemo(() => {
    if (!highlightMessageId) return -1;
    return messages.findIndex((m) => m.id === highlightMessageId);
  }, [messages, highlightMessageId]);

  // Cold deep-link opens centered on the target; otherwise open at the latest
  // message. Mount-only — see the `key`-per-conversation contract above.
  const initialIndex =
    highlightIndex >= 0 ? highlightIndex : Math.max(0, messages.length - 1);

  // A deep link that arrives after mount (user clicks a message link while the
  // conversation is already open) can't rely on the mount-only initial index —
  // scroll to it imperatively once it's in the loaded set.
  useEffect(() => {
    if (highlightIndex < 0) return;
    virtuosoRef.current?.scrollToIndex({
      index: highlightIndex,
      align: "center",
      behavior: "smooth",
    });
  }, [highlightIndex]);

  // Empty thread: render the placeholder + live affordances directly (no
  // Virtuoso). react-virtuoso skips Header/Footer when `data` is empty, and the
  // previous plain-map rendering always kept the agent-working / typing
  // indicators visible even before the first message — preserve that.
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
    <div className="min-h-0 min-w-0 flex-1">
        <Virtuoso
          ref={virtuosoRef}
          style={{ height: "100%" }}
          data={messages}
          initialTopMostItemIndex={initialIndex}
          increaseViewportBy={{ top: 400, bottom: 600 }}
          atBottomThreshold={120}
          atBottomStateChange={setIsNearBottom}
          // Stick to the bottom only while the user is already near it, so an
          // incoming message doesn't yank a user who scrolled up to read.
          followOutput={() => (isNearBottom ? "smooth" : false)}
          computeItemKey={(_, msg) => msg.id}
          components={{
            Header: () => <div className="pt-3" />,
            Footer: () =>
              footer ? <div className="px-5 pb-5 pt-2">{footer}</div> : <div className="pb-5" />,
          }}
          itemContent={(_, msg) => {
            const searchHighlighted = searchHitIds?.has(msg.id) ?? false;
            return (
              <div className="px-5 pt-1.5">
                <ChannelMessageBubble
                  message={msg}
                  currentUserId={currentUserId}
                  ownName={ownName}
                  highlighted={msg.id === highlightMessageId}
                  onQuote={onQuote}
                  onOpenThread={onOpenThread}
                  onScrollTo={onScrollToMessage}
                  onReact={onReact}
                  searchHighlighted={searchHighlighted}
                  searchQuery={searchHighlighted ? searchQuery : undefined}
                />
              </div>
            );
          }}
        />
    </div>
  );
}

export { MessageViewport, MessageViewport as ChannelMessageList };
