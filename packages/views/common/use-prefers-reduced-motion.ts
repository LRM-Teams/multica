"use client";

import { useSyncExternalStore } from "react";

const REDUCED_MOTION_QUERY = "(prefers-reduced-motion: reduce)";

function subscribe(onStoreChange: () => void): () => void {
  if (typeof window === "undefined" || typeof window.matchMedia !== "function") {
    return () => {};
  }
  const query = window.matchMedia(REDUCED_MOTION_QUERY);
  query.addEventListener?.("change", onStoreChange);
  return () => query.removeEventListener?.("change", onStoreChange);
}

function getSnapshot(): boolean {
  if (typeof window === "undefined" || typeof window.matchMedia !== "function") {
    return false;
  }
  return window.matchMedia(REDUCED_MOTION_QUERY).matches;
}

// Server render can't know the user's motion preference; report "motion is
// fine" and let the first client commit correct it (same contract as the
// private copy inside channels/components/message-parts-renderer.tsx, which a
// follow-up should fold into this hook — left alone here to keep this change's
// file surface mutually exclusive with LRM-1337).
function getServerSnapshot(): boolean {
  return false;
}

/**
 * `prefers-reduced-motion: reduce`, as reactive React state.
 *
 * Prefer Tailwind's `motion-reduce:` / `motion-safe:` variants when the
 * animation itself is a Tailwind utility. This hook exists for the case where
 * it can NOT work: animations declared as plain classes in
 * `packages/ui/styles/base.css` (`.animate-chat-impulse`,
 * `.animate-nav-progress-sweep`, …). `base.css` is imported *after*
 * `@import "tailwindcss"` in every app entry, so those single-class rules land
 * later in the stylesheet and win the source-order tie against
 * `motion-reduce:animate-none` — verified in real Chromium under emulated
 * `reduce` (LRM-1362), where the utility had no effect at all. Gating whether
 * the class is emitted at all is deterministic and specificity-independent.
 */
export function usePrefersReducedMotion(): boolean {
  return useSyncExternalStore(subscribe, getSnapshot, getServerSnapshot);
}
