// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { renderHook } from "@testing-library/react";
import type { VirtuosoHandle } from "react-virtuoso";
import type { ChannelMessage } from "@multica/core/types";
import { scrollToIndexUntilSettled, useUnreadAnchorScroll } from "./use-unread-anchor-scroll";

// The #883 fix re-issues scrollToIndex across animation frames. Run rAF
// synchronously (bounded by the helper's own arrival check / frame cap) so the
// settle completes within the test without real timers.
let origRaf: typeof globalThis.requestAnimationFrame;
let origCaf: typeof globalThis.cancelAnimationFrame;
beforeEach(() => {
  origRaf = globalThis.requestAnimationFrame;
  origCaf = globalThis.cancelAnimationFrame;
  globalThis.requestAnimationFrame = ((cb: FrameRequestCallback) => {
    cb(0);
    return 1;
  }) as typeof globalThis.requestAnimationFrame;
  globalThis.cancelAnimationFrame = (() => {}) as typeof globalThis.cancelAnimationFrame;
});
afterEach(() => {
  globalThis.requestAnimationFrame = origRaf;
  globalThis.cancelAnimationFrame = origCaf;
});

function handleWithSpy() {
  const scrollToIndex = vi.fn();
  const ref = { current: { scrollToIndex } as unknown as VirtuosoHandle };
  return { scrollToIndex, ref };
}

// A real jsdom element (not a bare object stub) whose top edge sits `top` px
// from the viewport top — needs real addEventListener/removeEventListener
// since #689's gesture-tracking effect attaches touch/wheel listeners to
// scrollContainerEl.
function elAt(top: number): HTMLElement {
  const el = document.createElement("div");
  el.getBoundingClientRect = () => ({ top }) as DOMRect;
  return el;
}

// The hook only reads `.id`; keep fixtures minimal.
function messages(ids: string[]): ChannelMessage[] {
  return ids.map((id) => ({ id }) as unknown as ChannelMessage);
}

