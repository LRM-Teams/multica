// @vitest-environment jsdom
import { act, renderHook } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { CameraAnimator } from "../canvas/camera/animator";
import { createSemanticCamera, type Viewport } from "./camera";
import { useSemanticFocus } from "./use-semantic-focus";
import type { ResearchCameraDriver } from "../canvas/camera/controller";

const SIZE = { width: 1280, height: 800 };
const INSETS = { top: 56, right: 184, bottom: 84, left: 16 };
const SAFE_X = 16 + (1280 - 16 - 184) / 2; // 556
const SAFE_Y = 56 + (800 - 56 - 84) / 2; // 386

function makeCamera() {
  const state = { x: 0, y: 0, zoom: 1 };
  const applied: Array<{ x: number; y: number; zoom: number }> = [];
  let pending: Array<(t: number) => void> = [];
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
  const driver: ResearchCameraDriver = {
    viewportSize: () => SIZE,
    getViewport: () => ({ ...state }),
    applyViewport: (vp) => {
      Object.assign(state, vp);
      applied.push({ ...vp });
    },
    insets: () => INSETS,
    reducedMotion: () => false,
  };
  const camera = createSemanticCamera(driver, { animator });
  return {
    camera,
    driver,
    applied,
    tick(ms: number) {
      time += ms;
      const frames = pending;
      pending = [];
      for (const f of frames) f(time);
    },
  };
}

function setup() {
  const cam = makeCamera();
  const logs: string[] = [];
  const setSelectedId: (id: string | null) => void = vi.fn(
    (id: string | null) => void logs.push(`select:${id ?? "null"}`),
  );
  const openInspector: (id: string) => void = vi.fn(
    (id: string) => void logs.push(`inspector:${id}`),
  );
  const promoteLod: (id: string) => void = vi.fn(
    (id: string) => void logs.push(`promote:${id}`),
  );
  const restoreLod: () => void = vi.fn(() => void logs.push("restoreLod"));
  const applyViewport: (vp: Viewport) => void = vi.fn((vp: Viewport) =>
    cam.driver.applyViewport(vp),
  );

  const props = {
    camera: cam.camera,
    getViewport: () => cam.driver.getViewport(),
    applyViewport,
    setSelectedId,
    openInspector,
    promoteLod,
    restoreLod,
  };
  const { result } = renderHook(() => useSemanticFocus(props));

  return { result, cam, logs, viFns: { setSelectedId, openInspector, promoteLod, restoreLod, applyViewport } };
}

describe("useSemanticFocus", () => {
  it("select → open Inspector → promote → capture snapshot → focus (AC3)", () => {
    const { result, cam, logs, viFns } = setup();
    act(() => {
      result.current.promoteAndFocus("n1", { x: 1000, y: 600, width: 240, height: 76 }, "Node 1");
    });
    expect(viFns.setSelectedId).toHaveBeenCalledWith("n1");
    expect(viFns.openInspector).toHaveBeenCalledWith("n1");
    expect(viFns.promoteLod).toHaveBeenCalledWith("n1");
    cam.tick(260);
    const last = cam.applied[cam.applied.length - 1]!;
    expect((1000 + 120 - last.x) * last.zoom).toBeCloseTo(SAFE_X);
    expect((600 + 38 - last.y) * last.zoom).toBeCloseTo(SAFE_Y);
    expect(result.current.focusActive).toBe(true);
    void logs;
  });

  it("Back restores original LOD, selection and viewport", () => {
    const { result, cam, viFns } = setup();
    act(() => {
      result.current.promoteAndFocus("n1", { x: 1000, y: 600, width: 200, height: 60 }, "A");
    });
    cam.tick(130);
    // user panned a bit
    act(() => {
      cam.driver.applyViewport({ x: 320, y: 300, zoom: 1 });
    });
    act(() => {
      result.current.back();
    });
    expect(viFns.restoreLod).toHaveBeenCalledTimes(1);
    expect(viFns.setSelectedId).toHaveBeenLastCalledWith("n1");
    // viewport restored to the snapshot captured at promotion (0,0).
    expect(cam.driver.getViewport().x).toBe(0);
    expect(cam.driver.getViewport().y).toBe(0);
    expect(result.current.focusActive).toBe(false);
  });

  it("continuous clicks: the second promotion supersedes the first (regression)", () => {
    const { result, cam } = setup();
    act(() => result.current.promoteAndFocus("a", { x: 0, y: 0, width: 200, height: 60 }, "A"));
    cam.tick(130);
    act(() => result.current.promoteAndFocus("b", { x: 5000, y: 3000, width: 200, height: 60 }, "B"));
    cam.tick(260);
    const last = cam.applied[cam.applied.length - 1]!;
    expect((5000 + 100 - last.x) * last.zoom).toBeCloseTo(SAFE_X);
    expect((3000 + 30 - last.y) * last.zoom).toBeCloseTo(SAFE_Y);
  });

  it("userInteracted cancels the in-flight auto camera", () => {
    const { result, cam } = setup();
    act(() => result.current.promoteAndFocus("n1", { x: 1000, y: 600, width: 240, height: 76 }, "A"));
    cam.tick(65);
    act(() => result.current.userInteracted());
    const countAtInterrupt = cam.applied.length;
    cam.tick(260);
    expect(cam.applied.length).toBe(countAtInterrupt);
    expect(result.current.hasPendingFocus).toBe(false);
  });

  it("reduced motion: promotion lands the final state directly (no probe)", () => {
    const cam2 = makeCamera();
    cam2.driver.reducedMotion = () => true;
    const { result } = renderHook(() =>
      useSemanticFocus({
        camera: cam2.camera,
        getViewport: () => cam2.driver.getViewport(),
        applyViewport: cam2.driver.applyViewport,
        setSelectedId: vi.fn(),
        openInspector: vi.fn(),
        promoteLod: vi.fn(),
        restoreLod: vi.fn(),
      }),
    );
    act(() => {
      result.current.promoteAndFocus("n1", { x: 1000, y: 600, width: 240, height: 76 }, "Snap");
    });
    // No animation frames needed — the controller snapped on apply.
    const last = cam2.applied[cam2.applied.length - 1]!;
    expect((1000 + 120 - last.x) * last.zoom).toBeCloseTo(SAFE_X);
    expect((600 + 38 - last.y) * last.zoom).toBeCloseTo(SAFE_Y);
    expect(cam2.applied.length).toBe(1);
  });
});
