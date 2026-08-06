// @vitest-environment node
import { describe, expect, it } from "vitest";
import { CameraAnimator, DEFAULT_DURATION_MS, type AnimValue } from "./animator";

/** Manual clock + RAf harness so tests are deterministic under node. */
function makeFake() {
  let rafPending: Array<{ id: number; cb: (t: number) => void }> = [];
  let nextId = 1;
  let time = 0;
  const clock = {
    raf: (cb: (t: number) => void) => {
      const id = nextId++;
      rafPending.push({ id, cb });
      return id;
    },
    cancelRaf: (id: number) => {
      rafPending = rafPending.filter((p) => p.id !== id);
    },
    now: () => time,
    /** Advance the fake clock and flush pending frames. */
    tick(ms: number): void {
      time += ms;
      const frames = rafPending;
      rafPending = [];
      for (const f of frames) f.cb(time);
    },
    cancelAll(): void {
      rafPending = [];
    },
  };
  return clock;
}

const from: AnimValue = { x: 0, y: 0, zoom: 1 };
const toA: AnimValue = { x: 100, y: 100, zoom: 1 };
const toB: AnimValue = { x: 400, y: 400, zoom: 1 };

describe("CameraAnimator", () => {
  it("interpolates toward the target over the duration", () => {
    const clock = makeFake();
    const animator = new CameraAnimator({
      raf: clock.raf,
      cancelRaf: clock.cancelRaf,
      now: clock.now,
    });
    const updates: AnimValue[] = [];
    animator.start(from, toA, (v) => updates.push({ ...v }));

    clock.tick(DEFAULT_DURATION_MS / 2);
    expect(updates.length).toBeGreaterThan(0);
    const mid = updates[updates.length - 1]!;
    expect(mid.x).toBeGreaterThan(0);
    expect(mid.x).toBeLessThan(100);

    clock.tick(DEFAULT_DURATION_MS);
    expect(animator.running).toBe(false);
    expect(updates[updates.length - 1]!.x).toBeCloseTo(100);
  });

  it("terminates at the exact end value when finished", () => {
    const clock = makeFake();
    let end: AnimValue | null = null;
    const animator = new CameraAnimator({
      raf: clock.raf,
      cancelRaf: clock.cancelRaf,
      now: clock.now,
    });
    animator.start(from, toA, (v) => (end = { ...v }));
    clock.tick(DEFAULT_DURATION_MS + 5);
    expect(end!.x).toBe(toA.x);
    expect(end!.y).toBe(toA.y);
  });

  it("a new start cancels the running tween and re-baselines (no stacking/drift)", () => {
    const clock = makeFake();
    const animator = new CameraAnimator({
      raf: clock.raf,
      cancelRaf: clock.cancelRaf,
      now: clock.now,
    });
    const updates: AnimValue[] = [];
    animator.start(from, toA, (v) => updates.push({ ...v }));
    // Interrupt mid-flight.
    clock.tick(DEFAULT_DURATION_MS / 2);
    const interruptAt = updates[updates.length - 1]!;

    // Second request re-baselines from the current position toward toB.
    animator.start(interruptAt, toB, (v) => updates.push({ ...v }));
    clock.tick(DEFAULT_DURATION_MS);
    const final = updates[updates.length - 1]!;
    expect(final.x).toBeCloseTo(toB.x);
    expect(animator.running).toBe(false);
    // Only one tween's worth of trailing frames — no leftover frames from tween A.
    expect(final.y).toBeCloseTo(toB.y);
  });

  it("stop() halts before reaching the target without firing onEnd", () => {
    const clock = makeFake();
    let ended = false;
    const animator = new CameraAnimator({
      raf: clock.raf,
      cancelRaf: clock.cancelRaf,
      now: clock.now,
    });
    animator.start(from, toA, () => {}, { onEnd: () => (ended = true) });
    clock.tick(DEFAULT_DURATION_MS / 4);
    animator.stop();
    clock.tick(DEFAULT_DURATION_MS);
    expect(animator.running).toBe(false);
    expect(ended).toBe(false);
  });

  it("duration 0 snaps straight to the target", () => {
    const clock = makeFake();
    let last: AnimValue | null = null;
    const animator = new CameraAnimator({
      raf: clock.raf,
      cancelRaf: clock.cancelRaf,
      now: clock.now,
    });
    animator.start(from, toA, (v) => (last = { ...v }), { durationMs: 0 });
    expect(animator.running).toBe(false);
    expect(last!.x).toBe(toA.x);
  });
});
