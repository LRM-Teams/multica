"use client";

import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
  type Ref,
  type ReactNode,
} from "react";
import { Virtuoso, type VirtuosoHandle } from "react-virtuoso";
import type { ChannelMessage } from "@multica/core/types";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { cn } from "@multica/ui/lib/utils";
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
 * The composer and activity strip stay in the caller; only the scrolling
 * message area is owned here.
 *
 * Opens scrolled to the latest message (chat convention), or to a deep-linked
 * `highlightMessageId` when present.
 */
type MessageViewportProps = {
  messages: ChannelMessage[];
  currentUserId: string | null;
  /** Display name for the viewer's own messages. */
  ownName?: string;
  /** Deep-link target id - scrolls to and ring-highlights that bubble. */
  highlightMessageId?: string | null;
  /** Centered placeholder shown when there are no messages yet. */
  emptyLabel: string;
  /** Content rendered at the top of the scroll window, before messages. */
  header?: ReactNode;
  /** Called when the user opens the message's side thread. */
  onOpenThread?: (message: ChannelMessage) => void;
  /**
   * Called when the user clicks an inline quote block to jump to the original.
   * The parent updates `highlightMessageId` so the list scrolls + highlights.
   */
  onScrollToMessage?: (messageId: string) => void;
  /** Toggle/add a lightweight emoji reaction on this message. */
  onReact?: (message: ChannelMessage, emoji: string) => void;
  /** Search hit ids - all matching messages get inline keyword marks while search is open. */
  searchHitIds?: Set<string>;
  /** Conversation search phrase used for inline keyword marks within search hits. */
  searchQuery?: string;
  /** Initial page is loading and no cached messages are available. */
  loading?: boolean;
  /** Older history page is loading above the current viewport. */
  loadingOlder?: boolean;
  /** Whether older history can be requested from the top affordance. */
  hasOlder?: boolean;
  /** Load the next older history page. */
  onLoadOlder?: () => void;
  loadOlderLabel?: string;
  loadingOlderLabel?: string;
  /** Localized error text for initial-load failures. */
  loadErrorLabel?: string;
  /** Retry the initial page without replacing the shell. */
  onRetry?: () => void;
};

