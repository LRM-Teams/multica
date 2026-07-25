import { useEffect, useRef } from "react";
import type { ChannelMessage } from "@multica/core/types";
import { useActiveScrollGesture } from "./use-active-scroll-gesture";
// DIAGNOSTIC ONLY (around-seq false-complete trace) — remove with the successor.
import { bssRecord } from "./bss-diagnostic";

// "At the bottom" tolerance: the last row counts as landed once its bottom edge
// sits within this band of the scroller's bottom edge. Absorbs sub-pixel /
// measurement drift and the last row's own height jitter so we don't re-write
// forever chasing an exact 0. Mirror of the anchor path's ANCHOR_TOP_BAND_PX,
// measured against the bottom edge instead of the top.
const BOTTOM_BAND_PX = 24;
// Frame cap backstop, matching the anchor settle's default.
const MAX_FRAMES = 180;

/**
 * "Land at the bottom on cold open" settle for the DEFAULT case — no deep-link
 * highlight and no unread anchor, i.e. a normal channel/DM open that should show
 * the latest message.
 *
 * Why direct scrollTop, not scrollToIndex (real-device P0, 2026-07-25): the
 * latest-message position was first seeded by the mount-once declarative
 * `initialTopMostItemIndex` (unreliable on a cold customScrollParent mount), then
 * by an imperative `scrollToIndex(lastIndex, align:"end")` settle. Both FAILED on
 * real devices: Iris measured the served build opening at scrollTop=0 with the
 * geometry predicate correctly reporting "not at bottom" — yet scrollToIndex
 * never moved the scroll. Root cause: scrollToIndex to the LAST index needs
 * Virtuoso's FULL content measurement to place it (unlike the unread-anchor
 * path's mid-list target, resolvable incrementally from the top), and that
 * measurement isn't ready during a cold mount.
 *
 * The scroll container is Virtuoso's `customScrollParent` — a real element WE
 * own. Writing its `scrollTop` directly moves the scroll regardless of Virtuoso's
 * internal measurement state (Iris confirmed on a real device: forcing
 * `scrollTop = scrollHeight` jumped to the bottom and stayed, no bounce). We
 * re-issue every frame so that as the rows render and `scrollHeight` grows
 * through the measurement-evolution window, `scrollTop` re-pins to the true
 * bottom each frame — not a one-shot. Completion is judged by the last row's
 * real geometry (#1211), immune to the scrollHeight metric lag.
 *
 * Ownership (Barry's contract): this owns the initial position ONLY for the
 * default-latest state (`enabled` — mutually exclusive with unread anchor and
 * deep-link highlight, which keep their own scrollToIndex positioning). On the
 * FIRST real user gesture during the settle it hands ownership to the user and
 * PERMANENTLY exits for this visit (no resume, unlike the anchor path's
 * pause-resume) — a cold mount starts at the top, so `isNearBottom` can't be used
 * to detect a real scroll-up; the gesture epoch is the durable signal. After it
 * exits (reached / gesture / timeout), the existing `followOutput` handles later
 * new messages once the user is at the bottom.
 */
