// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { renderHook } from "@testing-library/react";
import type { VirtuosoHandle } from "react-virtuoso";
import type { ChannelMessage } from "@multica/core/types";
import { scrollToIndexUntilSettled, useUnreadAnchorScroll } from "./use-unread-anchor-scroll";

// The #883 fix re-issues scrollToIndex across animation frames. Run rAF
// synchronously (bounded by the helper's own frame counter) so the settle
// completes within the test without real timers.
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

// The hook only reads `.id`; keep fixtures minimal.
function messages(ids: string[]): ChannelMessage[] {
  return ids.map((id) => ({ id }) as unknown as ChannelMessage);
}

describe("scrollToIndexUntilSettled (#883 measurement-race guard)", () => {
  it("re-issues scrollToIndex across frames, not just once", () => {
    const { scrollToIndex, ref } = handleWithSpy();
    scrollToIndexUntilSettled(ref.current, { index: 42, align: "start", behavior: "auto" }, 6);
    // Pre-#883-fix code fired a single scrollToIndex (landed before measurement);
    // the fix re-pins it across frames. A regression back to one call fails here.
    expect(scrollToIndex.mock.calls.length).toBeGreaterThan(1);
    expect(scrollToIndex).toHaveBeenLastCalledWith({ index: 42, align: "start", behavior: "auto" });
  });

  it("is a no-op (no throw) with a null handle", () => {
    expect(() => scrollToIndexUntilSettled(null, { index: 0, align: "start" })).not.toThrow();
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
        scrollerReady: true,
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
      scrollerReady: true,
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
        scrollerReady: true,
      }),
    );
    expect(scrollToIndex).not.toHaveBeenCalled();
  });

  it("waits for the scroll container: no scroll until scrollerReady flips true", () => {
    const { scrollToIndex, ref } = handleWithSpy();
    const base = {
      channelId: "c1",
      messages: messages(["m1", "m2", "m3"]),
      newMessagesDivider: { anchorMessageId: "m3", count: 1 },
      highlightMessageId: null as string | null,
      firstItemIndex: 0,
      virtuosoRef: ref,
    };
    const { rerender } = renderHook((p) => useUnreadAnchorScroll(p), {
      initialProps: { ...base, scrollerReady: false },
    });
    // First render is the bare placeholder scroller — Virtuoso isn't mounted yet.
    expect(scrollToIndex).not.toHaveBeenCalled();
    // Container captured → Virtuoso mounts → the anchor scroll fires (late arrival).
    rerender({ ...base, scrollerReady: true });
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
        scrollerReady: true,
      }),
    );
    expect(result.current.unreadAnchorIndex).toBe(-1);
    expect(scrollToIndex).not.toHaveBeenCalled();
  });
});