describe("scrollToIndexUntilSettled (#883 measurement-race guard)", () => {
  it("re-issues scrollToIndex across frames until the target arrives, not just once", () => {
    const { scrollToIndex, ref } = handleWithSpy();
    let frames = 0;
    // Arrives on the 4th check — models a far jump converging over several frames.
    const hasReached = () => ++frames >= 4;
    scrollToIndexUntilSettled(ref.current, hasReached, { index: 42, align: "start", behavior: "auto" });
    expect(scrollToIndex.mock.calls.length).toBe(4);
    expect(scrollToIndex).toHaveBeenLastCalledWith({ index: 42, align: "start", behavior: "auto" });
  });

  it("does NOT false-settle while the scroll has no effect — keeps re-issuing until the target arrives (Parker watchdog)", () => {
    const { scrollToIndex, ref } = handleWithSpy();
    // scrollToIndex is async: for the first N frames it has produced no visible
    // movement yet (a scrollTop-stability check would have wrongly stopped here at
    // the top). Arrival is only true once the row is actually rendered at the top.
    let checks = 0;
    const NO_EFFECT_FRAMES = 12;
    const hasReached = () => ++checks > NO_EFFECT_FRAMES;
    scrollToIndexUntilSettled(ref.current, hasReached, { index: 900, align: "start" }, { maxFrames: 40 });
    // Kept re-issuing through the whole no-effect stretch, then stopped on arrival.
    expect(scrollToIndex.mock.calls.length).toBe(NO_EFFECT_FRAMES + 1);
  });

  it("stops at the frame cap if the target never arrives", () => {
    const { scrollToIndex, ref } = handleWithSpy();
    scrollToIndexUntilSettled(ref.current, () => false, { index: 3, align: "start" }, { maxFrames: 5 });
    expect(scrollToIndex.mock.calls.length).toBe(5);
  });

  it("stops after a single scroll when the target is already at the top", () => {
    const { scrollToIndex, ref } = handleWithSpy();
    scrollToIndexUntilSettled(ref.current, () => true, { index: 3, align: "start" }, { maxFrames: 40 });
    expect(scrollToIndex.mock.calls.length).toBe(1);
  });

  it("is a no-op (no throw) with a null handle", () => {
    expect(() => scrollToIndexUntilSettled(null, () => true, { index: 0, align: "start" })).not.toThrow();
  });

  it("calls onSettleTimeout exactly once when the frame cap is hit without ever reaching (Parker fallback contract)", () => {
    const { ref } = handleWithSpy();
    const onSettleTimeout = vi.fn();
    scrollToIndexUntilSettled(ref.current, () => false, { index: 3, align: "start" }, {
      maxFrames: 5,
      onSettleTimeout,
    });
    expect(onSettleTimeout).toHaveBeenCalledTimes(1);
  });

  it("does NOT call onSettleTimeout when the target is reached before the cap", () => {
    const { ref } = handleWithSpy();
    const onSettleTimeout = vi.fn();
    let frames = 0;
    scrollToIndexUntilSettled(ref.current, () => ++frames >= 2, { index: 3, align: "start" }, {
      maxFrames: 40,
      onSettleTimeout,
    });
    expect(onSettleTimeout).not.toHaveBeenCalled();
  });

  // #689: real-device jank — scrolling *during* cold load fights the settle
  // loop's own re-issued scrollToIndex every frame. A live gesture must pause
  // the imperative scroll entirely rather than race the user's own input.
  it("skips scrollToIndex while a gesture is active, then resumes and completes once it ends", () => {
    const { scrollToIndex, ref } = handleWithSpy();
    // 15 skipped (gesture-active) frames, well past maxFrames, before the
    // gesture ends and the loop is allowed its first real scroll attempt.
    let ticks = 0;
    scrollToIndexUntilSettled(ref.current, () => true, { index: 3, align: "start" }, {
      maxFrames: 3,
      isGestureActive: () => ++ticks <= 15,
    });
    // Exactly one real scroll attempt — the 15 gesture-active ticks were
    // skipped, not spent, and hasReached is immediate once it actually runs.
    expect(scrollToIndex.mock.calls.length).toBe(1);
  });

  it("does not erode the settle budget while skipping gesture-active frames — timeout still needs maxFrames real attempts", () => {
    const { scrollToIndex, ref } = handleWithSpy();
    const onSettleTimeout = vi.fn();
    let ticks = 0;
    scrollToIndexUntilSettled(ref.current, () => false, { index: 3, align: "start" }, {
      maxFrames: 2,
      isGestureActive: () => ++ticks <= 20, // 20 skipped frames before gesture ends
      onSettleTimeout,
    });
    // Only maxFrames=2 real scroll attempts happened, not 20 — the gesture
    // window didn't count against the budget, but the real work still hits
    // the cap and reports timeout normally once it's genuinely exhausted.
    expect(scrollToIndex.mock.calls.length).toBe(2);
    expect(onSettleTimeout).toHaveBeenCalledTimes(1);
  });
});

// A scroll container + a rendered anchor row both pinned at the viewport top, so
// the hook's arrival check passes on the first frame (the scroll "lands").
function landedAt(anchorId: string) {
  return {
    scrollContainerEl: elAt(0),
    messageRefMap: new Map<string, HTMLElement>([[anchorId, elAt(0)]]),
  };
}

