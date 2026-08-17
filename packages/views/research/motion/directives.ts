/**
 * LRM-1477 — Motion directive builder (verb → scoped CSS class + inline style).
 *
 * Spec: §2.2 (display verb syntax: transform + opacity + static marker),
 * §6.1 (directives.ts — aligns with the trajectory animator directive shape),
 * §3 (pixel/timing tokens), §4 (reduced-motion / low-perf presentation).
 *
 * This module is side-effect-free. It returns data a component can apply to a
 * per-entity shell: a transition class (scoped via a queue id so multiple live
 * animations do not collide), per-verb displacement/opacity inline style, and
 * the persistent static marker class.
 */

import type { DisplayVerb, StaticMarker } from "./semantic-mapping";
import {
  MOTION_CONFLICT_GAP_PX,
  MOTION_ESCALATE_RISE_PX,
  MOTION_APPEAR_RISE_PX,
  MOTION_STALE_OPACITY,
  MOTION_EASING,
} from "./tokens";

/** Root class attached to the motion container for scoping. */
export const SEMANTIC_MOTION_ROOT = "research-semantic-motion";

/** Per-entity verb class emitted onto the entity shell. */
export function verbClass(verb: DisplayVerb, txnId: string): string {
  return `research-motion-${verb} txn-${txnId}`;
}

/** Persistent static marker class (Rule ② — retained after settle). */
export function markerClass(marker: StaticMarker): string | null {
  if (marker === "none") return null;
  return `research-motion-marker research-motion-marker-${marker}`;
}

export interface MotionDirective {
  /** Class string to apply to the entity shell. */
  className: string;
  /** Scoped inline style (transform/opacity/animation-delay, if any). */
  style: Record<string, string>;
  /** Persistent marker class, kept after the animation settles. */
  markerClass: string | null;
  /** data-verb marker for testability / debug overlay (Rule ② DOM). */
  dataVerb: DisplayVerb;
  /** Whether glow/blur is disabled (low-performance). */
  glowDisabled: boolean;
}

export type MotionDirectiveOptions = {
  reducedMotion: boolean;
  lowPerformance: boolean;
  animationDelayMs: number;
  durationMs: number;
  /** Displacement scale 0..1; low-perf halves it. */
  displacementScale?: number;
  /**
   * Semantic static marker (Rule ②). When supplied it carries through
   * reduced-motion (which collapses the display verb to `reappear`) so the
   * conflict / escalate / stale / revise marker persists at rest. Falls back
   * to a verb-derived marker when omitted.
   */
  marker?: StaticMarker;
};

/**
 * Build a single per-entity directive. Displacement comes only from the motion
 * tokens — never re-reads or writes layout values (interface to the trajectory
 * engine deals with lane displacement separately).
 */
export function buildMotionDirective(
  verb: DisplayVerb,
  txnId: string,
  opts: MotionDirectiveOptions,
): MotionDirective {
  const displacementScale =
    opts.displacementScale ?? (opts.lowPerformance ? 0.5 : 1);
  const reduced = opts.reducedMotion;
  const markerCls = opts.marker ? markerClass(opts.marker) : markerClassFor(verb);
  // Low-performance: no glow/blur — only opacity + transform (spec §4.2).
  const liveStyle: Record<string, string> = {
    animationDuration: `${opts.durationMs}ms`,
    animationTimingFunction: MOTION_EASING,
  };
  if (opts.animationDelayMs > 0) {
    liveStyle.animationDelay = `${opts.animationDelayMs}ms`;
  }

  // Reduced-motion collapses all displacement to 0 and uses a uniform fade
  // (Rule ④: static markers preserved separately).
  if (reduced) {
    liveStyle.animationName = "research-motion-fade-in";
    return {
      className: verbClass(verb, txnId),
      style: liveStyle,
      markerClass: markerCls,
      dataVerb: verb,
      glowDisabled: opts.lowPerformance,
    };
  }

  switch (verb) {
    case "appear":
      liveStyle.animationName = "research-motion-appear";
      liveStyle["--motion-rise-px"] = `${MOTION_APPEAR_RISE_PX * displacementScale}px`;
      break;
    case "merge":
      liveStyle.animationName = "research-motion-merge";
      liveStyle.opacity = "0.4";
      liveStyle["--motion-fusion-blur"] = opts.lowPerformance ? "0px" : "5px";
      break;
    case "conflict":
      liveStyle.animationName = "research-motion-conflict";
      liveStyle["--motion-gap-px"] = `${MOTION_CONFLICT_GAP_PX * displacementScale}px`;
      break;
    case "escalate":
      liveStyle.animationName = "research-motion-escalate";
      liveStyle["--motion-rise-px"] = `${MOTION_ESCALATE_RISE_PX * displacementScale}px`;
      break;
    case "stale":
      liveStyle.animationName = "research-motion-stale";
      liveStyle.opacity = `${MOTION_STALE_OPACITY}`;
      break;
    case "revise":
      liveStyle.animationName = "research-motion-revise";
      break;
    case "retire":
      // ⑤ 废弃: grey-out to stale level + kept clickable (never disappears).
      liveStyle.animationName = "research-motion-retire";
      liveStyle.opacity = `${MOTION_STALE_OPACITY}`;
      break;
    case "restart":
      // ⑥ 重启: short relation emphasis then weakens; old node kept.
      liveStyle.animationName = "research-motion-restart";
      break;
    case "regoal":
      // ⑦ 目标修改: impact-ordered highlight (single brightness pulse).
      liveStyle.animationName = "research-motion-regoal";
      break;
    case "reappear":
      liveStyle.animationName = "research-motion-fade-in";
      break;
    case "camera":
      liveStyle.animationName = "research-motion-camera";
      break;
  }

  return {
    className: verbClass(verb, txnId),
    style: liveStyle,
    markerClass: markerCls,
    dataVerb: verb,
    glowDisabled: opts.lowPerformance,
  };
}

