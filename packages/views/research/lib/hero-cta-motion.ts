/**
 * LRM-837 — research home hero CTA micro-interaction tokens.
 *
 * Timing maps to the shared motion contract in `packages/ui/styles/base.css`
 * (#820 / MotionContent): hover/press settle at moderate (200ms) with
 * `--motion-ease-out`. Press/active feedback must not depend on hover so
 * narrow / touch stays usable.
 */

/** AC: CTA hover/press ≤200ms → moderate tier. */
export const HERO_CTA_DURATION_MS = 200;

/**
 * Shared transition timing — Tailwind JIT-visible arbitrary values that
 * reference `--motion-duration-moderate` / `--motion-ease-out`.
 */
export const HERO_CTA_TRANSITION_CLASS =
  "transition-[background-color,box-shadow,transform,filter,border-color,color] duration-[var(--motion-duration-moderate)] ease-[var(--motion-ease-out)] motion-reduce:transition-none";

/**
 * Primary start CTA — hover (pointer), press (touch/mouse), keyboard focus.
 * Press uses scale/brightness so narrow viewports do not need hover.
 */
export const HERO_CTA_PRIMARY_CLASS = [
  HERO_CTA_TRANSITION_CLASS,
  "hover:bg-brand/90 hover:shadow-[0_6px_18px_-6px_color-mix(in_oklab,var(--brand)_55%,transparent)]",
  "active:scale-[0.98] active:brightness-[0.94] active:shadow-none",
  "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-brand focus-visible:ring-offset-2 focus-visible:ring-offset-background",
].join(" ");

/** Secondary params opener beside start — same timing + focus ring. */
export const HERO_CTA_SECONDARY_CLASS = [
  HERO_CTA_TRANSITION_CLASS,
  "hover:border-brand/40 hover:bg-brand/6",
  "active:scale-[0.98] active:bg-muted",
  "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-brand/40 focus-visible:ring-offset-2 focus-visible:ring-offset-background",
].join(" ");

/** Composer card chrome — focus-within ring settles on the same moderate token. */
export const HERO_COMPOSER_CARD_CLASS = [
  HERO_CTA_TRANSITION_CLASS,
  "focus-within:border-brand focus-within:shadow-[0_0_0_3px_color-mix(in_oklab,var(--brand)_22%,transparent)]",
].join(" ");
