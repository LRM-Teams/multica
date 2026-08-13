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
// react-doctor-disable-next-line react-doctor/no-flush-sync -- intentional: sync Virtuoso scroll parent before paint (LRM-273).
import { flushSync } from "react-dom";
import { Virtuoso, type VirtuosoHandle } from "react-virtuoso";
import type { ChannelMessage } from "@multica/core/types";
import type { OpenAgentPanelFn } from "@multica/core/agents";
import { channelMessageListItemKey } from "@multica/core/channels";

import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { cn } from "@multica/ui/lib/utils";
import { ChannelMessageBubble } from "./channel-message-bubble";
import { isLegacyRuntimeSystemNotice } from "./runtime-system-notice";
import { foldedIssueEventIds } from "./channel-system-event";
import { useMessageDayDividers } from "../../i18n/use-message-time";
import { useViewingTimezone } from "../../common/use-viewing-timezone";
import { useT } from "../../i18n/use-t";
import { useNewMessagesDivider } from "../hooks/use-new-messages-divider";
import { useNewMessagesPill } from "../hooks/use-new-arrivals-pill";
import { useUnreadAnchorScroll } from "../hooks/use-unread-anchor-scroll";
import { useBottomSettleScroll } from "../hooks/use-bottom-settle-scroll";
import { buildMessageGroupCompactMap, buildMessageGroupEndMap } from "./message-group-compact";
import { buildNoteWorkerPageIdByMessageId } from "@multica/core/notes/worker-reply-actions";

// Small centered date pill (Iris #303 A) — the inline date divider at each local
// day boundary. Pill is OK for *dates*; system event rows must not reuse this
// chip language (LRM-555 Frank: 禁胶囊 on system rows).
function DatePill({ label }: { label: string }) {
  return (
    <span className="rounded-full border border-border/60 bg-background/90 px-2.5 py-0.5 text-[11px] font-medium text-muted-foreground shadow-sm">
      {label}
    </span>
  );
}