describe("useUnreadAnchorScroll", () => {
  it("scrolls the anchor to the top on cold-load entry", () => {
    const { scrollToIndex, ref } = handleWithSpy();
    const { result } = renderHook(() =>
      useUnreadAnchorScroll({
        channelId: "c1",
        messages: messages(["m1", "m2", "m3", "m4"]),
        newMessagesDivider: { anchorMessageId: "m3", count: 1 },
        highlightMessageId: null,
        handleAttached: true,
        virtuosoRef: ref,
        ...landedAt("m3"),
      }),
    );
    expect(result.current.unreadAnchorIndex).toBe(2);
    // #689/#1189: `scrollToIndex` resolves against the LOCAL data array
    // (0..messages.length-1), never offset by `firstItemIndex` — see
    // channel-message-list.tsx's matching comment for the upstream evidence.
    expect(scrollToIndex).toHaveBeenCalledWith({ index: 2, align: "start", behavior: "auto" });
  });

  it("scrolls only once per conversation visit (guarded re-renders don't re-anchor)", () => {
    const { scrollToIndex, ref } = handleWithSpy();
    const props = {
      channelId: "c1",
      messages: messages(["m1", "m2", "m3"]),
      highlightMessageId: null as string | null,
      handleAttached: true,
      virtuosoRef: ref,
      ...landedAt("m3"),
    };
    const { rerender } = renderHook((p) => useUnreadAnchorScroll(p), {
      initialProps: { ...props, newMessagesDivider: { anchorMessageId: "m3", count: 1 } },
    });
    const afterEntry = scrollToIndex.mock.calls.length;
    rerender({ ...props, newMessagesDivider: { anchorMessageId: "m3", count: 1 } });
    expect(scrollToIndex.mock.calls.length).toBe(afterEntry);
  });

  it("stands down while a deep-link highlight owns the viewport", () => {
    const { scrollToIndex, ref } = handleWithSpy();
    renderHook(() =>
      useUnreadAnchorScroll({
        channelId: "c1",
        messages: messages(["m1", "m2", "m3"]),
        newMessagesDivider: { anchorMessageId: "m3", count: 1 },
        highlightMessageId: "m2",
        handleAttached: true,
        virtuosoRef: ref,
        ...landedAt("m3"),
      }),
    );
    expect(scrollToIndex).not.toHaveBeenCalled();
  });

  it("cursor arrives late — anchor flips invalid→valid, must still scroll (permanent watchdog)", () => {
    const { scrollToIndex, ref } = handleWithSpy();
    const base = {
      channelId: "c1",
      messages: messages(["m1", "m2", "m3"]),
      highlightMessageId: null as string | null,
      handleAttached: true,
      virtuosoRef: ref,
      ...landedAt("m3"),
      newMessagesDivider: null as { anchorMessageId: string; count: number } | null,
    };
    const { rerender } = renderHook((p) => useUnreadAnchorScroll(p), {
      initialProps: { ...base },
    });
    expect(scrollToIndex).not.toHaveBeenCalled();
    // ~100ms later the cursor resolves → divider computes → anchor flips valid.
    rerender({ ...base, newMessagesDivider: { anchorMessageId: "m3", count: 1 } });
    expect(scrollToIndex).toHaveBeenCalledWith({ index: 2, align: "start", behavior: "auto" });
  });

  it("waits for the scroll container: no scroll until it exists", () => {
    const { scrollToIndex, ref } = handleWithSpy();
    const base = {
      channelId: "c1",
      messages: messages(["m1", "m2", "m3"]),
      newMessagesDivider: { anchorMessageId: "m3", count: 1 },
      highlightMessageId: null as string | null,
      handleAttached: true,
      virtuosoRef: ref,
      messageRefMap: new Map<string, HTMLElement>([["m3", elAt(0)]]),
      scrollContainerEl: null as HTMLElement | null,
    };
    const { rerender } = renderHook((p) => useUnreadAnchorScroll(p), {
      initialProps: { ...base },
    });
    expect(scrollToIndex).not.toHaveBeenCalled();
    rerender({ ...base, scrollContainerEl: elAt(0) });
    expect(scrollToIndex).toHaveBeenCalledWith({ index: 2, align: "start", behavior: "auto" });
  });

  it("REGRESSION (#348 H1): waits for the Virtuoso handle to attach — no scroll until handleAttached flips true", () => {
    // The actual root cause: scrollContainerEl alone was treated as the
    // "scroller ready" signal, but Virtuoso's imperative handle can still be
    // null at that exact instant (ref attachment doesn't trigger a re-render,
    // so nothing re-ran the effect once it later attached). Reproduced by
    // holding `handleAttached` at false while `scrollContainerEl` is already
    // truthy, then flipping it — the effect must fire ONLY once the handle is
    // actually attached, not fire-and-silently-no-op while it's still null.
    const { scrollToIndex, ref } = handleWithSpy();
    const base = {
      channelId: "c1",
      messages: messages(["m1", "m2", "m3"]),
      newMessagesDivider: { anchorMessageId: "m3", count: 1 },
      highlightMessageId: null as string | null,
      virtuosoRef: ref,
      scrollContainerEl: elAt(0), // ready — but the handle hasn't attached yet
      messageRefMap: new Map<string, HTMLElement>([["m3", elAt(0)]]),
      handleAttached: false,
    };
    const { rerender } = renderHook((p) => useUnreadAnchorScroll(p), {
      initialProps: { ...base },
    });
    expect(scrollToIndex).not.toHaveBeenCalled();
    rerender({ ...base, handleAttached: true });
    expect(scrollToIndex).toHaveBeenCalledWith({ index: 2, align: "start", behavior: "auto" });
  });

  it("REGRESSION (#348): an effect re-run mid-settle does not permanently block retries", () => {
    // The actual #348 bug: the settle effect marked the visit "done" the moment
    // it FIRST ran, before the async retry loop had a chance to succeed. If
    // React re-ran the effect (any dependency touched by a stray re-render)
    // before `hasReached` ever returned true, the cleanup cancelled the pending
    // frame and the re-run's guard check saw "already done" — permanently
    // stopping retries with the anchor row never having rendered. Reproduce by
    // manually controlling rAF (not auto-firing) so we can interleave a
    // React re-render between animation frames.
    const pending: FrameRequestCallback[] = [];
    globalThis.requestAnimationFrame = ((cb: FrameRequestCallback) => {
      pending.push(cb);
      return pending.length;
    }) as typeof globalThis.requestAnimationFrame;

    const { scrollToIndex, ref } = handleWithSpy();
    const messageRefMap = new Map<string, HTMLElement>(); // anchor not yet virtualized in
    const props = {
      channelId: "c1",
      messages: messages(["m1", "m2", "m3"]),
      newMessagesDivider: { anchorMessageId: "m3", count: 1 },
      highlightMessageId: null as string | null,
      handleAttached: true,
      virtuosoRef: ref,
      scrollContainerEl: elAt(0),
      messageRefMap,
    };

    const { rerender } = renderHook((p) => useUnreadAnchorScroll(p), { initialProps: props });
    // Frame 1 ran synchronously inside the effect (tick() fires immediately);
    // hasReached() returned false (anchor not in messageRefMap yet), so frame 2
    // is queued.
    expect(scrollToIndex.mock.calls.length).toBe(1);
    expect(pending.length).toBe(1);

    // A stray re-render happens before frame 2 fires (any real-world dependency
    // touch — the exact trigger doesn't matter, only that the effect re-runs
    // mid-settle). Vary `messageRefMap`'s identity to force the effect deps to
    // differ; the anchor is still not virtualized in.
    rerender({ ...props, messageRefMap: new Map(messageRefMap) });
    // React's cleanup cancelled the queued frame-2 callback; the fixed hook's
    // re-run must restart the settle loop (fresh tick()) rather than treat the
    // interrupted attempt as "done".
    expect(scrollToIndex.mock.calls.length).toBe(2);

    // Now the anchor row actually gets virtualized in — the settle loop's next
    // (most-recently-scheduled) frame should find and reach it. (The mocked
    // `cancelAnimationFrame` from `beforeEach` is a no-op, so the earlier
    // cancelled callback stays in `pending` uninvoked — that's a test-harness
    // artifact, not a real pending scroll; the call-count assertions above are
    // what prove retries aren't blocked.)
    messageRefMap.set("m3", elAt(0));
    const nextFrame = pending.pop();
    nextFrame?.(0);
    expect(scrollToIndex.mock.calls.length).toBe(3);
  });

  it("falls back to latest + logs when the anchor row never renders within the settle timeout", () => {
    const { scrollToIndex, ref } = handleWithSpy();
    const warnSpy = vi.spyOn(console, "warn").mockImplementation(() => {});
    renderHook(() =>
      useUnreadAnchorScroll({
        channelId: "c1",
        messages: messages(["m1", "m2", "m3", "m4", "m5"]),
        newMessagesDivider: { anchorMessageId: "m3", count: 1 },
        highlightMessageId: null,
        handleAttached: true,
        virtuosoRef: ref,
        scrollContainerEl: elAt(0),
        messageRefMap: new Map(), // anchor never gets virtualized in
      }),
    );
    // Anchor scroll attempts exhaust the cap, then the fallback fires: scroll to
    // the last message, and a diagnosable warning is logged (not a silent no-op).
    expect(scrollToIndex).toHaveBeenLastCalledWith({ index: 4, align: "start", behavior: "auto" });
    expect(warnSpy).toHaveBeenCalledTimes(1);
    warnSpy.mockRestore();
  });

  it("REGRESSION (#348 ownership): claims scroll ownership while settling, releases it once the anchor is reached", () => {
    // The actual bug this closes: Virtuoso's own `followOutput` ("stick to
    // bottom") defaults on before the real cold-load position is known, and
    // fights the settle loop's scrollToIndex every frame — `hasReached()`
    // never sees the anchor arrive because something else keeps scrolling
    // back to the bottom. `isAnchorSettling` is the signal the caller must
    // gate `followOutput` on; it must be true for the whole in-flight window
    // and flip back to false the instant settle resolves (reached or timed
    // out), or `followOutput` would stay gated off forever.
    const pending: FrameRequestCallback[] = [];
    globalThis.requestAnimationFrame = ((cb: FrameRequestCallback) => {
      pending.push(cb);
      return pending.length;
    }) as typeof globalThis.requestAnimationFrame;

    const { ref } = handleWithSpy();
    const messageRefMap = new Map<string, HTMLElement>(); // anchor not yet virtualized in
    const { result, rerender } = renderHook(() =>
      useUnreadAnchorScroll({
        channelId: "c1",
        messages: messages(["m1", "m2", "m3"]),
        newMessagesDivider: { anchorMessageId: "m3", count: 1 },
        highlightMessageId: null,
        handleAttached: true,
        virtuosoRef: ref,
        scrollContainerEl: elAt(0),
        messageRefMap,
      }),
    );
    // Frame 1 ran synchronously (no reach yet) — settle is in flight, ownership claimed.
    expect(result.current.isAnchorSettling).toBe(true);

    // Anchor row virtualizes in; the next queued frame finds it and reaches.
    messageRefMap.set("m3", elAt(0));
    const nextFrame = pending.pop();
    nextFrame?.(0);
    rerender();
    expect(result.current.isAnchorSettling).toBe(false);
  });

  it("REGRESSION (#348 ownership): releases scroll ownership on settle timeout, not just on success", () => {
    const warnSpy = vi.spyOn(console, "warn").mockImplementation(() => {});
    const { ref } = handleWithSpy();
    const { result } = renderHook(() =>
      useUnreadAnchorScroll({
        channelId: "c1",
        messages: messages(["m1", "m2", "m3", "m4", "m5"]),
        newMessagesDivider: { anchorMessageId: "m3", count: 1 },
        highlightMessageId: null,
        handleAttached: true,
        virtuosoRef: ref,
        scrollContainerEl: elAt(0),
        messageRefMap: new Map(), // anchor never gets virtualized in — forces timeout
      }),
    );
    expect(result.current.isAnchorSettling).toBe(false);
    warnSpy.mockRestore();
  });

  it("reports no anchor and never scrolls when there is no unread divider", () => {
    const { scrollToIndex, ref } = handleWithSpy();
    const { result } = renderHook(() =>
      useUnreadAnchorScroll({
        channelId: "c1",
        messages: messages(["m1"]),
        newMessagesDivider: null,
        highlightMessageId: null,
        handleAttached: true,
        virtuosoRef: ref,
        scrollContainerEl: elAt(0),
        messageRefMap: new Map<string, HTMLElement>(),
      }),
    );
    expect(result.current.unreadAnchorIndex).toBe(-1);
    expect(scrollToIndex).not.toHaveBeenCalled();
  });

  // #689: the hook must wire real touch/wheel gesture state into the settle
  // loop's isGestureActive check — verified here at the listener-attachment
  // level (the yield/resume mechanics themselves are covered directly above
  // against scrollToIndexUntilSettled).
  it("#689: attaches touch/wheel gesture listeners to the scroll container and detaches them on unmount", () => {
    const { ref } = handleWithSpy();
    const container = elAt(0);
    const addSpy = vi.spyOn(container, "addEventListener");
    const removeSpy = vi.spyOn(container, "removeEventListener");
    const { unmount } = renderHook(() =>
      useUnreadAnchorScroll({
        channelId: "c1",
        messages: messages(["m1", "m2", "m3"]),
        newMessagesDivider: { anchorMessageId: "m3", count: 1 },
        highlightMessageId: null,
        handleAttached: true,
        virtuosoRef: ref,
        scrollContainerEl: container,
        messageRefMap: new Map([["m3", elAt(0)]]),
      }),
    );
    const attachedTypes = addSpy.mock.calls.map((call) => call[0]);
    expect(attachedTypes).toEqual(
      expect.arrayContaining(["touchstart", "touchend", "touchcancel", "wheel"]),
    );
    unmount();
    const detachedTypes = removeSpy.mock.calls.map((call) => call[0]);
    expect(detachedTypes).toEqual(
      expect.arrayContaining(["touchstart", "touchend", "touchcancel", "wheel"]),
    );
  });
});