function MessageViewport({
  messages,
  currentUserId,
  ownName,
  highlightMessageId,
  emptyLabel,
  header,
  onOpenThread,
  onScrollToMessage,
  onReact,
  searchHitIds,
  searchQuery,
  loading,
  loadingOlder,
  hasOlder,
  onLoadOlder,
  loadOlderLabel,
  loadingOlderLabel,
  loadErrorLabel,
  onRetry,
}: MessageViewportProps) {
  const scrollRef = useRef<HTMLDivElement>(null);
  const virtuosoRef = useRef<VirtuosoHandle>(null);
  const messageRefs = useRef<Map<string, HTMLDivElement> | null>(null);
  const previousMessageCountRef = useRef(messages.length);
  const previousFirstMessageIdRef = useRef(messages[0]?.id ?? null);
  const preserveScrollDeltaRef = useRef<number | null>(null);
  const [scrollContainerEl, setScrollContainerEl] = useState<HTMLDivElement | null>(null);
  const [directFallbackChannelId, setDirectFallbackChannelId] = useState<string | null>(null);
  const [isNearBottom, setIsNearBottom] = useState(true);
  const channelId = messages[0]?.channel_id;
  const canLoadOlder = !!hasOlder && !loadingOlder && !!onLoadOlder;

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

  const requestLoadOlder = useCallback(() => {
    const scroller = scrollRef.current;
    if (scroller) {
      preserveScrollDeltaRef.current = scroller.scrollHeight - scroller.scrollTop;
    }
    onLoadOlder?.();
  }, [onLoadOlder]);

  useLayoutEffect(() => {
    const scroller = scrollRef.current;
    const delta = preserveScrollDeltaRef.current;
    if (!scroller || delta === null) return;
    scroller.scrollTop = scroller.scrollHeight - delta;
    preserveScrollDeltaRef.current = null;
  }, [messages]);

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
    const previousCount = previousMessageCountRef.current;
    const previousFirstId = previousFirstMessageIdRef.current;
    const firstId = messages[0]?.id ?? null;
    const appendedAtBottom = messages.length > previousCount && firstId === previousFirstId;
    const initialMessages = previousCount === 0 && messages.length > 0;
    if (initialMessages || appendedAtBottom) {
      scroller.scrollTop = scroller.scrollHeight;
    }
    previousMessageCountRef.current = messages.length;
    previousFirstMessageIdRef.current = firstId;
  }, [highlightIndex, highlightMessageId, messageRefMap, messages]);

  if (loadErrorLabel) {
    return (
      <StaticMessageScroller header={header}>
        <button
          type="button"
          className="rounded-md px-3 py-2 text-sm hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
          onClick={onRetry}
        >
          {loadErrorLabel}
        </button>
      </StaticMessageScroller>
    );
  }

  if (loading) {
    return (
      <StaticMessageScroller
        header={header}
        centered={false}
      >
        <MessageRowsSkeleton />
      </StaticMessageScroller>
    );
  }

  // Empty thread: render the placeholder directly (no message rows).
  if (messages.length === 0) {
    return (
      <StaticMessageScroller header={header}>
        {emptyLabel}
      </StaticMessageScroller>
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

  if (!scrollContainerEl) {
    return (
      <div
        ref={setScrollContainerRef}
        className="virtuoso-scroller min-h-0 min-w-0 flex-1 overflow-y-auto"
        data-testid="message-scroller"
      />
    );
  }

  if (useDirectFallback) {
    return (
      <div
        ref={setScrollContainerRef}
        className="virtuoso-scroller min-h-0 min-w-0 flex-1 overflow-y-auto"
        data-testid="message-scroller"
        onScroll={(event) => {
          if (!canLoadOlder) return;
          if (event.currentTarget.scrollTop < 80) requestLoadOlder();
        }}
      >
        <div
          className={cn("virtuoso-item-list", !header && "pt-3")}
          data-testid="message-item-list"
        >
          {header}
          <div className={header ? "pt-2" : undefined}>
            <LoadOlderAffordance
              hasOlder={hasOlder}
              loadingOlder={loadingOlder}
              loadingOlderLabel={loadingOlderLabel}
              loadOlderLabel={loadOlderLabel}
              onLoadOlder={requestLoadOlder}
            />
          </div>
          {messages.map(renderRow)}
          <div className="pb-5" />
        </div>
      </div>
    );
  }

  return (
    <div
      ref={setScrollContainerRef}
      className="virtuoso-scroller min-h-0 min-w-0 flex-1 overflow-y-auto"
      data-testid="message-scroller"
      onScroll={(event) => {
        if (!canLoadOlder) return;
        if (event.currentTarget.scrollTop < 80) requestLoadOlder();
      }}
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
          Header: () => (
            <>
              {header}
              <div className={header ? "pt-2" : "pt-3"}>
                <LoadOlderAffordance
                  hasOlder={hasOlder}
                  loadingOlder={loadingOlder}
                  loadingOlderLabel={loadingOlderLabel}
                  loadOlderLabel={loadOlderLabel}
                  onLoadOlder={requestLoadOlder}
                />
              </div>
            </>
          ),
          Footer: () => <div className="pb-5" />,
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

function StaticMessageScroller({
  header,
  bodyClassName,
  centered = true,
  children,
}: {
  header?: ReactNode;
  bodyClassName?: string;
  centered?: boolean;
  children: ReactNode;
}) {
  return (
    <div
      className="flex min-h-0 min-w-0 flex-1 flex-col overflow-y-auto"
      data-testid="message-scroller"
    >
      {header}
      <div
        className={cn(
          "flex-1 px-5 pb-5 pt-3",
          centered && "flex items-center justify-center text-sm text-muted-foreground",
          bodyClassName,
        )}
      >
        {children}
      </div>
    </div>
  );
}

function LoadOlderAffordance({
  hasOlder,
  loadingOlder,
  loadingOlderLabel,
  loadOlderLabel,
  onLoadOlder,
}: {
  hasOlder?: boolean;
  loadingOlder?: boolean;
  loadingOlderLabel?: string;
  loadOlderLabel?: string;
  onLoadOlder: () => void;
}) {
  if (!hasOlder && !loadingOlder) return null;

  return (
    <div className="px-5 pb-2">
      {loadingOlder ? (
        <div className="text-center text-xs text-muted-foreground">{loadingOlderLabel}</div>
      ) : (
        <button
          type="button"
          className="mx-auto block rounded-md px-3 py-1.5 text-xs text-muted-foreground hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
          onClick={onLoadOlder}
        >
          {loadOlderLabel}
        </button>
      )}
    </div>
  );
}

function MessageRowsSkeleton() {
  const rows = [
    ["w-44", "w-72", "w-52"],
    ["w-28", "w-80", "w-40"],
    ["w-36", "w-64", "w-72"],
    ["w-32", "w-56", "w-44"],
    ["w-40", "w-72", "w-48"],
    ["w-24", "w-60", "w-36"],
  ];
  return (
    <div className="space-y-5" aria-hidden="true">
      {rows.map((widths, index) => (
        <div key={index} className="flex gap-3">
          <Skeleton className="mt-1 size-8 shrink-0 rounded-full opacity-60" />
          <div className="min-w-0 flex-1 space-y-2">
            <Skeleton className={`${widths[0]} h-3 max-w-full opacity-50`} />
            <Skeleton className={`${widths[1]} h-3 max-w-full opacity-40`} />
            <Skeleton className={`${widths[2]} h-3 max-w-full opacity-30`} />
          </div>
        </div>
      ))}
    </div>
  );
}

export { MessageViewport, MessageViewport as ChannelMessageList };
