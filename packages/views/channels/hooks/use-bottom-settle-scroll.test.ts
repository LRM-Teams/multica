// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { renderHook } from "@testing-library/react";
import type { ChannelMessage } from "@multica/core/types";
import { useBottomSettleScroll } from "./use-bottom-settle-scroll";

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
    lastRowEl,
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
  listReady: true,
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

  it("does not pin while the Virtuoso handle has not attached (loop waits, never writes)", () => {
    // The loop now STARTS before the handle attaches (so it can catch a late
    // attach — see the "attaches LATE" test), but it must not pin while detached;
    // with no attach ever coming it simply waits out the frame cap.
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    const h = harness({ finalHeight: 879, stepPerWrite: 90 });
    renderHook(() =>
      useBottomSettleScroll(
        baseProps({ listReady: false, scrollContainerEl: h.el, messageRefMap: h.map }),
      ),
    );
    flushFrames();
    expect(h.scrollTop).toBe(0); // never pinned — handle never attached
    expect(warn).toHaveBeenCalled(); // waited out the cap (no attach to pin with)
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

  // --- around-seq successor contract (2026-07-26) ---
  // NOTE: the ROOT bug is a browser-layer timing race — Virtuoso detaches/reattaches
  // its imperative handle mid-mount while the tail row measures/mounts ~440ms late,
  // and the effect (with `listReady` a dep) failed to re-arm the settle at that
  // moment. jsdom CANNOT reproduce that exact race (here `listReady` is a prop,
  // so any flip re-runs the old effect and it self-heals). These tests therefore
  // lock the NEW logic's *properties* (loop survives a detach and still lands the
  // bottom; a late-attaching handle still settles; short content settles at 0);
  // the definitive gate for the timing bug is Iris's real-device fully-read retest.

  it("survives a handle detach/reattach mid-settle via ONE persistent loop (no effect re-run) and lands the bottom", () => {
    // Long enough to be genuinely mid-settle after a few frames, but reachable
    // within the frame cap even after the detach wait (detach frames count toward
    // the cap; in prod a detach window is a few ms, well within the ~3s backstop).
    const h = harness({ finalHeight: 2000, stepPerWrite: 40 });
    // CRITICAL (Ronan): a SINGLE stable `messages` instance across all rerenders.
    // `messages` IS an effect dep, so `baseProps`' default fresh `messages(IDS)`
    // array would re-run the effect on every rerender — and the test would silently
    // stop exercising the property it locks ("one loop survives a handle-only flip
    // WITHOUT an effect re-run"). We assert cancelAnimationFrame is never called on
    // the handle-only rerenders: no cleanup ⇒ no effect re-run ⇒ same rAF chain.
    const stableMessages = messages(IDS);
    const props = (over: Partial<BottomProps> = {}): BottomProps =>
      baseProps({ messages: stableMessages, scrollContainerEl: h.el, messageRefMap: h.map, ...over });
    const { rerender } = renderHook((p: BottomProps) => useBottomSettleScroll(p), {
      initialProps: props(),
    });
    flushFrames(3); // pinning toward the moving bottom, tail not yet measurable
    const midway = h.scrollTop;
    expect(midway).toBeGreaterThan(0);
    expect(midway).toBeLessThan(2000 - CLIENT_HEIGHT);

    // Virtuoso detaches its handle mid-settle. A handle-ONLY rerender must NOT
    // cancel the running rAF (proving the effect did not re-run and restart it).
    const cancelsBaseline = cancelled.size;
    rerender(props({ listReady: false }));
    expect(cancelled.size).toBe(cancelsBaseline); // no cleanup → same persistent loop
    flushFrames(15);
    expect(h.scrollTop).toBe(midway); // kept looping but never pinned while detached

    // Reattach (handle-only again, no re-run) — the SAME loop re-arms and lands.
    rerender(props({ listReady: true }));
    expect(cancelled.size).toBe(cancelsBaseline); // still no effect re-run
    flushFrames();
    expect(h.scrollTop).toBe(2000 - CLIENT_HEIGHT); // landed, not stuck
  });

  it("a handle that attaches LATE (loop starts before attach) still settles, with no re-run at attach", () => {
    const h = harness({ finalHeight: 879, stepPerWrite: 90 });
    const stableMessages = messages(IDS);
    const props = (over: Partial<BottomProps> = {}): BottomProps =>
      baseProps({ messages: stableMessages, scrollContainerEl: h.el, messageRefMap: h.map, ...over });
    const { rerender } = renderHook((p: BottomProps) => useBottomSettleScroll(p), {
      // Mount with the handle NOT yet attached — the loop starts (guard no longer
      // gates on listReady) but waits, pinning nothing.
      initialProps: props({ listReady: false }),
    });
    flushFrames(5);
    expect(h.scrollTop).toBe(0); // nothing pinned while detached

    const cancelsBaseline = cancelled.size;
    rerender(props({ listReady: true })); // handle-only flip
    expect(cancelled.size).toBe(cancelsBaseline); // the SAME loop picks up the attach live
    flushFrames();
    expect(h.scrollTop).toBe(879 - CLIENT_HEIGHT); // pins to bottom once attached
  });

  it("short content (fits the viewport) completes at scrollTop 0 without a false timeout", () => {
    // scrollHeight never exceeds clientHeight and the tail row is measurable from
    // the start (short conversation), so its bottom edge is already within the band
    // of the container's bottom → hasReached() is true on frame one → completes at 0,
    // no timeout, no console warn.
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    const h = harness({ finalHeight: CLIENT_HEIGHT, stepPerWrite: 0 }); // sh stays === ch
    h.map.set(LAST_ID, h.lastRowEl); // tail rendered from the start (short list)
    renderHook(() =>
      useBottomSettleScroll(baseProps({ scrollContainerEl: h.el, messageRefMap: h.map })),
    );
    flushFrames();
    expect(h.scrollTop).toBe(0);
    expect(warn).not.toHaveBeenCalled();
  });

  it("warm LONG content already at the bottom (declarative mount landed) completes on frame 1, no false ~3s timeout", () => {
    // Regression guard (Ronan): a long list (sh > ch) that the mount-once
    // declarative position already landed at the true bottom. The per-frame direct
    // write is a NO-OP (scrollTop already at max), but the tail row's geometry is
    // already in the bottom band — completion MUST fire immediately off hasReached,
    // not spin the frame cap and log a spurious timeout. (An earlier "effective pin
    // must have MOVED scrollTop" gate wrongly timed this healthy path out.)
    const SH = 2000;
    const el = document.createElement("div");
    let scrollTop = SH - CLIENT_HEIGHT; // 1384 — already at the true bottom
    Object.defineProperty(el, "clientHeight", { value: CLIENT_HEIGHT, configurable: true });
    Object.defineProperty(el, "scrollHeight", { value: SH, configurable: true });
    Object.defineProperty(el, "scrollTop", {
      get: () => scrollTop,
      set: (v: number) => {
        scrollTop = Math.max(0, Math.min(v, SH - CLIENT_HEIGHT)); // write clamps → no-op at max
      },
      configurable: true,
    });
    el.getBoundingClientRect = () => ({ top: 0, bottom: CLIENT_HEIGHT }) as DOMRect;
    const lastRowEl = document.createElement("div");
    // bottom = SH - scrollTop = 616 = container bottom → within BOTTOM_BAND_PX.
    lastRowEl.getBoundingClientRect = () =>
      ({ top: SH - scrollTop - 40, bottom: SH - scrollTop }) as DOMRect;
    const map = new Map<string, HTMLElement>([[LAST_ID, lastRowEl]]);

    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    renderHook(() =>
      useBottomSettleScroll(baseProps({ scrollContainerEl: el, messageRefMap: map })),
    );
    flushFrames();
    expect(scrollTop).toBe(SH - CLIENT_HEIGHT); // held at the bottom (no-op pin)
    expect(warn).not.toHaveBeenCalled(); // completed frame 1 — no spurious timeout
  });

  it("does NOT settle while DETACHED on a transient bottom-band geometry; reattach lands the EXACT true bottom (not the sh===ch false bottom, not stuck at 0)", () => {
    // Ronan's successor-contract blocker: `hasReached()` alone can be true during a
    // detach/remount when the tail momentarily enters the ref map with a band
    // geometry (the untrustworthy measurement window). Completing there — on ZERO
    // attached pin — ends the loop for good (reattach can't restart the effect),
    // reproducing the stuck-at-top failure. Only an ATTACHED, pinned frame may
    // complete. Custom harness: scrollHeight is EXTERNALLY controlled (not
    // write-coupled) so we can model the transient collapsed height (sh===ch) while
    // detached, then the real measured total after reattach — and assert the exact
    // final landing, matching the red control. (The generic harness kept the tail
    // out of the map while detached, so it never exercised this branch.)
    const SH_FINAL = 2000;
    const el = document.createElement("div");
    let sh = CLIENT_HEIGHT; // 616 — transient collapsed height during the detach window
    let scrollTop = 0;
    Object.defineProperty(el, "clientHeight", { value: CLIENT_HEIGHT, configurable: true });
    Object.defineProperty(el, "scrollHeight", { get: () => sh, configurable: true });
    Object.defineProperty(el, "scrollTop", {
      get: () => scrollTop,
      set: (v: number) => {
        scrollTop = Math.max(0, Math.min(v, sh - CLIENT_HEIGHT)); // browser clamp
      },
      configurable: true,
    });
    el.getBoundingClientRect = () => ({ top: 0, bottom: CLIENT_HEIGHT }) as DOMRect;
    const lastRowEl = document.createElement("div");
    // bottom = sh - scrollTop: within the band ONLY at the true bottom for the
    // CURRENT sh. While detached at sh===ch with scrollTop 0 it is transiently in
    // the band (616 = container bottom) — the false-complete condition.
    lastRowEl.getBoundingClientRect = () =>
      ({ top: sh - scrollTop - 40, bottom: sh - scrollTop }) as DOMRect;
    const map = new Map<string, HTMLElement>([[LAST_ID, lastRowEl]]);

    const stableMessages = messages(IDS);
    const props = (over: Partial<BottomProps> = {}): BottomProps =>
      baseProps({ messages: stableMessages, scrollContainerEl: el, messageRefMap: map, ...over });
    const { rerender } = renderHook((p: BottomProps) => useBottomSettleScroll(p), {
      initialProps: props({ listReady: false }), // mounted DETACHED
    });
    flushFrames(6);
    // Buggy version: settles here at 0 on zero attached pin, stuck forever.
    // Fix: nothing pinned while detached → no premature settle.
    expect(scrollTop).toBe(0);

    // Measurement completes (the real total is now known) and the handle reattaches.
    // The same still-alive loop pins and lands the EXACT true bottom.
    sh = SH_FINAL;
    rerender(props({ listReady: true }));
    flushFrames();
    expect(scrollTop).toBe(SH_FINAL - CLIENT_HEIGHT); // 1384 — real bottom, NOT 0, NOT 616's false bottom
  });

  // --- LRM-1220: measurement lands AFTER an attached "reached" frame ---
  // jianghp3 (mobile web, #pr-frontend): every channel open landed on 「今天」+
  // 当日第一条 with 「加载更早消息」 visible at the top — i.e. scrollTop 0 of the
  // loaded window, ~7h behind the newest message.
  //
  // The `pinnedThisFrame` gate only excludes the DETACHED collapsed window. On a
  // cold mount the handle IS attached while Virtuoso — seeded with
  // `initialTopMostItemIndex = last` — has rendered ONLY the tail rows: the
  // scroller is still collapsed (scrollHeight === clientHeight, the pin write is a
  // clamped no-op) yet the tail row's bottom edge sits exactly on the container
  // bottom, so `hasReached()` is true on frame one. The settle then completes
  // PERMANENTLY. When the remaining rows measure a few hundred ms later the
  // content grows BELOW a scrollTop that is still 0 — and nothing is left alive to
  // re-pin.
  function collapsedThenMeasuredHarness() {
    const el = document.createElement("div");
    let sh = CLIENT_HEIGHT; // collapsed: only the tail rows are measured
    let scrollTop = 0;
    Object.defineProperty(el, "clientHeight", { value: CLIENT_HEIGHT, configurable: true });
    Object.defineProperty(el, "scrollHeight", { get: () => sh, configurable: true });
    Object.defineProperty(el, "scrollTop", {
      get: () => scrollTop,
      set: (v: number) => {
        scrollTop = Math.max(0, Math.min(v, sh - CLIENT_HEIGHT)); // browser clamp
      },
      configurable: true,
    });
    el.getBoundingClientRect = () => ({ top: 0, bottom: CLIENT_HEIGHT }) as DOMRect;
    const lastRowEl = document.createElement("div");
    // Derived, never mock-set: the tail row's bottom edge is the content bottom's
    // position after scrolling. Collapsed + scrollTop 0 → 616 → inside the band.
    lastRowEl.getBoundingClientRect = () =>
      ({ top: sh - scrollTop - 40, bottom: sh - scrollTop }) as DOMRect;
    const map = new Map<string, HTMLElement>([[LAST_ID, lastRowEl]]);
    return {
      el,
      map,
      get scrollTop() {
        return scrollTop;
      },
      measurementLands(total: number) {
        sh = total; // rows above the tail finally measure; scrollTop is untouched
      },
      userScrollTo(v: number) {
        scrollTop = Math.max(0, Math.min(v, sh - CLIENT_HEIGHT));
      },
    };
  }

  it("re-pins when the real measurement lands AFTER an attached bottom-band frame (LRM-1220: stuck on 今天首条)", () => {
    const h = collapsedThenMeasuredHarness();
    renderHook(() =>
      useBottomSettleScroll(baseProps({ scrollContainerEl: h.el, messageRefMap: h.map })),
    );
    // Collapsed window: the pin is a clamped no-op and the tail is transiently in
    // the bottom band. Nothing has actually scrolled yet either way.
    flushFrames(6);
    expect(h.scrollTop).toBe(0);

    // Virtuoso finishes measuring the rows above the tail: the content is now
    // 2000px tall and scrollTop 0 shows the OLDEST loaded row — the bug's exact
    // viewport. The settle must still be alive to correct it.
    h.measurementLands(2000);
    flushFrames();
    expect(h.scrollTop).toBe(2000 - CLIENT_HEIGHT); // 1384 — landed on the newest row
  });

  it("does NOT re-pin after post-reach measurement growth once the user has gestured", () => {
    // The re-pin window must never out-rank the user: a gesture is still a
    // permanent handoff, even if the content grows afterwards.
    const h = collapsedThenMeasuredHarness();
    renderHook(() =>
      useBottomSettleScroll(baseProps({ scrollContainerEl: h.el, messageRefMap: h.map })),
    );
    flushFrames(3);
    h.el.dispatchEvent(new Event("touchstart"));
    h.el.dispatchEvent(new Event("touchend"));

    h.measurementLands(2000);
    h.userScrollTo(0); // the user is reading history at the top
    flushFrames();
    expect(h.scrollTop).toBe(0); // ownership stayed with the user
  });

  it("pins a NON-VIRTUALIZED list (direct fallback: no Virtuoso handle will ever attach)", () => {
    // LRM-1220's other half: `ChannelMessageList` drops to plain divs when
    // Virtuoso renders nothing within 350ms, so its imperative handle stays null
    // forever. `listReady` therefore means "the rows are a settled render we may
    // pin", NOT "Virtuoso attached" — the caller passes
    // `handleAttached || useDirectFallback`. Reading it as attach-only meant this
    // path never wrote a single scrollTop and the open rested on the oldest
    // loaded row (verified on the real stack: 50 plain rows, scrollTop 0,
    // "settle timed out" in the console).
    const h = harness({ finalHeight: 3560, stepPerWrite: 120 });
    renderHook(() =>
      // Ready with no handle — exactly what the fallback branch passes.
      useBottomSettleScroll(
        baseProps({ listReady: true, scrollContainerEl: h.el, messageRefMap: h.map }),
      ),
    );
    flushFrames();
    expect(h.scrollTop).toBe(3560 - CLIENT_HEIGHT); // landed on the newest row
  });
});
