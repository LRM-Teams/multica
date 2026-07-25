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

/**
 * Models a cold Virtuoso mount honestly: until the last row renders (after
 * `rendersAfterAttempts` scroll attempts) the scroller reports
 * scrollHeight === clientHeight (distanceToBottom = 0 — the false-"at bottom"
 * trap) AND the last row is absent from the ref map. Only once rendered does the
 * height grow to its real value and scrollTop advance toward the bottom.
 */
function coldMountHarness(opts: {
  clientHeight: number;
  heightBefore: number;
  heightAfter: number;
  rendersAfterAttempts: number;
  perScrollDelta: number;
}) {
  const el = document.createElement("div");
  const map = new Map<string, HTMLElement>();
  let attempts = 0;
  let rendered = false;
  let height = opts.heightBefore;
  Object.defineProperty(el, "clientHeight", { get: () => opts.clientHeight });
  Object.defineProperty(el, "scrollHeight", { get: () => height });
  el.scrollTop = 0;
  const scrollToIndex = vi.fn((_loc: unknown) => {
    attempts += 1;
    if (!rendered && attempts >= opts.rendersAfterAttempts) {
      rendered = true;
      height = opts.heightAfter;
      map.set(LAST_ID, document.createElement("div"));
    }
    if (rendered) {
      el.scrollTop = Math.min(
        el.scrollTop + opts.perScrollDelta,
        height - opts.clientHeight,
      );
    }
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
    const h = coldMountHarness({
      clientHeight: 500,
      heightBefore: 1000,
      heightAfter: 1000,
      rendersAfterAttempts: 1, // last row present from the first attempt
      perScrollDelta: 500,
    });
    renderHook(() =>
      useBottomSettleScroll(
        baseProps({ virtuosoRef: h.ref, scrollContainerEl: h.el, messageRefMap: h.map }),
      ),
    );
    flushFrames();
    // messages.length - 1 = 2 (local index), never offset by firstItemIndex.
    expect(h.scrollToIndex).toHaveBeenCalledWith({ index: 2, align: "end", behavior: "auto" });
  });

  it("does NOT false-settle while the last row is unrendered even when the scroller reports distanceToBottom=0 (measurement race)", () => {
    // The trap: heightBefore === clientHeight → distanceToBottom = 0 before any
    // real layout. A metric-only predicate would settle on frame 1 and never
    // retry. The last row only renders on attempt 3, then one more scroll lands
    // the bottom → the loop must keep going until then.
    const h = coldMountHarness({
      clientHeight: 500,
      heightBefore: 500, // == clientHeight → distanceToBottom 0 (the false positive)
      heightAfter: 1000,
      rendersAfterAttempts: 3,
      perScrollDelta: 500, // once rendered, one scroll reaches the bottom
    });
    renderHook(() =>
      useBottomSettleScroll(
        baseProps({ virtuosoRef: h.ref, scrollContainerEl: h.el, messageRefMap: h.map }),
      ),
    );
    flushFrames();
    // Kept re-issuing through the unrendered frames (would be 1 under the buggy
    // metric-only predicate); settles only once the last row is actually in the
    // DOM and the scroll has landed.
    expect(h.scrollToIndex).toHaveBeenCalledTimes(3);
  });

  it("re-issues across frames until the bottom band is reached", () => {
    const h = coldMountHarness({
      clientHeight: 500,
      heightBefore: 1000, // last row rendered from the start (map set on attempt 1)
      heightAfter: 1000,
      rendersAfterAttempts: 1,
      perScrollDelta: 100, // needs 5 frames to close the 500px gap
    });
    renderHook(() =>
      useBottomSettleScroll(
        baseProps({ virtuosoRef: h.ref, scrollContainerEl: h.el, messageRefMap: h.map }),
      ),
    );
    flushFrames();
    expect(h.scrollToIndex).toHaveBeenCalledTimes(5);
  });

  it("yields to an active touch gesture (no scrollToIndex while held), then resumes on release", () => {
    // Never reaches the bottom on its own so we can observe the pause/resume.
    const h = coldMountHarness({
      clientHeight: 500,
      heightBefore: 1000,
      heightAfter: 1000,
      rendersAfterAttempts: 1,
      perScrollDelta: 10, // creeps, never settles within the frames we pump
    });
    renderHook(() =>
      useBottomSettleScroll(
        baseProps({ virtuosoRef: h.ref, scrollContainerEl: h.el, messageRefMap: h.map }),
      ),
    );
    // First tick ran during mount (gesture inactive) → one scroll issued.
    const beforeGesture = h.scrollToIndex.mock.calls.length;
    expect(beforeGesture).toBeGreaterThanOrEqual(1);

    // User grabs the scroller mid-settle.
    h.el.dispatchEvent(new Event("touchstart"));
    flushFrames(30);
    // No new imperative scrolls landed on top of the user's gesture.
    expect(h.scrollToIndex.mock.calls.length).toBe(beforeGesture);

    // User lets go → settle resumes.
    h.el.dispatchEvent(new Event("touchend"));
    flushFrames(30);
    expect(h.scrollToIndex.mock.calls.length).toBeGreaterThan(beforeGesture);
  });

  it("does nothing when disabled (a highlight/anchor owns the mount)", () => {
    const h = coldMountHarness({
      clientHeight: 500,
      heightBefore: 1000,
      heightAfter: 1000,
      rendersAfterAttempts: 1,
      perScrollDelta: 500,
    });
    renderHook(() =>
      useBottomSettleScroll(
        baseProps({
          enabled: false,
          virtuosoRef: h.ref,
          scrollContainerEl: h.el,
          messageRefMap: h.map,
        }),
      ),
    );
    flushFrames();
    expect(h.scrollToIndex).not.toHaveBeenCalled();
  });

  it("does nothing before the scroll container exists", () => {
    const h = coldMountHarness({
      clientHeight: 500,
      heightBefore: 1000,
      heightAfter: 1000,
      rendersAfterAttempts: 1,
      perScrollDelta: 500,
    });
    renderHook(() =>
      useBottomSettleScroll(
        baseProps({ virtuosoRef: h.ref, scrollContainerEl: null, messageRefMap: h.map }),
      ),
    );
    flushFrames();
    expect(h.scrollToIndex).not.toHaveBeenCalled();
  });

  it("does nothing while the Virtuoso handle has not attached", () => {
    const h = coldMountHarness({
      clientHeight: 500,
      heightBefore: 1000,
      heightAfter: 1000,
      rendersAfterAttempts: 1,
      perScrollDelta: 500,
    });
    renderHook(() =>
      useBottomSettleScroll(
        baseProps({
          handleAttached: false,
          virtuosoRef: h.ref,
          scrollContainerEl: h.el,
          messageRefMap: h.map,
        }),
      ),
    );
    flushFrames();
    expect(h.scrollToIndex).not.toHaveBeenCalled();
  });

  it("does nothing with no messages (empty conversation)", () => {
    const h = coldMountHarness({
      clientHeight: 500,
      heightBefore: 1000,
      heightAfter: 1000,
      rendersAfterAttempts: 1,
      perScrollDelta: 500,
    });
    renderHook(() =>
      useBottomSettleScroll(
        baseProps({
          messages: [],
          virtuosoRef: h.ref,
          scrollContainerEl: h.el,
          messageRefMap: h.map,
        }),
      ),
    );
    flushFrames();
    expect(h.scrollToIndex).not.toHaveBeenCalled();
  });

  it("only settles once per channel visit (a guarded re-render does not re-scroll)", () => {
    const h = coldMountHarness({
      clientHeight: 500,
      heightBefore: 1000,
      heightAfter: 1000,
      rendersAfterAttempts: 1,
      perScrollDelta: 500, // reaches the bottom on the first scroll
    });
    const { rerender } = renderHook((props: BottomProps) => useBottomSettleScroll(props), {
      initialProps: baseProps({
        virtuosoRef: h.ref,
        scrollContainerEl: h.el,
        messageRefMap: h.map,
      }),
    });
    flushFrames();
    const afterFirst = h.scrollToIndex.mock.calls.length;
    expect(afterFirst).toBeGreaterThanOrEqual(1);
    // Same channel, benign re-render (e.g. messages refetch echo) — already at
    // the bottom, must not re-anchor.
    rerender(
      baseProps({
        messages: messages(IDS),
        virtuosoRef: h.ref,
        scrollContainerEl: h.el,
        messageRefMap: h.map,
      }),
    );
    flushFrames();
    expect(h.scrollToIndex.mock.calls.length).toBe(afterFirst);
  });

  it("gives up at the frame cap and logs when the bottom is never reached", () => {
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    const h = coldMountHarness({
      clientHeight: 500,
      heightBefore: 1000,
      heightAfter: 1000,
      rendersAfterAttempts: 1,
      perScrollDelta: 0, // rendered but scroll never moves → never reaches
    });
    renderHook(() =>
      useBottomSettleScroll(
        baseProps({ virtuosoRef: h.ref, scrollContainerEl: h.el, messageRefMap: h.map }),
      ),
    );
    flushFrames();
    // Default settle cap is 180 frames.
    expect(h.scrollToIndex).toHaveBeenCalledTimes(180);
    expect(warn).toHaveBeenCalledWith(
      "[useBottomSettleScroll] settle timed out — never reached the bottom band",
      { channelId: "c1" },
    );
  });
});
