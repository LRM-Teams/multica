import { useEffect, useRef } from "react";
import type { ChannelMessage } from "@multica/core/types";
import { useActiveScrollGesture } from "./use-active-scroll-gesture";

// "At the bottom" tolerance: the last row counts as landed once its bottom edge
// sits within this band of the scroller's bottom edge. Absorbs sub-pixel /
// measurement drift and the last row's own height jitter so we don't re-write
// forever chasing an exact 0. Mirror of the anchor path's ANCHOR_TOP_BAND_PX,
// measured against the bottom edge instead of the top.
const BOTTOM_BAND_PX = 24;
// Frame cap backstop, matching the anchor settle's default.
const MAX_FRAMES = 180;
// After the tail row first lands in the bottom band, keep the loop alive (reading
// geometry, writing only when it drifts back out) for this many frames.
//
// LRM-1220 root cause: an attached bottom-band frame is NOT proof the list is
// fully measured. On a cold mount Virtuoso — seeded with
// `initialTopMostItemIndex = last` — renders ONLY the tail rows first, so the
// scroller is still collapsed (`scrollHeight === clientHeight`, the pin write
// clamps to a no-op) while the tail row's bottom edge sits exactly on the
// container bottom: `hasReached()` is true on frame one and the settle used to
// complete PERMANENTLY. A few hundred ms later the rows above the tail measure,
// the content grows BELOW a scrollTop that is still 0, and with the loop gone
// nothing re-pins — the open lands on the oldest loaded row (jianghp3: every
// mobile open stuck on 「今天」+ 当日第一条 with 「加载更早消息」 at the top,
// hours behind the newest message). The `pinnedThisFrame` gate does not cover
// this: during a cold mount the handle IS attached.
//
// So arrival stops the *writes*, not the *loop*. The window must outlast the
// measurement-evolution tail (real-device trace: ~440ms) with margin, while
// staying well inside MAX_FRAMES' spirit as a bounded budget. A user gesture
// still ends everything immediately and permanently — the re-pin never
// out-ranks the user (see the gesture check in `tick`).
const POST_REACH_WATCH_FRAMES = 90;

/**
 * "Land at the bottom on cold open" settle for the DEFAULT case — no deep-link
 * highlight and no unread anchor, i.e. a normal channel / DM / thread open that
 * should show the latest message.
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
 * to detect a real scroll-up; the gesture epoch is the durable signal. Reaching
 * the bottom band stops the per-frame writes but keeps the loop alive for a
 * bounded watch window (LRM-1220), so late measurement growth that drops the
 * viewport back to the top is corrected instead of stranded. After it exits
 * (watch window elapsed / gesture / timeout), the existing `followOutput` handles
 * later new messages once the user is at the bottom.
 */
