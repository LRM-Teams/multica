/**
 * Semantic LOD — safe-centre camera intent (LRM-1488).
 *
 * The click-to-promote flow (viewport-performance §1, route-topology §7)
 * must: write selection → open Inspector → compute the safe-centre camera →
 * move. This seam computes the *intent* (pure) and reuses the existing
 * 260ms `ResearchCameraController` authority so there is exactly one camera
 * tween — no second tween stack, interruption-safe rapid clicks and
 * reduced-motion snap.
 */
import {
  ResearchCameraController,
  type ResearchCameraDriver,
} from "../canvas/camera/controller";
import type { CameraAnimator } from "../canvas/camera/animator";
import {
  boundsCenter,
  safeCenterPoint,
  viewportCenterOnBounds,
  type Insets,
  type NodeBounds,
  type Size,
  type Viewport,
} from "../canvas/camera/geometry";

/** Spec viewport-performance §1: within this radius we do not move the camera. */
export const SAFE_CENTRE_RADIUS_PX = 72;

/** Spec viewport-performance §1: focus move duration (easeOutCubic). */
export const FOCUS_DURATION_MS = 260;

export interface SafeCentreIntent {
  /** Target viewport that puts the node centre in the safe region. */
  target: Viewport;
  /**
   * False when the node centre is already within SAFE_CENTRE_RADIUS_PX of the
   * safe centre → update selection only, do not move the camera (spec §1.4).
   */
  shouldMove: boolean;
}

/**
 * Compute the safe-centre camera intent for a node. Pure — no side effects.
 */
export function computeSafeCentreIntent(args: {
  viewport: Viewport;
  bounds: NodeBounds;
  viewportSize: Size;
  insets: Insets;
}): SafeCentreIntent {
  const { viewport, bounds, viewportSize, insets } = args;
  const target = viewportCenterOnBounds(
    bounds,
    viewport.zoom,
    viewportSize,
    insets,
  );
  const center = boundsCenter(bounds);
  const safe = safeCenterPoint(viewportSize, insets);
  const screenCenterX = (center.x - viewport.x) * viewport.zoom;
  const screenCenterY = (center.y - viewport.y) * viewport.zoom;
  const dist = Math.hypot(screenCenterX - safe.x, screenCenterY - safe.y);
  return { target, shouldMove: dist > SAFE_CENTRE_RADIUS_PX };
}

/**
 * A camera handle bound to the canonical `ResearchCameraController` — the
 * single 260ms focus authority. All interruption / reduced-motion /
 * user-interaction semantics come from that controller; nothing is duplicated.
 */
export class SemanticCameraHandle {
  private driver: ResearchCameraDriver;
  private controller: ResearchCameraController;

  constructor(driver: ResearchCameraDriver, controller: ResearchCameraController) {
    this.driver = driver;
    this.controller = controller;
  }

  get hasPendingFocus(): boolean {
    return this.controller.hasPendingFocus;
  }

  /**
   * Focus the camera on `bounds`. The 72px safe-centre radius decides whether
   * any camera move happens (spec §1.4); when it does, the existing
   * controller handles the 260ms ease, rapid-click supersede, user-interaction
   * cancel and reduced-motion snap.
   */
  focus(bounds: NodeBounds, label?: string): boolean {
    const viewport = this.driver.getViewport();
    const viewportSize = this.driver.viewportSize();
    const insets = this.driver.insets();
    const intent = computeSafeCentreIntent({ viewport, bounds, viewportSize, insets });
    if (!intent.shouldMove) return false;
    this.controller.focus({ bounds, label });
    return true;
  }

  /** Cancel any in-flight auto move (user pan / wheel / pinch). */
  userInteracted(): void {
    this.controller.userInteracted();
  }

  cancel(): void {
    this.controller.cancel();
  }
}

export interface SemanticCameraOptions {
  /** Override the 260ms focus duration (tests, reduced-motion defaults). */
  focusDurationMs?: number;
  /** Inject a fake animator so tests can drive frames deterministically. */
  animator?: CameraAnimator;
}

export function createSemanticCamera(
  driver: ResearchCameraDriver,
  options: SemanticCameraOptions = {},
): SemanticCameraHandle {
  const controller = new ResearchCameraController(driver, {
    focusDurationMs: options.focusDurationMs ?? FOCUS_DURATION_MS,
    animator: options.animator,
  });
  return new SemanticCameraHandle(driver, controller);
}

export type { NodeBounds, Size, Viewport, Insets, ResearchCameraDriver };
