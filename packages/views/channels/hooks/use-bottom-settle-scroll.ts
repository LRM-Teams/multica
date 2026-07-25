import { useEffect, useRef, type RefObject } from "react";
import type { VirtuosoHandle } from "react-virtuoso";
import type { ChannelMessage } from "@multica/core/types";
import { scrollToIndexUntilSettled } from "./use-unread-anchor-scroll";
import { useActiveScrollGesture } from "./use-active-scroll-gesture";

// "At the bottom" tolerance: the scroller is considered landed once its
// remaining distance to the bottom is within this band. Absorbs
// sub-pixel/measurement drift and the last row's own height jitter so we don't
// re-scroll forever chasing an exact 0. Matches the intent of the anchor path's
// ANCHOR_TOP_BAND_PX, just measured against the bottom edge.
const BOTTOM_BAND_PX = 24;

/**
 * Measure-safe "land at the bottom on cold open" settle for the DEFAULT case —
 * no deep-link highlight and no unread anchor, i.e. a normal channel/DM open
 * that should show the latest message.
 *
 * Why this exists (real-device P0, 2026-07-25): the latest-message position was
 * seeded ONLY by the mount-once declarative `initialTopMostItemIndex` prop. With
 * `customScrollParent` and asynchronously-loaded messages, that prop is
 * unreliable on a cold (fresh/no-cache) mount — Virtuoso can't apply the initial
 * scroll before it has measured the (lazily-rendered) rows, so the list is left
 * at scrollTop=0 (the oldest message pinned to the top) with nothing to correct
 * it. Iris measured exactly this: `initialTopMostItemIndex` computed to the last
 * index, yet the served list opened at scrollTop=0 with the oldest row at the
 * head. The unread-anchor path never hit this because it ALSO runs an imperative
 * `scrollToIndex` settle loop (#883) after mount; the imperative call is
 * reliable with customScrollParent where the declarative mount-once prop is not.
 * This hook gives the default-bottom case the same imperative safety net.
 *
 * Deliberately narrow: only activates when the caller wants the bottom
 * (`enabled` — no highlight, no unread anchor, initialScroll === "bottom") and
 * runs once per channel visit. It does not fight `followOutput` — both target
 * the bottom, so re-issuing `scrollToIndex(last)` while followOutput is also
 * pinning the bottom is idempotent, not a tug-of-war (unlike the anchor settle,
 * which must gate followOutput off because it targets a NON-bottom row). It DOES
 * yield to a live user gesture (#689), same as the anchor settle.
 */
export function useBottomSettleScroll({
  channelId,
  messages,
  enabled,
  handleAttached,
  virtuosoRef,
  scrollContainerEl,
  messageRefMap,
}: {
  channelId: string | undefined;
  messages: readonly ChannelMessage[];
  /**
   * True only when the default-bottom position actually owns the mount — no
   * deep-link highlight and no unread anchor, and the list wants to open at the
   * latest message (`initialScroll === "bottom"`). Mutually exclusive with the
   * unread-anchor settle, so the two never scroll at once.
   */
  enabled: boolean;
  /** Value-comparable readiness flag: flips true once Virtuoso's imperative
   * handle attaches (see useUnreadAnchorScroll for why it's a boolean, not the
   * handle object). */
  handleAttached: boolean;
  virtuosoRef: RefObject<VirtuosoHandle | null>;
  /** Virtuoso's `customScrollParent`, or null before it exists. Doubles as the
   * "scroller ready" gate and is measured to tell when the bottom is reached. */
  scrollContainerEl: HTMLElement | null;
  /** Rendered message-row DOM nodes keyed by message id. Used to confirm the
   * LAST row has actually rendered before trusting the scroll metrics — see
   * `hasReached` below. */
  messageRefMap: ReadonlyMap<string, HTMLElement>;
}): void {
  // Marks a channel visit's bottom scroll as resolved (reached the bottom band
  // or exhausted the settle timeout). NOT set at the top of the effect: an
  // "attempt was made" is not "the scroll is done", so a stray re-render must
  // not permanently block retries before the async settle finishes (#348 scar).
  const settledChannelRef = useRef<string | null>(null);

  // #689: yield to an active user touch/wheel gesture instead of fighting it.
  const activeGestureRef = useActiveScrollGesture(scrollContainerEl);

  useEffect(() => {
    if (!scrollContainerEl || !handleAttached || !enabled || messages.length === 0) return;
    if (settledChannelRef.current === channelId) return;

    const lastId = messages[messages.length - 1]?.id ?? null;

    // Arrived = the LAST row has actually rendered AND the scroller is within the
    // bottom band. The rendered-row gate is load-bearing: on a cold mount
    // Virtuoso hasn't measured its lazily-rendered rows yet, so the scroller can
    // report scrollHeight === clientHeight (distanceToBottom = 0) while the real
    // content is far taller and the last row isn't in the DOM. Trusting the
    // metric alone would false-settle on the very first frame — before any
    // scroll took effect — and never retry, exactly the measurement race (#883)
    // this settle exists to close. Requiring the last row in `messageRefMap`
    // means rows are rendered/measured, so the metric is meaningful.
    const hasReached = () => {
      if (!lastId || !messageRefMap.has(lastId)) return false;
      const distanceToBottom =
        scrollContainerEl.scrollHeight -
        scrollContainerEl.scrollTop -
        scrollContainerEl.clientHeight;
      const reached = distanceToBottom <= BOTTOM_BAND_PX;
      if (reached) settledChannelRef.current = channelId ?? null;
      return reached;
    };

    const lastIndex = Math.max(0, messages.length - 1);
    const disposeSettle = scrollToIndexUntilSettled(
      virtuosoRef.current,
      hasReached,
      // Local index (0..messages.length-1), never offset by firstItemIndex —
      // same index contract as every other scrollToIndex call (#1194). align
      // "end" pins the last row's bottom to the viewport bottom = at bottom.
      { index: lastIndex, align: "end", behavior: "auto" },
      {
        isGestureActive: () => activeGestureRef.current,
        onSettleTimeout: () => {
          // Give up cleanly instead of looping forever (e.g. rows never render).
          // Mark resolved and log for prod diagnosability; the declarative
          // initialTopMostItemIndex is the remaining fallback.
          settledChannelRef.current = channelId ?? null;
          // eslint-disable-next-line no-console
          console.warn(
            "[useBottomSettleScroll] settle timed out — never reached the bottom band",
            { channelId },
          );
        },
      },
    );
    return () => {
      disposeSettle();
    };
  }, [
    scrollContainerEl,
    handleAttached,
    enabled,
    channelId,
    virtuosoRef,
    messages,
    messageRefMap,
    activeGestureRef,
  ]);
}
