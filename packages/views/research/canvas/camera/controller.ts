/**
 * FE-05 — research canvas camera controller.
 *
 * Draws the FE-04 ViewModel's render geometry (node bounds resolved from the
 * unified view position plus a measured/assumed node size) toward the safe
 * centre of the viewport, with production-grade interruption semantics:
 *
 *   - Each `focus()` is a new authority. It cancels any in-flight animation and
 *     re-baselines from the *current* viewport, so rapid consecutive clicks do
 *     not stack tweens or drift.
 *   - `userInteracted()` cancels any auto-move immediately — the controller
 *     never wrestles the canvas away from a user who is dragging/pannning.
 *   - `prefersReducedMotion` snaps to the target instead of animating.
 *   - `announce` carries the polite live-region announcement for a11y.
 *
 * Renderer-agnostic: the driver supplies viewport read/apply and the a11y
 * channel. No React or DOM imports — testable in the node environment.
 */

import { CameraAnimator, type AnimValue } from "./animator";
import {
  viewportCenterOnBounds,
  type Insets,
  type NodeBounds,
  type Size,
  type Viewport,
} from "./geometry";

export interface ResearchCameraDriver {
  viewportSize: () => Size;
  getViewport: () => Viewport;
  /** Imperatively set the viewport (React Flow: `setViewport(vp, { duration: 0 })`). */
  applyViewport: (viewport: Viewport) => void;
  /** Screen-space overlay insets that define the safe centre region. */
  insets: () => Insets;
  /**
   * Read the reduced-motion preference lazily so OS changes apply to the next
   * focus. When true the controller snaps to target instead of animating.
   */
  reducedMotion: () => boolean;
  /** Polite live-region announcement for keyboard/screen-reader focus feedback. */
  announce?: (text: string) => void;
}

export interface CameraControllerOptions {
  /**
   * Injectable animator so tests can drive frames with a fake RAF clock. When
   * omitted the controller builds its own browser-backed animator.
   */
  animator?: CameraAnimator;
  /** Animation duration for focus moves (default 260ms; 0 snaps). */
  focusDurationMs?: number;
}

export interface CameraFocusOptions {
  /** E.163-friendly label used for the a11y announcement. */
  label?: string;
  /** Node bounds (position + measured size). */
  bounds: NodeBounds;
}

const EPSILON = 0.5;

const sameViewport = (a: Viewport, b: Viewport): boolean =>
  Math.abs(a.x - b.x) < EPSILON &&
  Math.abs(a.y - b.y) < EPSILON &&
  Math.abs(a.zoom - b.zoom) < EPSILON;

export class ResearchCameraController {
  private driver: ResearchCameraDriver;
  private animator: CameraAnimator;
  private focusDurationMs: number;
  /** Monotonic authority token — a newer request always wins. */
  private seq = 0;
  private pendingBounds: NodeBounds | null = null;
  private lastAnnouncedLabel: string | null = null;

  constructor(
    driver: ResearchCameraDriver,
    options: CameraControllerOptions = {},
  ) {
    this.driver = driver;
    this.animator = options.animator ?? new CameraAnimator();
    this.focusDurationMs = options.focusDurationMs ?? 260;
  }

  /**
   * Move the camera so the focused node sits in the safe centre region. Any
   * running auto-animation is cancelled and a fresh one starts from the current
   * viewport (no stacking, no drift). Always announces for a11y.
   */
  focus(options: CameraFocusOptions): void {
    const token = ++this.seq;
    this.pendingBounds = options.bounds;

    this.announce(options.label);

    const viewport = this.driver.getViewport();
    const target = viewportCenterOnBounds(
      options.bounds,
      viewport.zoom,
      this.driver.viewportSize(),
      this.driver.insets(),
    );

    if (this.driver.reducedMotion() || sameViewport(viewport, target)) {
      this.animator.stop();
      if (!sameViewport(viewport, target)) {
        this.driver.applyViewport(target);
      }
      this.clearPending();
      return;
    }

    this.animator.start(
      animFromViewport(viewport),
      animFromViewport(target),
      (value) => {
        if (token !== this.seq) return; // superseded — never apply stale frames
        this.driver.applyViewport({
          x: value.x,
          y: value.y,
          zoom: value.zoom,
        });
      },
      {
        durationMs: this.focusDurationMs,
        onEnd: () => {
          if (token !== this.seq) return;
          this.clearPending();
        },
      },
    );
  }

  /**
   * Called on real user interaction (e.g. React Flow `onMove` / pointerdown
   * during a pan). Cancels any auto-move so the user always stays in control.
   */
  userInteracted(): void {
    this.seq += 1;
    this.animator.stop();
    this.clearPending();
  }

  /** Cancel any in-flight/queued auto-move. */
  cancel(): void {
    this.seq += 1;
    this.animator.stop();
    this.clearPending();
  }

  /** True while a focus animation is running or queued. */
  get hasPendingFocus(): boolean {
    return this.pendingBounds !== null || this.animator.running;
  }

  private clearPending(): void {
    this.pendingBounds = null;
  }

  private announce(label?: string): void {
    if (!label || !this.driver.announce) return;
    const text = label;
    if (text === this.lastAnnouncedLabel) {
      // Retrigger identical announcements for repeated focus on the same node.
      this.driver.announce("");
    }
    this.lastAnnouncedLabel = text;
    this.driver.announce(text);
  }
}

function animFromViewport(viewport: Viewport): AnimValue {
  return { x: viewport.x, y: viewport.y, zoom: viewport.zoom };
}