function markerClassFor(verb: DisplayVerb): string | null {
  switch (verb) {
    case "conflict":
      return markerClass("conflict-border");
    case "escalate":
      return markerClass("escalate-emphasis");
    case "stale":
      return markerClass("stale-grey");
    case "revise":
      return markerClass("revise-pulse");
    case "appear":
      return markerClass("accepted-check"); // result_accepted → accepted marker
    case "retire":
      return markerClass("tombstone");
    case "restart":
      return markerClass("restart-relation");
    case "regoal":
      return markerClass("regoal-highlight");
    default:
      return null;
  }
}

/**
 * Scoped CSS for the semantic motion module. Classes are emitted under
 * `.research-semantic-motion` so they never leak into unrelated nodes, and a
 * plain builder is provided so tests can assert the keyframes exist without DOM.
 */
export function semanticMotionCss(): string {
  return `
.${SEMANTIC_MOTION_ROOT} [class*="research-motion-"] {
  will-change: transform, opacity;
}
@keyframes research-motion-appear {
  from { opacity: 0; transform: translateY(var(--motion-rise-px, 8px)) scale(0.96); }
  to   { opacity: 1; transform: translateY(0) scale(1); }
}
@keyframes research-motion-anchored-appear {
  0% {
    opacity: 0.08;
    transform: translate(var(--motion-anchor-x, 0), var(--motion-anchor-y, 0)) scale(0.48);
    filter: blur(var(--motion-anchor-blur, 4px)) brightness(1.25);
  }
  72% { opacity: 1; filter: blur(0) brightness(1.08); }
  100% { opacity: 1; transform: translate(0, 0) scale(1); filter: none; }
}
@keyframes research-motion-merge {
  0% {
    opacity: 0.32;
    transform: scale(0.68);
    filter: blur(var(--motion-fusion-blur, 5px)) brightness(1.38);
  }
  68% { opacity: 1; transform: scale(1.035); filter: blur(0) brightness(1.12); }
  100% { opacity: 1; transform: scale(1); filter: none; }
}
@keyframes research-motion-expansion-reveal {
  0% {
    opacity: 0.12;
    transform: translate(var(--expansion-origin-x, 0), var(--expansion-origin-y, 0)) scale(0.52);
    filter: blur(var(--expansion-blur, 5px)) brightness(1.35);
  }
  70% { opacity: 1; filter: blur(0) brightness(1.12); }
  100% { opacity: 1; transform: translate(0, 0) scale(1); filter: none; }
}
@keyframes research-motion-expansion-collapse {
  0% { transform: scale(0.94); filter: brightness(1.3); }
  58% { transform: scale(1.035); filter: brightness(1.16); }
  100% { transform: scale(1); filter: none; }
}
@keyframes research-motion-conflict {
  from { transform: translateX(calc(var(--motion-gap-px, 12px) * -1)); }
  to   { transform: translateX(0); }
}
@keyframes research-motion-escalate {
  from { transform: translateY(calc(var(--motion-rise-px, 8px) * -1)); filter: brightness(1.15); }
  to   { transform: translateY(0); filter: none; }
}
@keyframes research-motion-stale {
  from { opacity: 1; }
  to   { opacity: var(--motion-stale-opacity, 0.55); }
}
@keyframes research-motion-revise {
  0%   { box-shadow: 0 0 0 0 color-mix(in oklch, var(--brand) 0%, transparent); }
  50%  { box-shadow: 0 0 0 4px color-mix(in oklch, var(--brand) 50%, transparent); }
  100% { box-shadow: 0 0 0 0 color-mix(in oklch, var(--brand) 0%, transparent); }
}
@keyframes research-motion-retire {
  from { opacity: 1; }
  to   { opacity: var(--motion-stale-opacity, 0.55); }
}
@keyframes research-motion-restart {
  0%   { box-shadow: 0 0 0 0 color-mix(in oklch, var(--brand) 0%, transparent); }
  50%  { box-shadow: 0 0 0 4px color-mix(in oklch, var(--brand) 55%, transparent); }
  100% { box-shadow: 0 0 0 0 color-mix(in oklch, var(--brand) 0%, transparent); }
}
@keyframes research-motion-regoal {
  0%   { filter: brightness(1); }
  50%  { filter: brightness(1.18); }
  100% { filter: brightness(1); }
}
@keyframes research-motion-fade-in {
  from { opacity: 0; }
  to   { opacity: 1; }
}
@keyframes research-motion-camera {
  from { opacity: 0.96; }
  to   { opacity: 1; }
}
@media (prefers-reduced-motion: reduce) {
  .${SEMANTIC_MOTION_ROOT} [class*="research-motion-"] {
    animation: none !important;
  }
}
`;
}

/** Base inline style for a per-entity shell under semantic motion. */
export function semanticMotionBaseStyle(
  txnId: string,
): Record<string, string> {
  return { ["--research-motion-txn" as string]: txnId };
}
