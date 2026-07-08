import { useEffect, useMemo, useRef, useState, type RefObject } from "react";
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
// applies ("hasn't moved yet" is indistinguishable from "arrived").
//
// A frame cap backstops a target that never renders (e.g. it was deleted mid-
// settle). `onSettleTimeout` fires exactly once if the cap is hit without ever
// reaching — callers use it to fall back to a safe position AND log, so a stuck
// settle is diagnosable instead of a silent no-op (#348 postmortem: the caller
// previously self-marked "done" after a single failed check, permanently
// blocking retries — this only gives up after genuinely exhausting the cap).
//
// `behavior: "auto"` makes each repeat re-pin the same index idempotently (no
// jank). Returns a disposer so the caller can cancel on re-target/unmount.
export function scrollToIndexUntilSettled(
  handle: VirtuosoHandle | null,
  hasReached: () => boolean,
  location: { index: number; align: "start" | "center" | "end"; behavior?: "auto" | "smooth" },
  options?: { maxFrames?: number; onSettleTimeout?: () => void },
): () => void {
  if (!handle) {
    // Permanent, not temp: a null handle here means the caller fired before
    // Virtuoso attached its ref (or after it detached) — a silent no-op with no
    // scroll, no warn, no state change, indistinguishable from every other
    // "nothing happened" failure mode. Caught the team out twice (#348) before
    // this log existed. Cheap and rare enough in the healthy path to keep always-on.
    // eslint-disable-next-line no-console
    console.warn("[scrollToIndexUntilSettled] called with a null Virtuoso handle — no-op", { location });
    return () => {};
  }
  const maxFrames = options?.maxFrames ?? 180;
  let raf = 0;
  let frame = 0;
  const tick = () => {
    handle.scrollToIndex(location);
    frame += 1;
    if (hasReached()) return;
    if (frame >= maxFrames) {
      options?.onSettleTimeout?.();
      return;
    }
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
  handleAttached,
  virtuosoRef,
  scrollContainerEl,
  messageRefMap,
}: {
  channelId: string | undefined;
  messages: readonly ChannelMessage[];
  newMessagesDivider: NewMessagesDivider | null;
  highlightMessageId: string | null | undefined;
  firstItemIndex: number;
  /**
   * True once Virtuoso's imperative handle has attached — a value-comparable
   * BOOLEAN, not the handle object itself. Ref attachment doesn't trigger a
   * re-render, so an effect gated only on `scrollContainerEl` can run in the
   * exact render where the handle is still null and never get a second
   * chance (#348 root cause). We need a state value that flips when the
   * handle attaches, but NOT the handle object: react-virtuoso hands the
   * callback ref different object identities across some renders, so storing
   * the object (even with a reference-equality bail) doesn't actually bail —
   * it cascades into setState → re-render → new handle object → setState →
   * ... an infinite loop (shipped and reverted once — #365 incident). A
   * boolean compares by value, so React's built-in same-value bailout stops
   * the cascade regardless of how many identities the ref receives, as long
   * as attached-ness doesn't change. The handle itself is still read live off
   * `virtuosoRef.current` when actually scrolling.
   */
  handleAttached: boolean;
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
}): { unreadAnchorIndex: number; isAnchorSettling: boolean } {
  const unreadAnchorIndex = useMemo(() => {
    if (!newMessagesDivider) return -1;
    return messages.findIndex((m) => m.id === newMessagesDivider.anchorMessageId);
  }, [messages, newMessagesDivider]);

  // Whether the settle loop below currently owns scroll position. Virtuoso's
  // own `followOutput` ("stick to bottom on new content") defaults on before
  // the real cold-load position is known (#348 postmortem: `isNearBottom`
  // starts `true`, and the anchor may not arrive until ~100ms after mount, so
  // the initial mount lands at the bottom) — if both are live at once they
  // fight every frame: the settle loop's `scrollToIndex` moves toward the
  // anchor, `followOutput` smooth-scrolls back to the bottom, and
  // `hasReached()` never sees the anchor arrive. At any moment scroll
  // position may have only ONE owner: the caller must gate its own
  // `followOutput` on `!isAnchorSettling` while this is true.
  const [isAnchorSettling, setIsAnchorSettling] = useState(false);

  // A stable primitive, not the whole `newMessagesDivider` object — that object
  // is a fresh `useMemo` result on every recompute (e.g. a `messages` refetch
  // after the mark-read echo lands), even when the anchor itself hasn't changed.
  // Keying the effect below on the object identity was the root cause of #348's
  // failure: it re-ran the effect on every such churn, and each re-run's cleanup
  // cancelled the in-flight settle loop's pending frame.
  const anchorId = newMessagesDivider?.anchorMessageId ?? null;

  // Marks a channel visit's anchor scroll as resolved — either it actually
  // reached (see `hasReached` below) or it gave up after exhausting the settle
  // timeout. Deliberately NOT set at the top of the effect: doing so previously
  // treated "an attempt was made" as "the scroll is done", so if the effect
  // re-ran before the async settle loop finished (any dependency touched by a
  // stray re-render), the guard silently blocked every subsequent retry after
  // just one failed geometry check — the list stayed at its cold-load fallback
  // position forever. Only a genuine outcome (reached or timed out) may set it.
  const scrolledDividerChannelRef = useRef<string | null>(null);
  useEffect(() => {
    if (!scrollContainerEl || !handleAttached || highlightMessageId || unreadAnchorIndex < 0) return;
    if (scrolledDividerChannelRef.current === channelId) return;
    // Claim scroll ownership for the duration of the settle loop — see
    // `isAnchorSettling` above for why this must exclude `followOutput`.
    setIsAnchorSettling(true);
    // Arrived = the anchor row is rendered AND pinned near the scroller's top.
    // getBoundingClientRect reflects the real post-scroll layout, so — unlike a
    // scrollTop check — it can't be fooled by scrollToIndex's async lag (which
    // reads scrollTop=0 for the first frames). Until the row is virtualized into
    // the DOM and reaches the top, keep re-issuing.
    // TEMP DIAGNOSTIC (#348 H2 retest) — remove only after Iris confirms PASS.
    let frameNum = 0;
    const hasReached = () => {
      frameNum += 1;
      if (!anchorId) return false;
      const el = messageRefMap.get(anchorId);
      const reached = !!el && el.getBoundingClientRect().top - scrollContainerEl.getBoundingClientRect().top <= ANCHOR_TOP_BAND_PX;
      // eslint-disable-next-line no-console
      console.log("[#348 H2 diag] frame", frameNum, {
        elFound: !!el,
        reached,
        scrollContainerElScrollTop: scrollContainerEl.scrollTop,
        target: { index: firstItemIndex + unreadAnchorIndex, align: "start" },
      });
      if (reached) {
        scrolledDividerChannelRef.current = channelId ?? null;
        setIsAnchorSettling(false);
      }
      return reached;
    };
    // Measure-safe (react-virtuoso #883): the read cursor arrives ~100ms after
    // mount, so the list may still be measuring — re-issue until the anchor row
    // actually reaches the top, else a big-list far jump to the "N new messages"
    // divider lands far below the viewport.
    const disposeSettle = scrollToIndexUntilSettled(
      virtuosoRef.current,
      hasReached,
      { index: firstItemIndex + unreadAnchorIndex, align: "start", behavior: "auto" },
      {
        onSettleTimeout: () => {
          // Give up cleanly instead of retrying forever (e.g. the anchor row was
          // deleted and can never render) — mark this visit resolved so we don't
          // loop, log it for prod diagnosability, and fall back to the list's
          // default: latest message.
          scrolledDividerChannelRef.current = channelId ?? null;
          // eslint-disable-next-line no-console
          console.warn(
            "[useUnreadAnchorScroll] settle timed out — anchor row never reached the top band, falling back to latest",
            { channelId, anchorId },
          );
          virtuosoRef.current?.scrollToIndex({
            index: firstItemIndex + Math.max(0, messages.length - 1),
            align: "start",
            behavior: "auto",
          });
          setIsAnchorSettling(false);
        },
      },
    );
    // Release ownership on re-target/unmount too — otherwise a cancelled
    // settle (deps changed mid-flight) would leave `followOutput` permanently
    // gated off with nothing left to ever un-gate it.
    return () => {
      disposeSettle();
      setIsAnchorSettling(false);
    };
  }, [
    scrollContainerEl,
    handleAttached,
    channelId,
    unreadAnchorIndex,
    highlightMessageId,
    firstItemIndex,
    virtuosoRef,
    anchorId,
    messageRefMap,
    messages,
  ]);

  return { unreadAnchorIndex, isAnchorSettling };
}
