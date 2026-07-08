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
// small list converges in one. So we re-issue every animation frame and stop
// when the landing position holds steady — i.e. the container's `scrollTop` is
// unchanged across consecutive frames, meaning measurement has converged and the
// target index is actually where it should be — with a frame cap as a backstop.
// `behavior: "auto"` makes each repeat re-pin the same index idempotently (no
// jank). Returns a disposer so the caller can cancel on re-target/unmount.
export function scrollToIndexUntilSettled(
  handle: VirtuosoHandle | null,
  scroller: Pick<HTMLElement, "scrollTop"> | null,
  location: { index: number; align: "start" | "center" | "end"; behavior?: "auto" | "smooth" },
  maxFrames = 40,
): () => void {
  if (!handle) return () => {};
  let raf = 0;
  let frame = 0;
  let lastTop = Number.NaN;
  let stableFrames = 0;
  const tick = () => {
    handle.scrollToIndex(location);
    frame += 1;
    // Converged once the landing scrollTop stops moving between frames. With no
    // scroller to poll, NaN !== NaN keeps `stableFrames` at 0 → fall back to the
    // frame cap (still idempotent re-issues).
    const top = scroller ? scroller.scrollTop : Number.NaN;
    if (top === lastTop) {
      stableFrames += 1;
    } else {
      stableFrames = 0;
      lastTop = top;
    }
    if (stableFrames >= 2 || frame >= maxFrames) return;
    raf = requestAnimationFrame(tick);
  };
  tick();
  return () => {
    if (raf) cancelAnimationFrame(raf);
  };
}

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
   * viewport. It's also polled by the settle helper to detect when the landing
   * position has converged.
   */
  scrollContainerEl: HTMLElement | null;
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
    // Measure-safe (react-virtuoso #883): the read cursor arrives ~100ms after
    // mount, so the list may still be measuring — re-issue until the landing
    // scrollTop converges (not a fixed frame count), else a big-list far jump to
    // the "N new messages" divider lands far below the viewport.
    return scrollToIndexUntilSettled(virtuosoRef.current, scrollContainerEl, {
      index: firstItemIndex + unreadAnchorIndex,
      align: "start",
      behavior: "auto",
    });
  }, [scrollContainerEl, channelId, unreadAnchorIndex, highlightMessageId, firstItemIndex, virtuosoRef]);

  return { unreadAnchorIndex };
}
