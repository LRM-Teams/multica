import { useEffect, useRef, type RefObject } from "react";

export interface ActiveScrollGesture {
  /**
   * True WHILE a touch/wheel gesture is in progress. A settle loop that
   * pause-resumes (the unread-anchor path) reads this each frame to yield the
   * frame and resume after release.
   */
  activeGestureRef: RefObject<boolean>;
  /**
   * Monotonic counter bumped once at the START of every touch/wheel gesture. A
   * settle that must PERMANENTLY hand ownership to the user on the first real
   * gesture (the default-bottom direct-scroll path) captures this at start and
   * aborts for the visit if it ever changes — a persistent signal that survives
   * gesture release / wheel-idle, unlike the boolean which flips back to false.
   */
  gestureEpochRef: RefObject<number>;
}

/**
 * #689: tracks user touch/wheel gestures on the scroll container so a cold-load
 * settle can defer to them instead of fighting native scroll — the jank only
 * shows up scrolling *during* cold load (real device only; headless scrollTop
 * injection doesn't trigger real touch state and can't reproduce it, see the
 * #689 audit).
 *
 * Exposes two REF signals (never state — they flip on every gesture tick and
 * must not trigger a re-render):
 *  - `activeGestureRef`: in-progress boolean, for pause-resume settles.
 *  - `gestureEpochRef`: a start-of-gesture counter, for a settle that must
 *    permanently abort on the first real interaction (and stay aborted after
 *    release), where a flips-back boolean would wrongly let it resume.
 *
 * Shared by every cold-load settle so there is one gesture-gate implementation,
 * not a per-hook copy that could drift.
 */
export function useActiveScrollGesture(
  scrollContainerEl: HTMLElement | null,
): ActiveScrollGesture {
  const activeGestureRef = useRef(false);
  const gestureEpochRef = useRef(0);
  useEffect(() => {
    if (!scrollContainerEl) return;
    const beginGesture = () => {
      activeGestureRef.current = true;
      gestureEpochRef.current += 1;
    };
    const setInactive = () => {
      activeGestureRef.current = false;
    };
    // Wheel has no native "end" event — debounce idle to infer release. Only the
    // FIRST wheel tick of a burst bumps the epoch (a burst is one gesture); the
    // active flag stays true and the idle timer is what eventually clears it.
    let wheelIdleTimer: ReturnType<typeof setTimeout> | null = null;
    const onWheel = () => {
      if (!activeGestureRef.current) gestureEpochRef.current += 1;
      activeGestureRef.current = true;
      if (wheelIdleTimer) clearTimeout(wheelIdleTimer);
      wheelIdleTimer = setTimeout(setInactive, 150);
    };
    scrollContainerEl.addEventListener("touchstart", beginGesture, { passive: true });
    scrollContainerEl.addEventListener("touchend", setInactive, { passive: true });
    scrollContainerEl.addEventListener("touchcancel", setInactive, { passive: true });
    scrollContainerEl.addEventListener("wheel", onWheel, { passive: true });
    return () => {
      if (wheelIdleTimer) clearTimeout(wheelIdleTimer);
      scrollContainerEl.removeEventListener("touchstart", beginGesture);
      scrollContainerEl.removeEventListener("touchend", setInactive);
      scrollContainerEl.removeEventListener("touchcancel", setInactive);
      scrollContainerEl.removeEventListener("wheel", onWheel);
    };
  }, [scrollContainerEl]);
  return { activeGestureRef, gestureEpochRef };
}
