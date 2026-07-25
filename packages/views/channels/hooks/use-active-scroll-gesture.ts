import { useEffect, useRef, type RefObject } from "react";

/**
 * #689: tracks whether the user currently has an active touch/wheel gesture on
 * the scroll container, so a measure-safe settle loop can YIELD to it instead of
 * re-issuing `scrollToIndex` on top of native scroll every frame — the jank only
 * shows up scrolling *during* cold load (real device only; headless scrollTop
 * injection doesn't trigger real touch state and can't reproduce it, see the
 * #689 audit).
 *
 * Returns a REF, not state: it flips on every touchstart/touchend/wheel tick and
 * must never trigger a re-render. Shared by every cold-load settle (unread
 * anchor + default bottom) so there is one gesture-gate implementation, not a
 * per-hook copy that could drift.
 */
export function useActiveScrollGesture(
  scrollContainerEl: HTMLElement | null,
): RefObject<boolean> {
  const activeGestureRef = useRef(false);
  useEffect(() => {
    if (!scrollContainerEl) return;
    const setActive = () => {
      activeGestureRef.current = true;
    };
    const setInactive = () => {
      activeGestureRef.current = false;
    };
    // Wheel has no native "end" event — debounce idle to infer release.
    let wheelIdleTimer: ReturnType<typeof setTimeout> | null = null;
    const onWheel = () => {
      activeGestureRef.current = true;
      if (wheelIdleTimer) clearTimeout(wheelIdleTimer);
      wheelIdleTimer = setTimeout(setInactive, 150);
    };
    scrollContainerEl.addEventListener("touchstart", setActive, { passive: true });
    scrollContainerEl.addEventListener("touchend", setInactive, { passive: true });
    scrollContainerEl.addEventListener("touchcancel", setInactive, { passive: true });
    scrollContainerEl.addEventListener("wheel", onWheel, { passive: true });
    return () => {
      if (wheelIdleTimer) clearTimeout(wheelIdleTimer);
      scrollContainerEl.removeEventListener("touchstart", setActive);
      scrollContainerEl.removeEventListener("touchend", setInactive);
      scrollContainerEl.removeEventListener("touchcancel", setInactive);
      scrollContainerEl.removeEventListener("wheel", onWheel);
    };
  }, [scrollContainerEl]);
  return activeGestureRef;
}
