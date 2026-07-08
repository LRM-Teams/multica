// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { renderHook } from "@testing-library/react";
import type { VirtuosoHandle } from "react-virtuoso";
import type { ChannelMessage } from "@multica/core/types";
import { scrollToIndexUntilSettled, useUnreadAnchorScroll } from "./use-unread-anchor-scroll";

// The #883 fix re-issues scrollToIndex across animation frames. Run rAF
// synchronously (bounded by the helper's own frame counter / convergence) so the
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

// A scroll container whose `scrollTop` returns each value in `tops` on successive
// reads (last value sticks) — models Virtuoso's landing offset converging as
// off-screen item heights get measured over successive frames.
function scrollerReading(tops: number[]): HTMLElement {
  let i = 0;
  return {
    get scrollTop() {
      const v = tops[Math.min(i, tops.length - 1)];
      i += 1;
      return v;
    },
  } as unknown as HTMLElement;
}

// A ready container that never moves (converges immediately) — for the hook tests
// that only care that the anchor scroll fired.
function settledScroller(): HTMLElement {
  return { scrollTop: 0 } as unknown as HTMLElement;
}

// The hook only reads `.id`; keep fixtures minimal.
function messages(ids: string[]): ChannelMessage[] {
  return ids.map((id) => ({ id }) as unknown as ChannelMessage);
}

describe("scrollToIndexUntilSettled (#883 measurement-race guard)", () => {
  it("re-issues scrollToIndex across frames, not just once", () => {
    const { scrollToIndex, ref } = handleWithSpy();
    scrollToIndexUntilSettled(ref.current, settledScroller(), {
      index: 42,
      align: "start",
      behavior: "auto",
    });
    // Pre-#883-fix code fired a single scrollToIndex (landed before measurement);
    // the fix re-pins it across frames. A regression back to one call fails here.
    expect(scrollToIndex.mock.calls.length).toBeGreaterThan(1);
    expect(scrollToIndex).toHaveBeenLastCalledWith({ index: 42, align: "start", behavior: "auto" });
  });

  it("keeps re-issuing while the landing is still moving, then stops once it settles", () => {
    const { scrollToIndex, ref } = handleWithSpy();
    // A big-list far jump: the landing offset drifts for several frames as heights
    // get measured, then holds at 9000 — the exact case a fixed 6-frame settle
    // couldn't reach (Iris's 14k-px DM).
    const scroller = scrollerReading([1200, 4200, 7000, 9000, 9000, 9000, 9000]);
    scrollToIndexUntilSettled(ref.current, scroller, { index: 500, align: "start" }, 40);
    // Re-issued through the whole moving phase (would have stopped at 6 pre-fix)...
    expect(scrollToIndex.mock.calls.length).toBeGreaterThan(4);
    // ...but stopped once converged, well short of the cap.
    expect(scrollToIndex.mock.calls.length).toBeLessThan(40);
    expect(scrollToIndex).toHaveBeenLastCalledWith({ index: 500, align: "start" });
  });

  it("stops at the frame cap if the landing never settles", () => {
    const { scrollToIndex, ref } = handleWithSpy();
    // scrollTop changes every frame forever → never converges → cap backstops.
    let n = 0;
    const forever = {
      get scrollTop() {
        n += 100;
        return n;
      },
    } as unknown as HTMLElement;
    scrollToIndexUntilSettled(ref.current, forever, { index: 3, align: "start" }, 5);
    expect(scrollToIndex.mock.calls.length).toBe(5);
  });

  it("is a no-op (no throw) with a null handle", () => {
    expect(() =>
      scrollToIndexUntilSettled(null, settledScroller(), { index: 0, align: "start" }),
    ).not.toThrow();
  });
});

describe("useUnreadAnchorScroll", () => {
  it("settles the viewport at firstItemIndex + anchor index on cold-load entry", () => {
    const { scrollToIndex, ref } = handleWithSpy();
    const { result } = renderHook(() =>
      useUnreadAnchorScroll({
        channelId: "c1",
        messages: messages(["m1", "m2", "m3", "m4"]),
        newMessagesDivider: { anchorMessageId: "m3", count: 1 },
        highlightMessageId: null,
        firstItemIndex: 100,
        virtuosoRef: ref,
        scrollContainerEl: settledScroller(),
      }),
    );
    expect(result.current.unreadAnchorIndex).toBe(2);
    expect(scrollToIndex).toHaveBeenCalled();
    expect(scrollToIndex).toHaveBeenLastCalledWith({ index: 102, align: "start", behavior: "auto" });
  });

  it("scrolls only once per conversation visit (guarded re-renders don't re-anchor)", () => {
    const { scrollToIndex, ref } = handleWithSpy();
    const props = {
      channelId: "c1",
      messages: messages(["m1", "m2", "m3"]),
      highlightMessageId: null as string | null,
      firstItemIndex: 0,
      virtuosoRef: ref,
      scrollContainerEl: settledScroller(),
    };
    const { rerender } = renderHook((p) => useUnreadAnchorScroll(p), {
      initialProps: { ...props, newMessagesDivider: { anchorMessageId: "m3", count: 1 } },
    });
    const afterEntry = scrollToIndex.mock.calls.length;
    // A fresh divider object on the same channel re-runs the effect but must not re-scroll.
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
        firstItemIndex: 0,
        virtuosoRef: ref,
        scrollContainerEl: settledScroller(),
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
      firstItemIndex: 0,
      virtuosoRef: ref,
      scrollContainerEl: settledScroller(),
      newMessagesDivider: null as { anchorMessageId: string; count: number } | null,
    };
    // Cold-load: scroller ready, but the read cursor hasn't arrived → no divider
    // yet → anchor index is -1.
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
      firstItemIndex: 0,
      virtuosoRef: ref,
      scrollContainerEl: null as HTMLElement | null,
    };
    const { rerender } = renderHook((p) => useUnreadAnchorScroll(p), {
      initialProps: { ...base },
    });
    // First render is the bare placeholder scroller — Virtuoso isn't mounted yet.
    expect(scrollToIndex).not.toHaveBeenCalled();
    // Container captured → Virtuoso mounts → the anchor scroll fires (late arrival).
    rerender({ ...base, scrollContainerEl: settledScroller() });
    expect(scrollToIndex).toHaveBeenCalledWith({ index: 2, align: "start", behavior: "auto" });
  });

  it("reports no anchor and never scrolls when there is no unread divider", () => {
    const { scrollToIndex, ref } = handleWithSpy();
    const { result } = renderHook(() =>
      useUnreadAnchorScroll({
        channelId: "c1",
        messages: messages(["m1"]),
        newMessagesDivider: null,
        highlightMessageId: null,
        firstItemIndex: 0,
        virtuosoRef: ref,
        scrollContainerEl: settledScroller(),
      }),
    );
    expect(result.current.unreadAnchorIndex).toBe(-1);
    expect(scrollToIndex).not.toHaveBeenCalled();
  });
});