export function useBottomSettleScroll({
  channelId,
  messages,
  enabled,
  handleAttached,
  scrollContainerEl,
  messageRefMap,
}: {
  channelId: string | undefined;
  messages: readonly ChannelMessage[];
  /**
   * True only when the default-bottom position owns the mount — no deep-link
   * highlight and no unread anchor, and the list wants to open at the latest
   * message (`initialScroll === "bottom"`).
   */
  enabled: boolean;
  /** True once Virtuoso's imperative handle has attached — a value-comparable
   * readiness signal so the effect re-runs the instant the list is live. */
  handleAttached: boolean;
  /** Virtuoso's `customScrollParent`, or null before it exists. We write its
   * `scrollTop` directly and measure its bottom edge. */
  scrollContainerEl: HTMLElement | null;
  /** Rendered message-row DOM nodes keyed by message id — the last row's real
   * geometry is the completion check. */
  messageRefMap: ReadonlyMap<string, HTMLElement>;
}): void {
  // Marks a channel visit's bottom scroll as resolved (reached the bottom band,
  // handed off to a user gesture, or exhausted the frame cap). NOT set at the top
  // of the effect: an "attempt was made" is not "done", so a stray re-render must
  // not permanently block retries before the async settle finishes (#348 scar).
  const settledChannelRef = useRef<string | null>(null);

  const { activeGestureRef, gestureEpochRef } = useActiveScrollGesture(scrollContainerEl);

  // The gesture-handoff baseline is PER CHANNEL VISIT, not per effect run.
  // Captured once when the visit begins and preserved across every effect
  // cleanup/re-run within the same visit; reset only on a real channel change.
  // If it were re-captured each effect run (the effect depends on `messages`), a
  // normal message-array churn arriving after a gesture — but before the pending
  // frame observed the epoch and cancelled by cleanup — would re-baseline the
  // now-bumped epoch, see the gesture already released (active=false), and
  // resume direct-write, re-stealing scroll ownership from the user (Barry's
  // race). Snapshotting at visit start makes the epoch bump durably observable.
  const visitBaselineRef = useRef<{ channelId: string | null; epoch: number }>({
    channelId: null,
    epoch: 0,
  });
  if (visitBaselineRef.current.channelId !== (channelId ?? null)) {
    visitBaselineRef.current = {
      channelId: channelId ?? null,
      epoch: gestureEpochRef.current,
    };
  }

  useEffect(() => {
    if (!scrollContainerEl || !handleAttached || !enabled || messages.length === 0) return;
    if (settledChannelRef.current === channelId) return;

    const lastId = messages[messages.length - 1]?.id ?? null;
    // Per-visit baseline (see visitBaselineRef): any epoch beyond it means a real
    // touch/wheel began at some point this visit, so we hand off and stay off —
    // durably across effect re-runs, not just for this rAF chain.
    const startEpoch = visitBaselineRef.current.epoch;

    // DIAGNOSTIC: one entry per effect run — shows when `messages` populates
    // (e.g. 0→69 as the around page arrives) and re-targets the settle.
    bssRecord("effect", {
      msgLen: messages.length,
      tailInMap: !!lastId && messageRefMap.has(lastId),
      scrollTop: scrollContainerEl.scrollTop,
      scrollHeight: scrollContainerEl.scrollHeight,
      clientHeight: scrollContainerEl.clientHeight,
    });

    // Arrived = the last row is rendered AND its BOTTOM edge is within the band
    // of the scroller's bottom edge — real geometry, mirroring the unread-anchor
    // check (its top edge). Immune to the scrollHeight metric lag (an earlier
    // version trusted `scrollHeight - scrollTop - clientHeight`, which reads 0
    // transiently on a cold mount and false-settled at the top).
    const hasReached = () => {
      if (!lastId) return false;
      const el = messageRefMap.get(lastId);
      if (!el) {
        // DIAGNOSTIC: tail row not yet in the DOM/ref map.
        bssRecord("reach", { tailInMap: false, result: false });
        return false;
      }
      const rowBottom = el.getBoundingClientRect().bottom;
      const containerBottom = scrollContainerEl.getBoundingClientRect().bottom;
      const delta = rowBottom - containerBottom;
      const result = delta <= BOTTOM_BAND_PX;
      // DIAGNOSTIC: the exact geometry the completion decision is made on —
      // whether the tail row's bottom is within the band while content may still
      // be measuring (the suspected frame-of-false-completion).
      bssRecord("reach", {
        tailInMap: true,
        rowBottom: Math.round(rowBottom),
        containerBottom: Math.round(containerBottom),
        delta: Math.round(delta),
        scrollTop: scrollContainerEl.scrollTop,
        scrollHeight: scrollContainerEl.scrollHeight,
        result,
      });
      return result;
    };

    let raf = 0;
    let frame = 0;
    const tick = () => {
      // Permanent ownership handoff on any real user gesture — do NOT resume
      // even after touchend / wheel-idle; the user owns scroll now. Two signals:
      //  - `activeGestureRef.current`: a gesture is in progress RIGHT NOW,
      //    including one already underway when this effect ran / re-targeted
      //    (the epoch baseline captured below would miss that one).
      //  - epoch changed: a NEW gesture started at some point since we began.
      if (activeGestureRef.current || gestureEpochRef.current !== startEpoch) {
        settledChannelRef.current = channelId ?? null;
        bssRecord("settled", { reason: "gesture", frame });
        return;
      }
      // Directly pin the owned scroll parent to its current bottom. As rows
      // render and scrollHeight grows, this re-pins to the true bottom each
      // frame (measurement-evolution safe); the browser clamps to max scroll.
      const stBefore = scrollContainerEl.scrollTop;
      scrollContainerEl.scrollTop = scrollContainerEl.scrollHeight;
      frame += 1;
      // DIAGNOSTIC: did the direct write actually move scrollTop this frame?
      bssRecord("write", {
        frame,
        stBefore: Math.round(stBefore),
        stAfter: Math.round(scrollContainerEl.scrollTop),
        scrollHeight: scrollContainerEl.scrollHeight,
        clientHeight: scrollContainerEl.clientHeight,
      });
      if (hasReached()) {
        settledChannelRef.current = channelId ?? null;
        bssRecord("settled", { reason: "reached", frame });
        return;
      }
      if (frame >= MAX_FRAMES) {
        settledChannelRef.current = channelId ?? null;
        bssRecord("settled", { reason: "timeout", frame });
        // eslint-disable-next-line no-console
        console.warn(
          "[useBottomSettleScroll] settle timed out — never reached the bottom band",
          { channelId },
        );
        return;
      }
      raf = requestAnimationFrame(tick);
    };
    tick();
    return () => {
      if (raf) cancelAnimationFrame(raf);
    };
  }, [
    scrollContainerEl,
    handleAttached,
    enabled,
    channelId,
    messages,
    messageRefMap,
    activeGestureRef,
    gestureEpochRef,
  ]);
}
