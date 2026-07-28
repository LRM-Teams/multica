import { type ReactNode } from "react";
import { cn } from "@multica/ui/lib/utils";

/** Motion contract tiers — map to the `--motion-duration-*` tokens in base.css (#820). */
export type MotionTier = "fast" | "moderate" | "slow";

// Static per-tier class strings so Tailwind's JIT sees every arbitrary value.
// motion-safe → fade the entering content in; motion-reduce → no animation
// (instant), honouring prefers-reduced-motion.
const FADE_BY_TIER: Record<MotionTier, string> = {
  fast: "motion-safe:animate-[content-fade-in_var(--motion-duration-fast)_var(--motion-ease-out)] motion-reduce:animate-none",
  moderate:
    "motion-safe:animate-[content-fade-in_var(--motion-duration-moderate)_var(--motion-ease-out)] motion-reduce:animate-none",
  slow: "motion-safe:animate-[content-fade-in_var(--motion-duration-slow)_var(--motion-ease-out)] motion-reduce:animate-none",
};

/**
 * MotionContent — the shared motion contract (#820) for view / tab / panel /
 * menu content swaps. Enforces the contract clauses so callers can't get them
 * subtly wrong:
 *
 *   1. Semantics / focus / actionable state switch INSTANTLY. The new content
 *      mounts synchronously on `motionKey` change, so it is in the DOM + a11y
 *      tree immediately; motion never gates interaction on a frame.
 *   2. Only opacity animates the new content in — no transform, no layout shift,
 *      no reflow (opacity-only by contract; callers own layout via `className`).
 *   3. Exit is instant: the previous content unmounts at once, always faster
 *      than the enter fade.
 *   4. Rapid retarget renders only the final intent — the keyed remount
 *      reconciles to the latest `motionKey`, so there are no stacked fades or
 *      flicker-back.
 *   5. prefers-reduced-motion drops the animation entirely (instant swap).
 *
 * Pass the value identifying "which content is showing" as `motionKey`; render
 * the swappable content as children.
 */
export function MotionContent({
  motionKey,
  tier = "moderate",
  className,
  children,
}: {
  motionKey: string | number;
  tier?: MotionTier;
  className?: string;
  children: ReactNode;
}) {
  return (
    <div key={motionKey} className={cn(FADE_BY_TIER[tier], className)}>
      {children}
    </div>
  );
}
