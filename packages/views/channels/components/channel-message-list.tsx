"use client";

import {
  Fragment,
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
import { isLegacyRuntimeSystemNotice } from "./runtime-system-notice";
import { useMessageDayDividers } from "../../i18n/use-message-time";
import { useT } from "../../i18n/use-t";
import { useNewMessagesDivider } from "../hooks/use-new-messages-divider";

// Raft-style date separator inserted before the first message of each local day.
function DateDivider({ label }: { label: string }) {
  return (
    <div className="flex items-center gap-3 px-5 py-2" data-testid="date-divider">
      <div className="h-px flex-1 bg-border/60" />
      <span className="shrink-0 text-[11px] font-medium uppercase tracking-wide text-muted-foreground">
        {label}
      </span>
      <div className="h-px flex-1 bg-border/60" />
    </div>
  );
}

// "N new messages" separator pinned above the first unread message (#303). Brand
// tint (vs the muted date divider) so it reads as the "you're here" locator,
// glanceable but not loud — per Iris's spec.
function UnreadDivider({ count }: { count: number }) {
  const { t } = useT("common");
  return (
    <div className="flex items-center gap-3 px-5 py-2" data-testid="unread-divider">
      <div className="h-px flex-1 bg-brand/50" />
      <span className="shrink-0 text-[11px] font-medium uppercase tracking-wide text-brand">
        {t(($) => $.time.new_messages, { count })}
      </span>
      <div className="h-px flex-1 bg-brand/50" />
    </div>
  );
}

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
  /**
   * Viewer's read cursor for this conversation (BE `last_read_seq`). Drives the
   * "N new messages" divider: the list pins it above the first message past the
   * cursor as snapshotted on entry, and opens scrolled there. Omitted/undefined
   * → no divider (feature stays dark until the BE field lands).
   */
  lastReadSeq?: number | null;
  /**
   * Virtuoso's stable prepend anchor (see `channelMessagesFirstItemIndex`).
   * Callers with paginated history must recompute and pass this so loading
   * an older page doesn't jump the viewport; callers without pagination
   * (e.g. thread replies) can omit it.
   */
  firstItemIndex?: number;
  /** Centered placeholder shown when there are no messages yet. */
  emptyLabel: string;
  /** Content rendered at the top of the scroll window, before messages. */
  header?: ReactNode;
  /** Initial viewport anchor. Main conversations open at the latest message; threads open at root context. */
  initialScroll?: "bottom" | "top";
  /** Called when the user opens the message's side thread. */
  onOpenThread?: (message: ChannelMessage) => void;
  /**
   * Called when the user clicks an inline quote block to jump to the original.
   * The parent updates `highlightMessageId` so the list scrolls + highlights.
   */
  onScrollToMessage?: (messageId: string) => void;
  /** Toggle/add a lightweight emoji reaction on this message. */
  onReact?: (message: ChannelMessage, emoji: string) => void;
  /**
   * Save an inline edit of the viewer's own message. H5: this is an edit (a
   * PATCH), never a re-send — it must not go through a send/dispatch path.
   * Threaded to the bubble, which only exposes the edit affordance on own
   * messages when this is provided.
   */
  onEditMessage?: (message: ChannelMessage, content: string) => void;
  /** Soft-delete the viewer's own message; the bubble then renders a tombstone. */
  onDeleteMessage?: (message: ChannelMessage) => void;
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
  lastReadSeq,
  firstItemIndex = 0,
  emptyLabel,
  header,
  initialScroll = "bottom",
  onOpenThread,
  onScrollToMessage,
  onReact,
  onEditMessage,
  onDeleteMessage,
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
  // Only the direct-fallback path needs manual scroll-position preservation:
  // it renders plain divs with no virtualization, so prepending an older page
  // shifts everything below it and we have to restore the old offset by hand.
  // The Virtuoso path never touches this — its own `firstItemIndex` handles
  // prepend-without-jump, and fighting that with a second scrollTop write is
  // exactly what caused the viewport jumping this replaces.
  const preserveScrollDeltaRef = useRef<number | null>(null);
  const [scrollContainerEl, setScrollContainerEl] = useState<HTMLDivElement | null>(null);
  const [directFallbackChannelId, setDirectFallbackChannelId] = useState<string | null>(null);
  const [isNearBottom, setIsNearBottom] = useState(true);
  const channelId = messages[0]?.channel_id;
  const canLoadOlder = !!hasOlder && !loadingOlder && !!onLoadOlder;
  const dayDividers = useMessageDayDividers(messages);
  const newMessagesDivider = useNewMessagesDivider(channelId, messages, lastReadSeq);

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

  const unreadAnchorIndex = useMemo(() => {
    if (!newMessagesDivider) return -1;
    return messages.findIndex((m) => m.id === newMessagesDivider.anchorMessageId);
  }, [messages, newMessagesDivider]);

  // Fallback-only: captures the pre-prepend scroll offset so the layout
  // effect below can restore it once the older page's rows are in the DOM.
  const requestLoadOlderWithFallbackRestore = useCallback(() => {
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

  // Scrolls to a deep-linked / search-hit message. Deliberately does NOT
  // depend on `messages` — bottom-follow for newly-arrived messages is
  // Virtuoso's own `followOutput` job now; re-running this on every new
  // message during an open search used to re-fire scrollToIndex repeatedly.
  // Does depend on `scrollContainerEl`: the first render returns a bare
  // placeholder (Virtuoso isn't mounted yet, so `virtuosoRef.current` is
  // still null), and this effect must re-fire once Virtuoso actually mounts
  // on the following render — otherwise a highlight set before first mount
  // (e.g. opening a channel via a deep link) never scrolls into view.
  useEffect(() => {
    if (!highlightMessageId || highlightIndex < 0 || !scrollContainerEl) return;
    virtuosoRef.current?.scrollToIndex({
      index: firstItemIndex + highlightIndex,
      align: "center",
      behavior: "smooth",
    });
    messageRefMap.get(highlightMessageId)?.scrollIntoView({
      block: "center",
      behavior: "smooth",
    });
  }, [highlightMessageId, highlightIndex, firstItemIndex, messageRefMap, scrollContainerEl]);

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
    const dividerLabel = dayDividers.get(msg.id);
    const isUnreadAnchor = newMessagesDivider?.anchorMessageId === msg.id;
    return (
      <Fragment key={msg.id}>
        {dividerLabel && <DateDivider label={dividerLabel} />}
        {isUnreadAnchor && <UnreadDivider count={newMessagesDivider.count} />}
        <div
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
            onEdit={onEditMessage}
            onDelete={onDeleteMessage}
            searchHighlighted={searchHighlighted}
            searchQuery={searchHighlighted ? searchQuery : undefined}
          />
        </div>
      </Fragment>
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
          if (event.currentTarget.scrollTop < 80) requestLoadOlderWithFallbackRestore();
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
              onLoadOlder={requestLoadOlderWithFallbackRestore}
            />
          </div>
          {messages.map(renderRow)}
          <div className="pb-5" />
        </div>
      </div>
    );
  }

  // Open scrolled to: a deep-link target first, else the "new messages" divider
  // so the viewer starts where they left off (#303, Iris), else the chat
  // default (latest / thread root).
  const initialTopMostItemIndex =
    highlightIndex >= 0
      ? firstItemIndex + highlightIndex
      : unreadAnchorIndex >= 0
        ? firstItemIndex + unreadAnchorIndex
        : initialScroll === "bottom"
          ? firstItemIndex + Math.max(0, messages.length - 1)
          : firstItemIndex;

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
        firstItemIndex={firstItemIndex}
        initialTopMostItemIndex={initialTopMostItemIndex}
        increaseViewportBy={{ top: 320, bottom: 520 }}
        atBottomThreshold={120}
        atBottomStateChange={setIsNearBottom}
        followOutput={() => (!loadingOlder && isNearBottom ? "smooth" : false)}
        startReached={() => {
          if (canLoadOlder) onLoadOlder?.();
        }}
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
                  onLoadOlder={() => onLoadOlder?.()}
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

function ChannelMessageList(props: MessageViewportProps) {
  const messages = props.messages.some(isLegacyRuntimeSystemNotice)
    ? props.messages.filter((message) => !isLegacyRuntimeSystemNotice(message))
    : props.messages;

  return <MessageViewport {...props} messages={messages} />;
}

export { MessageViewport, ChannelMessageList };
