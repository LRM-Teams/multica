// @vitest-environment node
import { describe, expect, it } from "vitest";
import { CameraAnimator } from "./animator";
import {
  ResearchCameraController,
  type ResearchCameraDriver,
} from "./controller";
import type { NodeBounds, Viewport } from "./geometry";

const SIZE = { width: 1280, height: 800 };
const INSETS = { top: 56, right: 184, bottom: 84, left: 16 };
const SAFE_X = 16 + (1280 - 16 - 184) / 2; // 556
const SAFE_Y = 56 + (800 - 56 - 84) / 2; // 386
const DURATION = 260;

/** Fake RAF + clock to drive the CameraAnimator deterministically. */
function makeAnimator(): { animator: CameraAnimator; tick(ms: number): void } {
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

function makeDriver(overrides: Partial<ResearchCameraDriver> = {}): {
  driver: ResearchCameraDriver;
  viewport: Viewport;
  applied: Viewport[];
  announcements: string[];
} {
  const state: Viewport = { x: 0, y: 0, zoom: 1 };
  const applied: Viewport[] = [];
  const announcements: string[] = [];
  const driver: ResearchCameraDriver = {
    viewportSize: () => SIZE,
    getViewport: () => ({ ...state }),
    applyViewport: (vp) => {
      Object.assign(state, vp);
      applied.push({ ...vp });
    },
    insets: () => INSETS,
    reducedMotion: () => false,
    announce: (text) => announcements.push(text),
    ...overrides,
  };
  return { driver, viewport: state, applied, announcements };
}

const NODE: NodeBounds = { x: 1000, y: 600, width: 240, height: 76 };
const NODE_CENTER = { x: 1120, y: 638 };

function makeCam(driver: ResearchCameraDriver) {
  const { animator, tick } = makeAnimator();
  const cam = new ResearchCameraController(driver, { animator });
  return { cam, tick };
}

describe("ResearchCameraController focus", () => {
  it("centres the target node in the safe region (mouse click / detail jump)", () => {
    const { driver, applied } = makeDriver();
    const { cam, tick } = makeCam(driver);
    cam.focus({ bounds: NODE, label: "Node A" });
    tick(DURATION);

    const last = applied[applied.length - 1]!;
    expect((NODE_CENTER.x - last.x) * last.zoom).toBeCloseTo(SAFE_X);
    expect((NODE_CENTER.y - last.y) * last.zoom).toBeCloseTo(SAFE_Y);
    expect(cam.hasPendingFocus).toBe(false);
  });

  it("announces the focused node label via the live region", () => {
    const { driver, announcements } = makeDriver();
    const { cam, tick } = makeCam(driver);
    cam.focus({ bounds: NODE, label: "Insight A" });
    tick(DURATION);
    expect(announcements).toContain("Insight A");
    expect(cam.hasPendingFocus).toBe(false);
  });

  it("handles a node already at the safe centre without animating", () => {
    const targetX = NODE_CENTER.x - SAFE_X;
    const targetY = NODE_CENTER.y - SAFE_Y;
    const { driver, applied } = makeDriver({
      getViewport: () => ({ x: targetX, y: targetY, zoom: 1 }),
    });
    const { cam, tick } = makeCam(driver);
    cam.focus({ bounds: NODE, label: "near" });
    tick(DURATION);
    expect(applied.length).toBe(0);
    expect(cam.hasPendingFocus).toBe(false);
  });
});

describe("rapid focus interruption (AC2)", () => {
  it("a second focus wins and never stacks/drifts the animation", () => {
    const { driver, applied } = makeDriver();
    const { cam, tick } = makeCam(driver);
    const nodeA: NodeBounds = { x: 0, y: 0, width: 200, height: 60 };
    const nodeB: NodeBounds = { x: 5000, y: 3000, width: 200, height: 60 };
    const bCenter = { x: 5100, y: 3030 };

    cam.focus({ bounds: nodeA, label: "A" });
    tick(DURATION / 2); // A partially runs
    cam.focus({ bounds: nodeB, label: "B" }); // supersede
    tick(DURATION);

    const last = applied[applied.length - 1]!;
    expect((bCenter.x - last.x) * last.zoom).toBeCloseTo(SAFE_X);
    expect((bCenter.y - last.y) * last.zoom).toBeCloseTo(SAFE_Y);
    expect(cam.hasPendingFocus).toBe(false);
  });

  it("user drag interrupts an in-flight auto-move immediately", () => {
    const { driver, applied } = makeDriver();
    const { cam, tick } = makeCam(driver);
    cam.focus({ bounds: NODE, label: "auto" });
    tick(DURATION / 4);
    cam.userInteracted();
    expect(cam.hasPendingFocus).toBe(false);
    const countAtInterrupt = applied.length;
    tick(DURATION);
    // No further frames applied after interrupt → count stable.
    expect(applied.length).toBe(countAtInterrupt);
  });

  it("cancel() drops the active focus so no further frames apply", () => {
    const { driver, applied } = makeDriver();
    const { cam, tick } = makeCam(driver);
    cam.focus({ bounds: NODE, label: "x" });
    tick(DURATION / 4);
    cam.cancel();
    const count = applied.length;
    tick(DURATION);
    expect(applied.length).toBe(count);
    expect(cam.hasPendingFocus).toBe(false);
  });
});

describe("reduced motion (AC2/AC3)", () => {
  it("snaps directly to the safe centre instead of animating", () => {
    const { driver, applied } = makeDriver({ reducedMotion: () => true });
    const { cam, tick } = makeCam(driver);
    cam.focus({ bounds: NODE, label: "reduced" });
    tick(DURATION);

    expect(applied.length).toBe(1);
    expect((NODE_CENTER.x - applied[0]!.x) * applied[0]!.zoom).toBeCloseTo(SAFE_X);
    expect((NODE_CENTER.y - applied[0]!.y) * applied[0]!.zoom).toBeCloseTo(SAFE_Y);
    expect(cam.hasPendingFocus).toBe(false);
  });
});