// Inserted before the first message of each local day. Date dividers keep
// centered pill + side rules (LRM-564); system event rows are a separate track
// (left-aligned, no hairlines).
function DateDivider({ label }: { label: string }) {
  return (
    <div
      className="flex items-center gap-3 px-5 py-2"
      data-testid="date-divider"
    >
      <div aria-hidden className="h-px min-w-4 flex-1 bg-border/60" />
      <DatePill label={label} />
      <div aria-hidden className="h-px min-w-4 flex-1 bg-border/60" />
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

// Floating "N new messages ↓" jump pill (#303 follow-up). Shown when you're
// scrolled up and messages arrive live below you; clicking jumps to the first
// one. Same brand language as the divider; anchored bottom-center above the
// composer, pinned to the viewport (doesn't scroll with messages).
function NewMessagesPill({ count, onClick }: { count: number; onClick: () => void }) {
  const { t } = useT("common");
  return (
    <button
      type="button"
      onClick={onClick}
      className="absolute bottom-3 left-1/2 z-10 flex -translate-x-1/2 items-center gap-1 rounded-full bg-brand px-3 py-1.5 text-xs font-medium text-white shadow-md transition-colors hover:bg-brand/90"
      data-testid="new-messages-pill"
    >
      <span>{t(($) => $.time.new_messages, { count })}</span>
      <span aria-hidden="true">↓</span>
    </button>
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
   * True unread count frozen at entry (sidebar-same source), for the "N new
   * messages" divider (#340). The loaded window holds only ~limit/2 messages
   * past the anchor, so counting unread within it undercounts large-unread
   * conversations — this carries the real total. Omitted → fall back to the
   * count within the loaded window.
   */
  unreadCount?: number | null;
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
  /** Called when the user opens the message's side thread. */
  onOpenThread?: (message: ChannelMessage) => void;
  /**
   * Called when the user clicks an inline quote block to jump to the original.
   * The parent updates `highlightMessageId` so the list scrolls + highlights.
   */
  onScrollToMessage?: (messageId: string) => void;
  /** Toggle/add a lightweight emoji reaction on this message. */
  onReact?: (message: ChannelMessage, emoji: string) => void;
  /** Set a message as the caller-owned composer quote target. */
  onQuoteMessage?: (message: ChannelMessage) => void;
  /**
   * Save an inline edit of the viewer's own message. H5: this is an edit (a
   * PATCH), never a re-send — it must not go through a send/dispatch path.
   * Threaded to the bubble, which only exposes the edit affordance on own
   * messages when this is provided.
   */
  onEditMessage?: (message: ChannelMessage, content: string) => void;
  /** Retry a failed optimistic send (reuses the bubble's `client_message_id`). */
  onRetrySend?: (message: ChannelMessage) => void;
  /** Opens the side agent file/public-info panel for an agent-authored message
   *  (LRM-292: agentId + optional row identity snapshot). */
  onOpenAgent?: OpenAgentPanelFn;
  /** Opens the LRM-619 member Profile dock for a human-authored message. */
  onOpenMember?: (userId: string) => void;
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
  unreadCount,
  firstItemIndex = 0,
  emptyLabel,
  header,
  onOpenThread,
  onScrollToMessage,
  onReact,
  onQuoteMessage,
  onEditMessage,
  onRetrySend,
  onOpenAgent,
  onOpenMember,
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
  const scrollRef = useRef<HTMLElement | null>(null);
  const virtuosoRef = useRef<VirtuosoHandle>(null);
  // `virtuosoRef.current` alone can't gate a mount-time effect: ref attachment
  // doesn't trigger a re-render, so an effect that fires the instant its OTHER
  // dependencies are satisfied (e.g. `scrollContainerEl`) can run while the
  // handle is still null and never gets a second chance (#348 root cause).
  //
  // The fix is a BOOLEAN readiness flag, not the handle object itself (tried
  // and reverted — #365 incident): react-virtuoso hands the callback ref a
  // different object identity across some renders, so a reference-equality
  // bail on a handle-object state (`prev === node ? prev : node`) doesn't
  // actually bail — it cascades into setState → re-render → new handle
  // object → setState → ... an infinite loop that crashed the message pages
  // in production. A boolean compares by VALUE: React's built-in same-value
  // bailout (`Object.is`) skips the re-render whenever attached-ness doesn't
  // change, regardless of how many different object identities the ref
  // receives underneath. The handle itself is still read live off
  // `virtuosoRef.current` when actually needed — only the readiness SIGNAL
  // goes through state.
  const [handleAttached, setHandleAttached] = useState(false);
  const handleVirtuosoRef = useCallback((node: VirtuosoHandle | null) => {
    virtuosoRef.current = node;
    setHandleAttached(!!node);
  }, []);
  const messageRefs = useRef<Map<string, HTMLDivElement> | null>(null);
  // Only the direct-fallback path needs manual scroll-position preservation:
  // it renders plain divs with no virtualization, so prepending an older page
  // shifts everything below it and we have to restore the old offset by hand.
  // The Virtuoso path never touches this — its own `firstItemIndex` handles
  // prepend-without-jump, and fighting that with a second scrollTop write is
  // exactly what caused the viewport jumping this replaces.
  const preserveScrollDeltaRef = useRef<number | null>(null);
  const [directFallbackChannelId, setDirectFallbackChannelId] = useState<string | null>(null);
  const [isNearBottom, setIsNearBottom] = useState(true);
  const [scrollContainerEl, setScrollContainerEl] = useState<HTMLDivElement | null>(null);
  const channelId = messages[0]?.channel_id;
  const canLoadOlder = !!hasOlder && !loadingOlder && !!onLoadOlder;
  const tz = useViewingTimezone();
  const dayDividers = useMessageDayDividers(messages);
  // Fold redundant consecutive issue-lifecycle rows (item #7): a same-source
  // completed/status→done pair, or an exact repeat, renders once. Derived as a
  // Set (never a filtered array) so Virtuoso's data/indices — and thus the
  // anchor/pagination math — stay identical; a folded row simply renders null.
  const foldedIssueIds = useMemo(() => foldedIssueEventIds(messages), [messages]);
  const messageGroupCompact = useMemo(
    () =>
      buildMessageGroupCompactMap(messages, {
        foldedIds: foldedIssueIds,
        dateDividerIds: new Set(dayDividers.keys()),
        tz,
      }),
    [messages, foldedIssueIds, dayDividers, tz],
  );
  // LRM-1227 / LRM-1233 G: the joined bubble shell has no wrapping DOM node
  // (each message is its own Virtuoso row), so the last row of a group has to
  // know it owns the shell's bottom edge. Derived from the compact map above so
  // the two can never disagree.
  const messageGroupEnd = useMemo(
    () => buildMessageGroupEndMap(messages, messageGroupCompact, { foldedIds: foldedIssueIds }),
    [messages, messageGroupCompact, foldedIssueIds],
  );
  const noteWorkerPageIdByMessageId = useMemo(
    () => buildNoteWorkerPageIdByMessageId(messages),
    [messages],
  );
  const newMessagesDivider = useNewMessagesDivider(
    channelId,
    messages,
    lastReadSeq,
    currentUserId,
  );

  if (!messageRefs.current) {
    messageRefs.current = new Map<string, HTMLDivElement>();
  }
  const messageRefMap = messageRefs.current;
  const useDirectFallback = directFallbackChannelId === channelId;
  // #325: Virtuoso scrolls a caller-owned parent (`customScrollParent`), not its
  // own div. We render the scroll container ourselves and capture it into
  // `scrollContainerEl`, which (a) is Virtuoso's scroll parent, (b) gates the
  // mount-time scroll effects until the container exists — the first render is a
  // bare placeholder scroller, so Virtuoso only mounts on the second render once
  // this ref has set the state and the parent is laid out (this is what makes
  // `initialTopMostItemIndex` land correctly; giving Virtuoso its own flex
  // scroller regressed cold-load positioning), and (c) backs the fallback
  // scroll-position preservation + the render-detection probe via `scrollRef`.
  //
  // `flushSync` attaches Virtuoso in the same commit (before paint) so the list
  // never flashes an empty scroller between skeleton/loading and messages
  // (LRM-273).
  const setScrollContainerRef = useCallback((node: HTMLDivElement | null) => {
    scrollRef.current = node;
    if (node) {
      flushSync(() => {
        setScrollContainerEl(node);
      });
    } else {
      setScrollContainerEl(null);
    }
  }, []);

  const highlightIndex = useMemo(() => {
    if (!highlightMessageId) return -1;
    return messages.findIndex((m) => m.id === highlightMessageId);
  }, [messages, highlightMessageId]);

  // "Open scrolled to the unread divider" (#303) — self-contained plugin hook
  // (#325 phase-2 block 2). Owns the anchor derivation, the once-per-visit guard,
  // and the measure-safe (#883) settle-scroll; the core just reads back
  // `unreadAnchorIndex` to seed the Virtuoso mount position below.
  const { unreadAnchorIndex, isAnchorSettling } = useUnreadAnchorScroll({
    channelId,
    messages,
    newMessagesDivider,
    highlightMessageId,
    // A value-comparable readiness signal so the settle effect re-runs the
    // instant Virtuoso attaches, instead of possibly having already run (and
    // no-op'd) while the handle was still null with no second chance.
    handleAttached,
    virtuosoRef,
    // The scroll container gates the anchor scroll (Virtuoso only mounts once it
    // exists) and its rect tells the settle helper when the anchor row arrives.
    scrollContainerEl,
    messageRefMap,
  });

  // Cold-open "land at the latest message" safety net for the DEFAULT case (no
  // deep-link highlight, no unread anchor). The declarative mount-once
  // `initialTopMostItemIndex` below is unreliable with
  // customScrollParent + async data on a fresh/no-cache mount (real-device P0:
  // the list opened at scrollTop=0 with the oldest row on top despite the index
  // computing to the last row). The unread-anchor path never hit this because it
  // ALSO runs an imperative scrollToIndex settle after mount; this gives the
  // bottom-default case the same imperative net. Mutually exclusive with the
  // anchor/highlight settles. LRM-1156: thread surfaces get this net too — they
  // used to opt out of the bottom landing entirely.
  useBottomSettleScroll({
    channelId,
    messages,
    enabled: highlightIndex < 0 && unreadAnchorIndex < 0,
    // LRM-1220: the direct fallback renders plain, non-virtualized divs — no
    // Virtuoso, so `handleAttached` is permanently false and the settle used to
    // refuse to write a single scrollTop, timing out with the viewport resting on
    // the OLDEST loaded row (jianghp3 on mobile web: every open landed on 「今天」
    // + 当日第一条 with 「加载更早消息」 at the top). That path has no landing logic
    // of its own, so this settle is the only owner of its initial position; it is
    // "ready" precisely because there is no imperative handle to wait for.
    listReady: handleAttached || useDirectFallback,
    scrollContainerEl,
    messageRefMap,
  });

  // Floating "N new messages ↓" pill (#303) — self-contained plugin hook (#325
  // phase-2 block 1). It owns its own boundary/scroll state; the core list just
  // renders `pill` and forwards the callbacks. `isNearBottom` stays in the core
  // (followOutput reads it); the pill hook never touches it.
  const { pill, onReachedBottom, onPillClick } = useNewMessagesPill({
    messages,
    currentUserId,
    virtuosoRef,
  });
  const handleAtBottomStateChange = useCallback(
    (atBottom: boolean) => {
      setIsNearBottom(atBottom);
      if (atBottom) onReachedBottom();
    },
    [onReachedBottom],
  );

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

  // Scrolls to a deep-linked / search-hit message. Deliberately does NOT depend
  // on `messages` — bottom-follow for newly-arrived messages is Virtuoso's own
  // `followOutput` job; re-running on every new message during an open search
  // used to re-fire scrollToIndex repeatedly. Depends on `scrollContainerEl`: the
  // first render is a bare placeholder scroller (Virtuoso isn't mounted yet, so
  // `virtuosoRef.current` is still null), and this effect must re-fire once the
  // container exists and Virtuoso has mounted.
  useEffect(() => {
    if (!highlightMessageId || highlightIndex < 0 || !scrollContainerEl) return;
    // #689/#1189 index-contract fix: react-virtuoso's `scrollToIndex` (and
    // `initialTopMostItemIndex` below) resolve against the LOCAL data array
    // — 0..data.length-1 — never offset by `firstItemIndex`. `firstItemIndex`
    // is Virtuoso's own internal bookkeeping for prepend-without-jump; it is
    // not meant to be added to caller-supplied indices. Confirmed against the
    // library's own prepend example (`firstItemIndex=10000` + 20 items still
    // uses `initialTopMostItemIndex=19`, not `10019`) and its
    // `scrollToIndexSystem` source, which resolves purely against
    // `totalCount - 1` and never reads `firstItemIndex`. `highlightIndex` is
    // already local (`messages.findIndex(...)`).
    virtuosoRef.current?.scrollToIndex({
      index: highlightIndex,
      align: "center",
      behavior: "smooth",
    });
    messageRefMap.get(highlightMessageId)?.scrollIntoView({
      block: "center",
      behavior: "smooth",
    });
  }, [highlightMessageId, highlightIndex, messageRefMap, scrollContainerEl]);

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

  if (loading && messages.length === 0) {
    return (
      <StaticMessageScroller
        header={header}
        centered={false}
      >
        <MessageRowsSkeleton />
      </StaticMessageScroller>
    );
  }

  // Empty thread: primary empty copy uses foreground (LRM-357), not muted.
  if (messages.length === 0) {
    return (
      <StaticMessageScroller header={header}>
        <p
          data-slot="message-list-empty"
          className="text-sm text-foreground"
        >
          {emptyLabel}
        </p>
      </StaticMessageScroller>
    );
  }

  const renderRow = (msg: ChannelMessage) => {
    // A folded issue row is suppressed (a preceding row already conveys the
    // fact). Return null rather than dropping it from `messages` so the list's
    // virtualization/anchoring is untouched.
    if (foldedIssueIds.has(msg.id)) return null;
    // Stable across optimistic temp id → server ACK so rows do not remount
    // (LRM-273 secondary flash).
    const rowKey = channelMessageListItemKey(msg);
    const searchHighlighted = searchHitIds?.has(msg.id) ?? false;
    const dividerLabel = dayDividers.get(msg.id);
    const isUnreadAnchor = newMessagesDivider?.anchorMessageId === msg.id;
    const compact = messageGroupCompact.get(rowKey) ?? messageGroupCompact.get(msg.id) ?? false;
    const groupEnd = messageGroupEnd.get(rowKey) ?? messageGroupEnd.get(msg.id) ?? true;
    return (
      <Fragment key={rowKey}>
        {dividerLabel && <DateDivider label={dividerLabel} />}
        {isUnreadAnchor && (
          // #340: real unread total frozen at entry (sidebar-same source); the
          // window-local count is only a fallback when it's unavailable.
          <UnreadDivider count={unreadCount ?? newMessagesDivider.count} />
        )}
        <div
          ref={(node) => {
            if (node) {
              messageRefMap.set(rowKey, node);
              // Highlight/scroll may still look up by server id after ACK.
              if (rowKey !== msg.id) messageRefMap.set(msg.id, node);
            } else {
              messageRefMap.delete(rowKey);
              if (rowKey !== msg.id) messageRefMap.delete(msg.id);
            }
          }}
          // LRM-1227: inside a joined group the rows must touch, otherwise the
          // shell's side edges show a 1px break between continuations.
          className={cn("px-5", compact ? "pt-0" : "pt-1.5")}
          data-testid="message-row"
          data-message-group={compact ? "compact" : "lead"}
        >
          <ChannelMessageBubble
            message={msg}
            currentUserId={currentUserId}
            ownName={ownName}
            highlighted={msg.id === highlightMessageId}
            onOpenThread={onOpenThread}
            onScrollTo={onScrollToMessage}
            onReact={onReact}
            onQuote={onQuoteMessage}
            onEdit={onEditMessage}
            onRetrySend={onRetrySend}
            onOpenAgent={onOpenAgent}
            onOpenMember={onOpenMember}
            searchHighlighted={searchHighlighted}
            searchQuery={searchHighlighted ? searchQuery : undefined}
            compact={compact}
            groupEnd={groupEnd}
            noteWorkerPageId={noteWorkerPageIdByMessageId.get(msg.id) ?? null}
          />
        </div>
      </Fragment>
    );
  };

  // First render: no scroll container captured yet. Render a bare scroller div
  // whose ref sets `scrollContainerEl`; the next render mounts Virtuoso against
  // this now-laid-out parent (`customScrollParent`), which is what lets
  // `initialTopMostItemIndex` position correctly on cold load.
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

  // Open scrolled to: a deep-link target first, else the chat default — the
  // LATEST row. LRM-1068: do NOT pin the first-unread row on cold open — that
  // landed busy channels on 「今天」+ 当日第一条. Unread stays visible via the
  // divider + "N new" pill; jump-on-demand, don't steal the landing.
  // LRM-1156: thread surfaces share that default. They used to anchor at local
  // index 0 (pinned-root context), which made every thread open land on the
  // OLDEST reply and forced a manual scroll to read the newest one (Frank:
  // 「别老是让我滑动」). The root preview is the scroll window's header, so it
  // is still one scroll-up away.
  // #689/#1189 index-contract fix: `initialTopMostItemIndex` resolves
  // against the LOCAL data array (0..messages.length-1), same as
  // `scrollToIndex` above — never offset by `firstItemIndex`. See that
  // effect's comment for the evidence. `firstItemIndex` itself (passed to
  // Virtuoso below) is unaffected — it still does its real job of prepend
  // bookkeeping.
  const initialTopMostItemIndex:
    | number
    | { index: number; align: "start" | "center" } =
    highlightIndex >= 0
      ? { index: highlightIndex, align: "center" }
      : unreadAnchorIndex >= 0
        ? { index: unreadAnchorIndex, align: "start" }
        : Math.max(0, messages.length - 1);

  return (
    <div className="relative flex min-h-0 min-w-0 flex-1 flex-col">
      <div
        ref={setScrollContainerRef}
        className="virtuoso-scroller min-h-0 min-w-0 flex-1 overflow-y-auto"
        data-testid="message-scroller"
      >
        <Virtuoso
          ref={handleVirtuosoRef}
          customScrollParent={scrollContainerEl}
          data={messages}
          firstItemIndex={firstItemIndex}
          initialTopMostItemIndex={initialTopMostItemIndex}
          increaseViewportBy={{ top: 320, bottom: 520 }}
          // 2026-07-24: was 120px — generous enough that scrolling up even a
          // little to reread the last couple messages still read as "at
          // bottom" to Virtuoso, so a live message arriving mid-read yanked
          // the viewport back down (Frank: "我一上翻就是不想被拽走" — the
          // moment I scroll up, I don't want to be pulled back). The product
          // bar is intent, not distance: any deliberate upward scroll should
          // release follow immediately. 24px keeps a small allowance for
          // rubber-band/inertia settle noise right at the true bottom (avoids
          // flapping isNearBottom on sub-pixel scroll jitter) without reading
          // as "still at the bottom" for an actual reposition.
          atBottomThreshold={24}
          atBottomStateChange={handleAtBottomStateChange}
          // Scroll position has exactly one owner at a time (#348 postmortem):
          // while the unread-anchor settle loop is in flight it's re-issuing
          // `scrollToIndex` toward the anchor every frame — `followOutput`
          // smooth-scrolling back to the bottom during that window (its
          // `isNearBottom` default is `true` before the real cold-load
          // position is known) fights it, so `hasReached()` never sees the
          // anchor arrive and the settle loop times out at the bottom,
          // indistinguishable from never having tried. Gate it off for the
          // duration; the anchor hook hands ownership back the moment it
          // reaches or gives up.
          followOutput={() => (!loadingOlder && !isAnchorSettling && isNearBottom ? "smooth" : false)}
          startReached={() => {
            if (canLoadOlder) onLoadOlder?.();
          }}
          computeItemKey={(_, msg) => channelMessageListItemKey(msg)}
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
      {!isNearBottom && pill && (
        <NewMessagesPill count={pill.count} onClick={onPillClick} />
      )}
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
    <div className="space-y-1.5" aria-hidden="true" data-testid="message-rows-skeleton">
      {rows.map((widths) => (
        <div key={widths.join("-")} className="grid grid-cols-[28px_minmax(0,1fr)] gap-2.5 px-2 py-1.5 md:px-5">
          <Skeleton className="size-8 shrink-0 rounded-full opacity-60" />
          <div className="min-w-0 space-y-2">
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
