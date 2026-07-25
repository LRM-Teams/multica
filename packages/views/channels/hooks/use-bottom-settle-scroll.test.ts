// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { renderHook } from "@testing-library/react";
import type { ChannelMessage } from "@multica/core/types";
import { useBottomSettleScroll } from "./use-bottom-settle-scroll";
import * as bssDiag from "./bss-diagnostic";

// Manual frame pump: flush a bounded number of queued frames.
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
  delete (window as unknown as { __bssTraceEnabled?: boolean }).__bssTraceEnabled;
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
const CLIENT_HEIGHT = 616;

/**
 * Honest harness (Barry's #3): row geometry is DERIVED from the real scrollTop
 * setter + scrollHeight, never set by the mock action.
 *
 * Models the cold-mount measurement-evolution window: the scroller starts with
 * scrollHeight === clientHeight (the trap: distanceToBottom reads 0), and each
 * direct write (one per settle frame) "measures" a bit more content until it
 * reaches `finalHeight`. Consistent with a real browser:
 *  - `el.scrollTop` has a clamping setter (a write of scrollHeight lands at
 *    scrollHeight - clientHeight, the true bottom) — this is why direct scrollTop
 *    moves the scroll where Virtuoso's `scrollToIndex` (its internal model) did
 *    not.
 *  - the LAST row enters the ref map only once content has grown to include it
 *    (rows render/measure lazily) — so geometry is never read against a
 *    half-measured list, which is exactly what stops the frame-1 false-settle.
 *  - the last row's bottom edge = `scrollHeight - scrollTop` (content bottom's
 *    position after scrolling), derived, not mock-set.
 */
function harness(opts: { finalHeight: number; stepPerWrite: number }) {
  const el = document.createElement("div");
  let scrollTop = 0;
  let writes = 0;
  const scrollHeightNow = () =>
    Math.min(opts.finalHeight, CLIENT_HEIGHT + writes * opts.stepPerWrite);

  const lastRowEl = document.createElement("div");
  const map = new Map<string, HTMLElement>();
  lastRowEl.getBoundingClientRect = () => {
    const sh = scrollHeightNow();
    return { top: sh - scrollTop - 40, bottom: sh - scrollTop } as DOMRect;
  };

  Object.defineProperty(el, "clientHeight", { value: CLIENT_HEIGHT, configurable: true });
  Object.defineProperty(el, "scrollHeight", { get: scrollHeightNow, configurable: true });
  Object.defineProperty(el, "scrollTop", {
    get: () => scrollTop,
    set: (v: number) => {
      writes += 1; // one direct write = one measurement step of progress
      const sh = scrollHeightNow();
      scrollTop = Math.max(0, Math.min(v, sh - CLIENT_HEIGHT)); // browser clamp
      // The last row becomes measurable once the full content height is reached.
      if (sh >= opts.finalHeight) map.set(LAST_ID, lastRowEl);
    },
    configurable: true,
  });
  el.getBoundingClientRect = () => ({ top: 0, bottom: CLIENT_HEIGHT }) as DOMRect;

  return {
    el,
    map,
    get scrollTop() {
      return scrollTop;
    },
    get scrollHeight() {
      return scrollHeightNow();
    },
    userScrollTo(v: number) {
      // A user scroll must NOT count as a settle write / measurement step.
      const sh = scrollHeightNow();
      scrollTop = Math.max(0, Math.min(v, sh - CLIENT_HEIGHT));
    },
  };
}

type BottomProps = Parameters<typeof useBottomSettleScroll>[0];
const baseProps = (
  over: Partial<BottomProps> & Pick<BottomProps, "scrollContainerEl" | "messageRefMap">,
): BottomProps => ({
  channelId: "c1",
  messages: messages(IDS),
  enabled: true,
  handleAttached: true,
  ...over,
});

