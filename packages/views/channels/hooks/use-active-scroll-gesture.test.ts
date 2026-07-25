// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { renderHook } from "@testing-library/react";
import { useActiveScrollGesture } from "./use-active-scroll-gesture";

beforeEach(() => {
  vi.useFakeTimers();
});
afterEach(() => {
  vi.useRealTimers();
});

describe("useActiveScrollGesture", () => {
  it("flips active on touchstart and inactive on touchend", () => {
    const el = document.createElement("div");
    const { result } = renderHook(() => useActiveScrollGesture(el));
    expect(result.current.activeGestureRef.current).toBe(false);
    el.dispatchEvent(new Event("touchstart"));
    expect(result.current.activeGestureRef.current).toBe(true);
    el.dispatchEvent(new Event("touchend"));
    expect(result.current.activeGestureRef.current).toBe(false);
  });

  it("treats touchcancel as release", () => {
    const el = document.createElement("div");
    const { result } = renderHook(() => useActiveScrollGesture(el));
    el.dispatchEvent(new Event("touchstart"));
    expect(result.current.activeGestureRef.current).toBe(true);
    el.dispatchEvent(new Event("touchcancel"));
    expect(result.current.activeGestureRef.current).toBe(false);
  });

  it("goes active on wheel and releases after the idle debounce", () => {
    const el = document.createElement("div");
    const { result } = renderHook(() => useActiveScrollGesture(el));
    el.dispatchEvent(new Event("wheel"));
    expect(result.current.activeGestureRef.current).toBe(true);
    // Still active before the 150ms idle window elapses.
    vi.advanceTimersByTime(149);
    expect(result.current.activeGestureRef.current).toBe(true);
    vi.advanceTimersByTime(1);
    expect(result.current.activeGestureRef.current).toBe(false);
  });

  it("keeps extending the wheel idle window on each tick", () => {
    const el = document.createElement("div");
    const { result } = renderHook(() => useActiveScrollGesture(el));
    el.dispatchEvent(new Event("wheel"));
    vi.advanceTimersByTime(100);
    el.dispatchEvent(new Event("wheel")); // resets the debounce
    vi.advanceTimersByTime(100);
    expect(result.current.activeGestureRef.current).toBe(true); // reset at 100
    vi.advanceTimersByTime(50);
    expect(result.current.activeGestureRef.current).toBe(false);
  });

  it("bumps the gesture epoch once per gesture START (durable across release)", () => {
    const el = document.createElement("div");
    const { result } = renderHook(() => useActiveScrollGesture(el));
    const epoch = result.current.gestureEpochRef;
    expect(epoch.current).toBe(0);
    el.dispatchEvent(new Event("touchstart"));
    expect(epoch.current).toBe(1);
    // Release does NOT reset the epoch — the bump is durable so a permanent-abort
    // settle stays aborted after the user lifts off.
    el.dispatchEvent(new Event("touchend"));
    expect(epoch.current).toBe(1);
    // A second, distinct gesture bumps again.
    el.dispatchEvent(new Event("touchstart"));
    expect(epoch.current).toBe(2);
  });

  it("bumps the epoch once per wheel BURST, not per tick", () => {
    const el = document.createElement("div");
    const { result } = renderHook(() => useActiveScrollGesture(el));
    const epoch = result.current.gestureEpochRef;
    el.dispatchEvent(new Event("wheel"));
    el.dispatchEvent(new Event("wheel"));
    el.dispatchEvent(new Event("wheel"));
    expect(epoch.current).toBe(1); // one burst = one epoch bump
    vi.advanceTimersByTime(150); // idle → release
    el.dispatchEvent(new Event("wheel")); // a new burst after idle
    expect(epoch.current).toBe(2);
  });

  it("is a no-op with a null container and detaches listeners on unmount", () => {
    const nullRender = renderHook(() => useActiveScrollGesture(null));
    expect(nullRender.result.current.activeGestureRef.current).toBe(false);

    const el = document.createElement("div");
    const removeSpy = vi.spyOn(el, "removeEventListener");
    const { unmount } = renderHook(() => useActiveScrollGesture(el));
    unmount();
    // touchstart/touchend/touchcancel/wheel all detached.
    expect(removeSpy).toHaveBeenCalledWith("touchstart", expect.any(Function));
    expect(removeSpy).toHaveBeenCalledWith("touchend", expect.any(Function));
    expect(removeSpy).toHaveBeenCalledWith("touchcancel", expect.any(Function));
    expect(removeSpy).toHaveBeenCalledWith("wheel", expect.any(Function));
  });
});
