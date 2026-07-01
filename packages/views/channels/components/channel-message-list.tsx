"use client";

import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type Ref,
  type ReactNode,
} from "react";
import { Virtuoso, type VirtuosoHandle } from "react-virtuoso";
import type { ChannelMessage } from "@multica/core/types";
import { ChannelMessageBubble } from "./channel-message-bubble";

/**
 * Virtualized message list shared by the group conversation (channels-page)
 * and the DM channel conversation (dm-conversation).
 *
 * The list is windowed with `react-virtuoso` so desktop thread split resize
 * only reflows visible bubbles, not every message in a busy channel. A small
 * delayed fallback keeps the previous P0 visibility contract: if Virtuoso ever
 * mounts without producing item rows, we render direct rows rather than blanking
 * the conversation.
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
  const virtuosoRef = useRef<VirtuosoHandle>(null);
  const messageRefs = useRef<Map<string, HTMLDivElement> | null>(null);
  const [scrollContainerEl, setScrollContainerEl] = useState<HTMLDivElement | null>(null);
  const [directFallbackChannelId, setDirectFallbackChannelId] = useState<string | null>(null);
  const [isNearBottom, setIsNearBottom] = useState(true);
  const channelId = messages[0]?.channel_id;
  if (!messageRefs.current) {
    messageRefs.current = new Map<string, HTMLDivElement>();
  }
  const messageRefMap = messageRefs.current;
  const useDirectFallback = directFallbackChannelId === channelId;
  const setScrollContainerRef = useCallback((node: HTMLDivElement | null) => {
    scrollRef.current = node;
    setScrollContainerEl(node);
  }, []);

  const highlightIndex = useMemo(() => {
    if (!highlightMessageId) return -1;
    return messages.findIndex((m) => m.id === highlightMessageId);
  }, [messages, highlightMessageId]);

  useEffect(() => {
    if (messages.length === 0 || useDirectFallback) return;
    const timer = window.setTimeout(() => {
      const scroller = scrollRef.current;
      const renderedRows = scroller?.querySelectorAll('[data-testid="message-row"]').length ?? 0;
      if (renderedRows === 0) {
        setDirectFallbackChannelId(channelId ?? null);
      }
    }, 350);
    return () => window.clearTimeout(timer);
  }, [channelId, messages.length, useDirectFallback]);

  useEffect(() => {
    if (highlightMessageId) {
      if (highlightIndex < 0) return;
      virtuosoRef.current?.scrollToIndex({
        index: highlightIndex,
        align: "center",
        behavior: "smooth",
      });
      messageRefMap.get(highlightMessageId)?.scrollIntoView({
        block: "center",
        behavior: "smooth",
      });
      return;
    }
    const scroller = scrollRef.current;
    if (!scroller) return;
    scroller.scrollTop = scroller.scrollHeight;
  }, [highlightIndex, highlightMessageId, messageRefMap, messages.length]);

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

  const renderRow = (msg: ChannelMessage) => {
    const searchHighlighted = searchHitIds?.has(msg.id) ?? false;
    return (
      <div
        key={msg.id}
        ref={(node) => {
          if (node) {
            messageRefMap.set(msg.id, node);
          } else {
            messageRefMap.delete(msg.id);
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
  };

  if (useDirectFallback || !scrollContainerEl) {
    return (
      <div
        ref={setScrollContainerRef}
        className="virtuoso-scroller min-h-0 min-w-0 flex-1 overflow-y-auto"
        data-testid="message-scroller"
      >
        <div className="virtuoso-item-list pt-3" data-testid="message-item-list">
          {messages.map(renderRow)}
          {footer ? <div className="px-5 pb-5 pt-2">{footer}</div> : <div className="pb-5" />}
        </div>
      </div>
    );
  }

  return (
    <div
      ref={setScrollContainerRef}
      className="virtuoso-scroller min-h-0 min-w-0 flex-1 overflow-y-auto"
      data-testid="message-scroller"
    >
      <Virtuoso
        ref={virtuosoRef}
        customScrollParent={scrollContainerEl}
        data={messages}
        initialTopMostItemIndex={highlightIndex >= 0 ? highlightIndex : Math.max(0, messages.length - 1)}
        increaseViewportBy={{ top: 320, bottom: 520 }}
        atBottomThreshold={120}
        atBottomStateChange={setIsNearBottom}
        followOutput={() => (isNearBottom ? "smooth" : false)}
        computeItemKey={(_, msg) => msg.id}
        components={{
          List: VirtuosoItemList,
          Header: () => <div className="pt-3" />,
          Footer: () => (footer ? <div className="px-5 pb-5 pt-2">{footer}</div> : <div className="pb-5" />),
        }}
        itemContent={(_, msg) => renderRow(msg)}
      />
    </div>
  );
}

function VirtuosoItemList({
  ref,
  ...props
}: React.HTMLAttributes<HTMLDivElement> & { ref?: Ref<HTMLDivElement> }) {
  return (
    <div
      {...props}
      ref={ref}
      className={["virtuoso-item-list", props.className].filter(Boolean).join(" ")}
      data-testid="message-item-list"
    />
  );
}

export { MessageViewport, MessageViewport as ChannelMessageList };