describe("useBottomSettleScroll", () => {
  it("cold top, no gesture: per-frame direct write follows scrollHeight growth to the geometry bottom", () => {
    // 879 final (263 over the viewport), grows ~90px per write → a few frames.
    const h = harness({ finalHeight: 879, stepPerWrite: 90 });
    renderHook(() =>
      useBottomSettleScroll(baseProps({ scrollContainerEl: h.el, messageRefMap: h.map })),
    );
    flushFrames();
    expect(h.scrollHeight).toBe(879); // measurement completed
    expect(h.scrollTop).toBe(879 - CLIENT_HEIGHT); // 263 — pinned to the true bottom
  });

  it("does not false-settle at the top while scrollHeight is still === clientHeight (the trap)", () => {
    // Content never actually overflows in this contrived setup (never grows),
    // so the last row never becomes measurable → the settle can't confirm the
    // bottom and times out, rather than silently false-settling at scrollTop 0.
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    const h = harness({ finalHeight: 879, stepPerWrite: 0 }); // scrollHeight stuck at 616
    renderHook(() =>
      useBottomSettleScroll(baseProps({ scrollContainerEl: h.el, messageRefMap: h.map })),
    );
    flushFrames();
    expect(h.scrollTop).toBe(0);
    expect(warn).toHaveBeenCalledWith(
      "[useBottomSettleScroll] settle timed out — never reached the bottom band",
      { channelId: "c1" },
    );
  });

  it("hands off PERMANENTLY on the first real gesture — no re-pin after release", () => {
    const h = harness({ finalHeight: 5000, stepPerWrite: 20 }); // long, slow to settle
    renderHook(() =>
      useBottomSettleScroll(baseProps({ scrollContainerEl: h.el, messageRefMap: h.map })),
    );
    flushFrames(5); // a few settle frames — it's actively pinning toward the moving bottom
    expect(h.scrollTop).toBeGreaterThan(0);

    // User grabs, releases, and scrolls up to the top themselves.
    h.el.dispatchEvent(new Event("touchstart"));
    h.el.dispatchEvent(new Event("touchend"));
    h.userScrollTo(0);

    flushFrames(60);
    // The settle permanently exited on the gesture epoch bump — it must NOT have
    // re-pinned to the bottom after release. The user's position (0) stands.
    expect(h.scrollTop).toBe(0);
  });

  it("keeps pinning across a wheel that goes idle is NOT a thing — a wheel also hands off permanently", () => {
    const h = harness({ finalHeight: 5000, stepPerWrite: 20 });
    renderHook(() =>
      useBottomSettleScroll(baseProps({ scrollContainerEl: h.el, messageRefMap: h.map })),
    );
    flushFrames(5);
    expect(h.scrollTop).toBeGreaterThan(0);
    h.el.dispatchEvent(new Event("wheel")); // real wheel → epoch bump → permanent handoff
    h.userScrollTo(0);
    flushFrames(60);
    expect(h.scrollTop).toBe(0);
  });

  it("does not write when a gesture is ALREADY active as the settle starts (epoch baseline would miss it)", () => {
    const h = harness({ finalHeight: 5000, stepPerWrite: 20 });
    // Start disabled so the settle doesn't run yet, but the shared gesture hook
    // still attaches its listeners. A touch begins (bumps the epoch to 1) WHILE
    // disabled. Then enable: the settle's first run captures startEpoch = 1, so
    // the epoch is already at baseline and can't signal the in-progress gesture —
    // only the active flag can. It must hand off immediately, never writing.
    const { rerender } = renderHook((props: BottomProps) => useBottomSettleScroll(props), {
      initialProps: baseProps({ enabled: false, scrollContainerEl: h.el, messageRefMap: h.map }),
    });
    h.el.dispatchEvent(new Event("touchstart")); // epoch 0→1, active=true, while disabled
    rerender(baseProps({ enabled: true, scrollContainerEl: h.el, messageRefMap: h.map }));
    flushFrames(60);
    expect(h.scrollTop).toBe(0); // never pinned — aborted on the active flag
  });

  it("stays handed off across a messages churn that re-runs the effect after a gesture (per-visit baseline)", () => {
    // The race: gesture + release, then a normal messages-array churn re-runs the
    // effect BEFORE the pending frame observed the gesture (its rAF is cancelled
    // by cleanup). A per-effect-run epoch baseline would re-baseline the bumped
    // epoch, see the gesture released (active=false), and resume pinning. The
    // per-visit baseline keeps the handoff durable.
    const h = harness({ finalHeight: 5000, stepPerWrite: 20 });
    const { rerender } = renderHook((props: BottomProps) => useBottomSettleScroll(props), {
      initialProps: baseProps({ scrollContainerEl: h.el, messageRefMap: h.map }),
    });
    flushFrames(3); // actively pinning toward the moving bottom
    expect(h.scrollTop).toBeGreaterThan(0);

    // User grabs, releases, scrolls up.
    h.el.dispatchEvent(new Event("touchstart"));
    h.el.dispatchEvent(new Event("touchend"));
    h.userScrollTo(0);

    // Messages churn re-runs the effect (new array, same channel) WITHOUT first
    // flushing the pending frame.
    rerender(
      baseProps({ messages: messages([...IDS, "d"]), scrollContainerEl: h.el, messageRefMap: h.map }),
    );
    flushFrames(60);
    // Ownership stayed with the user — no re-pin after the churn.
    expect(h.scrollTop).toBe(0);
  });

  it("does nothing when disabled (a highlight/anchor owns the mount)", () => {
    const h = harness({ finalHeight: 879, stepPerWrite: 90 });
    renderHook(() =>
      useBottomSettleScroll(
        baseProps({ enabled: false, scrollContainerEl: h.el, messageRefMap: h.map }),
      ),
    );
    flushFrames();
    expect(h.scrollTop).toBe(0);
  });

  it("does nothing before the scroll container exists", () => {
    const h = harness({ finalHeight: 879, stepPerWrite: 90 });
    renderHook(() =>
      useBottomSettleScroll(baseProps({ scrollContainerEl: null, messageRefMap: h.map })),
    );
    flushFrames();
    expect(h.scrollTop).toBe(0);
  });

  it("does nothing while the Virtuoso handle has not attached", () => {
    const h = harness({ finalHeight: 879, stepPerWrite: 90 });
    renderHook(() =>
      useBottomSettleScroll(
        baseProps({ handleAttached: false, scrollContainerEl: h.el, messageRefMap: h.map }),
      ),
    );
    flushFrames();
    expect(h.scrollTop).toBe(0);
  });

  it("does nothing with no messages (empty conversation)", () => {
    const h = harness({ finalHeight: 879, stepPerWrite: 90 });
    renderHook(() =>
      useBottomSettleScroll(
        baseProps({ messages: [], scrollContainerEl: h.el, messageRefMap: h.map }),
      ),
    );
    flushFrames();
    expect(h.scrollTop).toBe(0);
  });

  it("only settles once per channel visit (a guarded re-render does not re-pin)", () => {
    const h = harness({ finalHeight: 879, stepPerWrite: 879 - CLIENT_HEIGHT }); // one write to full
    const { rerender } = renderHook((props: BottomProps) => useBottomSettleScroll(props), {
      initialProps: baseProps({ scrollContainerEl: h.el, messageRefMap: h.map }),
    });
    flushFrames();
    expect(h.scrollTop).toBe(879 - CLIENT_HEIGHT); // reached the bottom
    // User scrolls up; a benign re-render (e.g. messages refetch echo) must NOT re-pin.
    h.userScrollTo(0);
    rerender(baseProps({ messages: messages(IDS), scrollContainerEl: h.el, messageRefMap: h.map }));
    flushFrames();
    expect(h.scrollTop).toBe(0);
  });

  it("times out and logs if the last row never renders", () => {
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    // Content grows past the viewport but the last row is force-removed from the
    // map every time it's added → geometry never confirmable → cap + warn.
    const h = harness({ finalHeight: 879, stepPerWrite: 90 });
    const origSet = h.map.set.bind(h.map);
    h.map.set = ((k: string, v: HTMLElement) => (k === LAST_ID ? h.map : origSet(k, v))) as typeof h.map.set;
    renderHook(() =>
      useBottomSettleScroll(baseProps({ scrollContainerEl: h.el, messageRefMap: h.map })),
    );
    flushFrames();
    expect(warn).toHaveBeenCalledWith(
      "[useBottomSettleScroll] settle timed out — never reached the bottom band",
      { channelId: "c1" },
    );
  });

  it("DIAGNOSTIC zero-cost: never reaches a bssRecord call site on the default path (flag unset)", () => {
    // Every record site is guarded by `if (bssTraceEnabled()) bssRecord(...)`, so
    // with the flag unset the call is short-circuited and its field object (DOM
    // reads + Math.round) is never even constructed. Proving bssRecord is never
    // CALLED proves the fields are never evaluated (Barry's zero-cost contract).
    const rec = vi.spyOn(bssDiag, "bssRecord");
    const h = harness({ finalHeight: 879, stepPerWrite: 90 });
    renderHook(() =>
      useBottomSettleScroll(baseProps({ scrollContainerEl: h.el, messageRefMap: h.map })),
    );
    flushFrames();
    expect(h.scrollTop).toBe(879 - CLIENT_HEIGHT); // behaviour unchanged — still settles
    expect(rec).not.toHaveBeenCalled();
  });

  it("DIAGNOSTIC: records the effect/write/reach/settled chronology once opted in", () => {
    (window as unknown as { __bssTraceEnabled?: boolean }).__bssTraceEnabled = true;
    const rec = vi.spyOn(bssDiag, "bssRecord");
    const h = harness({ finalHeight: 879, stepPerWrite: 90 });
    renderHook(() =>
      useBottomSettleScroll(baseProps({ scrollContainerEl: h.el, messageRefMap: h.map })),
    );
    flushFrames();
    const kinds = new Set(rec.mock.calls.map((c) => c[0]));
    expect(kinds.has("effect")).toBe(true);
    expect(kinds.has("write")).toBe(true);
    expect(kinds.has("settled")).toBe(true);
    // tailSeq/sourceTailComplete are recorded (the source-contract fields Barry
    // needs to classify data-lag vs measurement-lag), even when absent → null.
    const effect = rec.mock.calls.find((c) => c[0] === "effect")?.[1];
    expect(effect).toHaveProperty("tailSeq");
    expect(effect).toHaveProperty("sourceTailComplete");
    expect(effect).toHaveProperty("messagesLoading");
  });
});
