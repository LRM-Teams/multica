// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { renderHook } from "@testing-library/react";
import type { VirtuosoHandle } from "react-virtuoso";
import type { ChannelMessage } from "@multica/core/types";
import { useBottomSettleScroll } from "./use-bottom-settle-scroll";

// The settle helper re-issues scrollToIndex across animation frames. Run rAF
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
  vi.restoreAllMocks();
});

// The hook only reads `.id`/length off messages; keep fixtures minimal.
function messages(ids: string[]): ChannelMessage[] {
  return ids.map((id) => ({ id }) as unknown as ChannelMessage);
}

/**
 * A scroll container whose distance-to-bottom the test controls. Fixed
 * scrollHeight/clientHeight; `scrollTop` is mutable. `distanceToBottom` =
 * scrollHeight - scrollTop - clientHeight. `perScrollDelta` is applied to
 * scrollTop each time the Virtuoso handle's `scrollToIndex` is invoked, so a
 * test can model "converges after N frames" or "never moves".
 */
function scrollerWithHandle(opts: {
  scrollHeight: number;
  clientHeight: number;
  perScrollDelta: number;
}) {
  const el = document.createElement("div");
  Object.defineProperty(el, "scrollHeight", { value: opts.scrollHeight, configurable: true });
  Object.defineProperty(el, "clientHeight", { value: opts.clientHeight, configurable: true });
  el.scrollTop = 0;
  const scrollToIndex = vi.fn((_loc: unknown) => {
    el.scrollTop = Math.min(el.scrollTop + opts.perScrollDelta, opts.scrollHeight);
  });
  const ref = { current: { scrollToIndex } as unknown as VirtuosoHandle };
  return { el, scrollToIndex, ref };
}

type BottomProps = Parameters<typeof useBottomSettleScroll>[0];
const baseProps = (
  over: Partial<BottomProps> & Pick<BottomProps, "virtuosoRef" | "scrollContainerEl">,
): BottomProps => ({
  channelId: "c1",
  messages: messages(["a", "b", "c"]),
  enabled: true,
  handleAttached: true,
  ...over,
});

describe("useBottomSettleScroll", () => {
  it("scrolls to the LAST local index with align:end and settles once at the bottom band", () => {
    const { el, scrollToIndex, ref } = scrollerWithHandle({
      scrollHeight: 1000,
      clientHeight: 500,
      perScrollDelta: 500, // one scroll lands exactly at the bottom
    });
    renderHook(() =>
      useBottomSettleScroll(baseProps({ virtuosoRef: ref, scrollContainerEl: el })),
    );
    // messages.length - 1 = 2 (local index), never offset by firstItemIndex.
    expect(scrollToIndex).toHaveBeenCalledWith({ index: 2, align: "end", behavior: "auto" });
    // Reached the bottom in a single frame → stops immediately.
    expect(scrollToIndex).toHaveBeenCalledTimes(1);
  });

  it("re-issues across frames until the bottom band is reached, not just once", () => {
    const { scrollToIndex, ref, el } = scrollerWithHandle({
      scrollHeight: 1000,
      clientHeight: 500,
      perScrollDelta: 100, // needs 5 frames to close the 500px gap
    });
    renderHook(() =>
      useBottomSettleScroll(baseProps({ virtuosoRef: ref, scrollContainerEl: el })),
    );
    expect(scrollToIndex).toHaveBeenCalledTimes(5);
  });

  it("does nothing when disabled (a highlight/anchor owns the mount)", () => {
    const { scrollToIndex, ref, el } = scrollerWithHandle({
      scrollHeight: 1000,
      clientHeight: 500,
      perScrollDelta: 500,
    });
    renderHook(() =>
      useBottomSettleScroll(
        baseProps({ enabled: false, virtuosoRef: ref, scrollContainerEl: el }),
      ),
    );
    expect(scrollToIndex).not.toHaveBeenCalled();
  });

  it("does nothing before the scroll container exists", () => {
    const { scrollToIndex, ref } = scrollerWithHandle({
      scrollHeight: 1000,
      clientHeight: 500,
      perScrollDelta: 500,
    });
    renderHook(() =>
      useBottomSettleScroll(baseProps({ virtuosoRef: ref, scrollContainerEl: null })),
    );
    expect(scrollToIndex).not.toHaveBeenCalled();
  });

  it("does nothing while the Virtuoso handle has not attached", () => {
    const { scrollToIndex, ref, el } = scrollerWithHandle({
      scrollHeight: 1000,
      clientHeight: 500,
      perScrollDelta: 500,
    });
    renderHook(() =>
      useBottomSettleScroll(
        baseProps({ handleAttached: false, virtuosoRef: ref, scrollContainerEl: el }),
      ),
    );
    expect(scrollToIndex).not.toHaveBeenCalled();
  });

  it("does nothing with no messages (empty conversation)", () => {
    const { scrollToIndex, ref, el } = scrollerWithHandle({
      scrollHeight: 1000,
      clientHeight: 500,
      perScrollDelta: 500,
    });
    renderHook(() =>
      useBottomSettleScroll(
        baseProps({ messages: [], virtuosoRef: ref, scrollContainerEl: el }),
      ),
    );
    expect(scrollToIndex).not.toHaveBeenCalled();
  });

  it("only settles once per channel visit (a guarded re-render does not re-scroll)", () => {
    const { scrollToIndex, ref, el } = scrollerWithHandle({
      scrollHeight: 1000,
      clientHeight: 500,
      perScrollDelta: 500,
    });
    const { rerender } = renderHook((props) => useBottomSettleScroll(props), {
      initialProps: baseProps({ virtuosoRef: ref, scrollContainerEl: el }),
    });
    expect(scrollToIndex).toHaveBeenCalledTimes(1);
    // Same channel, a benign re-render (e.g. messages refetch echo) — must not
    // re-anchor now that it already reached the bottom.
    rerender(baseProps({ messages: messages(["a", "b", "c"]), virtuosoRef: ref, scrollContainerEl: el }));
    expect(scrollToIndex).toHaveBeenCalledTimes(1);
  });

  it("gives up at the frame cap and logs when the bottom is never reached", () => {
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    const { scrollToIndex, ref, el } = scrollerWithHandle({
      scrollHeight: 1000,
      clientHeight: 500,
      perScrollDelta: 0, // scroll never moves → never reaches the band
    });
    renderHook(() =>
      useBottomSettleScroll(baseProps({ virtuosoRef: ref, scrollContainerEl: el })),
    );
    // Default settle cap is 180 frames.
    expect(scrollToIndex).toHaveBeenCalledTimes(180);
    expect(warn).toHaveBeenCalledWith(
      "[useBottomSettleScroll] settle timed out — never reached the bottom band",
      { channelId: "c1" },
    );
  });
});
