// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { renderHook } from "@testing-library/react";
import type { VirtuosoHandle } from "react-virtuoso";
import type { ChannelMessage } from "@multica/core/types";
import { useBottomSettleScroll } from "./use-bottom-settle-scroll";

// Manual frame pump (NOT recursive-synchronous rAF): the settle can skip frames
// indefinitely while a gesture is active, which a self-calling cb(0) rAF would
// turn into an infinite loop. Queue callbacks and flush a bounded number.
let nextId: number;
let scheduled: Array<{ id: number; cb: FrameRequestCallback }>;
let cancelled: Set<number>;
let origRaf: typeof globalThis.requestAnimationFrame;
let origCaf: typeof globalThis.cancelAnimationFrame;
beforeEach(() => {
  nextId = 1;
  scheduled = [];
  cancelled = new Set();
  origRaf = globalThis.requestAnimationFrame;
  origCaf = globalThis.cancelAnimationFrame;
  globalThis.requestAnimationFrame = ((cb: FrameRequestCallback) => {
    const id = nextId++;
    scheduled.push({ id, cb });
    return id;
  }) as typeof globalThis.requestAnimationFrame;
  globalThis.cancelAnimationFrame = ((id: number) => {
    cancelled.add(id);
  }) as typeof globalThis.cancelAnimationFrame;
});
afterEach(() => {
  globalThis.requestAnimationFrame = origRaf;
  globalThis.cancelAnimationFrame = origCaf;
  vi.restoreAllMocks();
});

function flushFrames(max = 400) {
  let n = 0;
  while (scheduled.length && n < max) {
    const next = scheduled.shift();
    if (!next) break;
    if (cancelled.has(next.id)) {
      cancelled.delete(next.id);
      continue;
    }
    n += 1;
    next.cb(0);
  }
}

function messages(ids: string[]): ChannelMessage[] {
  return ids.map((id) => ({ id }) as unknown as ChannelMessage);
}
const IDS = ["a", "b", "c"];
const LAST_ID = "c";

const CONTAINER_BOTTOM = 616;

/**
 * Models a cold Virtuoso mount HONESTLY, in the axis the hook actually reads:
 * the LAST ROW's real geometry (getBoundingClientRect().bottom) vs the
 * container's bottom edge. On the failing #1204 open the last row was rendered
 * but 263px BELOW the fold (rowBottom = 879, containerBottom = 616), while the
 * scroller's `scrollHeight` metric transiently read === clientHeight (the trap
 * that false-settled the old metric-based predicate). This harness exposes BOTH:
 * the trap metric AND the true row geometry, so a revert to the metric predicate
 * is caught.
 */
function coldMountHarness(opts: {
  rowBottomStart: number; // last row's bottom edge in viewport coords at scrollTop 0
  perScrollDelta: number; // px the row rises toward the fold per honored scroll
  rendersAfterAttempts?: number; // when the last row enters the ref map (default 1 = present)
  trapScrollHeightEqualsClient?: boolean; // expose the misleading metric
}) {
  const el = document.createElement("div");
  el.getBoundingClientRect = () =>
    ({ top: 0, bottom: CONTAINER_BOTTOM }) as DOMRect;
  // The trap: cold-mount scroller reports scrollHeight === clientHeight before
  // Virtuoso updates its size model. A metric predicate would read
  // distanceToBottom = 0 here and false-settle; the geometry predicate ignores it.
  Object.defineProperty(el, "clientHeight", { value: CONTAINER_BOTTOM, configurable: true });
  Object.defineProperty(el, "scrollHeight", {
    get: () => (opts.trapScrollHeightEqualsClient ? CONTAINER_BOTTOM : 10_000),
    configurable: true,
  });
  el.scrollTop = 0;

  const map = new Map<string, HTMLElement>();
  let rowBottom = opts.rowBottomStart;
  const lastRowEl = document.createElement("div");
  lastRowEl.getBoundingClientRect = () =>
    ({ top: rowBottom - 40, bottom: rowBottom }) as DOMRect;
  const rendersAfter = opts.rendersAfterAttempts ?? 1;
  if (rendersAfter <= 0) map.set(LAST_ID, lastRowEl);

  let attempts = 0;
  const scrollToIndex = vi.fn((_loc: unknown) => {
    attempts += 1;
    if (!map.has(LAST_ID) && attempts >= rendersAfter) map.set(LAST_ID, lastRowEl);
    // A honored scroll brings the last row up toward (and no further than) the
    // container's bottom edge.
    rowBottom = Math.max(CONTAINER_BOTTOM, rowBottom - opts.perScrollDelta);
  });
  const ref = { current: { scrollToIndex } as unknown as VirtuosoHandle };
  return { el, map, scrollToIndex, ref };
}

