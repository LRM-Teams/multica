// @vitest-environment node
import { describe, expect, it } from "vitest";
import { CameraAnimator } from "../canvas/camera/animator";
import { computeSafeCentreIntent, createSemanticCamera, SemanticCameraHandle } from "./camera";
import type { ResearchCameraDriver } from "../canvas/camera/controller";

const SIZE = { width: 1280, height: 800 };
const INSETS = { top: 56, right: 184, bottom: 84, left: 16 };
const SAFE_X = 16 + (1280 - 16 - 184) / 2; // 556
const SAFE_Y = 56 + (800 - 56 - 84) / 2; // 386

function makeAnimator() {
  let pending: Array<(time: number) => void> = [];
  let time = 0;
  const animator = new CameraAnimator({
    raf: (cb) => {
      pending.push(cb);
      return pending.length;
    },
    cancelRaf: (id) => {
      pending = pending.filter((_, i) => i + 1 !== id);
    },
    now: () => time,
  });
  return {
    animator,
    tick(ms: number) {
      time += ms;
      const frames = pending;
      pending = [];
      for (const f of frames) f(time);
    },
  };
}

function makeHandle(overrides: Partial<ResearchCameraDriver> = {}) {
  const state = { x: 0, y: 0, zoom: 1 };
  const applied: Array<{ x: number; y: number; zoom: number }> = [];
  const driver: ResearchCameraDriver = {
    viewportSize: () => SIZE,
    getViewport: () => ({ ...state }),
    applyViewport: (vp) => {
      Object.assign(state, vp);
      applied.push({ ...vp });
    },
    insets: () => INSETS,
    reducedMotion: () => false,
    ...overrides,
  };
  const a = makeAnimator();
  const handle = createSemanticCamera(driver, { animator: a.animator });
  return { driver, state, applied, handle, tick: a.tick };
}

describe("computeSafeCentreIntent (pure)", () => {
  it("computes a shouldMove=true intent for an off-centre node", () => {
    const intent = computeSafeCentreIntent({
      viewport: { x: 0, y: 0, zoom: 1 },
      bounds: { x: 1000, y: 600, width: 240, height: 76 },
      viewportSize: SIZE,
      insets: INSETS,
    });
    expect(intent.shouldMove).toBe(true);
    const cx = 1000 + 120;
    const cy = 600 + 38;
    expect((cx - intent.target.x) * intent.target.zoom).toBeCloseTo(SAFE_X);
    expect((cy - intent.target.y) * intent.target.zoom).toBeCloseTo(SAFE_Y);
  });

  it("returns shouldMove=false for a node already at the safe centre", () => {
    const bounds = { x: SAFE_X - 120, y: SAFE_Y - 38, width: 240, height: 76 };
    const intent = computeSafeCentreIntent({
      viewport: { x: 0, y: 0, zoom: 1 },
      bounds,
      viewportSize: SIZE,
      insets: INSETS,
    });
    expect(intent.shouldMove).toBe(false);
  });
});

describe("SemanticCameraHandle", () => {
  it("moves an off-centre node to the safe centre via the 260ms authority", () => {
    const { handle, tick, applied } = makeHandle();
    const bounds = { x: 1000, y: 600, width: 240, height: 76 };
    expect(handle.focus(bounds, "Insight A")).toBe(true);
    tick(260);
    const last = applied[applied.length - 1]!;
    expect((1000 + 120 - last.x) * last.zoom).toBeCloseTo(SAFE_X);
    expect((600 + 38 - last.y) * last.zoom).toBeCloseTo(SAFE_Y);
    expect(handle.hasPendingFocus).toBe(false);
  });

  it("does not move the camera when the node is already safe-centred", () => {
    const { handle, applied } = makeHandle();
    const bounds = { x: SAFE_X - 120, y: SAFE_Y - 38, width: 240, height: 76 };
    expect(handle.focus(bounds, "near")).toBe(false);
    expect(applied.length).toBe(0);
  });

  it("rapid consecutive clicks cancel the old intent (continuous-click regression)", () => {
    const { handle, tick, applied } = makeHandle();
    handle.focus({ x: 0, y: 0, width: 200, height: 60 }, "A");
    tick(130);
    handle.focus({ x: 5000, y: 3000, width: 200, height: 60 }, "B");
    tick(260);
    const last = applied[applied.length - 1]!;
    expect((5000 + 100 - last.x) * last.zoom).toBeCloseTo(SAFE_X);
    expect((3000 + 30 - last.y) * last.zoom).toBeCloseTo(SAFE_Y);
    expect(handle.hasPendingFocus).toBe(false);
  });

  it("user interaction cancels the in-flight auto move", () => {
    const { handle, tick, applied } = makeHandle();
    handle.focus({ x: 1000, y: 600, width: 240, height: 76 }, "auto");
    tick(65);
    handle.userInteracted();
    const countAtInterrupt = applied.length;
    tick(260);
    expect(applied.length).toBe(countAtInterrupt);
    expect(handle.hasPendingFocus).toBe(false);
  });

  it("reduced motion snaps directly to the safe centre (no probe/drift)", () => {
    const { handle, applied } = makeHandle({ reducedMotion: () => true });
    handle.focus({ x: 1000, y: 600, width: 240, height: 76 }, "snap");
    const last = applied[applied.length - 1]!;
    expect((1000 + 120 - last.x) * last.zoom).toBeCloseTo(SAFE_X);
    expect((600 + 38 - last.y) * last.zoom).toBeCloseTo(SAFE_Y);
    expect(handle.hasPendingFocus).toBe(false);
  });
});

describe("SemanticCameraHandle type export", () => {
  it("exports the handle type for consumers", () => {
    // Type-level guard only — ensures the symbol exists for `instanceof` use.
    expect(typeof SemanticCameraHandle).toBe("function");
  });
});
