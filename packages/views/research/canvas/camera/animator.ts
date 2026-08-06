/**
 * FE-05 — cancellable camera animation.
 *
 * Owns a single requestAnimationFrame tween. Starting a new tween cancels any
 * running one, so rapid consecutive focus requests never stack or drift: each
 * new request re-baselines from the *current* viewport and interpolates to the
 * new target using its own easing. The RAF loop is the only writer, so a
 * cancelled frame is simply never applied.
 *
 * Renderer-agnostic: `raf`/`cancelRaf`/`now` are injectable for node-env tests
 * and browser playback.
 */

export interface AnimValue {
  x: number;
  y: number;
  zoom: number;
}

export type Easing = (t: number) => number;

/** Ease-out cubic — fast start, gentle settle (used above the motion floor). */
export const easeOutCubic: Easing = (t) => 1 - Math.pow(1 - t, 3);

export const DEFAULT_DURATION_MS = 260;

export interface CameraAnimatorOptions {
  raf?: (cb: (time: number) => void) => number;
  cancelRaf?: (id: number) => void;
  now?: () => number;
}

const DEFAULT_RAF: (cb: (time: number) => void) => number =
  typeof requestAnimationFrame === "function"
    ? requestAnimationFrame
    : (() => {
        return (cb) => {
          const id = setTimeout(() => {
            cb(performance.now ? performance.now() : Date.now());
          }, 16) as unknown as number;
          return id;
        };
      })();

const DEFAULT_CANCEL_RAF: (id: number) => void =
  typeof cancelAnimationFrame === "function"
    ? cancelAnimationFrame
    : ((id: number) => clearTimeout(id as unknown as ReturnType<typeof setTimeout>));

export class CameraAnimator {
  private options: CameraAnimatorOptions;
  private rafId: number | null = null;
  private startValue: AnimValue | null = null;
  private endValue: AnimValue | null = null;
  private startedAt = 0;
  private durationMs = DEFAULT_DURATION_MS;
  private easing: Easing = easeOutCubic;
  private onUpdateValue: ((value: AnimValue) => void) | null = null;
  private onEndValue: (() => void) | null = null;
  private runningValue = false;

  constructor(options: CameraAnimatorOptions = {}) {
    this.options = options;
  }

  get running(): boolean {
    return this.runningValue;
  }

  /**
   * Begin animating `from → to` over `durationMs`. Interrupts and discards any
   * currently running tween. When reduced motion is requested, callers should
   * pass `durationMs: 0` to snap instantly instead.
   */
  start(
    from: AnimValue,
    to: AnimValue,
    onUpdate: (value: AnimValue) => void,
    opts: { durationMs?: number; easing?: Easing; onEnd?: () => void } = {},
  ): void {
    this.stop();
    this.startValue = from;
    this.endValue = to;
    this.durationMs = Math.max(0, opts.durationMs ?? DEFAULT_DURATION_MS);
    this.easing = opts.easing ?? easeOutCubic;
    this.onUpdateValue = onUpdate;
    this.onEndValue = opts.onEnd ?? null;
    this.runningValue = true;

    if (this.durationMs === 0) {
      onUpdate(to);
      this.finish();
      return;
    }

    this.startedAt = this.now();
    const tick = (): void => {
      if (!this.runningValue || !this.startValue || !this.endValue) return;
      const elapsed = this.now() - this.startedAt;
      const raw = Math.min(1, elapsed / this.durationMs);
      const t = this.easing(raw);
      const value: AnimValue = {
        x: this.startValue.x + (this.endValue.x - this.startValue.x) * t,
        y: this.startValue.y + (this.endValue.y - this.startValue.y) * t,
        zoom: this.startValue.zoom + (this.endValue.zoom - this.startValue.zoom) * t,
      };
      this.onUpdateValue?.(value);
      if (raw >= 1) {
        this.finish();
        return;
      }
      this.rafId = this.raf(tick);
    };
    this.rafId = this.raf(tick);
  }

  /** Cancel the running tween, if any. No onEnd callback fires. */
  stop(): void {
    if (this.rafId !== null && this.cancelRaf) {
      this.cancelRaf(this.rafId);
    }
    this.rafId = null;
    this.startValue = null;
    this.endValue = null;
    this.onUpdateValue = null;
    this.onEndValue = null;
    this.runningValue = false;
  }

  private finish(): void {
    const onEnd = this.onEndValue;
    this.rafId = null;
    this.startValue = null;
    this.endValue = null;
    this.onUpdateValue = null;
    this.onEndValue = null;
    this.runningValue = false;
    onEnd?.();
  }

  private raf(cb: (time: number) => void): number {
    return this.options.raf ? this.options.raf(cb) : DEFAULT_RAF(cb);
  }

  private cancelRaf(id: number): void {
    if (this.options.cancelRaf) this.options.cancelRaf(id);
    else DEFAULT_CANCEL_RAF(id);
  }

  private now(): number {
    if (this.options.now) return this.options.now();
    return typeof performance !== "undefined" && performance.now
      ? performance.now()
      : Date.now();
  }
}
