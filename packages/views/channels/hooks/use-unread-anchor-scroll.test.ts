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

// A DOM element whose top edge sits `top` px from the viewport top.
function elAt(top: number): HTMLElement {
  return { getBoundingClientRect: () => ({ top }) } as unknown as HTMLElement;
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
    scrollToIndexUntilSettled(ref.current, hasReached, { index: 900, align: "start" }, 40);
    // Kept re-issuing through the whole no-effect stretch, then stopped on arrival.
    expect(scrollToIndex.mock.calls.length).toBe(NO_EFFECT_FRAMES + 1);
  });

  it("stops at the frame cap if the target never arrives", () => {
    const { scrollToIndex, ref } = handleWithSpy();
    scrollToIndexUntilSettled(ref.current, () => false, { index: 3, align: "start" }, 5);
    expect(scrollToIndex.mock.calls.length).toBe(5);
  });

  it("stops after a single scroll when the target is already at the top", () => {
    const { scrollToIndex, ref } = handleWithSpy();
    scrollToIndexUntilSettled(ref.current, () => true, { index: 3, align: "start" }, 40);
    expect(scrollToIndex.mock.calls.length).toBe(1);
  });

  it("is a no-op (no throw) with a null handle", () => {
    expect(() => scrollToIndexUntilSettled(null, () => true, { index: 0, align: "start" })).not.toThrow();
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
        firstItemIndex: 100,
        virtuosoRef: ref,
        ...landedAt("m3"),
      }),
    );
    expect(result.current.unreadAnchorIndex).toBe(2);
    expect(scrollToIndex).toHaveBeenCalledWith({ index: 102, align: "start", behavior: "auto" });
  });

  it("scrolls only once per conversation visit (guarded re-renders don't re-anchor)", () => {
    const { scrollToIndex, ref } = handleWithSpy();
    const props = {
      channelId: "c1",
      messages: messages(["m1", "m2", "m3"]),
      highlightMessageId: null as string | null,
      firstItemIndex: 0,
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
        firstItemIndex: 0,
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
      firstItemIndex: 0,
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
      firstItemIndex: 0,
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
        scrollContainerEl: elAt(0),
        messageRefMap: new Map<string, HTMLElement>(),
      }),
    );
    expect(result.current.unreadAnchorIndex).toBe(-1);
    expect(scrollToIndex).not.toHaveBeenCalled();
  });
});
