import { useEffect, useMemo, useRef, type RefObject } from "react";
import type { VirtuosoHandle } from "react-virtuoso";
import type { ChannelMessage } from "@multica/core/types";
import type { NewMessagesDivider } from "./use-new-messages-divider";

// react-virtuoso #883: on a cold load `scrollToIndex` can run before the list's
// item heights are measured, so it lands at the wrong offset (the unread divider
// dropping far below the viewport was exactly this). The maintainer-acknowledged
// fix is to re-issue the scroll after mount until it settles.
//
// A FIXED frame count is not enough: a big list with a far jump (e.g. a 14k-px
// DM) needs many re-issues before the off-screen item heights converge, while a
// small list converges in one. So we re-issue every animation frame and stop only
// once the target has ACTUALLY ARRIVED — `hasReached()` checks the real rendered
// layout (the target row at the top of the viewport). We deliberately do NOT stop
// on "scroll position stopped changing": `scrollToIndex` is async, so scrollTop
// reads its pre-scroll value (0 on cold load) for the first frames, and a
// position-stability check would falsely settle at the top before the scroll ever
// applies ("hasn't moved yet" is indistinguishable from "arrived"). A frame cap
// backstops a target that never renders. `behavior: "auto"` makes each repeat
// re-pin the same index idempotently (no jank). Returns a disposer so the caller
// can cancel on re-target/unmount.
export function scrollToIndexUntilSettled(
  handle: VirtuosoHandle | null,
  hasReached: () => boolean,
  location: { index: number; align: "start" | "center" | "end"; behavior?: "auto" | "smooth" },
  maxFrames = 40,
): () => void {
  if (!handle) return () => {};
  let raf = 0;
  let frame = 0;
  const tick = () => {
    handle.scrollToIndex(location);
    frame += 1;
    if (hasReached() || frame >= maxFrames) return;
    raf = requestAnimationFrame(tick);
  };
  tick();
  return () => {
    if (raf) cancelAnimationFrame(raf);
  };
}

// The anchor row counts as "arrived at the top" once it is rendered and its top
// edge sits within this band of the scroll container's top. The band absorbs the
// "N new messages" divider that renders just above the anchor row and minor
// sub-pixel/measurement drift, so we don't re-scroll forever chasing an exact 0.
const ANCHOR_TOP_BAND_PX = 96;

/**
 * #325 phase-2 block 2: "open scrolled to the unread divider" (#303, Iris) as a
 * self-contained plugin hook. Owns the anchor-index derivation, the
 * once-per-conversation-visit guard, and the measure-safe (#883) settle-scroll.
 *
 * The core list only READS the returned `unreadAnchorIndex` to seed Virtuoso's
 * mount position (`initialTopMostItemIndex`) — it holds none of this state and
 * runs none of this effect. A deep-link/search highlight always wins: while
 * `highlightMessageId` is set the anchor scroll stands down (that target owns the
 * viewport). Scrolls once per channel visit; the read cursor can arrive ~100ms
 * after mount, so the effect covers that late arrival via the settle helper.
 */
export function useUnreadAnchorScroll({
  channelId,
  messages,
  newMessagesDivider,
  highlightMessageId,
  firstItemIndex,
  virtuosoRef,
  scrollContainerEl,
  messageRefMap,
}: {
  channelId: string | undefined;
  messages: readonly ChannelMessage[];
  newMessagesDivider: NewMessagesDivider | null;
  highlightMessageId: string | null | undefined;
  firstItemIndex: number;
  virtuosoRef: RefObject<VirtuosoHandle | null>;
  /**
   * The caller's scroll container (Virtuoso's `customScrollParent`), or null
   * before it exists. Doubles as the "scroller ready" gate — with
   * `customScrollParent` the first render is a bare placeholder scroller, so
   * `virtuosoRef.current` is null until the second render; firing the anchor
   * scroll earlier scrolls nothing and the divider is left far below the
   * viewport. Its rect is also read to tell when the anchor row has arrived.
   */
  scrollContainerEl: HTMLElement | null;
  /** The rendered message-row DOM nodes, keyed by message id — used to detect
   * when the anchor row has actually scrolled to the top of the viewport. */
  messageRefMap: ReadonlyMap<string, HTMLElement>;
}): { unreadAnchorIndex: number } {
  const unreadAnchorIndex = useMemo(() => {
    if (!newMessagesDivider) return -1;
    return messages.findIndex((m) => m.id === newMessagesDivider.anchorMessageId);
  }, [messages, newMessagesDivider]);

  const scrolledDividerChannelRef = useRef<string | null>(null);
  useEffect(() => {
    if (!scrollContainerEl || highlightMessageId || unreadAnchorIndex < 0) return;
    if (scrolledDividerChannelRef.current === channelId) return;
    scrolledDividerChannelRef.current = channelId ?? null;
    const anchorId = newMessagesDivider?.anchorMessageId ?? null;
    // Arrived = the anchor row is rendered AND pinned near the scroller's top.
    // getBoundingClientRect reflects the real post-scroll layout, so — unlike a
    // scrollTop check — it can't be fooled by scrollToIndex's async lag (which
    // reads scrollTop=0 for the first frames). Until the row is virtualized into
    // the DOM and reaches the top, keep re-issuing.
    const hasReached = () => {
      if (!anchorId) return false;
      const el = messageRefMap.get(anchorId);
      if (!el) return false;
      const rel = el.getBoundingClientRect().top - scrollContainerEl.getBoundingClientRect().top;
      return rel <= ANCHOR_TOP_BAND_PX;
    };
    // Measure-safe (react-virtuoso #883): the read cursor arrives ~100ms after
    // mount, so the list may still be measuring — re-issue until the anchor row
    // actually reaches the top, else a big-list far jump to the "N new messages"
    // divider lands far below the viewport.
    return scrollToIndexUntilSettled(virtuosoRef.current, hasReached, {
      index: firstItemIndex + unreadAnchorIndex,
      align: "start",
      behavior: "auto",
    });
  }, [
    scrollContainerEl,
    channelId,
    unreadAnchorIndex,
    highlightMessageId,
    firstItemIndex,
    virtuosoRef,
    newMessagesDivider,
    messageRefMap,
  ]);

  return { unreadAnchorIndex };
}