type BottomProps = Parameters<typeof useBottomSettleScroll>[0];
const baseProps = (
  over: Partial<BottomProps> &
    Pick<BottomProps, "virtuosoRef" | "scrollContainerEl" | "messageRefMap">,
): BottomProps => ({
  channelId: "c1",
  messages: messages(IDS),
  enabled: true,
  handleAttached: true,
  ...over,
});

describe("useBottomSettleScroll", () => {
  it("scrolls to the LAST local index with align:end", () => {
    const h = coldMountHarness({ rowBottomStart: CONTAINER_BOTTOM, perScrollDelta: 0 });
    renderHook(() =>
      useBottomSettleScroll(baseProps({ virtuosoRef: h.ref, scrollContainerEl: h.el, messageRefMap: h.map })),
    );
    flushFrames();
    // messages.length - 1 = 2 (local index), never offset by firstItemIndex.
    expect(h.scrollToIndex).toHaveBeenCalledWith({ index: 2, align: "end", behavior: "auto" });
  });

  it("does NOT false-settle when the scrollHeight metric reads 'at bottom' but the last row is below the fold (the #1204 runtime bug)", () => {
    // Trap metric ON (scrollHeight === clientHeight → distanceToBottom 0) AND the
    // last row is rendered but 263px below the fold. The old metric predicate
    // would settle on frame 1 (a single scroll). The geometry predicate must keep
    // re-issuing until the row's bottom actually reaches the container bottom.
    const h = coldMountHarness({
      rowBottomStart: CONTAINER_BOTTOM + 263,
      perScrollDelta: 90, // the row rises gradually; several frames to land
      trapScrollHeightEqualsClient: true,
    });
    renderHook(() =>
      useBottomSettleScroll(baseProps({ virtuosoRef: h.ref, scrollContainerEl: h.el, messageRefMap: h.map })),
    );
    flushFrames();
    // The geometry predicate keeps re-issuing while the row is still below the
    // fold, landing only after the row's bottom reaches the container bottom.
    // A metric-only predicate would false-settle on frame 1 (trap metric = 0)
    // and produce exactly 1 call — so > 1 is the regression that catches a revert.
    expect(h.scrollToIndex.mock.calls.length).toBeGreaterThan(1);
  });

  it("re-issues across frames until the last row's bottom reaches the container bottom", () => {
    const h = coldMountHarness({
      rowBottomStart: CONTAINER_BOTTOM + 500,
      perScrollDelta: 100, // 5 frames to close the 500px gap
    });
    renderHook(() =>
      useBottomSettleScroll(baseProps({ virtuosoRef: h.ref, scrollContainerEl: h.el, messageRefMap: h.map })),
    );
    flushFrames();
    expect(h.scrollToIndex).toHaveBeenCalledTimes(5);
  });

  it("settles immediately for a short list whose last row is already above the fold", () => {
    // Short conversation: last row bottom sits ABOVE the container bottom (content
    // doesn't fill the viewport) — already at the bottom, one scroll confirms it.
    const h = coldMountHarness({ rowBottomStart: CONTAINER_BOTTOM - 100, perScrollDelta: 0 });
    renderHook(() =>
      useBottomSettleScroll(baseProps({ virtuosoRef: h.ref, scrollContainerEl: h.el, messageRefMap: h.map })),
    );
    flushFrames();
    expect(h.scrollToIndex).toHaveBeenCalledTimes(1);
  });

  it("does not trust the metric until the last row has rendered", () => {
    // Last row not in the ref map until attempt 3; geometry can't be read before
    // then, so it must keep re-issuing rather than settle on the trap metric.
    const h = coldMountHarness({
      rowBottomStart: CONTAINER_BOTTOM, // once rendered, already at bottom
      perScrollDelta: 0,
      rendersAfterAttempts: 3,
      trapScrollHeightEqualsClient: true,
    });
    renderHook(() =>
      useBottomSettleScroll(baseProps({ virtuosoRef: h.ref, scrollContainerEl: h.el, messageRefMap: h.map })),
    );
    flushFrames();
    expect(h.scrollToIndex).toHaveBeenCalledTimes(3);
  });

  it("yields to an active touch gesture (no scrollToIndex while held), then resumes on release", () => {
    const h = coldMountHarness({ rowBottomStart: CONTAINER_BOTTOM + 5000, perScrollDelta: 10 });
    renderHook(() =>
      useBottomSettleScroll(baseProps({ virtuosoRef: h.ref, scrollContainerEl: h.el, messageRefMap: h.map })),
    );
    const beforeGesture = h.scrollToIndex.mock.calls.length;
    expect(beforeGesture).toBeGreaterThanOrEqual(1);
    h.el.dispatchEvent(new Event("touchstart"));
    flushFrames(30);
    expect(h.scrollToIndex.mock.calls.length).toBe(beforeGesture);
    h.el.dispatchEvent(new Event("touchend"));
    flushFrames(30);
    expect(h.scrollToIndex.mock.calls.length).toBeGreaterThan(beforeGesture);
  });

  it("does nothing when disabled (a highlight/anchor owns the mount)", () => {
    const h = coldMountHarness({ rowBottomStart: CONTAINER_BOTTOM, perScrollDelta: 0 });
    renderHook(() =>
      useBottomSettleScroll(baseProps({ enabled: false, virtuosoRef: h.ref, scrollContainerEl: h.el, messageRefMap: h.map })),
    );
    flushFrames();
    expect(h.scrollToIndex).not.toHaveBeenCalled();
  });

  it("does nothing before the scroll container exists", () => {
    const h = coldMountHarness({ rowBottomStart: CONTAINER_BOTTOM, perScrollDelta: 0 });
    renderHook(() =>
      useBottomSettleScroll(baseProps({ virtuosoRef: h.ref, scrollContainerEl: null, messageRefMap: h.map })),
    );
    flushFrames();
    expect(h.scrollToIndex).not.toHaveBeenCalled();
  });

  it("does nothing while the Virtuoso handle has not attached", () => {
    const h = coldMountHarness({ rowBottomStart: CONTAINER_BOTTOM, perScrollDelta: 0 });
    renderHook(() =>
      useBottomSettleScroll(baseProps({ handleAttached: false, virtuosoRef: h.ref, scrollContainerEl: h.el, messageRefMap: h.map })),
    );
    flushFrames();
    expect(h.scrollToIndex).not.toHaveBeenCalled();
  });

  it("does nothing with no messages (empty conversation)", () => {
    const h = coldMountHarness({ rowBottomStart: CONTAINER_BOTTOM, perScrollDelta: 0 });
    renderHook(() =>
      useBottomSettleScroll(baseProps({ messages: [], virtuosoRef: h.ref, scrollContainerEl: h.el, messageRefMap: h.map })),
    );
    flushFrames();
    expect(h.scrollToIndex).not.toHaveBeenCalled();
  });

  it("only settles once per channel visit (a guarded re-render does not re-scroll)", () => {
    const h = coldMountHarness({ rowBottomStart: CONTAINER_BOTTOM, perScrollDelta: 0 });
    const { rerender } = renderHook((props: BottomProps) => useBottomSettleScroll(props), {
      initialProps: baseProps({ virtuosoRef: h.ref, scrollContainerEl: h.el, messageRefMap: h.map }),
    });
    flushFrames();
    const afterFirst = h.scrollToIndex.mock.calls.length;
    expect(afterFirst).toBeGreaterThanOrEqual(1);
    rerender(baseProps({ messages: messages(IDS), virtuosoRef: h.ref, scrollContainerEl: h.el, messageRefMap: h.map }));
    flushFrames();
    expect(h.scrollToIndex.mock.calls.length).toBe(afterFirst);
  });

  it("gives up at the frame cap and logs when the bottom is never reached", () => {
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    // Row stays far below the fold forever (scroll never honored).
    const h = coldMountHarness({ rowBottomStart: CONTAINER_BOTTOM + 500, perScrollDelta: 0 });
    renderHook(() =>
      useBottomSettleScroll(baseProps({ virtuosoRef: h.ref, scrollContainerEl: h.el, messageRefMap: h.map })),
    );
    flushFrames();
    expect(h.scrollToIndex).toHaveBeenCalledTimes(180);
    expect(warn).toHaveBeenCalledWith(
      "[useBottomSettleScroll] settle timed out — never reached the bottom band",
      { channelId: "c1" },
    );
  });
});