export function useBottomSettleScroll({
  channelId,
  messages,
  enabled,
  listReady,
  scrollContainerEl,
  messageRefMap,
}: {
  channelId: string | undefined;
  messages: readonly ChannelMessage[];
  /**
   * True only when the default-bottom position owns the mount — no deep-link
   * highlight and no unread anchor. Every chat surface (channel, DM, thread)
   * wants the latest row on open (LRM-1156).
   */
  enabled: boolean;
  /**
   * True while the rows in the scroll container are a settled render we may pin
   * against. Two ways the list can be live, and the settle must serve BOTH:
   *  - the virtualized path: Virtuoso's imperative handle has attached (a
   *    value-comparable readiness signal, so a mid-mount detach/reattach flap is
   *    observable per frame instead of killing the settle);
   *  - the non-virtualized direct-fallback path (LRM-1220): Virtuoso is not
   *    rendered at all, so its handle is permanently null. Reading "no handle" as
   *    "not ready" there meant the settle NEVER wrote a single scrollTop, timed
   *    out after the frame cap, and left the open resting at scrollTop 0 — the
   *    oldest loaded row. That is the mobile-web report ("每次进群都跳到今天第一条
   *    消息"): the fallback renders plain divs with no landing logic of its own,
   *    so this settle is the ONLY thing that can put the newest row on screen.
   */
  listReady: boolean;
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

  // Live `listReady` for the settle loop to read each frame. The loop must
  // SURVIVE Virtuoso detaching/reattaching its imperative handle during the
  // cold-mount measurement window (real-device trace 2026-07-26): a mid-settle
  // detach cancelled the rAF chain, and the effect re-run that fired when the tail
  // finally became measurable (~440ms later) happened to see `listReady=false`
  // and bailed on the guard → the settle never re-armed → stuck at the top. Reading
  // it via a ref (NOT as an effect dep) keeps ONE persistent loop alive across the
  // flaps instead of cancel-restarting; and because `messageRefMap` is a stable ref
  // (the tail row entering it never re-triggers the effect), only a live loop that
  // keeps checking geometry every frame can catch the tail the moment it mounts.
  const listReadyRef = useRef(listReady);
  listReadyRef.current = listReady;

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
    const lastId = messages[messages.length - 1]?.id ?? null;

    // NOTE: `listReady` is deliberately NOT gated here (nor an effect dep) —
    // the loop starts as soon as the container + data exist and reads the LIVE
    // attach state per frame, so a transient detach can't kill the settle.
    if (!scrollContainerEl || !enabled || messages.length === 0) return;
    if (settledChannelRef.current === channelId) return;

    // Per-visit baseline (see visitBaselineRef): any epoch beyond it means a real
    // touch/wheel began at some point this visit, so we hand off and stay off —
    // durably across effect re-runs, not just for this rAF chain.
    const startEpoch = visitBaselineRef.current.epoch;

    let raf = 0;
    let frame = 0;
    // Set the first time an attached, pinned frame confirms the tail row in the
    // bottom band. From then on the loop stops writing unconditionally and only
    // corrects drift (see POST_REACH_WATCH_FRAMES).
    let reached = false;
    let watchFrame = 0;

    // Arrived = the last row is rendered AND its BOTTOM edge is within the band
    // of the scroller's bottom edge — real geometry, mirroring the unread-anchor
    // check (its top edge). Immune to the scrollHeight metric lag (an earlier
    // version trusted `scrollHeight - scrollTop - clientHeight`, which reads 0
    // transiently on a cold mount and false-settled at the top).
    const hasReached = () => {
      if (!lastId) return false;
      const el = messageRefMap.get(lastId);
      if (!el) return false;
      const rowBottom = el.getBoundingClientRect().bottom;
      const containerBottom = scrollContainerEl.getBoundingClientRect().bottom;
      return rowBottom - containerBottom <= BOTTOM_BAND_PX;
    };

    const tick = () => {
      // Permanent ownership handoff on any real user gesture — do NOT resume
      // even after touchend / wheel-idle; the user owns scroll now. Two signals:
      //  - `activeGestureRef.current`: a gesture is in progress RIGHT NOW,
      //    including one already underway when this effect ran / re-targeted
      //    (the epoch baseline captured below would miss that one).
      //  - epoch changed: a NEW gesture started at some point since we began.
      if (activeGestureRef.current || gestureEpochRef.current !== startEpoch) {
        settledChannelRef.current = channelId ?? null;
        return;
      }
      // Pin the owned scroll parent to its current bottom — but ONLY while
      // Virtuoso's handle is attached. During a transient detach/reattach we keep
      // the loop alive and skip the write (the container may be mid-remount), so a
      // detach can't permanently kill the settle before the tail becomes
      // measurable. As rows render and scrollHeight grows, this re-pins to the true
      // bottom each frame (measurement-evolution safe); the browser clamps to max.
      let pinnedThisFrame = false;
      if (listReadyRef.current) {
        // Once arrived, write ONLY when the tail has drifted back out of the band
        // (the measurement-evolution growth this window exists to catch). A
        // read-only watch frame keeps the healthy path from re-writing scrollTop
        // ~90 extra times per open.
        if (!reached || !hasReached()) {
          scrollContainerEl.scrollTop = scrollContainerEl.scrollHeight;
        }
        pinnedThisFrame = true;
      }
      frame += 1;
      // Arrival ONLY counts in a frame where the handle was attached (so the
      // direct write above was live). Gating on `pinnedThisFrame` is the
      // successor contract's teeth: while the handle is DETACHED a
      // detach/remount can transiently put the tail in the ref map with a
      // geometry that momentarily lands in the band (the untrustworthy
      // measurement-evolution window), and arriving there on zero attached pin
      // would start the watch countdown against a list that has not moved at all.
      // We do NOT require the pin to have MOVED scrollTop, so a warm mount
      // already at the bottom (no-op write) still arrives on frame one.
      if (!reached && pinnedThisFrame && hasReached()) {
        reached = true;
      }
      if (reached) {
        watchFrame += 1;
        // Bounded hand-back: after the watch window, Virtuoso's `followOutput`
        // owns "stick to bottom for new messages" as before.
        if (watchFrame >= POST_REACH_WATCH_FRAMES) {
          settledChannelRef.current = channelId ?? null;
          return;
        }
      } else if (frame >= MAX_FRAMES) {
        settledChannelRef.current = channelId ?? null;
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
    // `listReady` is intentionally excluded — the loop reads it live via
    // `listReadyRef` so a detach/reattach flap doesn't cancel-restart (and
    // potentially bail) the settle. See listReadyRef above.
  }, [
    scrollContainerEl,
    enabled,
    channelId,
    messages,
    messageRefMap,
    activeGestureRef,
    gestureEpochRef,
  ]);
}
