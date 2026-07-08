import { useEffect, useMemo, useRef, type RefObject } from "react";
import type { VirtuosoHandle } from "react-virtuoso";
import type { ChannelMessage } from "@multica/core/types";
import type { NewMessagesDivider } from "./use-new-messages-divider";

// react-virtuoso #883: on a cold load `scrollToIndex` can run before the list's
// item heights are measured, so it lands at the wrong offset (the unread divider
// dropping far below the viewport was exactly this). The maintainer-acknowledged
// fix is to re-issue the scroll after mount until it settles. We re-issue across
// a few animation frames — as measurement completes the target converges, and
// with `behavior: "auto"` the repeats just re-pin the same index (idempotent, no
// jank). Returns a disposer so the caller can cancel on re-target/unmount.
export function scrollToIndexUntilSettled(
  handle: VirtuosoHandle | null,
  location: { index: number; align: "start" | "center" | "end"; behavior?: "auto" | "smooth" },
  frames = 6,
): () => void {
  if (!handle) return () => {};
  let raf = 0;
  let remaining = frames;
  const tick = () => {
    handle.scrollToIndex(location);
    if (remaining > 0) {
      remaining -= 1;
      raf = requestAnimationFrame(tick);
    }
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
}: {
  channelId: string | undefined;
  messages: readonly ChannelMessage[];
  newMessagesDivider: NewMessagesDivider | null;
  highlightMessageId: string | null | undefined;
  firstItemIndex: number;
  virtuosoRef: RefObject<VirtuosoHandle | null>;
}): { unreadAnchorIndex: number } {
  const unreadAnchorIndex = useMemo(() => {
    if (!newMessagesDivider) return -1;
    return messages.findIndex((m) => m.id === newMessagesDivider.anchorMessageId);
  }, [messages, newMessagesDivider]);

  const scrolledDividerChannelRef = useRef<string | null>(null);
  useEffect(() => {
    if (highlightMessageId || unreadAnchorIndex < 0) return;
    if (scrolledDividerChannelRef.current === channelId) return;
    scrolledDividerChannelRef.current = channelId ?? null;
    // Measure-safe (react-virtuoso #883): the read cursor arrives ~100ms after
    // mount, so the list may still be measuring — re-issue until it settles, else
    // the "N new messages" divider lands far below the viewport.
    return scrollToIndexUntilSettled(virtuosoRef.current, {
      index: firstItemIndex + unreadAnchorIndex,
      align: "start",
      behavior: "auto",
    });
  }, [channelId, unreadAnchorIndex, highlightMessageId, firstItemIndex, virtuosoRef]);

  return { unreadAnchorIndex };
}
